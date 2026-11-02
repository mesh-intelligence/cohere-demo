// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The rel10.5 adapter suite. It proves the three test-suite cases this unit
// owns -- (b) metadata-key parity across all five tiers, (c) only searchable
// segments reaching a chunk, and (g) the clearance mapping with its caveat --
// against fixtures written here rather than against the source datasets, which
// live outside the repository and are hundreds of megabytes. The fixtures carry
// the record shapes the datasets actually use, read from them while this was
// written.

// ukraineTestManifest is the declared corpus configuration the adapters read:
// the ordered clearance set, the marking mapping, the caveat vocabulary, and
// the per-tier policy. It stands in for the Ukraine corpus manifest, which the
// downstream repoint authors.
func ukraineTestManifest() corpusManifest {
	manifest := corpusManifest{
		ID:              "ukraine-2022",
		Version:         "1.0",
		ClearanceLevels: []string{"unclassified", "restricted", "confidential", "secret"},
		ClearanceMarkings: map[string]string{
			"UNCLASSIFIED//FOUO": "unclassified",
			"CONFIDENTIAL":       "confidential",
			"SECRET":             "secret",
			"SECRET//NOFORN":     "secret",
		},
		ClearanceCaveats: []string{"NOFORN"},
		TierClearance: map[string]string{
			sourceTypeOSINTNews:       "unclassified",
			sourceTypeOSINTAssessment: "unclassified",
			sourceTypeSIGINTEvent:     "unclassified",
			sourceTypeSIGINTSeries:    "unclassified",
		},
		// Admiralty ratings for the tiers whose documents declare none. Source
		// reliability first, information credibility second.
		//
		//   B2  ISW              a research organization with a stated method,
		//                        reporting it stands behind
		//   B2  air force        an official release from the service that
		//                        observed the event
		//   B3  general staff    an official release, but one belligerent's
		//                        count of its opponent's losses, which is not
		//                        independently confirmed
		//   C3  telegram news    twelve outlets from state media to Western
		//                        wires, unverified message by message
		TierAdmiralty: map[string]string{
			sourceTypeOSINTAssessment: "B2",
			sourceTypeSIGINTEvent:     "B2",
			sourceTypeSIGINTSeries:    "B3",
			sourceTypeOSINTNews:       "C3",
		},
	}
	return manifest
}

func ukraineTestClearanceMapping() clearanceMapping {
	manifest := ukraineTestManifest()
	return clearanceMapping{
		Markings: manifest.ClearanceMarkings,
		Caveats:  manifest.ClearanceCaveats,
	}
}

const humintFixture = `{"id":"HR-22-001","date":"2022-08-14","classification":"SECRET//NOFORN",` +
	`"source_id":"GRANITE","reliability":"B3","source_access":"freight logistics manager",` +
	`"collector":"DEFTECH STATION KYIV","title":"Rail node status",` +
	`"related_messages":[{"id":"23052","src":"BBC Russia","date":"2022-08-13"}],` +
	`"tags":["rail"],"text":"Classified by: DEFTECH/DDI; reasons 1.4(b),(d).\n\n` +
	`1. (S) SUMMARY: Source reporting indicates the rail node remained in use. END SUMMARY.\n\n` +
	`2. (S) Source GRANITE told this station that dispatcher traffic showed no interruption.\n\n` +
	`3. (S) FIELD COMMENT: Consistent with public reporting. END COMMENT."}`

const iswFixture = `{"date":"2022-08-14","title":"Russian Offensive Campaign Assessment, August 14",` +
	`"url":"russian-offensive-campaign-assessment-august-14",` +
	`"text":"Karolina Hird, Grace Mappes, and Mason Clark August 14, 8:30pm ET   ` +
	`Click here to see ISW's interactive map of the Russian invasion of Ukraine. ` +
	`Russian forces conducted limited ground attacks near the settlement. ` +
	`Ukrainian forces continued interdiction strikes against logistics nodes. ` +
	`[1] https://t.me/mod_russia/23394 [2] https://www.bbc.com/news/world-europe-64255249"}`

// adapterFixtures runs every adapter once, so the cases that hold the five to
// one shape do not each rebuild the set.
func adapterFixtures(t *testing.T) map[string]corpusEnvelope {
	t.Helper()
	manifest := ukraineTestManifest()
	mapping := ukraineTestClearanceMapping()

	humint, err := adaptHumintRecord([]byte(humintFixture), mapping, manifest)
	if err != nil {
		t.Fatalf("humint adapter: %v", err)
	}
	isw, err := adaptISWRecord([]byte(iswFixture), manifest)
	if err != nil {
		t.Fatalf("isw adapter: %v", err)
	}
	telegram, err := adaptTelegramRow(map[string]string{
		"id": "51673", "group_id": "meduzalive", "group_name": "Meduza",
		"text": "Сообщение о начале операции. ДАННОЕ СООБЩЕНИЕ (МАТЕРИАЛ) СОЗДАНО ИНОСТРАННЫМ АГЕНТОМ.",
		"date": "2022-08-14",
	}, manifest)
	if err != nil {
		t.Fatalf("telegram adapter: %v", err)
	}
	missile, err := adaptMissileRow(map[string]string{
		"time_start": "2022-10-10 06:00", "model": "Shahed-136", "launch_place": "Crimea",
		"target": "Ukraine", "launched": "28.0", "destroyed": "20.0", "affected region": "Kyiv",
	}, 7, manifest)
	if err != nil {
		t.Fatalf("missile adapter: %v", err)
	}
	losses, err := adaptLossesDelta(
		lossesDay{
			Date:      "2022-08-14",
			Personnel: map[string]string{"date": "2022-08-14", "personnel": "43750", "POW": ""},
			Equipment: map[string]string{"date": "2022-08-14", "tank": "1864", "drone": "async"},
		},
		lossesDay{
			Date:      "2022-08-13",
			Personnel: map[string]string{"date": "2022-08-13", "personnel": "43500", "POW": ""},
			Equipment: map[string]string{"date": "2022-08-13", "tank": "1860", "drone": "async"},
		},
		manifest)
	if err != nil {
		t.Fatalf("losses adapter: %v", err)
	}
	return map[string]corpusEnvelope{
		sourceTypeHumint:          humint,
		sourceTypeOSINTAssessment: isw,
		sourceTypeOSINTNews:       telegram,
		sourceTypeSIGINTEvent:     missile,
		sourceTypeSIGINTSeries:    losses,
	}
}

