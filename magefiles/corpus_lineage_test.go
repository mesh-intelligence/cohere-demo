// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"strings"
	"testing"
)

// The rel10.5 lineage suite. It proves test-suite cases (e) and (f) -- that
// every chunk carries a complete and internally consistent lineage block, and
// that cross-tier provenance resolves from a derived document to the reporting
// it was built from.
//
// What lineage buys is the difference between a citation and a reference. A
// brief citing chunk isw-2022-08-14-medium-900-0007 has satisfied the letter of
// "every assertion traceable" and given the analyst a string. With the parent
// block that string resolves to the passage, the assessment it was cut from,
// and the character range to highlight inside it.

func lineageFixtureEnvelopes() []corpusEnvelope {
	assessment := profileFixtureEnvelope()
	report := corpusEnvelope{
		DocumentID:  "HR-22-014",
		SourceType:  sourceTypeHumint,
		Clearance:   "secret",
		Caveats:     []string{"NOFORN"},
		Issued:      "2022-08-14",
		Origin:      "GRANITE",
		Reliability: "B3",
		Provenance: corpusProvenance{
			Collector: "DEFTECH STATION KYIV",
			DerivedFrom: []corpusDerivedRef{{
				Tier:     sourceTypeOSINTNews,
				ID:       "23052",
				Relation: derivedRelationWrittenOver,
			}},
		},
		Title:     "Rail node status",
		Synthetic: true,
		RawRef:    corpusRawRef{Store: "humint", ID: "HR-22-014", Path: humintDatasetPath},
		Segments: []corpusSegment{
			{Role: segmentRoleAuthority, Searchable: false, Text: "Classified by: DEFTECH/DDI."},
			{Role: segmentRoleSummary, Searchable: true, Text: "1. (S) SUMMARY: The node held. END SUMMARY."},
			{Role: segmentRoleBody, Searchable: true, Text: "2. (S) Dispatcher traffic showed no interruption."},
			{Role: segmentRoleComment, Searchable: true, Text: "3. (S) FIELD COMMENT: Consistent with public reporting."},
		},
	}
	message := corpusEnvelope{
		DocumentID:  "tg-23052",
		SourceType:  sourceTypeOSINTNews,
		Clearance:   "unclassified",
		Issued:      "2022-08-13",
		Origin:      "BBC Russia",
		Reliability: "unrated_open_source",
		Provenance:  corpusProvenance{Collector: "BBC Russia"},
		Title:       "Rail traffic report",
		RawRef:      corpusRawRef{Store: "ukraine_news", ID: "23052", Path: telegramDatasetPath},
		Segments: []corpusSegment{
			{Role: segmentRoleBody, Searchable: true, Text: "Rail traffic through the node continued overnight."},
		},
	}
	return []corpusEnvelope{assessment, report, message}
}

// Case (e). Every chunk at every profile carries a complete lineage block whose
// parts agree with each other (srd017 R5.1, R5.6, rel10.5-uc001 S6).
func TestLineageIsPresentAndInternallyConsistent(t *testing.T) {
	manifest := ukraineTestManifest()
	records := chunkEnvelopesAtProfiles(
		lineageFixtureEnvelopes(), defaultChunkProfiles(), manifest)
	if len(records) == 0 {
		t.Fatal("the fixture produced no records")
	}
	byID := make(map[string]corpusChunkRecord, len(records))
	for _, record := range records {
		byID[record.ChunkID] = record
	}

	for _, record := range records {
		parent := record.Parent
		if parent == nil {
			t.Errorf("chunk %s carries no parent block", record.ChunkID)
			continue
		}
		if parent.DocumentID == "" {
			t.Errorf("chunk %s names no parent document", record.ChunkID)
		}
		if parent.RawRef.Store == "" || parent.RawRef.ID == "" {
			t.Errorf("chunk %s carries an incomplete raw reference %+v",
				record.ChunkID, parent.RawRef)
		}
		if parent.Position < 1 || parent.Position > parent.SiblingCount {
			t.Errorf("chunk %s is at position %d of %d siblings",
				record.ChunkID, parent.Position, parent.SiblingCount)
		}
		if len(parent.CharSpan) != 2 {
			t.Errorf("chunk %s carries a char span of %d values",
				record.ChunkID, len(parent.CharSpan))
			continue
		}
		if parent.CharSpan[0] < 0 || parent.CharSpan[1] < parent.CharSpan[0] {
			t.Errorf("chunk %s carries the span %v, which does not run forwards",
				record.ChunkID, parent.CharSpan)
		}
	}
}

