// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// The rel10.5 profile suite. It proves test-suite case (d), the per-profile
// chunk counts, and the half of case (c) that belongs to the chunker rather
// than the adapters -- that only searchable segments reach a chunk.
//
// The assertion the release turns on is that medium-900 reproduces the rel10.4
// chunker character for character. Every vector in the committed store is keyed
// to the text that chunker wrote, so a profile that merely resembles it orphans
// all of them while the identity comparison still passes.

// profileFixtureEnvelope is a document with four searchable segments and two
// that are carried but never embedded, so the counts differ per profile and the
// exclusion has something to exclude.
func profileFixtureEnvelope() corpusEnvelope {
	paragraph := strings.Repeat("Ukrainian forces continued interdiction strikes. ", 12)
	return corpusEnvelope{
		DocumentID:  "isw-2022-08-14",
		SourceType:  sourceTypeOSINTAssessment,
		Clearance:   "unclassified",
		Issued:      "2022-08-14",
		Origin:      "Institute for the Study of War",
		Reliability: "published_assessment",
		Provenance:  corpusProvenance{Collector: "Institute for the Study of War"},
		Title:       "Russian Offensive Campaign Assessment",
		RawRef:      corpusRawRef{Store: "isw", ID: "assessment", Path: iswDatasetPath},
		Segments: []corpusSegment{
			{Role: segmentRoleByline, Searchable: false, Text: "Karolina Hird 8:30pm ET"},
			{Role: segmentRoleSummary, Searchable: true, Text: "# Key assessment\n\n" + paragraph},
			{Role: segmentRoleBody, Searchable: true, Text: paragraph},
			{Role: segmentRoleBody, Searchable: true, Text: "# Southern axis\n\n" + paragraph},
			{Role: segmentRoleBody, Searchable: true, Text: paragraph},
			{Role: segmentRoleEndnotes, Searchable: false, Text: "[1] https://t.me/mod_russia/23394"},
		},
	}
}

// Case (d), and the requirement the release turns on. The medium-900 profile
// produces exactly the text the rel10.4 chunker produces for the same body,
// compared across the two code paths rather than by calling one from the other
// (srd017 R4.3, rel10.5-uc001 S5).
func TestMediumProfileReproducesTheRel104ChunkerByteForByte(t *testing.T) {
	envelope := profileFixtureEnvelope()
	manifest := ukraineTestManifest()
	profile, found := chunkProfileNamed(chunkProfileMedium)
	if !found {
		t.Fatal("the medium-900 profile is not declared")
	}

	// The rel10.4 path: a document whose body is this envelope's searchable
	// text, cut by the chunker the committed store was written with.
	document := corpusDocument{
		Path:        "documents/isw.md",
		FrontMatter: corpusFrontMatter{SourceID: "isw-2022-08-14", Clearance: "unclassified"},
		Body:        envelope.searchableText(),
	}
	legacy := chunkCorpusDocument(document, manifest, defaultCorpusChunkParameters())
	current := chunkEnvelope(envelope, profile, manifest)

	if len(legacy) != len(current) {
		t.Fatalf("the rel10.4 chunker produced %d chunks and medium-900 produced %d",
			len(legacy), len(current))
	}
	for index := range legacy {
		if legacy[index].Text != current[index].Text {
			t.Errorf("chunk %d differs between the chunkers.\nrel10.4: %q\nmedium-900: %q",
				index+1, legacy[index].Text, current[index].Text)
		}
		if legacy[index].TextSHA256 != current[index].TextSHA256 {
			t.Errorf("chunk %d hashes differently, so its stored vectors would be"+
				" orphaned while the identity comparison still passed", index+1)
		}
	}
}