// Case (b). Every adapter populates the same top-level fields, so nothing
// downstream branches on which one ran. The assertion is on the set of fields
// rather than on the values, since the values differ by tier and that is the
// point (srd017 R1.2, rel10.5-uc001 S3).
func TestEnvelopeAdaptersPopulateIdenticalMetadataKeys(t *testing.T) {
	envelopes := adapterFixtures(t)
	if len(envelopes) != len(corpusSourceTypes()) {
		t.Fatalf("built %d envelopes for %d declared tiers",
			len(envelopes), len(corpusSourceTypes()))
	}
	for _, sourceType := range corpusSourceTypes() {
		envelope, built := envelopes[sourceType]
		if !built {
			t.Fatalf("no adapter produced an envelope for declared tier %q", sourceType)
		}
		populated := envelope.populatedFields()
		for _, field := range corpusEnvelopeRequiredFields() {
			if !populated[field] {
				t.Errorf("tier %s populates no %s; every adapter populates the same"+
					" fields (srd017 R1.2)", sourceType, field)
			}
		}
		if envelope.SourceType != sourceType {
			t.Errorf("tier %s declares source type %q", sourceType, envelope.SourceType)
		}
	}
}

// An envelope naming a tier outside the declared five is rejected, so a typo in
// an adapter is a build failure rather than a source type the store carries and
// the interface facets never offer (srd017 R1.1).
func TestEnvelopeRejectsAnUndeclaredSourceType(t *testing.T) {
	envelope := adapterFixtures(t)[sourceTypeHumint]
	envelope.SourceType = "osint"
	err := envelope.validate()
	if err == nil {
		t.Fatal("an envelope naming an undeclared tier validated")
	}
	if !strings.Contains(err.Error(), "declared tiers") {
		t.Errorf("error does not name the declared set: %v", err)
	}
}

// Case (c), first half. The material each tier carries for display and citation
// is present in the served document and absent from the text that is chunked
// (srd017 R1.4, rel10.5-uc001 S3). Asserted on both sides: dropping the
// material would also pass a chunk-side check while losing the citation
// apparatus a brief's sources point at.
func TestAdaptersCarryUnsearchableMaterialOutOfTheChunkedText(t *testing.T) {
	envelopes := adapterFixtures(t)
	cases := []struct {
		tier     string
		role     string
		fragment string
	}{
		{sourceTypeHumint, segmentRoleAuthority, "Classified by: DEFTECH/DDI"},
		{sourceTypeOSINTAssessment, segmentRoleEndnotes, "https://t.me/mod_russia/23394"},
		{sourceTypeOSINTAssessment, segmentRoleByline, "Karolina Hird"},
		{sourceTypeOSINTNews, segmentRoleDisclaimer, "ИНОСТРАННЫМ АГЕНТОМ"},
	}
	for _, testCase := range cases {
		envelope := envelopes[testCase.tier]
		segment, found := segmentWithRole(envelope, testCase.role)
		if !found {
			t.Errorf("tier %s carries no %s segment", testCase.tier, testCase.role)
			continue
		}
		if segment.Searchable {
			t.Errorf("tier %s marks its %s segment searchable; it is carried for"+
				" display and citation and never embedded (srd017 R1.4)",
				testCase.tier, testCase.role)
		}
		if !strings.Contains(segment.Text, testCase.fragment) {
			t.Errorf("tier %s %s segment does not carry %q",
				testCase.tier, testCase.role, testCase.fragment)
		}
		if strings.Contains(envelope.searchableText(), testCase.fragment) {
			t.Errorf("tier %s puts %q into the text that is chunked; only searchable"+
				" segments are chunked (srd017 R4.2)", testCase.tier, testCase.fragment)
		}
	}
}

// Case (c), second half. The searchable text is what a chunk is cut from, so a
// tier whose segments are all unsearchable is an adapter defect rather than an
// empty document.
func TestEnvelopeRejectsADocumentWithNothingSearchable(t *testing.T) {
	envelope := adapterFixtures(t)[sourceTypeOSINTAssessment]
	for index := range envelope.Segments {
		envelope.Segments[index].Searchable = false
	}
	if err := envelope.validate(); err == nil {
		t.Fatal("an envelope with no searchable segment validated")
	}
}

// The HUMINT segmenter names the report's own structure, so a later profile can
// chunk one segment per unit and a citation can say which part of the report it
// came from.
func TestHumintAdapterNamesSummaryBodyAndComment(t *testing.T) {
	envelope := adapterFixtures(t)[sourceTypeHumint]
	roles := make([]string, 0, len(envelope.Segments))
	for _, segment := range envelope.Segments {
		roles = append(roles, segment.Role)
	}
	for _, want := range []string{
		segmentRoleAuthority, segmentRoleSummary, segmentRoleBody, segmentRoleComment,
	} {
		if _, found := segmentWithRole(envelope, want); !found {
			t.Errorf("humint segments %v carry no %s", roles, want)
		}
	}
	if roles[0] != segmentRoleAuthority {
		t.Errorf("the authority line is not the first segment: %v", roles)
	}
}

// Case (g). Each marking lands on the declared ordered set, NOFORN rides beside
// secret rather than above it, and the real tiers take their level from tier
// policy without any document text being read (srd017 R2.2, R2.3, R2.4,
// rel10.5-uc001 S4).
func TestAdapterClearanceMappingLandsOnTheDeclaredSet(t *testing.T) {
	manifest := ukraineTestManifest()
	mapping := ukraineTestClearanceMapping()
	cases := []struct {
		marking string
		level   string
		caveats []string
	}{
		{"UNCLASSIFIED//FOUO", "unclassified", nil},
		{"CONFIDENTIAL", "confidential", nil},
		{"SECRET", "secret", nil},
		{"SECRET//NOFORN", "secret", []string{"NOFORN"}},
	}
	for _, testCase := range cases {
		level, caveats := clearanceFromMarking(testCase.marking, mapping, manifest)
		if level != testCase.level {
			t.Errorf("marking %s maps to %q, want %q", testCase.marking, level, testCase.level)
		}
		if strings.Join(caveats, ",") != strings.Join(testCase.caveats, ",") {
			t.Errorf("marking %s carries caveats %v, want %v",
				testCase.marking, caveats, testCase.caveats)
		}
		if !manifest.declaresClearance(level) {
			t.Errorf("marking %s produced %q, which is outside the declared ordered set",
				testCase.marking, level)
		}
	}
}

