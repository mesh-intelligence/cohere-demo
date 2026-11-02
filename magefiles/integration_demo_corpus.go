// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// The rel10.5 tracer (rel10.5-uc001). It proves the release end to end: the
// committed store holds the corpus the manifest declares, both collections seed
// from it without calling the embedding provider, a cited chunk resolves to its
// parent and its versions, and the requester's clearance governs that path
// exactly as it governs retrieval.
//
// It runs in phases, each naming what it proves and each skipping on its own
// dependencies rather than on the target's. The store phase needs neither
// provider nor cluster, so it runs on a clone with nothing installed; the seed
// and serving phases need Chroma; the turn phase needs the mesh and a
// credential. A phase that cannot run says which dependency is missing rather
// than passing quietly.
//
// This replaces mage integration:corpusBuild, retired with the synthetic corpus
// it traced (srd017 R9.1). The mechanism is the same and the corpus is not.

const (
	// demoCorpusRetrievalCollection and demoCorpusDocumentsCollection are
	// written and dropped by this target alone, so a run leaves the demo's own
	// collections untouched.
	demoCorpusRetrievalCollection = "demo-corpus-probe"
	demoCorpusDocumentsCollection = "demo-corpus-documents-probe"

	// demoCorpusCompositionTopN is the effective cutoff at composition -- the
	// smaller of the per-source retrieval breadth (n_results 20, GH-336) and
	// the rerank composition budget (top_n 8) -- and therefore the cutoff the
	// corpus is sized against: a relevant document ranked below it never
	// reaches composition, which is the condition srd014 R1.3 states.
	demoCorpusCompositionTopN = 8
)

// DemoCorpus proves the rel10.5 release over the versioned store -- the
// tracked publishable tiers united with the private checkout's keep-private
// tiers (srd017 R9.7).
func (Integration) DemoCorpus() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}

	fmt.Println("integration:demoCorpus: store phase")
	if err := assertDemoCorpusStore(root); err != nil {
		return err
	}

	if reason := corpusSeedDockerSkipReason(); reason != "" {
		fmt.Printf("SKIP integration:demoCorpus seed and serving phases: %s\n", reason)
		return nil
	}
	stopChroma, err := startChromaForDemoCorpus()
	if err != nil {
		return err
	}
	defer stopChroma()

	fmt.Println("integration:demoCorpus: seed phase")
	if err := assertDemoCorpusSeedsWithoutTheProvider(root); err != nil {
		return err
	}

	fmt.Println("integration:demoCorpus: serving phase")
	if err := assertDemoCorpusServingResolvesAndWithholds(root); err != nil {
		return err
	}

	fmt.Println("integration:demoCorpus: passed")
	return nil
}

// corpusSeedDockerSkipReason names the missing dependency when the phases that
// need a database cannot run, so a run says why rather than passing quietly.
func corpusSeedDockerSkipReason() string {
	if _, err := exec.LookPath("docker"); err != nil {
		return "docker not found on PATH"
	}
	return ""
}

// startChromaForDemoCorpus brings a Chroma up for the seed and serving phases,
// reusing one already answering rather than killing it (GH-708).
func startChromaForDemoCorpus() (func(), error) {
	dataDir, err := os.MkdirTemp("", "demo-corpus-chroma-")
	if err != nil {
		return nil, fmt.Errorf("create the chroma data directory: %w", err)
	}
	containerID, err := ensureChromaServer(dataDir)
	if err != nil {
		_ = os.RemoveAll(dataDir)
		return nil, err
	}
	return func() {
		stopChromaContainer(containerID)
		_ = os.RemoveAll(dataDir)
	}, nil
}

