// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/magefile/mage/mg"
	"gopkg.in/yaml.v3"
)

// Corpus owns the offline build behind the demo corpus. It runs ahead of and
// independently of any running mesh, cluster, or Chroma instance, so building
// the store and seeding from it are separable in time and in failure
// (srd015 R4.1).
type Corpus mg.Namespace

// corpusEmbeddingIdentity is one embedding family as the build sees it: the
// name the rag-server reports for a collection seeded from these vectors
// (srd015 R3.2), and the call that produces one.
type corpusEmbeddingIdentity struct {
	identity  string
	provider  string
	inputType string
	embed     func(text string) ([]float64, error)
}

// corpusBuildReport is what one build did, and it separates the two things a
// rebuild has to be judged on: how many vectors it computed and how many
// records it wrote. Store equality alone does not establish the skip, because a
// build that recomputed every vector and wrote back an identical record would
// leave the store unchanged while still paying the provider (rel10.4-uc001 S3).
type corpusBuildReport struct {
	Identity  string
	Provider  string
	Dimension int
	Chunks    int
	Embedded  int
	Reused    int
	// Provider calls the build did not make because another chunk in the same
	// build already carried the identical passage (srd017 R6.5).
	Deduplicated int
	Written      int
	Removed      []string
}

// corpusDataRootEnv points the build at the source datasets. They are hundreds
// of megabytes and live outside the repository, so the path is supplied rather
// than derived from the checkout; unset means a data directory beside it.
const corpusDataRootEnv = "CORPUS_DATA_ROOT"

// corpusDataRoot resolves where the source datasets live: the environment
// variable, else a local data directory that actually holds a payload, else the
// private data checkout's data directory (GH-910). The local directory is
// checked for a payload rather than for existence because the repository tracks
// the per-tier READMEs under data/, so the directory exists in every clone
// while the datasets live in the private checkout.
func corpusDataRoot(root string) string {
	//nolint:forbidigo // the dataset root is operator-supplied, like the credential.
	if value := strings.TrimSpace(os.Getenv(corpusDataRootEnv)); value != "" {
		return value
	}
	local := filepath.Join(root, "data")
	if _, err := os.Stat(filepath.Join(local, "isw", "isw_dailies.jsonl")); err == nil {
		return local
	}
	if private := corpusPrivateRoot(root); private != "" {
		if _, err := os.Stat(filepath.Join(private, "data")); err == nil {
			return filepath.Join(private, "data")
		}
	}
	return local
}

// Build reads the source datasets through the normalized envelope, chunks the
// window at every declared profile, and writes the store the demo seeds from,
// embedding the declared selection once through the declared client.
//
// It computes a vector only for a chunk and identity pair the store does not
// already hold, so a build over unchanged inputs at an unchanged identity
// issues zero embedding requests (srd015 R3.3, R4.3), and the selection's
// ceiling is checked before the first request rather than after the last
// (srd017 R6.3).
func (Corpus) Build() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	identity, err := resolveCorpusBuildIdentity(root)
	if err != nil {
		return err
	}
	corpusRoot, err := corpusCaseRoot(root)
	if err != nil {
		return err
	}
	dataRoot := corpusBuildDataRoot(corpusRoot)
	if _, err := os.Stat(dataRoot); err != nil {
		return fmt.Errorf("corpus:build: the source datasets are not at %s."+
			" They are held outside the repository; point %s at them"+
			" (see the README beside each tier under data/): %w",
			dataRoot, corpusDataRootEnv, err)
	}
	fmt.Printf("corpus:build: embedding through %s as identity %q\n",
		identity.provider, identity.identity)
	report, slice, err := buildUkraineCorpusStore(corpusRoot, dataRoot, identity,
		corpusSliceFor(corpusRoot), defaultChunkProfiles(), corpusEmbedSelectionFor(corpusRoot))
	if err != nil {
		return err
	}
	if corpusRootIsCase(corpusRoot) {
		fmt.Printf("corpus:build: converted %d documents in %s\n", slice.total(), corpusRoot)
	} else {
		fmt.Printf("corpus:build: converted %d documents (humint %d, assessments %d,"+
			" news %d of %d, strikes %d, losses %d)\n",
			slice.total(), slice.Humint, slice.OSINTAssessment, slice.OSINTNews,
			slice.TelegramCandidate, slice.SIGINTEvent, slice.SIGINTSeries)
	}
	fmt.Printf("corpus:build: %d chunks, %d embedded, %d already held, %d served"+
		" from the passage cache, %d records written, dimension %d\n",
		report.Chunks, report.Embedded, report.Reused, report.Deduplicated,
		report.Written, report.Dimension)
	for _, removed := range report.Removed {
		fmt.Printf("corpus:build: removed %s; the corpus no longer produces it\n", removed)
	}
	return nil
}

