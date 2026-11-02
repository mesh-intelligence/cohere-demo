// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The rel10.5 slice suite. It proves test-suite case (a), the window filter and
// its boundaries, and the sampling half of case (h), against a dataset root
// written per test rather than against the real files: the datasets are 343MB
// and live outside the repository, so a suite that required them would be
// unrunnable on a clone.

// writeSliceFixture builds a dataset root holding a few records per tier,
// including the days either side of the window so the boundary cases have
// something to exclude.
func writeSliceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"humint", "isw", "ukraine_news", "missile_attacks", "war_losses"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}

	humint := []string{
		humintFixtureOn("HR-OUT-EARLY", "2022-06-30"),
		humintFixtureOn("HR-IN-OPEN", sliceWindowStart),
		humintFixtureOn("HR-IN-CLOSE", sliceWindowEnd),
		humintFixtureOn("HR-OUT-LATE", "2023-07-01"),
	}
	writeFixtureFile(t, filepath.Join(root, humintDatasetPath), strings.Join(humint, "\n"))

	isw := []string{
		iswFixtureOn("2022-06-30"),
		iswFixtureOn(sliceWindowStart),
		iswFixtureOn(sliceWindowEnd),
		iswFixtureOn("2023-07-01"),
	}
	writeFixtureFile(t, filepath.Join(root, iswDatasetPath), strings.Join(isw, "\n"))

	// Two channels, four messages on one in-window day, plus a message either
	// side of the window. The per-channel rule has something to drop and the
	// window filter has something to exclude.
	news := strings.Join([]string{
		"id,group_id,group_name,text,date",
		`1,tass_agency,TASS,"short",2022-06-30`,
		`2,tass_agency,TASS,"aaaa",` + sliceWindowStart,
		`3,tass_agency,TASS,"aaaaaaaa",` + sliceWindowStart,
		`4,tass_agency,TASS,"aa",` + sliceWindowStart,
		`5,meduzalive,Meduza,"bbbbbb",` + sliceWindowStart,
		`6,tass_agency,TASS,"late",2023-07-01`,
	}, "\n")
	writeFixtureFile(t, filepath.Join(root, telegramDatasetPath), news)

	// Three rows on one in-window day, one out of window. The day folds to one
	// digest naming all three rows.
	missile := strings.Join([]string{
		"time_start,time_end,model,launch_place,target,launched,destroyed,affected region",
		"2022-06-30 06:00,2022-06-30 08:00,Kalibr,Black Sea,Ukraine,4.0,2.0,Odesa",
		sliceWindowStart + " 06:00," + sliceWindowStart + " 08:00,Shahed-136,Crimea,Ukraine,10.0,8.0,Kyiv",
		sliceWindowStart + " 09:00," + sliceWindowStart + " 10:00,Kalibr,Black Sea,Ukraine,4.0,3.0,Odesa",
		sliceWindowStart + " 11:00," + sliceWindowStart + " 12:00,Iskander,Belgorod,Ukraine,2.0,0.0,Kharkiv",
	}, "\n")
	writeFixtureFile(t, filepath.Join(root, missileDatasetPath), missile)

	// Descending date order, as the real files carry it, with the day before the
	// window so the first in-window delta has a predecessor.
	personnel := strings.Join([]string{
		"date,day,personnel,personnel*,POW",
		"2023-07-01,494,240000,240000,",
		sliceWindowEnd + ",493,239000,239000,",
		sliceWindowStart + ",128,36000,36000,",
		"2022-06-30,127,35800,35800,",
	}, "\n")
	writeFixtureFile(t, filepath.Join(root, lossesPersonnelPath), personnel)
	equipment := strings.Join([]string{
		"date,day,tank,APC,field artillery,MRL,drone,aircraft,helicopter,naval ship,cruise missiles",
		"2023-07-01,494,4100,8000,4200,650,3600,315,310,18,1300",
		sliceWindowEnd + ",493,4090,7990,4190,649,3590,315,310,18,1295",
		sliceWindowStart + ",128,1600,3800,800,250,700,220,188,15,150",
		"2022-06-30,127,1590,3790,795,249,695,220,188,15,148",
	}, "\n")
	writeFixtureFile(t, filepath.Join(root, lossesEquipmentPath), equipment)
	return root
}

