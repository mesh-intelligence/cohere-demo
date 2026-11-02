// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// The rel10.5 embed suite. It proves the half of test-suite case (h) that costs
// money: the ceiling is checked before the first request, a re-run embeds
// nothing, and widening the selection pays only for what it added.
//
// Every case here counts requests at a fake client rather than reading the
// store afterwards. That is the same reason rel10.4 counted them: a build that
// embedded the corpus and wrote identical records leaves a store
// indistinguishable from one that embedded nothing, and the difference is the
// invoice.

// testEmbedSelection is the shipped selection widened to the whole conversion
// window, for the cases about allowances, profiles, and the ceiling. They build
// fixtures at fixed dates and would otherwise fail whenever the shipped
// sub-window moved, which says nothing about what they assert.
func testEmbedSelection() corpusEmbedSelection {
	selection := defaultCorpusEmbedSelection()
	selection.WindowStart, selection.WindowEnd = sliceOuterStart, sliceOuterEnd
	return selection
}

// embedEverything selects every profile and gives every tier an allowance above
// anything a fixture produces, for the cases that are about the store rather
// than about the budget.
func embedEverything() corpusEmbedSelection {
	perTier := make(map[string]int, len(corpusSourceTypes()))
	for _, tier := range corpusSourceTypes() {
		perTier[tier] = corpusEmbedCeiling
	}
	profiles := make([]string, 0, len(defaultChunkProfiles()))
	for _, profile := range defaultChunkProfiles() {
		profiles = append(profiles, profile.Name)
	}
	return corpusEmbedSelection{
		WindowStart: sliceWindowStart,
		WindowEnd:   sliceWindowEnd,
		Profiles:    profiles,
		PerTier:     perTier,
		Ceiling:     corpusEmbedCeiling,
	}
}

// embedCandidates builds a corpus of documents in one tier, each cutting to one
// chunk at the named profile, so a case can size a selection exactly.
func embedCandidates(t *testing.T, tier string, count int, issued string) []corpusEmbedCandidate {
	t.Helper()
	manifest := ukraineTestManifest()
	candidates := make([]corpusEmbedCandidate, 0, count)
	for index := 0; index < count; index++ {
		envelope := corpusEnvelope{
			DocumentID:  fmt.Sprintf("%s-%04d", tier, index),
			SourceType:  tier,
			Clearance:   "unclassified",
			Issued:      issued,
			Origin:      "test",
			Reliability: "official_release",
			Provenance:  corpusProvenance{Collector: "test"},
			Title:       "Test document",
			RawRef:      corpusRawRef{Store: "test", ID: fmt.Sprintf("%d", index), Path: "test/test.csv"},
			Segments: []corpusSegment{{
				Role: segmentRoleBody, Searchable: true,
				Text: fmt.Sprintf("Document %d of the %s tier.", index, tier),
			}},
		}
		byProfile := chunkEnvelopeAtProfiles(envelope, defaultChunkProfiles(), manifest)
		records := make([]corpusChunkRecord, 0, len(defaultChunkProfiles()))
		for _, profile := range defaultChunkProfiles() {
			records = append(records, byProfile[profile.Name]...)
		}
		candidates = append(candidates, corpusEmbedCandidate{Envelope: envelope, Records: records})
	}
	return candidates
}

