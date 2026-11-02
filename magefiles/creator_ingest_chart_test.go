// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"strings"
	"testing"
)

// creatorContainerEnv returns the creator container's env block from a render,
// so an assertion about the creator cannot be satisfied by another pod's
// variable of the same name. The applier and the chatbot both carry Cohere
// variables, and a substring search over the whole render would find theirs.
func creatorContainerEnv(t *testing.T, rendered string) string {
	t.Helper()
	const marker = "name: creator"
	start := strings.Index(rendered, marker)
	if start < 0 {
		t.Fatalf("the render declares no creator container")
	}
	section := rendered[start:]
	if end := strings.Index(section, "volumeMounts"); end > 0 {
		section = section[:end]
	}
	return section
}

// The child runs as a subprocess of the creator and inherits its environment.
// Without a Chroma authority it falls back to the loopback address a developer
// host answers, which in a pod resolves to nothing.
func TestCreatorCarriesTheIngestChromaAuthority(t *testing.T) {
	out, ok := renderChart(t, "controlPlane.enabled=true")
	if !ok {
		t.Fatalf("control-plane render failed:\n%s", out)
	}
	env := creatorContainerEnv(t, out)
	for _, want := range []string{
		`{name: CHROMA_URL, value: "http://t-chatbot-mesh-rag0-chroma:8000"}`,
		`{name: CHROMA_HOST, value: "t-chatbot-mesh-rag0-chroma"}`,
		`{name: CHROMA_PORT, value: "8000"}`,
	} {
		if !strings.Contains(env, want) {
			t.Errorf("the creator container omits %s", want)
		}
	}
}

// The Chroma is a choice, not a lookup: a mesh has one per RAG unit. Naming a
// unit moves the ingest to it, and nothing about the ordering of the list
// decides where documents land.
func TestCreatorIngestChromaFollowsTheNamedRagUnit(t *testing.T) {
	out, ok := renderChart(t, "controlPlane.enabled=true",
		"controlPlane.creator.ingestRagUnit=rag1")
	if !ok {
		t.Fatalf("render failed:\n%s", out)
	}
	env := creatorContainerEnv(t, out)
	if !strings.Contains(env, "t-chatbot-mesh-rag1-chroma") {
		t.Error("naming rag1 did not move the ingest Chroma to that unit")
	}
	if strings.Contains(env, "t-chatbot-mesh-rag0-chroma") {
		t.Error("the creator still points at rag0's Chroma after naming rag1")
	}
}

// srd002 R3.3 excludes any source whose reported identity differs from the
// query vector's. An ingest that embedded at Ollama while the release serves
// Cohere would add a source no turn can retrieve, so the family follows the
// release rather than the child's ambient default.
func TestCreatorIngestEmbeddingFollowsTheEnabledProvider(t *testing.T) {
	// The units are repointed alongside the provider, which is how demo:up
	// installs a Cohere release: enabling the tier without repointing them is the
	// configuration that excludes every source from every turn.
	out, ok := renderChart(t, "controlPlane.enabled=true",
		"cohere.enabled=true", "cohere.existingSecret=cohere-credential",
		"ragUnits[0].name=rag0", "ragUnits[0].description=d",
		"ragUnits[0].collection=corpus", "ragUnits[0].replicas=1",
		"ragUnits[0].embeddingModel=embed-v4.0")
	if !ok {
		t.Fatalf("render failed:\n%s", out)
	}
	env := creatorContainerEnv(t, out)
	for _, want := range []string{
		`{name: CORPUS_INGEST_EMBEDDER, value: "cohere"}`,
		`{name: CORPUS_EMBEDDING_MODEL, value: "embed-v4.0"}`,
	} {
		if !strings.Contains(env, want) {
			t.Errorf("the creator container omits %s", want)
		}
	}
}

