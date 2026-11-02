// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The investment-research case (srd018): US energy-sector research over numeric
// issuer facts, government analysis and releases, and invented analyst notes. It is
// a demonstration over a small corpus and it is not investment advice.

const (
	investmentCaseName = "investment-research"

	sourceTypeFilingFacts    = "filing_facts"
	sourceTypeEnergyAnalysis = "energy_analysis"
	sourceTypeMacroRelease   = "macro_release"
	sourceTypeAnalystNote    = "analyst_note"

	investmentValuesFile  = "investment-research-values.yaml"
	investmentWindowStart = "2024-01-01"
	investmentWindowEnd   = "2024-12-31"

	investmentFactsPath    = "filing_facts/facts.jsonl"
	investmentAnalysisPath = "energy_analysis/articles.jsonl"
	investmentMacroPath    = "macro_release/releases.jsonl"
	investmentNotesPath    = "analyst_note/notes.jsonl"
	investmentEntitiesPath = "analyst_note/real-entities.yaml"

	// Carried on every note, so the artifact says what it is wherever it is read
	// (srd018 R4.3).
	investmentNoteNotice = "Invented for a demonstration. The issuers, deals, people, and views in this note are fictional, and it makes no claim about any real company."
)

func init() {
	corpusCaseDefinitions[investmentCaseName] = corpusCaseDefinition{
		Label:      "the investment research case",
		ValuesFile: investmentValuesFile,
		SourceTypes: []string{
			sourceTypeFilingFacts, sourceTypeEnergyAnalysis, sourceTypeMacroRelease, sourceTypeAnalystNote,
		},
		Slice:     investmentSliceParameters,
		Selection: investmentEmbedSelection,
		Load:      loadInvestmentCase,
	}
}

func investmentSliceParameters() corpusSliceParameters {
	return corpusSliceParameters{WindowStart: investmentWindowStart, WindowEnd: investmentWindowEnd}
}

// investmentEmbedSelection embeds the retrieval profile. Notes, facts, and releases
// fit their allowances whole. The analysis tier is capped, because it holds most of the
// corpus's text and an uncapped tier crowds the others out of composition (the defense
// corpus measured this in GH-286). The ceiling is checked before the first request
// (srd018 R1.4).
func investmentEmbedSelection() corpusEmbedSelection {
	return corpusEmbedSelection{
		WindowStart: investmentWindowStart,
		WindowEnd:   investmentWindowEnd,
		Profiles:    []string{chunkProfileMedium},
		PerTier: map[string]int{
			sourceTypeFilingFacts:    60,
			sourceTypeEnergyAnalysis: 260,
			sourceTypeMacroRelease:   90,
			sourceTypeAnalystNote:    90,
		},
		Ceiling: 500,
	}
}

// investmentFact is one reported value from an issuer's XBRL facts. It holds the
// number and what identifies it, and nothing the filer wrote in prose (srd018 R2.4).
type investmentFact struct {
	Issuer    string `json:"issuer"`
	Ticker    string `json:"ticker"`
	CIK       string `json:"cik"`
	Concept   string `json:"concept"`
	Value     int64  `json:"value"`
	Unit      string `json:"unit"`
	Start     string `json:"start,omitempty"`
	End       string `json:"end"`
	Form      string `json:"form"`
	FiscalPer string `json:"fiscal_period"`
	FiscalYr  int    `json:"fiscal_year"`
	Accession string `json:"accession"`
	Filed     string `json:"filed"`
}

// investmentConceptLabels names each concept this case reads, in the order a
// document renders them. The labels are this build's own words, because the
// taxonomy's labels and descriptions are not ours to copy.
var investmentConceptLabels = []struct{ Concept, Label string }{
	{"Revenues", "revenue"},
	{"NetIncomeLoss", "net income"},
	{"NetCashProvidedByUsedInOperatingActivities", "operating cash flow"},
	{"PaymentsToAcquirePropertyPlantAndEquipment", "capital expenditure"},
	{"PaymentsToAcquireProductiveAssets", "capital expenditure"},
	{"Assets", "total assets"},
	{"CashAndCashEquivalentsAtCarryingValue", "cash and cash equivalents"},
}

