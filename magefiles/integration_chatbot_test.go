// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCitedRecordNumbersBracketed(t *testing.T) {
	answer := "The project has a capacity of 88 megawatts [record 1], produced by 22 turbines [record 3]."
	got := citedRecordNumbers(answer)
	want := []int{1, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("citedRecordNumbers = %v, want %v", got, want)
	}
}

func TestCitedRecordNumbersDedupesAndSorts(t *testing.T) {
	answer := "See record 2 and Record #2, and also RECORD 1."
	got := citedRecordNumbers(answer)
	want := []int{1, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("citedRecordNumbers = %v, want %v", got, want)
	}
}

func TestCitedRecordNumbersUngrounded(t *testing.T) {
	answer := "The retrieved chunks do not contain the answer, so I cannot reference them."
	if got := citedRecordNumbers(answer); len(got) != 0 {
		t.Fatalf("citedRecordNumbers on ungrounded answer = %v, want empty", got)
	}
}

func TestChatResponseDecodesTrace(t *testing.T) {
	var resp chatResponse
	if err := json.Unmarshal([]byte(`{"answer":"grounded [record 1]","trace":{"status":"succeeded","terminal_signal":"LLMResponded"}}`), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Answer == "" {
		t.Fatalf("answer is empty")
	}
	if resp.Trace.Status != "succeeded" {
		t.Fatalf("trace.status = %q, want succeeded", resp.Trace.Status)
	}
	if got := citedRecordNumbers(resp.Answer); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("citedRecordNumbers = %v, want [1]", got)
	}
}

func TestChatbotFanOutFixturePinsInventoryVocabulary(t *testing.T) {
	const wantQuestion = "List every named project or system described across the knowledge base. Return one line for each of these three entries: the corpus-agent system, the development methodology, and the photovoltaic energy project. Use the exact name from the retrieved chunks."
	if chatbotFanOutQuestion != wantQuestion {
		t.Fatalf("fan-out question = %q, want %q", chatbotFanOutQuestion, wantQuestion)
	}
	wantDocs := []struct{ id, text string }{
		{"solar-1", "Knowledge-base project and system inventory answer: the exact name of the photovoltaic energy project is Solar Ridge. Return the proper name Solar Ridge when listing every named project or system. It has a capacity of 55 megawatts across 140000 panels on a decommissioned quarry."},
		{"solar-2", "Knowledge-base system inventory entry: the exact project name Solar Ridge identifies the photovoltaic system that feeds a 33 kilovolt substation and includes a 12 megawatt-hour battery for evening dispatch."},
	}
	if !reflect.DeepEqual(disjointCorpus2Docs, wantDocs) {
		t.Fatalf("rag1 fan-out fixture = %#v, want %#v", disjointCorpus2Docs, wantDocs)
	}
}

