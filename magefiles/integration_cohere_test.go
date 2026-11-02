// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The live target cannot be unit tested — it needs a provider, a container, and
// models. Its decisions can be, and these are the ones that would otherwise
// only be exercised on a machine that has all three.

func TestCohereSkipReasonNamesTheMissingCredential(t *testing.T) {
	t.Setenv(cohereCredentialEnv, "")
	pinAbsentCredentialFile(t)
	reason := cohereSkipReason("..")
	if reason == "" {
		t.Fatal("no skip reason with the credential unset; integration:all would fail on a machine with no Cohere access")
	}
	if !strings.Contains(reason, cohereCredentialEnv) {
		t.Errorf("skip reason %q does not name %s; an operator cannot tell what to export", reason, cohereCredentialEnv)
	}
}

// A credential of spaces is unset in every sense that matters, and reporting it
// as present sends the target into a standup that cannot authenticate.
func TestCohereSkipReasonTreatsBlankCredentialAsUnset(t *testing.T) {
	t.Setenv(cohereCredentialEnv, "   ")
	pinAbsentCredentialFile(t)
	if reason := cohereSkipReason(".."); !strings.Contains(reason, cohereCredentialEnv) {
		t.Errorf("a whitespace credential produced %q, want a skip naming %s", reason, cohereCredentialEnv)
	}
}

func TestCohereClientDeclarationIsPinned(t *testing.T) {
	if err := assertCohereClientPinned(".."); err != nil {
		t.Fatalf("shipped chatbot profile: %v", err)
	}
}

func TestCohereClientDeclarationReadsTheShippedProfile(t *testing.T) {
	client, err := cohereClientDeclaration("..")
	if err != nil {
		t.Fatal(err)
	}
	// Read unexpanded: the declaration is the environment reference, and pinning
	// the resolved value here would fail the moment a deployment configured a
	// private in-boundary endpoint, which srd009 R1.2 exists to allow.
	if !strings.Contains(client.BaseURL, "COHERE_BASE_URL") {
		t.Errorf("base_url is %q; srd009 R1.2 requires a deployment-supplied endpoint", client.BaseURL)
	}
	if client.LimitsRef != "cohere_provider" {
		t.Errorf("limits_ref is %q, want cohere_provider so egress is allowlisted (srd009 R1.4)", client.LimitsRef)
	}
}

func TestCohereFailureSignalsMustBeDistinct(t *testing.T) {
	same := restOperationDeclaration{
		Failures: []struct {
			Status []int  `yaml:"status"`
			Signal string `yaml:"signal"`
		}{
			{Status: []int{401}, Signal: "ProviderFailed"},
			{Status: []int{429}, Signal: "ProviderFailed"},
		},
	}
	if err := assertCohereFailureSignalsDistinct(same); err == nil {
		t.Error("one signal for every status passed; an operator could not tell a rejected credential from a rate limit (srd009 R1.6)")
	}
	none := restOperationDeclaration{}
	if err := assertCohereFailureSignalsDistinct(none); err == nil {
		t.Error("an operation mapping no failure status passed")
	}
}

func TestCohereCredentialByReferenceOnlyRejectsALiteral(t *testing.T) {
	root := t.TempDir()
	chatbot := filepath.Join(root, "agents", "chatbot")
	if err := os.MkdirAll(chatbot, 0o755); err != nil {
		t.Fatal(err)
	}
	leaked := "auth:\n  cohere_bearer:\n    " + cohereCredentialEnv + ": co-live-abcdef0123456789\n"
	if err := os.WriteFile(filepath.Join(chatbot, "rest.yaml"), []byte(leaked), 0o644); err != nil {
		t.Fatal(err)
	}
	err := assertCohereCredentialByReferenceOnly(root)
	if err == nil {
		t.Fatal("a credential written into a profile passed the check (srd009 R1.3)")
	}
	if !strings.Contains(err.Error(), "R1.3") {
		t.Errorf("failure %q does not cite the requirement it enforces", err)
	}
}

