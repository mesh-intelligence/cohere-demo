// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The prose-drift tripwire (GH-306). mage audit validates index paths,
// touchpoints, and traceability edges; summary prose, goal statements, and
// diagram labels are outside what it reads, so a figure stated in three
// documents disagreed with itself five times before anything caught it. A
// general figure-extraction check was scoped and rejected -- numbers pervade
// these documents and most repetition is legitimate, so extraction drowns the
// signal in false positives. What holds is a tripwire: each drift found by
// hand becomes a banned phrase in the document that carried it, with the
// authoritative statement named, so the same correction cannot silently come
// undone. New drifts join the table when review finds them.
func TestProseDoesNotReintroduceKnownDrifts(t *testing.T) {
	t.Parallel()
	tripwires := []struct {
		file      string
		banned    string
		authority string
	}{
		{
			// GH-294: SPECIFICATIONS described a twelve-month corpus window in
			// two passages. Window statements belong to srd017 R3.1, which
			// declares December 2022.
			file:      "docs/SPECIFICATIONS.yaml",
			banned:    `twelve[- ]month`,
			authority: "srd017 R3.1 declares the December 2022 window",
		},
		{
			// GH-294 and GH-306: the trace diagram said "twelve months
			// converted whole" and G3 said "one twelve-month window". R3.1
			// declares the window inside the tier-overlap span; R3.3 converts
			// the declared window, not the span. The overview's twelve-month
			// sizing illustration and R3.3's recorded measurement stay legal:
			// only the window claim is banned.
			file:      "docs/specs/software-requirements/srd017-real-demo-corpus.yaml",
			banned:    `twelve[- ]month window|twelve months converted`,
			authority: "srd017 R3.1 declares the window inside the overlap span",
		},
		{
			// GH-305: VISION called the corpus synthetic in the Evidence phase
			// and the risk list. Four tiers are real published reporting and
			// one is invented, and srd017 R9.4 requires the mixed
			// qualification on every measured-figure claim.
			file:      "docs/VISION.yaml",
			banned:    `a synthetic, clearly fictional corpus|seed corpus is synthetic`,
			authority: "srd017 R9.4 states the mixed real-and-synthetic qualification",
		},
	}
	for _, tripwire := range tripwires {
		data, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(tripwire.file)))
		if err != nil {
			t.Fatalf("read %s: %v", tripwire.file, err)
		}
		pattern := regexp.MustCompile(`(?i)` + tripwire.banned)
		if match := pattern.Find(data); match != nil {
			t.Errorf("%s carries %q, a corrected prose drift reintroduced; %s (GH-306)",
				tripwire.file, match, tripwire.authority)
		}
	}
}
