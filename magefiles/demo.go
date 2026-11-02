// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/magefile/mage/mg"
	"github.com/mesh-intelligence/declarative-agents/magefiles/kindrig"
	"gopkg.in/yaml.v3"
)

const (
	// demoCohereSecret names the Secret the chart's secretKeyRef points at. The
	// chart accepts a name, never a credential.
	demoCohereSecret      = "cohere-credential"
	demoCohereSecretKey   = "api-key"
	demoSerpapiSecret     = "serpapi-credential"
	demoSerpapiSecretKey  = "api-key"
	chatbotDemoValuesFile = "kind-values.yaml"
)

// Demo keeps the verbs operators know as wrappers over the platform lifecycle
// (eng01 Table 9): the demo is the application on the shared da-platform, not
// a cluster of its own (GH-788).
type Demo mg.Namespace

// Doctor checks the shared ENG01 toolchain and host resources without mutation.
func Doctor() error {
	return kindrig.Doctor()
}

// Up brings up the pinned da-platform, or re-proves a running one, and deploys
// the application onto it: platform:up then app:up.
func (Demo) Up() error {
	if err := Doctor(); err != nil {
		return fmt.Errorf("demo requested but preflight failed: %w", err)
	}
	if err := (Platform{}).Up(); err != nil {
		return err
	}
	return (App{}).Up()
}

// Down removes the application and leaves the platform running for every
// other application on it: app:down.
func (Demo) Down() error {
	return (App{}).Down()
}

// demoCohereEmbedModel reads the chart's own Cohere embedding model, so the
// value the RAG units are repointed to and the value the chatbot renders into
// COHERE_EMBED_MODEL come from one declaration rather than a repeated literal.
func demoCohereEmbedModel(staged string) string {
	data, err := os.ReadFile(filepath.Join(staged, "values.yaml"))
	if err != nil {
		return ""
	}
	var values struct {
		Cohere struct {
			EmbedModel string `yaml:"embedModel"`
		} `yaml:"cohere"`
	}
	if err := yaml.Unmarshal(data, &values); err != nil {
		return ""
	}
	return strings.TrimSpace(values.Cohere.EmbedModel)
}

// secretsFileKey reads one named key from the operator store, with the same
// main-checkout fallback the Cohere reader keeps: a worktree without its own
// copied .secrets still resolves.
func secretsFileKey(name string) string {
	data, err := os.ReadFile(filepath.Join(".secrets", "keys.json"))
	if err != nil {
		if root := gitMainCheckoutRoot(); root != "" {
			data, err = os.ReadFile(filepath.Join(root, ".secrets", "keys.json"))
		}
	}
	if err != nil {
		return ""
	}
	var store map[string]any
	if err := json.Unmarshal(data, &store); err != nil {
		return ""
	}
	if value, ok := store[name].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

// serpapiCredential reads the metered search key from the same operator
// store as the Cohere key, under "serpapi". Empty is the normal case: the
// fixture engine needs no credential, and app:up never blocks on this.
func serpapiCredential() string {
	if value := strings.TrimSpace(os.Getenv("SERPAPI_API_KEY")); value != "" { //nolint:forbidigo // operator-supplied credential; the single audited read of this channel.
		return value
	}
	return secretsFileKey("serpapi")
}

func demoRequiredModels(chartDir string) ([]string, error) {
	var values struct {
		Ollama struct {
			Models struct {
				Embedding string   `yaml:"embedding"`
				Chat      []string `yaml:"chat"`
				Tier      string   `yaml:"tier"`
			} `yaml:"models"`
		} `yaml:"ollama"`
	}
	if err := readIntegrationYAML(filepath.Join(chartDir, "values.yaml"),
		"chart values", &values); err != nil {
		return nil, err
	}
	candidates := append([]string{values.Ollama.Models.Embedding},
		values.Ollama.Models.Chat...)
	candidates = append(candidates, values.Ollama.Models.Tier)
	seen := map[string]bool{}
	models := make([]string, 0, len(candidates))
	for _, model := range candidates {
		if model != "" && !seen[model] {
			seen[model] = true
			models = append(models, model)
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("chart values declare no Ollama models for the demo")
	}
	return models, nil
}
