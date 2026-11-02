// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"strings"
	"testing"
)

// The seed's fail-closed leg and its identity selection are proven here without
// a Chroma, because both decisions are made before anything is written. The
// case exists because a fallback succeeds: the demo answers, the operator sees
// nothing wrong, and the release has silently reverted to the path it replaced
// (rel10.4-uc001 S8, E5).

func storeRecordsForTest(identities ...string) map[string]corpusChunkRecord {
	record := corpusChunkRecord{
		ChunkID:       "HUM-0001-0001",
		Corpus:        "test-corpus",
		CorpusVersion: "3.2",
		Synthetic:     true,
		SourcePath:    "documents/hum-0001.md",
		Position:      1,
		Text:          "A fictional observation.",
		Metadata: corpusChunkMetadata{
			SourceID:                        "HUM-0001",
			SourceType:                      "humint",
			Clearance:                       "confidential",
			AdmiraltySourceReliability:      "B",
			AdmiraltyInformationCredibility: 2,
			Keywords:                        []string{"harbour", "loading"},
		},
	}
	for _, identity := range identities {
		record = record.withVector(identity, []float64{0.1, 0.2, 0.3})
	}
	return map[string]corpusChunkRecord{record.ChunkID: record}
}

func TestSeedFailsClosedNamingTheWantedAndTheHeldIdentities(t *testing.T) {
	records := storeRecordsForTest("qwen3-embedding:8b")
	err := assertCorpusStoreHoldsIdentity(records, "embed-v4.0")
	if err == nil {
		t.Fatal("a store holding no vectors for the deployed identity was accepted;" +
			" the seed would have written a collection every source is excluded from")
	}
	message := err.Error()
	if !strings.Contains(message, "embed-v4.0") {
		t.Errorf("the error does not name the identity the seed wanted: %s", message)
	}
	if !strings.Contains(message, "qwen3-embedding:8b") {
		t.Errorf("the error does not name the identities the store holds: %s", message)
	}
}

func TestSeedAcceptsAStoreHoldingTheDeployedIdentityAlongsideOthers(t *testing.T) {
	records := storeRecordsForTest("qwen3-embedding:8b", "embed-v4.0")
	for _, identity := range []string{"qwen3-embedding:8b", "embed-v4.0"} {
		if err := assertCorpusStoreHoldsIdentity(records, identity); err != nil {
			t.Errorf("a store holding both families rejected %q: %v", identity, err)
		}
	}
}

func TestSeedReportsAnEmptyStoreRatherThanAnEmptyIdentityList(t *testing.T) {
	err := assertCorpusStoreHoldsIdentity(map[string]corpusChunkRecord{}, "embed-v4.0")
	if err == nil {
		t.Fatal("an empty store was accepted")
	}
	if !strings.Contains(err.Error(), "none") {
		t.Errorf("the error does not say the store holds no identities at all: %v", err)
	}
}

func TestSeedMetadataCarriesTheDeclaredFacetsOntoTheCollectionRecord(t *testing.T) {
	records := storeRecordsForTest("embed-v4.0")
	metadata := corpusChunkChromaMetadata(records["HUM-0001-0001"], "2022-12-16")

	// The clearance tag rel11.0 filters on and the facets rel11.5 selects by
	// have to arrive from the build rather than be derived at ingest.
	if metadata["clearance"] != "confidential" {
		t.Errorf("collection metadata carries clearance %v, want confidential", metadata["clearance"])
	}
	if metadata["source_type"] != "humint" || metadata["source_id"] != "HUM-0001" {
		t.Errorf("collection metadata carries source %v/%v, want HUM-0001/humint",
			metadata["source_id"], metadata["source_type"])
	}
	if metadata["admiralty_source_reliability"] != "B" ||
		metadata["admiralty_information_credibility"] != 2 {
		t.Errorf("collection metadata carries Admiralty %v/%v, want B/2",
			metadata["admiralty_source_reliability"],
			metadata["admiralty_information_credibility"])
	}
	if metadata["keywords"] != "harbour,loading" {
		t.Errorf("collection metadata carries keywords %v, want the joined declared list",
			metadata["keywords"])
	}
	if metadata["synthetic"] != true {
		t.Error("collection metadata does not mark the record synthetic")
	}

	// The envelope's issued date arrives as an orderable integer key (GH-730)
	// so a date-windowed facet can range over it; an unparseable date omits
	// the key rather than seeding a value ranges would misorder.
	if metadata["date_key"] != 20221216 {
		t.Errorf("collection metadata carries date_key %v, want 20221216", metadata["date_key"])
	}
	if undated := corpusChunkChromaMetadata(records["HUM-0001-0001"], ""); undated["date_key"] != nil {
		t.Errorf("an undated envelope must omit date_key, got %v", undated["date_key"])
	}

	// Chroma metadata values are scalars. A list here is rejected at the add,
	// which is why the keyword facet is joined rather than written as authored.
	for key, value := range metadata {
		switch value.(type) {
		case string, int, bool, float64:
		default:
			t.Errorf("metadata %q is %T, which Chroma cannot store", key, value)
		}
	}
}