// Case (d). Each profile cuts the same document a different way, and the counts
// follow from what each profile is rather than from a number written down
// (srd017 R4.1).
func TestProfilesYieldTheirDeclaredChunkCounts(t *testing.T) {
	envelope := profileFixtureEnvelope()
	manifest := ukraineTestManifest()
	counts := make(map[string]int)
	for _, profile := range defaultChunkProfiles() {
		counts[profile.Name] = len(chunkEnvelope(envelope, profile, manifest))
	}

	searchableSegments := 0
	for _, segment := range envelope.Segments {
		if segment.Searchable {
			searchableSegments++
		}
	}
	if counts[chunkProfileAtomic] != searchableSegments {
		t.Errorf("atomic produced %d chunks for %d searchable segments",
			counts[chunkProfileAtomic], searchableSegments)
	}
	if counts[chunkProfileLarge] >= counts[chunkProfileMedium] {
		t.Errorf("large-1800 produced %d chunks and medium-900 produced %d;"+
			" the larger target cuts fewer",
			counts[chunkProfileLarge], counts[chunkProfileMedium])
	}
	if counts[chunkProfileMedium] < 2 {
		t.Fatalf("the fixture is too short to exercise the medium profile: %d chunks",
			counts[chunkProfileMedium])
	}
}

// Case (c), the chunker's half. Material the adapters marked unsearchable
// reaches no chunk at any profile (srd017 R4.2).
func TestNoProfileChunksAnUnsearchableSegment(t *testing.T) {
	envelope := profileFixtureEnvelope()
	manifest := ukraineTestManifest()
	excluded := []string{"Karolina Hird", "https://t.me/mod_russia/23394"}
	for _, profile := range defaultChunkProfiles() {
		for _, record := range chunkEnvelope(envelope, profile, manifest) {
			for _, fragment := range excluded {
				if strings.Contains(record.Text, fragment) {
					t.Errorf("profile %s put %q into chunk %s; only searchable"+
						" segments are chunked (srd017 R4.2)",
						profile.Name, fragment, record.ChunkID)
				}
			}
		}
	}
}

// Every record names the profile that cut it, and the identifiers distinguish
// profiles so the profiles of one document coexist without collision
// (srd017 R4.4).
func TestProfileRecordsNameTheirProfileAndDoNotCollide(t *testing.T) {
	envelope := profileFixtureEnvelope()
	manifest := ukraineTestManifest()
	seen := make(map[string]string)
	for _, profile := range defaultChunkProfiles() {
		records := chunkEnvelope(envelope, profile, manifest)
		if len(records) == 0 {
			t.Errorf("profile %s produced no chunk for a document with searchable text",
				profile.Name)
		}
		for _, record := range records {
			if record.ChunkProfile != profile.Name {
				t.Errorf("chunk %s names profile %q, cut by %q",
					record.ChunkID, record.ChunkProfile, profile.Name)
			}
			if previous, collided := seen[record.ChunkID]; collided {
				t.Errorf("identifier %s is produced by both %s and %s",
					record.ChunkID, previous, profile.Name)
			}
			seen[record.ChunkID] = profile.Name
			if !strings.Contains(record.ChunkID, profile.Name) {
				t.Errorf("identifier %s does not distinguish its profile", record.ChunkID)
			}
		}
	}
}

// The record a profile mints carries the envelope's declared metadata, and the
// clearance is checked against the ordered set a second time here: a level
// outside it is written at the highest rather than passed through
// (srd017 R2.5).
func TestProfileRecordsCarryDeclaredMetadataAndFailClosed(t *testing.T) {
	manifest := ukraineTestManifest()
	profile, _ := chunkProfileNamed(chunkProfileAtomic)

	envelope := profileFixtureEnvelope()
	records := chunkEnvelope(envelope, profile, manifest)
	if len(records) == 0 {
		t.Fatal("the fixture produced no chunk")
	}
	if records[0].Metadata.SourceType != sourceTypeOSINTAssessment {
		t.Errorf("chunk metadata names source type %q", records[0].Metadata.SourceType)
	}
	if records[0].Metadata.Clearance != "unclassified" {
		t.Errorf("chunk metadata names clearance %q", records[0].Metadata.Clearance)
	}

	envelope.Clearance = "cosmic-top-secret"
	records = chunkEnvelope(envelope, profile, manifest)
	if records[0].Metadata.Clearance != manifest.highestClearance() {
		t.Errorf("a clearance outside the declared set was written as %q rather than"+
			" the highest declared level %q",
			records[0].Metadata.Clearance, manifest.highestClearance())
	}
}