// NOFORN is a caveat and not a fifth level. Modelling it as a level would
// reorder a set rel11.0 iterates as pairs, so the assertion is that the caveated
// marking and the plain one land on the same level and differ only in the flag
// (srd017 R2.3).
func TestNoforCaveatIsNotAFifthClearanceLevel(t *testing.T) {
	manifest := ukraineTestManifest()
	mapping := ukraineTestClearanceMapping()
	plain, plainCaveats := clearanceFromMarking("SECRET", mapping, manifest)
	caveated, caveats := clearanceFromMarking("SECRET//NOFORN", mapping, manifest)
	if plain != caveated {
		t.Errorf("SECRET maps to %q and SECRET//NOFORN to %q; the caveat restricts"+
			" who may receive a secret document and does not make it more"+
			" classified than secret (srd017 R2.3)", plain, caveated)
	}
	if len(plainCaveats) != 0 {
		t.Errorf("SECRET carries caveats %v", plainCaveats)
	}
	if len(caveats) != 1 || caveats[0] != "NOFORN" {
		t.Errorf("SECRET//NOFORN carries caveats %v, want [NOFORN]", caveats)
	}
	for _, level := range manifest.ClearanceLevels {
		if strings.EqualFold(level, "noforn") {
			t.Error("NOFORN appears in the declared ordered clearance set")
		}
	}
}

// An absent or unrecognized marking is written at the highest declared level. A
// guess that lands low discloses, so the direction of the default is the whole
// of the requirement (srd017 R2.5, srd010 R1.3).
func TestUnrecognizedMarkingFailsClosedAtTheHighestLevel(t *testing.T) {
	manifest := ukraineTestManifest()
	mapping := ukraineTestClearanceMapping()
	for _, marking := range []string{"", "   ", "TOP SECRET//SCI", "restricted-ish"} {
		level, _ := clearanceFromMarking(marking, mapping, manifest)
		if level != manifest.highestClearance() {
			t.Errorf("marking %q maps to %q, want the highest declared level %q",
				marking, level, manifest.highestClearance())
		}
	}
}

// The four real tiers take their clearance from tier policy, because the
// material is published and its level is a fact about where it came from rather
// than about what it says. Nothing reads a document to decide it (srd017 R2.4).
func TestRealTiersTakeClearanceFromTierPolicy(t *testing.T) {
	envelopes := adapterFixtures(t)
	for _, tier := range []string{
		sourceTypeOSINTNews, sourceTypeOSINTAssessment,
		sourceTypeSIGINTEvent, sourceTypeSIGINTSeries,
	} {
		if got := envelopes[tier].Clearance; got != "unclassified" {
			t.Errorf("tier %s is tagged %q, want the declared tier policy unclassified",
				tier, got)
		}
		if envelopes[tier].Synthetic {
			t.Errorf("tier %s is marked synthetic", tier)
		}
	}
	if !envelopes[sourceTypeHumint].Synthetic {
		t.Error("the humint tier is not marked synthetic; the corpus identifies its" +
			" invented tier in the artifact (srd017 R9.3)")
	}
}

// A tier the manifest declares no policy for fails closed, for the same reason
// an unrecognized marking does.
func TestTierWithNoDeclaredPolicyFailsClosed(t *testing.T) {
	manifest := ukraineTestManifest()
	delete(manifest.TierClearance, sourceTypeOSINTNews)
	if got := tierClearance(sourceTypeOSINTNews, manifest); got != manifest.highestClearance() {
		t.Errorf("an undeclared tier is tagged %q, want the highest declared level %q",
			got, manifest.highestClearance())
	}
}

// A HUMINT report names the Telegram messages it was written over, so the
// provenance walk reaches the public reporting behind a source-protected
// document (srd017 R5.4).
func TestHumintAdapterCarriesCrossTierProvenance(t *testing.T) {
	envelope := adapterFixtures(t)[sourceTypeHumint]
	if len(envelope.Provenance.DerivedFrom) != 1 {
		t.Fatalf("humint carries %d derived references, want 1",
			len(envelope.Provenance.DerivedFrom))
	}
	derived := envelope.Provenance.DerivedFrom[0]
	if derived.Tier != sourceTypeOSINTNews || derived.ID != "23052" {
		t.Errorf("derived reference is %+v, want the telegram message it was written over",
			derived)
	}
	if derived.Relation != derivedRelationWrittenOver {
		t.Errorf("derived relation is %q, want %q", derived.Relation, derivedRelationWrittenOver)
	}
}

