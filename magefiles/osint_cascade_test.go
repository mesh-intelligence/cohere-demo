// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os/exec"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The search-branch rerank pass (srd011 R3): the cascade's second pass calls
// the one rerank operation the RAG pass calls, reorders within what the
// search returned, trims to the operation's declared budget, and degrades to
// engine order without failing the turn. Suite cases 4, 6, 7, 8, and 9's
// static halves.

// TestCascadePassesShareOneRerankOperation is srd011 R4.4 and suite case 4:
// exactly one rerank operation is declared, and every pass's word names it.
// The passes differ in the candidate set alone -- each dispatches its parse
// word behind the operation's one parse_candidates binding, which the
// command-state store resolves to the most recent match.
func TestCascadePassesShareOneRerankOperation(t *testing.T) {
	t.Parallel()
	// The one operation is the Cohere library's, expanded once (srd058 R1.3);
	// the hand-written client declares none of its own.
	var provider struct {
		Expand []struct {
			Fragment string `yaml:"fragment"`
		} `yaml:"expand"`
	}
	if err := yaml.Unmarshal(readAgentYAML(t, "chatbot", "provider-rest.yaml"), &provider); err != nil {
		t.Fatalf("parse provider-rest.yaml: %v", err)
	}
	var rerankOps int
	for _, expansion := range provider.Expand {
		if strings.HasSuffix(expansion.Fragment, "/rerank-fragment.yaml") {
			rerankOps++
		}
	}
	if rerankOps != 1 {
		t.Fatalf("%d rerank expansions declared; srd011 R4.4 requires exactly one", rerankOps)
	}
	if strings.Contains(string(readAgentYAML(t, "chatbot", "rest.yaml")), "/v2/rerank") {
		t.Error("rest.yaml hand-writes a rerank operation beside the library's")
	}

	tools := chatbotDeclaredTools(t)
	for _, word := range []string{"cohere_rerank_rag", "cohere_rerank_search", "cohere_rerank_fused"} {
		config, ok := tools[word]
		if !ok {
			t.Errorf("%s is not declared", word)
			continue
		}
		if !strings.Contains(config, "rest_ref: reranker") || !strings.Contains(config, "operation: rerank") || strings.Contains(config, "rerank_candidates") {
			t.Errorf("%s does not call the shared rerank operation:\n%s", word, config)
		}
	}
	// The candidate hand-off: the search pass's parse word stands behind the
	// operation's parse_candidates binding on the Cohere machine.
	spec := readMachineSpec(t, "request-machine.yaml")
	labeled := map[string]bool{}
	for _, tr := range spec.Transitions {
		if tr.Label == "parse_candidates" {
			labeled[tr.Action] = true
		}
	}
	// Since GH-443 the marshalling is one pipeline word per pass; each is
	// dispatched under the shared parse_candidates label the operation reads.
	for _, marshal := range []string{"marshal_rag_candidates", "marshal_osint_candidates", "marshal_fused_candidates"} {
		if !labeled[marshal] {
			t.Errorf("%s is not dispatched under the parse_candidates label; the shared operation would score the wrong candidates", marshal)
		}
	}
}