func humintFixtureOn(id, date string) string {
	return fmt.Sprintf(`{"id":%q,"date":%q,"classification":"CONFIDENTIAL",`+
		`"source_id":"GRANITE","reliability":"B3","collector":"DEFTECH STATION KYIV",`+
		`"title":"Rail node status","related_messages":[],"tags":[],`+
		`"text":"1. (C) SUMMARY: The rail node remained in use. END SUMMARY."}`, id, date)
}

// iswFixtureOn writes an assessment whose body names its own date. Two
// assessments carrying identical prose would each be embedded on their own
// merits, which is correct and makes a case about re-embedding impossible to
// state, so the fixture reads like the source it stands in for.
func iswFixtureOn(date string) string {
	return fmt.Sprintf(`{"date":%q,"title":"Russian Offensive Campaign Assessment",`+
		`"url":"assessment-%s","text":"Karolina Hird and Mason Clark 8:30pm ET `+
		`Russian forces conducted limited ground attacks near the settlement on %s. `+
		`[1] https://t.me/mod_russia/23394"}`, date, date, date)
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func loadFixtureSlice(t *testing.T, params corpusSliceParameters) ([]corpusEnvelope, corpusSliceReport) {
	t.Helper()
	root := writeSliceFixture(t)
	envelopes, report, err := loadCorpusSlice(
		root, params, ukraineTestClearanceMapping(), ukraineTestManifest())
	if err != nil {
		t.Fatalf("load slice: %v", err)
	}
	return envelopes, report
}

// Case (a). The window is inclusive at both ends, and a document issued the day
// before it opens or the day after it closes is absent from the slice entirely.
// The boundary days are asserted by identifier rather than by count, because an
// off-by-one at one end and a compensating error at the other leaves the totals
// right (srd017 R3.1, rel10.5-uc001 S2).
func TestSliceWindowAdmitsItsBoundsAndDropsTheDaysEitherSide(t *testing.T) {
	envelopes, _ := loadFixtureSlice(t, defaultCorpusSliceParameters())
	issued := make(map[string]bool)
	ids := make(map[string]bool)
	for _, envelope := range envelopes {
		issued[envelope.Issued] = true
		ids[envelope.DocumentID] = true
	}
	for _, bound := range []string{sliceWindowStart, sliceWindowEnd} {
		if !issued[bound] {
			t.Errorf("no document issued on the inclusive bound %s survived the window", bound)
		}
	}
	for _, outside := range []string{"2022-06-30", "2023-07-01"} {
		if issued[outside] {
			t.Errorf("a document issued on %s survived the window %s..%s",
				outside, sliceWindowStart, sliceWindowEnd)
		}
	}
	for _, id := range []string{"HR-IN-OPEN", "HR-IN-CLOSE"} {
		if !ids[id] {
			t.Errorf("in-window document %s is absent from the slice", id)
		}
	}
	for _, id := range []string{"HR-OUT-EARLY", "HR-OUT-LATE"} {
		if ids[id] {
			t.Errorf("out-of-window document %s survived the window", id)
		}
	}
}

// The window reads the issued date each source carries rather than a date the
// loader assigned, so a tier whose file states dates in another layout is
// filtered on the same value the envelope records (srd017 R1.5, R3.1).
func TestSliceWindowFiltersOnTheIssuedDateTheSourceCarries(t *testing.T) {
	envelopes, _ := loadFixtureSlice(t, defaultCorpusSliceParameters())
	for _, envelope := range envelopes {
		if envelope.Issued < sliceWindowStart || envelope.Issued > sliceWindowEnd {
			t.Errorf("document %s carries issued %s, outside the window",
				envelope.DocumentID, envelope.Issued)
		}
	}
}

// Case (h), sampling half. The news tier is bounded per channel per day and by
// a total cap, and the rule keeps the longest messages of each channel so the
// selection favours reporting over one-line headlines (srd017 R3.5).
func TestSliceSamplesTheNewsTierPerChannelPerDay(t *testing.T) {
	params := defaultCorpusSliceParameters()
	envelopes, report := loadFixtureSlice(t, params)
	perChannelDay := make(map[string]int)
	kept := make(map[string]bool)
	for _, envelope := range envelopes {
		if envelope.SourceType != sourceTypeOSINTNews {
			continue
		}
		perChannelDay[envelope.Origin+"|"+envelope.Issued]++
		kept[envelope.DocumentID] = true
	}
	for key, count := range perChannelDay {
		if count > params.TelegramPerChannelPerDay {
			t.Errorf("%s kept %d messages, above the declared %d per channel per day",
				key, count, params.TelegramPerChannelPerDay)
		}
	}
	// TASS carries three in-window messages on the opening day, of 8, 4 and 2
	// runes. At one per channel per day the longest is kept and the rest
	// dropped, which is the rule: prefer reporting over a headline.
	if !kept["tg-tass_agency-3"] {
		t.Errorf("the sampler did not keep the longest message: kept %v", kept)
	}
	for _, shorter := range []string{"tg-tass_agency-2", "tg-tass_agency-4"} {
		if kept[shorter] {
			t.Errorf("the sampler kept %s over a longer message from the same"+
				" channel on the same day", shorter)
		}
	}
	if report.TelegramCandidate <= report.TelegramSampled {
		t.Errorf("the fixture offered %d candidates and the sampler kept %d;"+
			" the case proves nothing unless it dropped something",
			report.TelegramCandidate, report.TelegramSampled)
	}
}

// The total cap bounds the tier whatever the per-channel rule would let
// through, so a change to one rule cannot silently widen the tier past the
// other.
func TestSliceNewsTotalCapBoundsTheTier(t *testing.T) {
	params := defaultCorpusSliceParameters()
	params.TelegramCap = 1
	envelopes, _ := loadFixtureSlice(t, params)
	news := 0
	for _, envelope := range envelopes {
		if envelope.SourceType == sourceTypeOSINTNews {
			news++
		}
	}
	if news != 1 {
		t.Errorf("the tier holds %d messages against a declared cap of 1", news)
	}
}

// The two row-oriented tiers arrive as one envelope per day rather than one per
// row, and the digest names every row it absorbed so a reader reaches the rows
// without finding them itself (srd017 R3.4, R5.4).
func TestSliceAggregatesRowTiersIntoOneEnvelopePerDay(t *testing.T) {
	envelopes, report := loadFixtureSlice(t, defaultCorpusSliceParameters())
	strikes := envelopesOfTier(envelopes, sourceTypeSIGINTEvent)
	if len(strikes) != 1 {
		t.Fatalf("three in-window attack rows on one day produced %d envelopes, want 1",
			len(strikes))
	}
	if report.SIGINTEvent != 1 {
		t.Errorf("the report counts %d strike digests, want 1", report.SIGINTEvent)
	}
	digest := strikes[0]
	if len(digest.Provenance.DerivedFrom) != 3 {
		t.Errorf("the digest names %d source rows, want the 3 it aggregated",
			len(digest.Provenance.DerivedFrom))
	}
	text := digest.searchableText()
	for _, model := range []string{"Shahed-136", "Kalibr", "Iskander"} {
		if !strings.Contains(text, model) {
			t.Errorf("the digest does not state %s: %s", model, text)
		}
	}

	losses := envelopesOfTier(envelopes, sourceTypeSIGINTSeries)
	if len(losses) != 2 {
		t.Fatalf("two in-window loss days produced %d envelopes, want 2", len(losses))
	}
	for _, envelope := range losses {
		if count := strings.Count(envelope.DocumentID, "personnel"); count > 0 {
			t.Errorf("the loss tier emits a per-series document %s rather than one"+
				" envelope per day", envelope.DocumentID)
		}
	}
}

// The loss files are cumulative totals in descending date order, so the delta is
// taken after sorting ascending. A delta computed in the file's own order is
// the negation of the day's reporting, which reads as a plausible figure with
// the wrong sign.
func TestSliceLossDeltaIsTakenInAscendingDateOrder(t *testing.T) {
	envelopes, _ := loadFixtureSlice(t, defaultCorpusSliceParameters())
	for _, envelope := range envelopesOfTier(envelopes, sourceTypeSIGINTSeries) {
		if envelope.Issued != sliceWindowStart {
			continue
		}
		// The fixture reports 35,800 on the day before the window and 36,000 on
		// its opening day, so the day's change is +200 rather than -200.
		text := envelope.searchableText()
		if !strings.Contains(text, "+200") {
			t.Errorf("the opening day's personnel change is not +200: %s", text)
		}
		if strings.Contains(text, "-200") {
			t.Errorf("the delta was taken in the file's descending order: %s", text)
		}
		return
	}
	t.Fatal("no loss envelope was issued on the opening day of the window")
}

// The day before the window supplies the first day's difference without being
// converted itself, so bounding what the corpus holds does not cost the first
// day its delta.
func TestSliceFirstWindowDayTakesItsDeltaFromOutsideTheWindow(t *testing.T) {
	envelopes, _ := loadFixtureSlice(t, defaultCorpusSliceParameters())
	for _, envelope := range envelopesOfTier(envelopes, sourceTypeSIGINTSeries) {
		if envelope.Issued == "2022-06-30" {
			t.Error("the out-of-window day used for the first delta was itself converted")
		}
	}
}

// Case (h), determinism half. Two runs over unchanged inputs return
// byte-identical lists, which is what makes a rebuild idempotent rather than
// merely equivalent (srd017 R3.5, rel10.5-uc001 S2).
func TestSliceOrderingIsDeterministicAcrossRuns(t *testing.T) {
	root := writeSliceFixture(t)
	params := defaultCorpusSliceParameters()
	first, _, err := loadCorpusSlice(root, params, ukraineTestClearanceMapping(), ukraineTestManifest())
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	second, _, err := loadCorpusSlice(root, params, ukraineTestClearanceMapping(), ukraineTestManifest())
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("two runs produced %d and %d envelopes", len(first), len(second))
	}
	for index := range first {
		if first[index].DocumentID != second[index].DocumentID {
			t.Fatalf("position %d holds %s on the first run and %s on the second;"+
				" the ordering is decided by map iteration rather than by the data",
				index, first[index].DocumentID, second[index].DocumentID)
		}
		if first[index].searchableText() != second[index].searchableText() {
			t.Errorf("document %s carries different text across runs",
				first[index].DocumentID)
		}
	}
}

