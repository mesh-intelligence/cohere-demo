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

// The chart co-generates the chatbot's rest.yaml, so a Cohere client added only
// to the packaged profile is invisible to every deployment. These lock the two
// halves together and pin the property that makes the provider switchable
// rather than a fork: with it disabled, the chart renders what it rendered
// before the provider existed (srd009 R6.3).

func renderChart(t *testing.T, sets ...string) (string, bool) {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	args := []string{"template", "t", findChartDir(t)}
	for _, set := range sets {
		args = append(args, "--set", set)
	}
	out, err := exec.Command("helm", args...).CombinedOutput()
	return string(out), err == nil
}

func TestChartOmitsCohereWhenDisabled(t *testing.T) {
	out, ok := renderChart(t)
	if !ok {
		t.Fatalf("default render failed:\n%s", out)
	}
	// Absent, not merely unselected. A deployment with no credential should have
	// nothing to misconfigure.
	for _, absent := range []string{
		"COHERE_BASE_URL", "COHERE_API_KEY", "COHERE_EMBED_MODEL",
		"COHERE_RERANK_MODEL", "COHERE_RERANK_TOP_N",
		"cohere_bearer", "cohere_provider", "compose_brief", "embed_query_cohere", "/v2/embed",
		"/opt/cohere/rerank-fragment.yaml", "/opt/cohere/embed-document-fragment.yaml", "/v2/rerank",
	} {
		if strings.Contains(out, absent) {
			t.Errorf("the default render carries %q; the disabled provider must render nothing (srd009 R6.3)", absent)
		}
	}
}

func TestChartRendersCohereClientAndSecretReference(t *testing.T) {
	out, ok := renderChart(t, "cohere.enabled=true", "cohere.existingSecret=cohere-credential")
	if !ok {
		t.Fatalf("enabled render failed:\n%s", out)
	}
	// The co-generated rest.yaml and the Deployment environment must both carry
	// the provider, or the pod reads a client the chart never wired.
	rest := configMapKeyBlock(out, "agents__chatbot__rest.yaml")
	if rest == "" {
		t.Fatal("co-generated agents__chatbot__rest.yaml key not found in render")
	}
	for _, want := range []string{
		"cohere_bearer", "token_ref: COHERE_API_KEY", "cohere_provider",
		"compose_brief", "path: /v2/chat",
		// The fan-out reads the parsed query vector; the embedding itself comes
		// from the library operation embed-rest.yaml instantiates (srd058 R4.2).
		"$from(parse_query_vector).body.embedding",
	} {
		if !strings.Contains(rest, want) {
			t.Errorf("co-generated rest.yaml missing %q; the chart wired an environment the client does not use", want)
		}
	}
	for _, want := range []string{"COHERE_BASE_URL", "COHERE_HOST", "COHERE_SCHEME", "COHERE_PORT", "COHERE_EMBED_MODEL", "secretKeyRef"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q", want)
		}
	}
}

// The allowlist and the base URL are one configuration point. A render that
// moved the endpoint while leaving the allowlist behind would produce a
// deployment whose every Cohere call is refused by its own client — the
// in-boundary case srd009 R1.2 exists to support.
func TestChartMovesTheCohereAllowlistWithTheEndpoint(t *testing.T) {
	out, ok := renderChart(t,
		"cohere.enabled=true",
		"cohere.existingSecret=cohere-credential",
		"cohere.baseURL=https://cohere.ministry.internal",
		"cohere.host=cohere.ministry.internal",
	)
	if !ok {
		t.Fatalf("in-boundary render failed:\n%s", out)
	}
	rest := configMapKeyBlock(out, "agents__chatbot__rest.yaml")
	if !strings.Contains(rest, "base_url: https://cohere.ministry.internal") {
		t.Error("the co-generated client does not carry the configured in-boundary endpoint")
	}
	if !strings.Contains(rest, "hosts: [cohere.ministry.internal]") {
		t.Error("the egress allowlist still names the hosted endpoint; every in-boundary call would be refused by the client")
	}
	if strings.Contains(rest, "hosts: [api.cohere.com]") {
		t.Error("the render carries the default allowlist host alongside an in-boundary endpoint")
	}
	// The library rerank and document embedding take the same endpoint as their
	// allow arguments (srd058 R1.6), or they would reach the hosted API.
	provider := configMapKeyBlock(out, "agents__chatbot__provider-rest.yaml")
	if strings.Count(provider, "allow_host: cohere.ministry.internal") != 2 {
		t.Errorf("the co-generated provider-rest.yaml does not point both library expansions at the in-boundary endpoint:\n%s", provider)
	}
}

