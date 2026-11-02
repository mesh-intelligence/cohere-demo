// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The named unit gates of test-rel11.5-intsum-ui.yaml. Each Go test here is
// one the suite cites by name, so the suite's go_test references resolve to a
// runnable check rather than to a description.
//
// The form-contract behaviour lives in the SPA's vitest suite, where the
// contract itself lives; the Go tests for those cases run the named vitest
// tests rather than re-implementing the contract in a second language the way
// the template cross-check deliberately does for structure. A missing
// checkout or toolchain is a named skip, matching the other UI gates.

// runNamedVitest runs the SPA tests whose names contain each pattern and
// fails when any pattern matches no test -- a renamed vitest test must break
// this gate rather than leave the suite citing a check that no longer runs.
func runNamedVitest(t *testing.T, file string, patterns ...string) {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	app := chatbotUIAppDir(filepath.Dir(root))
	if reason := chatbotUISkipReason(app); reason != "" {
		t.Skipf("SPA checkout unavailable: %s", reason)
	}
	if err := ensureChatbotUIDependencies(app); err != nil {
		t.Fatalf("install SPA dependencies: %v", err)
	}
	for _, pattern := range patterns {
		command := exec.Command("npx", "vitest", "run", "-t", pattern, file)
		command.Dir = app
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("vitest -t %q failed:\n%s", pattern, out)
		}
		if strings.Contains(string(out), "No test files found") ||
			!strings.Contains(string(out), "passed") {
			t.Fatalf("vitest -t %q matched no passing test:\n%s", pattern, out)
		}
	}
}

// TestPanelRoutesMatchTheServedConfiguration is the suite's name for the
// two-direction panel cross-check (srd016 R1.3):
// TestChatbotUIPanelsMatchTheRegistry holds the assertions, and this gate pins
// that both directions stay checked against the same two artifacts. Since
// GH-737 those are ui.yaml's version 2 panels and the SPA's panel registry.
func TestPanelRoutesMatchTheServedConfiguration(t *testing.T) {
	t.Parallel()
	declared := declaredUIPanels(t)
	registry := registryPanels(t)
	for _, panel := range declared.Panels {
		if panel.Package == "@declarative-agents/ui-kit" {
			continue
		}
		if !registry[panel.ID] {
			t.Errorf("panel %q is declared in ui.yaml and absent from the application", panel.ID)
		}
	}
	declaredIDs := map[string]bool{}
	for _, panel := range declared.Panels {
		declaredIDs[panel.ID] = true
	}
	for id := range registry {
		if !declaredIDs[id] {
			t.Errorf("panel %q is mounted by the application and absent from ui.yaml", id)
		}
	}
}

// TestFormFieldsTraceToResponseData: every filled field traces to response
// data or stated context, and a section with no data renders empty rather
// than plausible (srd016 R3.1, R3.2).
func TestFormFieldsTraceToResponseData(t *testing.T) {
	runNamedVitest(t, "src/intsumForm.test.ts",
		"fills a section only from prose the provider composed",
		"leaves a section the provider wrote no heading for empty",
		"fills the header only from context the caller supplied",
	)
}

// TestDegradedTurnRendersAnEmptyForm: each degraded outcome the mesh
// distinguishes renders the form's headings without content (srd016 R3.3).
func TestDegradedTurnRendersAnEmptyForm(t *testing.T) {
	runNamedVitest(t, "src/intsumForm.test.ts",
		"renders an empty form for each degraded outcome",
		"renders the headings and no content for a turn that composed nothing",
	)
}

// TestSourceEvaluationTableCarriesAdmiraltyRatings: the table carries the
// declared ratings and an ungraded source shows as ungraded (srd016 R3.4).
func TestSourceEvaluationTableCarriesAdmiraltyRatings(t *testing.T) {
	runNamedVitest(t, "src/intsumForm.test.ts",
		"carries the declared reliability and credibility of a graded source",
		"shows a source carrying no grade as ungraded rather than blank",
	)
}

