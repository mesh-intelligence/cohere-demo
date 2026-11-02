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

// The exporter agent profile (srd013 R1-R5): the export machine's shape, the
// fixed-argv contract on the rendering words, the engine's declared
// confinement, and the absence of any client the export path could reach a
// model, a store, or a network through.

type exporterToolsDoc struct {
	Tools []struct {
		Name   string         `yaml:"name"`
		Type   string         `yaml:"type"`
		Init   string         `yaml:"init"`
		Binary string         `yaml:"binary"`
		Args   []string       `yaml:"args"`
		Config map[string]any `yaml:"config"`
	} `yaml:"tools"`
}

func readExporterYAML(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "agents", "exporter", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func exporterDeclaredTools(t *testing.T, files ...string) exporterToolsDoc {
	t.Helper()
	var all exporterToolsDoc
	for _, file := range files {
		var doc exporterToolsDoc
		if err := yaml.Unmarshal(readExporterYAML(t, file), &doc); err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		all.Tools = append(all.Tools, doc.Tools...)
	}
	return all
}

// TestExporterExecWordsCarryFixedArgv is srd013 R2.2 and R2.3 and suite case
// 5: every exec word's argv is static declared configuration -- the only
// substitution is the workspace environment expansion, never a result
// selector -- and the document's only route to a binary is the fixed file
// path.
func TestExporterExecWordsCarryFixedArgv(t *testing.T) {
	t.Parallel()
	doc := exporterDeclaredTools(t, "exec-declarations.yaml")
	if len(doc.Tools) != 4 {
		t.Fatalf("declared %d exec words, want clean_workspace, md_to_tex, render_pdf, encode_pdf", len(doc.Tools))
	}
	var templated bool
	for _, tool := range doc.Tools {
		if tool.Type != "exec" {
			t.Errorf("%s is type %q, not exec", tool.Name, tool.Type)
		}
		if tool.Name == "md_to_tex" {
			for _, arg := range tool.Args {
				if strings.Contains(arg, "--template=") && strings.Contains(arg, "brief-template.tex") {
					templated = true
				}
			}
		}
		for _, arg := range tool.Args {
			if strings.Contains(arg, "$.") || strings.Contains(arg, "$from(") {
				t.Errorf("%s argv carries a result selector %q; srd013 R2.2 forbids any runtime value in argv", tool.Name, arg)
			}
			if strings.Contains(arg, "${") &&
				!strings.Contains(arg, "${EXPORTER_WORK_DIR:-/work}") &&
				!strings.Contains(arg, "${EXPORTER_PROFILE_DIR:-/profiles}") {
				t.Errorf("%s argv carries an unexpected expansion %q; only the workspace and profile roots expand", tool.Name, arg)
			}
		}
	}
	if !templated {
		t.Error("md_to_tex does not declare the projected brief-template.tex; the form restyle (GH-454) would silently fall back to pandoc's default")
	}
}

// TestExporterEngineRunsConfined is srd013 R2.4 and R2.5 and suite cases 6
// and 7: the engine's argv declares shell escape off and paranoid file
// access, so the constraints are reviewable configuration rather than image
// trivia.
func TestExporterEngineRunsConfined(t *testing.T) {
	t.Parallel()
	doc := exporterDeclaredTools(t, "exec-declarations.yaml")
	for _, tool := range doc.Tools {
		if tool.Name != "render_pdf" {
			continue
		}
		argv := strings.Join(tool.Args, " ")
		for _, want := range []string{"-no-shell-escape", "openin_any=p", "openout_any=p", "-halt-on-error", "-output-directory"} {
			if !strings.Contains(argv, want) {
				t.Errorf("render_pdf argv is missing %q: %v", want, tool.Args)
			}
		}
		return
	}
	t.Fatal("render_pdf is not declared")
}

// TestExporterBindsNoClients is srd013 R3.1 and suite case 8's static half:
// the exporter's REST definition declares servers only -- no client, no
// auth beyond none, no host outside loopback -- so no export word could
// generate, retrieve, or call anything even before the NetworkPolicy denies
// the packets.
func TestExporterBindsNoClients(t *testing.T) {
	t.Parallel()
	var rest struct {
		Rest struct {
			Clients map[string]any `yaml:"clients"`
			Limits  map[string]struct {
				Network struct {
					Hosts []string `yaml:"hosts"`
				} `yaml:"network"`
			} `yaml:"limits"`
		} `yaml:"rest"`
	}
	if err := yaml.Unmarshal(readExporterYAML(t, "rest.yaml"), &rest); err != nil {
		t.Fatalf("parse exporter rest.yaml: %v", err)
	}
	if len(rest.Rest.Clients) != 0 {
		t.Errorf("the exporter declares %d REST clients; srd013 R3.1 allows none", len(rest.Rest.Clients))
	}
	for name, limits := range rest.Rest.Limits {
		for _, host := range limits.Network.Hosts {
			if host != "127.0.0.1" && host != "localhost" {
				t.Errorf("limits %s permit host %q; the exporter reaches nothing", name, host)
			}
		}
	}
	for _, file := range []string{"export-declarations.yaml", "exec-declarations.yaml", "declarations.yaml"} {
		content := string(readExporterYAML(t, file))
		for _, banned := range []string{"invoke_llm", "rest_client_invoke", "cohere", "rag_query"} {
			if strings.Contains(content, banned) {
				t.Errorf("%s carries %q; the export path binds no model, retrieval, or provider client", file, banned)
			}
		}
	}
}

// TestExportMachineFailsCleanAndEncodesLast is srd013 R5.1, R5.2, and R1.5
// as machine structure: every failure leg routes through the cleanup word
// before Failed, the encode is the last word before Done so a partial PDF
// can never be the mapped output, and the endpoint maps the encoder's
// output as the returned document.
func TestExportMachineFailsCleanAndEncodesLast(t *testing.T) {
	t.Parallel()
	var machine struct {
		Transitions []struct {
			State  string `yaml:"state"`
			Signal string `yaml:"signal"`
			Next   string `yaml:"next"`
			Action string `yaml:"action"`
		} `yaml:"transitions"`
	}
	if err := yaml.Unmarshal(readExporterYAML(t, "export-machine.yaml"), &machine); err != nil {
		t.Fatalf("parse export machine: %v", err)
	}
	stages := map[string]bool{"PreparingWorkspace": true, "ConvertingTex": true, "RenderingPdf": true, "Reporting": true}
	var encodeDispatched bool
	for _, tr := range machine.Transitions {
		if stages[tr.State] && tr.Next == "Failed" {
			t.Errorf("%s/%s terminates in Failed without cleanup; srd013 R5.1 rolls back first", tr.State, tr.Signal)
		}
		failure := tr.Signal == "ToolFailed" || (tr.Signal == "CommandError" && tr.State != "AwaitingExport" && tr.State != "CleaningUp")
		if stages[tr.State] && failure && tr.Action != "clean_workspace" {
			t.Errorf("%s/%s dispatches %q, not the cleanup word", tr.State, tr.Signal, tr.Action)
		}
		if tr.State == "RenderingPdf" && tr.Signal == "ToolDone" {
			encodeDispatched = tr.Action == "encode_pdf" && tr.Next == "Reporting"
		}
		if tr.State == "Reporting" && tr.Signal == "ToolDone" && (tr.Next != "Done" || tr.Action != "") {
			t.Errorf("the encoder's success must terminate in Done with no further action, got %s/%s", tr.Next, tr.Action)
		}
	}
	if !encodeDispatched {
		t.Error("a successful render must dispatch encode_pdf into Reporting; the encoded PDF is the final output Done maps")
	}
	rest := string(readExporterYAML(t, "rest.yaml"))
	if !strings.Contains(rest, "pdf_base64: $.output") {
		t.Error("the export endpoint does not map the encoder's output as the returned document (srd013 R1.5)")
	}
}