// The issued date is the one the source carries, in each of the layouts the
// datasets state dates in (srd017 R1.5).
func TestAdapterIssuedDateReadsTheSourceLayouts(t *testing.T) {
	cases := map[string]string{
		"2022-08-14":          "2022-08-14",
		"2022-10-10 06:00":    "2022-10-10",
		"2022-10-10 06:00:30": "2022-10-10",
	}
	for raw, want := range cases {
		got, err := normalizedIssuedDate(raw)
		if err != nil {
			t.Errorf("date %q: %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("date %q normalized to %q, want %q", raw, got, want)
		}
	}
}

// A record whose date is absent or unparseable fails rather than being
// defaulted, because a default places a document inside or outside the window
// by accident (srd017 R1.5).
func TestAdapterRejectsARecordWhoseDateCannotBeEstablished(t *testing.T) {
	for _, raw := range []string{"", "   ", "not a date", "14/08/2022"} {
		if _, err := normalizedIssuedDate(raw); err == nil {
			t.Errorf("date %q was accepted; an unestablishable date fails the build", raw)
		}
	}
	manifest := ukraineTestManifest()
	mapping := ukraineTestClearanceMapping()
	undated := strings.Replace(humintFixture, `"date":"2022-08-14"`, `"date":""`, 1)
	if _, err := adaptHumintRecord([]byte(undated), mapping, manifest); err == nil {
		t.Fatal("a humint record carrying no date was adapted")
	}
}

// The ISW dataset carries a record whose title states a publication year three
// years from its date field. The window filter selects on that field, so the
// disagreement is a document that would be admitted or dropped by accident.
func TestISWAdapterRejectsATitleDateContradictingTheRecordDate(t *testing.T) {
	manifest := ukraineTestManifest()
	contradicted := strings.Replace(iswFixture,
		"Russian Offensive Campaign Assessment, August 14",
		"Russian Offensive Campaign Assessment, August 14 2025", 1)
	_, err := adaptISWRecord([]byte(contradicted), manifest)
	if err == nil {
		t.Fatal("an isw record whose title contradicts its date field was adapted")
	}
	if !strings.Contains(err.Error(), "publication date") {
		t.Errorf("error does not name the disagreement: %v", err)
	}
}

// The row-oriented tiers render their columns as prose, because a retrieval
// corpus holds documents and a serialized row retrieves badly.
func TestRowAdaptersRenderFiguresAsProse(t *testing.T) {
	envelopes := adapterFixtures(t)
	missile := envelopes[sourceTypeSIGINTEvent].searchableText()
	for _, want := range []string{"Shahed-136", "Crimea", "launched 28", "destroyed 20"} {
		if !strings.Contains(missile, want) {
			t.Errorf("missile digest does not state %q: %s", want, missile)
		}
	}
	losses := envelopes[sourceTypeSIGINTSeries].searchableText()
	// The day's reported change, with the running total beside it.
	if !strings.Contains(losses, "+250") {
		t.Errorf("losses digest does not state the day's change: %s", losses)
	}
	if !strings.Contains(losses, "43750") {
		t.Errorf("losses digest does not state the running total: %s", losses)
	}
	if !strings.Contains(losses, "+4") {
		t.Errorf("losses digest does not state the equipment change: %s", losses)
	}
	if strings.Contains(losses, "POW") {
		t.Errorf("losses digest states a column the source left blank: %s", losses)
	}
	if strings.Contains(losses, "Drone") {
		t.Errorf("losses digest states a column the source could not report: %s", losses)
	}
}

// A cumulative running total answers how many since the invasion began, which
// is a question about the war rather than about the day an analyst asked for.
// The digest reports the difference between consecutive days, and a day whose
// predecessor is missing reports the total rather than inventing a change.
func TestLossesDeltaReportsTheDayRatherThanTheWar(t *testing.T) {
	manifest := ukraineTestManifest()
	today := lossesDay{
		Date:      "2022-08-14",
		Personnel: map[string]string{"personnel": "43750"},
		Equipment: map[string]string{"tank": "1864"},
	}
	withPrevious, err := adaptLossesDelta(today, lossesDay{
		Date:      "2022-08-13",
		Personnel: map[string]string{"personnel": "43500"},
		Equipment: map[string]string{"tank": "1860"},
	}, manifest)
	if err != nil {
		t.Fatalf("losses delta: %v", err)
	}
	if text := withPrevious.searchableText(); !strings.Contains(text, "+250") {
		t.Errorf("delta against the previous day is not stated: %s", text)
	}
	withoutPrevious, err := adaptLossesDelta(today, lossesDay{}, manifest)
	if err != nil {
		t.Fatalf("losses delta with no previous day: %v", err)
	}
	text := withoutPrevious.searchableText()
	if !strings.Contains(text, "running total 43750") {
		t.Errorf("a day with no predecessor does not report the running total: %s", text)
	}
	if strings.Contains(text, "+") {
		t.Errorf("a day with no predecessor invented a change: %s", text)
	}
}

// Both row tiers name the source rows they were built from, so a digest
// aggregating them inherits references that resolve (srd017 R5.4). The strike
// tier names the one row it converted; the loss tier names both files, since
// its unit is the day and each day is read from two.
func TestRowAdaptersNameTheSourceRowTheyConverted(t *testing.T) {
	envelopes := adapterFixtures(t)
	wantReferences := map[string]int{
		sourceTypeSIGINTEvent:  1,
		sourceTypeSIGINTSeries: 2,
	}
	for tier, want := range wantReferences {
		derived := envelopes[tier].Provenance.DerivedFrom
		if len(derived) != want {
			t.Errorf("tier %s carries %d derived references, want %d",
				tier, len(derived), want)
			continue
		}
		for _, reference := range derived {
			if reference.Relation != derivedRelationAggregates {
				t.Errorf("tier %s declares relation %q, want %q",
					tier, reference.Relation, derivedRelationAggregates)
			}
			if !strings.Contains(reference.ID, ".csv") {
				t.Errorf("tier %s names %q rather than a source row", tier, reference.ID)
			}
		}
	}
}

// The raw reference resolves to the dataset record the envelope was built from,
// so a served document is attributable to a line in a file (srd017 R1.6).
func TestAdaptersReferenceTheUntransformedSourceRecord(t *testing.T) {
	for tier, envelope := range adapterFixtures(t) {
		if envelope.RawRef.Store == "" || envelope.RawRef.ID == "" {
			t.Errorf("tier %s carries an incomplete raw reference %+v", tier, envelope.RawRef)
		}
		if !strings.Contains(envelope.RawRef.Path, "/") {
			t.Errorf("tier %s raw reference names no dataset path: %+v", tier, envelope.RawRef)
		}
	}
}

// searchableText joins the searchable segments in document order, which is the
// body the chunker splits and the body a character span indexes into. Order and
// exclusion are both asserted, because a join that reordered segments would
// leave every later lineage offset pointing at the wrong passage.
func TestSearchableTextJoinsSearchableSegmentsInOrder(t *testing.T) {
	envelope := corpusEnvelope{Segments: []corpusSegment{
		{Role: segmentRoleAuthority, Searchable: false, Text: "authority"},
		{Role: segmentRoleSummary, Searchable: true, Text: "first"},
		{Role: segmentRoleBody, Searchable: true, Text: "second"},
		{Role: segmentRoleEndnotes, Searchable: false, Text: "endnotes"},
	}}
	if got := envelope.searchableText(); got != "first\n\nsecond" {
		t.Errorf("searchable text is %q, want %q", got, "first\n\nsecond")
	}
}

// The cases above run against fixtures, which prove the contract and cannot
// prove the adapters read the datasets they were written for. This one runs
// them over the real files when CORPUS_DATA_ROOT names a directory holding
// them, and skips when it does not: the datasets are hundreds of megabytes and
// live outside the repository, so requiring them would make the suite
// unrunnable on a clone rather than more thorough.
func TestAdaptersReadTheRealDatasetsWhenPresent(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("CORPUS_DATA_ROOT"))
	if root == "" {
		t.Skip("CORPUS_DATA_ROOT is unset; the source datasets live outside the repository")
	}
	manifest := ukraineTestManifest()
	mapping := ukraineTestClearanceMapping()

	adaptedHumint := 0
	for _, line := range readJSONLines(t, filepath.Join(root, "humint", "humint_reports.jsonl"), 40) {
		envelope, err := adaptHumintRecord(line, mapping, manifest)
		if err != nil {
			t.Errorf("humint adapter on a real record: %v", err)
			continue
		}
		adaptedHumint++
		if envelope.Clearance == "" {
			t.Errorf("humint record %s carries no clearance", envelope.DocumentID)
		}
	}
	if adaptedHumint == 0 {
		t.Error("no humint record was adapted")
	}

	withEndnotes, withByline, rejectedForDate := 0, 0, 0
	for _, line := range readJSONLines(t, filepath.Join(root, "isw", "isw_dailies.jsonl"), 40) {
		envelope, err := adaptISWRecord(line, manifest)
		if err != nil {
			// The dataset carries a record whose title year disagrees with its
			// date field. Rejecting it is the required behaviour, so it is
			// counted rather than failing the case.
			if strings.Contains(err.Error(), "publication date") {
				rejectedForDate++
				continue
			}
			t.Errorf("isw adapter on a real record: %v", err)
			continue
		}
		if _, found := segmentWithRole(envelope, segmentRoleEndnotes); found {
			withEndnotes++
		}
		if _, found := segmentWithRole(envelope, segmentRoleByline); found {
			withByline++
		}
	}
	if withEndnotes == 0 {
		t.Error("no real ISW assessment yielded an endnote segment; the tail is" +
			" roughly a quarter of that tier's text and must not be embedded")
	}
	if withByline == 0 {
		t.Error("no real ISW assessment yielded a byline segment")
	}
	t.Logf("isw: %d with endnotes, %d with byline, %d rejected for a contradicted date",
		withEndnotes, withByline, rejectedForDate)
}

