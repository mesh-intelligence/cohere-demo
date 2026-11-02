// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The rel10.5 store suite. It proves what srd017 R7 asks of the multi-version
// store: a document's envelope and all its profiles read back together, the
// index naming the profiles and their vector counts, pruning that removes every
// variant of a document rather than one profile's worth, and a rebuild over
// unchanged inputs writing nothing.
//
// The prune case is the one that matters most. A chunk identifier carries its
// profile, so pruning on the identifier alone removes one profile's chunks and
// leaves the rest -- and a seed reading the store afterwards loads chunks of a
// document the corpus no longer holds.

func storeFixtureEnvelopes() []corpusEnvelope {
	return lineageFixtureEnvelopes()
}

// writeFixtureStore cuts the fixture documents at every profile and writes them,
// returning the root and the identifiers the build wanted.
func writeFixtureStore(
	t *testing.T, root string, envelopes []corpusEnvelope,
) (map[string]bool, map[string]bool, int) {
	t.Helper()
	prepareSplitSibling(t, root)
	manifest := ukraineTestManifest()
	wantedDocuments := make(map[string]bool, len(envelopes))
	wantedChunks := make(map[string]bool)
	written := 0
	for _, envelope := range envelopes {
		wantedDocuments[envelope.DocumentID] = true
		if changed, err := writeCorpusEnvelopeIfChanged(root, envelope); err != nil {
			t.Fatalf("write envelope %s: %v", envelope.DocumentID, err)
		} else if changed {
			written++
		}
		byProfile := chunkEnvelopeAtProfiles(envelope, defaultChunkProfiles(), manifest)
		for _, profile := range defaultChunkProfiles() {
			for _, record := range byProfile[profile.Name] {
				wantedChunks[record.ChunkID] = true
				if changed, err := writeCorpusDocumentChunkIfChanged(root, record); err != nil {
					t.Fatalf("write chunk %s: %v", record.ChunkID, err)
				} else if changed {
					written++
				}
			}
		}
	}
	return wantedDocuments, wantedChunks, written
}

// A document's raw envelope and every profile cut from it are read back
// together under its identifier, rather than gathered from across the store
// (srd017 R7.1, rel10.5-uc001 S8).
func TestStoreCoLocatesAnEnvelopeWithEveryProfile(t *testing.T) {
	root := t.TempDir()
	envelopes := storeFixtureEnvelopes()
	writeFixtureStore(t, root, envelopes)

	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		t.Fatalf("load the document store: %v", err)
	}
	if len(documents) != len(envelopes) {
		t.Fatalf("the store holds %d documents for %d envelopes",
			len(documents), len(envelopes))
	}
	for _, envelope := range envelopes {
		document, held := documents[envelope.DocumentID]
		if !held {
			t.Errorf("the store holds no document %s", envelope.DocumentID)
			continue
		}
		if document.Envelope.DocumentID != envelope.DocumentID {
			t.Errorf("document %s reads back with envelope %s",
				envelope.DocumentID, document.Envelope.DocumentID)
		}
		if document.Envelope.searchableText() != envelope.searchableText() {
			t.Errorf("document %s round-tripped with different searchable text",
				envelope.DocumentID)
		}
		if len(document.Envelope.Segments) != len(envelope.Segments) {
			t.Errorf("document %s round-tripped with %d segments, wrote %d",
				envelope.DocumentID, len(document.Envelope.Segments), len(envelope.Segments))
		}
		profiles := document.profiles()
		if len(profiles) != len(defaultChunkProfiles()) {
			t.Errorf("document %s holds profiles %v, want all %d",
				envelope.DocumentID, profiles, len(defaultChunkProfiles()))
		}
		for _, profile := range defaultChunkProfiles() {
			if len(document.chunksAtProfile(profile.Name)) == 0 {
				t.Errorf("document %s holds no chunk at profile %s",
					envelope.DocumentID, profile.Name)
			}
		}
	}
}

// The unsearchable segments survive the round trip, because the serving path
// returns the whole document when an analyst opens a citation and the
// classification line and endnote tail are part of what they read
// (srd017 R1.4, R7.5).
func TestStoreRoundTripsUnsearchableSegments(t *testing.T) {
	root := t.TempDir()
	envelopes := storeFixtureEnvelopes()
	writeFixtureStore(t, root, envelopes)

	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		t.Fatalf("load the document store: %v", err)
	}
	report := documents["HR-22-014"]
	found := false
	for _, segment := range report.Envelope.Segments {
		if segment.Role == segmentRoleAuthority {
			found = true
			if segment.Searchable {
				t.Error("the authority segment round-tripped as searchable")
			}
			if !strings.Contains(segment.Text, "Classified by") {
				t.Errorf("the authority segment round-tripped as %q", segment.Text)
			}
		}
	}
	if !found {
		t.Error("the authority segment did not survive the round trip")
	}
}

