// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const overlapFixtureSentence = "the quick brown fox jumps over the lazy dog near the riverbank"

func overlapFixturePrivate(t *testing.T) string {
	t.Helper()
	private := t.TempDir()
	write := func(relative string, data []byte) {
		path := filepath.Join(private, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("data/isw/dailies.jsonl", []byte(`{"text":"`+overlapFixtureSentence+`"}`+"\n"))
	write("data/isw/README.md", []byte("provenance readme words that are allowed to repeat in the tracked tree verbatim"))
	var zipped bytes.Buffer
	archive := gzip.NewWriter(&zipped)
	_, _ = archive.Write([]byte("вчера вечером в городе прошли очень большие и долгие переговоры сторон"))
	_ = archive.Close()
	write("data/news/news.csv.gz", zipped.Bytes())
	write("store/documents/isw-1/chunk.yaml", []byte("chunk_id: isw-1\nsource_path: a\ncorpus: b\ncorpus_version: c\nposition: 1\ntext_sha256: d\nsynthetic: true\n"+
		"text: a stored chunk passage long enough to count as source text for the sweep\n"))
	return private
}

func overlapFixtureRuns(t *testing.T, tracked map[string]string) []overlapRun {
	t.Helper()
	candidates := overlapCandidates(tracked)
	if _, err := overlapMarkPrivate(overlapFixturePrivate(t), candidates); err != nil {
		t.Fatal(err)
	}
	return overlapRuns(tracked, candidates)
}

// A copied sentence is found whatever punctuation or case surrounds it, and the
// run is reported as the whole stretch rather than as overlapping eight-word
// pieces.
func TestOverlapFindsACopiedSentenceDespiteFormatting(t *testing.T) {
	runs := overlapFixtureRuns(t, map[string]string{
		"a.go": `x := "The quick brown fox" + "jumps, over the lazy dog: near the riverbank"`,
	})
	if len(runs) != 1 || runs[0].Words != 12 || runs[0].File != "a.go" {
		t.Fatalf("want one 12-word run in a.go, got %+v", runs)
	}
}

// A paraphrase shares words and no run, and a seven-word match sits under the
// threshold, so neither is reported.
func TestOverlapIgnoresParaphraseAndShortMatches(t *testing.T) {
	runs := overlapFixtureRuns(t, map[string]string{
		"a.md": "a fast brown fox leapt the lazy dog by the river",
		"b.md": "the quick brown fox jumps over the sleeping cat",
	})
	if len(runs) != 0 {
		t.Fatalf("want nothing reported, got %+v", runs)
	}
}

// The gzipped Telegram tier and non-Latin scripts are in scope, because that is
// where the largest keep-private tier lives.
func TestOverlapReadsGzippedTiersAndCyrillic(t *testing.T) {
	runs := overlapFixtureRuns(t, map[string]string{
		"a.md": "вчера вечером в городе прошли очень большие и долгие переговоры",
	})
	if len(runs) != 1 || runs[0].Words != 10 {
		t.Fatalf("want one 10-word Cyrillic run, got %+v", runs)
	}
}

// Store records are read by field, so sharing a schema is not copying, while
// their text fields stay in scope. Private READMEs are out of scope because the
// tracked copies are canonical.
func TestOverlapSeparatesStoreSchemaFromStoreText(t *testing.T) {
	runs := overlapFixtureRuns(t, map[string]string{
		"schema.yaml": "chunk_id: x\nsource_path: y\ncorpus: z\ncorpus_version: w\nposition: 2\ntext_sha256: v\nsynthetic: false\ntext: other\n",
		"quote.md":    "a stored chunk passage long enough to count as source text",
		"readme.md":   "provenance readme words that are allowed to repeat in the tracked tree verbatim",
	})
	if len(runs) != 1 || runs[0].File != "quote.md" {
		t.Fatalf("want only quote.md reported, got %+v", runs)
	}
}

// A review covers one run in one file. Editing the run, or copying it elsewhere,
// makes it unreviewed again.
func TestOverlapReviewCoversOneRunInOneFile(t *testing.T) {
	run := overlapRun{File: "a.go", Words: 9, Text: "one two three four five six seven eight nine"}
	entry := overlapReviewedEntry{File: "a.go", Fingerprint: run.fingerprint(), Words: 9, Reason: "test"}
	if got := overlapUnreviewed([]overlapRun{run}, []overlapReviewedEntry{entry}); len(got) != 0 {
		t.Errorf("a reviewed run was reported: %+v", got)
	}
	moved := run
	moved.File = "b.go"
	edited := run
	edited.Text = strings.Replace(run.Text, "nine", "ten", 1)
	if got := overlapUnreviewed([]overlapRun{moved, edited}, []overlapReviewedEntry{entry}); len(got) != 2 {
		t.Errorf("a moved or edited run should be unreviewed, got %+v", got)
	}
}