// The collection is the one the targeted unit's rag-server serves, so documents
// the ingest writes are documents a turn can retrieve.
func TestCreatorIngestCollectionIsTheServedCollection(t *testing.T) {
	out, ok := renderChart(t, "controlPlane.enabled=true",
		"controlPlane.creator.ingestRagUnit=rag1")
	if !ok {
		t.Fatalf("render failed:\n%s", out)
	}
	env := creatorContainerEnv(t, out)
	if !strings.Contains(env, `{name: CORPUS_INGEST_COLLECTION, value: "corpus2"}`) {
		t.Error("the ingest collection is not the collection rag1 serves")
	}
}

// srd002 R3.3 compares each rag-server's reported identity against the query
// vector's, so the family the corpus is written in, the family the unit reports,
// and the family the query embeds at have to be one string.
//
// This test used to require the opposite of what it now asserts: that the ingest
// model follow ragUnits[].embeddingModel even with the Cohere tier on, so that
// repointing one unit could not add documents at a family that unit does not
// report. The reasoning was right and the variable was wrong. The chart binds
// the corpus child's embedder root to the Cohere library whenever the tier is
// on, so pinning a unit to another family did not make the child embed at it --
// it made the child embed at Cohere while the unit reported something else and
// the creator's family gate compared against a third answer, which is the
// divergence the old expectation was written to prevent (GH-213).
//
// The per-unit field still decides with the tier off, where the child embeds
// through the Ollama library.
func TestCreatorIngestModelFollowsTheSelectedEmbeddingPath(t *testing.T) {
	out, ok := renderChart(t, "controlPlane.enabled=true",
		"cohere.enabled=true", "cohere.existingSecret=cohere-credential",
		"ragUnits[0].name=rag0", "ragUnits[0].description=d",
		"ragUnits[0].collection=corpus", "ragUnits[0].replicas=1",
		"ragUnits[0].embeddingModel=qwen3-embedding:8b")
	if !ok {
		t.Fatalf("render failed:\n%s", out)
	}
	env := creatorContainerEnv(t, out)
	// The tier is on and the unit is pinned to the other family. The child
	// embeds through the Cohere library either way, so the gate's operand has to
	// name the model that library embeds with.
	if !strings.Contains(env, `{name: CORPUS_INGEST_EMBEDDER, value: "cohere"}`) {
		t.Fatal("the corpus child does not take the Cohere embedding path, so this case proves nothing")
	}
	if !strings.Contains(env, `{name: CORPUS_EMBEDDING_MODEL, value: "embed-v4.0"}`) {
		t.Error("the ingest model does not name the family the selected embed operation writes at," +
			" so the creator's gate refuses the family it ingests at and admits one it does not")
	}
}

// The endpoint and its allowlist move together (srd009 R1.2). A repointed base
// URL behind an allowlist still naming the hosted endpoint is a refusal.
func TestCreatorCarriesTheCohereAllowlistWithTheEndpoint(t *testing.T) {
	out, ok := renderChart(t, "controlPlane.enabled=true",
		"cohere.enabled=true", "cohere.existingSecret=cohere-credential",
		"cohere.baseURL=http://in-boundary.example:9000", "cohere.host=in-boundary.example",
		"cohere.scheme=http", "cohere.port=9000")
	if !ok {
		t.Fatalf("render failed:\n%s", out)
	}
	env := creatorContainerEnv(t, out)
	for _, want := range []string{
		`{name: COHERE_BASE_URL, value: "http://in-boundary.example:9000"}`,
		`{name: COHERE_HOST, value: "in-boundary.example"}`,
		`{name: COHERE_SCHEME, value: "http"}`,
		`{name: COHERE_PORT, value: "9000"}`,
	} {
		if !strings.Contains(env, want) {
			t.Errorf("the creator container omits %s; the allowlist did not move with the endpoint", want)
		}
	}
}

