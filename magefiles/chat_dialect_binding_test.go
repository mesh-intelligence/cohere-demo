// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// The chatbot's invoke_llm words name a chat dialect under a library root, and
// the release tier moves the tier-switched ones by rebinding that root (srd058
// R2.2), not by rewriting the declarations. These hold the three pieces that
// have to agree: every word names a declared root, the Cohere-off render passes
// the rebind to both chatbot containers, and the Cohere-on render passes none.

func TestChatbotInvokeLLMWordsNameADeclaredDialectRoot(t *testing.T) {
	var profile struct {
		Libraries map[string]string `yaml:"libraries"`
	}
	readIntakeYAML(t, filepath.Join(agentDir(t, "chatbot"), "request-profile.yaml"), &profile)
	for _, file := range []string{"request-declarations.yaml", "request-osint-declarations.yaml"} {
		var decls struct {
			Tools []struct {
				Name   string `yaml:"name"`
				Init   string `yaml:"init"`
				Config struct {
					Provider string `yaml:"provider"`
					Dialect  string `yaml:"dialect"`
				} `yaml:"config"`
			} `yaml:"tools"`
		}
		readIntakeYAML(t, filepath.Join(agentDir(t, "chatbot"), file), &decls)
		for _, tool := range decls.Tools {
			if tool.Init != "invoke_llm" {
				continue
			}
			if tool.Config.Provider != "" {
				t.Errorf("%s %s still names provider %q; name a dialect under a library root (srd058 R3.2)",
					file, tool.Name, tool.Config.Provider)
			}
			if dialectLibrary(tool.Config.Dialect, profile.Libraries) == "" {
				t.Errorf("%s %s reads dialect %q, which no root the request profile declares resolves to a provider library",
					file, tool.Name, tool.Config.Dialect)
			}
		}
	}
}

func TestChatbotRebindsProvidersToOllamaOnlyWithCohereOff(t *testing.T) {
	const rebind = "- \"--library\"\n            - \"providers=/opt/agent-core/tools/providers/ollama\""
	off, ok := renderChart(t)
	if !ok {
		t.Fatalf("render failed:\n%s", off)
	}
	if got := strings.Count(off, rebind); got != 2 {
		t.Errorf("the Cohere-off render passes the providers rebind %d time(s), want 2 (validate-config init and the chatbot)", got)
	}
	on, ok := renderChart(t, "cohere.enabled=true", "cohere.existingSecret=cohere-credential")
	if !ok {
		t.Fatalf("render failed:\n%s", on)
	}
	if strings.Contains(on, "providers=/opt/agent-core/tools/providers/ollama") {
		t.Error("the Cohere-on render still rebinds the providers root to Ollama")
	}
	var profile struct {
		Libraries map[string]string `yaml:"libraries"`
	}
	readIntakeYAML(t, filepath.Join(agentDir(t, "chatbot"), "profile.yaml"), &profile)
	if _, ok := profile.Libraries["providers"]; !ok {
		t.Error("the chatbot profile declares no providers root; agent-core refuses the rebind at startup")
	}
}