// Case (e), the neighbour half. Previous and next resolve within the same
// profile of the same document, and the first and last chunk carry no reference
// rather than one that dangles (srd017 R5.2).
func TestLineageNeighboursResolveWithinTheProfile(t *testing.T) {
	manifest := ukraineTestManifest()
	records := chunkEnvelopesAtProfiles(
		lineageFixtureEnvelopes(), defaultChunkProfiles(), manifest)
	byID := make(map[string]corpusChunkRecord, len(records))
	for _, record := range records {
		byID[record.ChunkID] = record
	}

	for _, record := range records {
		parent := record.Parent
		for label, neighbour := range map[string]string{
			"prev": parent.PrevChunkID,
			"next": parent.NextChunkID,
		} {
			if neighbour == "" {
				continue
			}
			resolved, found := byID[neighbour]
			if !found {
				t.Errorf("chunk %s names a %s chunk %s that does not exist",
					record.ChunkID, label, neighbour)
				continue
			}
			if resolved.ChunkProfile != record.ChunkProfile {
				t.Errorf("chunk %s names a %s chunk cut at profile %s",
					record.ChunkID, label, resolved.ChunkProfile)
			}
			if resolved.Parent.DocumentID != parent.DocumentID {
				t.Errorf("chunk %s names a %s chunk of another document %s",
					record.ChunkID, label, resolved.Parent.DocumentID)
			}
		}
		if parent.Position == 1 && parent.PrevChunkID != "" {
			t.Errorf("the first chunk %s names a previous chunk %s",
				record.ChunkID, parent.PrevChunkID)
		}
		if parent.Position == parent.SiblingCount && parent.NextChunkID != "" {
			t.Errorf("the last chunk %s names a next chunk %s",
				record.ChunkID, parent.NextChunkID)
		}
	}
}

// Case (e), the version half. A chunk resolves to the coarser chunk covering
// the same passage, so an interface can widen from a sentence to the section it
// sits in (srd017 R5.3).
func TestLineageCoarserParentResolvesToAChunkCoveringTheSamePassage(t *testing.T) {
	manifest := ukraineTestManifest()
	records := chunkEnvelopesAtProfiles(
		lineageFixtureEnvelopes(), defaultChunkProfiles(), manifest)
	byID := make(map[string]corpusChunkRecord, len(records))
	for _, record := range records {
		byID[record.ChunkID] = record
	}

	linked := 0
	for _, record := range records {
		coarser := record.Parent.CoarserParent
		if coarser == "" {
			if coarserProfileOf(record.ChunkProfile) != "" {
				t.Errorf("chunk %s at profile %s names no coarser parent, though"+
					" %s coarsens to %s", record.ChunkID, record.ChunkProfile,
					record.ChunkProfile, coarserProfileOf(record.ChunkProfile))
			}
			continue
		}
		resolved, found := byID[coarser]
		if !found {
			t.Errorf("chunk %s names a coarser parent %s that does not exist",
				record.ChunkID, coarser)
			continue
		}
		linked++
		if resolved.ChunkProfile != coarserProfileOf(record.ChunkProfile) {
			t.Errorf("chunk %s at profile %s resolves to a parent at %s",
				record.ChunkID, record.ChunkProfile, resolved.ChunkProfile)
		}
		if resolved.Parent.DocumentID != record.Parent.DocumentID {
			t.Errorf("chunk %s resolves to a coarser parent of another document",
				record.ChunkID)
		}
		// The parent covers the passage, so the spans overlap.
		start, end := record.Parent.CharSpan[0], record.Parent.CharSpan[1]
		parentStart, parentEnd := resolved.Parent.CharSpan[0], resolved.Parent.CharSpan[1]
		if end <= parentStart || start >= parentEnd {
			t.Errorf("chunk %s spans %v and its coarser parent spans %v; they do"+
				" not overlap, so the link is not covering the same passage",
				record.ChunkID, record.Parent.CharSpan, resolved.Parent.CharSpan)
		}
	}
	if linked == 0 {
		t.Error("no chunk linked to a coarser parent, so the case proves nothing")
	}
}

// The coarsening chain runs from the smallest profile to the largest and stops
// there, so the largest names no coarser parent rather than one that does not
// exist.
func TestTheCoarseningChainEndsAtTheLargestProfile(t *testing.T) {
	if coarser := coarserProfileOf("a-profile-nobody-declared"); coarser != "" {
		t.Errorf("an undeclared profile coarsens to %q", coarser)
	}
	if coarser := coarserProfileOf(chunkProfileLarge); coarser != "" {
		t.Errorf("the largest profile coarsens to %q", coarser)
	}
	if coarserProfileOf(chunkProfileAtomic) != chunkProfileMedium {
		t.Error("atomic does not coarsen to medium-900")
	}
	if coarserProfileOf(chunkProfileMedium) != chunkProfileLarge {
		t.Error("medium-900 does not coarsen to large-1800")
	}
}