// A rebuild over unchanged inputs writes no record at all (srd017 R7.4). This
// is asserted on the write count rather than on the store's contents, because a
// build that rewrote every file with identical bytes would leave the store
// equal while doing the work.
func TestStoreRebuildOverUnchangedInputWritesNothing(t *testing.T) {
	root := t.TempDir()
	envelopes := storeFixtureEnvelopes()
	_, _, first := writeFixtureStore(t, root, envelopes)
	if first == 0 {
		t.Fatal("the first build wrote nothing")
	}
	_, _, second := writeFixtureStore(t, root, envelopes)
	if second != 0 {
		t.Errorf("a rebuild over unchanged input wrote %d records", second)
	}
}

// Editing one document rewrites that document and leaves the others untouched,
// so the cost of a corpus change is proportional to the change.
func TestStoreRebuildWritesOnlyWhatChanged(t *testing.T) {
	root := t.TempDir()
	envelopes := storeFixtureEnvelopes()
	writeFixtureStore(t, root, envelopes)

	edited := append([]corpusEnvelope(nil), envelopes...)
	edited[1].Segments = append([]corpusSegment(nil), envelopes[1].Segments...)
	edited[1].Segments[1].Text = "1. (S) SUMMARY: The node was destroyed. END SUMMARY."
	_, _, written := writeFixtureStore(t, root, edited)
	if written == 0 {
		t.Fatal("editing a document wrote nothing")
	}
	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		t.Fatalf("load the document store: %v", err)
	}
	untouched := documents[envelopes[0].DocumentID]
	for _, chunk := range untouched.Chunks {
		if strings.Contains(chunk.Text, "destroyed") {
			t.Errorf("editing one document changed chunk %s of another", chunk.ChunkID)
		}
	}
}

// Removing a document removes every profile variant of it (srd017 R7.3).
// Pruning on the chunk identifier alone would leave the other profiles behind,
// and a seed reading the store afterwards would load chunks of a document the
// corpus no longer holds.
func TestStorePruneRemovesEveryProfileVariantOfADocument(t *testing.T) {
	root := t.TempDir()
	envelopes := storeFixtureEnvelopes()
	writeFixtureStore(t, root, envelopes)

	dropped := envelopes[1].DocumentID
	kept := make([]corpusEnvelope, 0, len(envelopes)-1)
	for _, envelope := range envelopes {
		if envelope.DocumentID != dropped {
			kept = append(kept, envelope)
		}
	}
	wantedDocuments, wantedChunks, _ := writeFixtureStore(t, root, kept)

	removed, err := pruneCorpusDocumentStore(root, wantedDocuments, wantedChunks)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(removed) == 0 {
		t.Fatal("pruning removed nothing after a document was dropped")
	}
	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		t.Fatalf("load after prune: %v", err)
	}
	if _, held := documents[dropped]; held {
		t.Errorf("document %s survived the prune", dropped)
	}
	if _, err := os.Stat(mustCorpusDocumentDir(t, root, dropped)); !os.IsNotExist(err) {
		t.Errorf("the directory of document %s survived the prune", dropped)
	}
	for _, envelope := range kept {
		if _, held := documents[envelope.DocumentID]; !held {
			t.Errorf("pruning removed document %s, which the corpus still holds",
				envelope.DocumentID)
		}
	}
}

