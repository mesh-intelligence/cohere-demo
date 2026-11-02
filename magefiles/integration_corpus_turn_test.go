// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

const turnStoreIdentity = "embed-v4.0"

// composedSource builds one source outcome that reached composition carrying
// the given retrieved document.
func composedSource(name, document string) chatbotSourceOutcome {
	var outcome chatbotSourceOutcome
	outcome.Name = name
	outcome.Documents = [][]string{{document}}
	return outcome
}

// groundedTurn is the response a correct turn returns: both declared sources
// composed from retrieved documents, nothing excluded, nothing failed, and the
// query embedded at the identity the store recorded.
func groundedTurn() chatResponse {
	var resp chatResponse
	resp.Answer = "Air defence reported Shahed drones intercepted over Kyiv oblast."
	resp.Trace.Status = "succeeded"
	resp.Metadata.QueryEmbeddingModel = turnStoreIdentity
	resp.Metadata.Sources.Composed = []chatbotSourceOutcome{
		composedSource("rag0", "HUM-0114 reports transit activity."),
		composedSource("rag1", "SIG-3308 reports emitter activity."),
	}
	return resp
}

func TestCorpusTurnGroundedAcceptsACorrectTurn(t *testing.T) {
	if err := assertCorpusTurnGrounded(groundedTurn(), http.StatusOK, turnStoreIdentity); err != nil {
		t.Fatalf("a grounded turn was rejected: %v", err)
	}
}

// The case the tracer exists for. An excluded source still answers 200 with
// fluent prose, so every assertion about the answer passes and only the
// partition says the brief was composed from nothing.
func TestCorpusTurnGroundedRejectsAnExcludedSource(t *testing.T) {
	resp := groundedTurn()
	resp.Metadata.Sources.EmbeddingModelExcluded = []chatbotSourceOutcome{
		composedSource("rag1", ""),
	}
	err := assertCorpusTurnGrounded(resp, http.StatusOK, turnStoreIdentity)
	if err == nil {
		t.Fatal("a turn that excluded a source on embedding identity was accepted")
	}
	if !strings.Contains(err.Error(), "rag1") {
		t.Errorf("error %q does not name the excluded source", err)
	}
}

