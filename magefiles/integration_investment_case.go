// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The rel15.0 tracer (srd018 R8.1). It drives the mesh deployed with the case's values
// file and seeded from the case's store, and it asserts on which documents reached the
// memo rather than on how the memo reads. A deployed turn needs organization access,
// because the runtime modules and image are private.

const (
	investmentLowestLevel  = "unclassified"
	investmentHighestLevel = "secret"
	investmentTemplateName = "investment-committee-memo-markdown@1"
	// Paced apart to stay under provider rate limits, as the conformance tracer is.
	investmentLegPace          = 45 * time.Second
	investmentBroadAttempts    = 4
	investmentProviderAttempts = 4

	investmentBroadTasking = "Assess capital discipline among US oil and gas producers in 2024" +
		" against natural gas prices, Permian production growth, and the rate backdrop."
	investmentGatedTasking = "What is the status of Velmora Energy's approach for Sablecrest" +
		" Midstream's Delaware gathering assets under Project Larkspur?"
)

// investmentTurnFacts is what a turn's assertions read, separated from the transport so
// a fixture can exercise them without a mesh.
type investmentTurnFacts struct {
	Level      string
	AnsweredBy string
	Answer     string
	Composed   []string
	Citations  []string
	Verdict    *conformanceVerdict
}

// investmentMemoChecks are the memo's required-section patterns, built from the schema
// so the tracer re-evaluates what the schema declares.
type investmentMemoChecks struct {
	sections []struct {
		label   string
		pattern *regexp.Regexp
	}
}

func loadInvestmentMemoChecks(caseRoot string) (investmentMemoChecks, error) {
	var checks investmentMemoChecks
	data, err := os.ReadFile(filepath.Join(caseRoot, "memo-schema.yaml"))
	if err != nil {
		return checks, err
	}
	var schema struct {
		Sections []struct {
			Label    string   `yaml:"label"`
			Required bool     `yaml:"required"`
			Headings []string `yaml:"headings"`
		} `yaml:"sections"`
	}
	if err := yaml.Unmarshal(data, &schema); err != nil {
		return checks, err
	}
	for _, section := range schema.Sections {
		if !section.Required {
			continue
		}
		pattern, err := regexp.Compile(`(?s)^(?:|.*\n)#{1,6}[ \t]+(?:` + strings.Join(section.Headings, "|") + `)[ \t]*\n+[ \t]*[^\s#].*$`)
		if err != nil {
			return checks, err
		}
		checks.sections = append(checks.sections, struct {
			label   string
			pattern *regexp.Regexp
		}{section.Label, pattern})
	}
	return checks, nil
}

// missingSections names the required sections a memo lacks.
func (c investmentMemoChecks) missingSections(answer string) []string {
	var missing []string
	for _, section := range c.sections {
		if !section.pattern.MatchString(answer) {
			missing = append(missing, section.label)
		}
	}
	return missing
}

// investmentChunkSources maps every chunk the store holds to its document.
func investmentChunkSources(documents map[string]corpusStoredDocument) map[string]corpusStoredDocument {
	sources := make(map[string]corpusStoredDocument)
	for _, document := range documents {
		for _, chunk := range document.Chunks {
			sources[chunk.ChunkID] = document
		}
	}
	return sources
}

