// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cohereEmbedText embeds one text at the Cohere embeddings operation and
// returns the flat vector, matching ollamaEmbedQuery's signature so a probe or
// seeder takes either as its embedding function.
//
// The demo corpus is seeded through the corpus-ingest agent (app:seed). This
// direct embed serves the query-side probes, which embed a search_query rather
// than ingesting, and the integration corpora, which the chatbot and cohere
// targets seed inline as a deterministic test seam — their disjoint corpus is a
// set of in-memory documents, not a corpus directory the agent ingests.
//
// inputType is the caller's, not a constant: the corpus embeds as
// search_document while a query embeds as search_query, and a seeder that hard
// coded either would silently produce the mismatch srd009 R2.2 exists to
// prevent.
func cohereEmbedText(model, inputType, text string) ([]float64, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"model":           model,
		"input_type":      inputType,
		"embedding_types": []string{"float"},
		"texts":           []string{text},
	})
	if err != nil {
		return nil, fmt.Errorf("encode embed request: %w", err)
	}
	baseURL := envOrDefault("COHERE_BASE_URL", cohereEmbedDefaultBase)
	request, err := http.NewRequest(http.MethodPost,
		strings.TrimSuffix(baseURL, "/")+cohereEmbedPath, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build embed request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+cohereCredential())

	response, err := (&http.Client{Timeout: cohereEmbedProbeTimeout}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", cohereEmbedPath, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read embed response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		// The body names a rejected model or input type. It carries no credential.
		return nil, fmt.Errorf("POST %s returned %d for model %q input_type %q: %s",
			cohereEmbedPath, response.StatusCode, model, inputType, strings.TrimSpace(string(body)))
	}

	var decoded struct {
		Embeddings struct {
			Float [][]float64 `json:"float"`
		} `json:"embeddings"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	rows := decoded.Embeddings.Float
	// One text, one row. The turn's capture word makes the same assumption, and
	// a seeder that quietly took rows[0] out of several would embed the corpus
	// with a vector belonging to a different text.
	if len(rows) != 1 {
		return nil, fmt.Errorf("one text yielded %d embedding rows, want 1", len(rows))
	}
	if len(rows[0]) == 0 {
		return nil, fmt.Errorf("embeddings.float[0] is an empty vector")
	}
	return rows[0], nil
}

// cohereSeedDocument embeds one corpus document as a document rather than as a
// query. The asymmetry with the turn's search_query is the point: the provider's
// retrieval models distinguish the two, and seeding a corpus as queries costs
// recall without failing anything.
func cohereSeedDocument(model, text string) ([]float64, error) {
	return cohereEmbedText(model, cohereEmbedDocumentInputType, text)
}

// chromaDropCollection removes a collection so the next write recreates it at
// the incoming vector's dimension.
//
// Chroma fixes a collection's dimension when it is created and rejects any
// later write of another width — "Collection expecting embedding with dimension
// of 4096, got 1536" is what a corpus seeded by Ollama says to a Cohere vector.
// Repointing the embedding family is therefore a full reseed, not an append,
// which is why rel10.3-uc001 carries the migration as first-class work rather
// than treating the repoint as a declaration change.
//
// A collection that is not there is the state this wants, so a 404 is success.
func chromaDropCollection(collection string) error {
	url := chromaCollectionsURL + "/" + collection
	data, status, err := requestHTTP(http.MethodDelete, url, "")
	if err != nil {
		return fmt.Errorf("delete %s collection: %w", collection, err)
	}
	if status/100 == 2 || status == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("delete %s collection: status %d: %s", collection, status, data)
}

// chromaCollectionsURL is the Chroma collections endpoint the seed writes
// through: the port-forward address for app:seed and the local rig, or the
// unit's own in-cluster Service when corpus:seedCluster retargets it.
var chromaCollectionsURL = chromaDefaultBaseURL + chromaCollectionsPath

const (
	chromaDefaultBaseURL  = "http://127.0.0.1:8000"
	chromaCollectionsPath = "/api/v2/tenants/default_tenant/databases/default_database/collections"
)

// setChromaBaseURL points every collection write at one Chroma instance. The
// seed paths are single-threaded, so retargeting per unit is safe.
func setChromaBaseURL(base string) {
	chromaCollectionsURL = strings.TrimSuffix(base, "/") + chromaCollectionsPath
}

// chromaCollectionAdd resolves a Chroma collection and adds the documents with
// the vectors the caller embedded. It is the write half seedChromaCorpus2
// already performed inline, lifted so both the disjoint corpus and the fixture
// corpus seed through one path.
//
// metadatas is per-document collection metadata, one entry per document, and
// each carries a clearance level. The store seed supplies it so the clearance
// tag rel11.0 filters on and the facets rel11.5 selects by arrive from the build
// rather than being derived at ingest (srd015 R5.3). srd010 R1.2 binds every
// write path: an untagged record matches no clearance window and is unreachable
// at every level, which reads as an empty corpus rather than as the missing
// tag, so an untagged write is refused here.
func chromaCollectionAdd(
	collection string,
	ids, docs []string,
	embeddings [][]float64,
	metadatas []map[string]interface{},
) error {
	if len(metadatas) != len(ids) {
		return fmt.Errorf("add to %s: %d metadata entries for %d documents; every record needs a clearance level (srd010 R1.2)",
			collection, len(metadatas), len(ids))
	}
	for i, metadata := range metadatas {
		if level, _ := metadata["clearance"].(string); level == "" {
			return fmt.Errorf("add to %s: record %s carries no clearance level (srd010 R1.2)", collection, ids[i])
		}
	}
	base := chromaCollectionsURL
	data, status, err := requestHTTP(http.MethodPost, base,
		fmt.Sprintf(`{"name":%q,"get_or_create":true}`, collection))
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return fmt.Errorf("resolve %s collection: status %d: %s", collection, status, data)
	}
	var resolved struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &resolved); err != nil {
		return fmt.Errorf("decode collection id: %w", err)
	}
	// The add is batched. A corpus of a few thousand chunks at 1,536 dimensions
	// is tens of megabytes of JSON, and one request that size times out before
	// Chroma answers -- which reads as an unreachable database rather than as a
	// payload nobody chunked.
	for start := 0; start < len(ids); start += chromaAddBatchSize {
		end := start + chromaAddBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		body := map[string]interface{}{
			"ids": ids[start:end], "documents": docs[start:end],
			"embeddings": embeddings[start:end],
		}
		body["metadatas"] = metadatas[start:end]
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		addData, addStatus, err := requestHTTPWithClient(chromaAddClient, http.MethodPost,
			base+"/"+resolved.ID+"/add", string(payload))
		if err != nil {
			return fmt.Errorf("add records %d-%d to %s: %w", start, end, collection, err)
		}
		if addStatus/100 != 2 {
			return fmt.Errorf("add records %d-%d to %s: status %d: %s",
				start, end, collection, addStatus, addData)
		}
	}
	return nil
}

// chromaAddBatchSize is how many records one add carries. Small enough that a
// batch of 1,536-dimension vectors stays a few megabytes and answers inside the
// client timeout, large enough that a few thousand chunks is tens of requests
// rather than thousands.
const chromaAddBatchSize = 200

// chromaAddClient carries the batched adds. A batch is a few megabytes, which
// the probe client's two seconds cover on a local cluster but not through a
// port-forward to a cloud one, where the upload alone takes longer (GH-882).
var chromaAddClient = &http.Client{Timeout: 2 * time.Minute}

// seedCohereCorpusFixture embeds the shipped corpus fixture at Cohere and writes
// it to a collection a rag unit serves. The integration:cohere and
// integration:chatbot targets use it to seed inline.
//
// app:seed no longer uses it — the demo corpus ingests through the corpus-ingest
// agent now that upstream normalizes the provider row
// (pre-move upstream declarative-agents#1863, GH-135). The integration targets
// keep the inline seam because they seed a disjoint in-memory corpus alongside
// this fixture without standing up the agent's Cohere ingest, and
// integration:chroma proves the agent's own ingest separately.
func seedCohereCorpusFixture(profilesRoot, model, collection string) error {
	corpusDir := filepath.Join(profilesRoot, chromaCorpusFixture, "corpus")
	entries, err := os.ReadDir(corpusDir)
	if err != nil {
		return fmt.Errorf("read corpus fixture: %w", err)
	}
	var ids, docs []string
	var embeddings [][]float64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(corpusDir, entry.Name()))
		if err != nil {
			return fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		vector, err := cohereSeedDocument(model, string(raw))
		if err != nil {
			return fmt.Errorf("embed %s: %w", entry.Name(), err)
		}
		ids = append(ids, entry.Name())
		docs = append(docs, string(raw))
		embeddings = append(embeddings, vector)
	}
	if len(ids) == 0 {
		return fmt.Errorf("corpus fixture %s holds no documents", corpusDir)
	}
	return chromaCollectionAdd(collection, ids, docs, embeddings, fixtureClearanceMetadatas(len(ids)))
}

// seedCohereDisjointCorpus embeds the disjoint documents at Cohere and writes
// them to rag1's collection, so both served collections share one embedding
// space with the query vector (srd002 R3.3).
func seedCohereDisjointCorpus(model string) error {
	var ids, docs []string
	var embeddings [][]float64
	for _, d := range disjointCorpus2Docs {
		vector, err := cohereSeedDocument(model, d.text)
		if err != nil {
			return fmt.Errorf("embed %s: %w", d.id, err)
		}
		ids = append(ids, d.id)
		docs = append(docs, d.text)
		embeddings = append(embeddings, vector)
	}
	return chromaCollectionAdd(chromaCorpus2, ids, docs, embeddings, fixtureClearanceMetadatas(len(ids)))
}

// fixtureClearanceMetadatas tags the inline integration fixtures with the
// lowest declared level. They are synthetic documents with no classification
// of their own, and the turns that read them send no clearance header, so they
// run at the lowest level (srd010 R4.1); a higher tag would hide the fixture
// from the turn that is supposed to compose over it.
func fixtureClearanceMetadatas(n int) []map[string]interface{} {
	metadatas := make([]map[string]interface{}, n)
	for i := range metadatas {
		metadatas[i] = fixtureChunkMetadata(clearanceLevels[0])
	}
	return metadatas
}

// fixtureChunkMetadata is the metadata row a synthetic fixture chunk carries:
// every field the chatbot's document render reads, which the corpus build
// writes for real chunks (corpusChunkChromaMetadata). A row missing one fails
// the render template and the turn with it, so the fixtures carry the same
// contract. A synthetic document has no graded source, and the corpus grades an
// ungraded source F6, the Admiralty scale's own "cannot be judged" (srd015 R2.3).
func fixtureChunkMetadata(level string) map[string]interface{} {
	return map[string]interface{}{
		"clearance":                         level,
		"clearance_rank":                    clearanceRank(level),
		"admiralty_source_reliability":      "F",
		"admiralty_information_credibility": "6",
	}
}

// fixtureDocumentCollection is the collection a rag-server's by-identifier read
// resolves (agents/rag-server/rest.yaml RAG_DOCUMENT_COLLECTION). It is not the
// retrieval collection: records are addressed by id there, never by similarity
// (srd017 R8.1).
const fixtureDocumentCollection = "corpus_documents"

// seedFixtureDocumentCollection writes both inline fixture corpora to the
// document collection under the ids the retrieval collections use, the way
// app:seed fills it for the real corpus. A revision re-grounds its carried
// claims by reading their records back by id (GH-604), and against an empty
// document collection that read answers with nothing.
func seedFixtureDocumentCollection(profilesRoot string) error {
	corpusDir := filepath.Join(profilesRoot, chromaCorpusFixture, "corpus")
	entries, err := os.ReadDir(corpusDir)
	if err != nil {
		return fmt.Errorf("read corpus fixture: %w", err)
	}
	var ids, docs []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(corpusDir, entry.Name()))
		if err != nil {
			return fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		ids = append(ids, entry.Name())
		docs = append(docs, string(raw))
	}
	for _, d := range disjointCorpus2Docs {
		ids = append(ids, d.id)
		docs = append(docs, d.text)
	}
	embeddings := make([][]float64, len(ids))
	for i := range embeddings {
		embeddings[i] = documentPlaceholderVector()
	}
	return chromaCollectionAdd(fixtureDocumentCollection, ids, docs, embeddings, fixtureClearanceMetadatas(len(ids)))
}