// investmentArticle is one EIA Today in Energy article: EIA's own text, with images
// and charts left behind because EIA says some may be licensed from others.
type investmentArticle struct {
	ID         string   `json:"id"`
	Date       string   `json:"date"`
	Title      string   `json:"title"`
	URL        string   `json:"url"`
	DataSource string   `json:"data_source,omitempty"`
	Paragraphs []string `json:"paragraphs"`
}

// investmentRelease is one government macro release.
type investmentRelease struct {
	ID         string   `json:"id"`
	Publisher  string   `json:"publisher"`
	Date       string   `json:"date"`
	Title      string   `json:"title"`
	URL        string   `json:"url"`
	Paragraphs []string `json:"paragraphs"`
}

func loadInvestmentCase(
	dataRoot string, params corpusSliceParameters, manifest corpusManifest,
) ([]corpusEnvelope, error) {
	var envelopes []corpusEnvelope
	facts, err := loadInvestmentFacts(dataRoot, params, manifest)
	if err != nil {
		return nil, err
	}
	envelopes = append(envelopes, facts...)
	analysis, err := loadInvestmentAnalysis(dataRoot, params, manifest)
	if err != nil {
		return nil, err
	}
	envelopes = append(envelopes, analysis...)
	macro, err := loadInvestmentMacro(dataRoot, params, manifest)
	if err != nil {
		return nil, err
	}
	envelopes = append(envelopes, macro...)
	notes, err := loadInvestmentNotes(dataRoot, params, manifest)
	if err != nil {
		return nil, err
	}
	return append(envelopes, notes...), nil
}

// readInvestmentJSONL decodes one record per line, failing on the first bad one.
func readInvestmentJSONL[T any](dataRoot, relative string) ([]T, error) {
	handle, err := os.Open(filepath.Join(dataRoot, filepath.FromSlash(relative)))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", relative, err)
	}
	defer func() { _ = handle.Close() }()
	var records []T
	scanner := bufio.NewScanner(handle)
	scanner.Buffer(make([]byte, 1<<20), 1<<26)
	for line := 1; scanner.Scan(); line++ {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var record T
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", relative, line, err)
		}
		records = append(records, record)
	}
	return records, scanner.Err()
}

// loadInvestmentFacts groups facts by filing into one document each, so a citation
// names one filing rather than one number.
func loadInvestmentFacts(
	dataRoot string, params corpusSliceParameters, manifest corpusManifest,
) ([]corpusEnvelope, error) {
	facts, err := readInvestmentJSONL[investmentFact](dataRoot, investmentFactsPath)
	if err != nil {
		return nil, err
	}
	byFiling := map[string][]investmentFact{}
	for _, fact := range facts {
		if params.inWindow(fact.Filed) {
			byFiling[fact.Accession] = append(byFiling[fact.Accession], fact)
		}
	}
	accessions := make([]string, 0, len(byFiling))
	for accession := range byFiling {
		accessions = append(accessions, accession)
	}
	sort.Strings(accessions)
	envelopes := make([]corpusEnvelope, 0, len(accessions))
	for _, accession := range accessions {
		envelope, err := adaptInvestmentFiling(byFiling[accession], manifest)
		if err != nil {
			return nil, err
		}
		envelopes = append(envelopes, envelope)
	}
	return envelopes, nil
}

