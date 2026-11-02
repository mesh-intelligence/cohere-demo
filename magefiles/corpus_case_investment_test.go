// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func investmentCaseDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", corpusCasesDir, investmentCaseName)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("case directory: %v", err)
	}
	return dir
}

func investmentTestManifest(t *testing.T) corpusManifest {
	t.Helper()
	manifest, err := readCorpusManifest(investmentCaseDir(t))
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

// A filing document holds reported values in this build's words, and only the
// current period's value, because a filing repeats prior periods for comparison.
func TestInvestmentCaseFilingRendersTheCurrentPeriod(t *testing.T) {
	base := investmentFact{
		Issuer: "Example Energy", Ticker: "EXA", Form: "10-Q", FiscalPer: "Q3", FiscalYr: 2024,
		Accession: "0000000000-24-000001", Filed: "2024-11-04", Unit: "USD",
	}
	prior, current, ytd, assets := base, base, base, base
	prior.Concept, prior.Value, prior.Start, prior.End = "Revenues", 100_000_000_000, "2023-07-01", "2023-09-30"
	current.Concept, current.Value, current.Start, current.End = "Revenues", 90_016_000_000, "2024-07-01", "2024-09-30"
	ytd.Concept, ytd.Value, ytd.Start, ytd.End = "Revenues", 260_000_000_000, "2024-01-01", "2024-09-30"
	assets.Concept, assets.Value, assets.End = "Assets", 461_916_000_000, "2024-09-30"

	envelope, err := adaptInvestmentFiling([]investmentFact{prior, ytd, current, assets}, investmentTestManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	text := envelope.searchableText()
	if !strings.Contains(text, "$90.02 billion for the period 2024-07-01 to 2024-09-30") {
		t.Errorf("the current quarter should be reported:\n%s", text)
	}
	if !strings.Contains(text, "$461.92 billion as of 2024-09-30") {
		t.Errorf("an instant value should read as of its date:\n%s", text)
	}
	if strings.Contains(text, "100.00 billion") || strings.Contains(text, "260.00 billion") {
		t.Errorf("a comparison period reached the document:\n%s", text)
	}
	if envelope.DocumentID != "facts-exa-0000000000-24-000001" {
		t.Errorf("document id %s", envelope.DocumentID)
	}
}

// Taxonomy labels and descriptions in the API response are dropped, and only USD
// values on periodic reports filed in the window survive.
func TestInvestmentCaseTiersSelectFactsAndNoProse(t *testing.T) {
	body := `{"facts":{"us-gaap":{
	  "Revenues":{"label":"TAXONOMY LABEL","description":"TAXONOMY DESCRIPTION","units":{"USD":[
	    {"start":"2024-01-01","end":"2024-03-31","val":5,"accn":"a","fy":2024,"fp":"Q1","form":"10-Q","filed":"2024-05-01"},
	    {"start":"2022-01-01","end":"2022-03-31","val":6,"accn":"b","fy":2022,"fp":"Q1","form":"10-Q","filed":"2022-05-01"},
	    {"start":"2024-01-01","end":"2024-03-31","val":7,"accn":"c","fy":2024,"fp":"Q1","form":"8-K","filed":"2024-05-01"}]}},
	  "EarningsPerShareBasic":{"units":{"USD/shares":[{"end":"2024-03-31","val":1.53,"accn":"a","form":"10-Q","filed":"2024-05-01"}]}}
	}}}`
	facts, err := selectInvestmentFacts(body, "Example", "EXA", "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Accession != "a" || facts[0].Value != 5 {
		t.Fatalf("want the one in-window 10-Q value, got %+v", facts)
	}
}

// A page carrying a third-party rights notice is refused, because EIA's terms say
// licensed material can appear on its pages (srd018 R2.3).
func TestInvestmentCaseTiersRefuseThirdPartyRightsNotices(t *testing.T) {
	listed := investmentListedArticle{ID: "1", Date: "2024-01-02", Title: "T"}
	clean := `<div class="tie-article"><div class="source"><strong>Data source: </strong>Bloomberg, L.P.</div>
	  <p>The price of crude oil averaged a lower level during the year than it did before.</p>
	  <p><strong>Principal contributor: </strong>A Person</p>
	  <p>Text after the contributor line is not part of the article body at all.</p></div>`
	article, ok := parseInvestmentArticle(clean, listed, "https://example.test")
	if !ok || len(article.Paragraphs) != 1 {
		t.Fatalf("want one paragraph, got %+v", article)
	}
	if article.DataSource != "Bloomberg, L.P." {
		t.Errorf("data source %q", article.DataSource)
	}
	marked := strings.Replace(clean, "during the year", "during the year, copyright 2024 Someone,", 1)
	if _, ok := parseInvestmentArticle(marked, listed, "https://example.test"); ok {
		t.Error("an article with a rights notice should be refused")
	}
}

// An article carries the acknowledgment EIA's terms ask for, date included.
func TestInvestmentCaseTiersCarryAttribution(t *testing.T) {
	envelope, err := adaptInvestmentArticle(investmentArticle{
		ID: "1", Date: "2024-01-02", Title: "T", URL: "https://www.eia.gov/x", DataSource: "Bloomberg",
		Paragraphs: []string{"A paragraph of analysis long enough to chunk."},
	}, investmentTestManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	var authority string
	for _, segment := range envelope.Segments {
		if segment.Role == segmentRoleAuthority {
			authority = segment.Text
			if segment.Searchable {
				t.Error("the acknowledgment should stay out of the vectors")
			}
		}
	}
	if !strings.Contains(authority, "Source: U.S. Energy Information Administration (Jan 2024)") ||
		!strings.Contains(authority, "Bloomberg") {
		t.Errorf("acknowledgment incomplete: %q", authority)
	}
}

// Each tier's README records its terms and verdict, and the contact file is ignored.
func TestInvestmentCaseTiersRecordTheirTerms(t *testing.T) {
	dir := investmentCaseDir(t)
	for _, tier := range []string{sourceTypeFilingFacts, sourceTypeEnergyAnalysis, sourceTypeMacroRelease} {
		data, err := os.ReadFile(filepath.Join(dir, corpusCaseDataDir, tier, "README.md"))
		if err != nil {
			t.Fatalf("%s: %v", tier, err)
		}
		for _, want := range []string{"Terms read", "Verdict", "Publishable"} {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s README is missing %q", tier, want)
			}
		}
	}
	if !gitIgnores(t, investmentContactPath) {
		t.Errorf("%s is not ignored; the operator contact must not be tracked", investmentContactPath)
	}
}

// The source data's records are facts, and the tracked files name no operator.
func TestInvestmentCaseTiersTrackedDataHoldsNoProseFields(t *testing.T) {
	dataRoot := filepath.Join(investmentCaseDir(t), corpusCaseDataDir)
	facts, err := readInvestmentJSONL[investmentFact](dataRoot, investmentFactsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) == 0 {
		t.Fatal("no facts tracked")
	}
	raw, err := os.ReadFile(filepath.Join(dataRoot, filepath.FromSlash(investmentFactsPath)))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"label"`, `"description"`, `"text"`} {
		if strings.Contains(string(raw), field) {
			t.Errorf("filing_facts carries a prose field %s", field)
		}
	}
}

func TestInvestmentCaseRatingsComeFromTheManifest(t *testing.T) {
	manifest := investmentTestManifest(t)
	for tier, want := range map[string]string{
		sourceTypeFilingFacts: "A2", sourceTypeEnergyAnalysis: "A2",
		sourceTypeMacroRelease: "A1", sourceTypeAnalystNote: "F6",
	} {
		if got := manifest.TierAdmiralty[tier]; got != want {
			t.Errorf("%s rated %q, want %q", tier, got, want)
		}
	}
}

func TestInvestmentCaseAccessRealTiersSitAtTheLowestLevel(t *testing.T) {
	manifest := investmentTestManifest(t)
	lowest := manifest.ClearanceLevels[0]
	for _, tier := range []string{sourceTypeFilingFacts, sourceTypeEnergyAnalysis, sourceTypeMacroRelease} {
		if got := tierClearance(tier, manifest); got != lowest {
			t.Errorf("%s sits at %q, want %q", tier, got, lowest)
		}
	}
}

// The Board's pages leave a paragraph tag unclosed ahead of the statement. Run
// together, the opening paragraph reads as the release line and is filtered away, and
// every statement silently loses its first paragraph.
func TestInvestmentCaseTiersKeepTheParagraphAfterAnUnclosedTag(t *testing.T) {
	page := `<div id="article"><p class="time">For release at 2:00 p.m. EST
	  <p>Recent indicators suggest that economic activity has been expanding at a solid pace.</p>
	  <p>The Committee seeks to achieve maximum employment and inflation at the rate of 2 percent.</p>
	  <div id="lastUpdate">x</div>`
	release, err := parseInvestmentFOMC(page, "20240131", "https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(release.Paragraphs) != 2 || !strings.HasPrefix(release.Paragraphs[0], "Recent indicators") {
		t.Fatalf("the opening paragraph was lost: %q", release.Paragraphs)
	}
}

// Some articles set the data credit in an unclosed paragraph rather than a source
// block. It stays the article's credit and does not become its first sentence.
func TestInvestmentCaseTiersKeepACreditInAnUnclosedParagraph(t *testing.T) {
	page := `<div class="tie-article"><p>Data source: Vortexa
	  <p>In 2023, Brazil and Saudi Arabia replaced France and Germany among importers.</p></div>`
	article, ok := parseInvestmentArticle(page, investmentListedArticle{ID: "1", Date: "2024-01-02", Title: "T"}, "u")
	if !ok || len(article.Paragraphs) != 1 || !strings.HasPrefix(article.Paragraphs[0], "In 2023") {
		t.Fatalf("paragraphs %q", article.Paragraphs)
	}
	if article.DataSource != "Vortexa" {
		t.Errorf("credit %q", article.DataSource)
	}
}

func investmentFixtureNote() investmentNote {
	return investmentNote{
		ID: "note-x", Date: "2024-03-01", Label: "Internal", Rating: "B2",
		Issuers: []string{"Velmora Energy"}, Title: "A title",
		Paragraphs: []string{"Velmora Energy holds its budget flat this year."},
	}
}

// A note naming a real issuer, ticker, or declared company fails naming both.
func TestInvestmentCaseGuardRefusesRealEntities(t *testing.T) {
	banned, err := investmentBannedEntities(filepath.Join(investmentCaseDir(t), corpusCaseDataDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"Exxon Mobil is preparing an undisclosed acquisition.",
		"EXXON MOBIL CORPORATION plans a deal.",
		"Chevron plans a deal.",
		"XOM is planning a deal.",
		"Halliburton told us about a contract.",
	} {
		note := investmentFixtureNote()
		note.Paragraphs = []string{text}
		err := checkInvestmentNotes([]investmentNote{note}, banned)
		if err == nil {
			t.Errorf("%q should fail the guard", text)
			continue
		}
		if !strings.Contains(err.Error(), "note-x") {
			t.Errorf("the failure should name the note: %v", err)
		}
	}
}

// A ticker that spells a word is matched as written, so ordinary prose survives,
// while a name is matched whole, so a longer word containing it survives too.
func TestInvestmentCaseGuardDoesNotFlagOrdinaryWords(t *testing.T) {
	banned, err := investmentBannedEntities(filepath.Join(investmentCaseDir(t), corpusCaseDataDir))
	if err != nil {
		t.Fatal(err)
	}
	note := investmentFixtureNote()
	note.Paragraphs = []string{"The cop on the beat and an oxy torch went to the eog of the shellfish market at the bpm."}
	if err := checkInvestmentNotes([]investmentNote{note}, banned); err != nil {
		t.Errorf("ordinary words should pass: %v", err)
	}
}

func TestInvestmentCaseGuardRefusesTheLanguageOfAdvice(t *testing.T) {
	note := investmentFixtureNote()
	note.Paragraphs = []string{"We set a price target above the current level."}
	if err := checkInvestmentNotes([]investmentNote{note}, nil); err == nil {
		t.Error("a price target should fail the guard")
	}
}

// The tracked notes pass the guard and identify themselves as invented.
func TestInvestmentCaseGuardAcceptsTheTrackedNotes(t *testing.T) {
	dataRoot := filepath.Join(investmentCaseDir(t), corpusCaseDataDir)
	manifest := investmentTestManifest(t)
	envelopes, err := loadInvestmentNotes(dataRoot, investmentSliceParameters(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(envelopes) == 0 {
		t.Fatal("no notes loaded")
	}
	for _, envelope := range envelopes {
		if !envelope.Synthetic {
			t.Errorf("%s is not marked synthetic", envelope.DocumentID)
		}
		found := false
		for _, segment := range envelope.Segments {
			if segment.Text == investmentNoteNotice {
				found = !segment.Searchable
			}
		}
		if !found {
			t.Errorf("%s does not carry the unsearchable invented notice", envelope.DocumentID)
		}
	}
}

// Each label maps as declared, and an unmapped label is written at the highest level.
func TestInvestmentCaseAccessLabelsMapAndFailClosed(t *testing.T) {
	manifest := investmentTestManifest(t)
	mapping := clearanceMapping{Markings: manifest.ClearanceMarkings}
	for label, want := range map[string]string{
		"Public": "unclassified", "Internal": "restricted",
		"Restricted list": "confidential", "Wall-crossed": "secret", "Board only": "secret", "": "secret",
	} {
		note := investmentFixtureNote()
		note.Label = label
		envelope, err := adaptInvestmentNote(note, mapping, manifest)
		if err != nil {
			t.Fatal(err)
		}
		if envelope.Clearance != want {
			t.Errorf("label %q wrote level %q, want %q", label, envelope.Clearance, want)
		}
	}
	if len(manifest.AccessLevelLabels) != len(manifest.ClearanceLevels) {
		t.Errorf("every level needs a label: %v", manifest.AccessLevelLabels)
	}
}

func TestInvestmentCaseRatingsFallBackToCannotBeJudged(t *testing.T) {
	manifest := investmentTestManifest(t)
	note := investmentFixtureNote()
	note.Rating = ""
	envelope, err := adaptInvestmentNote(note, clearanceMapping{Markings: manifest.ClearanceMarkings}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Reliability != "F6" {
		t.Errorf("an unrated note carries %q, want F6", envelope.Reliability)
	}
}

// The retrieval conditions hold over the built store (srd018 R4.4): every level has a
// retrievable document, so every access pair has something to withhold; the declared
// tasking's relevant material spans tiers and exceeds what a turn composes from; and
// real tiers sit at the lowest level.
func TestInvestmentCaseConditionsHoldOverTheBuiltStore(t *testing.T) {
	root := investmentCaseDir(t)
	manifest := investmentTestManifest(t)
	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) == 0 {
		t.Fatal("the case store holds no documents")
	}
	const identity = "embed-v4.0"
	retrievable := func(document corpusStoredDocument) bool {
		for _, chunk := range document.Chunks {
			if _, held := chunk.vector(identity); held {
				return true
			}
		}
		return false
	}
	byLevel := map[string]int{}
	for id, document := range documents {
		if !retrievable(document) {
			continue
		}
		byLevel[document.Envelope.Clearance]++
		if document.Envelope.SourceType != sourceTypeAnalystNote && document.Envelope.Clearance != manifest.ClearanceLevels[0] {
			t.Errorf("%s is a real document above the lowest level", id)
		}
		if corpusDocumentKeepPrivate(id) {
			t.Errorf("%s carries a keep-private prefix in a case store", id)
		}
	}
	for _, level := range manifest.ClearanceLevels {
		if byLevel[level] == 0 {
			t.Errorf("no retrievable document at level %q; that access pair withholds nothing", level)
		}
	}
	relevant := manifest.RetrievalConditions.RelevantToPrimaryTaskng
	const compositionTopN = 5
	if len(relevant) <= compositionTopN {
		t.Errorf("%d relevant documents do not exceed the %d a turn composes from", len(relevant), compositionTopN)
	}
	tiers := map[string]bool{}
	for _, id := range relevant {
		document, held := documents[id]
		if !held {
			t.Errorf("relevant document %s is not in the store", id)
			continue
		}
		if !retrievable(document) {
			t.Errorf("relevant document %s has no embedded chunk, so retrieval cannot reach it", id)
		}
		tiers[document.Envelope.SourceType] = true
	}
	if len(tiers) < 3 {
		t.Errorf("relevant documents span %d tiers, want at least 3: %v", len(tiers), tiers)
	}
}

// Every store record of a note identifies the corpus's mixed nature in the artifact,
// and a note's chunks are marked synthetic while a real tier's are not.
func TestInvestmentCaseConditionsMarkInventedRecords(t *testing.T) {
	documents, err := loadCorpusDocumentStore(investmentCaseDir(t))
	if err != nil {
		t.Fatal(err)
	}
	for id, document := range documents {
		invented := document.Envelope.SourceType == sourceTypeAnalystNote
		if document.Envelope.Synthetic != invented {
			t.Errorf("%s: synthetic=%v, invented=%v", id, document.Envelope.Synthetic, invented)
		}
	}
	index, err := readCorpusStoreIndex(investmentCaseDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(strings.Fields(index.SyntheticNotice), " "), "not investment advice") {
		t.Error("the store's notice should carry the no-advice statement")
	}
}

type investmentMemoSchema struct {
	ID       string `yaml:"id"`
	Version  string `yaml:"version"`
	Sections []struct {
		ID       string   `yaml:"id"`
		Label    string   `yaml:"label"`
		Required bool     `yaml:"required"`
		Headings []string `yaml:"headings"`
	} `yaml:"sections"`
}

type investmentReportValues struct {
	Chatbot struct {
		Report struct {
			Instruction     string `yaml:"instruction"`
			TemplateVersion string `yaml:"templateVersion"`
			Sections        map[string]struct {
				Headings string `yaml:"headings"`
				Label    string `yaml:"label"`
			} `yaml:"sections"`
		} `yaml:"report"`
	} `yaml:"chatbot"`
}

// The values file binds what the schema declares, so the check and the schema cannot
// disagree about what a memo is, and a verdict names the schema (srd018 R6.1, R6.3).
func TestInvestmentCaseMemoValuesMatchTheSchema(t *testing.T) {
	var schema investmentMemoSchema
	data, err := os.ReadFile(filepath.Join(investmentCaseDir(t), "memo-schema.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	var values investmentReportValues
	data, err = os.ReadFile(filepath.Join(findChartDir(t), "ci", investmentValuesFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &values); err != nil {
		t.Fatal(err)
	}
	report := values.Chatbot.Report
	if want := schema.ID + "@" + schema.Version; report.TemplateVersion != want {
		t.Errorf("templateVersion %q, want %q", report.TemplateVersion, want)
	}
	for _, section := range schema.Sections {
		bound, ok := report.Sections[section.ID]
		if !ok {
			t.Errorf("values bind no section %s", section.ID)
			continue
		}
		if want := strings.Join(section.Headings, "|"); bound.Headings != want {
			t.Errorf("section %s headings %q, want %q", section.ID, bound.Headings, want)
		}
		if bound.Label != section.Label {
			t.Errorf("section %s label %q, want %q", section.ID, bound.Label, section.Label)
		}
		if !strings.Contains(report.Instruction, section.Label) && !strings.Contains(strings.Join(strings.Fields(report.Instruction), " "), section.Label) {
			t.Errorf("the instruction does not name %q", section.Label)
		}
	}
	lowered := strings.ToLower(report.Instruction)
	if !strings.Contains(lowered, "not investment advice") || !strings.Contains(lowered, "do not recommend") {
		t.Error("the instruction must state it is not advice and forbid a recommendation (srd018 R8.4)")
	}
}

// The case overlay differs from the defense overlay only in the report and the
// corpus description, so it cannot drift into a different deployment.
func TestInvestmentCaseValuesDifferFromKindOnlyInTheBinding(t *testing.T) {
	load := func(name string) map[string]interface{} {
		data, err := os.ReadFile(filepath.Join(findChartDir(t), "ci", name))
		if err != nil {
			t.Fatal(err)
		}
		var values map[string]interface{}
		if err := yaml.Unmarshal(data, &values); err != nil {
			t.Fatal(err)
		}
		delete(values["chatbot"].(map[string]interface{}), "report")
		for _, unit := range values["ragUnits"].([]interface{}) {
			delete(unit.(map[string]interface{}), "description")
		}
		return values
	}
	if !reflect.DeepEqual(load(chatbotDemoValuesFile), load(investmentValuesFile)) {
		t.Errorf("%s has drifted from %s beyond the report and the corpus description",
			investmentValuesFile, chatbotDemoValuesFile)
	}
}

func investmentRenderedConformance(t *testing.T, extra ...string) string {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	staged, cleanup, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
	if err != nil {
		t.Fatalf("stage chart: %v", err)
	}
	t.Cleanup(cleanup)
	// The composition instruction renders only on the Cohere tier.
	args := append([]string{"template", "relx", staged, "--namespace", "nsy",
		"--set", "cohere.enabled=true", "--set", "cohere.existingSecret=cohere"}, extra...)
	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	return string(out)
}

// A default render is the packaged declarations, so the defense profile is untouched.
func TestInvestmentCaseMemoDefaultRenderKeepsTheDefenseTemplate(t *testing.T) {
	out := investmentRenderedConformance(t)
	for _, want := range []string{
		"(?:Bottom Line Up Front|BLUF|Key Judgements|Key Judgments)",
		`"template_version": "1"`,
		"You compose a command-staff intelligence brief from the",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the default render lost %q", want)
		}
	}
}

// The case's render binds the memo through the same chain, leaving no defense heading
// in a pattern, a defect, or the version a verdict names.
func TestInvestmentCaseMemoRenderBindsTheChain(t *testing.T) {
	out := investmentRenderedConformance(t, "-f", filepath.Join(findChartDir(t), "ci", investmentValuesFile))
	for _, want := range []string{
		"(?:Investment Thesis|Thesis|Summary)",
		"(?:Analysis|Sector Analysis)",
		"(?:Risks and Gaps|Risks|Gaps)",
		"required section Investment Thesis",
		"template order: Investment Thesis, then Analysis, then Risks and Gaps",
		`"template_version": "investment-committee-memo-markdown@1"`,
		"You compose a first-draft investment-committee memo",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the case render lost %q", want)
		}
	}
	for _, absent := range []string{
		"(?:Bottom Line Up Front|BLUF",
		"required section Bottom Line Up Front",
		`"template_version": "1"`,
		"You compose a command-staff intelligence brief from the",
	} {
		if strings.Contains(out, absent) {
			t.Errorf("the case render still carries %q", absent)
		}
	}
}

// Selecting the case picks its overlay, and nothing selected keeps the defense one.
func TestInvestmentCaseValuesSelection(t *testing.T) {
	t.Setenv(corpusCaseEnv, "")
	if got := corpusCaseValuesFile(); got != chatbotDemoValuesFile {
		t.Errorf("no case selected the overlay %s", got)
	}
	t.Setenv(corpusCaseEnv, investmentCaseName)
	if got := corpusCaseValuesFile(); got != investmentValuesFile {
		t.Errorf("the case selected the overlay %s", got)
	}
}

func investmentTracerFixture(t *testing.T) (map[string]corpusStoredDocument, investmentMemoChecks, []string) {
	t.Helper()
	sources := map[string]corpusStoredDocument{
		"eia-1-medium-900-0001":  {DocumentID: "eia-1", Envelope: corpusEnvelope{SourceType: sourceTypeEnergyAnalysis, Clearance: "unclassified"}},
		"note-1-medium-900-0001": {DocumentID: "note-1", Envelope: corpusEnvelope{SourceType: sourceTypeAnalystNote, Clearance: "secret"}},
	}
	checks, err := loadInvestmentMemoChecks(investmentCaseDir(t))
	if err != nil {
		t.Fatal(err)
	}
	return sources, checks, []string{"unclassified", "restricted", "confidential", "secret"}
}

const investmentFixtureMemo = "# Investment Thesis\nA thesis.\n\n# Analysis\nAn argument.\n\n# Risks and Gaps\n" +
	"A gap.\n\nDemonstration over a small corpus mixing public and invented material; not investment advice.\n"

func investmentFixtureTurn() investmentTurnFacts {
	return investmentTurnFacts{
		Level: "unclassified", AnsweredBy: "cohere-command", Answer: investmentFixtureMemo,
		Composed: []string{"eia-1-medium-900-0001"}, Citations: []string{"eia-1-medium-900-0001"},
		Verdict: &conformanceVerdict{Verdict: "conformant", TemplateVersion: investmentTemplateName},
	}
}

func TestInvestmentCaseTracerAcceptsAGoodTurn(t *testing.T) {
	sources, checks, levels := investmentTracerFixture(t)
	if err := assertInvestmentTurn("leg", investmentFixtureTurn(), sources, checks, levels); err != nil {
		t.Fatal(err)
	}
}

// Each way a turn can fail the case is refused, naming what went wrong.
func TestInvestmentCaseTracerRefusesBadTurns(t *testing.T) {
	sources, checks, levels := investmentTracerFixture(t)
	for name, mutate := range map[string]func(*investmentTurnFacts){
		"degraded tier":    func(turn *investmentTurnFacts) { turn.AnsweredBy = "ollama-tier" },
		"composed nothing": func(turn *investmentTurnFacts) { turn.Composed = nil },
		"another corpus":   func(turn *investmentTurnFacts) { turn.Composed = []string{"isw-2022-12-01-medium-900-0001"} },
		"above the level":  func(turn *investmentTurnFacts) { turn.Composed = []string{"note-1-medium-900-0001"} },
		"no verdict":       func(turn *investmentTurnFacts) { turn.Verdict = nil },
		"defense template": func(turn *investmentTurnFacts) { turn.Verdict.TemplateVersion = "1" },
		"untrue conformant": func(turn *investmentTurnFacts) {
			turn.Answer = strings.Replace(investmentFixtureMemo, "# Analysis", "# Other", 1)
		},
		"untrue nonconform": func(turn *investmentTurnFacts) { turn.Verdict.Verdict = "non_conformant" },
		"a recommendation":  func(turn *investmentTurnFacts) { turn.Answer += "\nWe recommend a position.\n" },
		"cites nothing":     func(turn *investmentTurnFacts) { turn.Citations = nil },
		"cites elsewhere":   func(turn *investmentTurnFacts) { turn.Citations = []string{"isw-1"} },
		"cites above level": func(turn *investmentTurnFacts) { turn.Citations = []string{"note-1-medium-900-0001"} },
		"no qualification": func(turn *investmentTurnFacts) {
			turn.Answer = strings.Replace(investmentFixtureMemo, "not investment advice", "a memo", 1)
		},
	} {
		turn := investmentFixtureTurn()
		mutate(&turn)
		if err := assertInvestmentTurn("leg", turn, sources, checks, levels); err == nil {
			t.Errorf("%s should be refused", name)
		}
	}
}

// A cleared requester may reach the note, and the helper names it for the pair.
func TestInvestmentCaseTracerSeesNotesAtTheHighestLevel(t *testing.T) {
	sources, checks, levels := investmentTracerFixture(t)
	turn := investmentFixtureTurn()
	turn.Level, turn.Composed = "secret", []string{"note-1-medium-900-0001"}
	if err := assertInvestmentTurn("leg", turn, sources, checks, levels); err != nil {
		t.Fatal(err)
	}
	if notes := investmentComposedNotes(turn.Composed, sources); len(notes) != 1 || notes[0] != "note-1" {
		t.Errorf("notes %v", notes)
	}
}

// A refusal is correct behavior when the documents do not answer: it owes no citations
// or memo footer, as long as its verdict tells the truth.
func TestInvestmentCaseTracerAcceptsARefusal(t *testing.T) {
	sources, checks, levels := investmentTracerFixture(t)
	turn := investmentFixtureTurn()
	turn.Answer = "I could not find information on that."
	turn.Citations = nil
	turn.Verdict.Verdict = "non_conformant"
	if err := assertInvestmentTurn("leg", turn, sources, checks, levels); err != nil {
		t.Fatal(err)
	}
}
