// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Seeding from the store, which is where this release stops paying the
// provider to show the system. The seed selects the vectors whose recorded
// identity matches the embedding model the deployed release declares and
// writes them with their stored metadata; it computes nothing, so it needs no
// reachable embedding provider and no credential (srd015 R5.1, R5.2).
//
// The seam is the seed target rather than a catalog word or a configured
// operation. srd015 leaves the choice to this release and R5.5 bounds it: the
// canonical corpus-ingest machine's listing, chunking, and threading behavior
// is untouched here, and `mage seed` and `mage integration:chroma` still drive
// it over the ingest fixture. A REST-client operation cannot read a local file,
// so rebinding the child's embedder library does not reach a stored vector
// without an upstream change.

// corpusSeedReport is what one collection received.
type corpusSeedReport struct {
	Collection string
	Identity   string
	Chunks     int
	Dimension  int
}

// seedCollectionFromCorpusStore writes every chunk record into one collection,
// taking each vector from the store under the deployed identity and each
// metadata value from what the build copied off the document's front matter
// (srd015 R5.1, R5.3).
//
// The collection is dropped first. Chroma pins a collection's dimension at
// creation, so a seed that finds a collection left at another family's width is
// rejected on the first add; a store seed is a full reseed by construction
// rather than an append (rel10.3-uc001).
func seedCollectionFromCorpusStore(root, collection, identity string) (corpusSeedReport, error) {
	report := corpusSeedReport{Collection: collection, Identity: identity}
	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		return report, err
	}
	if len(documents) == 0 {
		return report, fmt.Errorf(
			"the store at %s holds no documents; run mage corpus:build before seeding",
			corpusStoreDocumentsDir)
	}
	if note := corpusStoreMissingTiersNote(root, documents); note != "" {
		fmt.Println(note)
	}

	// The collection holds the declared selection alone (srd017 R8.3). The
	// store may carry more vectors than the selection names, because narrowing
	// an allocation keeps what a wider one already paid for (R6.4) -- so
	// seeding by vector-presence would quietly widen retrieval past the
	// declared allocation, and a rebalance would rebalance nothing.
	candidates := make([]corpusEmbedCandidate, 0, len(documents))
	for _, documentID := range sortedDocumentIDs(documents) {
		document := documents[documentID]
		candidates = append(candidates, corpusEmbedCandidate{
			Envelope: document.Envelope, Records: document.Chunks,
		})
	}
	plan, err := planCorpusEmbedding(candidates, corpusEmbedSelectionFor(root))
	if err != nil {
		return report, err
	}

	ids := make([]string, 0, plan.count())
	texts := make([]string, 0, plan.count())
	metadatas := make([]map[string]interface{}, 0, plan.count())
	embeddings := make([][]float64, 0, plan.count())
	for _, documentID := range sortedDocumentIDs(documents) {
		for _, record := range documents[documentID].Chunks {
			if !plan.Selected[record.ChunkID] {
				// A chunk outside the selection is not searched. It is still
				// served by identifier from the document collection, so a
				// citation into it resolves even though retrieval cannot reach
				// it (srd017 R6.4, R8.2).
				continue
			}
			vector, held := record.vector(identity)
			if !held || len(vector) == 0 {
				return report, fmt.Errorf(
					"chunk %s is in the declared embed selection and carries no vector"+
						" for identity %q; the store is behind the selection, so run"+
						" mage corpus:build before seeding", record.ChunkID, identity)
			}
			if report.Dimension == 0 {
				report.Dimension = len(vector)
			} else if len(vector) != report.Dimension {
				return report, fmt.Errorf(
					"chunk %s holds a %d-dimension vector for identity %q where earlier"+
						" chunks hold %d; a collection cannot hold both",
					record.ChunkID, len(vector), identity, report.Dimension)
			}
			ids = append(ids, record.ChunkID)
			texts = append(texts, record.Text)
			metadatas = append(metadatas, corpusChunkChromaMetadata(record, documents[documentID].Envelope.Issued))
			embeddings = append(embeddings, vector)
		}
	}
	if len(ids) == 0 {
		return report, fmt.Errorf(
			"no chunk in the store carries a vector for identity %q; the store was"+
				" built under another embedding family, so rebuild it rather than"+
				" seeding a collection every source would be excluded from"+
				" (srd015 R5.4)", identity)
	}

	if err := chromaDropCollection(collection); err != nil {
		return report, err
	}
	if err := chromaCollectionAdd(collection, ids, texts, embeddings, metadatas); err != nil {
		return report, err
	}
	report.Chunks = len(ids)
	return report, nil
}

