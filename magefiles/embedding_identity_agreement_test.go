// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestRenderedEmbeddingIdentitiesAgree proves a deployment ingests, reports, and
// queries under one embedding identity.
//
// srd002 R3.3 excludes any source whose reported identity differs from the query
// vector's, so these have to be the same string or the turn retrieves nothing
// while still answering. Three consumers read it: RAG_EMBEDDING_MODEL, which the
// rag-server reports; CORPUS_EMBEDDING_MODEL, which the creator's family gate
// compares an intent against and the corpus child embeds with; and the
// co-generated topology.
//
// All three took ragUnits[].embeddingModel, which does not follow the Cohere
// tier, while the chart sent the corpus child to Cohere's embedding. Enabling the tier without editing
// every unit therefore wrote the corpus in one family, reported another, and
// left the creator's gate refusing the family it ingests at while admitting the
// one it does not (GH-213).
func TestRenderedEmbeddingIdentitiesAgree(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)

	for _, tier := range []struct {
		enabled string
		want    string
	}{
		{"false", "qwen3-embedding:8b"},
		{"true", "embed-v4.0"},
	} {
		args := []string{"template", "rel", chartDir, "--namespace", "nsy",
			"--set", "controlPlane.enabled=true", "--set", "cohere.enabled=" + tier.enabled}
		if tier.enabled == "true" {
			args = append(args, "--set", "cohere.existingSecret=k", "--set", "cohere.secretKey=v")
		}
		out, err := exec.Command("helm", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("helm template with cohere.enabled=%s: %v\n%s", tier.enabled, err, out)
		}
		rendered := string(out)

		identities := map[string][]string{
			"RAG_EMBEDDING_MODEL":    envValues(rendered, "RAG_EMBEDDING_MODEL"),
			"CORPUS_EMBEDDING_MODEL": envValues(rendered, "CORPUS_EMBEDDING_MODEL"),
			"topology embedding_model": regexp.
				MustCompile(`"embedding_model":\s*"([^"]+)"`).
				FindAllStringSubmatch(rendered, -1)[0][1:],
		}
		for source, values := range identities {
			if len(values) == 0 {
				t.Errorf("cohere.enabled=%s renders no %s", tier.enabled, source)
				continue
			}
			for _, value := range unique(values) {
				if value != tier.want {
					t.Errorf("cohere.enabled=%s renders %s = %q, want %q; a source reporting"+
						" anything else is excluded from every turn",
						tier.enabled, source, value, tier.want)
				}
			}
		}
	}
}

// envValues returns every value rendered for one container environment name.
func envValues(rendered, name string) []string {
	pattern := regexp.MustCompile(`\{name: ` + regexp.QuoteMeta(name) + `, value: "([^"]*)"\}`)
	var values []string
	for _, match := range pattern.FindAllStringSubmatch(rendered, -1) {
		values = append(values, match[1])
	}
	return values
}

func unique(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// TestCreatorFamilyGateReadsTheIngestIdentity pins the operand the gate compares
// an intent against. It has to be the identity the corpus child embeds with, not
// a second variable that happens to agree at the defaults.
func TestCreatorFamilyGateReadsTheIngestIdentity(t *testing.T) {
	body := readAgentFile(t, "creator", "request-declarations.yaml")
	if !strings.Contains(body, "right: ${CORPUS_EMBEDDING_MODEL:-qwen3-embedding:8b}") {
		t.Error("the family gate no longer compares against CORPUS_EMBEDDING_MODEL;" +
			" if the operand moved, the chart must set the new name from the same" +
			" tier-derived identity, or the gate inverts again")
	}
}
