// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// renderedChunkMetadataFields returns every metadata field the chatbot's
// document renders read from a retrieved chunk's row. A row missing one fails
// the render template, and the turn with it.
func renderedChunkMetadataFields(t *testing.T) []string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "agents", "chatbot", "request-fanout-declarations.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, match := range regexp.MustCompile(`\{\{ json metadata\.([a-z_]+) \}\}`).FindAllStringSubmatch(string(source), -1) {
		seen[match[1]] = true
	}
	fields := make([]string, 0, len(seen))
	for field := range seen {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	if len(fields) == 0 {
		t.Fatal("no document render reads chunk metadata; the contract this test holds has moved")
	}
	return fields
}

// Every writer of a chunk a turn can retrieve carries the full row the render
// reads, with a rank that agrees with the declared order. The agent-driven
// ingest wrote the clearance alone, so every document ingested through the
// control plane failed the turn that retrieved it (GH-760); the inline
// integration fixtures had the same gap.
func TestChunkWritersCarryTheRowTheDocumentRenderReads(t *testing.T) {
	fields := renderedChunkMetadataFields(t)

	rest, err := os.ReadFile(filepath.Join("..", "agents", "corpus-ingest", "corpus-rest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	line := regexp.MustCompile(`(?m)^\s*metadatas: (\[.*\])\s*$`).FindSubmatch(rest)
	if line == nil {
		t.Fatal("agents/corpus-ingest/corpus-rest.yaml writes no metadatas row")
	}
	var ingestRows []map[string]any
	if err := json.Unmarshal(line[1], &ingestRows); err != nil || len(ingestRows) != 1 {
		t.Fatalf("ingest metadatas %s: %v", line[1], err)
	}

	// A revision reads its carried citations back from the document
	// collection and renders those rows too, so its chunk rows are held to the
	// same contract (GH-843).
	servedChunk, err := documentChromaMetadata(corpusStoredDocument{DocumentID: "HR-22-008"}, corpusChunkRecord{
		ChunkID:  chunkIdentifier("HR-22-008", chunkProfileMedium, 1),
		Metadata: corpusChunkMetadata{Clearance: "secret", AdmiraltySourceReliability: "C", AdmiraltyInformationCredibility: 3},
		Parent:   &corpusChunkParent{DocumentID: "HR-22-008", Position: 1, SiblingCount: 2},
	})
	if err != nil {
		t.Fatal(err)
	}

	writers := map[string]map[string]any{
		"corpus-ingest agent":       ingestRows[0],
		"integration fixture":       fixtureChunkMetadata(clearanceLevels[0]),
		"graded clearance fixture":  fixtureChunkMetadata(clearanceLevels[len(clearanceLevels)-1]),
		"document collection chunk": servedChunk,
	}
	for name, row := range writers {
		for _, field := range fields {
			if _, ok := row[field]; !ok {
				t.Errorf("%s row lacks %q, which the document render reads: %v", name, field, row)
			}
		}
		level, _ := row["clearance"].(string)
		rank, ok := row["clearance_rank"].(float64)
		if !ok {
			if intRank, isInt := row["clearance_rank"].(int); isInt {
				rank, ok = float64(intRank), true
			}
		}
		if !ok || int(rank) != clearanceRank(level) {
			t.Errorf("%s row ranks %q at %v, want %d from the declared order %s",
				name, level, row["clearance_rank"], clearanceRank(level), strings.Join(clearanceLevels, " < "))
		}
	}
}