// Every document carries an Admiralty rating on both axes, which is what
// rel12.2 checks a brief's source-evaluation table against (srd017 R2.6).
//
// A document declaring its own grade keeps it; one declaring none takes its
// tier's. Neither reads the document -- the first is copied from the source and
// the second is declared in the corpus manifest, which is what an Admiralty
// rating is for, since it evaluates a source rather than a sentence.
func TestEveryTierCarriesAnAdmiraltyRating(t *testing.T) {
	manifest := ukraineTestManifest()
	for tier, envelope := range adapterFixtures(t) {
		letter, credibility := admiraltyForEnvelope(envelope, manifest)
		if letter == "" || credibility == 0 {
			t.Errorf("tier %s carries no Admiralty rating, so a brief citing it"+
				" leaves an unrated row in the source-evaluation table"+
				" (srd017 R2.6)", tier)
			continue
		}
		if letter < "A" || letter > "F" {
			t.Errorf("tier %s rates source reliability %q, outside A-F", tier, letter)
		}
		if credibility < 1 || credibility > 6 {
			t.Errorf("tier %s rates credibility %d, outside 1-6", tier, credibility)
		}
	}
}

// A document declaring its own grade keeps it rather than taking its tier's.
// The synthetic tier grades each source per report, and that grade is the
// collector's evaluation of that source on that day.
func TestADocumentsOwnRatingWinsOverItsTiers(t *testing.T) {
	manifest := ukraineTestManifest()
	manifest.TierAdmiralty[sourceTypeHumint] = "F6"

	envelope := adapterFixtures(t)[sourceTypeHumint]
	if envelope.Reliability != "B3" {
		t.Fatalf("the humint fixture declares reliability %q", envelope.Reliability)
	}
	letter, credibility := admiraltyForEnvelope(envelope, manifest)
	if letter != "B" || credibility != 3 {
		t.Errorf("a document declaring B3 was rated %s%d from its tier",
			letter, credibility)
	}
}

// A tier the manifest rates not at all returns no rating rather than an
// invented one. A rating nobody assigned is what srd015 R2.3 forbids the build
// from producing, and an unrated row is a visible gap where a fabricated grade
// would not be.
func TestAnUnratedTierProducesNoRating(t *testing.T) {
	manifest := ukraineTestManifest()
	delete(manifest.TierAdmiralty, sourceTypeOSINTNews)

	envelope := adapterFixtures(t)[sourceTypeOSINTNews]
	letter, credibility := admiraltyForEnvelope(envelope, manifest)
	if letter != "" || credibility != 0 {
		t.Errorf("an unrated tier produced the rating %s%d, which nobody assigned",
			letter, credibility)
	}
}

// The letter-number reader accepts the grades the scale defines and rejects
// everything else, including the descriptive reliabilities the real tiers carry
// on their documents.
func TestAdmiraltyRatingIsReadRatherThanInvented(t *testing.T) {
	rated := map[string]struct {
		letter string
		number int
	}{
		"B3": {"B", 3},
		"A1": {"A", 1},
		"C4": {"C", 4},
	}
	for reliability, want := range rated {
		letter, number := admiraltyFromReliability(reliability)
		if letter != want.letter || number != want.number {
			t.Errorf("reliability %s read as %s%d, want %s%d",
				reliability, letter, number, want.letter, want.number)
		}
	}
	for _, unrated := range []string{
		"published_assessment", "unrated_open_source", "official_release", "unrated", "", "Z9", "B7",
	} {
		letter, number := admiraltyFromReliability(unrated)
		if letter != "" || number != 0 {
			t.Errorf("reliability %q produced the rating %s%d, which nobody assigned",
				unrated, letter, number)
		}
	}
}