// buildCorpusStore is the build, with the provider supplied by the caller. The
// integration target drives it at two identities and with a failing embed to
// prove behavior a live single-provider run cannot reach.
func buildCorpusStore(
	root string, identity corpusEmbeddingIdentity, params corpusChunkParameters,
) (corpusBuildReport, error) {
	report := corpusBuildReport{Identity: identity.identity, Provider: identity.provider}
	if strings.TrimSpace(identity.identity) == "" {
		return report, fmt.Errorf("corpus build: no embedding identity named;" +
			" a vector stored under an empty identity is a vector no seed can select (srd015 R3.1)")
	}
	manifest, err := readCorpusManifest(root)
	if err != nil {
		return report, err
	}
	documents, err := readCorpusDocuments(root)
	if err != nil {
		return report, err
	}
	records := make([]corpusChunkRecord, 0, len(documents))
	for _, document := range documents {
		records = append(records, chunkCorpusDocument(document, manifest, params)...)
	}
	return buildCorpusStoreFromChunks(root, identity, params, manifest, records)
}

// buildCorpusStoreFromChunks embeds and stores a set of chunk records. It is
// the half of the build that does not care where the chunks came from, which is
// what lets one build serve two chunkers: the single-profile document chunker
// rel10.4 reads corpus/documents with, and the multi-profile envelope chunker
// rel10.5 cuts the source datasets with (srd017 R4).
//
// Everything the release established about embedding survives the split. A
// chunk whose text is unchanged carries its vectors forward, a build at an
// already-held identity issues no request, and a dimension disagreement fails
// the build rather than seeding a collection that cannot hold both.
func buildCorpusStoreFromChunks(
	root string,
	identity corpusEmbeddingIdentity,
	params corpusChunkParameters,
	manifest corpusManifest,
	records []corpusChunkRecord,
) (corpusBuildReport, error) {
	report := corpusBuildReport{Identity: identity.identity, Provider: identity.provider}
	stored, err := loadCorpusStore(root)
	if err != nil {
		return report, err
	}

	wanted := make(map[string]bool)
	for _, record := range records {
		wanted[record.ChunkID] = true
		report.Chunks++
		completed, embedded, err := resolveCorpusChunkVector(record, stored, identity)
		if err != nil {
			return report, err
		}
		if embedded {
			report.Embedded++
		} else {
			report.Reused++
		}
		values, _ := completed.vector(identity.identity)
		if report.Dimension == 0 {
			report.Dimension = len(values)
		} else if len(values) != report.Dimension {
			return report, fmt.Errorf(
				"chunk %s embedded to %d dimensions under identity %q but earlier chunks"+
					" embedded to %d; a collection pins its dimension at creation and cannot"+
					" hold both (srd015 R3.5)",
				completed.ChunkID, len(values), identity.identity, report.Dimension)
		}
		written, err := writeCorpusChunkRecordIfChanged(root, completed)
		if err != nil {
			return report, err
		}
		if written {
			report.Written++
		}
	}

	removed, err := pruneCorpusStore(root, stored, wanted)
	if err != nil {
		return report, err
	}
	report.Removed = removed

	if err := writeCorpusStoreIndexFromDisk(root, manifest, params, identity); err != nil {
		return report, err
	}
	return report, nil
}

