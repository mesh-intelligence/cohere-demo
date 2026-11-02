// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// The GKE deploy layers ci/gcp-values.yaml, which binds no report, and the
// deploy runner takes one values path -- so the investment case ran on kind
// only (GH-932). The case's BINDING -- its report, its interface vocabulary,
// and its units' descriptions and collections -- merges into the overrides
// document instead: everything environment-shaped (registries, sizes, the
// Cohere-only tier, the embedding identity the GKE corpus is seeded with)
// stays the overlay's.

// gcpCaseValues is the slice of a case values file that is binding rather than
// environment.
type gcpCaseValues struct {
	Chatbot struct {
		Report map[string]any `yaml:"report"`
		UI     map[string]any `yaml:"ui"`
	} `yaml:"chatbot"`
	RagUnits []map[string]any `yaml:"ragUnits"`
}

// gcpCaseBinding reads the selected case's binding for a GKE deploy. The
// defense default binds nothing (its report and vocabulary are the packaged
// ones), so it returns nil; a selected case returns its chatbot.report,
// chatbot.ui, and its ragUnits re-identified to the environment's embedding
// identity, because srd002 R3.3 excludes any source whose reported identity
// differs from the query vector's.
func gcpCaseBinding(root, embedIdentity string) (map[string]any, error) {
	name := strings.TrimSpace(os.Getenv(corpusCaseEnv)) //nolint:forbidigo // the case is an explicit operator choice
	if name == "" {
		return nil, nil
	}
	if _, known := corpusCaseDefinitions[name]; !known {
		return nil, fmt.Errorf("%s names %q, which is not a known case; known cases: %s",
			corpusCaseEnv, name, strings.Join(corpusKnownCaseNames(), ", "))
	}
	path := filepath.Join(root, "helm", "ci", corpusCaseValuesFile())
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("gcp:deploy: read the case binding: %w", err)
	}
	var values gcpCaseValues
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("gcp:deploy: decode the case binding %s: %w", path, err)
	}
	binding := map[string]any{}
	chatbot := map[string]any{}
	if len(values.Chatbot.Report) > 0 {
		chatbot["report"] = values.Chatbot.Report
	}
	if len(values.Chatbot.UI) > 0 {
		chatbot["ui"] = values.Chatbot.UI
	}
	if len(chatbot) > 0 {
		binding["chatbot"] = chatbot
	}
	if len(values.RagUnits) > 0 {
		units := make([]any, 0, len(values.RagUnits))
		for _, unit := range values.RagUnits {
			bound := make(map[string]any, len(unit))
			for key, value := range unit {
				bound[key] = value
			}
			if embedIdentity != "" {
				bound["embeddingModel"] = embedIdentity
			}
			units = append(units, bound)
		}
		binding["ragUnits"] = units
	}
	if len(binding) == 0 {
		return nil, fmt.Errorf("gcp:deploy: the case values %s carry no binding", path)
	}
	return binding, nil
}