// TestFusedTopNIsTheComposedSet is srd011 R4.3 and suite case 3: the fused
// path's final compose carries the shared composed_documents label the one
// parse reads, its content is the reordered union trimmed by the declared
// budget, and both the rows and the ordering come from the fusion's own
// steps -- so the composed set equals the first N of the fused ordering by
// construction, with N the same declared top_n every pass shares.
func TestFusedTopNIsTheComposedSet(t *testing.T) {
	t.Parallel()
	tools := chatbotDeclaredTools(t)
	reorder, ok := tools["reorder_fused_documents"]
	if !ok {
		t.Fatal("reorder_fused_documents is not declared")
	}
	for _, want := range []string{
		"items: $from(parse_union_documents).body.documents",
		"order: $from(cohere_rerank_fused).body.mapped.results",
		"index_field: index",
	} {
		if !strings.Contains(reorder, want) {
			t.Errorf("reorder_fused_documents is missing %q", want)
		}
	}
	if render := tools["render_fused_documents"]; !strings.Contains(render, "items: $from(reorder_fused_documents).body.items") {
		t.Error("render_fused_documents does not render the fused rows")
	}
	if compose := tools["compose_fused_documents"]; !strings.Contains(compose, "$from(render_fused_documents).$") {
		t.Error("compose_fused_documents does not wrap the fused render")
	}
	if candidates := tools["marshal_fused_candidates"]; !strings.Contains(candidates, "{{ json data.content }}") {
		t.Error("the fusion's candidates are not the union documents' contents; re-scoring content is what keeps the ordering one scale (srd011 R4.2)")
	}
	spec := readMachineSpec(t, "request-machine.yaml")
	var composed bool
	for _, tr := range spec.Transitions {
		if tr.Action == "compose_fused_documents" {
			composed = true
			if tr.Next != "ComposingFusedDocuments" || tr.Label != "composed_documents" {
				t.Errorf("compose_fused_documents dispatches to %s under label %q; the fused set must reach the one document parse",
					tr.Next, tr.Label)
			}
		}
	}
	if !composed {
		t.Error("no transition dispatches compose_fused_documents")
	}
}

// TestFusionFallsBackToTheRerankedRagBranch is srd011 R4.5 as machine
// structure: every failure inside the fusion lands on the degraded status
// word, and the degraded path composes the reranked RAG branch through the
// shared label -- never Failed, never an unranked union.
func TestFusionFallsBackToTheRerankedRagBranch(t *testing.T) {
	t.Parallel()
	fusionStates := map[string]bool{
		"ParsingUnionDocuments":     true,
		"RenderingFusedCandidates":  true,
		"ComposingFusedCandidates":  true,
		"ParsingFusedCandidates":    true,
		"RerankingFused":            true,
		"RenderingFusedRerankOrder": true,
		"ComposingFusedRerankOrder": true,
		"CheckingFusedRerankOrder":  true,
		"ReorderingFusedDocuments":  true,
		"RenderingFusedDocuments":   true,
		"DeclaringFusionApplied":    true,
	}
	spec := readMachineSpec(t, "request-machine.yaml")
	var seen int
	for _, tr := range spec.Transitions {
		if !fusionStates[tr.State] {
			continue
		}
		seen++
		if tr.Next == "Failed" {
			t.Errorf("%s/%s fails the turn; srd011 R4.5 requires the fusion to fall back", tr.State, tr.Signal)
		}
		failure := tr.Signal == "CommandError" || tr.Signal == "ParseFailed" ||
			strings.HasPrefix(tr.Signal, "Provider") || tr.Signal == "FusedRerankOrderEmpty"
		if failure && tr.Action != "declare_fusion_degraded" {
			t.Errorf("%s/%s dispatches %s, not the fusion degradation", tr.State, tr.Signal, tr.Action)
		}
	}
	if seen == 0 {
		t.Fatal("the Cohere machine carries no fusion pass")
	}
	var fallback bool
	for _, tr := range spec.Transitions {
		if tr.State == "DeclaringFusionDegraded" && tr.Signal == "FusionStatusDeclared" {
			fallback = true
			if tr.Next != "ParsingDocuments" || tr.Action != "compose_documents" || tr.Label != "composed_documents" {
				t.Errorf("the fusion fallback composes %s/%s/%s; want the reranked RAG branch through the shared label",
					tr.Next, tr.Action, tr.Label)
			}
		}
	}
	if !fallback {
		t.Error("no fallback transition for DeclaringFusionDegraded")
	}
}

