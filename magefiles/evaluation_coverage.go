// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Citation coverage (srd014 R3): the fraction of a brief's assertions
// carrying a citation that resolves to a document in the composition set,
// computed from the structured citation metadata alone (R3.4), under the
// segmentation rule declared in evaluation/README.md (R3.2). A citation that
// does not resolve covers nothing (R3.3), the threshold is complete
// coverage, and any brief falling short is itemized rather than absorbed
// into an average (R3.5).

// evaluationAssertion is one segmented assertion with its rune range in the
// answer, the range citation spans are matched against.
type evaluationAssertion struct {
	Text  string
	Start int
	End   int
}

// segmentAssertions applies the declared rule: split on newlines, drop blank
// and heading lines, strip one leading list marker. Offsets are rune
// offsets, the unit the citation spans use.
func segmentAssertions(answer string) []evaluationAssertion {
	runes := []rune(answer)
	var assertions []evaluationAssertion
	lineStart := 0
	for index := 0; index <= len(runes); index++ {
		if index < len(runes) && runes[index] != '\n' {
			continue
		}
		line := string(runes[lineStart:index])
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			text := trimmed
			for _, marker := range []string{"- ", "* "} {
				if strings.HasPrefix(text, marker) {
					text = strings.TrimPrefix(text, marker)
					break
				}
			}
			assertions = append(assertions, evaluationAssertion{
				Text: text, Start: lineStart, End: index,
			})
		}
		lineStart = index + 1
	}
	return assertions
}

type evaluationCitationSpan struct {
	Start    int    `json:"start"`
	End      int    `json:"end"`
	RecordID string `json:"record_id"`
}

// decodeCitationSpans reads the raw citations field leniently: the mesh
// renders an empty string on the uncited paths, and only an actual array
// carries spans.
func decodeCitationSpans(raw json.RawMessage) []evaluationCitationSpan {
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, "[") {
		return nil
	}
	var spans []evaluationCitationSpan
	if err := json.Unmarshal(raw, &spans); err != nil {
		return nil
	}
	return spans
}

// evaluationCoverageResult is one brief's coverage, itemized down to the
// uncovered assertions (R3.5) and inspectable per tasking (R5.3).
type evaluationCoverageResult struct {
	Tasking    string   `json:"tasking"`
	AnsweredBy string   `json:"answered_by"`
	Assertions int      `json:"assertions"`
	Covered    int      `json:"covered"`
	Coverage   float64  `json:"coverage"`
	Unresolved int      `json:"unresolved_citations"`
	Uncovered  []string `json:"uncovered,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// coverageForTurn applies the declared rule to one brief. An assertion is
// covered when a citation span overlaps its rune range and the citation's
// record id is in the composed set; a span naming a document outside the
// composed set is counted as unresolved and covers nothing (R3.3).
func coverageForTurn(tasking string, turn evaluationTurn) evaluationCoverageResult {
	result := evaluationCoverageResult{Tasking: tasking, AnsweredBy: turn.Metadata.AnsweredBy}
	assertions := segmentAssertions(turn.Answer)
	result.Assertions = len(assertions)
	composed := map[string]bool{}
	for _, id := range turn.Metadata.Composition.Documents {
		composed[id] = true
	}
	var resolved []evaluationCitationSpan
	for _, span := range decodeCitationSpans(turn.Metadata.Citations) {
		if composed[span.RecordID] {
			resolved = append(resolved, span)
		} else {
			result.Unresolved++
		}
	}
	for _, assertion := range assertions {
		var covered bool
		for _, span := range resolved {
			if span.Start < assertion.End && span.End > assertion.Start {
				covered = true
				break
			}
		}
		if covered {
			result.Covered++
		} else {
			sample := assertion.Text
			if len(sample) > 80 {
				sample = sample[:80]
			}
			result.Uncovered = append(result.Uncovered, sample)
		}
	}
	if result.Assertions > 0 {
		result.Coverage = float64(result.Covered) / float64(result.Assertions)
	}
	return result
}

// Coverage measures srd014 R3 against the deployed mesh in its default
// shape: one brief per corpus tasking, segmented under the declared rule,
// coverage from the structured citation metadata against the composed set.
// Skips with the cause named when the mesh is absent.
func (Evaluation) Coverage() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	base := envOrDefault("CHATBOT_UI_BASE", chatbotUIDefaultBase)
	if err := chatbotUIReachable(base); err != nil {
		fmt.Printf("SKIP evaluation:coverage: %s does not serve the mesh (%v);"+
			" run mage app:up and mage app:seed first\n", base, err)
		return nil
	}
	corpus, err := loadEvaluationCorpus(root)
	if err != nil {
		return err
	}
	results, short := runEvaluationCoverage(base, corpus)
	var sum float64
	var counted, failed int
	for _, result := range results {
		if result.Error != "" {
			failed++
			continue
		}
		sum += result.Coverage
		counted++
	}
	average := 0.0
	if counted > 0 {
		average = sum / float64(counted)
	}
	verdict := "FAIL"
	if len(short) == 0 && failed == 0 && counted > 0 {
		verdict = "PASS"
	}
	fmt.Printf("evaluation:coverage %s - average %.3f over %d briefs (threshold: complete coverage);"+
		" %d briefs short, %d failed; measured over the mixed real-and-synthetic %s v%s\n",
		verdict, average, counted, len(short), failed, corpus.ID, corpus.Version)
	for _, name := range short {
		fmt.Printf("evaluation:coverage short brief: %s\n", name)
	}
	if verdict == "FAIL" {
		return fmt.Errorf("citation coverage is not complete over the corpus (srd014 R3.5)")
	}
	return nil
}

// runEvaluationCoverage drives one brief per tasking and folds per-brief
// results; the itemized short list carries every brief under complete
// coverage (R3.5).
func runEvaluationCoverage(base string, corpus evaluationCorpus) ([]evaluationCoverageResult, []string) {
	var results []evaluationCoverageResult
	var short []string
	for index, tasking := range corpus.Taskings {
		if index > 0 {
			time.Sleep(evaluationPace)
		}
		turn, _, err := runEvaluationTurn(base, tasking.Text, tasking.Clearance)
		if err != nil {
			results = append(results, evaluationCoverageResult{Tasking: tasking.ID, Error: err.Error()})
			fmt.Printf("evaluation: %s coverage FAILED: %v\n", tasking.ID, err)
			continue
		}
		result := coverageForTurn(tasking.ID, turn)
		results = append(results, result)
		if result.Coverage < 1.0 {
			short = append(short, fmt.Sprintf("%s (%.2f, %d/%d, answered_by=%s, %d unresolved)",
				tasking.ID, result.Coverage, result.Covered, result.Assertions,
				result.AnsweredBy, result.Unresolved))
		}
		fmt.Printf("evaluation: %s coverage %.2f (%d/%d assertions, answered_by=%s)\n",
			tasking.ID, result.Coverage, result.Covered, result.Assertions, result.AnsweredBy)
	}
	return results, short
}