// assertCorpusStoreHoldsIdentity fails the seed when the store holds no vectors
// for the deployed identity, naming the identity it wanted and the identities
// the store holds. It does not fall back to computing them: the fallback
// succeeds, which is the problem -- the demo answers, the operator sees nothing
// wrong, and the release has quietly reverted to the path it replaced, paying
// provider spend on a path specified not to (srd015 R5.4).
func assertCorpusStoreHoldsIdentity(records map[string]corpusChunkRecord, identity string) error {
	held := make(map[string]bool)
	for _, record := range records {
		for _, vector := range record.Vectors {
			held[vector.Identity] = true
		}
	}
	if held[identity] {
		return nil
	}
	names := make([]string, 0, len(held))
	for name := range held {
		names = append(names, name)
	}
	sort.Strings(names)
	available := strings.Join(names, ", ")
	if available == "" {
		available = "none"
	}
	return fmt.Errorf(
		"the chunk store holds no vectors for embedding identity %q; it holds: %s."+
			" The seed does not embed the corpus to recover, because a fallback would"+
			" restore both the provider spend and the identity drift the store exists to"+
			" remove, and it would do so silently. Run mage corpus:build with the"+
			" provider for %q configured, or deploy the release that declares an identity"+
			" the store holds (srd015 R5.4)",
		identity, available, identity)
}

// corpusChunkChromaMetadata flattens a chunk's stored metadata into the scalar
// values a Chroma record holds, so the clearance tag rel11.0 filters on and the
// facets rel11.5 selects by arrive from the build rather than being derived at
// ingest (srd015 R5.3).
//
// Keywords are joined rather than stored as a list because Chroma metadata
// values are scalars. A facet query therefore matches a substring of this
// field; the list as authored stays legible in the chunk record itself.
func corpusChunkChromaMetadata(record corpusChunkRecord, issued string) map[string]interface{} {
	metadata := map[string]interface{}{
		"chunk_id":       record.ChunkID,
		"corpus":         record.Corpus,
		"corpus_version": record.CorpusVersion,
		"synthetic":      record.Synthetic,
		"source_path":    record.SourcePath,
		"source_id":      record.Metadata.SourceID,
		"source_type":    record.Metadata.SourceType,
		"clearance":      record.Metadata.Clearance,
		// The clearance's position in the declared order (GH-378). The level
		// names do not sort -- "secret" precedes "unclassified"
		// lexicographically -- so a reader-release filter that admits
		// everything at or below a level needs an orderable companion, and
		// deriving it once at seed time keeps the ordering the one the
		// declared set states rather than one a comparison invents.
		"clearance_rank":                    clearanceRank(record.Metadata.Clearance),
		"admiralty_source_reliability":      record.Metadata.AdmiraltySourceReliability,
		"admiralty_information_credibility": record.Metadata.AdmiraltyInformationCredibility,
		"keywords":                          strings.Join(record.Metadata.Keywords, ","),
	}
	// The envelope's issued date as an orderable integer (GH-730): Chroma's
	// range operators compare numbers only, so 2022-12-16 seeds as 20221216.
	// A date-windowed query filters on this key; a record whose envelope
	// carries no parseable date omits it and is excluded from windowed
	// retrieval by construction, the same posture harvested rows get.
	if parsed, err := time.Parse(corpusEnvelopeDateLayout, strings.TrimSpace(issued)); err == nil {
		metadata["date_key"] = parsed.Year()*10000 + int(parsed.Month())*100 + parsed.Day()
	}
	return metadata
}