// resolveCorpusChunkVector returns the record to write and whether the provider
// was called for it.
//
// A stored record whose text still hashes the same carries every family's
// vectors forward, so a build at a second identity adds to the record rather
// than displacing what another family wrote (srd015 R3.4). A text that changed
// carries none of them forward: the vectors on that record describe prose the
// corpus no longer holds, and keeping them would be the drift the store exists
// to remove. Metadata is copied fresh every build either way, because it does
// not enter the embedding.
func resolveCorpusChunkVector(
	record corpusChunkRecord,
	stored map[string]corpusChunkRecord,
	identity corpusEmbeddingIdentity,
) (corpusChunkRecord, bool, error) {
	if previous, held := stored[record.ChunkID]; held && previous.TextSHA256 == record.TextSHA256 {
		record.Vectors = previous.Vectors
	}
	if values, held := record.vector(identity.identity); held && len(values) > 0 {
		return record, false, nil
	}
	values, err := identity.embed(record.Text)
	if err != nil {
		// The failing chunk is named, and nothing is written for it. A record
		// carrying an absent or truncated vector is what the next build would
		// read as already held and never retry (srd015 R4.4).
		return record, false, fmt.Errorf("corpus build: embed chunk %s from %s at identity %q: %w",
			record.ChunkID, record.SourcePath, identity.identity, err)
	}
	if len(values) == 0 {
		return record, false, fmt.Errorf(
			"corpus build: chunk %s from %s embedded to an empty vector at identity %q",
			record.ChunkID, record.SourcePath, identity.identity)
	}
	return record.withVector(identity.identity, values), true, nil
}

// writeCorpusChunkRecordIfChanged writes only when the encoded record differs
// from what is on disk, so a build that computed nothing new leaves the store
// byte-identical and its mtimes untouched.
func writeCorpusChunkRecordIfChanged(root string, record corpusChunkRecord) (bool, error) {
	encoded, err := encodeCorpusYAML(record)
	if err != nil {
		return false, fmt.Errorf("encode chunk %s: %w", record.ChunkID, err)
	}
	path := filepath.Join(root, filepath.FromSlash(corpusStoreChunksDir),
		corpusRecordFilename(record.ChunkID))
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, encoded) {
		return false, nil
	}
	return true, writeCorpusChunkRecord(root, record)
}

// pruneCorpusStore removes records the corpus no longer produces, so a
// shortened or deleted document does not leave chunks a seed would still write
// into the collection.
func pruneCorpusStore(
	root string, stored map[string]corpusChunkRecord, wanted map[string]bool,
) ([]string, error) {
	var removed []string
	for _, chunkID := range sortedCorpusChunkIDs(stored) {
		if wanted[chunkID] {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(corpusStoreChunksDir),
			corpusRecordFilename(chunkID))
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("remove orphaned chunk record %s: %w", chunkID, err)
		}
		removed = append(removed, chunkID)
	}
	return removed, nil
}

// writeCorpusStoreIndexFromDisk recomputes the index from every record the
// store now holds rather than from the identity just built, so the file names
// all the families present and each one's dimension (srd015 R4.5, R6.2).
func writeCorpusStoreIndexFromDisk(
	root string,
	manifest corpusManifest,
	params corpusChunkParameters,
	built corpusEmbeddingIdentity,
) error {
	records, err := loadCorpusStore(root)
	if err != nil {
		return err
	}
	// The index this build replaces is where the other families' provenance
	// lives. A first build has none, and an unreadable one is not worth failing
	// a build over, so both yield an empty carry-forward set.
	previous, err := readCorpusStoreIndex(root)
	if err != nil {
		previous = corpusStoreIndex{}
	}
	index := corpusStoreIndex{
		Corpus:          manifest.ID,
		CorpusVersion:   manifest.Version,
		Synthetic:       manifest.Synthetic,
		SyntheticNotice: strings.TrimSpace(manifest.SyntheticNotice),
		Chunking:        params,
		Chunks:          len(records),
		Identities:      corpusStoreIdentities(records, built, previous.Identities),
	}
	return writeCorpusStoreIndex(root, index)
}

// corpusStoreIdentities summarizes which families the records carry. The
// provider and input type are known first-hand only for the identity this build
// wrote; for every other family they are copied from the index this build
// replaces, because a store that forgets how a vector was produced is a store
// whose identities are names without provenance, and the integration target
// reads the provider to decide which client can embed a query at it.
func corpusStoreIdentities(
	records map[string]corpusChunkRecord,
	built corpusEmbeddingIdentity,
	previous []corpusStoreIdentity,
) []corpusStoreIdentity {
	carried := make(map[string]corpusStoreIdentity, len(previous))
	for _, entry := range previous {
		carried[entry.Identity] = entry
	}
	dimensions := make(map[string]int)
	counts := make(map[string]int)
	for _, chunkID := range sortedCorpusChunkIDs(records) {
		for _, vector := range records[chunkID].Vectors {
			counts[vector.Identity]++
			dimensions[vector.Identity] = vector.Dimension
		}
	}
	identities := make([]corpusStoreIdentity, 0, len(counts))
	for identity, count := range counts {
		entry := corpusStoreIdentity{
			Identity:  identity,
			Dimension: dimensions[identity],
			Chunks:    count,
		}
		switch held, ok := carried[identity]; {
		case identity == built.identity:
			entry.Provider = built.provider
			entry.InputType = built.inputType
		case ok:
			entry.Provider = held.Provider
			entry.InputType = held.InputType
		}
		identities = append(identities, entry)
	}
	sort.SliceStable(identities, func(i, j int) bool {
		return identities[i].Identity < identities[j].Identity
	})
	return identities
}