// The chart takes a Secret name, never a credential. This is the check between
// a convenient values field and a credential in `helm get values`.
func TestChartAcceptsNoCohereCredentialValue(t *testing.T) {
	out, ok := renderChart(t, "cohere.enabled=true", "cohere.existingSecret=cohere-credential",
		"cohere.apiKey=co-live-abcdef0123456789")
	if ok {
		t.Fatal("the chart accepted a cohere.apiKey values field; a credential would land in helm get values (srd009 R1.3)")
	}
	if !strings.Contains(out, "additionalProperties") && !strings.Contains(out, "apiKey") {
		t.Errorf("the rejection does not name the unknown field:\n%s", firstRunes(out, 300))
	}
}

// TestNeitherRestHalfHandWritesAProviderOperation holds both halves of the
// chatbot's REST to the provider library (srd058 R1.3, R4.2): the query
// embedding, the rerank, and the harvest's document embedding. request-machine.yaml
// is projected verbatim while rest.yaml is co-generated, so a hand-written
// operation surviving in either half would give the same operation two
// declarations for a reviewer to keep in step, and the library one is the one
// the tier rebinds.
func TestNeitherRestHalfHandWritesAProviderOperation(t *testing.T) {
	out, ok := renderChart(t, "cohere.enabled=true", "cohere.existingSecret=cohere-credential")
	if !ok {
		t.Fatalf("enabled render failed:\n%s", out)
	}
	rendered := configMapKeyBlock(out, "agents__chatbot__rest.yaml")
	if rendered == "" {
		t.Fatal("co-generated agents__chatbot__rest.yaml key not found in render")
	}
	shipped, err := os.ReadFile("../agents/chatbot/rest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for half, body := range map[string]string{"co-generated": rendered, "shipped": string(shipped)} {
		for _, stale := range []string{"embed_query_cohere:", "path: /api/embeddings", "input_type: search_query", "path: /v2/rerank", "input_type: search_document"} {
			if strings.Contains(body, stale) {
				t.Errorf("the %s rest.yaml still declares %q; the operation is the library's", half, stale)
			}
		}
	}
}