// The order is total, so no two documents tie. A tie would leave the sort free
// to order them either way and the determinism above would hold only by luck.
func TestSliceOrderingIsTotal(t *testing.T) {
	envelopes, _ := loadFixtureSlice(t, defaultCorpusSliceParameters())
	seen := make(map[string]bool, len(envelopes))
	for _, envelope := range envelopes {
		key := envelope.SourceType + "|" + envelope.Issued + "|" + envelope.DocumentID
		if seen[key] {
			t.Errorf("two documents share the sort key %s", key)
		}
		seen[key] = true
	}
	for index := 1; index < len(envelopes); index++ {
		previous, current := envelopes[index-1], envelopes[index]
		if previous.SourceType > current.SourceType {
			t.Errorf("tier order breaks at position %d: %s before %s",
				index, previous.SourceType, current.SourceType)
		}
	}
}

// The report states what each tier contributed, so a build says what it read
// rather than leaving the counts to be recovered from the store.
func TestSliceReportCountsEveryTier(t *testing.T) {
	envelopes, report := loadFixtureSlice(t, defaultCorpusSliceParameters())
	if report.total() != len(envelopes) {
		t.Errorf("the report totals %d against %d envelopes", report.total(), len(envelopes))
	}
	if report.Humint != 2 {
		t.Errorf("the report counts %d humint reports, want the 2 in window", report.Humint)
	}
	if report.OSINTAssessment != 2 {
		t.Errorf("the report counts %d assessments, want the 2 in window",
			report.OSINTAssessment)
	}
}