// TestSearchPassReordersWithinTheSearchResults is srd011 R3.3 and suite case
// 8: the reorder's rows and ordering both come from the branch's own steps,
// so no document the search did not surface can enter, and the reorder
// builtin refuses out-of-range or duplicate positions rather than inventing
// membership.
func TestSearchPassReordersWithinTheSearchResults(t *testing.T) {
	t.Parallel()
	tools := chatbotDeclaredTools(t)
	reorder, ok := tools["reorder_osint_results"]
	if !ok {
		t.Fatal("reorder_osint_results is not declared")
	}
	for _, want := range []string{
		// The pass reorders within the snippet-bearing partition of what the
		// search returned; the partition drops rows, never adds them.
		"items: $from(partition_search_results).body.matched",
		"order: $from(cohere_rerank_search).body.mapped.results",
		"index_field: index",
	} {
		if !strings.Contains(reorder, want) {
			t.Errorf("reorder_osint_results is missing %q; the pass must reorder within what the search returned", want)
		}
	}
	if candidates := tools["marshal_osint_candidates"]; !strings.Contains(candidates, "items: $from(partition_search_results).body.matched") {
		t.Error("the search pass's candidate list is not rendered from the search results")
	}
}

// TestSearchCutoffIsTheDeclaredSharedBudget is srd011 R3.2 and suite case 7:
// the trim comes from the one operation's declared top_n -- configuration,
// independent of how many results the engine returned -- because the ordering
// names at most that many rows and the reorder emits only the rows the
// ordering names.
func TestSearchCutoffIsTheDeclaredSharedBudget(t *testing.T) {
	t.Parallel()
	provider := string(readAgentYAML(t, "chatbot", "provider-rest.yaml"))
	if !strings.Contains(provider, "top_n: ${COHERE_RERANK_TOP_N:-8}") {
		t.Error("the shared rerank operation no longer declares its top_n; the search cutoff rides that declaration")
	}
	tools := chatbotDeclaredTools(t)
	if reorder := tools["reorder_osint_results"]; strings.Contains(reorder, "top_n") || strings.Contains(reorder, "limit") {
		t.Error("reorder_osint_results declares its own cutoff; the declared budget lives on the one operation")
	}
}

// TestSearchRerankDegradesToEngineOrder is srd011 R3.4 as machine structure:
// every failure inside the pass lands on the degraded status word, the
// degraded path renders engine order, and no pass state reaches Failed.
func TestSearchRerankDegradesToEngineOrder(t *testing.T) {
	t.Parallel()
	passStates := map[string]bool{
		"RenderingOsintCandidates":  true,
		"ComposingOsintCandidates":  true,
		"ParsingOsintCandidates":    true,
		"RerankingSearch":           true,
		"RenderingOsintRerankOrder": true,
		"ComposingOsintRerankOrder": true,
		"CheckingOsintRerankOrder":  true,
		"ReorderingOsintResults":    true,
	}
	spec := readMachineSpec(t, "request-machine.yaml")
	var seen int
	for _, tr := range spec.Transitions {
		if !passStates[tr.State] {
			continue
		}
		seen++
		if tr.Next == "Failed" {
			t.Errorf("%s/%s fails the turn; srd011 R3.4 requires the pass to degrade", tr.State, tr.Signal)
		}
		failure := tr.Signal == "CommandError" || tr.Signal == "ParseFailed" ||
			strings.HasPrefix(tr.Signal, "Provider") || tr.Signal == "OsintRerankOrderEmpty"
		if failure && tr.Action != "declare_osint_rerank_degraded" {
			t.Errorf("%s/%s dispatches %s, not the degraded status word", tr.State, tr.Signal, tr.Action)
		}
	}
	if seen == 0 {
		t.Fatal("the Cohere machine carries no search rerank pass")
	}
	for state, action := range map[string]string{
		"DeclaringOsintRerankApplied":  "render_osint_documents",
		"DeclaringOsintRerankDegraded": "render_osint_documents_unranked",
	} {
		var found bool
		for _, tr := range spec.Transitions {
			if tr.State != state || tr.Signal != "OsintRerankStatusDeclared" {
				continue
			}
			found = true
			if tr.Next != "RenderingOsintDocuments" || tr.Action != action || tr.Label != "osint_documents" {
				t.Errorf("%s rejoins at %s/%s/%s; want RenderingOsintDocuments/%s/osint_documents",
					state, tr.Next, tr.Action, tr.Label, action)
			}
		}
		if !found {
			t.Errorf("no rejoin transition for %s", state)
		}
	}
}