func TestCohereCredentialByReferenceOnlyAllowsReferencesAndScenarioValues(t *testing.T) {
	root := t.TempDir()
	chatbot := filepath.Join(root, "agents", "chatbot")
	scenario := filepath.Join(chatbot, "tests", "cohere-command")
	if err := os.MkdirAll(scenario, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chatbot, "rest.yaml"),
		[]byte("auth:\n  cohere_bearer:\n    token_ref: "+cohereCredentialEnv+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scenario, "scenario.yaml"),
		[]byte("env:\n  - "+cohereCredentialEnv+"=scenario-not-a-real-key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := assertCohereCredentialByReferenceOnly(root); err != nil {
		t.Fatalf("a token_ref and a scenario literal were rejected: %v", err)
	}
}

// The shipped tree is the case that matters: this is the check standing between
// a debugging paste and a published credential.
func TestCohereCredentialAbsentFromShippedProfiles(t *testing.T) {
	if err := assertCohereCredentialByReferenceOnly(".."); err != nil {
		t.Fatal(err)
	}
}

func TestCohereDispatchOutcomeReadsTheSignal(t *testing.T) {
	spans := []cohereSpanDetail{
		commandSpan("parse_tier", "ToolDone"),
		commandSpan("invoke_cohere_command", "BriefComposed"),
	}
	attempted, signal := cohereDispatchOutcome(spans)
	if !attempted || signal != "BriefComposed" {
		t.Errorf("attempted=%v signal=%q, want true and BriefComposed", attempted, signal)
	}

	// A turn answered by an Ollama word must not read as a Cohere dispatch, or
	// the live assertion would pass on exactly the run it exists to catch.
	fallbackOnly := []cohereSpanDetail{
		commandSpan("parse_tier", "ParseFailed"),
		commandSpan("invoke_llm_fast", "LLMResponded"),
	}
	if attempted, _ := cohereDispatchOutcome(fallbackOnly); attempted {
		t.Error("a turn with no invoke_cohere_command span reported a Cohere dispatch")
	}
}

func TestCohereCredentialAbsentFromSpansCatchesALeak(t *testing.T) {
	credential := "co-live-abcdef0123456789"
	clean := []cohereSpanDetail{commandSpan("invoke_cohere_command", "BriefComposed")}
	if err := assertCohereCredentialAbsentFromSpans(clean, credential); err != nil {
		t.Errorf("clean spans were rejected: %v", err)
	}
	leaked := []cohereSpanDetail{{
		Name: "execute_tool invoke_cohere_command",
		Attributes: []chromaTraceKV{
			kvAttr("command.name", "invoke_cohere_command"),
			kvAttr("http.request.header.authorization", "Bearer "+credential),
		},
	}}
	if err := assertCohereCredentialAbsentFromSpans(leaked, credential); err == nil {
		t.Error("a credential exported in a span attribute passed (srd009 R1.3)")
	}
	// An empty credential means the live tier never ran; scanning for it would
	// match every span and fail every clean run.
	if err := assertCohereCredentialAbsentFromSpans(leaked, ""); err != nil {
		t.Errorf("an empty credential produced a failure: %v", err)
	}
}

func TestFirstRunesBoundsAnExcerpt(t *testing.T) {
	if got := firstRunes("  short  ", 40); got != "short" {
		t.Errorf("firstRunes trimmed to %q, want %q", got, "short")
	}
	long := strings.Repeat("a", 60)
	got := firstRunes(long, 40)
	if len([]rune(got)) != 43 || !strings.HasSuffix(got, "...") {
		t.Errorf("firstRunes(%d runes) = %d runes %q, want 40 plus an ellipsis", len(long), len([]rune(got)), got)
	}
}

func TestCohereRefusalMessageReadsAnEventReason(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace.json")
	span := `{"Name":"execute_tool invoke_cohere_command",
	  "Attributes":[{"Key":"command.name","Value":{"Value":"invoke_cohere_command"}}],
	  "Status":{"Code":"Error","Description":""},
	  "Events":[{"Name":"command_failed","Attributes":[{"Key":"reason","Value":{"Value":"host \"cohere.invalid\" is not allowed"}}]}]}`
	if err := os.WriteFile(trace, []byte(span), 0o644); err != nil {
		t.Fatal(err)
	}
	message, dispatched, err := cohereRefusalMessage(trace)
	if err != nil {
		t.Fatal(err)
	}
	if !dispatched {
		t.Fatal("the invoke_cohere_command span was not recognised as a dispatch")
	}
	if !strings.Contains(message, "not allowed") {
		t.Errorf("refusal message %q does not carry the policy text", message)
	}
}