// The identity the store recorded and the identity the query was embedded at
// are one value. Two names mean retrieval compared vectors from two spaces,
// whatever the answer reads like.
func TestCorpusTurnGroundedRejectsADivergentQueryIdentity(t *testing.T) {
	resp := groundedTurn()
	resp.Metadata.QueryEmbeddingModel = "qwen3-embedding:8b"
	err := assertCorpusTurnGrounded(resp, http.StatusOK, turnStoreIdentity)
	if err == nil {
		t.Fatal("a turn embedded at another family was accepted")
	}
	for _, want := range []string{"qwen3-embedding:8b", turnStoreIdentity} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// A source listed as composed but carrying no retrieved document is the fluent
// brief composed from nothing, wearing the right partition.
func TestCorpusTurnGroundedRejectsComposedWithoutDocuments(t *testing.T) {
	resp := groundedTurn()
	resp.Metadata.Sources.Composed = []chatbotSourceOutcome{composedSource("rag0", "   ")}
	if err := assertCorpusTurnGrounded(resp, http.StatusOK, turnStoreIdentity); err == nil {
		t.Fatal("a source that composed no retrieved document was accepted")
	}
}

func TestCorpusTurnGroundedRejectsNoComposedSource(t *testing.T) {
	resp := groundedTurn()
	resp.Metadata.Sources.Composed = nil
	if err := assertCorpusTurnGrounded(resp, http.StatusOK, turnStoreIdentity); err == nil {
		t.Fatal("a turn where no source reached composition was accepted")
	}
}

// A failed query is a degraded turn, which is a different case than the tracer:
// every declared source serves the seeded collection here.
func TestCorpusTurnGroundedRejectsAFailedSource(t *testing.T) {
	resp := groundedTurn()
	resp.Metadata.Sources.QueryFailed = []chatbotSourceOutcome{composedSource("rag1", "")}
	err := assertCorpusTurnGrounded(resp, http.StatusOK, turnStoreIdentity)
	if err == nil {
		t.Fatal("a turn with a failed source was accepted")
	}
	if !strings.Contains(err.Error(), "rag1") {
		t.Errorf("error %q does not name the failed source", err)
	}
}

func TestCorpusTurnGroundedRejectsAnEmptyBrief(t *testing.T) {
	resp := groundedTurn()
	resp.Answer = "   "
	if err := assertCorpusTurnGrounded(resp, http.StatusOK, turnStoreIdentity); err == nil {
		t.Fatal("a turn answering 200 with an empty brief was accepted")
	}
}

func TestCorpusTurnGroundedRejectsANonOKStatus(t *testing.T) {
	if err := assertCorpusTurnGrounded(groundedTurn(), http.StatusInternalServerError,
		turnStoreIdentity); err == nil {
		t.Fatal("a turn that did not answer 200 was accepted")
	}
}

// One turn embeds once. A cycle that re-embedded the corpus at turn time is the
// path this release replaces, and it produces an identical brief.
func TestCorpusTurnEmbedRequestsWantsExactlyOne(t *testing.T) {
	if err := assertCorpusTurnEmbedRequests(corpusTurnExpectedEmbedRequests); err != nil {
		t.Fatalf("the expected count was rejected: %v", err)
	}
	for _, count := range []int{0, 2, 37} {
		if err := assertCorpusTurnEmbedRequests(count); err == nil {
			t.Errorf("a turn issuing %d embedding requests was accepted", count)
		}
	}
}

// The rerank and composition calls a turn makes travel to the same base URL.
// Counting those too would report a number that says nothing about how often
// the corpus was embedded.
func TestCorpusTurnEmbedProxyCountsOnlyTheEmbedOperation(t *testing.T) {
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	counting := &corpusTurnEmbedProxy{
		upstream: target,
		proxy:    httputil.NewSingleHostReverseProxy(target),
	}
	server := httptest.NewServer(counting)
	defer server.Close()

	for _, path := range []string{cohereEmbedPath, "/v2/rerank", "/v2/chat", cohereEmbedPath} {
		resp, err := http.Post(server.URL+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}

	if got := counting.embedCount(); got != 2 {
		t.Errorf("the proxy counted %d embed requests, want 2 of the 4 issued", got)
	}
	if len(seen) != 4 {
		t.Errorf("the upstream saw %d requests (%v), want all 4 forwarded", len(seen), seen)
	}
}

// The Ollama tier composes through the same template with no structured spans
// to render, so the field arrives as the empty string. Decoding the list shape
// alone rejected the whole response, which read as a broken turn when the turn
// had answered from retrieved documents.
func TestChatResponseDecodesEmptyRenderedCitations(t *testing.T) {
	const body = `{"answer":"a brief","metadata":{"citations":"","dropped_citations":"",` +
		`"query_embedding_model":"embed-v4.0"}}`
	var resp chatResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("an Ollama-tier response was rejected: %v", err)
	}
	if len(resp.Metadata.Citations) != 0 || len(resp.Metadata.DroppedCitations) != 0 {
		t.Errorf("the empty rendering decoded to %d citations and %d dropped, want none",
			len(resp.Metadata.Citations), len(resp.Metadata.DroppedCitations))
	}
}

// The Cohere path still decodes as a list, so the assertions that require real
// citations keep reading them.
func TestChatResponseDecodesCitationList(t *testing.T) {
	const body = `{"metadata":{"citations":[{"start":1,"end":4,"text":"abc",` +
		`"record_id":"OSI-2218-0001"}]}}`
	var resp chatResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Metadata.Citations) != 1 ||
		resp.Metadata.Citations[0].RecordID != "OSI-2218-0001" {
		t.Errorf("citations decoded to %+v, want the one span it carries", resp.Metadata.Citations)
	}
}

// A non-empty string is neither shape. Swallowing it would hide a composition
// template that started rendering something nobody reads.
func TestChatResponseRejectsUnexpectedCitationString(t *testing.T) {
	var resp chatResponse
	err := json.Unmarshal([]byte(`{"metadata":{"citations":"see below"}}`), &resp)
	if err == nil {
		t.Fatal("citations rendered as a non-empty string were accepted")
	}
	if !strings.Contains(err.Error(), "see below") {
		t.Errorf("error %q does not quote the unexpected rendering", err)
	}
}

// A collection seeded at a family the chatbot cannot embed a query at is not a
// degraded turn but an empty one, so the phase says so instead of reporting the
// empty answer as a defect.
func TestCorpusTurnSkipsANonCohereIdentity(t *testing.T) {
	reason := corpusTurnSkipReason("..",
		corpusStoreIdentity{Identity: "qwen3-embedding:8b", Provider: "ollama"}, "")
	if reason == "" {
		t.Fatal("a turn over an Ollama-seeded collection was not skipped")
	}
	if !strings.Contains(reason, "qwen3-embedding:8b") {
		t.Errorf("skip reason %q does not name the identity it cannot serve", reason)
	}
}

// The reason the caller already computed travels through unchanged, so the seed
// and the turn cannot disagree about why a provider is out of reach.
func TestCorpusTurnSkipsOnTheCallersEmbedReason(t *testing.T) {
	const upstream = "Ollama not reachable at http://127.0.0.1:11434/api/version"
	if reason := corpusTurnSkipReason("..",
		corpusStoreIdentity{Identity: turnStoreIdentity, Provider: "cohere"},
		upstream); reason != upstream {
		t.Errorf("skip reason is %q, want the caller's reason %q", reason, upstream)
	}
}
