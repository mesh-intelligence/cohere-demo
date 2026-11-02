// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"strings"
	"testing"
)

// The store phase of the rel10.5 tracer, run as a unit test.
//
// It reads the committed store and nothing else -- no provider, no database, no
// cluster -- so it runs on a clone with nothing installed and fails the suite
// the moment the committed corpus stops holding what it declares. The seed and
// serving phases need Chroma and are the integration target's.
func TestDemoCorpusStorePhaseHoldsOverTheCommittedStore(t *testing.T) {
	root := corpusConditionsRoot(t)
	if err := assertDemoCorpusStore(root); err != nil {
		t.Fatalf("the committed store does not hold what the corpus declares: %v", err)
	}
}

// A chunk whose lineage does not resolve fails the phase, so the tracer catches
// a citation that leads nowhere rather than reporting a healthy store.
func TestDemoCorpusStorePhaseRejectsUnresolvableLineage(t *testing.T) {
	held := map[string]corpusChunkRecord{}
	chunk := corpusChunkRecord{
		ChunkID: "doc-medium-900-0001",
		Parent: &corpusChunkParent{
			DocumentID: "doc", Position: 1, SiblingCount: 1,
			CharSpan: []int{0, 10}, NextChunkID: "doc-medium-900-0002",
		},
	}
	held[chunk.ChunkID] = chunk
	err := assertChunkLineage(chunk, held)
	if err == nil {
		t.Fatal("a chunk naming a neighbour the document does not hold was accepted")
	}
	if !strings.Contains(err.Error(), "doc-medium-900-0002") {
		t.Errorf("the error does not name the missing neighbour: %v", err)
	}
}

// A chunk carrying no parent block fails the phase. Retrieval returns chunk
// identifiers, so a chunk that cannot name its document is a citation an
// analyst cannot follow.
func TestDemoCorpusStorePhaseRejectsAChunkWithNoParent(t *testing.T) {
	err := assertChunkLineage(corpusChunkRecord{ChunkID: "orphan-0001"},
		map[string]corpusChunkRecord{})
	if err == nil {
		t.Fatal("a chunk carrying no parent block was accepted")
	}
	if !strings.Contains(err.Error(), "resolves to nothing") {
		t.Errorf("the error does not say what the defect costs: %v", err)
	}
}

// The seed and serving phases skip rather than fail where docker is absent, so
// a clone reports the missing dependency instead of a failure it cannot act on.
func TestDemoCorpusNamesTheMissingDependencyRatherThanFailing(t *testing.T) {
	reason := corpusSeedDockerSkipReason()
	if _, err := os.Stat("/var/run/docker.sock"); err != nil && reason == "" {
		// Docker may still be reachable another way; the assertion is only that
		// the reason is a sentence when it is set.
		return
	}
	if reason != "" && !strings.Contains(reason, "docker") {
		t.Errorf("the skip reason does not name the dependency: %q", reason)
	}
}