// assertDemoCorpusStore holds the committed store to what the corpus declares.
// It reads files and nothing else, so a clone runs it with nothing installed.
func assertDemoCorpusStore(root string) error {
	manifest, err := readCorpusManifest(root)
	if err != nil {
		return err
	}
	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		return err
	}
	if len(documents) == 0 {
		return fmt.Errorf("the committed store holds no documents; the demo seeds from it")
	}
	// The target proves the rel10.5 release over the whole corpus; a store
	// missing the keep-private tiers would quietly prove something smaller.
	if err := requireFullCorpusStore(root, documents); err != nil {
		return err
	}

	tiers := map[string]int{}
	levels := map[string]int{}
	profiles := map[string]int{}
	embedded := map[string]int{}
	lineage := 0
	for _, document := range documents {
		tiers[document.Envelope.SourceType]++
		levels[document.Envelope.Clearance]++
		byID := map[string]corpusChunkRecord{}
		for _, chunk := range document.Chunks {
			byID[chunk.ChunkID] = chunk
		}
		for _, chunk := range document.Chunks {
			profiles[chunk.ChunkProfile]++
			if _, held := chunk.vector(demoCorpusStoreIdentity(manifest, document)); held {
				embedded[chunk.ChunkProfile]++
			}
			if err := assertChunkLineage(chunk, byID); err != nil {
				return err
			}
			lineage++
		}
	}

	// Every declared tier contributes, so a tasking's relevant documents sit
	// across sources rather than within one (srd017 R1.1).
	for _, tier := range corpusSourceTypes() {
		if tiers[tier] == 0 {
			return fmt.Errorf("the store holds no %s document; the corpus declares"+
				" five tiers and a tasking is supposed to draw across them", tier)
		}
	}

	// More than one clearance level, or the two-level assertion has nothing to
	// withhold (srd017 R9.5).
	if len(levels) < 2 {
		return fmt.Errorf("the store holds one clearance level %v; the two-level"+
			" assertion has nothing to withhold", sortedCountKeys(levels))
	}

	// Every declared profile cut something, and no profile carries more vectors
	// than the ceiling allows (srd017 R4.1, R6.2).
	for _, profile := range defaultChunkProfiles() {
		if profiles[profile.Name] == 0 {
			return fmt.Errorf("the store holds no chunk at profile %s", profile.Name)
		}
		if embedded[profile.Name] > corpusEmbedCeiling {
			return fmt.Errorf("profile %s carries %d vectors, above the declared"+
				" ceiling of %d", profile.Name, embedded[profile.Name], corpusEmbedCeiling)
		}
	}
	if embedded[chunkProfileMedium] == 0 {
		return fmt.Errorf("the retrieval profile carries no vectors, so a seeded" +
			" collection would answer nothing")
	}

	// The documents the manifest names relevant are in the store, or the
	// tasking the demo verifies has nothing to retrieve (srd015 R1.5).
	relevant := manifest.RetrievalConditions.RelevantToPrimaryTaskng
	if len(relevant) <= demoCorpusCompositionTopN {
		return fmt.Errorf("the manifest names %d relevant documents against a"+
			" composition cutoff of %d; the cutoff has to bite for a rerank to"+
			" have anything to recover (srd014 R1.3)",
			len(relevant), demoCorpusCompositionTopN)
	}
	for _, id := range relevant {
		if _, held := documents[id]; !held {
			return fmt.Errorf("the manifest names %s relevant to the primary tasking"+
				" and the store does not hold it", id)
		}
	}

	fmt.Printf("integration:demoCorpus: %d documents, %d chunks, lineage complete;"+
		" tiers %v; levels %v; embedded %v\n",
		len(documents), lineage, sortedCountKeys(tiers), sortedCountKeys(levels),
		sortedCountKeys(embedded))
	return nil
}

// demoCorpusStoreIdentity names the identity the store was written under.
func demoCorpusStoreIdentity(manifest corpusManifest, document corpusStoredDocument) string {
	for _, chunk := range document.Chunks {
		for _, vector := range chunk.Vectors {
			return vector.Identity
		}
	}
	return ""
}

// assertChunkLineage holds one chunk's parent block to the consistency R5.6
// requires, which is what makes a citation resolve rather than dangle.
func assertChunkLineage(chunk corpusChunkRecord, byID map[string]corpusChunkRecord) error {
	parent := chunk.Parent
	if parent == nil {
		return fmt.Errorf("chunk %s carries no parent block, so a citation naming"+
			" it resolves to nothing (srd017 R5.1)", chunk.ChunkID)
	}
	if parent.Position < 1 || parent.Position > parent.SiblingCount {
		return fmt.Errorf("chunk %s is at position %d of %d siblings",
			chunk.ChunkID, parent.Position, parent.SiblingCount)
	}
	if len(parent.CharSpan) != 2 || parent.CharSpan[1] < parent.CharSpan[0] {
		return fmt.Errorf("chunk %s carries the span %v, which does not run forwards",
			chunk.ChunkID, parent.CharSpan)
	}
	for label, neighbour := range map[string]string{
		"prev": parent.PrevChunkID, "next": parent.NextChunkID,
		"coarser parent": parent.CoarserParent,
	} {
		if neighbour == "" {
			continue
		}
		if _, held := byID[neighbour]; !held {
			return fmt.Errorf("chunk %s names a %s %s the document does not hold",
				chunk.ChunkID, label, neighbour)
		}
	}
	return nil
}