// assertInvestmentTurn holds a turn to what every leg must show: Command wrote it, it
// composed only from this case's store, the verdict names the memo schema and tells the
// truth, no document sits above the requester's level, and it carries the qualification
// and no recommendation.
func assertInvestmentTurn(
	leg string, turn investmentTurnFacts, sources map[string]corpusStoredDocument,
	checks investmentMemoChecks, levels []string,
) error {
	if !strings.Contains(strings.ToLower(turn.AnsweredBy), "cohere") && !strings.Contains(strings.ToLower(turn.AnsweredBy), "command") {
		return fmt.Errorf("%s: answered by %q; a degraded tier says nothing about the case", leg, turn.AnsweredBy)
	}
	if len(turn.Composed) == 0 {
		return fmt.Errorf("%s: the turn composed from nothing", leg)
	}
	rank := func(level string) int {
		for index, declared := range levels {
			if declared == level {
				return index
			}
		}
		return len(levels)
	}
	for _, id := range turn.Composed {
		document, held := sources[id]
		if !held {
			return fmt.Errorf("%s: composed %s, which this case's store does not hold;"+
				" the mesh is not serving this case", leg, id)
		}
		if rank(document.Envelope.Clearance) > rank(turn.Level) {
			return fmt.Errorf("%s: composed %s at level %q above the requester's %q",
				leg, id, document.Envelope.Clearance, turn.Level)
		}
	}
	if turn.Verdict == nil {
		return fmt.Errorf("%s: the turn carries no conformance verdict", leg)
	}
	if turn.Verdict.TemplateVersion != investmentTemplateName {
		return fmt.Errorf("%s: the verdict names %q, want %q", leg, turn.Verdict.TemplateVersion, investmentTemplateName)
	}
	missing := checks.missingSections(turn.Answer)
	if conformant := turn.Verdict.Verdict == "conformant"; conformant != (len(missing) == 0) {
		return fmt.Errorf("%s: the verdict says %q while the schema finds missing sections %v",
			leg, turn.Verdict.Verdict, missing)
	}
	lowered := strings.ToLower(turn.Answer)
	for _, phrase := range investmentInadmissiblePhrases {
		if strings.Contains(lowered, phrase) {
			return fmt.Errorf("%s: the memo says %q; this case makes no recommendation", leg, phrase)
		}
	}
	for _, id := range turn.Citations {
		document, held := sources[id]
		if !held {
			return fmt.Errorf("%s: cited %s, which this case's store does not hold", leg, id)
		}
		if rank(document.Envelope.Clearance) > rank(turn.Level) {
			return fmt.Errorf("%s: cited %s above the requester's level", leg, id)
		}
	}
	// A turn with nothing to say is a correct refusal rather than a memo, so only a
	// memo owes citations and the qualification.
	if turn.Verdict.Verdict != "conformant" {
		return nil
	}
	if len(turn.Citations) == 0 {
		return fmt.Errorf("%s: a conformant memo cited nothing (srd018 R8.1)", leg)
	}
	if !strings.Contains(strings.Join(strings.Fields(lowered), " "), "not investment advice") {
		return fmt.Errorf("%s: the memo does not carry the qualification (srd018 R8.2, R8.4)", leg)
	}
	return nil
}

// investmentComposedNotes names the analyst notes a turn composed from.
func investmentComposedNotes(composed []string, sources map[string]corpusStoredDocument) []string {
	var notes []string
	for _, id := range composed {
		if document, held := sources[id]; held && document.Envelope.SourceType == sourceTypeAnalystNote {
			notes = append(notes, document.DocumentID)
		}
	}
	return notes
}

// investmentTurn drives one turn, retrying only a provider timeout. Long memos with many
// citations can outrun the chatbot's request budget, which belongs to every profile.
// Retries are counted and printed so the rate stays visible.
func investmentTurn(base, level, tasking string) (investmentTurnFacts, error) {
	var lastErr error
	for attempt := 1; attempt <= investmentProviderAttempts; attempt++ {
		turn, err := investmentSingleTurn(base, level, tasking)
		if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
			return turn, err
		}
		lastErr = err
		fmt.Printf("integration:investmentCase: provider timeout on attempt %d at %s\n", attempt, level)
		if attempt < investmentProviderAttempts {
			time.Sleep(investmentLegPace)
		}
	}
	return investmentTurnFacts{}, lastErr
}