// The prose-to-tail cut and the reconciliation that catches it landing wrong
// (srd017 R1.7, R10.5; rel10.5 suite case (o)).

// iswAssessmentText assembles an assessment the way the tier writes one, so a
// case states only the part it is about.
func iswAssessmentText(prose, tail string, withNote bool) string {
	text := "Karolina Hird, Grace Mappes, and George Barros 8:30pm ET\n\n" + prose
	if withNote {
		text += " " + iswClosingNote
	}
	if tail != "" {
		text += " " + tail
	}
	return text
}

// The defect the cut was rewritten for. The tier writes its links by hand, and
// an assessment whose first entry drops the "h" from its scheme had that entry
// admitted into the searchable body by a link-anchored cut and embedded as
// though it were assessment.
func TestISWTailCutSurvivesAMistypedFirstEntry(t *testing.T) {
	text := iswAssessmentText(
		"Russian forces continued offensive operations near Bakhmut.[1] Ukrainian"+
			" forces struck a bridge in Kherson Oblast.[2]",
		"[1] ttps://www do rbc dot ru/politics/09/12/2022/639; https://ria"+
			" dot ru/20221209/putin-1837547068.html [2] https://t.me/rybar/41602",
		true)

	segments := segmentISWText(text)
	body, found := segmentTextWithRole(segments, segmentRoleBody)
	if !found {
		t.Fatal("the assessment yielded no body segment")
	}
	if strings.Contains(body, "ttps://") || strings.Contains(body, " dot ") {
		t.Errorf("the body carries citation apparatus, so the cut fell after the"+
			" mistyped entry rather than before it:\n%q", body)
	}
	// The note itself is carried in its own segment (R1.8), so the body ends at
	// the prose before it rather than at the note.
	if !strings.HasSuffix(body, "Kherson Oblast.[2]") {
		t.Errorf("the body does not end at the prose before the closing note:\n%q", body)
	}
	if note, found := segmentTextWithRole(segments, segmentRoleDisclaimer); !found ||
		note != iswClosingNote {
		t.Error("the closing note was not carried into its own segment")
	}
	tail, found := segmentTextWithRole(segments, segmentRoleEndnotes)
	if !found {
		t.Fatal("the assessment yielded no endnote segment")
	}
	if !strings.HasPrefix(tail, "[1]") {
		t.Errorf("the tail starts at %.20q, want the first entry", tail)
	}
}

// An assessment carrying no closing note is cut by the link-anchored pattern,
// which is what the tier's abbreviated reports need.
func TestISWTailCutFallsBackWithoutTheClosingNote(t *testing.T) {
	text := iswAssessmentText(
		"Russian authorities continued filtration measures in occupied Ukraine.[50]",
		"[50] https://t.me/rybar/41602", false)

	segments := segmentISWText(text)
	body, _ := segmentTextWithRole(segments, segmentRoleBody)
	tail, found := segmentTextWithRole(segments, segmentRoleEndnotes)
	if !found {
		t.Fatal("the abbreviated assessment yielded no endnote segment")
	}
	if strings.Contains(body, "https://") {
		t.Errorf("the body carries a link:\n%q", body)
	}
	if !strings.HasPrefix(tail, "[50]") {
		t.Errorf("the tail starts at %.20q, want the first entry", tail)
	}
}

