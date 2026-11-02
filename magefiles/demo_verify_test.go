// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestDemoVerifyAcceptsComposedSourcesCarryingDocuments(t *testing.T) {
	body := `{"answer":"The Chroma corpus agents ingest and read.","metadata":{"sources":{` +
		`"composed":[{"name":"rag0","signal":"Answered","documents":[["chunk one","chunk two"]]},` +
		`{"name":"rag1","signal":"Answered","documents":[["chunk three"]]}],` +
		`"not_selected":[],"embedding_model_excluded":[],"query_failed":[]}}}`
	names, err := demoVerifyComposedSources([]byte(body), http.StatusOK)
	if err != nil {
		t.Fatalf("composed turn rejected: %v", err)
	}
	if strings.Join(names, ",") != "rag0 (2 document(s)),rag1 (1 document(s))" {
		t.Fatalf("composed source names = %v, want rag0 with 2 and rag1 with 1", names)
	}
}

// An unseeded cluster is the failure this target exists to catch, and it does
// not look like an error: the rag-server answers the query from the empty
// collection it bound at startup, so it composes with nothing to contribute.
func TestDemoVerifyRejectsComposedSourcesThatRetrievedNothing(t *testing.T) {
	body := `{"answer":"I do not know.","metadata":{"sources":{` +
		`"composed":[{"name":"rag0","signal":"Answered","documents":[[]]}]}}}`
	names, err := demoVerifyComposedSources([]byte(body), http.StatusOK)
	if err == nil {
		t.Fatalf("a turn that retrieved nothing was accepted, names = %v", names)
	}
	for _, want := range []string{"retrieved no documents", "rag0 (0 document(s))", "mage app:seed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %q", err, want)
		}
	}
}

func TestDemoVerifyRejectsEmptyComposedAndNamesTheSiblingLists(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "embedding model mismatch",
			body: `{"metadata":{"sources":{"composed":[],` +
				`"embedding_model_excluded":[{"name":"rag0"}]}}}`,
			want: "excluded for embedding-model mismatch: rag0",
		},
		{
			name: "query failed",
			body: `{"metadata":{"sources":{"composed":[],` +
				`"query_failed":[{"name":"rag1"}]}}}`,
			want: "query failed: rag1",
		},
		{
			name: "source router selected nothing",
			body: `{"metadata":{"sources":{"composed":[],"not_selected":["rag0","rag1"]}}}`,
			want: "not selected by the source router: rag0, rag1",
		},
		{
			name: "degradation reported in the body",
			body: `{"error":"rag0 unreachable","metadata":{"sources":{"composed":[]}}}`,
			want: "response reported: rag0 unreachable",
		},
		{
			name: "every outcome empty reads as an unseeded corpus",
			body: `{"answer":"I do not know.","metadata":{"sources":{"composed":[]}}}`,
			want: "mage app:seed",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			names, err := demoVerifyComposedSources([]byte(test.body), http.StatusOK)
			if err == nil {
				t.Fatalf("empty composed accepted, names = %v", names)
			}
			if !strings.Contains(err.Error(), "composed no sources") {
				t.Errorf("error does not identify the empty composed list: %v", err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want it to name %q", err, test.want)
			}
		})
	}
}

func TestDemoVerifyRejectsNonOKStatusWithTheStatusAndBody(t *testing.T) {
	names, err := demoVerifyComposedSources([]byte("404 page not found"), http.StatusNotFound)
	if err == nil {
		t.Fatalf("non-200 accepted, names = %v", names)
	}
	if !strings.Contains(err.Error(), "status 404") {
		t.Errorf("error does not carry the status: %v", err)
	}
	if !strings.Contains(err.Error(), "404 page not found") {
		t.Errorf("error does not carry the body: %v", err)
	}
	if strings.Contains(err.Error(), "composed no sources") {
		t.Errorf("a transport failure is reported as an attribution failure: %v", err)
	}
}