// resolveCorpusBuildIdentity names the family this build writes, offline. It
// reads the chart the demo installs rather than a deployed release, because a
// build that needed a running cluster to decide what to embed would defeat the
// separation R4.1 draws between building the store and seeding from it.
func resolveCorpusBuildIdentity(root string) (corpusEmbeddingIdentity, error) {
	if cohereCredential() != "" {
		return corpusCohereIdentity(root)
	}
	identity, err := corpusOllamaIdentity(root)
	if err != nil {
		return identity, err
	}
	fmt.Printf("corpus:build: neither %s nor %s supplies a Cohere credential, so this"+
		" build writes the %s identity; supply one to build the Cohere family\n",
		cohereCredentialEnv, cohereCredentialFile, identity.identity)
	return identity, nil
}

// corpusCohereIdentity embeds through the same declared client and embed
// operation the mesh uses -- one base URL, one credential accessor, one model
// name, one allowlisted egress -- rather than through a client of its own
// (srd015 R4.2). Two configurations that agree today are two configurations
// that can diverge.
func corpusCohereIdentity(root string) (corpusEmbeddingIdentity, error) {
	model, err := corpusCohereEmbedModel(root)
	if err != nil {
		return corpusEmbeddingIdentity{}, err
	}
	return corpusEmbeddingIdentity{
		identity:  model,
		provider:  "cohere",
		inputType: cohereEmbedDocumentInputType,
		embed: func(text string) ([]float64, error) {
			return cohereSeedDocument(model, text)
		},
	}, nil
}

// corpusOllamaIdentity embeds through the host Ollama at the model the demo
// chart declares for its RAG units. Ollama draws no distinction between a
// document and a query embedding, so the corpus side calls the same operation
// the query side does.
func corpusOllamaIdentity(root string) (corpusEmbeddingIdentity, error) {
	model, err := corpusOllamaEmbedModel(root)
	if err != nil {
		return corpusEmbeddingIdentity{}, err
	}
	return corpusEmbeddingIdentity{
		identity: model,
		provider: "ollama",
		embed: func(text string) ([]float64, error) {
			return ollamaEmbedQuery(model, text)
		},
	}, nil
}

// corpusCohereEmbedModel reads the chart's own Cohere embedding model, which is
// what demo:up sets each RAG unit's embeddingModel to and therefore what the
// rag-server reports for a Cohere-seeded collection.
func corpusCohereEmbedModel(root string) (string, error) {
	path := filepath.Join(applicationChartDir(root), "values.yaml")
	var values struct {
		Cohere struct {
			EmbedModel string `yaml:"embedModel"`
		} `yaml:"cohere"`
	}
	if err := readCorpusBuildYAML(path, &values); err != nil {
		return "", err
	}
	model := strings.TrimSpace(values.Cohere.EmbedModel)
	if model == "" {
		return "", fmt.Errorf("%s declares no cohere.embedModel", path)
	}
	return model, nil
}

// corpusOllamaEmbedModel reads the embedding model the demo values declare for
// the RAG units, which becomes RAG_EMBEDDING_MODEL on the rag-server and is the
// identity it reports in its query response metadata (srd015 R3.2).
func corpusOllamaEmbedModel(root string) (string, error) {
	path := demoValuesPath(applicationChartDir(root))
	var values struct {
		RAGUnits []struct {
			Name           string `yaml:"name"`
			EmbeddingModel string `yaml:"embeddingModel"`
		} `yaml:"ragUnits"`
	}
	if err := readCorpusBuildYAML(path, &values); err != nil {
		return "", err
	}
	for _, unit := range values.RAGUnits {
		if model := strings.TrimSpace(unit.EmbeddingModel); model != "" {
			return model, nil
		}
	}
	return "", fmt.Errorf("%s declares no ragUnits[].embeddingModel", path)
}

func readCorpusBuildYAML(path string, into interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, into); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