// The per-tier counts the use case states, measured against the real datasets
// when CORPUS_DATA_ROOT names them and skipped when it does not. The fixtures
// above prove the rules; only the datasets prove the numbers, and the numbers
// are what rel10.5-uc001 S2 commits to.
func TestSliceRealPerTierCountsWhenTheDatasetsArePresent(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("CORPUS_DATA_ROOT"))
	if root == "" {
		t.Skip("CORPUS_DATA_ROOT is unset; the source datasets live outside the repository")
	}
	params := defaultCorpusSliceParameters()
	envelopes, report, err := loadCorpusSlice(
		root, params, ukraineTestClearanceMapping(), ukraineTestManifest())
	if err != nil {
		if strings.Contains(err.Error(), "publication date") {
			t.Skipf("the ISW dataset still carries the record whose title date"+
				" contradicts its date field (GH-274); the loader refuses it, which"+
				" is the required behaviour: %v", err)
		}
		t.Fatalf("load the real slice: %v", err)
	}
	counts := map[string]int{
		"humint reports":       report.Humint,
		"ISW assessments":      report.OSINTAssessment,
		"strike daily digests": report.SIGINTEvent,
		"loss daily deltas":    report.SIGINTSeries,
	}
	// The synthetic tier spans the year; the rest span the declared month.
	want := map[string]int{
		"humint reports":       27,
		"ISW assessments":      30,
		"strike daily digests": 11,
		"loss daily deltas":    31,
	}
	for tier, got := range counts {
		if got != want[tier] {
			t.Errorf("the window holds %d %s, want %d", got, tier, want[tier])
		}
	}
	if report.OSINTNews > params.TelegramCap {
		t.Errorf("the news tier holds %d messages against a declared cap of %d",
			report.OSINTNews, params.TelegramCap)
	}
	if report.TelegramCandidate < report.OSINTNews {
		t.Errorf("the sampler kept %d of %d candidates",
			report.OSINTNews, report.TelegramCandidate)
	}
	if report.total() != len(envelopes) {
		t.Errorf("the report totals %d against %d envelopes", report.total(), len(envelopes))
	}
	t.Logf("window %s..%s holds %d documents: humint %d, assessments %d,"+
		" news %d sampled from %d, strikes %d, losses %d",
		params.WindowStart, params.WindowEnd, len(envelopes), report.Humint,
		report.OSINTAssessment, report.OSINTNews, report.TelegramCandidate,
		report.SIGINTEvent, report.SIGINTSeries)
}

