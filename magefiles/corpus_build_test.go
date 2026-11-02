// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The build's behavior is proven here against a counting stand-in for the
// provider rather than against a live one. Every assertion this file makes is
// about how many times the provider was called and what was written, which is
// exactly what a live run cannot show: a build that recomputed every vector and
// wrote back an identical record looks the same from the outside
// (rel10.4-uc001 S3).

// countingEmbedder records how many texts it was asked to embed and returns a
// vector derived from the text, so a reused vector is distinguishable from a
// recomputed one by value as well as by count.
type countingEmbedder struct {
	calls     int
	dimension int
	failOn    string
	texts     []string
}

func (e *countingEmbedder) identity(name string) corpusEmbeddingIdentity {
	return corpusEmbeddingIdentity{
		identity: name,
		provider: "test",
		embed: func(text string) ([]float64, error) {
			e.calls++
			e.texts = append(e.texts, text)
			if e.failOn != "" && strings.Contains(text, e.failOn) {
				return nil, fmt.Errorf("provider rejected the text")
			}
			dimension := e.dimension
			if dimension == 0 {
				dimension = 4
			}
			values := make([]float64, dimension)
			for i := range values {
				values[i] = float64(len(text)+i) / 100
			}
			return values, nil
		},
	}
}

const testCorpusManifest = `id: test-corpus
version: "3.2"
title: Test Corpus
synthetic: true
synthetic_notice: Every document here is invented.
clearance_levels:
  - unclassified
  - restricted
  - confidential
  - secret
source_types:
  humint: Human-source summaries.
  osint: Public reporting.
`

// writeTestCorpusDocument writes one document with the given front matter and a
// body long enough to produce the requested number of paragraphs.
func writeTestCorpusDocument(t *testing.T, root, name, frontMatter string, paragraphs int) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(corpusDocumentsDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create documents dir: %v", err)
	}
	var body strings.Builder
	body.WriteString("---\n")
	body.WriteString(frontMatter)
	body.WriteString("---\n\n")
	for i := 0; i < paragraphs; i++ {
		body.WriteString(fmt.Sprintf(
			"Paragraph %d of %s describes a fictional observation at a fictional place"+
				" and repeats enough words that the paragraph reaches a realistic length"+
				" for a retrieval unit rather than a single line.\n\n", i+1, name))
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func newTestCorpus(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, corpusDir), 0o755); err != nil {
		t.Fatalf("create corpus dir: %v", err)
	}
	manifest := filepath.Join(root, filepath.FromSlash(corpusManifestPath))
	if err := os.WriteFile(manifest, []byte(testCorpusManifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return root
}

// storeSnapshot reads every file under the store, so "unchanged" is a claim
// about bytes rather than about a count the build reported about itself.
func storeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	base := filepath.Join(root, filepath.FromSlash(corpusStoreDir))
	err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		snapshot[filepath.ToSlash(relative)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot store: %v", err)
	}
	return snapshot
}

func loadStoreForTest(t *testing.T, root string) map[string]corpusChunkRecord {
	t.Helper()
	records, err := loadCorpusStore(root)
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	return records
}

func TestCorpusBuildCopiesDeclaredFrontMatterOntoEveryChunk(t *testing.T) {
	root := newTestCorpus(t)
	writeTestCorpusDocument(t, root, "hum-0001.md", `source_id: HUM-0001
source_type: humint
clearance: confidential
admiralty:
  source_reliability: B
  information_credibility: 2
keywords: [harbour, loading]
synthetic: true
`, 6)

	embedder := &countingEmbedder{}
	report, err := buildCorpusStore(root, embedder.identity("test-embed"), defaultCorpusChunkParameters())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if report.Chunks < 2 {
		t.Fatalf("a six-paragraph document produced %d chunks, want at least 2", report.Chunks)
	}

	records := loadStoreForTest(t, root)
	if len(records) != report.Chunks {
		t.Fatalf("store holds %d records, build reported %d chunks", len(records), report.Chunks)
	}
	for _, id := range sortedCorpusChunkIDs(records) {
		record := records[id]
		metadata := record.Metadata
		if metadata.SourceID != "HUM-0001" || metadata.SourceType != "humint" {
			t.Errorf("chunk %s carries source %q/%q, want HUM-0001/humint",
				id, metadata.SourceID, metadata.SourceType)
		}
		if metadata.Clearance != "confidential" {
			t.Errorf("chunk %s carries clearance %q, want the document's declared confidential",
				id, metadata.Clearance)
		}
		if metadata.AdmiraltySourceReliability != "B" || metadata.AdmiraltyInformationCredibility != 2 {
			t.Errorf("chunk %s carries Admiralty %q/%d, want B/2",
				id, metadata.AdmiraltySourceReliability, metadata.AdmiraltyInformationCredibility)
		}
		if strings.Join(metadata.Keywords, ",") != "harbour,loading" {
			t.Errorf("chunk %s carries keywords %v, want the document's declared pair", id, metadata.Keywords)
		}
		if record.CorpusVersion != "3.2" || !record.Synthetic {
			t.Errorf("chunk %s records corpus version %q synthetic %v, want 3.2/true",
				id, record.CorpusVersion, record.Synthetic)
		}
	}
}