// TestThreePassCascadeIsTheDefault is srd011 R6.4 and suite case 10: the
// three-pass cascade is the default, the single-fusion collapse is a
// declared, rendered configuration -- not a code path a request can select
// -- and the rewrite mechanism is pinned the way the air-gap toggle's is.
func TestThreePassCascadeIsTheDefault(t *testing.T) {
	t.Parallel()
	values := readChartFile(t, "values.yaml")
	if !strings.Contains(values, "cascade: three_pass") {
		t.Error("values.yaml does not default the cascade to three_pass")
	}
	decl := string(readAgentYAML(t, "chatbot", "request-osint-declarations.yaml"))
	if strings.Count(decl, "left: cascade-three-pass") != 1 || strings.Contains(decl, "left: cascade-single-fusion") {
		t.Error("the cascade-mode operand is not unique in the committed declarations; the rendered rewrite would miss or overreach")
	}
	tpl := readChartFile(t, "templates", "profiles-configmap.yaml")
	if !strings.Contains(tpl, `replace "left: cascade-three-pass" "left: cascade-single-fusion"`) {
		t.Error("the profiles ConfigMap does not rewrite the cascade operand for a single_fusion deployment")
	}
	// The schema admits exactly the two recorded modes: single_fusion renders,
	// anything else is refused at template time.
	_ = renderOSINTChart(t, "--set", "osint.cascade=single_fusion")
	if _, err := exec.Command("helm", "template", "relx", findChartDir(t),
		"--set", "osint.cascade=bogus").CombinedOutput(); err == nil {
		t.Error("an undeclared cascade mode rendered; the schema must refuse it")
	}
}

// TestSingleFusionSkipsThePerBranchPasses is the machine half of suite case
// 10: under the collapse each branch takes a skip leg that records the
// skipped status and composes in retrieval order, a comparison failure takes
// the default three-pass path, and the fusion itself is untouched -- the
// collapse changes what feeds the union, never how it is ordered.
func TestSingleFusionSkipsThePerBranchPasses(t *testing.T) {
	t.Parallel()
	spec := readMachineSpec(t, "request-machine.yaml")
	type route struct{ next, action, label string }
	want := map[string]map[string]route{
		"CheckingRagCascadeMode": {
			"CascadeThreePass":    {"MarshallingRagCandidates", "marshal_rag_candidates", "parse_candidates"},
			"CascadeSingleFusion": {"DeclaringRerankSkipped", "declare_rerank_skipped", "rerank_status"},
			"CommandError":        {"MarshallingRagCandidates", "marshal_rag_candidates", "parse_candidates"},
		},
		"CheckingSearchCascadeMode": {
			"CascadeThreePass":    {"MarshallingOsintCandidates", "marshal_osint_candidates", "parse_candidates"},
			"CascadeSingleFusion": {"DeclaringOsintRerankSkipped", "declare_osint_rerank_skipped", "osint_rerank_status"},
			"CommandError":        {"MarshallingOsintCandidates", "marshal_osint_candidates", "parse_candidates"},
		},
		"DeclaringRerankSkipped": {
			"RerankStatusDeclared": {"ComposingDocuments", "render_documents_unranked", "rag_documents"},
		},
		"DeclaringOsintRerankSkipped": {
			"OsintRerankStatusDeclared": {"RenderingOsintDocuments", "render_osint_documents_unranked", "osint_documents"},
		},
	}
	found := map[string]map[string]bool{}
	for _, tr := range spec.Transitions {
		routes, ok := want[tr.State]
		if !ok {
			continue
		}
		expect, ok := routes[tr.Signal]
		if !ok {
			continue
		}
		if found[tr.State] == nil {
			found[tr.State] = map[string]bool{}
		}
		found[tr.State][tr.Signal] = true
		if tr.Next != expect.next || tr.Action != expect.action || tr.Label != expect.label {
			t.Errorf("%s/%s routes %s/%s/%s; want %s/%s/%s",
				tr.State, tr.Signal, tr.Next, tr.Action, tr.Label, expect.next, expect.action, expect.label)
		}
	}
	for state, routes := range want {
		for signal := range routes {
			if !found[state][signal] {
				t.Errorf("no transition for %s/%s", state, signal)
			}
		}
	}
}