// A document that shrinks loses the chunks past its new end, at every profile
// it shrank at, while the document itself stays.
func TestStorePruneRemovesTheChunksAShrunkDocumentNoLongerHas(t *testing.T) {
	root := t.TempDir()
	envelopes := storeFixtureEnvelopes()
	writeFixtureStore(t, root, envelopes)

	before, err := loadCorpusDocumentStore(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	target := envelopes[1].DocumentID
	beforeCount := len(before[target].Chunks)

	shrunk := append([]corpusEnvelope(nil), envelopes...)
	shrunk[1].Segments = []corpusSegment{
		{Role: segmentRoleSummary, Searchable: true, Text: "1. (S) SUMMARY: The node held. END SUMMARY."},
	}
	wantedDocuments, wantedChunks, _ := writeFixtureStore(t, root, shrunk)
	if _, err := pruneCorpusDocumentStore(root, wantedDocuments, wantedChunks); err != nil {
		t.Fatalf("prune: %v", err)
	}

	after, err := loadCorpusDocumentStore(root)
	if err != nil {
		t.Fatalf("load after prune: %v", err)
	}
	document, held := after[target]
	if !held {
		t.Fatalf("the shrunk document %s was removed entirely", target)
	}
	if len(document.Chunks) >= beforeCount {
		t.Errorf("the shrunk document holds %d chunks, was %d; the chunks past its"+
			" new end survived", len(document.Chunks), beforeCount)
	}
	for _, chunk := range document.Chunks {
		if !wantedChunks[chunk.ChunkID] {
			t.Errorf("orphaned chunk %s survived the prune", chunk.ChunkID)
		}
	}
}

// The index names each profile with its chunk count and how many of them carry
// a vector (srd017 R6.6, R7.2). The two differ because the embedding budget
// selects a subset, and an index reporting only the total leaves what was paid
// for to be recovered by counting records.
func TestStoreIndexReportsChunkAndVectorCountsPerProfile(t *testing.T) {
	root := t.TempDir()
	envelopes := storeFixtureEnvelopes()
	writeFixtureStore(t, root, envelopes)

	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	profiles := corpusStoreProfiles(documents, "test-identity")
	if len(profiles) != len(defaultChunkProfiles()) {
		t.Fatalf("the index names %d profiles, want %d",
			len(profiles), len(defaultChunkProfiles()))
	}
	for _, profile := range profiles {
		if profile.Chunks == 0 {
			t.Errorf("profile %s reports no chunks", profile.Profile)
		}
		if profile.Embedded != 0 {
			t.Errorf("profile %s reports %d embedded chunks in a store whose records"+
				" carry no vector", profile.Profile, profile.Embedded)
		}
	}

	// A vector on one chunk moves that profile's embedded count and no other's.
	target := documents[envelopes[0].DocumentID].chunksAtProfile(chunkProfileAtomic)[0]
	if _, err := writeCorpusDocumentChunkIfChanged(
		root, target.withVector("test-identity", []float64{0.1, 0.2})); err != nil {
		t.Fatalf("write an embedded chunk: %v", err)
	}
	documents, err = loadCorpusDocumentStore(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	for _, profile := range corpusStoreProfiles(documents, "test-identity") {
		want := 0
		if profile.Profile == chunkProfileAtomic {
			want = 1
		}
		if profile.Embedded != want {
			t.Errorf("profile %s reports %d embedded chunks, want %d",
				profile.Profile, profile.Embedded, want)
		}
	}
}

// A document directory holding chunks and no envelope is an error rather than a
// document with none: the envelope is what the serving path returns when an
// analyst opens a citation, and a chunk whose parent cannot be opened is the
// citation-leads-nowhere case lineage exists to prevent.
func TestStoreRejectsADocumentDirectoryWithNoEnvelope(t *testing.T) {
	root := t.TempDir()
	envelopes := storeFixtureEnvelopes()
	writeFixtureStore(t, root, envelopes)

	target := mustCorpusDocumentDir(t, root, envelopes[0].DocumentID)
	if err := os.Remove(filepath.Join(target, corpusEnvelopeRecordName)); err != nil {
		t.Fatalf("remove the envelope: %v", err)
	}
	if _, err := loadCorpusDocumentStore(root); err == nil {
		t.Fatal("a document directory carrying no envelope loaded without error")
	}
}

// A chunk carrying no parent block cannot be placed, and saying so is better
// than writing it somewhere the serving path will not look for it.
func TestStoreRefusesAChunkWithNoParentDocument(t *testing.T) {
	root := t.TempDir()
	orphan := corpusChunkRecord{ChunkID: "orphan-0001", Text: "no parent"}
	if _, err := writeCorpusDocumentChunkIfChanged(root, orphan); err == nil {
		t.Fatal("a chunk naming no parent document was written")
	}
}

// The store stays text a reviewer reads without running anything (srd015 R6.3,
// srd017 R7.5), with the lineage legible in the record rather than encoded.
func TestStoreRecordsStayLegibleText(t *testing.T) {
	root := t.TempDir()
	envelopes := storeFixtureEnvelopes()
	writeFixtureStore(t, root, envelopes)

	dir := mustCorpusDocumentDir(t, root, envelopes[1].DocumentID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the document directory: %v", err)
	}
	sawEnvelope := false
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		if entry.Name() == corpusEnvelopeRecordName {
			sawEnvelope = true
			if !strings.Contains(string(data), "document_id:") {
				t.Error("the envelope record does not read as YAML")
			}
			continue
		}
		text := string(data)
		for _, field := range []string{"chunk_id:", "chunk_profile:", "parent:", "char_span:"} {
			if !strings.Contains(text, field) {
				t.Errorf("chunk record %s carries no %s", entry.Name(), field)
			}
		}
	}
	if !sawEnvelope {
		t.Error("the document directory carries no envelope record")
	}
}

// The store built from the real datasets, over one month so the case runs in
// seconds rather than minutes. Skipped when the datasets are absent.
//
// Two properties are asserted that the fixtures cannot reach at this scale: the
// store holds exactly the documents the slice loader reported, and a rebuild
// over unchanged inputs writes nothing. Both failed on the real corpus before
// the news identifier carried its channel, and neither failed on the fixtures.
func TestStoreRealBuildIsCompleteAndIdempotentWhenTheDatasetsArePresent(t *testing.T) {
	dataRoot := strings.TrimSpace(os.Getenv("CORPUS_DATA_ROOT"))
	if dataRoot == "" {
		t.Skip("CORPUS_DATA_ROOT is unset; the source datasets live outside the repository")
	}
	root := t.TempDir()
	writeUkraineManifestFixture(t, root)

	slice := defaultCorpusSliceParameters()
	slice.WindowStart, slice.WindowEnd = "2022-10-01", "2022-10-31"
	slice.TelegramPerChannelPerDay = 1
	identity := corpusEmbeddingIdentity{
		identity: "test-identity",
		provider: "test",
		embed:    func(string) ([]float64, error) { return []float64{0.1, 0.2, 0.3}, nil },
	}

	report, sliceReport, err := buildUkraineCorpusStore(
		root, dataRoot, identity, slice, defaultChunkProfiles(), embedEverything())
	if err != nil {
		if strings.Contains(err.Error(), "publication date") {
			t.Skipf("the ISW dataset still carries the record GH-274 reports: %v", err)
		}
		t.Fatalf("build: %v", err)
	}

	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(documents) != sliceReport.total() {
		t.Errorf("the store holds %d documents and the slice reported %d;"+
			" documents collided on their identifiers and one overwrote another",
			len(documents), sliceReport.total())
	}
	index, err := readCorpusStoreIndex(root)
	if err != nil {
		t.Fatalf("read the index: %v", err)
	}
	if index.Chunks != report.Chunks {
		t.Errorf("the index counts %d chunks and the build cut %d",
			index.Chunks, report.Chunks)
	}
	if index.Documents != len(documents) {
		t.Errorf("the index counts %d documents and the store holds %d",
			index.Documents, len(documents))
	}
	if len(index.Profiles) != len(defaultChunkProfiles()) {
		t.Errorf("the index names %d profiles, want %d",
			len(index.Profiles), len(defaultChunkProfiles()))
	}

	rebuild, _, err := buildUkraineCorpusStore(
		root, dataRoot, identity, slice, defaultChunkProfiles(), embedEverything())
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if rebuild.Written != 0 {
		t.Errorf("a rebuild over unchanged inputs wrote %d records", rebuild.Written)
	}
	if len(rebuild.Removed) != 0 {
		t.Errorf("a rebuild over unchanged inputs pruned %d records", len(rebuild.Removed))
	}
	if rebuild.Embedded != 0 {
		t.Errorf("a rebuild over unchanged inputs issued %d embedding requests",
			rebuild.Embedded)
	}
	t.Logf("%d documents, %d chunks; rebuild wrote %d", len(documents), report.Chunks, rebuild.Written)
}

// writeUkraineManifestFixture writes a corpus manifest the rel10.5 build reads,
// so the real-data cases declare their own vocabularies rather than depending
// on the shipped manifest and failing whenever it is revised.
func writeUkraineManifestFixture(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, corpusDir), 0o755); err != nil {
		t.Fatalf("create the corpus directory: %v", err)
	}
	manifest := `id: ukraine-2022
version: "1.0"
title: Ukraine 2022 Corpus
synthetic: false
clearance_levels: [unclassified, restricted, confidential, secret]
clearance_markings:
  "UNCLASSIFIED//FOUO": unclassified
  "CONFIDENTIAL": confidential
  "SECRET": secret
  "SECRET//NOFORN": secret
clearance_caveats: [NOFORN]
tier_clearance:
  osint_news: unclassified
  osint_assessment: unclassified
  sigint_event: unclassified
  sigint_series: unclassified
tier_admiralty:
  osint_assessment: B2
  sigint_event: B2
  sigint_series: B3
  osint_news: C3
`
	path := filepath.Join(root, filepath.FromSlash(corpusManifestPath))
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatalf("write the manifest: %v", err)
	}
}

// mustCorpusDocumentDir resolves a document's directory or fails the test; the
// fixtures are publishable-tier documents, so resolution cannot need the
// private checkout.
func mustCorpusDocumentDir(t *testing.T, root, documentID string) string {
	t.Helper()
	dir, err := corpusDocumentDir(root, documentID)
	if err != nil {
		t.Fatalf("resolve document directory %s: %v", documentID, err)
	}
	return dir
}
