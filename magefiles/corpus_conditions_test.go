// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"strings"
	"testing"
)

// The corpus conditions downstream releases exercise, checked over the shipped
// store without a provider or a cluster.
//
// Downstream releases do not need documents, they need conditions. rel11.0
// needs a document above the lower level of a clearance pair or its assertion
// withholds nothing. rel12.2 checks a brief's source-evaluation table against
// Admiralty rows, so every document needs one. srd014 R1.3 needs relevant
// documents that retrieval ranks below the cutoff, which is why the corpus is
// sized past the top-N a turn composes from.
//
// A corpus that answers a tasking without these leaves those releases passing
// over material that exercises none of them (rel10.4-uc001 S7, E9;
// srd017 R9.5).
//
// The check reads the committed store rather than a document directory. The
// store is what the demo seeds from and what a clone carries, so it is the
// artifact whose properties matter.
func TestCorpusConditionsHoldOverTheShippedStore(t *testing.T) {
	root := corpusConditionsRoot(t)
	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		t.Fatalf("read the shipped store: %v", err)
	}
	if len(documents) == 0 {
		t.Fatal("the shipped store holds no documents; the demo seeds from it")
	}
	manifest, err := readCorpusManifest(root)
	if err != nil {
		t.Fatalf("read the corpus manifest: %v", err)
	}

	tiers := map[string]int{}
	levels := map[string]int{}
	unrated := []string{}
	synthetic := 0
	for _, document := range documents {
		envelope := document.Envelope
		tiers[envelope.SourceType]++
		levels[envelope.Clearance]++
		if envelope.Synthetic {
			synthetic++
		}
		for _, chunk := range document.Chunks {
			if chunk.Metadata.AdmiraltySourceReliability == "" ||
				chunk.Metadata.AdmiraltyInformationCredibility == 0 {
				unrated = append(unrated, chunk.ChunkID)
			}
			break
		}
	}

	// A tasking's relevant documents sit across sources rather than within one
	// (srd015 R1.2, srd017 R1.1).
	for _, tier := range corpusSourceTypes() {
		if tiers[tier] == 0 {
			t.Errorf("the store holds no %s document; a tasking's relevant"+
				" documents are supposed to sit across sources", tier)
		}
	}

	// More than one clearance level, with material above the lowest, or the
	// rel11.0 two-level assertion has nothing to withhold (srd010, srd017 R9.5).
	if len(levels) < 2 {
		t.Errorf("the store holds one clearance level %v; the two-level assertion"+
			" has nothing to withhold", levels)
	}
	lowest := manifest.ClearanceLevels[0]
	above := 0
	for level, count := range levels {
		if level != lowest {
			above += count
		}
	}
	if above == 0 {
		t.Errorf("every document is at the lowest declared level %q, so no pair"+
			" the rel11.0 assertion iterates has a document to withhold", lowest)
	}
	for level := range levels {
		if !manifest.declaresClearance(level) {
			t.Errorf("the store holds a document at %q, which the manifest does not"+
				" declare", level)
		}
	}

	// Every document carries an Admiralty rating on both axes, because rel12.2
	// checks the brief's source-evaluation table against these rows
	// (srd015 R1.4, srd017 R2.6).
	if len(unrated) > 0 {
		t.Errorf("%d documents carry no Admiralty rating on both axes, so a brief"+
			" citing them leaves an unrated row; first is %s",
			len(unrated), unrated[0])
	}

	// The corpus states what it is made of wherever a figure over it is read
	// (srd017 R9.3, R9.4).
	if synthetic == 0 {
		t.Error("no document is marked synthetic; the invented tier is what the" +
			" clearance demonstration gates and it must say so in the artifact")
	}
	if synthetic == len(documents) {
		t.Error("every document is marked synthetic; four of the five tiers are" +
			" real published reporting")
	}
	if !strings.Contains(manifest.SyntheticNotice, "invented") {
		t.Error("the corpus manifest carries no notice saying which of its material" +
			" is invented")
	}

	// The corpus is sized past the top-N a turn composes from, so the cutoff
	// bites and a rerank has something to recover (srd014 R1.3, srd015 R1.5).
	relevant := manifest.RetrievalConditions.RelevantToPrimaryTaskng
	if len(relevant) < 9 {
		t.Errorf("the manifest names %d documents relevant to the primary tasking;"+
			" a turn composes eight (the rerank budget, with retrieval breadth"+
			" above it since GH-336), so fewer leaves no document below the"+
			" cutoff", len(relevant))
	}
	held := map[string]bool{}
	for id := range documents {
		held[id] = true
	}
	for _, id := range relevant {
		if !held[id] {
			t.Errorf("the manifest names %s relevant to the primary tasking and the"+
				" store does not hold it", id)
		}
	}
}

// corpusConditionsRoot resolves the repository root from the package directory
// the test runs in.
func corpusConditionsRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for _, candidate := range []string{working + "/..", working} {
		if _, err := os.Stat(candidate + "/" + corpusManifestPath); err == nil {
			return candidate
		}
	}
	t.Fatalf("cannot locate %s from %s", corpusManifestPath, working)
	return ""
}