// TestCitationIndexDeduplicatesAndBannerMarksComposedMaterial: one index
// entry per cited record, and the banner marks the composed sources rather
// than the requester (srd016 R3.5, R3.6).
func TestCitationIndexDeduplicatesAndBannerMarksComposedMaterial(t *testing.T) {
	runNamedVitest(t, "src/intsumForm.test.ts",
		"indexes one entry per record however many times the brief cited it",
		"marks the banner from the composed sources and never from the requester",
	)
}

// TestProvenanceWalkCollapsesRepeatedCitations: the walk's entry is the
// citation index, so the collapse that keeps seventy citations one node is
// the index's dedup carrying its assertions into the walk (srd016 R6.4).
func TestProvenanceWalkCollapsesRepeatedCitations(t *testing.T) {
	runNamedVitest(t, "src/intsumForm.test.ts",
		"indexes one entry per record however many times the brief cited it",
		"collects the distinct assertions resting on one record",
	)
}

// TestFacetsNarrowAndNeverWiden pins the structural argument (srd016 R4.3):
// the clearance conjunct is composed at the retrieval tier from its own
// resolved window, first in the conjunction, and the caller's facet is only
// ever a further conjunct -- so no facet can raise the level or widen the
// admitted set, whatever the client sends.
func TestFacetsNarrowAndNeverWiden(t *testing.T) {
	t.Parallel()
	decl := string(readAgentYAML(t, "rag-server", "request-declarations.yaml"))
	if !strings.Contains(decl,
		`template: '{"where": {"$and": [{"clearance": {"$in": {{ json allowed }}}}, {{ json facet_where }}]}}'`) {
		t.Fatal("the faceted filter no longer conjoins the caller's facet under the resolved clearance window")
	}
	if !strings.Contains(decl, "allowed: $from(render_clearance_window).body.allowed") {
		t.Error("the faceted filter's clearance operand must come from the resolved window, never the request")
	}
	// The client side keeps the same discipline: the payload builder never
	// composes a clearance term, pinned by the SPA suite.
	runNamedVitest(t, "src/api.test.ts", "never composes a clearance conjunct")
}

// TestDocumentReadClearanceComesFromTheAuthenticatedContext: the document
// reads derive the level from the request header the way the chat turn does,
// no body field can name it, and a read whose filter cannot apply is refused
// rather than served open (srd016 R5.3, R5.5; srd017 R8.7).
func TestDocumentReadClearanceComesFromTheAuthenticatedContext(t *testing.T) {
	t.Parallel()
	chatbotRest := yamlDoc(t, readAgentYAML(t, "chatbot", "rest.yaml"))
	for _, endpoint := range []string{"documents", "documents_query"} {
		spec := walk(t, chatbotRest, "rest", "servers", "chatbot_chat", "endpoints", endpoint)
		schema := walk(t, spec, "request", "body_schema")
		properties, _ := schema["properties"].(map[string]any)
		if _, present := properties["max_clearance"]; present {
			t.Errorf("%s accepts a clearance in the body; the level must come from the header alone", endpoint)
		}
		request := walk(t, spec, "machine_request", "request")
		headers, _ := request["headers"].(map[string]any)
		if headers["max_clearance"] != "$.x-analyst-clearance" {
			t.Errorf("%s does not derive the level from the authenticated header", endpoint)
		}
	}
	ragRest := string(readAgentYAML(t, "rag-server", "rest.yaml"))
	if !strings.Contains(ragRest, "error: clearance_refused") {
		t.Error("the rag-server no longer maps a refused level; an inapplicable filter must refuse rather than serve")
	}
	if !strings.Contains(ragRest, "document_filter: {type: object, minProperties: 1}") {
		t.Error("the filter read no longer requires a non-empty constraint; an empty one would read as every record at the level")
	}
}