// Case (f). Cross-tier provenance travels onto every chunk of a derived
// document and resolves against the slice, so a HUMINT report reaches the
// public message it was written over (srd017 R5.4, rel10.5-uc001 S6).
func TestLineageCrossTierProvenanceResolvesAgainstTheSlice(t *testing.T) {
	manifest := ukraineTestManifest()
	envelopes := lineageFixtureEnvelopes()
	records := chunkEnvelopesAtProfiles(envelopes, defaultChunkProfiles(), manifest)

	documents := make(map[string]bool, len(envelopes))
	for _, envelope := range envelopes {
		documents[envelope.DocumentID] = true
	}

	checked := 0
	for _, record := range records {
		if record.Metadata.SourceType != sourceTypeHumint {
			continue
		}
		if record.Provenance == nil || len(record.Provenance.DerivedFrom) == 0 {
			t.Errorf("humint chunk %s carries no cross-tier provenance", record.ChunkID)
			continue
		}
		for _, derived := range record.Provenance.DerivedFrom {
			if derived.Tier != sourceTypeOSINTNews {
				t.Errorf("chunk %s names tier %q", record.ChunkID, derived.Tier)
			}
			if derived.Relation != derivedRelationWrittenOver {
				t.Errorf("chunk %s declares relation %q", record.ChunkID, derived.Relation)
			}
			// The reference resolves against the converted slice, in the
			// identifier form the news adapter mints.
			if !documents["tg-"+derived.ID] {
				t.Errorf("chunk %s names telegram message %s, which is not in the slice",
					record.ChunkID, derived.ID)
			}
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no humint chunk was checked, so the case proves nothing")
	}
}

// Lineage is metadata and never enters the embedded text (srd017 R5.5).
// Editing a document's prose changes the hash; changing what surrounds it does
// not, which is what keeps a rebuild from re-embedding the corpus every time it
// grows by one document.
func TestLineageDoesNotEnterTheEmbeddedText(t *testing.T) {
	manifest := ukraineTestManifest()
	profile, _ := chunkProfileNamed(chunkProfileAtomic)

	envelope := lineageFixtureEnvelopes()[1]
	before := chunkEnvelope(envelope, profile, manifest)
	if len(before) == 0 {
		t.Fatal("the fixture produced no chunk")
	}

	// Metadata around the passage changes; the passage does not.
	moved := envelope
	moved.Title = "A different title entirely"
	moved.Provenance = corpusProvenance{
		Collector: "ANOTHER STATION",
		DerivedFrom: []corpusDerivedRef{
			{Tier: sourceTypeOSINTNews, ID: "23052", Relation: derivedRelationWrittenOver},
			{Tier: sourceTypeOSINTNews, ID: "99999", Relation: derivedRelationWrittenOver},
		},
	}
	after := chunkEnvelope(moved, profile, manifest)
	if len(after) != len(before) {
		t.Fatalf("changing metadata changed the chunk count from %d to %d",
			len(before), len(after))
	}
	for index := range before {
		if before[index].TextSHA256 != after[index].TextSHA256 {
			t.Errorf("chunk %s changed hash when only metadata around it changed;"+
				" a rebuild would re-embed a passage nobody edited",
				before[index].ChunkID)
		}
		if !strings.Contains(after[index].Text, "SUMMARY") &&
			!strings.Contains(after[index].Text, "Dispatcher") &&
			!strings.Contains(after[index].Text, "COMMENT") {
			t.Errorf("chunk %s does not carry the passage it was cut from",
				after[index].ChunkID)
		}
		if strings.Contains(after[index].Text, "ANOTHER STATION") ||
			strings.Contains(after[index].Text, "23052") {
			t.Errorf("chunk %s carries lineage inside its embedded text",
				after[index].ChunkID)
		}
	}

	// Editing the prose does change the hash.
	edited := envelope
	edited.Segments = append([]corpusSegment(nil), envelope.Segments...)
	edited.Segments[1].Text = "1. (S) SUMMARY: The node was destroyed. END SUMMARY."
	rewritten := chunkEnvelope(edited, profile, manifest)
	if rewritten[0].TextSHA256 == before[0].TextSHA256 {
		t.Error("editing the passage left the hash unchanged, so a rebuild would" +
			" keep a vector describing prose the corpus no longer holds")
	}
}

// The character span locates the chunk inside the document's searchable body,
// so an interface highlights the passage rather than searching for the text.
func TestLineageCharSpanLocatesTheChunkInTheBody(t *testing.T) {
	manifest := ukraineTestManifest()
	envelope := lineageFixtureEnvelopes()[1]
	body := envelope.searchableText()

	for _, profileName := range []string{chunkProfileAtomic} {
		profile, _ := chunkProfileNamed(profileName)
		for _, record := range chunkEnvelope(envelope, profile, manifest) {
			span := record.Parent.CharSpan
			if span[1] > len(body) {
				t.Errorf("chunk %s spans to %d in a body of %d characters",
					record.ChunkID, span[1], len(body))
				continue
			}
			// The segment splitter cuts contiguous pieces, so the span holds
			// exactly the chunk's text.
			if got := body[span[0]:span[1]]; got != record.Text {
				t.Errorf("chunk %s spans %v, which holds %q rather than its text %q",
					record.ChunkID, span, got, record.Text)
			}
		}
	}
}

// Spans advance through the document rather than repeating, so a chunk whose
// text appears twice is located at its own occurrence.
func TestLineageSpansAdvanceThroughTheDocument(t *testing.T) {
	manifest := ukraineTestManifest()
	repeated := corpusEnvelope{
		DocumentID:  "tg-repeat",
		SourceType:  sourceTypeOSINTNews,
		Clearance:   "unclassified",
		Issued:      "2022-08-14",
		Origin:      "TASS",
		Reliability: "unrated_open_source",
		Provenance:  corpusProvenance{Collector: "TASS"},
		Title:       "Repeated lines",
		RawRef:      corpusRawRef{Store: "ukraine_news", ID: "repeat", Path: telegramDatasetPath},
		Segments: []corpusSegment{
			{Role: segmentRoleBody, Searchable: true, Text: "The same line."},
			{Role: segmentRoleBody, Searchable: true, Text: "The same line."},
			{Role: segmentRoleBody, Searchable: true, Text: "A different line."},
		},
	}
	profile, _ := chunkProfileNamed(chunkProfileAtomic)
	records := chunkEnvelope(repeated, profile, manifest)
	if len(records) != 3 {
		t.Fatalf("the fixture produced %d chunks, want 3", len(records))
	}
	first, second := records[0].Parent.CharSpan, records[1].Parent.CharSpan
	if second[0] <= first[0] {
		t.Errorf("the repeated line is located at %v and %v; the second occurrence"+
			" resolved to the first", first, second)
	}
}

// Lineage over the real corpus, where the documents are long enough that the
// coarsening chain has something to link. Skipped when the datasets are absent.
func TestLineageHoldsOverTheRealCorpusWhenPresent(t *testing.T) {
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
	// One assessment is enough to exercise the chain, and cutting the whole
	// corpus at four profiles to assert a property of one document is waste.
	var assessment corpusEnvelope
	for _, envelope := range envelopes {
		if envelope.SourceType == sourceTypeOSINTAssessment {
			assessment = envelope
			break
		}
	}
	if assessment.DocumentID == "" {
		t.Fatal("the slice holds no assessment")
	}

	byProfile := chunkEnvelopeAtProfiles(assessment, defaultChunkProfiles(), manifest)
	medium := byProfile[chunkProfileMedium]
	large := byProfile[chunkProfileLarge]
	if len(medium) < 2 || len(large) < 2 {
		t.Fatalf("a real assessment cut into %d medium and %d large chunks",
			len(medium), len(large))
	}
	largeByID := make(map[string]corpusChunkRecord, len(large))
	for _, record := range large {
		largeByID[record.ChunkID] = record
	}
	for _, record := range medium {
		coarser, found := largeByID[record.Parent.CoarserParent]
		if !found {
			t.Fatalf("medium chunk %s names a coarser parent %q that is not a"+
				" large chunk of the same document",
				record.ChunkID, record.Parent.CoarserParent)
		}
		if record.Parent.CharSpan[1] <= coarser.Parent.CharSpan[0] ||
			record.Parent.CharSpan[0] >= coarser.Parent.CharSpan[1] {
			t.Errorf("medium chunk %s spans %v and its parent spans %v",
				record.ChunkID, record.Parent.CharSpan, coarser.Parent.CharSpan)
		}
	}
	t.Logf("assessment %s: %d atomic, %d medium, %d large",
		assessment.DocumentID, len(byProfile[chunkProfileAtomic]), len(medium),
		len(large))
}
