// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mesh-intelligence/declarative-agents/magefiles/apprig"
	"gopkg.in/yaml.v3"
)

// The defense default binds nothing; the selected case binds its report, its
// vocabulary, and its units at the environment's embedding identity (GH-932).
func TestGcpCaseBindingCarriesTheCaseAtTheEnvironmentIdentity(t *testing.T) {
	root := filepath.Dir(findChartDir(t))

	t.Setenv(corpusCaseEnv, "")
	binding, err := gcpCaseBinding(root, "embed-v4.0")
	if err != nil || binding != nil {
		t.Fatalf("default case binding = %v, %v; want none", binding, err)
	}

	t.Setenv(corpusCaseEnv, investmentCaseName)
	binding, err = gcpCaseBinding(root, "embed-v4.0")
	if err != nil {
		t.Fatal(err)
	}
	chatbot, ok := binding["chatbot"].(map[string]any)
	if !ok || chatbot["report"] == nil || chatbot["ui"] == nil {
		t.Fatalf("binding carries no report and vocabulary: %v", binding)
	}
	units, ok := binding["ragUnits"].([]any)
	if !ok || len(units) == 0 {
		t.Fatalf("binding carries no units: %v", binding)
	}
	unit := units[0].(map[string]any)
	if unit["embeddingModel"] != "embed-v4.0" {
		t.Errorf("unit identity %v, want the environment's embed-v4.0", unit["embeddingModel"])
	}
	if description, _ := unit["description"].(string); !strings.Contains(description, "energy") {
		t.Errorf("unit description %q is not the case's", description)
	}

	t.Setenv(corpusCaseEnv, "no-such-case")
	if _, err := gcpCaseBinding(root, "embed-v4.0"); err == nil {
		t.Error("an unknown case bound silently")
	}
}

// The overrides document carries the case binding last, over the defense
// defaults the overlay pins.
func TestGcpCohereOverridesMergeTheCaseBinding(t *testing.T) {
	binding := map[string]any{
		"chatbot":  map[string]any{"report": map[string]any{"templateVersion": "memo@1"}},
		"ragUnits": []any{map[string]any{"name": "rag0", "collection": "corpus", "embeddingModel": "embed-v4.0"}},
	}
	images := gcpDeployImages{
		Registries: gcpRegistries{Ghcr: gcpTestGhcr, Dockerhub: gcpTestDockerhub},
		RuntimeTag: "v0.20261003.0", ExporterTag: "v0.20261003.0-abc", Host: gcpTestHost,
	}
	overrides, err := gcpCohereOverrides(apprig.Resolved{}, images, nil, nil, binding)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := yaml.Unmarshal([]byte(overrides), &values); err != nil {
		t.Fatal(err)
	}
	chatbot := values["chatbot"].(map[string]any)
	report := chatbot["report"].(map[string]any)
	if report["templateVersion"] != "memo@1" {
		t.Errorf("report did not merge: %v", chatbot)
	}
	if _, ok := values["ragUnits"].([]any); !ok {
		t.Errorf("units did not merge: %v", values["ragUnits"])
	}
}