func TestCorpusBuildIdentifiersDeriveFromSourceAndPosition(t *testing.T) {
	root := newTestCorpus(t)
	writeTestCorpusDocument(t, root, "osi-0002.md", `source_id: OSI-0002
source_type: osint
clearance: unclassified
admiralty:
  source_reliability: C
  information_credibility: 3
keywords: [tender]
synthetic: true
`, 6)

	embedder := &countingEmbedder{}
	if _, err := buildCorpusStore(root, embedder.identity("test-embed"), defaultCorpusChunkParameters()); err != nil {
		t.Fatalf("build: %v", err)
	}
	records := loadStoreForTest(t, root)
	ids := sortedCorpusChunkIDs(records)
	for position, id := range ids {
		want := fmt.Sprintf("OSI-0002-%04d", position+1)
		if id != want {
			t.Errorf("chunk id %q, want %q derived from source and position", id, want)
		}
		if records[id].Position != position+1 {
			t.Errorf("chunk %s records position %d, want %d", id, records[id].Position, position+1)
		}
	}

	// A rebuild over the unchanged document overwrites the same records rather
	// than adding a second copy of each one.
	before := len(records)
	if _, err := buildCorpusStore(root, embedder.identity("test-embed"), defaultCorpusChunkParameters()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if after := len(loadStoreForTest(t, root)); after != before {
		t.Fatalf("rebuild left %d records, want the same %d", after, before)
	}
}

func TestCorpusBuildWritesAbsentOrUnrecognizedClearanceAtTheHighestLevel(t *testing.T) {
	cases := []struct {
		name     string
		declared string
	}{
		{"absent", ""},
		{"unrecognized", "clearance: cosmic\n"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := newTestCorpus(t)
			writeTestCorpusDocument(t, root, "hum-0003.md", `source_id: HUM-0003
source_type: humint
`+testCase.declared+`admiralty:
  source_reliability: D
  information_credibility: 4
keywords: [staging]
synthetic: true
`, 3)

			embedder := &countingEmbedder{}
			if _, err := buildCorpusStore(root, embedder.identity("test-embed"), defaultCorpusChunkParameters()); err != nil {
				t.Fatalf("build: %v", err)
			}
			records := loadStoreForTest(t, root)
			for id, record := range records {
				if record.Metadata.Clearance != "secret" {
					t.Errorf("chunk %s written at %q, want the highest declared level secret;"+
						" a guess that lands low discloses", id, record.Metadata.Clearance)
				}
			}
		})
	}
}