func TestDemoVerifyRejectsUndecodableBody(t *testing.T) {
	names, err := demoVerifyComposedSources([]byte("<html>ingress is not ready</html>"), http.StatusOK)
	if err == nil {
		t.Fatalf("undecodable body accepted, names = %v", names)
	}
	if !strings.Contains(err.Error(), "decode chat response") {
		t.Errorf("error does not identify the decode failure: %v", err)
	}
}

func TestDemoVerifyExcerptBoundsAnUnexpectedBody(t *testing.T) {
	if got := demoVerifyExcerpt([]byte("   ")); got != "(empty body)" {
		t.Errorf("blank body excerpt = %q, want (empty body)", got)
	}
	long := strings.Repeat("x", demoVerifyBodyLimit+50)
	got := demoVerifyExcerpt([]byte(long))
	if len(got) != demoVerifyBodyLimit+3 || !strings.HasSuffix(got, "...") {
		t.Errorf("long body excerpt length = %d, want %d with an ellipsis",
			len(got), demoVerifyBodyLimit+3)
	}
}

func TestDemoVerifyNamesAnUnnamedComposedSourceByIndex(t *testing.T) {
	body := `{"metadata":{"sources":{"composed":[{"name":"","documents":[["chunk one"]]}]}}}`
	names, err := demoVerifyComposedSources([]byte(body), http.StatusOK)
	if err != nil {
		t.Fatalf("composed turn rejected: %v", err)
	}
	if strings.Join(names, ",") != "source 0 (1 document(s))" {
		t.Fatalf("unnamed composed source = %v, want [source 0 (1 document(s))]", names)
	}
}

// The answering tier is what a deployed gate has instead of a trace. These
// cover the decisions that assertion makes, since a cluster is not available to
// a unit test and the gate is worthless if it cannot fail.

func TestDemoVerifyAnsweringTierReportsTheTier(t *testing.T) {
	body := []byte(`{"answer":"a brief","metadata":{"answered_by":"cohere","sources":{"composed":[]}}}`)
	tier, err := demoVerifyAnsweringTier(body, false)
	if err != nil {
		t.Fatalf("a response naming its tier was rejected: %v", err)
	}
	if tier != "cohere" {
		t.Errorf("tier = %q, want cohere", tier)
	}
}

// A profile predating the field would otherwise read as "no tier" and pass
// silently, which is the failure this whole field exists to prevent.
func TestDemoVerifyAnsweringTierRejectsAMissingField(t *testing.T) {
	body := []byte(`{"answer":"a brief","metadata":{"sources":{"composed":[]}}}`)
	_, err := demoVerifyAnsweringTier(body, false)
	if err == nil {
		t.Fatal("a response with no metadata.answered_by passed")
	}
	if !strings.Contains(err.Error(), "answered_by") {
		t.Errorf("failure %q does not name the missing field", err)
	}
}

func TestDemoVerifyAnsweringTierRejectsUndecodableBody(t *testing.T) {
	if _, err := demoVerifyAnsweringTier([]byte("not json"), false); err == nil {
		t.Error("an undecodable body passed the tier check")
	}
}

// The gate must fail a Cohere-enabled deployment whose turn an Ollama word
// answered, and must accept the same response when the provider is off.
func TestDemoVerifyAnsweringTierRequiresCohereWhenEnabled(t *testing.T) {
	body := []byte(`{"answer":"a brief","metadata":{"answered_by":"ollama","sources":{"composed":[]}}}`)
	if _, err := demoVerifyAnsweringTier(body, true); err == nil {
		t.Error("an Ollama-answered turn passed on a Cohere-enabled release")
	}
	if _, err := demoVerifyAnsweringTier(body, false); err != nil {
		t.Errorf("an Ollama-answered turn was rejected on an Ollama-only release: %v", err)
	}
}