// Cutting every envelope at every profile is deterministic and orders records by
// profile, so a store diff stays readable when one profile's parameters change.
func TestChunkingAtEveryProfileIsDeterministic(t *testing.T) {
	manifest := ukraineTestManifest()
	envelopes := []corpusEnvelope{profileFixtureEnvelope()}
	envelopes = append(envelopes, func() corpusEnvelope {
		second := profileFixtureEnvelope()
		second.DocumentID = "isw-2022-08-15"
		second.Issued = "2022-08-15"
		return second
	}())

	first := chunkEnvelopesAtProfiles(envelopes, defaultChunkProfiles(), manifest)
	second := chunkEnvelopesAtProfiles(envelopes, defaultChunkProfiles(), manifest)
	if len(first) != len(second) {
		t.Fatalf("two runs produced %d and %d records", len(first), len(second))
	}
	for index := range first {
		if first[index].ChunkID != second[index].ChunkID {
			t.Fatalf("position %d holds %s then %s across runs",
				index, first[index].ChunkID, second[index].ChunkID)
		}
	}
	profileOrder := make([]string, 0, 4)
	for _, record := range first {
		if len(profileOrder) == 0 || profileOrder[len(profileOrder)-1] != record.ChunkProfile {
			profileOrder = append(profileOrder, record.ChunkProfile)
		}
	}
	if len(profileOrder) != len(defaultChunkProfiles()) {
		t.Errorf("records are interleaved across profiles rather than grouped: %v",
			profileOrder)
	}
}

// A passage carrying no line breaks is cut to the target rather than becoming
// one chunk at whatever length it happens to be.
//
// This is the case real reporting brought and the synthetic corpus never did.
// The ISW assessments arrive as a single continuous run of tens of thousands of
// characters with no newline anywhere, so the blank-line splitter yields one
// paragraph and every size profile produced one chunk per assessment. The size
// profiles stopped meaning anything, and the provider would have truncated each
// passage it was given, storing a vector that describes an assessment's opening
// lines under a chunk claiming to be the whole document.
func TestAParagraphWithNoLineBreaksIsCutToTheTarget(t *testing.T) {
	sentence := "Russian forces conducted limited ground attacks near the settlement. "
	body := strings.TrimSpace(strings.Repeat(sentence, 200))
	if strings.Contains(body, "\n") {
		t.Fatal("the fixture is supposed to carry no line break")
	}
	params := defaultCorpusChunkParameters()
	chunks := chunkCorpusText(body, params)
	if len(chunks) < 5 {
		t.Fatalf("a %d character run produced %d chunks at a %d character target",
			len(body), len(chunks), params.TargetCharacters)
	}
	for index, chunk := range chunks {
		// The accumulator carries a previous piece forward as overlap, so a
		// chunk may reach twice the target; what it may not do is hold the
		// whole run.
		if len(chunk) > 2*params.TargetCharacters {
			t.Errorf("chunk %d is %d characters against a %d character target",
				index+1, len(chunk), params.TargetCharacters)
		}
		if strings.TrimSpace(chunk) == "" {
			t.Errorf("chunk %d is empty", index+1)
		}
	}
	// Every sentence survives the split somewhere, so cutting loses no text.
	joined := strings.Join(chunks, " ")
	if !strings.Contains(joined, strings.TrimSpace(sentence)) {
		t.Error("the split dropped the passage's text")
	}
}

// A body whose paragraphs already fit the target chunks exactly as it did
// before the oversize split existed. This is what keeps the committed store's
// vectors valid: none of its paragraphs exceeds the target, so none of them
// reaches the new path (srd017 R4.3).
func TestParagraphsWithinTheTargetAreUnaffectedByTheOversizeSplit(t *testing.T) {
	paragraphs := []string{
		"Ukrainian forces continued interdiction strikes against logistics nodes.",
		"Russian forces conducted limited ground attacks near the settlement.",
	}
	body := strings.Join(paragraphs, "\n\n")
	params := defaultCorpusChunkParameters()
	for _, paragraph := range paragraphs {
		if len(paragraph) > params.TargetCharacters {
			t.Fatalf("the fixture paragraph is longer than the target")
		}
	}
	split := splitOversizeParagraphs(paragraphs, params.TargetCharacters)
	if len(split) != len(paragraphs) {
		t.Errorf("paragraphs within the target were split into %d pieces", len(split))
	}
	for index := range paragraphs {
		if split[index] != paragraphs[index] {
			t.Errorf("paragraph %d was rewritten: %q", index, split[index])
		}
	}
	chunks := chunkCorpusText(body, params)
	if len(chunks) != 1 {
		t.Errorf("a body of two short paragraphs produced %d chunks", len(chunks))
	}
}