func TestCohereRefusalMessageReportsNoDispatch(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace.json")
	span := `{"Name":"execute_tool invoke_llm_fast",
	  "Attributes":[{"Key":"command.name","Value":{"Value":"invoke_llm_fast"}}]}`
	if err := os.WriteFile(trace, []byte(span), 0o644); err != nil {
		t.Fatal(err)
	}
	_, dispatched, err := cohereRefusalMessage(trace)
	if err != nil {
		t.Fatal(err)
	}
	if dispatched {
		t.Error("a trace with no invoke_cohere_command span reported a dispatch")
	}
}

func commandSpan(name, signal string) cohereSpanDetail {
	return cohereSpanDetail{
		Name: "execute_tool " + name,
		Attributes: []chromaTraceKV{
			kvAttr("command.name", name),
			kvAttr("command.signal", signal),
		},
	}
}

func kvAttr(key, value string) chromaTraceKV {
	var attribute chromaTraceKV
	attribute.Key = key
	attribute.Value.Value = value
	return attribute
}

// A trace whose spans a reader cannot see is indistinguishable from a turn that
// never ran the word, so the reader that decides this must be exercised against
// the shape the exporter actually writes: pretty-printed, not one span per line.
func TestCohereSpansReadsPrettyPrintedTraces(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace.json")
	pretty := `{
  "Name": "execute_tool invoke_cohere_command",
  "Attributes": [
    {
      "Key": "command.name",
      "Value": { "Value": "invoke_cohere_command" }
    },
    {
      "Key": "command.signal",
      "Value": { "Value": "BriefComposed" }
    }
  ]
}
{
  "Name": "execute_tool compose_response",
  "Attributes": [
    { "Key": "command.name", "Value": { "Value": "compose_response" } }
  ]
}`
	if err := os.WriteFile(trace, []byte(pretty), 0o644); err != nil {
		t.Fatal(err)
	}
	spans, err := cohereSpans(trace)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 2 {
		t.Fatalf("decoded %d spans from a pretty-printed trace, want 2", len(spans))
	}
	attempted, signal := cohereDispatchOutcome(spans)
	if !attempted || signal != "BriefComposed" {
		t.Errorf("attempted=%v signal=%q, want true and BriefComposed", attempted, signal)
	}
}