// A slice carrying two documents under one identifier fails the build.
//
// This is not a hypothetical. The Telegram tier numbers messages per channel,
// so the message number repeats across channels -- over a thousand collisions
// in a single month of the window. Keyed on the number alone, one channel's
// message silently overwrote another's in the store, the store held fewer
// documents than the loader reported, and a rebuild was not idempotent because
// which message won depended on write order.
func TestSliceRefusesTwoDocumentsUnderOneIdentifier(t *testing.T) {
	first := corpusEnvelope{
		DocumentID: "tg-70354", SourceType: sourceTypeOSINTNews, Issued: "2022-10-04",
		RawRef: corpusRawRef{Store: "ukraine_news", ID: "70354"},
	}
	second := first
	second.RawRef.ID = "70354-other"
	err := assertUniqueDocumentIDs([]corpusEnvelope{first, second})
	if err == nil {
		t.Fatal("two documents sharing an identifier were accepted")
	}
	if !strings.Contains(err.Error(), "tg-70354") {
		t.Errorf("the error does not name the identifier: %v", err)
	}
	if err := assertUniqueDocumentIDs([]corpusEnvelope{first}); err != nil {
		t.Errorf("a slice with no collision was rejected: %v", err)
	}
}

// The news identifier carries the channel as well as the message number, which
// is what makes it unique across the tier.
func TestNewsDocumentIdentifierCarriesTheChannel(t *testing.T) {
	manifest := ukraineTestManifest()
	rows := []map[string]string{
		{"id": "70354", "group_id": "tass_agency", "group_name": "TASS", "text": "one", "date": "2022-10-04"},
		{"id": "70354", "group_id": "rian_ru", "group_name": "RIA", "text": "two", "date": "2022-10-04"},
	}
	envelopes := make([]corpusEnvelope, 0, len(rows))
	for _, row := range rows {
		envelope, err := adaptTelegramRow(row, manifest)
		if err != nil {
			t.Fatalf("adapt %s: %v", row["group_id"], err)
		}
		envelopes = append(envelopes, envelope)
	}
	if envelopes[0].DocumentID == envelopes[1].DocumentID {
		t.Errorf("two channels' message 70354 share the identifier %s",
			envelopes[0].DocumentID)
	}
	if err := assertUniqueDocumentIDs(envelopes); err != nil {
		t.Errorf("the two messages collide: %v", err)
	}
}

func envelopesOfTier(envelopes []corpusEnvelope, sourceType string) []corpusEnvelope {
	matched := make([]corpusEnvelope, 0, len(envelopes))
	for _, envelope := range envelopes {
		if envelope.SourceType == sourceType {
			matched = append(matched, envelope)
		}
	}
	return matched
}