// A single sentence longer than the target is cut on a rune boundary rather
// than left whole or dropped, and the cut does not break a multi-byte
// character.
func TestASentenceLongerThanTheTargetIsCutOnARuneBoundary(t *testing.T) {
	runOn := strings.Repeat("Сообщение о начале операции ", 200)
	pieces := splitParagraphOnSentences(strings.TrimSpace(runOn), 900)
	if len(pieces) < 2 {
		t.Fatalf("a %d character sentence produced %d pieces", len(runOn), len(pieces))
	}
	for index, piece := range pieces {
		if !utf8.ValidString(piece) {
			t.Errorf("piece %d is not valid UTF-8; the cut split a character", index+1)
		}
		if len(piece) > 900 {
			t.Errorf("piece %d is %d bytes against a 900 character target",
				index+1, len(piece))
		}
	}
}

// The per-profile chunk counts over the real datasets, which is the measurement
// the embedding budget is set against. Skipped when the datasets are absent.
func TestProfileRealChunkCountsWhenTheDatasetsArePresent(t *testing.T) {
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
	assessmentDocuments := 0
	for _, envelope := range envelopes {
		if envelope.SourceType == sourceTypeOSINTAssessment {
			assessmentDocuments++
		}
	}
	for _, profile := range defaultChunkProfiles() {
		total := 0
		assessments := 0
		for _, envelope := range envelopes {
			cut := len(chunkEnvelope(envelope, profile, manifest))
			total += cut
			if envelope.SourceType == sourceTypeOSINTAssessment {
				assessments += cut
			}
		}
		t.Logf("%-12s %6d chunks, of which %d from %d ISW assessments",
			profile.Name, total, assessments, assessmentDocuments)
		if total == 0 {
			t.Errorf("profile %s cut the corpus into nothing", profile.Name)
		}
		// An assessment averages tens of thousands of characters, so a size
		// profile yielding one chunk per assessment would mean the oversize
		// split had stopped working and every stored vector would describe a
		// truncated passage.
		if profile.Splitter == chunkSplitterParagraph && assessments <= assessmentDocuments {
			t.Errorf("profile %s cut %d chunks from %d assessments, at or below"+
				" one per document; the size profile is not cutting them",
				profile.Name, assessments, assessmentDocuments)
		}
	}
}

// The three profiles are the declared set, and medium-900 takes its parameters
// from the rel10.4 defaults rather than restating them -- which is what makes
// the byte-identity above hold by construction rather than by coincidence.
func TestDeclaredProfilesMatchTheRequirement(t *testing.T) {
	profiles := defaultChunkProfiles()
	if len(profiles) != 3 {
		t.Fatalf("the build declares %d profiles, want the 3 srd017 R4.1 names", len(profiles))
	}
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		names = append(names, profile.Name)
	}
	want := fmt.Sprintf("%s,%s,%s",
		chunkProfileAtomic, chunkProfileMedium, chunkProfileLarge)
	if strings.Join(names, ",") != want {
		t.Errorf("the declared profiles are %v, want %s", names, want)
	}
	medium, _ := chunkProfileNamed(chunkProfileMedium)
	defaults := defaultCorpusChunkParameters()
	if medium.TargetCharacters != defaults.TargetCharacters ||
		medium.OverlapCharacters != defaults.OverlapCharacters {
		t.Errorf("medium-900 is %d/%d against the rel10.4 defaults %d/%d; the two"+
			" must not drift or every stored vector is orphaned",
			medium.TargetCharacters, medium.OverlapCharacters,
			defaults.TargetCharacters, defaults.OverlapCharacters)
	}
}

// Citation entries resolved onto the chunks that cite them (srd017 R10.4;
// rel10.5 suite case (p)).