// A closing note with nothing after it ends the assessment. The link-anchored
// fallback is deliberately not tried, because any match it found would lie
// before the note and cut the assessment in half.
func TestISWClosingNoteWithNoTailLeavesTheBodyWhole(t *testing.T) {
	text := iswAssessmentText(
		"Russian forces continued offensive operations near Bakhmut.[1]", "", true)

	segments := segmentISWText(text)
	if _, found := segmentTextWithRole(segments, segmentRoleEndnotes); found {
		t.Error("an assessment with no tail yielded an endnote segment")
	}
	// The note is carried in its own segment either way; what this case holds is
	// that the assessment before it survives whole rather than being cut by a
	// fallback match lying further back in the prose.
	if note, found := segmentTextWithRole(segments, segmentRoleDisclaimer); !found ||
		note != iswClosingNote {
		t.Error("the closing note was not carried into its own segment")
	}
	body, _ := segmentTextWithRole(segments, segmentRoleBody)
	if !strings.HasSuffix(body, "near Bakhmut.[1]") {
		t.Errorf("the assessment was cut short:\n%q", body)
	}
}

// A bracketed index inside a link is not an entry. Facebook permalinks carry
// "?__cft__[0]=", which a rule reading every bracketed number as an entry both
// invents an entry from and truncates the genuine entry before it.
func TestISWBracketedIndexInsideALinkOpensNoEntry(t *testing.T) {
	tail := "[1] https://www.facebook.com/GeneralStaff.ua/posts/pfbid028Fj?__cft__[0]" +
		"=AZWT5akh1meKaLw08dbViXATtycy0dVuw9cN6O ; https://t.me/rybar/41602" +
		" [2] https://t.me/wargonzo/9526"
	text := iswAssessmentText("Ukrainian forces struck a bridge.[1] Fighting continued.[2]",
		tail, true)

	segments := segmentISWText(text)
	got, found := segmentTextWithRole(segments, segmentRoleEndnotes)
	if !found {
		t.Fatal("the assessment yielded no endnote segment")
	}
	if got != tail {
		t.Errorf("the tail was cut inside a link.\n got: %q\nwant: %q", got, tail)
	}
	if start := iswEntryStart(tail, 0); start != 0 {
		t.Errorf("the first entry starts at %d, want 0", start)
	}
	envelope := corpusEnvelope{DocumentID: "isw-2022-12-19", Segments: segments}
	if err := reconcileISWMarkers(envelope); err != nil {
		t.Errorf("markers citing entries the tail carries did not reconcile: %v", err)
	}
}

// Reconciliation names the document and every unresolved marker, because they
// share one cause and reporting the first would hide it.
func TestISWMarkerReconciliationNamesEveryUnresolvedMarker(t *testing.T) {
	envelope := corpusEnvelope{
		DocumentID: "isw-2022-12-09",
		Segments: []corpusSegment{
			{Role: segmentRoleBody, Searchable: true,
				Text: "Fighting continued near Bakhmut.[1] Strikes hit Kherson.[2]" +
					" Filtration measures continued.[3] And again.[1]"},
			{Role: segmentRoleEndnotes, Searchable: false,
				Text: "[3] https://t.me/rybar/41602"},
		},
	}

	err := reconcileISWMarkers(envelope)
	if err == nil {
		t.Fatal("markers citing entries the tail does not carry reconciled")
	}
	for _, want := range []string{"isw-2022-12-09", "[1]", "[2]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not name %s: %v", want, err)
		}
	}
	if strings.Count(err.Error(), "[1]") != 1 {
		t.Errorf("a marker cited twice is reported twice: %v", err)
	}
}

// The unsearchable segments are where the citation apparatus lives, so a
// marker inside one is not a claim the prose made and is not reconciled.
func TestISWMarkerReconciliationReadsOnlySearchableSegments(t *testing.T) {
	envelope := corpusEnvelope{
		DocumentID: "isw-2022-12-01",
		Segments: []corpusSegment{
			{Role: segmentRoleByline, Searchable: false, Text: "Karolina Hird [99]"},
			{Role: segmentRoleBody, Searchable: true, Text: "Fighting continued.[1]"},
			{Role: segmentRoleEndnotes, Searchable: false,
				Text: "[1] https://t.me/rybar/41602"},
		},
	}

	if err := reconcileISWMarkers(envelope); err != nil {
		t.Errorf("a marker in an unsearchable segment was reconciled: %v", err)
	}
}

// The fixtures above prove the rules; this proves they hold over the documents
// the corpus actually converts. It reads the real dataset when
// CORPUS_DATA_ROOT names it and skips otherwise, as the tier's other real-data
// case does.
func TestISWWindowReconcilesOverTheRealDataset(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("CORPUS_DATA_ROOT"))
	if root == "" {
		t.Skip("CORPUS_DATA_ROOT is unset; the source datasets live outside the repository")
	}
	manifest := ukraineTestManifest()

	converted, withNote := 0, 0
	for _, line := range readJSONLines(t, filepath.Join(root, "isw", "isw_dailies.jsonl"), 4000) {
		envelope, err := adaptISWRecord(line, manifest)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(envelope.Issued, "2022-12") {
			continue
		}
		converted++
		if err := reconcileISWMarkers(envelope); err != nil {
			t.Errorf("a converted assessment does not reconcile: %v", err)
		}
		for _, segment := range envelope.Segments {
			if !segment.Searchable {
				continue
			}
			for _, apparatus := range []string{"http", " dot ", "(dot)"} {
				if strings.Contains(segment.Text, apparatus) {
					t.Errorf("%s carries %q in a searchable segment, so citation"+
						" apparatus is being embedded as assessment",
						envelope.DocumentID, apparatus)
				}
			}
			if strings.Contains(segment.Text, "does not receive any classified material") {
				t.Errorf("%s carries the closing note in a searchable segment,"+
					" so it enters a vector (srd017 R1.8)", envelope.DocumentID)
			}
		}
		if _, carries := segmentTextWithRole(envelope.Segments, segmentRoleDisclaimer); carries {
			withNote++
		}
	}
	if converted == 0 {
		t.Fatal("the dataset yielded no December 2022 assessment")
	}
	if withNote != 27 {
		t.Errorf("%d assessments carry the closing note as a segment, want 27"+
			" -- three of the thirty are abbreviated reports that omit it", withNote)
	}
	t.Logf("isw: %d December 2022 assessments reconciled, %d carrying the closing note",
		converted, withNote)
}

// Reading the citation tail into numbered entries (srd017 R10.1, R10.2, R10.3;
// rel10.5 suite case (p)).

// The closing note carried out of the searchable body (srd017 R1.8).

