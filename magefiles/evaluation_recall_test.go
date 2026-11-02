// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"strings"
	"testing"
)

// Retrieval recall (srd014 R2): the arithmetic, the chunk-to-document
// mapping the labels are measured through, and the composition-ids
// projection the measurement reads.

// TestParentDocumentIDStripsChunkSuffixes pins the store's chunk identity
// scheme onto the document identity the labels are written at: every declared
// chunking profile strips, an external URL identity passes through untouched,
// and a document id with no suffix is its own parent.
func TestParentDocumentIDStripsChunkSuffixes(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"isw-2022-12-16-medium-900-0002":                              "isw-2022-12-16",
		"HR-22-014-atomic-0001":                                       "HR-22-014",
		"losses-2022-12-17-large-1800-01":                             "losses-2022-12-17",
		"missile-2022-12-31":                                          "missile-2022-12-31",
		"https://osint-fixture.invalid/reports/december-2022-strikes": "https://osint-fixture.invalid/reports/december-2022-strikes",
	}
	for chunk, want := range cases {
		if got := parentDocumentID(chunk); got != want {
			t.Errorf("parentDocumentID(%q) = %q, want %q", chunk, got, want)
		}
	}
}

// TestTaskingRecallCountsAtTheComposedSet is srd014 R2.1 and R2.2 as
// arithmetic: the numerator is labeled documents present in the composed
// set, the denominator is the label count, a document retrieved but dropped
// before composition does not count, and the missing list names what recall
// lost.
func TestTaskingRecallCountsAtTheComposedSet(t *testing.T) {
	t.Parallel()
	tasking := evaluationTasking{ID: "t"}
	for _, id := range []string{"doc-a", "doc-b", "doc-c", "doc-d"} {
		tasking.Relevant = append(tasking.Relevant, struct {
			ID     string `yaml:"id"`
			Source string `yaml:"source"`
		}{ID: id, Source: "osint_assessment"})
	}
	turn := evaluationTurn{}
	turn.Metadata.Composition.Documents = []string{
		"doc-a-medium-900-0001",
		"doc-a-medium-900-0002",
		"doc-c-atomic-0003",
		"doc-unrelated-medium-900-0001",
	}
	recall, missing := taskingRecall(tasking, composedDocuments(turn))
	if recall != 0.5 {
		t.Errorf("recall = %v, want 0.5 (two of four labels composed)", recall)
	}
	if strings.Join(missing, ",") != "doc-b,doc-d" {
		t.Errorf("missing = %v; the report must name what recall lost", missing)
	}
}

// TestCompositionIdsAreProjectedForTheResponse pins the srd014 R2.2
// observability: the projection reads the validated composed array's ids and
// nothing else, the machine dispatches it under the composition_ids label
// between the document parse and the prompt, and compose_response reports it
// as the composition documents list.
func TestCompositionIdsAreProjectedForTheResponse(t *testing.T) {
	t.Parallel()
	tools := chatbotDeclaredTools(t)
	render, ok := tools["render_composition_ids"]
	if !ok {
		t.Fatal("render_composition_ids is not declared")
	}
	if !strings.Contains(render, "items: $from(parse_documents).body.documents") ||
		!strings.Contains(render, "{{ json id }}") {
		t.Error("the projection must render the validated composed ids and only the ids")
	}
	if compose := tools["compose_response"]; !strings.Contains(compose, "$from(composition_ids).$") ||
		!strings.Contains(compose, `"composition": {"documents": [{{ composition }}]}`) {
		t.Error("compose_response does not report the composition documents list")
	}
	spec := readMachineSpec(t, "request-machine.yaml")
	var dispatched, degrades bool
	for _, tr := range spec.Transitions {
		if tr.Action == "render_composition_ids" {
			dispatched = tr.State == "Composing" && tr.Signal == "DocumentsParsed" && tr.Label == "composition_ids"
		}
		if tr.State == "RenderingCompositionIds" && tr.Signal == "CommandError" {
			degrades = tr.Next == "SelectingTier" && tr.Action == "compose_prompt"
		}
	}
	if !dispatched {
		t.Error("render_composition_ids is not dispatched from the document parse under the composition_ids label")
	}
	if !degrades {
		t.Error("a projection failure must degrade to an unreported composition, never the turn")
	}
}