// The chart accepts a Secret name, not a credential (srd009 R1.3), so no value
// reaches a manifest or `helm get values`.
func TestCreatorTakesTheCohereCredentialByReference(t *testing.T) {
	out, ok := renderChart(t, "controlPlane.enabled=true",
		"cohere.enabled=true", "cohere.existingSecret=cohere-credential")
	if !ok {
		t.Fatalf("render failed:\n%s", out)
	}
	env := creatorContainerEnv(t, out)
	if !strings.Contains(env, "secretKeyRef") ||
		!strings.Contains(env, `name: "cohere-credential"`) {
		t.Error("the creator does not take COHERE_API_KEY by Secret reference")
	}
	for _, leak := range []string{"co-", "api-key: "} {
		if strings.Contains(env, leak+"live") {
			t.Errorf("the creator env carries what looks like a credential value (%q)", leak)
		}
	}
}

// A mesh with no provider enabled has nothing to misconfigure. The variables are
// absent rather than present and empty, matching how the chatbot renders.
func TestCreatorOmitsProviderConfigurationWhenCohereIsDisabled(t *testing.T) {
	out, ok := renderChart(t, "controlPlane.enabled=true")
	if !ok {
		t.Fatalf("render failed:\n%s", out)
	}
	env := creatorContainerEnv(t, out)
	for _, absent := range []string{
		"COHERE_API_KEY", "COHERE_BASE_URL", "COHERE_API_URL", "COHERE_EMBED_MODEL",
		// The embedder stays unset so the child keeps the Ollama library its
		// profile binds; naming it would be configuration with nothing to
		// configure.
		"CORPUS_INGEST_EMBEDDER",
	} {
		if strings.Contains(env, absent) {
			t.Errorf("the creator carries %q with no provider enabled", absent)
		}
	}
	// The model is still named, because an Ollama-family ingest embeds at the
	// identity its unit reports just as a Cohere one does. Leaving it unset would
	// fall back to the child's own default, which no unit is obliged to match.
	if !strings.Contains(env, `{name: CORPUS_EMBEDDING_MODEL, value: "qwen3-embedding:8b"}`) {
		t.Error("the creator does not name the Ollama identity its unit reports")
	}
}

// An operator pointing the ingest at an in-cluster Ollama sets the one
// variable both its embedding library and its invoke_llm dialect read.
func TestCreatorIngestOllamaURLReachesTheChild(t *testing.T) {
	out, ok := renderChart(t, "controlPlane.enabled=true",
		"controlPlane.creator.ingestOllamaURL=http://t-chatbot-mesh-ollama:11434")
	if !ok {
		t.Fatalf("render failed:\n%s", out)
	}
	env := creatorContainerEnv(t, out)
	if want := `{name: OLLAMA_URL, value: "http://t-chatbot-mesh-ollama:11434"}`; !strings.Contains(env, want) {
		t.Errorf("the creator container omits %s", want)
	}
	if strings.Contains(env, "CORPUS_INGEST_OLLAMA") {
		t.Error("the creator still sets a corpus-ingest Ollama variable nothing reads")
	}
}

// The child's clients must be repointable at all. Loopback literals are correct
// on a developer host and unreachable in a pod, and an allowlist still pinned to
// 127.0.0.1 would refuse a repointed base URL rather than reach it.
func TestCorpusIngestClientsTakeTheirAuthorityFromTheEnvironment(t *testing.T) {
	data, err := os.ReadFile("../agents/corpus-ingest/corpus-rest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	declared := string(data)
	for _, want := range []string{
		"${CHROMA_URL:-http://127.0.0.1:8000}",
		"${CHROMA_HOST:-127.0.0.1}",
		"${CHROMA_PORT:-8000}",
	} {
		if !strings.Contains(declared, want) {
			t.Errorf("corpus-rest.yaml does not reference %s", want)
		}
	}
	// The defaults are what a developer host answers, so a host-side ingest that
	// exported none of these behaves exactly as it did.
	if strings.Contains(declared, "base_url: http://127.0.0.1:") {
		t.Error("a client still hardcodes a loopback base_url; it cannot be repointed in a pod")
	}
}
