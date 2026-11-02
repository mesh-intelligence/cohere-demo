// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// envReference matches a ${NAME:-default} environment reference.
var envReference = regexp.MustCompile(`\$\{([A-Z_]+):-[^}]*\}`)

// TestQueryEmbeddingIdentityReadsOneReference proves the embed request body and
// declare_query_model name the same environment variable, per tier.
//
// srd002 R3.3 compares the identity declare_query_model puts into command state
// against what each rag-server reports, and excludes any source that differs --
// while the answer still returns 200. The identity that is true is whatever the
// embed request actually sent, so the two reads have to resolve from one
// variable or a deployment that sets one and not the other answers ungrounded
// with nothing failing (GH-228 item 7; the GH-213 chart fix derived the
// server-side identities, this pins the query side).
func TestQueryEmbeddingIdentityReadsOneReference(t *testing.T) {
	embed := readAgentFile(t, "chatbot", "embed-rest.yaml")
	declarations := readAgentFile(t, "chatbot", "request-declarations.yaml")

	embedRef := envRefAfter(t, embed, "/opt/providers/embed-query-fragment.yaml", "model: ")
	declareRef := envRefAfter(t, declarations, "- name: declare_query_model", `{"model": "`)
	if embedRef != declareRef {
		t.Errorf("the committed profile embeds with %s and declares %s: a deployment"+
			" setting one and not the other is excluded from every turn while"+
			" answering 200", embedRef, declareRef)
	}

	// The disabled tier: packaging rewrites declare_query_model's reference and
	// the query embedding's model together, and both must read the variable the
	// Deployment sets.
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	staged, cleanup, err := stageSmokeChart(chartDir, chartParent(t, chartDir))
	if err != nil {
		t.Fatalf("stage chart: %v", err)
	}
	defer cleanup()
	out, err := exec.Command("helm", "template", "rel", staged, "--namespace", "nsy").CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	// Scope each read to the ConfigMap key it lives in: the rendered document
	// carries every agent, and an unscoped anchor matches the wrong one.
	rendered := string(out)
	embedKey := sliceFrom(t, rendered, "agents__chatbot__embed-rest.yaml: |-")
	declKey := sliceFrom(t, rendered, "agents__chatbot__request-declarations.yaml: |-")
	renderedDeclare := envRefAfter(t, declKey, "- name: declare_query_model", `{"model": "`)
	if renderedDeclare != "CHATBOT_EMBEDDING_MODEL" {
		t.Errorf("the disabled tier declares the query model from %s, want CHATBOT_EMBEDDING_MODEL", renderedDeclare)
	}
	renderedEmbed := envRefAfter(t, embedKey, "/opt/providers/embed-query-fragment.yaml", "model: ")
	if renderedEmbed != renderedDeclare {
		t.Errorf("the disabled tier embeds with %s while declare_query_model reads %s:"+
			" it reports an identity nothing embedded with", renderedEmbed, renderedDeclare)
	}
	if !strings.Contains(rendered, "name: CHATBOT_EMBEDDING_MODEL") {
		t.Error("the disabled tier's Deployment does not set CHATBOT_EMBEDDING_MODEL")
	}
}

// literalAfter returns the rest of the line following `prefix` after `anchor`.
func literalAfter(t *testing.T, body, anchor, prefix string) string {
	t.Helper()
	at := strings.Index(body, anchor)
	if at < 0 {
		t.Fatalf("anchor %q not found", anchor)
	}
	rest := body[at:]
	from := strings.Index(rest, prefix)
	if from < 0 {
		t.Fatalf("prefix %q not found after %q", prefix, anchor)
	}
	line := rest[from+len(prefix):]
	if end := strings.IndexAny(line, "\n}"); end > 0 {
		line = line[:end]
	}
	return strings.TrimSpace(line)
}

// envRefAfter returns the environment variable named by the first ${NAME:-...}
// reference following `prefix` after `anchor` in body.
func envRefAfter(t *testing.T, body, anchor, prefix string) string {
	t.Helper()
	at := strings.Index(body, anchor)
	if at < 0 {
		t.Fatalf("anchor %q not found", anchor)
	}
	rest := body[at:]
	from := strings.Index(rest, prefix)
	if from < 0 {
		t.Fatalf("prefix %q not found after %q", prefix, anchor)
	}
	match := envReference.FindStringSubmatch(rest[from:])
	if match == nil {
		t.Fatalf("no env reference after %q %q", anchor, prefix)
	}
	return match[1]
}

// sliceFrom returns body from the first occurrence of anchor to the next
// ConfigMap data key, so a search cannot drift into another agent's profile.
func sliceFrom(t *testing.T, body, anchor string) string {
	t.Helper()
	at := strings.Index(body, anchor)
	if at < 0 {
		t.Fatalf("rendered chart carries no %q", anchor)
	}
	rest := body[at+len(anchor):]
	if next := strings.Index(rest, ".yaml: |-"); next > 0 {
		if line := strings.LastIndex(rest[:next], "\n"); line > 0 {
			return rest[:line]
		}
	}
	return rest
}

// chartParent returns the profiles root the staging helper expects.
func chartParent(t *testing.T, chartDir string) string {
	t.Helper()
	return strings.TrimSuffix(chartDir, "/helm")
}