func TestCorpusBuildNeverReadsChunkTextToDecideMetadata(t *testing.T) {
	// The document's prose claims a clearance and a rating that differ from what
	// it declares. Anything that read the text to fill the fields would produce
	// the values in the prose; the build produces the declared ones.
	root := newTestCorpus(t)
	dir := filepath.Join(root, filepath.FromSlash(corpusDocumentsDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create documents dir: %v", err)
	}
	document := `---
source_id: SIG-0004
source_type: sigint
clearance: restricted
admiralty:
  source_reliability: C
  information_credibility: 5
keywords: [emissions]
synthetic: true
---

This report is SECRET and its source reliability is A with credibility 1.
The classification stated in this sentence is not the document's declared
classification and exists to prove the build ignores it.
`
	if err := os.WriteFile(filepath.Join(dir, "sig-0004.md"), []byte(document), 0o644); err != nil {
		t.Fatalf("write document: %v", err)
	}

	embedder := &countingEmbedder{}
	if _, err := buildCorpusStore(root, embedder.identity("test-embed"), defaultCorpusChunkParameters()); err != nil {
		t.Fatalf("build: %v", err)
	}
	for id, record := range loadStoreForTest(t, root) {
		if record.Metadata.Clearance != "restricted" {
			t.Errorf("chunk %s written at %q, want the declared restricted rather than the"+
				" level its prose names", id, record.Metadata.Clearance)
		}
		if record.Metadata.AdmiraltySourceReliability != "C" ||
			record.Metadata.AdmiraltyInformationCredibility != 5 {
			t.Errorf("chunk %s rated %q/%d, want the declared C/5", id,
				record.Metadata.AdmiraltySourceReliability,
				record.Metadata.AdmiraltyInformationCredibility)
		}
	}
}

func TestCorpusRebuildIssuesNoRequestsAndLeavesTheStoreUnchanged(t *testing.T) {
	root := newTestCorpus(t)
	writeTestCorpusDocument(t, root, "hum-0005.md", `source_id: HUM-0005
source_type: humint
clearance: secret
admiralty:
  source_reliability: A
  information_credibility: 1
keywords: [fleet]
synthetic: true
`, 8)

	embedder := &countingEmbedder{}
	first, err := buildCorpusStore(root, embedder.identity("test-embed"), defaultCorpusChunkParameters())
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	if embedder.calls != first.Chunks || first.Embedded != first.Chunks {
		t.Fatalf("first build embedded %d of %d chunks in %d calls, want one call per chunk",
			first.Embedded, first.Chunks, embedder.calls)
	}
	before := storeSnapshot(t, root)

	embedder.calls = 0
	second, err := buildCorpusStore(root, embedder.identity("test-embed"), defaultCorpusChunkParameters())
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	// Both halves are asserted. A build that recomputed every vector and wrote
	// back an identical record would satisfy the second alone while still paying
	// the provider, so store equality is not treated as sufficient evidence.
	if embedder.calls != 0 {
		t.Errorf("second build issued %d embedding requests, want 0", embedder.calls)
	}
	if second.Reused != second.Chunks || second.Embedded != 0 {
		t.Errorf("second build reused %d and embedded %d of %d chunks, want all reused",
			second.Reused, second.Embedded, second.Chunks)
	}
	if second.Written != 0 {
		t.Errorf("second build wrote %d records, want 0", second.Written)
	}
	after := storeSnapshot(t, root)
	if len(before) != len(after) {
		t.Fatalf("store holds %d files after the rebuild, want the original %d", len(after), len(before))
	}
	for name, content := range before {
		if after[name] != content {
			t.Errorf("%s changed across a rebuild over an unchanged corpus", name)
		}
	}
}

func TestCorpusBuildAtASecondIdentityAddsWithoutDisplacingTheFirst(t *testing.T) {
	root := newTestCorpus(t)
	writeTestCorpusDocument(t, root, "osi-0006.md", `source_id: OSI-0006
source_type: osint
clearance: unclassified
admiralty:
  source_reliability: B
  information_credibility: 2
keywords: [shipping]
synthetic: true
`, 6)

	ollama := &countingEmbedder{dimension: 8}
	first, err := buildCorpusStore(root, ollama.identity("qwen3-embedding:8b"), defaultCorpusChunkParameters())
	if err != nil {
		t.Fatalf("first identity build: %v", err)
	}
	firstVectors := map[string][]float64{}
	for id, record := range loadStoreForTest(t, root) {
		values, held := record.vector("qwen3-embedding:8b")
		if !held {
			t.Fatalf("chunk %s carries no vector for the identity just built", id)
		}
		firstVectors[id] = append([]float64(nil), values...)
	}

	cohere := &countingEmbedder{dimension: 5}
	second, err := buildCorpusStore(root, cohere.identity("embed-v4.0"), defaultCorpusChunkParameters())
	if err != nil {
		t.Fatalf("second identity build: %v", err)
	}
	if cohere.calls != second.Chunks {
		t.Errorf("the second identity build issued %d requests for %d chunks, want one each",
			cohere.calls, second.Chunks)
	}
	if second.Chunks != first.Chunks {
		t.Fatalf("the two builds saw %d and %d chunks", first.Chunks, second.Chunks)
	}

	for id, record := range loadStoreForTest(t, root) {
		identities := record.identities()
		sort.Strings(identities)
		if strings.Join(identities, ",") != "embed-v4.0,qwen3-embedding:8b" {
			t.Errorf("chunk %s carries identities %v, want both families", id, identities)
		}
		for _, vector := range record.Vectors {
			if vector.Dimension != len(vector.Values) {
				t.Errorf("chunk %s identity %s records dimension %d for %d values",
					id, vector.Identity, vector.Dimension, len(vector.Values))
			}
		}
		kept, _ := record.vector("qwen3-embedding:8b")
		original := firstVectors[id]
		if len(kept) != len(original) {
			t.Fatalf("chunk %s first-family vector is now %d long, was %d", id, len(kept), len(original))
		}
		for i := range kept {
			if kept[i] != original[i] {
				t.Errorf("chunk %s first-family vector changed at %d", id, i)
			}
		}
	}

	index, err := readCorpusStoreIndex(root)
	if err != nil {
		t.Fatalf("read store index: %v", err)
	}
	if len(index.Identities) != 2 {
		t.Fatalf("store index names %d identities, want 2", len(index.Identities))
	}
	dimensions := map[string]int{}
	for _, held := range index.Identities {
		dimensions[held.Identity] = held.Dimension
	}
	if dimensions["qwen3-embedding:8b"] != 8 || dimensions["embed-v4.0"] != 5 {
		t.Errorf("store index records dimensions %v, want 8 and 5 per identity", dimensions)
	}

	// A third build at the first identity finds every pair already held, which
	// is what makes the store seed either family without a rebuild.
	ollama.calls = 0
	if _, err := buildCorpusStore(root, ollama.identity("qwen3-embedding:8b"), defaultCorpusChunkParameters()); err != nil {
		t.Fatalf("rebuild at the first identity: %v", err)
	}
	if ollama.calls != 0 {
		t.Errorf("rebuilding the first identity issued %d requests, want 0", ollama.calls)
	}
}

func TestCorpusBuildEmbeddingFailureNamesTheChunkAndWritesNoPartialRecord(t *testing.T) {
	root := newTestCorpus(t)
	writeTestCorpusDocument(t, root, "hum-0007.md", `source_id: HUM-0007
source_type: humint
clearance: restricted
admiralty:
  source_reliability: C
  information_credibility: 3
keywords: [rotation]
synthetic: true
`, 8)

	// The stand-in rejects every text carrying the marker the last paragraphs
	// hold, so the failure lands after earlier chunks have already been written.
	failing := &countingEmbedder{failOn: "Paragraph 8"}
	_, err := buildCorpusStore(root, failing.identity("test-embed"), defaultCorpusChunkParameters())
	if err == nil {
		t.Fatal("a build whose provider failed returned no error")
	}
	if !strings.Contains(err.Error(), "HUM-0007-") {
		t.Errorf("the error names no chunk: %v", err)
	}

	records := loadStoreForTest(t, root)
	for id, record := range records {
		values, held := record.vector("test-embed")
		if !held || len(values) == 0 {
			t.Errorf("chunk %s was written with an absent vector; a partial record is what the"+
				" next build reads as already held and never retries", id)
		}
		for _, vector := range record.Vectors {
			if vector.Identity != "test-embed" {
				t.Errorf("chunk %s carries a vector under identity %q, which did not produce it",
					id, vector.Identity)
			}
		}
	}

	// The chunk that failed is absent rather than present-and-empty, so a later
	// build retries it.
	retry := &countingEmbedder{}
	report, err := buildCorpusStore(root, retry.identity("test-embed"), defaultCorpusChunkParameters())
	if err != nil {
		t.Fatalf("retry build: %v", err)
	}
	if report.Embedded == 0 {
		t.Error("the retry build embedded nothing, so the failed chunk was never retried")
	}
	if len(loadStoreForTest(t, root)) != report.Chunks {
		t.Errorf("the retry build left %d records for %d chunks",
			len(loadStoreForTest(t, root)), report.Chunks)
	}
}

func TestCorpusBuildRecomputesWhenTheDocumentTextChanges(t *testing.T) {
	root := newTestCorpus(t)
	frontMatter := `source_id: OSI-0008
source_type: osint
clearance: unclassified
admiralty:
  source_reliability: C
  information_credibility: 3
keywords: [rail]
synthetic: true
`
	writeTestCorpusDocument(t, root, "osi-0008.md", frontMatter, 4)

	embedder := &countingEmbedder{}
	if _, err := buildCorpusStore(root, embedder.identity("test-embed"), defaultCorpusChunkParameters()); err != nil {
		t.Fatalf("first build: %v", err)
	}

	// Rewriting the document with different prose changes the chunk text under
	// the same identifiers. A stored vector for the old text is not a vector for
	// this chunk, and reusing it is the drift the store exists to remove.
	writeTestCorpusDocument(t, root, "osi-0008.md", frontMatter, 5)
	embedder.calls = 0
	report, err := buildCorpusStore(root, embedder.identity("test-embed"), defaultCorpusChunkParameters())
	if err != nil {
		t.Fatalf("rebuild after an edit: %v", err)
	}
	if embedder.calls == 0 {
		t.Error("an edited document was rebuilt with no embedding request, so a stale vector was reused")
	}
	if report.Reused == report.Chunks {
		t.Error("every chunk was reused after the document changed")
	}
}

func TestCorpusBuildRemovesRecordsTheCorpusNoLongerProduces(t *testing.T) {
	root := newTestCorpus(t)
	frontMatter := `source_id: HUM-0009
source_type: humint
clearance: secret
admiralty:
  source_reliability: B
  information_credibility: 2
keywords: [medical]
synthetic: true
`
	writeTestCorpusDocument(t, root, "hum-0009.md", frontMatter, 9)
	embedder := &countingEmbedder{}
	long, err := buildCorpusStore(root, embedder.identity("test-embed"), defaultCorpusChunkParameters())
	if err != nil {
		t.Fatalf("first build: %v", err)
	}

	writeTestCorpusDocument(t, root, "hum-0009.md", frontMatter, 2)
	short, err := buildCorpusStore(root, embedder.identity("test-embed"), defaultCorpusChunkParameters())
	if err != nil {
		t.Fatalf("rebuild after shortening: %v", err)
	}
	if short.Chunks >= long.Chunks {
		t.Fatalf("shortening the document produced %d chunks against %d", short.Chunks, long.Chunks)
	}
	if len(short.Removed) == 0 {
		t.Error("the rebuild reported no removed records")
	}
	if held := len(loadStoreForTest(t, root)); held != short.Chunks {
		t.Errorf("the store holds %d records for %d chunks; a seed would write chunks the"+
			" corpus no longer produces", held, short.Chunks)
	}
}

func TestCorpusStoreIndexNamesTheCorpusVersionAndTheIdentitiesHeld(t *testing.T) {
	root := newTestCorpus(t)
	writeTestCorpusDocument(t, root, "osi-0010.md", `source_id: OSI-0010
source_type: osint
clearance: unclassified
admiralty:
  source_reliability: B
  information_credibility: 2
keywords: [warning]
synthetic: true
`, 4)

	embedder := &countingEmbedder{dimension: 6}
	report, err := buildCorpusStore(root, embedder.identity("embed-v4.0"), defaultCorpusChunkParameters())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	index, err := readCorpusStoreIndex(root)
	if err != nil {
		t.Fatalf("read store index: %v", err)
	}
	if index.CorpusVersion != "3.2" || index.Corpus != "test-corpus" {
		t.Errorf("store index names corpus %q version %q, want test-corpus/3.2",
			index.Corpus, index.CorpusVersion)
	}
	if !index.Synthetic || index.SyntheticNotice == "" {
		t.Error("store index does not identify the corpus as synthetic in the artifact")
	}
	if index.Chunks != report.Chunks {
		t.Errorf("store index counts %d chunks, build wrote %d", index.Chunks, report.Chunks)
	}
	if len(index.Identities) != 1 {
		t.Fatalf("store index names %d identities, want 1", len(index.Identities))
	}
	held := index.Identities[0]
	if held.Identity != "embed-v4.0" || held.Dimension != 6 || held.Chunks != report.Chunks {
		t.Errorf("store index records %+v, want embed-v4.0 at dimension 6 over every chunk", held)
	}
	if index.Chunking.TargetCharacters == 0 || index.Chunking.Splitter == "" {
		t.Errorf("store index records no chunking parameters: %+v", index.Chunking)
	}
}

func TestCorpusStoreVectorsAreLegibleNumbersRatherThanAnEncodedBlob(t *testing.T) {
	root := newTestCorpus(t)
	writeTestCorpusDocument(t, root, "sig-0011.md", `source_id: SIG-0011
source_type: sigint
clearance: confidential
admiralty:
  source_reliability: A
  information_credibility: 2
keywords: [radar]
synthetic: true
`, 3)

	embedder := &countingEmbedder{dimension: 3}
	if _, err := buildCorpusStore(root, embedder.identity("embed-v4.0"), defaultCorpusChunkParameters()); err != nil {
		t.Fatalf("build: %v", err)
	}
	path := filepath.Join(root, filepath.FromSlash(corpusStoreChunksDir),
		corpusRecordFilename("SIG-0011-0001"))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read chunk record: %v", err)
	}
	text := string(data)
	for _, want := range []string{"chunk_id:", "identity: embed-v4.0", "dimension: 3", "values:", "clearance: confidential"} {
		if !strings.Contains(text, want) {
			t.Errorf("chunk record does not carry %q; a reviewer reads this file without running anything", want)
		}
	}
	if strings.Contains(text, "!!binary") {
		t.Error("a vector is stored as an encoded blob rather than as a sequence of numbers")
	}
}