// The note is written verbatim in nearly every full assessment, so chunked it
// makes the last chunk of each one resemble the others rather than the
// assessment it was cut from. It is carried for display and kept out of every
// vector.
func TestISWClosingNoteIsCarriedUnsearchable(t *testing.T) {
	text := iswAssessmentText(
		"Russian forces continued offensive operations near Bakhmut.[1]",
		"[1] https://t.me/rybar/41602", true)

	segments := segmentISWText(text)
	note, found := segmentTextWithRole(segments, segmentRoleDisclaimer)
	if !found {
		t.Fatal("the closing note yielded no segment of its own")
	}
	if note != iswClosingNote {
		t.Errorf("the note segment is not the note verbatim:\n%q", note)
	}
	for _, segment := range segments {
		if segment.Role == segmentRoleDisclaimer && segment.Searchable {
			t.Error("the closing note is searchable, so it enters a vector")
		}
	}
	body, _ := segmentTextWithRole(segments, segmentRoleBody)
	if strings.Contains(body, "does not receive any classified material") {
		t.Errorf("the body still carries the note:\n%q", body)
	}
	if !strings.HasSuffix(body, "near Bakhmut.[1]") {
		t.Errorf("the body was cut somewhere other than at the note:\n%q", body)
	}
	envelope := corpusEnvelope{Segments: segments}
	if strings.Contains(envelope.searchableText(), "classified material") {
		t.Error("the note reaches the searchable text the chunker splits")
	}
}

// The tier's abbreviated reports carry no note, and they segment as they did
// before the note was given a segment of its own.
func TestISWAssessmentWithoutTheClosingNoteIsUnchanged(t *testing.T) {
	text := iswAssessmentText(
		"Russian authorities continued filtration measures.[50]",
		"[50] https://t.me/rybar/41602", false)

	segments := segmentISWText(text)
	if _, found := segmentTextWithRole(segments, segmentRoleDisclaimer); found {
		t.Error("an assessment carrying no note yielded a note segment")
	}
	body, _ := segmentTextWithRole(segments, segmentRoleBody)
	if !strings.HasSuffix(body, "filtration measures.[50]") {
		t.Errorf("the body of a note-less assessment changed:\n%q", body)
	}
}

// The tier separates references inside one entry every way it writes
// whitespace and punctuation, and defanging puts spaces inside a single
// address. Splitting on the scheme is what survives both; splitting on a
// semicolon or on whitespace cuts addresses in half or glues them together.
func TestISWReferencesSplitOnTheSchemeAndNotOnSeparators(t *testing.T) {
	for _, test := range []struct {
		name  string
		entry string
		want  []string
	}{{
		name:  "non-breaking space around the semicolon",
		entry: "https://t.me/modmilby/20425\u00a0;\u00a0https://t.me/modmilby/20426",
		want:  []string{"https://t.me/modmilby/20425", "https://t.me/modmilby/20426"},
	}, {
		name:  "a bare space and no semicolon at all",
		entry: "https://t.me/wargonzo/9532 https://t.me/rybar/41602",
		want:  []string{"https://t.me/wargonzo/9532", "https://t.me/rybar/41602"},
	}, {
		name:  "no space at all",
		entry: "https://t.me/rybar/41805;https://t.me/readovkanews/48389",
		want:  []string{"https://t.me/rybar/41805", "https://t.me/readovkanews/48389"},
	}, {
		name:  "a defanged address keeps the spaces inside it",
		entry: "https://armyinform dot com.ua/2022/12/01/bryfing; https://t.me/rybar/41602",
		want: []string{"https://armyinform dot com.ua/2022/12/01/bryfing",
			"https://t.me/rybar/41602"},
	}, {
		name:  "a parenthesised defang is left as written",
		entry: "https://interfax(dot)com/newsroom/top-stories/85598/",
		want:  []string{"https://interfax(dot)com/newsroom/top-stories/85598/"},
	}} {
		t.Run(test.name, func(t *testing.T) {
			got := extractISWReferenceURLs(test.entry)
			if len(got) != len(test.want) {
				t.Fatalf("split into %d references, want %d:\n%q", len(got), len(test.want), got)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Errorf("reference %d is %q, want %q", i+1, got[i], test.want[i])
				}
			}
		})
	}
}

// An entry runs to the next entry's bracket, so what the source wrote between
// them is its text -- including an entry that names a work rather than an
// address, and an entry the tier left empty. Neither is dropped, because a
// dropped entry leaves its marker resolving to nothing.
func TestISWReferencesCarryEntriesThatNameNoAddress(t *testing.T) {
	tail := "[1] https://t.me/rybar/41602 [2] Russia's Zapad-2021 Exercise | Institute" +
		" for the Study of War (understandingwar.org) [3] [4] https://t.me/wargonzo/9526"

	references := parseISWReferences(tail)
	if len(references) != 4 {
		t.Fatalf("parsed %d entries, want 4: %+v", len(references), references)
	}
	byNumber := make(map[int]corpusReference)
	for _, reference := range references {
		byNumber[reference.Number] = reference
	}
	if got := byNumber[2]; len(got.URLs) != 0 ||
		!strings.HasPrefix(got.Text, "Russia's Zapad-2021") {
		t.Errorf("the bibliographic entry lost its text or invented an address: %+v", got)
	}
	if got := byNumber[3]; got.Text != "" || len(got.URLs) != 0 {
		t.Errorf("the empty entry is not carried empty: %+v", got)
	}
	if got := byNumber[4]; len(got.URLs) != 1 {
		t.Errorf("the entry after the empty one lost its address: %+v", got)
	}
}

// The bracketed index inside a Facebook permalink is not an entry, so it
// neither invents entry zero nor truncates the entry carrying the link.
func TestISWReferencesIgnoreABracketedIndexInsideALink(t *testing.T) {
	tail := "[1] https://www.facebook.com/GeneralStaff.ua/posts/pfbid028?__cft__[0]" +
		"=AZWT5akh1meKaLw08dbViXATtycy0dVuw9cN6O [2] https://t.me/rybar/41602"

	references := parseISWReferences(tail)
	if len(references) != 2 {
		t.Fatalf("parsed %d entries, want 2: %+v", len(references), references)
	}
	if references[0].Number != 1 || references[1].Number != 2 {
		t.Errorf("entries are numbered %d and %d, want 1 and 2",
			references[0].Number, references[1].Number)
	}
	if !strings.Contains(references[0].URLs[0], "__cft__[0]") {
		t.Errorf("entry one's address was truncated at the bracketed index: %q",
			references[0].URLs[0])
	}
}

