// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Citation coverage (srd014 R3): the declared segmentation rule as behavior,
// and the coverage arithmetic including the R3.3 rule that an unresolvable
// citation covers nothing.

// TestSegmentAssertionsAppliesTheDeclaredRule pins the four steps of the
// rule declared in evaluation/README.md: blank lines and headings drop, one
// list marker strips, and offsets are rune offsets so a span computed by
// rune agrees past a non-ASCII character.
func TestSegmentAssertionsAppliesTheDeclaredRule(t *testing.T) {
	t.Parallel()
	brief := "# Assessment\n\nRussian forces struck on Dec 29.\n- Sixty-nine missiles were launched.\n*  starred item\n\n# Gaps\n- The exact count is unclear.\n"
	assertions := segmentAssertions(brief)
	if len(assertions) != 4 {
		t.Fatalf("segmented %d assertions, want 4: %+v", len(assertions), assertions)
	}
	if assertions[0].Text != "Russian forces struck on Dec 29." {
		t.Errorf("first assertion %q", assertions[0].Text)
	}
	if assertions[1].Text != "Sixty-nine missiles were launched." {
		t.Errorf("bullet marker not stripped: %q", assertions[1].Text)
	}
	if assertions[2].Text != " starred item" {
		t.Errorf("exactly one marker strips: %q", assertions[2].Text)
	}

	// Determinism: the same brief segments identically.
	again := segmentAssertions(brief)
	for index := range assertions {
		if assertions[index] != again[index] {
			t.Fatal("segmentation is not deterministic")
		}
	}

	// Rune offsets: a non-ASCII character ahead of a line must not shift it.
	unicodeBrief := "Кремль отреагировал.\ncited line\n"
	segments := segmentAssertions(unicodeBrief)
	if len(segments) != 2 || segments[1].Start != 21 {
		t.Errorf("rune offsets diverged: %+v", segments)
	}
}

func coverageFixtureTurn(answer string, citations string, composed ...string) evaluationTurn {
	var turn evaluationTurn
	turn.Answer = answer
	turn.Metadata.AnsweredBy = "cohere"
	turn.Metadata.Composition.Documents = composed
	turn.Metadata.Citations = json.RawMessage(citations)
	return turn
}

// TestCoverageCountsResolvedSpansAgainstTheComposedSet is srd014 R3.1, R3.3,
// and R3.4 as arithmetic: a span covering a line counts only when its record
// id is in the composed set, an unresolvable span is counted and covers
// nothing, and the uncovered assertions are itemized.
func TestCoverageCountsResolvedSpansAgainstTheComposedSet(t *testing.T) {
	t.Parallel()
	answer := "covered line\nuncovered line\nbadly cited line\n"
	citations := `[{"start":0,"end":7,"record_id":"doc-a"},{"start":28,"end":34,"record_id":"doc-outside"}]`
	turn := coverageFixtureTurn(answer, citations, "doc-a", "doc-b")
	result := coverageForTurn("t", turn)
	if result.Assertions != 3 || result.Covered != 1 {
		t.Errorf("covered %d of %d, want 1 of 3", result.Covered, result.Assertions)
	}
	if result.Unresolved != 1 {
		t.Errorf("unresolved = %d; a citation outside the composed set must be counted (srd014 R3.3)", result.Unresolved)
	}
	if len(result.Uncovered) != 2 || !strings.Contains(result.Uncovered[1], "badly cited") {
		t.Errorf("uncovered = %v; the badly cited line covers nothing", result.Uncovered)
	}
	if result.Coverage <= 0.33 || result.Coverage >= 0.34 {
		t.Errorf("coverage = %v, want one third", result.Coverage)
	}
}

// TestCoverageReadsOnlyTheStructuredMetadata pins the R3.4 boundary: a brief
// whose text is full of bracketed source names but whose citation metadata
// is the empty-string wart yields zero coverage -- the text is never
// scraped.
func TestCoverageReadsOnlyTheStructuredMetadata(t *testing.T) {
	t.Parallel()
	turn := coverageFixtureTurn("claim one (rag0, chunk 1)\nclaim two [isw-2022-12-16]\n", `""`, "doc-a")
	result := coverageForTurn("t", turn)
	if result.Covered != 0 || result.Coverage != 0 {
		t.Errorf("coverage = %v over uncited metadata; the answer text must never be scraped", result.Coverage)
	}
	if result.Assertions != 2 {
		t.Errorf("assertions = %d, want 2", result.Assertions)
	}
}