func TestAssertChatbotFanOutResponseRequiresDualRAGEvidence(t *testing.T) {
	valid := decodeChatResponse(t, `{
		"answer":"Chroma Corpus Agents and Solar Ridge are described.",
		"trace":{"status":"succeeded"},
		"metadata":{
			"query_counts":{"succeeded":2,"failed":0},
			"sources":{
				"not_selected":[],
				"composed":[
					{"name":"rag0","signal":"QueryResponded","documents":[["Chroma Corpus Agents use a vector database."]]},
					{"name":"rag1","signal":"QueryResponded","documents":[["Knowledge-base project inventory entry: Solar Ridge."]]}
				],
				"embedding_model_excluded":[],
				"query_failed":[]
			}
		}
	}`)
	if err := assertChatbotFanOutResponse(valid, 200); err != nil {
		t.Fatalf("valid dual-RAG fan-out evidence failed: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*chatResponse)
		wantErr string
	}{
		{
			name: "free-form answer must use rag1",
			mutate: func(resp *chatResponse) {
				resp.Answer = "Only Chroma Corpus Agents are described."
			},
			wantErr: "answer omits",
		},
		{
			name: "rag0 must compose retrieval evidence",
			mutate: func(resp *chatResponse) {
				resp.Metadata.Sources.Composed = resp.Metadata.Sources.Composed[1:]
			},
			wantErr: "rag0 evidence",
		},
		{
			name: "rag1 retrieval evidence must contain Solar Ridge",
			mutate: func(resp *chatResponse) {
				resp.Metadata.Sources.Composed[1].Documents = [][]string{{"unrelated rag1 text"}}
			},
			wantErr: "rag1 evidence",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := decodeChatResponse(t, mustJSON(t, valid))
			tt.mutate(&resp)
			err := assertChatbotFanOutResponse(resp, 200)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestAssertChatbotDegradedResponsePinsRag1DownContrast(t *testing.T) {
	valid := decodeChatResponse(t, `{
		"answer":"Chroma Corpus Agents are described.",
		"trace":{"status":"succeeded"},
		"metadata":{
			"query_counts":{"succeeded":1,"failed":1},
			"sources":{
				"not_selected":[],
				"composed":[
					{"name":"rag0","signal":"QueryResponded","documents":[["Chroma Corpus Agents use a vector database."]]}
				],
				"embedding_model_excluded":[],
				"query_failed":[
					{"name":"rag1","signal":"CommandError"}
				]
			}
		}
	}`)
	if err := assertChatbotDegradedResponse(valid, 200); err != nil {
		t.Fatalf("valid rag1-down contrast failed: %v", err)
	}

	valid.Metadata.Sources.QueryFailed = nil
	if err := assertChatbotDegradedResponse(valid, 200); err == nil ||
		!strings.Contains(err.Error(), "want rag1 CommandError") {
		t.Fatalf("missing rag1 failure evidence error = %v", err)
	}
}

func decodeChatResponse(t *testing.T, raw string) chatResponse {
	t.Helper()
	var resp chatResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("decode chat response fixture: %v", err)
	}
	return resp
}

func mustJSON(t *testing.T, value interface{}) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode chat response fixture: %v", err)
	}
	return string(data)
}

func TestAssertChatbotTierSelectionTraceAttributesAnswerDispatch(t *testing.T) {
	trace := writeChromaTrace(t, []string{
		chatbotSpanLine("machine_request chatbot/chat", "turn-fast", "", nil),
		chatbotSpanLine("chat qwen-fast", "source-selector", "turn-fast", map[string]string{"gen_ai.request.model": "qwen-fast"}),
		chatbotSpanLine("chat qwen-fast", "tier-selector", "turn-fast", map[string]string{"gen_ai.request.model": "qwen-fast"}),
		chatbotSpanLine("execute_tool parse_tier", "fast-parse", "turn-fast", map[string]string{"command.name": "parse_tier", "command.signal": "ToolDone"}),
		chatbotSpanLine("chat qwen-fast", "late-tier-selector", "turn-fast", map[string]string{"command.name": "select_tier", "gen_ai.request.model": "qwen-fast"}),
		chatbotSpanLine("chat qwen-fast", "fast-answer", "turn-fast", map[string]string{"command.name": "invoke_llm_fast", "gen_ai.request.model": "qwen-fast"}),
		chatbotSpanLine("execute_tool compose_response", "fast-compose", "turn-fast", map[string]string{"command.name": "compose_response"}),
		chatbotSpanLine("machine_request chatbot/chat", "turn-deep", "", nil),
		chatbotSpanLine("chat qwen-fast", "deep-selector", "turn-deep", map[string]string{"gen_ai.request.model": "qwen-fast"}),
		chatbotSpanLine("execute_tool parse_tier", "deep-parse", "turn-deep", map[string]string{"command.name": "parse_tier", "command.signal": "ToolDone"}),
		chatbotSpanLine("chat ornith-deep", "deep-answer", "turn-deep", map[string]string{"command.name": "invoke_llm_deep", "gen_ai.request.model": "ornith-deep"}),
		chatbotSpanLine("execute_tool compose_response", "deep-compose", "turn-deep", map[string]string{"command.name": "compose_response"}),
	})
	if err := assertChatbotTierSelectionTrace(trace, "qwen-fast", "ornith-deep"); err != nil {
		t.Fatalf("expected attributed fast and deep answer dispatches to pass: %v", err)
	}
}

func TestAssertChatbotTierSelectionTraceDoesNotRequireBothTiers(t *testing.T) {
	trace := writeChromaTrace(t, []string{
		chatbotSpanLine("machine_request chatbot/chat", "turn-1", "", nil),
		chatbotSpanLine("execute_tool parse_tier", "parse-1", "turn-1", map[string]string{"command.name": "parse_tier", "command.signal": "ToolDone"}),
		chatbotSpanLine("chat qwen-fast", "answer-1", "turn-1", map[string]string{"command.name": "invoke_llm_fast", "gen_ai.request.model": "qwen-fast"}),
		chatbotSpanLine("execute_tool compose_response", "compose-1", "turn-1", map[string]string{"command.name": "compose_response"}),
		chatbotSpanLine("machine_request chatbot/chat", "turn-2", "", nil),
		chatbotSpanLine("execute_tool parse_tier", "parse-2", "turn-2", map[string]string{"command.name": "parse_tier", "command.signal": "ToolDone"}),
		chatbotSpanLine("chat qwen-fast", "answer-2", "turn-2", map[string]string{"command.name": "invoke_llm_fast", "gen_ai.request.model": "qwen-fast"}),
		chatbotSpanLine("execute_tool compose_response", "compose-2", "turn-2", map[string]string{"command.name": "compose_response"}),
	})
	if err := assertChatbotTierSelectionTrace(trace, "qwen-fast", "ornith-deep"); err != nil {
		t.Fatalf("two model-dependent fast selections should pass: %v", err)
	}
}

func TestAssertChatbotTierSelectionTraceRejectsInvalidAttribution(t *testing.T) {
	tests := []struct {
		name    string
		lines   []string
		wantErr string
	}{
		{
			name: "selector-only Qwen is not an answer dispatch",
			lines: []string{
				chatbotSpanLine("machine_request chatbot/chat", "turn", "", nil),
				chatbotSpanLine("chat qwen-fast", "selector", "turn", map[string]string{"gen_ai.request.model": "qwen-fast"}),
				chatbotSpanLine("execute_tool parse_tier", "parse", "turn", map[string]string{"command.name": "parse_tier", "command.signal": "ToolDone"}),
				chatbotSpanLine("execute_tool compose_response", "compose", "turn", map[string]string{"command.name": "compose_response"}),
			},
			wantErr: "0 answer-word dispatch spans",
		},
		{
			name: "undeclared answer model",
			lines: []string{
				chatbotSpanLine("machine_request chatbot/chat", "turn", "", nil),
				chatbotSpanLine("execute_tool parse_tier", "parse", "turn", map[string]string{"command.name": "parse_tier", "command.signal": "ToolDone"}),
				chatbotSpanLine("chat other", "answer", "turn", map[string]string{"command.name": "invoke_llm_fast", "gen_ai.request.model": "other"}),
				chatbotSpanLine("execute_tool compose_response", "compose", "turn", map[string]string{"command.name": "compose_response"}),
			},
			wantErr: "undeclared model",
		},
		{
			name: "two answer words",
			lines: []string{
				chatbotSpanLine("machine_request chatbot/chat", "turn", "", nil),
				chatbotSpanLine("execute_tool parse_tier", "parse", "turn", map[string]string{"command.name": "parse_tier", "command.signal": "ToolDone"}),
				chatbotSpanLine("chat qwen-fast", "fast", "turn", map[string]string{"command.name": "invoke_llm_fast", "gen_ai.request.model": "qwen-fast"}),
				chatbotSpanLine("chat ornith-deep", "deep", "turn", map[string]string{"command.name": "invoke_llm_deep", "gen_ai.request.model": "ornith-deep"}),
				chatbotSpanLine("execute_tool compose_response", "compose", "turn", map[string]string{"command.name": "compose_response"}),
			},
			wantErr: "2 answer-word dispatch spans",
		},
		{
			name: "answer dispatch without model",
			lines: []string{
				chatbotSpanLine("machine_request chatbot/chat", "turn", "", nil),
				chatbotSpanLine("execute_tool parse_tier", "parse", "turn", map[string]string{"command.name": "parse_tier", "command.signal": "ToolDone"}),
				chatbotSpanLine("chat unknown", "answer", "turn", map[string]string{"command.name": "invoke_llm_fast"}),
				chatbotSpanLine("execute_tool compose_response", "compose", "turn", map[string]string{"command.name": "compose_response"}),
			},
			wantErr: "has no gen_ai.request.model",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := assertChatbotTierSelectionTrace(writeChromaTrace(t, tt.lines), "qwen-fast", "ornith-deep")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func chatbotSpanLine(name, spanID, parentID string, attrs map[string]string) string {
	attributes := make([]map[string]interface{}, 0, len(attrs))
	for key, value := range attrs {
		attributes = append(attributes, map[string]interface{}{
			"Key": key, "Value": map[string]interface{}{"Type": "STRING", "Value": value},
		})
	}
	line, err := json.Marshal(map[string]interface{}{
		"Name":        name,
		"SpanContext": map[string]string{"TraceID": "trace", "SpanID": spanID},
		"Parent":      map[string]string{"TraceID": "trace", "SpanID": parentID},
		"Attributes":  attributes,
	})
	if err != nil {
		panic(err)
	}
	return string(line)
}

func TestChatbotRequiredModelsResolveShippedDefaults(t *testing.T) {
	for _, name := range []string{
		"CORPUS_EMBEDDING_MODEL",
		"CHATBOT_EMBEDDING_MODEL",
		"CHATBOT_TIER_MODEL",
		"CHATBOT_FAST_MODEL",
		"CHATBOT_DEEP_MODEL",
	} {
		unsetTestEnv(t, name)
	}

	got, err := chatbotRequiredModels(filepath.Dir(findChartDir(t)))
	if err != nil {
		t.Fatalf("chatbotRequiredModels: %v", err)
	}
	want := []string{"ornith:9b", "qwen2.5:3b", "qwen3-embedding:8b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("required models = %v, want shipped defaults %v", got, want)
	}
}

func TestChatbotRequiredModelsUseDeploymentEnvironment(t *testing.T) {
	t.Setenv("CORPUS_EMBEDDING_MODEL", "corpus-embed")
	t.Setenv("CHATBOT_EMBEDDING_MODEL", "chatbot-embed")
	t.Setenv("CHATBOT_TIER_MODEL", "chatbot-tier")
	t.Setenv("CHATBOT_FAST_MODEL", "chatbot-fast")
	t.Setenv("CHATBOT_DEEP_MODEL", "chatbot-deep")

	got, err := chatbotRequiredModels(filepath.Dir(findChartDir(t)))
	if err != nil {
		t.Fatalf("chatbotRequiredModels: %v", err)
	}
	// chatbot-tier stays: select_tier remains an Ollama word for the Ollama
	// machine variant. What left the inventory is select_sources, which runs
	// at the Cohere provider (GH-425); the Cohere machine's tier is a composed
	// policy word that calls no model at all.
	want := []string{
		"chatbot-deep", "chatbot-embed", "chatbot-fast", "chatbot-tier",
		"corpus-embed", "qwen2.5:3b",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("required models = %v, want environment-selected %v", got, want)
	}
}

func TestResolveModelReferenceLeavesLiteralName(t *testing.T) {
	const model = "qwen2.5:3b"
	if got := resolveModelReference(model); got != model {
		t.Fatalf("resolveModelReference(%q) = %q", model, got)
	}
}

func unsetTestEnv(t *testing.T, name string) {
	t.Helper()
	value, set := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unset %s: %v", name, err)
	}
	t.Cleanup(func() {
		if set {
			_ = os.Setenv(name, value)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}

// A fallback turn and a dispatched turn are nearly identical in the span log:
// both run one declared answer word with a configured model, because the
// fallback word is itself a declared answer word. That is why the old assertion
// passed throughout the period the $tool selector never dispatched (GH-84).
// These build both shapes and require the assertion to tell them apart.

const (
	tierTestFastModel = "qwen2.5:3b"
	tierTestDeepModel = "ornith:9b"
)

func tierSpan(name, spanID, parentID string, attrs map[string]string) string {
	var pairs []string
	for key, value := range attrs {
		pairs = append(pairs, fmt.Sprintf(
			`{"Key":%q,"Value":{"Value":%q}}`, key, value))
	}
	return fmt.Sprintf(
		`{"Name":%q,"StartTime":"2026-08-25T12:00:00Z","SpanContext":{"TraceID":"t","SpanID":%q},`+
			`"Parent":{"TraceID":"t","SpanID":%q},"Attributes":[%s]}`,
		name, spanID, parentID, strings.Join(pairs, ","))
}

// chatbotTurnTrace builds one turn: the machine_request span, a parse_tier span
// carrying parseSignal, the answer word's chat span, and compose_response.
func chatbotTurnTrace(parseSignal, answerModel string) string {
	spans := []string{
		tierSpan("machine_request chat", "turn", "", map[string]string{"command.name": "machine_request"}),
		tierSpan("parse_response parse_tier", "parse", "turn", map[string]string{
			"command.name": "parse_tier", "command.signal": parseSignal,
		}),
		tierSpan("chat "+answerModel, "answer", "turn", map[string]string{
			"command.name": "invoke_llm_fast", "command.signal": "LLMResponded",
			"gen_ai.request.model": answerModel,
		}),
		tierSpan("execute_tool compose_response", "compose", "turn", map[string]string{
			"command.name": "compose_response", "command.signal": "Composed",
		}),
	}
	return strings.Join(spans, "\n") + "\n"
}

func TestChatbotTierSelectionAcceptsADispatchedTurn(t *testing.T) {
	trace := writeTrace(t, chatbotTurnTrace("ToolDone", tierTestFastModel))
	if err := assertChatbotTierSelectionTrace(trace, tierTestFastModel, tierTestDeepModel); err != nil {
		t.Fatalf("a dispatched turn was rejected: %v", err)
	}
}

// The defect this issue records. Everything the old assertion checked is still
// true here: one declared answer word, its configured model, one parse_tier and
// one compose_response. Only parse_tier's signal says the selection never
// resolved.
func TestChatbotTierSelectionRejectsAFallbackTurn(t *testing.T) {
	trace := writeTrace(t, chatbotTurnTrace("ParseFailed", tierTestFastModel))
	err := assertChatbotTierSelectionTrace(trace, tierTestFastModel, tierTestDeepModel)
	if err == nil {
		t.Fatal("a turn that answered on the fallback was accepted")
	}
	for _, want := range []string{"answered on the fallback", "ParseFailed", "ToolDone"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

// TaskCompleted is parse_tier's other non-dispatch outcome, and it reaches the
// same fallback transition.
func TestChatbotTierSelectionRejectsATaskCompletedTurn(t *testing.T) {
	trace := writeTrace(t, chatbotTurnTrace("TaskCompleted", tierTestFastModel))
	if err := assertChatbotTierSelectionTrace(trace, tierTestFastModel, tierTestDeepModel); err == nil {
		t.Fatal("a turn whose selection resolved to TaskCompleted was accepted")
	}
}

// A span log with no command.signal at all -- an older trace, or a shape change
// upstream -- must fail rather than read as dispatched.
func TestChatbotTierSelectionRejectsATurnWithNoSignalRecorded(t *testing.T) {
	spans := []string{
		tierSpan("machine_request chat", "turn", "", map[string]string{"command.name": "machine_request"}),
		tierSpan("parse_response parse_tier", "parse", "turn", map[string]string{"command.name": "parse_tier"}),
		tierSpan("chat "+tierTestFastModel, "answer", "turn", map[string]string{
			"command.name": "invoke_llm_fast", "gen_ai.request.model": tierTestFastModel,
		}),
		tierSpan("execute_tool compose_response", "compose", "turn", map[string]string{"command.name": "compose_response"}),
	}
	trace := writeTrace(t, strings.Join(spans, "\n")+"\n")
	if err := assertChatbotTierSelectionTrace(trace, tierTestFastModel, tierTestDeepModel); err == nil {
		t.Fatal("a turn with no recorded parse_tier signal was accepted as dispatched")
	}
}

// The deep tier is the case the fallback masked in practice: the selector routed
// to invoke_llm_deep and the fast model answered.
func TestChatbotTierSelectionAcceptsADispatchedDeepTurn(t *testing.T) {
	trace := writeTrace(t, chatbotTurnTrace("ToolDone", tierTestDeepModel))
	if err := assertChatbotTierSelectionTrace(trace, tierTestFastModel, tierTestDeepModel); err != nil {
		t.Fatalf("a dispatched deep turn was rejected: %v", err)
	}
}

// cohereTurnTrace builds a turn on the Cohere machine: parse_tier dispatches
// Command's compose, then any fallback words the machine ran after it.
func cohereTurnTrace(composeSignal string, fallbackModels ...string) string {
	spans := []string{
		tierSpan("machine_request chat", "turn", "", map[string]string{"command.name": "machine_request"}),
		tierSpan("parse_response parse_tier", "parse", "turn", map[string]string{
			"command.name": "parse_tier", "command.signal": "ToolDone",
		}),
		tierSpan("execute_tool invoke_cohere_command", "compose-cohere", "turn", map[string]string{
			"command.name": "invoke_cohere_command", "command.signal": composeSignal,
		}),
	}
	for i, model := range fallbackModels {
		spans = append(spans, tierSpan("chat "+model, fmt.Sprintf("fallback-%d", i), "turn", map[string]string{
			"command.name": "invoke_llm_fast", "command.signal": "LLMResponded",
			"gen_ai.request.model": model,
		}))
	}
	spans = append(spans, tierSpan("execute_tool compose_response", "compose", "turn", map[string]string{
		"command.name": "compose_response", "command.signal": "Composed",
	}))
	return strings.Join(spans, "\n") + "\n"
}

// On the Cohere machine every grounded turn composes with Command
// (declare_cohere_tier_choice), so that is the one answer word a healthy turn
// dispatches, with no "chat <model>" span of its own.
func TestChatbotTierSelectionAcceptsACohereComposedTurn(t *testing.T) {
	trace := writeTrace(t, cohereTurnTrace("BriefComposed"))
	if err := assertChatbotTierSelectionTrace(trace, tierTestFastModel, tierTestDeepModel); err != nil {
		t.Fatalf("a Command-composed turn was rejected: %v", err)
	}
}

// A rejected compose degrades to the fast word (srd009 R3.4). The turn answers,
// but on the fallback, which is what the target exists to tell apart.
func TestChatbotTierSelectionRejectsACohereComposeThatFellBack(t *testing.T) {
	for name, trace := range map[string]string{
		"rejected then fast": cohereTurnTrace("ProviderRejected", tierTestFastModel),
		"rejected alone":     cohereTurnTrace("ProviderRejected"),
	} {
		if err := assertChatbotTierSelectionTrace(writeTrace(t, trace), tierTestFastModel, tierTestDeepModel); err == nil {
			t.Errorf("%s: a Command compose that did not compose was accepted", name)
		}
	}
}

// The rag-server names its collection once per endpoint. A rag1 variant that
// rewrote only the first left the query endpoint on rag0's corpus, so both
// sources returned the same records (GH-760).
func TestRag1VariantServesTheDisjointCollectionOnEveryEndpoint(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(root, "agents", "rag-server", "rest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	endpoints := strings.Count(string(source), "name: ${RAG_COLLECTION:-corpus}\n")
	if endpoints == 0 {
		t.Fatal("rag-server rest.yaml no longer names ${RAG_COLLECTION:-corpus}; update the rag1 variant")
	}
	profile, cleanup, err := generateRag1Variant(root)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	variant, err := os.ReadFile(filepath.Join(filepath.Dir(profile), "rest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(variant), "${RAG_COLLECTION:-corpus}") {
		t.Fatal("rag1 variant still serves the rag0 collection on some endpoint")
	}
	if got := strings.Count(string(variant), "name: ${RAG_COLLECTION:-"+chromaCorpus2+"}\n"); got != endpoints {
		t.Fatalf("rag1 variant names %s on %d endpoints, want %d", chromaCorpus2, got, endpoints)
	}
}
