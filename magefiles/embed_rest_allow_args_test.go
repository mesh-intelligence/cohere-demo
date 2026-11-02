// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"
)

// A provider library fragment's endpoint and allowlist are its allow_scheme,
// allow_host, and allow_port arguments, and it reads no environment (srd058
// R1.6). So the chart renders them as literals into the chatbot's and the
// corpus-ingest child's embed-rest.yaml from the endpoint each one reaches: the
// Cohere pair on the Cohere tier, the LLM URL the chatbot takes as OLLAMA_URL
// on the Ollama tier, and ingestOllamaURL for an Ollama-embedding child. A child
// with neither keeps the library defaults, the loopback Ollama it reached when
// it had no OLLAMA_URL.
func TestEmbedRestRendersAllowArguments(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	staged, cleanup, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
	if err != nil {
		t.Fatalf("stage chart: %v", err)
	}
	defer cleanup()

	cohere := []string{"cohere.enabled=true", "cohere.existingSecret=cohere-credential"}
	inBoundary := append(append([]string{}, cohere...),
		"cohere.baseURL=http://in-boundary.example:9000", "cohere.host=in-boundary.example",
		"cohere.scheme=http", "cohere.port=9000")
	cases := []struct {
		name       string
		sets       []string
		chatbotURL string // empty: the chatbot Deployment's OLLAMA_URL
		ingestURL  string // empty: the library defaults, no allow arguments
	}{
		{name: "Ollama tier"},
		{
			name:       "external LLM",
			sets:       []string{"ollama.enabled=false", "llm.externalURL=https://llm.example:8443"},
			chatbotURL: "https://llm.example:8443",
		},
		{
			name:      "ingest Ollama",
			sets:      []string{"controlPlane.creator.ingestOllamaURL=http://ingest-ollama:11500"},
			ingestURL: "http://ingest-ollama:11500",
		},
		{name: "Cohere tier", sets: cohere, chatbotURL: "https://api.cohere.com:443", ingestURL: "https://api.cohere.com:443"},
		{name: "in-boundary Cohere", sets: inBoundary, chatbotURL: "http://in-boundary.example:9000", ingestURL: "http://in-boundary.example:9000"},
		{
			name: "Cohere tier, Ollama ingest",
			sets: append(append([]string{}, cohere...),
				"controlPlane.creator.ingestEmbedding.provider=ollama",
				"controlPlane.creator.ingestOllamaURL=http://ingest-ollama:11500"),
			chatbotURL: "https://api.cohere.com:443",
			ingestURL:  "http://ingest-ollama:11500",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := renderChartProfiles(t, staged, tc.sets...)

			want := tc.chatbotURL
			if want == "" {
				want = chatbotOllamaURL(t, helmTemplateStaged(t, staged, tc.sets...))
			}
			requireAllowArguments(t, filepath.Join(root, "agents", "chatbot", "embed-rest.yaml"), want)

			ingest := filepath.Join(root, "agents", "corpus-ingest", "embed-rest.yaml")
			if tc.ingestURL == "" {
				if args := embedExpansionArgs(t, ingest); args["allow_host"] != "" {
					t.Errorf("an Ollama ingest with no ingestOllamaURL keeps the library defaults, got %v", args)
				}
				return
			}
			requireAllowArguments(t, ingest, tc.ingestURL)
		})
	}
}

func helmTemplateStaged(t *testing.T, staged string, sets ...string) string {
	t.Helper()
	args := []string{"template", "rel", staged, "--namespace", "nsy"}
	for _, set := range sets {
		args = append(args, "--set", set)
	}
	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	return string(out)
}

var chatbotOllamaEnv = regexp.MustCompile(`- name: OLLAMA_URL\s+value: "([^"]+)"`)

// chatbotOllamaURL is the OLLAMA_URL the chatbot Deployment's container takes.
func chatbotOllamaURL(t *testing.T, render string) string {
	t.Helper()
	match := chatbotOllamaEnv.FindStringSubmatch(render)
	if match == nil {
		t.Fatal("the chatbot Deployment sets no OLLAMA_URL")
	}
	return match[1]
}

func embedExpansionArgs(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var file struct {
		Expand []struct {
			Args map[string]string `yaml:"args"`
		} `yaml:"expand"`
	}
	if err := yaml.Unmarshal(data, &file); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(file.Expand) != 1 {
		t.Fatalf("%s expands %d fragments, want 1", path, len(file.Expand))
	}
	return file.Expand[0].Args
}

func requireAllowArguments(t *testing.T, path, endpoint string) {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse %s: %v", endpoint, err)
	}
	port := parsed.Port()
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[parsed.Scheme]
	}
	args := embedExpansionArgs(t, path)
	want := map[string]string{"allow_scheme": parsed.Scheme, "allow_host": parsed.Hostname(), "allow_port": port}
	for key, value := range want {
		if args[key] != value {
			t.Errorf("%s %s = %q, want %q (endpoint %s)", path, key, args[key], value, endpoint)
		}
	}
}