// referenceFixtureEnvelope is an assessment whose prose cites three entries
// across two paragraphs, with a tail carrying a fourth nothing cites.
func referenceFixtureEnvelope() corpusEnvelope {
	return corpusEnvelope{
		DocumentID:  "isw-2022-12-09",
		SourceType:  sourceTypeOSINTAssessment,
		Clearance:   "unclassified",
		Issued:      "2022-12-09",
		Origin:      "Institute for the Study of War",
		Reliability: "published_assessment",
		Provenance:  corpusProvenance{Collector: "Institute for the Study of War"},
		Title:       "Russian Offensive Campaign Assessment",
		RawRef:      corpusRawRef{Store: "isw", ID: "assessment", Path: iswDatasetPath},
		Segments: []corpusSegment{
			{Role: segmentRoleBody, Searchable: true,
				Text: "Russian forces continued offensive operations near Bakhmut.[1]" +
					" Ukrainian forces struck a bridge in Kherson Oblast.[2] The same" +
					" strike was reported again.[1]"},
			{Role: segmentRoleEndnotes, Searchable: false, Text: "the tail"},
		},
		References: []corpusReference{
			{Number: 1, Text: "https://t.me/rybar/41602", URLs: []string{"https://t.me/rybar/41602"}},
			{Number: 2, Text: "https://t.me/wargonzo/9526", URLs: []string{"https://t.me/wargonzo/9526"}},
			{Number: 3, Text: "Russia's Zapad-2021 Exercise | Institute for the Study of War"},
		},
	}
}

// The chunk carries the entries its own text cites, once each however often
// cited, and carries nothing the document holds but the chunk does not cite.
func TestChunkCarriesTheReferencesItsTextCites(t *testing.T) {
	envelope := referenceFixtureEnvelope()
	profile, _ := chunkProfileNamed(chunkProfileAtomic)

	records := chunkEnvelope(envelope, profile, ukraineTestManifest())
	if len(records) != 1 {
		t.Fatalf("cut %d chunks, want 1", len(records))
	}
	got := records[0].References
	if len(got) != 2 {
		t.Fatalf("the chunk carries %d references, want 2: %+v", len(got), got)
	}
	if got[0].Number != 1 || got[1].Number != 2 {
		t.Errorf("references are %d and %d, want the order the text cites them",
			got[0].Number, got[1].Number)
	}
	for _, reference := range got {
		if reference.Number == 3 {
			t.Error("the chunk carries an entry its text does not cite")
		}
	}
}

// The assertion the unit turns on. The stored vector was computed over this
// text and the build carries a vector forward only where its digest is
// unchanged, so resolution that touched the text would re-buy every vector the
// tier holds.
func TestResolvingReferencesLeavesTextAndDigestUntouched(t *testing.T) {
	envelope := referenceFixtureEnvelope()
	bare := envelope
	bare.References = nil

	for _, profile := range defaultChunkProfiles() {
		withReferences := chunkEnvelope(envelope, profile, ukraineTestManifest())
		without := chunkEnvelope(bare, profile, ukraineTestManifest())
		if len(withReferences) != len(without) {
			t.Fatalf("%s cut %d chunks with references and %d without",
				profile.Name, len(withReferences), len(without))
		}
		for index := range withReferences {
			if withReferences[index].Text != without[index].Text {
				t.Errorf("%s chunk %d: resolution changed the text.\n with: %q\nwithout: %q",
					profile.Name, index+1, withReferences[index].Text, without[index].Text)
			}
			if withReferences[index].TextSHA256 != without[index].TextSHA256 {
				t.Errorf("%s chunk %d: resolution changed the digest, so every stored"+
					" vector for this chunk would be discarded and bought again",
					profile.Name, index+1)
			}
			if len(withReferences[index].References) == 0 {
				t.Errorf("%s chunk %d resolved no references", profile.Name, index+1)
			}
		}
	}
}

// A marker carried into two chunks by the profile's overlap resolves in both,
// because each chunk is read on its own text.
func TestAMarkerInOverlappingChunksResolvesInBoth(t *testing.T) {
	paragraph := strings.Repeat("Ukrainian forces continued interdiction strikes. ", 12)
	envelope := referenceFixtureEnvelope()
	envelope.Segments = []corpusSegment{
		{Role: segmentRoleBody, Searchable: true,
			Text: paragraph + "\n\nRussian forces held the line.[1]\n\n" + paragraph},
	}
	profile, _ := chunkProfileNamed(chunkProfileMedium)

	records := chunkEnvelope(envelope, profile, ukraineTestManifest())
	citing := 0
	for _, record := range records {
		if strings.Contains(record.Text, "[1]") {
			citing++
			if len(record.References) != 1 || record.References[0].Number != 1 {
				t.Errorf("a chunk citing [1] resolved %+v", record.References)
			}
		}
	}
	if citing < 2 {
		t.Skipf("the fixture produced %d chunks carrying the marker; overlap did"+
			" not duplicate it, so there is nothing to assert here", citing)
	}
}