// adaptInvestmentFiling renders one filing's reported values as text. The text is
// this build's, the numbers are the filer's, and no sentence comes from the filing.
func adaptInvestmentFiling(facts []investmentFact, manifest corpusManifest) (corpusEnvelope, error) {
	if len(facts) == 0 {
		return corpusEnvelope{}, fmt.Errorf("a filing document needs at least one fact")
	}
	first := facts[0]
	issued, err := normalizedIssuedDate(first.Filed)
	if err != nil {
		return corpusEnvelope{}, fmt.Errorf("filing %s: %w", first.Accession, err)
	}
	period := fmt.Sprintf("fiscal %d %s", first.FiscalYr, first.FiscalPer)
	lines := []string{fmt.Sprintf("%s (%s) reported the following values in its Form %s filed %s for %s.",
		first.Issuer, first.Ticker, first.Form, issued, period)}
	seen := map[string]bool{}
	for _, entry := range investmentConceptLabels {
		fact, ok := investmentCurrentFact(facts, entry.Concept)
		if !ok || seen[entry.Label] {
			continue
		}
		seen[entry.Label] = true
		lines = append(lines, investmentFactSentence(entry.Label, fact))
	}
	if len(lines) == 1 {
		return corpusEnvelope{}, fmt.Errorf("filing %s carries none of the concepts this case reads", first.Accession)
	}
	envelope := corpusEnvelope{
		DocumentID:  "facts-" + strings.ToLower(first.Ticker) + "-" + first.Accession,
		SourceType:  sourceTypeFilingFacts,
		Clearance:   tierClearance(sourceTypeFilingFacts, manifest),
		Issued:      issued,
		Origin:      "U.S. Securities and Exchange Commission, EDGAR",
		Reliability: "reported_filing_values",
		Provenance:  corpusProvenance{Collector: "U.S. Securities and Exchange Commission, EDGAR"},
		Title:       fmt.Sprintf("%s reported values, Form %s, %s", first.Issuer, first.Form, period),
		RawRef:      corpusRawRef{Store: "edgar-xbrl", ID: first.Accession, Path: investmentFactsPath},
		Segments: []corpusSegment{
			{Role: segmentRoleAuthority, Text: "Source: U.S. Securities and Exchange Commission, EDGAR XBRL company facts, accession " + first.Accession + "."},
			{Role: segmentRoleBody, Searchable: true, Text: strings.Join(lines, "\n\n")},
		},
	}
	return envelope, envelope.validate()
}

// investmentCurrentFact picks a concept's value for the filing's own period: the
// latest end date, and the shortest span at that date, because a filing also
// repeats prior periods and year-to-date figures for comparison.
func investmentCurrentFact(facts []investmentFact, concept string) (investmentFact, bool) {
	var best investmentFact
	found := false
	for _, fact := range facts {
		if fact.Concept != concept {
			continue
		}
		if !found || fact.End > best.End || (fact.End == best.End && fact.Start > best.Start) {
			best, found = fact, true
		}
	}
	return best, found
}

func investmentFactSentence(label string, fact investmentFact) string {
	amount := investmentMoney(fact.Value)
	if fact.Start != "" {
		return fmt.Sprintf("Reported %s was %s for the period %s to %s.", label, amount, fact.Start, fact.End)
	}
	return fmt.Sprintf("Reported %s was %s as of %s.", label, amount, fact.End)
}

// investmentMoney renders a dollar amount the way an analyst would read it.
func investmentMoney(value int64) string {
	negative := value < 0
	if negative {
		value = -value
	}
	var rendered string
	switch {
	case value >= 1_000_000_000:
		rendered = "$" + strconv.FormatFloat(float64(value)/1e9, 'f', 2, 64) + " billion"
	case value >= 1_000_000:
		rendered = "$" + strconv.FormatFloat(float64(value)/1e6, 'f', 1, 64) + " million"
	default:
		rendered = "$" + strconv.FormatInt(value, 10)
	}
	if negative {
		return "negative " + rendered
	}
	return rendered
}

func loadInvestmentAnalysis(
	dataRoot string, params corpusSliceParameters, manifest corpusManifest,
) ([]corpusEnvelope, error) {
	articles, err := readInvestmentJSONL[investmentArticle](dataRoot, investmentAnalysisPath)
	if err != nil {
		return nil, err
	}
	envelopes := make([]corpusEnvelope, 0, len(articles))
	for _, article := range articles {
		if !params.inWindow(article.Date) {
			continue
		}
		envelope, err := adaptInvestmentArticle(article, manifest)
		if err != nil {
			return nil, err
		}
		envelopes = append(envelopes, envelope)
	}
	return envelopes, nil
}