func investmentSingleTurn(base, level, tasking string) (investmentTurnFacts, error) {
	body, err := json.Marshal(map[string]interface{}{"message": tasking, "history": []interface{}{}})
	if err != nil {
		return investmentTurnFacts{}, err
	}
	request, err := http.NewRequest(http.MethodPost, base+conformanceChatEndpoint, bytes.NewReader(body))
	if err != nil {
		return investmentTurnFacts{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Analyst-Clearance", level)
	client := &http.Client{Timeout: conformanceTurnTimeout}
	response, err := client.Do(request)
	if err != nil {
		return investmentTurnFacts{}, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return investmentTurnFacts{}, err
	}
	if response.StatusCode != http.StatusOK {
		return investmentTurnFacts{}, fmt.Errorf("chat returned status %d: %s", response.StatusCode, excerpt(raw))
	}
	var answer chatResponse
	if err := json.Unmarshal(raw, &answer); err != nil {
		return investmentTurnFacts{}, fmt.Errorf("decode the turn: %w", err)
	}
	var decoded conformanceTurn
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return investmentTurnFacts{}, fmt.Errorf("decode the verdict: %w", err)
	}
	citations := make([]string, 0, len(answer.Metadata.Citations))
	for _, citation := range answer.Metadata.Citations {
		citations = append(citations, citation.RecordID)
	}
	return investmentTurnFacts{
		Level: level, AnsweredBy: answer.Metadata.AnsweredBy, Answer: answer.Answer,
		Composed: composedRecordIDs(answer), Citations: citations, Verdict: decoded.Metadata.Conformance,
	}, nil
}

// InvestmentCase proves a cited, memo-conformant, access-gated turn over the case
// (srd018 R8.1). Run it after selecting the case for app:up and app:seed.
func (Integration) InvestmentCase() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	base := envOrDefault("CHATBOT_UI_BASE", chatbotUIDefaultBase)
	if err := chatbotUIReachable(base); err != nil {
		fmt.Printf("SKIP integration:investmentCase: %s does not serve the mesh (%v); run"+
			" CORPUS_CASE=%s mage app:up and app:seed first\n", base, err, investmentCaseName)
		return nil
	}
	caseRoot := filepath.Join(root, corpusCasesDir, investmentCaseName)
	manifest, err := readCorpusManifest(caseRoot)
	if err != nil {
		return err
	}
	documents, err := loadCorpusDocumentStore(caseRoot)
	if err != nil {
		return err
	}
	sources := investmentChunkSources(documents)
	checks, err := loadInvestmentMemoChecks(caseRoot)
	if err != nil {
		return err
	}

	// A live provider decides its own structure, so the broad leg retries, paced, until
	// a memo conforms. Every attempt is still held to the truth of its verdict.
	var broad investmentTurnFacts
	for attempt := 1; attempt <= investmentBroadAttempts; attempt++ {
		broad, err = investmentTurn(base, investmentLowestLevel, investmentBroadTasking)
		if err != nil {
			return fmt.Errorf("broad leg: %w", err)
		}
		if err := assertInvestmentTurn("broad leg", broad, sources, checks, manifest.ClearanceLevels); err != nil {
			return err
		}
		if notes := investmentComposedNotes(broad.Composed, sources); len(notes) != 0 {
			return fmt.Errorf("broad leg: public requester composed gated notes %v", notes)
		}
		fmt.Printf("integration:investmentCase: broad leg attempt %d composed %d chunks and cited %d at %s,"+
			" verdict %q under %s\n", attempt, len(broad.Composed), len(broad.Citations),
			investmentLowestLevel, broad.Verdict.Verdict, broad.Verdict.TemplateVersion)
		if broad.Verdict.Verdict == "conformant" {
			break
		}
		if attempt == investmentBroadAttempts {
			return fmt.Errorf("broad leg: no conformant memo in %d attempts; last defects %v",
				attempt, broad.Verdict.Defects)
		}
		time.Sleep(investmentLegPace)
	}
	fmt.Println(broad.Answer)

	time.Sleep(investmentLegPace)
	cleared, err := investmentTurn(base, investmentHighestLevel, investmentGatedTasking)
	if err != nil {
		return fmt.Errorf("cleared leg: %w", err)
	}
	if err := assertInvestmentTurn("cleared leg", cleared, sources, checks, manifest.ClearanceLevels); err != nil {
		return err
	}
	clearedNotes := investmentComposedNotes(cleared.Composed, sources)
	if len(clearedNotes) == 0 {
		return fmt.Errorf("cleared leg: composed %v and no gated note, so the pair cannot show a"+
			" withheld document", cleared.Composed)
	}
	fmt.Printf("integration:investmentCase: cleared leg composed gated notes %v\n", clearedNotes)

	time.Sleep(investmentLegPace)
	uncleared, err := investmentTurn(base, investmentLowestLevel, investmentGatedTasking)
	if err != nil {
		return fmt.Errorf("uncleared leg: %w", err)
	}
	if err := assertInvestmentTurn("uncleared leg", uncleared, sources, checks, manifest.ClearanceLevels); err != nil {
		return err
	}
	if notes := investmentComposedNotes(uncleared.Composed, sources); len(notes) != 0 {
		return fmt.Errorf("uncleared leg: the same question at the lowest level composed gated notes %v", notes)
	}
	if notes := investmentComposedNotes(uncleared.Citations, sources); len(notes) != 0 {
		return fmt.Errorf("uncleared leg: cited gated notes %v", notes)
	}
	fmt.Println("integration:investmentCase: uncleared leg composed and cited no gated note; the same" +
		" question at the highest level composed some")
	return nil
}