// TestRecallSteerStringsMatchTheCommittedDeclarations pins the harness's
// steer constants to the committed files, so a rename in the declarations
// fails here instead of silently steering nothing.
func TestRecallSteerStringsMatchTheCommittedDeclarations(t *testing.T) {
	t.Parallel()
	fanout := string(readAgentYAML(t, "chatbot", "request-fanout-declarations.yaml"))
	if !strings.Contains(fanout, evaluationRagCheckEnabled) {
		t.Error("the RAG order-check signals moved; the without-rerank steer would miss")
	}
	if strings.Contains(fanout, evaluationRagCheckInverted) {
		t.Error("the committed declarations carry the inverted RAG order check")
	}
	osint := string(readAgentYAML(t, "chatbot", "request-osint-declarations.yaml"))
	if !strings.Contains(osint, osintEnabledOperand) {
		t.Error("the air-gap operand moved; the branch-disabled steer would miss")
	}
}

// TestRecallCeilingIsTheAchievableBound pins the GH-576 normalisation: a
// 10-document labeled set scored at N=8 can reach 0.8 at best, so the ceiling
// is 8/10; an N covering the whole set (or an untrimmed run, n<=0) has
// ceiling 1.
func TestRecallCeilingIsTheAchievableBound(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n, relevant int
		want        float64
	}{
		{8, 10, 0.8},
		{8, 8, 1},
		{8, 3, 1},
		{0, 10, 1},
		{12, 10, 1},
		{8, 0, 1},
	}
	for _, c := range cases {
		if got := recallCeiling(c.n, c.relevant); got != c.want {
			t.Errorf("recallCeiling(%d, %d) = %v, want %v", c.n, c.relevant, got, c.want)
		}
	}
}

// TestTaskingRecallAtKTruncatesTheCompositionOrder pins the same-K comparator
// (GH-576): the first k chunks of the untrimmed composition order are the
// cosine@K set, and a relevant document sitting past k does not count.
func TestTaskingRecallAtKTruncatesTheCompositionOrder(t *testing.T) {
	t.Parallel()
	tasking := evaluationTasking{}
	tasking.Relevant = append(tasking.Relevant,
		struct {
			ID     string `yaml:"id"`
			Source string `yaml:"source"`
		}{ID: "isw-2022-12-16"},
		struct {
			ID     string `yaml:"id"`
			Source string `yaml:"source"`
		}{ID: "losses-2022-12-17"})
	ids := []string{
		"isw-2022-12-16-medium-900-0001",
		"missile-2022-12-05-medium-900-0001",
		"losses-2022-12-17-medium-900-0001",
	}
	if got := taskingRecallAtK(tasking, ids, 2); got != 0.5 {
		t.Errorf("recall at k=2 = %v, want 0.5 (the losses document sits past the cut)", got)
	}
	if got := taskingRecallAtK(tasking, ids, 3); got != 1 {
		t.Errorf("recall at k=3 = %v, want 1", got)
	}
	if got := taskingRecallAtK(tasking, ids, 0); got != 1 {
		t.Errorf("recall untruncated = %v, want 1", got)
	}
}

// TestSweepShapePatchesTheRenderedLiterals pins the sweep's patch mechanics
// (GH-576): breadth and top_n rewrite every rendered occurrence, three_pass
// leaves the cascade operand alone, and single_fusion rewrites it once.
func TestSweepShapePatchesTheRenderedLiterals(t *testing.T) {
	t.Parallel()
	rest := "a\n  n_results: 20\nb\n  n_results: 20\n  top_n: 8\n"
	patched := strings.ReplaceAll(rest, "n_results: 20", "n_results: 40")
	patched = strings.ReplaceAll(patched, "top_n: 8", "top_n: 12")
	if strings.Contains(patched, "n_results: 20") || !strings.Contains(patched, "top_n: 12") {
		t.Fatalf("rendered literal rewrite failed: %q", patched)
	}
	if got := renderedLiteral(rest, "top_n"); got != "8" {
		t.Errorf("renderedLiteral top_n = %q, want 8", got)
	}
	if got := renderedLiteral(rest, "n_results"); got != "20" {
		t.Errorf("renderedLiteral n_results = %q, want 20", got)
	}
	osint := "x\n      left: cascade-three-pass\ny\n"
	if !strings.Contains(strings.Replace(osint, evalCascadeThreePass, evalCascadeSingleFusion, 1),
		"cascade-single-fusion") {
		t.Error("cascade operand rewrite failed")
	}
}