// adaptInvestmentArticle carries the acknowledgment EIA's reuse terms ask for,
// publication date included, on every document (srd018 R2.7).
func adaptInvestmentArticle(article investmentArticle, manifest corpusManifest) (corpusEnvelope, error) {
	issued, err := normalizedIssuedDate(article.Date)
	if err != nil {
		return corpusEnvelope{}, fmt.Errorf("article %s: %w", article.ID, err)
	}
	const publisher = "U.S. Energy Information Administration"
	authority := "Source: " + publisher + " (" + investmentMonthYear(issued) + "), " + article.URL + "."
	if article.DataSource != "" {
		authority += " Data source as EIA states it: " + article.DataSource + "."
	}
	envelope := corpusEnvelope{
		DocumentID:  "eia-" + article.ID,
		SourceType:  sourceTypeEnergyAnalysis,
		Clearance:   tierClearance(sourceTypeEnergyAnalysis, manifest),
		Issued:      issued,
		Origin:      publisher,
		Reliability: "published_analysis",
		Provenance:  corpusProvenance{Collector: publisher},
		Title:       article.Title,
		RawRef:      corpusRawRef{Store: "eia-today-in-energy", ID: article.URL, Path: investmentAnalysisPath},
		Segments: []corpusSegment{
			{Role: segmentRoleAuthority, Text: authority},
			{Role: segmentRoleBody, Searchable: true, Text: strings.Join(article.Paragraphs, "\n\n")},
		},
	}
	return envelope, envelope.validate()
}

func investmentMonthYear(issued string) string {
	parsed, err := time.Parse(corpusEnvelopeDateLayout, issued)
	if err != nil {
		return issued
	}
	return parsed.Format("Jan 2006")
}

func loadInvestmentMacro(
	dataRoot string, params corpusSliceParameters, manifest corpusManifest,
) ([]corpusEnvelope, error) {
	releases, err := readInvestmentJSONL[investmentRelease](dataRoot, investmentMacroPath)
	if err != nil {
		return nil, err
	}
	envelopes := make([]corpusEnvelope, 0, len(releases))
	for _, release := range releases {
		if !params.inWindow(release.Date) {
			continue
		}
		issued, err := normalizedIssuedDate(release.Date)
		if err != nil {
			return nil, fmt.Errorf("release %s: %w", release.ID, err)
		}
		envelope := corpusEnvelope{
			DocumentID:  "macro-" + release.ID,
			SourceType:  sourceTypeMacroRelease,
			Clearance:   tierClearance(sourceTypeMacroRelease, manifest),
			Issued:      issued,
			Origin:      release.Publisher,
			Reliability: "official_release",
			Provenance:  corpusProvenance{Collector: release.Publisher},
			Title:       release.Title,
			RawRef:      corpusRawRef{Store: "government-release", ID: release.URL, Path: investmentMacroPath},
			Segments: []corpusSegment{
				{Role: segmentRoleAuthority, Text: "Source: " + release.Publisher + ", " + release.URL + "."},
				{Role: segmentRoleBody, Searchable: true, Text: strings.Join(release.Paragraphs, "\n\n")},
			},
		}
		if err := envelope.validate(); err != nil {
			return nil, err
		}
		envelopes = append(envelopes, envelope)
	}
	return envelopes, nil
}

// investmentNote is one invented analyst note. Label is the barrier it sits behind.
type investmentNote struct {
	ID         string   `json:"id"`
	Date       string   `json:"date"`
	Label      string   `json:"label"`
	Rating     string   `json:"rating,omitempty"`
	Issuers    []string `json:"issuers"`
	Title      string   `json:"title"`
	Paragraphs []string `json:"paragraphs"`
}

func loadInvestmentNotes(
	dataRoot string, params corpusSliceParameters, manifest corpusManifest,
) ([]corpusEnvelope, error) {
	notes, err := readInvestmentJSONL[investmentNote](dataRoot, investmentNotesPath)
	if err != nil {
		return nil, err
	}
	banned, err := investmentBannedEntities(dataRoot)
	if err != nil {
		return nil, err
	}
	if err := checkInvestmentNotes(notes, banned); err != nil {
		return nil, err
	}
	mapping := clearanceMapping{Markings: manifest.ClearanceMarkings, Caveats: manifest.ClearanceCaveats}
	envelopes := make([]corpusEnvelope, 0, len(notes))
	for _, note := range notes {
		if !params.inWindow(note.Date) {
			continue
		}
		envelope, err := adaptInvestmentNote(note, mapping, manifest)
		if err != nil {
			return nil, err
		}
		envelopes = append(envelopes, envelope)
	}
	return envelopes, nil
}