// assertDemoCorpusSeedsWithoutTheProvider seeds both collections and counts the
// embedding requests at a stand-in the declared client is pointed at.
//
// Counting is the whole assertion. A demo that answers proves nothing about
// where its vectors came from, because a seed that re-embedded the corpus
// produces the same collection at a cost that scales with how often the system
// is shown (srd015 R5.2, srd017 R8.8).
func assertDemoCorpusSeedsWithoutTheProvider(root string) error {
	counter, stop, err := startCorpusEmbedCounter()
	if err != nil {
		return err
	}
	defer stop()

	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		return err
	}
	identity := ""
	for _, document := range documents {
		if identity = demoCorpusStoreIdentity(corpusManifest{}, document); identity != "" {
			break
		}
	}
	if identity == "" {
		return fmt.Errorf("the store carries no embedding identity, so a seed has" +
			" no vectors to select")
	}

	seeded, err := seedCollectionFromCorpusStore(root, demoCorpusRetrievalCollection, identity)
	if err != nil {
		return err
	}
	retrieval := seeded.Chunks
	served, err := seedDocumentsCollection(root, demoCorpusDocumentsCollection)
	if err != nil {
		return err
	}
	if requests := counter.count(); requests != 0 {
		return fmt.Errorf("seeding issued %d embedding requests; the store exists so"+
			" that starting the demo costs no provider spend (srd015 R5.2)", requests)
	}

	heldRetrieval, err := chromaNamedCollectionCount(demoCorpusRetrievalCollection)
	if err != nil {
		return err
	}
	if heldRetrieval != retrieval {
		return fmt.Errorf("the retrieval collection holds %d records and the seed"+
			" wrote %d", heldRetrieval, retrieval)
	}
	heldDocuments, err := chromaNamedCollectionCount(demoCorpusDocumentsCollection)
	if err != nil {
		return err
	}
	if heldDocuments != served.Documents+served.Chunks {
		return fmt.Errorf("the document collection holds %d records and the seed"+
			" wrote %d envelopes and %d chunks",
			heldDocuments, served.Documents, served.Chunks)
	}

	fmt.Printf("integration:demoCorpus: seeded %d retrieval records and %d document"+
		" records (%d envelopes, %d chunks) for %d embedding requests\n",
		retrieval, heldDocuments, served.Documents, served.Chunks, counter.count())
	return nil
}

// assertDemoCorpusServingResolvesAndWithholds walks a citation to its source
// and then asks for a document above the requester's level three ways.
//
// The walk is what the release is for. The withholding is what keeps it from
// undoing rel11.0: reading by identifier reaches exactly the documents the
// retrieval filter exists to withhold (srd017 R8.5, R8.7).
func assertDemoCorpusServingResolvesAndWithholds(root string) error {
	manifest, err := readCorpusManifest(root)
	if err != nil {
		return err
	}
	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		return err
	}

	highest := manifest.highestClearance()
	cleared, err := newCorpusDocumentReader(
		demoCorpusDocumentsCollection, highest, manifest.ClearanceLevels)
	if err != nil {
		return err
	}

	// A chunk of a document at the highest level, which is what the walk
	// resolves and what a lower clearance must not reach.
	target, chunkID := "", ""
	for _, documentID := range sortedDocumentIDs(documents) {
		document := documents[documentID]
		if document.Envelope.Clearance != highest || len(document.Chunks) == 0 {
			continue
		}
		target, chunkID = documentID, document.Chunks[0].ChunkID
		break
	}
	if target == "" {
		return fmt.Errorf("the store holds no document at the highest declared level"+
			" %q, so the withholding assertion would prove nothing", highest)
	}

	chunk, found, err := cleared.GetChunk(chunkID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("a fully cleared requester could not read chunk %s", chunkID)
	}
	if chunk.Parent == nil || chunk.Parent.DocumentID != target {
		return fmt.Errorf("chunk %s resolves to %+v rather than to document %s",
			chunkID, chunk.Parent, target)
	}
	document, found, err := cleared.GetDocument(chunk.Parent.DocumentID)
	if err != nil {
		return err
	}
	if !found || strings.TrimSpace(document.Text) == "" {
		return fmt.Errorf("the parent document %s did not resolve to a readable"+
			" document", chunk.Parent.DocumentID)
	}
	versions, err := cleared.GetVersions(chunk.Parent.DocumentID)
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		return fmt.Errorf("document %s resolved to no chunk versions, so an"+
			" interface cannot move between chunk sizes", chunk.Parent.DocumentID)
	}

	// The same three reads at a lower clearance return nothing.
	lower := manifest.ClearanceLevels[0]
	uncleared, err := newCorpusDocumentReader(
		demoCorpusDocumentsCollection, lower, manifest.ClearanceLevels)
	if err != nil {
		return err
	}
	if _, found, err := uncleared.GetDocument(target); err != nil {
		return err
	} else if found {
		return fmt.Errorf("a %s requester read document %s, which is %s;"+
			" the serving path is a way around the retrieval filter (srd017 R8.7)",
			lower, target, highest)
	}
	if _, found, err := uncleared.GetChunk(chunkID); err != nil {
		return err
	} else if found {
		return fmt.Errorf("a %s requester read chunk %s of a %s document",
			lower, chunkID, highest)
	}
	lowerVersions, err := uncleared.GetVersions(target)
	if err != nil {
		return err
	}
	if len(lowerVersions) != 0 {
		return fmt.Errorf("a %s requester read %d versions of a %s document",
			lower, len(lowerVersions), highest)
	}
	listed, err := uncleared.ListDocuments(corpusDocumentFilter{})
	if err != nil {
		return err
	}
	for _, record := range listed {
		if record.Clearance != lower {
			return fmt.Errorf("a %s requester's facet query listed %s, which is %s",
				lower, record.DocumentID, record.Clearance)
		}
	}

	fmt.Printf("integration:demoCorpus: chunk %s resolved to document %s and %d"+
		" versions at %s; a %s requester reached none of them and listed %d documents\n",
		chunkID, target, len(versions), highest, lower, len(listed))
	return nil
}