func TestCohereSpansReportsAnEmptyTrace(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(trace, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cohereSpans(trace); err == nil {
		t.Error("an empty trace decoded without error; every assertion would then read as a missing word")
	}
}

// A rejected credential refuses every operation on the client, and since rel10.3
// the embed is the first one a turn reaches. Reading the embed's signal is what
// separates a refused credential from a malformed request or an outage; reading
// invoke_cohere_command's would find no span at all, because composition is never
// reached (GH-127).
func TestCohereEmbedOutcomeReportsTheRefusalSignal(t *testing.T) {
	refused := []cohereSpanDetail{
		commandSpan(cohereEmbedWord, "ProviderUnauthorized"),
	}
	attempted, signal := cohereEmbedOutcome(refused)
	if !attempted {
		t.Fatalf("a trace carrying a %s span reported no embed attempt", cohereEmbedWord)
	}
	if signal != "ProviderUnauthorized" {
		t.Errorf("embed signal = %q, want ProviderUnauthorized", signal)
	}

	// A turn that never reached the provider must not read as a refusal. The
	// two are the same empty answer and opposite findings.
	none := []cohereSpanDetail{commandSpan("invoke_llm_fast", "LLMResponded")}
	if attempted, _ := cohereEmbedOutcome(none); attempted {
		t.Error("a turn with no embed span reported an embed attempt")
	}
}

// The embed and the composition are read from different spans, and a scenario
// that confused them would assert against a word that never ran.
func TestCohereEmbedAndDispatchOutcomesReadDifferentWords(t *testing.T) {
	spans := []cohereSpanDetail{
		commandSpan(cohereEmbedWord, "QueryEmbedded"),
		commandSpan("invoke_cohere_command", "BriefComposed"),
	}
	if _, embed := cohereEmbedOutcome(spans); embed != "QueryEmbedded" {
		t.Errorf("embed outcome = %q, want QueryEmbedded", embed)
	}
	if _, composed := cohereDispatchOutcome(spans); composed != "BriefComposed" {
		t.Errorf("dispatch outcome = %q, want BriefComposed", composed)
	}
}

// The revision leg's verdict rests on the machine path, because a revision
// that never joined the carried records reads just as fluently (GH-604).
func TestRevisionJoinVerdictHoldsTheReGroundingPath(t *testing.T) {
	joined := []cohereSpanDetail{
		commandSpan("check_prior_sources", "PriorSourcesAbsent"),
		commandSpan("check_prior_sources", "PriorSourcesPresent"),
		commandSpan("read_documents", "DocumentsRead"),
		commandSpan("read_documents", "ClearanceRefused"),
		commandSpan("select_new_prior_ids", "NewPriorIdsFound"),
		commandSpan("select_new_prior_rows", "NewPriorRowsSelected"),
		commandSpan("parse_chunk_union", "ChunkUnionParsed"),
	}
	if err := assertRevisionJoinedCarriedRows(joined); err != nil {
		t.Fatalf("a joined revision was rejected: %v", err)
	}
	for name, spans := range map[string][]cohereSpanDetail{
		"no cited ids":       {commandSpan("check_prior_sources", "PriorSourcesAbsent")},
		"no unit read":       {commandSpan("check_prior_sources", "PriorSourcesPresent"), commandSpan("read_documents", "ClearanceRefused")},
		"already retrieved":  append(joined[:3:3], commandSpan("select_new_prior_ids", "PriorIdsAlreadyRetrieved")),
		"union never parsed": joined[:6:6],
	} {
		if err := assertRevisionJoinedCarriedRows(spans); err == nil {
			t.Errorf("%s: a revision that joined nothing passed", name)
		}
	}
}

func TestCorpusCitedRecordIDsSkipsExternalAndRepeats(t *testing.T) {
	var resp chatResponse
	resp.Metadata.Citations = chatCitations{
		{RecordID: "chroma-corpus-agents.md"},
		{RecordID: "https://example.org/a"},
		{RecordID: "chroma-corpus-agents.md"},
		{RecordID: "osint-1"},
		{RecordID: " spec-driven-development.md "},
		{RecordID: ""},
	}
	got := corpusCitedRecordIDs(resp)
	want := []string{"chroma-corpus-agents.md", "spec-driven-development.md"}
	if !slices.Equal(got, want) {
		t.Fatalf("cited ids = %v, want %v", got, want)
	}
}

// The agentic search's verdict rests on the trace too: the tool call, the
// validated proposal, and at least one self search answered (GH-611).
func TestRevisionSearchVerdictCountsTheProposalPasses(t *testing.T) {
	searched := []cohereSpanDetail{
		commandSpan("check_revision_search", "RevisionSearchSkipped"),
		commandSpan("check_revision_search", "RevisionSearchRequested"),
		commandSpan("propose_searches_via_tool", "LLMResponded"),
		commandSpan("parse_proposed_searches", "ProposedSearchesParsed"),
		commandSpan("check_proposal_1", "ProposalPresent"),
		commandSpan("search_proposed_query", "ProposalSearched"),
		commandSpan("search_proposed_query", "ProposalSearched"),
		commandSpan("search_proposed_query", "CommandError"),
	}
	passes, err := assertRevisionSearchedItsProposals(searched)
	if err != nil || passes != 2 {
		t.Fatalf("passes = %d, err = %v; want 2 answered searches for three dispatches", passes, err)
	}
	for name, spans := range map[string][]cohereSpanDetail{
		"not a revision":     searched[:1],
		"proposal failed":    append(searched[:2:2], commandSpan("propose_searches_via_tool", "ProviderUnavailable")),
		"nothing proposed":   append(searched[:4:4], commandSpan("check_proposal_1", "ProposalAbsent")),
		"no search answered": append(searched[:5:5], commandSpan("search_proposed_query", "CommandError")),
	} {
		if _, err := assertRevisionSearchedItsProposals(spans); err == nil {
			t.Errorf("%s: a revision that searched nothing passed", name)
		}
	}
}