func TestChunkCorpusTextCarriesOverlapAndStaysDeterministic(t *testing.T) {
	body := ""
	for i := 0; i < 6; i++ {
		body += fmt.Sprintf("Paragraph %d holds enough words to be a retrieval unit on its"+
			" own rather than a single short line of prose, and it runs on for long enough"+
			" that six of them together exceed the target chunk size and force the splitter"+
			" to emit more than one chunk.\n\n", i+1)
	}
	params := defaultCorpusChunkParameters()
	first := chunkCorpusText(body, params)
	second := chunkCorpusText(body, params)
	if len(first) < 2 {
		t.Fatalf("six paragraphs produced %d chunks, want at least 2", len(first))
	}
	if strings.Join(first, "\x00") != strings.Join(second, "\x00") {
		t.Fatal("chunking the same body twice produced different chunks")
	}
	tail := strings.Split(strings.TrimSpace(first[0]), "\n\n")
	last := tail[len(tail)-1]
	if !strings.HasPrefix(first[1], last) {
		t.Error("the second chunk does not carry the first chunk's closing paragraph forward as overlap")
	}
}

func TestReadCorpusManifestRejectsAManifestWithoutAnOrderedClearanceSet(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, corpusDir), 0o755); err != nil {
		t.Fatalf("create corpus dir: %v", err)
	}
	manifest := filepath.Join(root, filepath.FromSlash(corpusManifestPath))
	if err := os.WriteFile(manifest, []byte("id: x\nversion: \"1\"\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if _, err := readCorpusManifest(root); err == nil {
		t.Fatal("a manifest declaring no clearance levels was accepted")
	}
}

func TestSplitCorpusFrontMatterRejectsADocumentWithoutADeclaredBlock(t *testing.T) {
	if _, _, err := splitCorpusFrontMatter("# No front matter\n\nJust prose.\n"); err == nil {
		t.Fatal("a document with no front matter was accepted; its absent clearance would" +
			" have been written at the highest level and hidden the authoring mistake")
	}
	if _, _, err := splitCorpusFrontMatter("---\nsource_type: humint\n---\n\nProse.\n"); err == nil {
		t.Fatal("a document declaring no source_id was accepted")
	}
}