// The citation chain reaches the pod through the composition the chart stages,
// not through the profile a local rig loads. Every other gate — the scenario
// critics, integration:cohere, the SPA suite — passes against sources the chart
// never staged, so a packaging step that dropped these files would leave the
// whole release green and the deployed panel showing nothing (srd009 R4.1,
// R4.2).
func TestChartStagesTheCitationSurfacingChain(t *testing.T) {
	applicationRoot := filepath.Clean("..")
	catalogRoot, err := resolveCatalogRoot("citation staging test", applicationRoot)
	if err != nil {
		t.Fatal(err)
	}
	chart := filepath.Join(t.TempDir(), "chatbot-mesh")
	if err := stageChatbotChartSource(filepath.Join(applicationRoot, "helm"), chart); err != nil {
		t.Fatal(err)
	}
	if err := stageChatbotComposition(chart, applicationRoot, catalogRoot); err != nil {
		t.Fatal(err)
	}

	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(
			chart, "profiles", "agents", "chatbot", name))
		if err != nil {
			t.Fatalf("the composition staged no %s: %v", name, err)
		}
		return string(data)
	}

	declarations := read("request-fanout-declarations.yaml")
	machine := read("request-machine.yaml")
	for _, word := range []string{
		"flatten_citations",
		"partition_cited_sources",
		"render_citations",
		"render_dropped_citations",
		"compose_citations",
		"parse_citations",
	} {
		if !strings.Contains(declarations, word) {
			t.Errorf("the staged fan-out declarations carry no %s word;"+
				" the deployed turn would surface no citations", word)
		}
	}
	// The response is where a reader meets the result. Declaring the chain
	// without carrying its output leaves the panel reading a field the pod
	// never emits.
	for _, field := range []string{`"citations"`, `"dropped_citations"`} {
		if !strings.Contains(declarations, field) {
			t.Errorf("the staged response template omits %s", field)
		}
	}
	// The documents payload is what the citations resolve against. One document
	// per source would make every citation name a rag unit rather than a chunk.
	if !strings.Contains(declarations, "flatten_chunks") {
		t.Error("the staged declarations carry no flatten_chunks word;" +
			" the deployed turn would send one document per source and cite only rag units")
	}

	// The rerank pass ships the same way and fails the same way if it does not:
	// a deployment missing these words composes in the fan-out's order while
	// every local gate passes (srd009 R5).
	for _, word := range []string{
		"marshal_rag_candidates",
		"cohere_rerank_rag",
		"check_rerank_order",
		"reorder_candidates",
		"render_documents_unranked",
		"declare_rerank_applied",
		"declare_rerank_degraded",
	} {
		if !strings.Contains(declarations, word) {
			t.Errorf("the staged fan-out declarations carry no %s word;"+
				" the deployed turn would compose in the fan-out order", word)
		}
	}
	if !strings.Contains(declarations, `"rerank"`) {
		t.Error("the staged response template omits the rerank outcome," +
			" so a deployed degradation would be invisible (srd009 R5.4)")
	}
	for _, state := range []string{
		"RerankingRag", "ReorderingCandidates", "CheckingRerankOrder",
		"DeclaringRerankApplied", "DeclaringRerankDegraded",
	} {
		if !strings.Contains(machine, state) {
			t.Errorf("the staged request machine declares no %s state", state)
		}
	}

	// The chatbot's REST client is co-generated by the chart rather than staged
	// from the profile, so an operation added only to agents/chatbot/rest.yaml
	// loads locally and fails the pod with "REST operation is not defined on
	// client cohere". That is how the rerank reached a deployment before this
	// assertion existed.
	rest := read("rest.yaml")
	for _, operation := range []string{"compose_brief", "select_sources_tool"} {
		if !strings.Contains(rest, operation) {
			t.Errorf("the co-generated chatbot rest.yaml declares no %s operation;"+
				" the deployed pod would refuse to load a word that dispatches it", operation)
		}
	}
	// The rerank and the harvest's document embedding come from the Cohere
	// library, co-generated the same way (srd058 R1.3).
	provider := read("provider-rest.yaml")
	for _, fragment := range []string{"/opt/cohere/rerank-fragment.yaml", "/opt/cohere/embed-document-fragment.yaml"} {
		if !strings.Contains(provider, fragment) {
			t.Errorf("the co-generated chatbot provider-rest.yaml expands no %s;"+
				" the deployed pod would refuse to load a word that dispatches it", fragment)
		}
	}

	// A word the machine never reaches is a word that does not run, and the
	// declarations alone cannot show that it is reached.
	for _, state := range []string{
		"FlatteningCitations",
		"PartitioningCitedSources",
		"RenderingCitations",
		"RenderingDroppedCitations",
		"ComposingCitations",
		"ParsingCitations",
	} {
		if !strings.Contains(machine, state) {
			t.Errorf("the staged request machine declares no %s state", state)
		}
	}
}