// Every tier but the assessments carries no entries, so its records encode
// exactly as they did before chunks gained references.
func TestATierCarryingNoEntriesResolvesNothing(t *testing.T) {
	envelope := referenceFixtureEnvelope()
	envelope.SourceType = sourceTypeOSINTNews
	envelope.References = nil
	profile, _ := chunkProfileNamed(chunkProfileAtomic)

	records := chunkEnvelope(envelope, profile, ukraineTestManifest())
	for _, record := range records {
		if record.References != nil {
			t.Errorf("a tier carrying no entries resolved %+v", record.References)
		}
		encoded, err := encodeCorpusYAML(record)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if strings.Contains(string(encoded), "references") {
			t.Errorf("a record with no references encodes the key:\n%s", encoded)
		}
	}
}

// The fixtures prove the rule; this proves it over what the corpus converts.
// It is a local verification rather than suite evidence, because it skips
// wherever the source datasets are absent and a skipped test proves nothing.
func TestChunkReferencesResolveOverTheRealDataset(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("CORPUS_DATA_ROOT"))
	if root == "" {
		t.Skip("CORPUS_DATA_ROOT is unset; the source datasets live outside the repository")
	}
	manifest := ukraineTestManifest()
	profile, _ := chunkProfileNamed(chunkProfileMedium)

	documents, citing, resolved := 0, 0, 0
	for _, line := range readJSONLines(t, filepath.Join(root, "isw", "isw_dailies.jsonl"), 4000) {
		envelope, err := adaptISWRecord(line, manifest)
		if err != nil || !strings.HasPrefix(envelope.Issued, "2022-12") {
			continue
		}
		documents++
		for _, record := range chunkEnvelope(envelope, profile, manifest) {
			markers := iswMarkerPattern.FindAllString(record.Text, -1)
			if len(markers) == 0 {
				if len(record.References) != 0 {
					t.Errorf("%s cites nothing and carries %d references",
						record.ChunkID, len(record.References))
				}
				continue
			}
			citing++
			if len(record.References) == 0 {
				t.Errorf("%s cites %v and resolved nothing", record.ChunkID, markers)
				continue
			}
			resolved += len(record.References)
			if record.TextSHA256 != corpusTextDigest(record.Text) {
				t.Errorf("%s digest does not describe its own text", record.ChunkID)
			}
		}
	}
	if documents != 30 {
		t.Fatalf("converted %d December assessments, want 30", documents)
	}
	if citing == 0 {
		t.Fatal("no chunk carried a citation marker")
	}
	// One concrete anchor: the closing chunk of the first assessment in the
	// window cites [76] and [77], and both resolve to addresses its tail
	// carries.
	for _, line := range readJSONLines(t, filepath.Join(root, "isw", "isw_dailies.jsonl"), 4000) {
		envelope, err := adaptISWRecord(line, manifest)
		if err != nil || envelope.DocumentID != "isw-2022-12-01" {
			continue
		}
		records := chunkEnvelope(envelope, profile, manifest)
		closing := records[len(records)-1]
		carried := make(map[int]corpusReference)
		for _, reference := range closing.References {
			carried[reference.Number] = reference
		}
		for _, wanted := range []int{76, 77} {
			reference, held := carried[wanted]
			if !held {
				t.Errorf("the closing chunk of isw-2022-12-01 does not resolve [%d];"+
					" it carries %+v", wanted, closing.References)
				continue
			}
			if len(reference.URLs) == 0 {
				t.Errorf("[%d] resolved to an entry carrying no address: %+v",
					wanted, reference)
			}
		}
	}
	t.Logf("isw medium-900: %d documents, %d chunks citing, %d references resolved",
		documents, citing, resolved)
}
