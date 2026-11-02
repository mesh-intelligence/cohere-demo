// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The corpus vectors and the query vector have to come from one provider. The
// seed selects it by rebinding the child's embedder root to the provider's
// library (srd058 R2.2) and naming the identity that library embeds with.
func TestSeedEmbeddingSelectsTheCohereLibrary(t *testing.T) {
	provider := demoSeedEmbeddingProvider{cohere: true, model: "embed-v4.0"}
	args := provider.libraryArgs("/core")
	want := []string{"--library", "embedder=" + filepath.Join("/core", "tools", "providers", "cohere")}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Errorf("library args = %v, want %v", args, want)
	}
	environment := provider.environment()
	if len(environment) != 1 || environment[0] != "CORPUS_EMBEDDING_MODEL=embed-v4.0" {
		t.Errorf("environment = %v, want [CORPUS_EMBEDDING_MODEL=embed-v4.0]", environment)
	}
}

// The shipped default stays Ollama: a seed that silently repointed every
// developer's corpus at a paid provider would be the wrong default, and the
// profile already binds the Ollama library.
func TestSeedEmbeddingOverridesNothingForOllama(t *testing.T) {
	provider := demoSeedEmbeddingProvider{}
	if environment := provider.environment(); len(environment) != 0 {
		t.Errorf("the Ollama path set %v, want no overrides", environment)
	}
	if args := provider.libraryArgs("/core"); len(args) != 0 {
		t.Errorf("the Ollama path rebinds %v, want the profile's own binding", args)
	}
}

// An override has to win over whatever the operator exported, or a stale
// COHERE_EMBED_MODEL in the shell would seed vectors under one identity while
// the deployed chatbot declares another.
func TestChromaChildEnvironmentOverridesInheritedEntries(t *testing.T) {
	inherited := []string{
		"PATH=/usr/bin",
		"COHERE_EMBED_MODEL=stale-model",
		"CORPUS_EMBEDDING_MODEL=stale-model",
	}
	environment := chromaChildEnvironment(
		inherited, "OTEL_RESOURCE_ATTRIBUTES=service.name=test", "",
		"CORPUS_EMBEDDING_MODEL=embed-v4.0",
		"COHERE_EMBED_MODEL=embed-v4.0",
	)
	counts := map[string][]string{}
	for _, entry := range environment {
		key, value, _ := strings.Cut(entry, "=")
		counts[key] = append(counts[key], value)
	}
	for key, want := range map[string]string{
		"CORPUS_EMBEDDING_MODEL": "embed-v4.0",
		"COHERE_EMBED_MODEL":     "embed-v4.0",
	} {
		if len(counts[key]) != 1 {
			t.Errorf("%s appears %d times, want exactly one", key, len(counts[key]))
			continue
		}
		if counts[key][0] != want {
			t.Errorf("%s = %q, want %q", key, counts[key][0], want)
		}
	}
	if len(counts["PATH"]) != 1 || counts["PATH"][0] != "/usr/bin" {
		t.Errorf("PATH = %v, want the inherited /usr/bin preserved", counts["PATH"])
	}
}

// Embedding through Cohere means the host Ollama embedding model is never
// called, so requiring it would skip a seed the provider can serve. The
// corpus-ingest chat model stays required either way.
func TestSeedRequiredModelsDropTheOllamaEmbeddingModelForCohere(t *testing.T) {
	applicationRoot := filepath.Clean("..")
	ollamaModels, err := chromaSeedRequiredModels(applicationRoot, demoSeedEmbeddingProvider{})
	if err != nil {
		t.Fatalf("required models for the Ollama path: %v", err)
	}
	cohereModels, err := chromaSeedRequiredModels(
		applicationRoot, demoSeedEmbeddingProvider{cohere: true, model: "embed-v4.0"})
	if err != nil {
		t.Fatalf("required models for the Cohere path: %v", err)
	}
	if len(cohereModels) >= len(ollamaModels) {
		t.Errorf("Cohere required %v and Ollama required %v; the embedding model should drop",
			cohereModels, ollamaModels)
	}
	embed, err := chromaEmbedModelFromConfig(applicationRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range cohereModels {
		if model == embed {
			t.Errorf("the Cohere path still requires the Ollama embedding model %q", embed)
		}
	}
}

// Enabling the provider without repointing the RAG units is the configuration
// that answers 200 with every source silently excluded, so the two move
// together or not at all.
func TestCohereTierRepointsEveryRagUnitEmbeddingModel(t *testing.T) {
	chart := t.TempDir()
	if err := os.MkdirAll(filepath.Join(chart, "ci"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chart, "values.yaml"),
		[]byte("cohere:\n  enabled: false\n  embedModel: embed-v4.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	valuesPath := filepath.Join(chart, "ci", chatbotDemoValuesFile)
	if err := os.WriteFile(valuesPath,
		[]byte("ragUnits:\n  - name: rag0\n    embeddingModel: qwen3-embedding:8b\n"+
			"  - name: rag1\n    embeddingModel: qwen3-embedding:8b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	units, err := chartRagUnits(valuesPath)
	if err != nil {
		t.Fatal(err)
	}
	values := cohereApplicationExtras(cohereExtrasInput{
		CohereEnabled: true, EmbedModel: demoCohereEmbedModel(chart), RagUnits: units,
	})
	if values["cohere"].(map[string]any)["enabled"] != true {
		t.Error("the Cohere tier does not enable the provider")
	}
	repointed, _ := values["ragUnits"].([]any)
	if len(repointed) != 2 {
		t.Fatalf("repointed %d RAG units, want 2", len(repointed))
	}
	for _, unit := range repointed {
		if model := unit.(map[string]any)["embeddingModel"]; model != "embed-v4.0" {
			t.Errorf("unit %v keeps embedding model %v", unit.(map[string]any)["name"], model)
		}
	}
}

// Without a credential the mesh comes up on Ollama exactly as before, and
// nothing about the RAG units changes.
func TestOllamaTierLeavesTheProviderAndRagUnitsAlone(t *testing.T) {
	values := cohereApplicationExtras(cohereExtrasInput{
		EmbedModel: "embed-v4.0",
		RagUnits:   []map[string]any{{"name": "rag0", "embeddingModel": "qwen3-embedding:8b"}},
	})
	for _, key := range []string{"cohere", "ragUnits"} {
		if _, set := values[key]; set {
			t.Errorf("the Ollama tier sets %s", key)
		}
	}
}