// The query embedding identity is the only thing in the response that names
// which provider embedded the tasking, so these cases carry the whole rel10.3
// deployed assertion.
func TestDemoVerifyQueryEmbeddingAcceptsTheConfiguredModel(t *testing.T) {
	body := []byte(`{"answer":"a brief","metadata":{"answered_by":"cohere",` +
		`"query_embedding_model":"embed-v4.0","sources":{"composed":[]}}}`)
	model, err := demoVerifyQueryEmbedding(
		body, demoCohereRelease{Enabled: true, EmbedModel: "embed-v4.0"})
	if err != nil {
		t.Fatalf("a turn embedded by the configured model was rejected: %v", err)
	}
	if model != "embed-v4.0" {
		t.Errorf("model = %q, want embed-v4.0", model)
	}
}

// The repoint is invisible in the answer and in the composed sources: a mesh
// still embedding through Ollama composes and retrieves exactly as a
// Cohere-embedded one does. Comparing the declared identity is what catches it.
func TestDemoVerifyQueryEmbeddingRejectsAnotherProvidersModel(t *testing.T) {
	body := []byte(`{"answer":"a brief","metadata":{"answered_by":"cohere",` +
		`"query_embedding_model":"qwen3-embedding:8b","sources":{"composed":[]}}}`)
	_, err := demoVerifyQueryEmbedding(
		body, demoCohereRelease{Enabled: true, EmbedModel: "embed-v4.0"})
	if err == nil {
		t.Fatal("a turn embedded by the host Ollama passed the Cohere embedding assertion")
	}
	for _, want := range []string{"qwen3-embedding:8b", "embed-v4.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("failure %q does not name %q", err, want)
		}
	}
}

// A profile predating the field would read as "no identity" and pass silently.
func TestDemoVerifyQueryEmbeddingRejectsAMissingField(t *testing.T) {
	body := []byte(`{"answer":"a brief","metadata":{"answered_by":"cohere","sources":{"composed":[]}}}`)
	_, err := demoVerifyQueryEmbedding(
		body, demoCohereRelease{Enabled: true, EmbedModel: "embed-v4.0"})
	if err == nil {
		t.Fatal("a response with no metadata.query_embedding_model passed")
	}
	if !strings.Contains(err.Error(), "query_embedding_model") {
		t.Errorf("failure %q does not name the missing field", err)
	}
}

// A release that enables Cohere without naming an embedding model has nothing
// to compare against; the gate reports what the turn declared rather than
// failing a deployment the chart permits.
func TestDemoVerifyQueryEmbeddingReportsWhenTheReleaseNamesNoModel(t *testing.T) {
	body := []byte(`{"answer":"a brief","metadata":{"answered_by":"cohere",` +
		`"query_embedding_model":"embed-v4.0","sources":{"composed":[]}}}`)
	model, err := demoVerifyQueryEmbedding(body, demoCohereRelease{Enabled: true})
	if err != nil {
		t.Fatalf("a release naming no embedding model failed the gate: %v", err)
	}
	if model != "embed-v4.0" {
		t.Errorf("model = %q, want the declared embed-v4.0", model)
	}
}

// Repointing the query without reseeding is the failure rel10.3 exists to
// prevent, and it returns 200 with fluent prose. An excluded source never
// reaches the composed list, so the composed-source assertions cannot see it.
func TestDemoVerifyNoEmbeddingExclusionRejectsAnUnreseededMesh(t *testing.T) {
	body := []byte(`{"answer":"a brief","metadata":{"sources":{"composed":[],` +
		`"embedding_model_excluded":[{"name":"rag0"}],` +
		`"not_selected":[],"query_failed":[]}}}`)
	err := demoVerifyNoEmbeddingExclusion(body)
	if err == nil {
		t.Fatal("a mesh whose sources were all excluded passed the exclusion check")
	}
	for _, want := range []string{"rag0", "app:seed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("failure %q does not name %q", err, want)
		}
	}
}

func TestDemoVerifyNoEmbeddingExclusionAcceptsAReseededMesh(t *testing.T) {
	body := []byte(`{"answer":"a brief","metadata":{"sources":{` +
		`"composed":[{"name":"rag0"}],` +
		`"embedding_model_excluded":[],"not_selected":[],"query_failed":[]}}}`)
	if err := demoVerifyNoEmbeddingExclusion(body); err != nil {
		t.Fatalf("a reseeded mesh was rejected: %v", err)
	}
}