// The tier's longest assessment numbers its entries past a hundred.
func TestISWReferencesReadThreeDigitEntryNumbers(t *testing.T) {
	tail := "[99] https://t.me/rybar/41602 [100] https://t.me/wargonzo/9526" +
		" [105] https://t.me/readovkanews/47978"

	references := parseISWReferences(tail)
	if len(references) != 3 {
		t.Fatalf("parsed %d entries, want 3: %+v", len(references), references)
	}
	if references[2].Number != 105 {
		t.Errorf("the last entry is numbered %d, want 105", references[2].Number)
	}
}

// A tier that carries no tail carries no references, and its envelope encodes
// exactly as it did before assessments gained them.
func TestReferencesAreOmittedFromATierThatCarriesNoTail(t *testing.T) {
	envelope := corpusEnvelope{
		DocumentID: "tg-rbc_news-64950",
		SourceType: sourceTypeOSINTNews,
		Segments: []corpusSegment{
			{Role: segmentRoleBody, Searchable: true, Text: "Fighting continued."},
		},
	}
	if envelope.References != nil {
		t.Fatal("a tier with no tail carries references")
	}
	encoded, err := encodeCorpusYAML(envelope)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(string(encoded), "references") {
		t.Errorf("an envelope with no references encodes the key:\n%s", encoded)
	}
}

// The fixtures prove the rules; this proves they hold over what the corpus
// converts. Every marker the prose cites resolves to an entry the parse
// produced, which is the property the chunk build depends on.
func TestISWReferencesParseOverTheRealDataset(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("CORPUS_DATA_ROOT"))
	if root == "" {
		t.Skip("CORPUS_DATA_ROOT is unset; the source datasets live outside the repository")
	}
	manifest := ukraineTestManifest()

	documents, entries, withoutURL := 0, 0, 0
	for _, line := range readJSONLines(t, filepath.Join(root, "isw", "isw_dailies.jsonl"), 4000) {
		envelope, err := adaptISWRecord(line, manifest)
		if err != nil || !strings.HasPrefix(envelope.Issued, "2022-12") {
			continue
		}
		documents++
		if len(envelope.References) == 0 {
			t.Errorf("%s parsed no references from its tail", envelope.DocumentID)
			continue
		}
		numbers := make(map[int]bool)
		for _, reference := range envelope.References {
			if reference.Number < 1 {
				t.Errorf("%s parsed entry %d, which no tail numbers",
					envelope.DocumentID, reference.Number)
			}
			if numbers[reference.Number] {
				t.Errorf("%s parsed entry %d twice", envelope.DocumentID, reference.Number)
			}
			numbers[reference.Number] = true
			entries++
			if len(reference.URLs) == 0 {
				withoutURL++
			}
		}
		// Every marker the prose cites resolves, which is what #387 relies on.
		for _, segment := range envelope.Segments {
			if !segment.Searchable {
				continue
			}
			for _, match := range iswMarkerPattern.FindAllStringSubmatch(segment.Text, -1) {
				number, err := strconv.Atoi(match[1])
				if err != nil || numbers[number] {
					continue
				}
				t.Errorf("%s cites [%d], which its parsed references do not hold",
					envelope.DocumentID, number)
			}
		}
		if envelope.DocumentID == "isw-2022-12-12" {
			for wanted := 1; wanted <= 105; wanted++ {
				if !numbers[wanted] {
					t.Errorf("isw-2022-12-12 is missing entry %d", wanted)
				}
			}
		}
	}
	if documents != 30 {
		t.Errorf("converted %d December assessments, want 30", documents)
	}
	t.Logf("isw: %d documents, %d entries, %d carrying no address",
		documents, entries, withoutURL)
}

func segmentTextWithRole(segments []corpusSegment, role string) (string, bool) {
	for _, segment := range segments {
		if segment.Role == role {
			return segment.Text, true
		}
	}
	return "", false
}

// readJSONLines returns up to limit lines of a JSON Lines file.
func readJSONLines(t *testing.T, path string, limit int) [][]byte {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Skipf("dataset %s is not readable: %v", path, err)
	}
	defer file.Close()
	lines := make([][]byte, 0, limit)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for scanner.Scan() && len(lines) < limit {
		if len(strings.TrimSpace(scanner.Text())) == 0 {
			continue
		}
		lines = append(lines, append([]byte(nil), scanner.Bytes()...))
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return lines
}

func segmentWithRole(envelope corpusEnvelope, role string) (corpusSegment, bool) {
	for _, segment := range envelope.Segments {
		if segment.Role == role {
			return segment, true
		}
	}
	return corpusSegment{}, false
}

func TestISWEnvelopeCarriesTheParsedReferences(t *testing.T) {
	text := iswAssessmentText(
		"Russian forces struck Kherson Oblast.[1]",
		"[1] https://t.me/rybar/41602", true)
	record, err := json.Marshal(map[string]string{
		"date":  "2022-12-09",
		"title": "Russian Offensive Campaign Assessment, December 9",
		"url":   "https://understandingwar.org/backgrounder/december-9",
		"text":  text,
	})
	if err != nil {
		t.Fatalf("encode record: %v", err)
	}

	envelope, err := adaptISWRecord(record, ukraineTestManifest())
	if err != nil {
		t.Fatalf("adapt: %v", err)
	}
	if len(envelope.References) != 1 {
		t.Fatalf("the envelope carries %d references, want 1", len(envelope.References))
	}
	if envelope.References[0].Number != 1 {
		t.Errorf("the reference lost its number: %+v", envelope.References[0])
	}
	tail, found := segmentTextWithRole(envelope.Segments, segmentRoleEndnotes)
	if !found || !strings.Contains(tail, "https://t.me/rybar/41602") {
		t.Error("the endnote segment no longer carries the tail verbatim; parsing" +
			" reads the tail rather than replacing it")
	}
}