// Case (h), the ceiling half. A selection over the ceiling fails, names the
// profile and its count, and is reported from the plan rather than from the
// pass -- so no request was issued (srd017 R6.2, R6.3, rel10.5-uc001 S7).
func TestEmbedSelectionOverTheCeilingFailsBeforeAnyRequest(t *testing.T) {
	selection := testEmbedSelection()
	selection.PerTier = map[string]int{sourceTypeOSINTNews: 10_000}
	candidates := embedCandidates(t, sourceTypeOSINTNews, corpusEmbedCeiling+1, "2022-08-14")

	_, err := planCorpusEmbedding(candidates, selection)
	if err == nil {
		t.Fatal("a selection above the ceiling was planned without error")
	}
	if !strings.Contains(err.Error(), chunkProfileMedium) {
		t.Errorf("the error does not name the profile: %v", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d", corpusEmbedCeiling+1)) {
		t.Errorf("the error does not name the count: %v", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d", corpusEmbedCeiling)) {
		t.Errorf("the error does not name the ceiling: %v", err)
	}
}

// A selection at the ceiling exactly is allowed. The bound is inclusive, and a
// case asserting only the breach would pass against an implementation that
// refused the last chunk it should have taken.
func TestEmbedSelectionAtTheCeilingIsAllowed(t *testing.T) {
	selection := testEmbedSelection()
	selection.PerTier = map[string]int{sourceTypeOSINTNews: 10_000}
	candidates := embedCandidates(t, sourceTypeOSINTNews, corpusEmbedCeiling, "2022-08-14")

	plan, err := planCorpusEmbedding(candidates, selection)
	if err != nil {
		t.Fatalf("a selection at the ceiling was refused: %v", err)
	}
	if plan.count() != corpusEmbedCeiling {
		t.Errorf("the plan selected %d chunks, want the ceiling %d",
			plan.count(), corpusEmbedCeiling)
	}
}

// The per-tier allocation bounds each tier, so one tier cannot consume the
// budget the others were allocated.
func TestEmbedSelectionHoldsEachTierToItsAllowance(t *testing.T) {
	selection := testEmbedSelection()
	selection.PerTier = map[string]int{
		sourceTypeOSINTNews:       5,
		sourceTypeOSINTAssessment: 3,
	}
	candidates := append(
		embedCandidates(t, sourceTypeOSINTNews, 40, "2022-08-14"),
		embedCandidates(t, sourceTypeOSINTAssessment, 40, "2022-08-15")...)

	plan, err := planCorpusEmbedding(candidates, selection)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.PerTier[sourceTypeOSINTNews] != 5 {
		t.Errorf("the news tier took %d of an allowance of 5",
			plan.PerTier[sourceTypeOSINTNews])
	}
	if plan.PerTier[sourceTypeOSINTAssessment] != 3 {
		t.Errorf("the assessment tier took %d of an allowance of 3",
			plan.PerTier[sourceTypeOSINTAssessment])
	}
	if plan.count() != 8 {
		t.Errorf("the plan selected %d chunks, want 8", plan.count())
	}
}

// A tier the selection does not budget for contributes nothing rather than
// everything. An undeclared allowance is an authoring omission, and defaulting
// it to unlimited is the direction that costs money.
func TestEmbedSelectionGivesAnUndeclaredTierNothing(t *testing.T) {
	selection := testEmbedSelection()
	selection.PerTier = map[string]int{sourceTypeOSINTNews: 5}
	candidates := append(
		embedCandidates(t, sourceTypeOSINTNews, 10, "2022-08-14"),
		embedCandidates(t, sourceTypeSIGINTEvent, 10, "2022-08-15")...)

	plan, err := planCorpusEmbedding(candidates, selection)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.PerTier[sourceTypeSIGINTEvent] != 0 {
		t.Errorf("an unbudgeted tier took %d chunks of the budget",
			plan.PerTier[sourceTypeSIGINTEvent])
	}
}

// Only the declared profiles are embedded, so the three profiles kept for
// reading by identifier cost nothing (srd017 R6.1, R8.2).
func TestEmbedSelectionEmbedsOnlyTheDeclaredProfiles(t *testing.T) {
	selection := testEmbedSelection()
	selection.PerTier = map[string]int{sourceTypeOSINTNews: 100}
	candidates := embedCandidates(t, sourceTypeOSINTNews, 10, "2022-08-14")

	plan, err := planCorpusEmbedding(candidates, selection)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	for profile, count := range plan.PerProfile {
		if !selection.embedsProfile(profile) {
			t.Errorf("profile %s was selected with %d chunks though the selection"+
				" does not declare it", profile, count)
		}
	}
	if plan.PerProfile[chunkProfileMedium] == 0 {
		t.Error("the declared retrieval profile selected nothing")
	}
	if plan.PerProfile[chunkProfileAtomic] != 0 {
		t.Errorf("an undeclared profile selected %d chunks",
			plan.PerProfile[chunkProfileAtomic])
	}
}

// The embed sub-window may be narrower than the conversion window, which is how
// the corpus holds twelve months of citable documents while paying to search a
// shorter span (srd017 R3.3, R6.1).
func TestEmbedSelectionWindowMayBeNarrowerThanTheConversionWindow(t *testing.T) {
	selection := defaultCorpusEmbedSelection()
	selection.WindowStart, selection.WindowEnd = "2022-10-01", "2022-10-31"
	selection.PerTier = map[string]int{sourceTypeOSINTNews: 100}
	candidates := append(
		embedCandidates(t, sourceTypeOSINTNews, 5, "2022-10-15"),
		embedCandidates(t, sourceTypeOSINTNews, 5, "2023-01-15")...)

	plan, err := planCorpusEmbedding(candidates, selection)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.count() != 5 {
		t.Errorf("the plan selected %d chunks; only the 5 inside the embed"+
			" sub-window should have been selected", plan.count())
	}
}

// A tier declared full-window is embedded across the whole conversion window
// rather than the sub-window.
//
// This is what keeps the clearance demonstration working. The synthetic tier
// holds 27 reports across the year and is the only tier carrying more than one
// clearance level; a single month of it would leave the rel11.0 two-level
// assertion with one level and nothing to withhold.
func TestAFullWindowTierIgnoresTheEmbedSubWindow(t *testing.T) {
	selection := defaultCorpusEmbedSelection()
	selection.WindowStart, selection.WindowEnd = "2022-12-01", "2022-12-31"
	selection.FullWindowTiers = []string{sourceTypeHumint}
	selection.PerTier = map[string]int{
		sourceTypeHumint:    100,
		sourceTypeOSINTNews: 100,
	}
	// Both tiers carry documents inside and outside the sub-window.
	candidates := append(
		embedCandidates(t, sourceTypeHumint, 3, "2022-08-14"),
		embedCandidates(t, sourceTypeHumint, 2, "2022-12-14")...)
	candidates = append(candidates,
		embedCandidates(t, sourceTypeOSINTNews, 3, "2022-08-14")...)
	candidates = append(candidates,
		embedCandidates(t, sourceTypeOSINTNews, 2, "2022-12-14")...)

	plan, err := planCorpusEmbedding(candidates, selection)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.PerTier[sourceTypeHumint] != 5 {
		t.Errorf("the full-window tier selected %d of its 5 documents; the"+
			" sub-window was applied to a tier exempt from it",
			plan.PerTier[sourceTypeHumint])
	}
	if plan.PerTier[sourceTypeOSINTNews] != 2 {
		t.Errorf("a tier not declared full-window selected %d documents, want the"+
			" 2 inside the sub-window", plan.PerTier[sourceTypeOSINTNews])
	}
}

// The shipped selection exempts the synthetic tier and no other. Exempting a
// bulky tier would put the store back over the size the repository can carry,
// which is the constraint the sub-window exists to satisfy.
func TestOnlyTheSyntheticTierSpansTheFullWindow(t *testing.T) {
	selection := defaultCorpusEmbedSelection()
	if !selection.spansFullWindow(sourceTypeHumint) {
		t.Error("the synthetic tier is not exempt from the embed sub-window, so a" +
			" single month of it leaves the two-level assertion one level")
	}
	for _, tier := range []string{
		sourceTypeOSINTAssessment, sourceTypeOSINTNews,
		sourceTypeSIGINTEvent, sourceTypeSIGINTSeries,
	} {
		if selection.spansFullWindow(tier) {
			t.Errorf("tier %s spans the full window; the committed store is sized"+
				" by the sub-window and this puts it back over", tier)
		}
	}
	// The sub-window and the conversion window coincide: the store's weight
	// narrowed conversion to the same month the selection already paid for
	// (srd017 R3.1, R3.3). The seam is kept because they are separate bounds,
	// and what matters is that both are inside the span the tiers overlap over.
	if selection.WindowStart < sliceOuterStart || selection.WindowEnd > sliceOuterEnd {
		t.Errorf("the embed sub-window %s..%s falls outside the bounds the tiers"+
			" overlap over", selection.WindowStart, selection.WindowEnd)
	}
}

// The plan is deterministic, so two runs select the same chunks and a re-run
// embeds nothing.
func TestEmbedSelectionIsDeterministic(t *testing.T) {
	selection := testEmbedSelection()
	selection.PerTier = map[string]int{sourceTypeOSINTNews: 7}
	candidates := embedCandidates(t, sourceTypeOSINTNews, 30, "2022-08-14")

	first, err := planCorpusEmbedding(candidates, selection)
	if err != nil {
		t.Fatalf("first plan: %v", err)
	}
	second, err := planCorpusEmbedding(candidates, selection)
	if err != nil {
		t.Fatalf("second plan: %v", err)
	}
	if len(first.Selected) != len(second.Selected) {
		t.Fatalf("two plans selected %d and %d chunks",
			len(first.Selected), len(second.Selected))
	}
	for id := range first.Selected {
		if !second.Selected[id] {
			t.Errorf("chunk %s was selected by one plan and not the other", id)
		}
	}
}

// buildEmbedFixture writes a manifest and a small dataset root, returning both
// roots so a case can build over them.
func buildEmbedFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	prepareSplitSibling(t, root)
	writeUkraineManifestFixture(t, root)
	return root, writeSliceFixture(t)
}

// The pass embeds the selected chunks and no others, counted at the client
// (srd017 R6.4, rel10.5-uc001 S7).
func TestEmbedPassRequestsVectorsOnlyForSelectedChunks(t *testing.T) {
	root, dataRoot := buildEmbedFixture(t)
	client := &countingEmbedder{}
	selection := embedEverything()
	selection.Profiles = []string{chunkProfileMedium}

	report, _, err := buildUkraineCorpusStore(
		root, dataRoot, client.identity("test-identity"),
		defaultCorpusSliceParameters(), defaultChunkProfiles(), selection)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if client.calls == 0 {
		t.Fatal("the build issued no embedding request")
	}
	// A passage carried by more than one chunk is embedded once, so the
	// requests the client saw plus the ones the cache served account for every
	// chunk that was given a vector.
	if client.calls+report.Deduplicated != report.Embedded {
		t.Errorf("the client saw %d requests and the cache served %d, which does"+
			" not account for %d embedded chunks",
			client.calls, report.Deduplicated, report.Embedded)
	}

	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	embedded, unembedded := 0, 0
	for _, document := range documents {
		for _, chunk := range document.Chunks {
			_, held := chunk.vector("test-identity")
			if chunk.ChunkProfile == chunkProfileMedium {
				if !held {
					t.Errorf("selected chunk %s carries no vector", chunk.ChunkID)
				}
				embedded++
				continue
			}
			if held {
				t.Errorf("unselected chunk %s at profile %s carries a vector",
					chunk.ChunkID, chunk.ChunkProfile)
			}
			unembedded++
		}
	}
	if embedded == 0 || unembedded == 0 {
		t.Fatalf("the fixture embedded %d chunks and left %d unembedded;"+
			" the case proves nothing unless it did both", embedded, unembedded)
	}
	if client.calls+report.Deduplicated != embedded {
		t.Errorf("the client saw %d requests and the cache served %d, for %d"+
			" embedded chunks", client.calls, report.Deduplicated, embedded)
	}
	if client.calls > embedded {
		t.Errorf("the client saw %d requests for %d embedded chunks",
			client.calls, embedded)
	}
}

// Case (h), the re-run half. A build over unchanged inputs at an unchanged
// selection issues no embedding request (srd017 R6.5).
func TestEmbedPassOverUnchangedInputIssuesNoRequest(t *testing.T) {
	root, dataRoot := buildEmbedFixture(t)
	selection := embedEverything()
	selection.Profiles = []string{chunkProfileMedium}

	first := &countingEmbedder{}
	if _, _, err := buildUkraineCorpusStore(root, dataRoot, first.identity("test-identity"),
		defaultCorpusSliceParameters(), defaultChunkProfiles(), selection); err != nil {
		t.Fatalf("first build: %v", err)
	}
	if first.calls == 0 {
		t.Fatal("the first build embedded nothing")
	}

	second := &countingEmbedder{}
	report, _, err := buildUkraineCorpusStore(root, dataRoot, second.identity("test-identity"),
		defaultCorpusSliceParameters(), defaultChunkProfiles(), selection)
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if second.calls != 0 {
		t.Errorf("a rebuild over unchanged input issued %d embedding requests",
			second.calls)
	}
	if report.Written != 0 {
		t.Errorf("a rebuild over unchanged input wrote %d records", report.Written)
	}
}

// Widening the selection pays for the chunks it adds and not for the ones it
// already held, which is what makes extending the demonstration toward the full
// window a delta cost rather than a rebuild (srd017 R6.5).
func TestWideningTheSelectionEmbedsOnlyTheDelta(t *testing.T) {
	root, dataRoot := buildEmbedFixture(t)

	narrow := embedEverything()
	narrow.Profiles = []string{chunkProfileMedium}
	narrow.PerTier = map[string]int{sourceTypeOSINTAssessment: 1}

	first := &countingEmbedder{}
	if _, _, err := buildUkraineCorpusStore(root, dataRoot, first.identity("test-identity"),
		defaultCorpusSliceParameters(), defaultChunkProfiles(), narrow); err != nil {
		t.Fatalf("narrow build: %v", err)
	}
	if first.calls == 0 {
		t.Fatal("the narrow build embedded nothing")
	}

	wide := narrow
	wide.PerTier = map[string]int{
		sourceTypeOSINTAssessment: 100,
		sourceTypeHumint:          100,
		sourceTypeOSINTNews:       100,
	}
	second := &countingEmbedder{}
	if _, _, err := buildUkraineCorpusStore(root, dataRoot, second.identity("test-identity"),
		defaultCorpusSliceParameters(), defaultChunkProfiles(), wide); err != nil {
		t.Fatalf("wide build: %v", err)
	}
	if second.calls == 0 {
		t.Fatal("widening the selection embedded nothing")
	}
	for _, text := range second.texts {
		for _, already := range first.texts {
			if text == already {
				t.Errorf("widening re-embedded a passage the store already held: %q",
					text[:min(len(text), 60)])
			}
		}
	}
}

// Narrowing the selection removes the vectors it no longer reaches (srd017
// R6.4): the seed writes the selection alone into the retrieval collection, so
// a kept vector is repository weight nothing serves. The removal issues no
// request, and widening again re-embeds exactly what it re-adds -- which is
// R6.5 doing what it always did over a store that now holds less.
func TestNarrowingTheSelectionPrunesVectorsOutsideIt(t *testing.T) {
	root, dataRoot := buildEmbedFixture(t)
	wide := embedEverything()
	wide.Profiles = []string{chunkProfileMedium}

	first := &countingEmbedder{}
	if _, _, err := buildUkraineCorpusStore(root, dataRoot, first.identity("test-identity"),
		defaultCorpusSliceParameters(), defaultChunkProfiles(), wide); err != nil {
		t.Fatalf("wide build: %v", err)
	}

	narrow := wide
	narrow.PerTier = map[string]int{sourceTypeOSINTAssessment: 1}
	second := &countingEmbedder{}
	if _, _, err := buildUkraineCorpusStore(root, dataRoot, second.identity("test-identity"),
		defaultCorpusSliceParameters(), defaultChunkProfiles(), narrow); err != nil {
		t.Fatalf("narrow build: %v", err)
	}
	if second.calls != 0 {
		t.Errorf("narrowing the selection issued %d embedding requests; removal"+
			" costs nothing at the provider", second.calls)
	}

	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	kept := 0
	for _, document := range documents {
		for _, chunk := range document.Chunks {
			if _, held := chunk.vector("test-identity"); held {
				kept++
			}
		}
	}
	if kept != 1 {
		t.Errorf("the narrowed store carries %d vectors, want exactly the"+
			" one-chunk selection; a vector outside the selection is weight"+
			" nothing serves", kept)
	}

	// Widening back re-embeds what pruning removed and nothing else: the
	// re-added chunks cost requests, the still-held chunk costs none.
	third := &countingEmbedder{}
	if _, _, err := buildUkraineCorpusStore(root, dataRoot, third.identity("test-identity"),
		defaultCorpusSliceParameters(), defaultChunkProfiles(), wide); err != nil {
		t.Fatalf("re-widened build: %v", err)
	}
	if third.calls != first.calls-1 {
		t.Errorf("re-widening issued %d requests, want %d -- the pruned chunks"+
			" and only the pruned chunks", third.calls, first.calls-1)
	}
}

// Editing a document re-embeds the passages that changed and no others, because
// the identity that decides it is the text hash (srd015 R3.3, srd017 R5.5).
func TestEditingADocumentReEmbedsOnlyTheTouchedChunks(t *testing.T) {
	root := t.TempDir()
	prepareSplitSibling(t, root)
	writeUkraineManifestFixture(t, root)
	dataRoot := writeSliceFixture(t)
	selection := embedEverything()
	selection.Profiles = []string{chunkProfileMedium}

	first := &countingEmbedder{}
	if _, _, err := buildUkraineCorpusStore(root, dataRoot, first.identity("test-identity"),
		defaultCorpusSliceParameters(), defaultChunkProfiles(), selection); err != nil {
		t.Fatalf("first build: %v", err)
	}

	// Rewrite one HUMINT report's prose in the dataset.
	humint := []string{
		humintFixtureOn("HR-OUT-EARLY", "2022-06-30"),
		strings.Replace(humintFixtureOn("HR-IN-OPEN", sliceWindowStart),
			"The rail node remained in use.", "The rail node was destroyed.", 1),
		humintFixtureOn("HR-IN-CLOSE", sliceWindowEnd),
		humintFixtureOn("HR-OUT-LATE", "2023-07-01"),
	}
	if err := os.WriteFile(dataRoot+"/"+humintDatasetPath,
		[]byte(strings.Join(humint, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("rewrite the humint dataset: %v", err)
	}

	second := &countingEmbedder{}
	if _, _, err := buildUkraineCorpusStore(root, dataRoot, second.identity("test-identity"),
		defaultCorpusSliceParameters(), defaultChunkProfiles(), selection); err != nil {
		t.Fatalf("second build: %v", err)
	}
	if second.calls == 0 {
		t.Fatal("editing a document embedded nothing")
	}
	if second.calls > 2 {
		t.Errorf("editing one document re-embedded %d chunks", second.calls)
	}
	for _, text := range second.texts {
		if !strings.Contains(text, "destroyed") {
			t.Errorf("editing one document re-embedded an untouched passage: %q", text)
		}
	}
}

// The store records the selection that decided what was paid for, so the store
// says what it cost rather than leaving it to be counted (srd017 R6.1, R6.6).
func TestStoreIndexRecordsTheEmbedSelectionAndItsCounts(t *testing.T) {
	root, dataRoot := buildEmbedFixture(t)
	selection := embedEverything()
	selection.Profiles = []string{chunkProfileMedium}

	client := &countingEmbedder{}
	if _, _, err := buildUkraineCorpusStore(root, dataRoot, client.identity("test-identity"),
		defaultCorpusSliceParameters(), defaultChunkProfiles(), selection); err != nil {
		t.Fatalf("build: %v", err)
	}
	index, err := readCorpusStoreIndex(root)
	if err != nil {
		t.Fatalf("read the index: %v", err)
	}
	if index.EmbedSelection == nil {
		t.Fatal("the index records no embed selection")
	}
	if index.EmbedSelection.Ceiling != corpusEmbedCeiling {
		t.Errorf("the index records a ceiling of %d", index.EmbedSelection.Ceiling)
	}
	for _, profile := range index.Profiles {
		if profile.Embedded > profile.Chunks {
			t.Errorf("profile %s reports %d embedded of %d chunks",
				profile.Profile, profile.Embedded, profile.Chunks)
		}
		if profile.Embedded > corpusEmbedCeiling {
			t.Errorf("profile %s reports %d embedded chunks, above the ceiling %d",
				profile.Profile, profile.Embedded, corpusEmbedCeiling)
		}
		if profile.Profile == chunkProfileMedium && profile.Embedded == 0 {
			t.Error("the retrieval profile reports no embedded chunks")
		}
		if profile.Profile != chunkProfileMedium && profile.Embedded != 0 {
			t.Errorf("unselected profile %s reports %d embedded chunks",
				profile.Profile, profile.Embedded)
		}
	}
}

// The allocation the demonstration runs at fits the ceiling. A default that did
// not would fail every build until someone noticed.
func TestDefaultEmbedSelectionFitsTheCeiling(t *testing.T) {
	selection := defaultCorpusEmbedSelection()
	total := 0
	for _, allowance := range selection.PerTier {
		total += allowance
	}
	if total > selection.Ceiling {
		t.Errorf("the declared per-tier allowances total %d, above the ceiling %d",
			total, selection.Ceiling)
	}
	for _, tier := range corpusSourceTypes() {
		if _, declared := selection.PerTier[tier]; !declared {
			t.Errorf("the default selection declares no allowance for tier %s,"+
				" so that tier contributes nothing to retrieval", tier)
		}
	}
	if len(selection.Profiles) != 1 || selection.Profiles[0] != chunkProfileMedium {
		t.Errorf("the default selection embeds %v; embedding a profile nothing"+
			" searches multiplies the bill for no retrieval", selection.Profiles)
	}
}

// The declared allocation against the real corpus. This is the case that says
// what the demonstration costs: the plan is built over the full twelve-month
// slice and its per-profile totals are held to the ceiling, with the provider
// uncalled throughout. Skipped when the datasets are absent.
func TestEmbedPlanOverTheRealCorpusFitsTheCeilingWhenPresent(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("CORPUS_DATA_ROOT"))
	if root == "" {
		t.Skip("CORPUS_DATA_ROOT is unset; the source datasets live outside the repository")
	}
	manifest := ukraineTestManifest()
	envelopes, _, err := loadCorpusSlice(root, defaultCorpusSliceParameters(),
		ukraineTestClearanceMapping(), manifest)
	if err != nil {
		if strings.Contains(err.Error(), "publication date") {
			t.Skipf("the ISW dataset still carries the record GH-274 reports: %v", err)
		}
		t.Fatalf("load the real slice: %v", err)
	}
	candidates := make([]corpusEmbedCandidate, 0, len(envelopes))
	for _, envelope := range envelopes {
		byProfile := chunkEnvelopeAtProfiles(envelope, defaultChunkProfiles(), manifest)
		records := make([]corpusChunkRecord, 0, len(defaultChunkProfiles()))
		for _, profile := range defaultChunkProfiles() {
			records = append(records, byProfile[profile.Name]...)
		}
		candidates = append(candidates, corpusEmbedCandidate{Envelope: envelope, Records: records})
	}

	selection := defaultCorpusEmbedSelection()
	plan, err := planCorpusEmbedding(candidates, selection)
	if err != nil {
		t.Fatalf("the declared allocation does not fit the real corpus: %v", err)
	}
	for profile, count := range plan.PerProfile {
		if count > selection.Ceiling {
			t.Errorf("profile %s selected %d chunks, above the ceiling %d",
				profile, count, selection.Ceiling)
		}
	}
	if plan.count() == 0 {
		t.Fatal("the declared allocation selected nothing from the real corpus")
	}
	t.Log(describeEmbedPlan(plan, selection))
	t.Logf("the demonstration pays for %d of %d chunks considered at the declared profiles",
		plan.count(), plan.Considered)
}

// A passage carried by more than one chunk is embedded once (GH-280). The
// vectors the sharing chunks receive are identical, because a vector is a pure
// function of the text and the model and the second request would buy nothing.
func TestIdenticalPassagesAreEmbeddedOnce(t *testing.T) {
	client := &countingEmbedder{}
	identity, memo := memoizeEmbedder(client.identity("test-identity"))

	const passage = "Russian forces conducted limited ground attacks near the settlement."
	first, err := identity.embed(passage)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	second, err := identity.embed(passage)
	if err != nil {
		t.Fatalf("embed again: %v", err)
	}
	if client.calls != 1 {
		t.Errorf("the same passage was sent to the provider %d times", client.calls)
	}
	if memo.requestsSaved() != 1 {
		t.Errorf("the cache reports %d saved requests, want 1", memo.requestsSaved())
	}
	if len(first) != len(second) {
		t.Fatalf("the two vectors are %d and %d values", len(first), len(second))
	}
	for index := range first {
		if first[index] != second[index] {
			t.Errorf("the cached vector differs at %d: %v against %v",
				index, first[index], second[index])
		}
	}

	// A different passage still reaches the provider.
	if _, err := identity.embed(passage + " And another sentence."); err != nil {
		t.Fatalf("embed a different passage: %v", err)
	}
	if client.calls != 2 {
		t.Errorf("a different passage did not reach the provider: %d calls", client.calls)
	}
}

// The cache hands out copies, so a caller that mutates the vector it was given
// cannot corrupt what every later chunk receives.
func TestTheEmbedCacheHandsOutCopies(t *testing.T) {
	client := &countingEmbedder{}
	identity, _ := memoizeEmbedder(client.identity("test-identity"))

	const passage = "A passage carried by two chunks."
	first, err := identity.embed(passage)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("the fake embedder returned an empty vector")
	}
	first[0] = 999
	second, err := identity.embed(passage)
	if err != nil {
		t.Fatalf("embed again: %v", err)
	}
	if second[0] == 999 {
		t.Error("mutating a returned vector corrupted the cached one")
	}
}

// A provider failure is not cached, so the build fails on it rather than
// serving the failure to every chunk sharing the passage.
func TestTheEmbedCacheDoesNotCacheAFailure(t *testing.T) {
	client := &countingEmbedder{failOn: "rejected"}
	identity, _ := memoizeEmbedder(client.identity("test-identity"))

	if _, err := identity.embed("a rejected passage"); err == nil {
		t.Fatal("the fake embedder was expected to reject this text")
	}
	if _, err := identity.embed("a rejected passage"); err == nil {
		t.Fatal("the second attempt was served from the cache rather than failing")
	}
	if client.calls != 2 {
		t.Errorf("a failure was cached: the provider saw %d calls", client.calls)
	}
}

// A corpus holding no duplicate passage is embedded exactly as it was before
// the cache existed, so this is a saving rather than a change in behaviour.
func TestACorpusWithNoDuplicatePassagesIsUnaffected(t *testing.T) {
	client := &countingEmbedder{}
	identity, memo := memoizeEmbedder(client.identity("test-identity"))
	for index := 0; index < 20; index++ {
		if _, err := identity.embed(fmt.Sprintf("passage number %d", index)); err != nil {
			t.Fatalf("embed %d: %v", index, err)
		}
	}
	if client.calls != 20 {
		t.Errorf("20 distinct passages produced %d provider calls", client.calls)
	}
	if memo.requestsSaved() != 0 {
		t.Errorf("the cache saved %d requests over distinct passages",
			memo.requestsSaved())
	}
}

// The duplicate rate over the real corpus, which is what says whether the cache
// is worth having. Skipped when the datasets are absent.
func TestEmbedDuplicateRateOverTheRealCorpusWhenPresent(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("CORPUS_DATA_ROOT"))
	if root == "" {
		t.Skip("CORPUS_DATA_ROOT is unset; the source datasets live outside the repository")
	}
	manifest := ukraineTestManifest()
	envelopes, _, err := loadCorpusSlice(root, defaultCorpusSliceParameters(),
		ukraineTestClearanceMapping(), manifest)
	if err != nil {
		if strings.Contains(err.Error(), "publication date") {
			t.Skipf("the ISW dataset still carries the record GH-274 reports: %v", err)
		}
		t.Fatalf("load the real slice: %v", err)
	}
	candidates := make([]corpusEmbedCandidate, 0, len(envelopes))
	byID := make(map[string]corpusChunkRecord)
	for _, envelope := range envelopes {
		byProfile := chunkEnvelopeAtProfiles(envelope, defaultChunkProfiles(), manifest)
		records := make([]corpusChunkRecord, 0, len(defaultChunkProfiles()))
		for _, profile := range defaultChunkProfiles() {
			records = append(records, byProfile[profile.Name]...)
		}
		for _, record := range records {
			byID[record.ChunkID] = record
		}
		candidates = append(candidates, corpusEmbedCandidate{Envelope: envelope, Records: records})
	}
	plan, err := planCorpusEmbedding(candidates, defaultCorpusEmbedSelection())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	digests := make(map[string]int, plan.count())
	for id := range plan.Selected {
		digests[byID[id].TextSHA256]++
	}
	duplicates := 0
	for _, count := range digests {
		duplicates += count - 1
	}
	t.Logf("the declared selection holds %d chunks carrying %d distinct passages;"+
		" the cache saves %d provider calls (%.1f%%)",
		plan.count(), len(digests), duplicates,
		100*float64(duplicates)/float64(plan.count()))
	if len(digests) > plan.count() {
		t.Errorf("counted %d distinct passages among %d chunks", len(digests), plan.count())
	}
}
