// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The export relay (srd013 R4.1, R4.2, GH-352): the SPA's export action
// reaches the exporter through the chatbot's origin, because the exporter's
// NetworkPolicy admits only that caller. These tests pin the relay's shape --
// one fixed operation, a body selected from the endpoint's seeded fields
// alone, and a rendered endpoint that exists exactly when the exporter does.

func TestExportRelayWordCarriesFixedOperation(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "agents", "chatbot", "export-relay-declarations.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Tools []struct {
			Name   string         `yaml:"name"`
			Init   string         `yaml:"init"`
			Config map[string]any `yaml:"config"`
		} `yaml:"tools"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Tools) != 1 || doc.Tools[0].Name != "relay_export" {
		t.Fatalf("the relay declares %d words, want relay_export alone", len(doc.Tools))
	}
	word := doc.Tools[0]
	if word.Init != "rest_client_invoke" {
		t.Errorf("relay_export init is %q, not the declared client invoke", word.Init)
	}
	if ref, _ := word.Config["rest_ref"].(string); ref != "exporter" {
		t.Errorf("relay_export binds client %q, want exporter", ref)
	}
	if op, _ := word.Config["operation"].(string); op != "render_pdf" {
		t.Errorf("relay_export invokes operation %q, want render_pdf", op)
	}
}

func TestExportRelayMachineTerminatesPerOutcome(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "agents", "chatbot", "export-relay-machine.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var machine struct {
		Transitions []struct {
			State  string `yaml:"state"`
			Signal string `yaml:"signal"`
			Next   string `yaml:"next"`
		} `yaml:"transitions"`
	}
	if err := yaml.Unmarshal(data, &machine); err != nil {
		t.Fatal(err)
	}
	wants := map[string]string{
		"ExportRendered": "Rendered",
		"ExportFailed":   "RenderFailed",
		"CommandError":   "Failed",
	}
	for _, tr := range machine.Transitions {
		if tr.State != "Relaying" {
			continue
		}
		if next, ok := wants[tr.Signal]; !ok || tr.Next != next {
			t.Errorf("Relaying/%s -> %s; each exporter outcome must map to its own terminal state", tr.Signal, tr.Next)
		}
		delete(wants, tr.Signal)
	}
	for signal := range wants {
		t.Errorf("the relay machine handles no Relaying/%s leg", signal)
	}
}

// TestExportEndpointRendersWithTheExporter pins the toggle: the chat origin
// serves /api/v1/export exactly when the exporter workload exists, so a
// render without the exporter carries no endpoint that could only 502.
func TestExportEndpointRendersWithTheExporter(t *testing.T) {
	t.Parallel()
	enabled := renderExporterChart(t)
	for _, want := range []string{"path: /api/v1/export", "base_url: http://relx-chatbot-mesh-exporter:18120", "machine: export-relay-machine.yaml"} {
		if !strings.Contains(enabled, want) {
			t.Errorf("the enabled render is missing %q", want)
		}
	}
	disabled := renderExporterChart(t, "--set", "exporter.enabled=false")
	for _, banned := range []string{"path: /api/v1/export", "exporter_render", "render_pdf"} {
		if strings.Contains(disabled, banned) {
			t.Errorf("the exporter-disabled render still carries %q", banned)
		}
	}
}