// sortedDocumentIDs orders a document store, so a run reads it the same way
// twice.
func sortedDocumentIDs(documents map[string]corpusStoredDocument) []string {
	ids := make([]string, 0, len(documents))
	for id := range documents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// sortedCountKeys renders a count map for a report line.
func sortedCountKeys(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rendered := make([]string, 0, len(keys))
	for _, key := range keys {
		rendered = append(rendered, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return rendered
}

// corpusEmbedCounter records requests arriving at a stand-in for the embedding
// provider. Pointing the declared client at it makes "no request was issued" a
// measurement rather than an inference.
type corpusEmbedCounter struct {
	mutex    sync.Mutex
	requests int
}

func (c *corpusEmbedCounter) count() int {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.requests
}

func (c *corpusEmbedCounter) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	c.mutex.Lock()
	c.requests++
	c.mutex.Unlock()
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"embeddings":{"float":[[0.0]]}}`))
}

// startCorpusEmbedCounter stands a counter up on a local port and points the
// declared Cohere base URL at it, restoring the previous value on stop.
//
// The process environment is the channel because the claim being measured is a
// negative: that the seed issues no embedding request at all. A base URL
// threaded through as a parameter would prove nothing, since code that never
// calls the embedder never reads the parameter either. Redirecting the client
// the seed could have called is what makes its silence observable.
func startCorpusEmbedCounter() (*corpusEmbedCounter, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("listen for the embedding counter: %w", err)
	}
	counter := &corpusEmbedCounter{}
	server := &http.Server{Handler: counter, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()

	//nolint:forbidigo // Redirecting the declared client is how the seed's zero-request claim is measured rather than asserted.
	previous, had := os.LookupEnv("COHERE_BASE_URL")
	//nolint:forbidigo // Same redirect; restored by the returned stop.
	if err := os.Setenv("COHERE_BASE_URL", "http://"+listener.Addr().String()); err != nil {
		_ = server.Close()
		return nil, nil, fmt.Errorf("point the embedding client at the counter: %w", err)
	}
	stop := func() {
		if had {
			//nolint:forbidigo // Restores the operator's value after the measurement.
			_ = os.Setenv("COHERE_BASE_URL", previous)
		} else {
			//nolint:forbidigo // Same restore.
			_ = os.Unsetenv("COHERE_BASE_URL")
		}
		_ = server.Close()
	}
	return counter, stop, nil
}

// chromaResolveCollectionID resolves a collection by name.
func chromaResolveCollectionID(collection string) (string, error) {
	return chromaResolveCollection(collection)
}

// chromaNamedCollectionCount reads one collection's item count, so the seed is
// checked against what Chroma holds rather than against what the seeder said.
func chromaNamedCollectionCount(collection string) (int, error) {
	id, err := chromaResolveCollectionID(collection)
	if err != nil {
		return 0, err
	}
	data, status, err := requestHTTP(http.MethodGet, chromaCollectionsURL+"/"+id+"/count", "")
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("read %s count: status %d: %s", collection, status, data)
	}
	var count int
	if err := json.Unmarshal(data, &count); err != nil {
		return 0, fmt.Errorf("decode %s count: %w", collection, err)
	}
	return count, nil
}