// adaptInvestmentNote sets the level from the note's declared label, so an unmapped
// label fails closed at the highest level (srd018 R3.4), and takes the tier's
// cannot-be-judged rating when the note declares none (R5.2).
func adaptInvestmentNote(
	note investmentNote, mapping clearanceMapping, manifest corpusManifest,
) (corpusEnvelope, error) {
	issued, err := normalizedIssuedDate(note.Date)
	if err != nil {
		return corpusEnvelope{}, fmt.Errorf("note %s: %w", note.ID, err)
	}
	clearance, caveats := clearanceFromMarking(note.Label, mapping, manifest)
	rating := strings.TrimSpace(note.Rating)
	if rating == "" {
		rating = manifest.TierAdmiralty[sourceTypeAnalystNote]
	}
	const origin = "Invented research desk"
	envelope := corpusEnvelope{
		DocumentID:  note.ID,
		SourceType:  sourceTypeAnalystNote,
		Clearance:   clearance,
		Caveats:     caveats,
		Issued:      issued,
		Origin:      origin,
		Reliability: rating,
		Provenance:  corpusProvenance{Collector: origin},
		Title:       note.Title,
		Synthetic:   true,
		RawRef:      corpusRawRef{Store: "invented-notes", ID: note.ID, Path: investmentNotesPath},
		Segments: []corpusSegment{
			{Role: segmentRoleAuthority, Text: investmentNoteNotice},
			{Role: segmentRoleBody, Searchable: true, Text: strings.Join(note.Paragraphs, "\n\n")},
		},
	}
	return envelope, envelope.validate()
}

// investmentBannedEntities is every real name a note may not carry: the issuers
// filing_facts reads, their tickers, and the declared list of other real companies.
func investmentBannedEntities(dataRoot string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dataRoot, filepath.FromSlash(investmentEntitiesPath)))
	if err != nil {
		return nil, fmt.Errorf("read the declared real entities: %w", err)
	}
	var declared struct {
		Entities []string `yaml:"entities"`
	}
	if err := yaml.Unmarshal(data, &declared); err != nil {
		return nil, fmt.Errorf("parse %s: %w", investmentEntitiesPath, err)
	}
	if len(declared.Entities) == 0 {
		return nil, fmt.Errorf("%s declares no entities, which would leave the guard with"+
			" only the filing_facts issuers", investmentEntitiesPath)
	}
	banned := append([]string(nil), declared.Entities...)
	for _, issuer := range investmentIssuers {
		banned = append(banned, issuer.Name, issuer.Ticker, investmentShortName(issuer.Name))
	}
	return banned, nil
}

// investmentShortName is the name people actually write: Exxon Mobil Corporation
// without its corporate suffix.
func investmentShortName(name string) string {
	for _, suffix := range []string{" Corporation", ", Inc.", " Inc."} {
		name = strings.TrimSuffix(name, suffix)
	}
	return name
}

// investmentInadmissiblePhrases keep a note from reading as advice (srd018 R8.4).
var investmentInadmissiblePhrases = []string{
	"price target", "buy rating", "sell rating", "overweight", "underweight", "we recommend",
}

// checkInvestmentNotes fails the build on a note that names a real entity or reads as
// advice, naming the note and the match (srd018 R4.2). Names match whole words without
// regard to case; tickers are upper-case acronyms that also spell ordinary words, so
// they match as written.
func checkInvestmentNotes(notes []investmentNote, banned []string) error {
	for _, note := range notes {
		if len(note.Issuers) == 0 {
			return fmt.Errorf("note %s names no issuer; a note about no invented issuer cannot"+
				" be checked against the guard's purpose", note.ID)
		}
		text := note.Title + "\n" + strings.Join(note.Issuers, "\n") + "\n" + strings.Join(note.Paragraphs, "\n")
		for _, entity := range banned {
			pattern := `\b` + regexp.QuoteMeta(entity) + `\b`
			if strings.ToUpper(entity) != entity {
				pattern = `(?i)` + pattern
			}
			if regexp.MustCompile(pattern).MatchString(text) {
				return fmt.Errorf("note %s names %q, a real entity; analyst notes name invented"+
					" issuers only (srd018 R4.1)", note.ID, entity)
			}
		}
		lowered := strings.ToLower(text)
		for _, phrase := range investmentInadmissiblePhrases {
			if strings.Contains(lowered, phrase) {
				return fmt.Errorf("note %s says %q; this case makes no recommendation (srd018 R8.4)",
					note.ID, phrase)
			}
		}
	}
	return nil
}
