// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The OSINT search branch (srd011 R1, R2, R5): the outbound request carries
// the tasking and nothing else, results normalize into the RAG candidate
// shape, and every branch failure degrades to a status word rather than
// failing the turn. These pin the declared contracts in the committed files
// and the deployed render, where a drift would otherwise surface only live.

type osintRestDoc struct {
	Rest struct {
		Limits map[string]struct {
			Redirect struct {
				Mode string `yaml:"mode"`
			} `yaml:"redirect"`
			Network struct {
				Schemes []string `yaml:"schemes"`
				Hosts   []string `yaml:"hosts"`
				Ports   []any    `yaml:"ports"`
			} `yaml:"network"`
		} `yaml:"limits"`
		Clients map[string]struct {
			BaseURL    string `yaml:"base_url"`
			Operations map[string]struct {
				Method          string `yaml:"method"`
				BaseURLSource   string `yaml:"base_url_source"`
				BaseURLSelector string `yaml:"base_url_selector"`
				Params          struct {
					Path         map[string]any    `yaml:"path"`
					Headers      map[string]any    `yaml:"headers"`
					Query        map[string]any    `yaml:"query"`
					InputMapping map[string]string `yaml:"input_mapping"`
				} `yaml:"params"`
			} `yaml:"operations"`
		} `yaml:"clients"`
	} `yaml:"rest"`
}

// deployedOSINTRest extracts and decodes the co-generated rest.yaml key from a
// rendered chart manifest.
func deployedOSINTRest(t *testing.T, render string) osintRestDoc {
	t.Helper()
	restKey := "agents__chatbot__rest.yaml: |-"
	idx := strings.Index(render, restKey)
	if idx < 0 {
		t.Fatal("the rendered profiles ConfigMap carries no co-generated rest.yaml key")
	}
	body := render[idx+len(restKey):]
	if end := strings.Index(body, "\n  agents__"); end > 0 {
		body = body[:end]
	}
	return decodeOSINTRest(t, "deployed", []byte(unindentConfigMapValue(body)))
}

func decodeOSINTRest(t *testing.T, label string, data []byte) osintRestDoc {
	t.Helper()
	var doc osintRestDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%s: parse rest yaml: %v", label, err)
	}
	return doc
}

// TestOSINTEgressCarriesTaskingOnly is srd011 R2.1-R2.4 as a declared,
// reviewable fact: in the committed rest.yaml and in the deployed render, the
// search operation's only runtime binding is the tasking, and the client never
// follows a redirect (R1.4). A second selector appearing in that mapping --
// clearance, history, chunk content -- fails here before it ever leaves an
// enclave.
func TestOSINTEgressCarriesTaskingOnly(t *testing.T) {
	t.Parallel()
	committed := decodeOSINTRest(t, "committed", readAgentYAML(t, "chatbot", "rest.yaml"))
	deployed := deployedOSINTRest(t, renderOSINTChart(t))

	for label, doc := range map[string]osintRestDoc{"committed": committed, "deployed": deployed} {
		client, ok := doc.Rest.Clients["osint"]
		if !ok {
			t.Errorf("%s rest.yaml declares no osint client", label)
			continue
		}
		op, ok := client.Operations["search"]
		if !ok {
			t.Errorf("%s osint client declares no search operation", label)
			continue
		}
		if op.Method != "GET" {
			t.Errorf("%s search method is %q, not GET", label, op.Method)
		}
		// The declared egress is the derived search terms under the shared label
		// (GH-457): the model's distillation of the tasking, or the raw question
		// on the fallback leg -- still exactly one declared parameter.
		if len(op.Params.InputMapping) != 1 || op.Params.InputMapping["q"] != "$from(search_terms).$" {
			t.Errorf("%s search input_mapping must bind exactly q to the declared tasking, got %v",
				label, op.Params.InputMapping)
		}
		limits, ok := doc.Rest.Limits["osint_search"]
		if !ok {
			t.Errorf("%s rest.yaml declares no osint_search limits", label)
			continue
		}
		if limits.Redirect.Mode != "none" {
			t.Errorf("%s osint_search redirect mode is %q; srd011 R1.4 requires none", label, limits.Redirect.Mode)
		}
	}
}

// unindentConfigMapValue strips the four-space block indentation a ConfigMap
// literal value carries in a rendered manifest.
func unindentConfigMapValue(block string) string {
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "    ")
	}
	return strings.Join(lines, "\n")
}

// TestOSINTDocumentsMatchTheRAGCandidateShape is srd011 R1.6 and R7.3: the
// normalized external document carries the same id/data envelope
// parse_documents validates for a RAG chunk, the osint source mark, and the
// URL provenance a citation exposes.
func TestOSINTDocumentsMatchTheRAGCandidateShape(t *testing.T) {
	t.Parallel()
	decl := string(readAgentYAML(t, "chatbot", "request-osint-declarations.yaml"))
	for _, want := range []string{
		// The id is positional (osint-N): a URL identity leaks the engine's
		// full link into every citation chip, and the URL rides separately as
		// provenance.
		`{"id": "osint-{{ position }}"`,
		`"source": "osint"`,
		`"record_id": "osint-{{ position }}"`,
		`"url": {{ json link }}`,
		`"content": {{ json snippet }}`,
	} {
		if !strings.Contains(decl, want) {
			t.Errorf("render_osint_documents is missing %q from the candidate envelope", want)
		}
	}
}

// TestOSINTBranchDegradesAndNeverFails is srd011 R5.1-R5.3 as machine
// structure, on both request machines: no transition out of a branch state
// reaches Failed, every CommandError inside the branch lands on the errored
// status word, and each status leg rejoins the turn at the grounding render.
func TestOSINTBranchDegradesAndNeverFails(t *testing.T) {
	t.Parallel()
	branchStates := map[string]bool{
		"CheckingOsintEnabled":    true,
		"Searching":               true,
		"CheckingOsintRefusal":    true,
		"CheckingOsintResults":    true,
		"RenderingOsintDocuments": true,
	}
	for _, machine := range []string{"request-machine.yaml", "request-machine-ollama.yaml"} {
		spec := readMachineSpec(t, machine)
		var seen int
		for _, tr := range spec.Transitions {
			if !branchStates[tr.State] {
				continue
			}
			seen++
			if tr.Next == "Failed" {
				t.Errorf("%s: %s/%s fails the turn; srd011 R5.1 requires the branch to degrade",
					machine, tr.State, tr.Signal)
			}
			wantOnError := "declare_osint_errored"
			if tr.State == "Searching" {
				// A failed search routes through the refusal check, which
				// separates a policy drop from an outage (srd011 R1.3).
				wantOnError = "check_osint_dropped"
			}
			if tr.Signal == "CommandError" && tr.Action != wantOnError {
				t.Errorf("%s: %s/CommandError dispatches %s, want %s",
					machine, tr.State, tr.Action, wantOnError)
			}
		}
		if seen == 0 {
			t.Errorf("%s carries no OSINT branch transitions", machine)
		}
		for _, exit := range []string{"DeclaringOsintComposed", "DeclaringOsintEmpty", "DeclaringOsintErrored", "DeclaringOsintDropped", "DeclaringOsintDisabled"} {
			key := fmt.Sprintf("%s/OsintStatusDeclared", exit)
			var found bool
			for _, tr := range spec.Transitions {
				if tr.State+"/"+tr.Signal != key {
					continue
				}
				found = true
				if tr.Next != "RenderingDocuments" || tr.Action != "render_grounding" {
					t.Errorf("%s: %s rejoins at %s/%s rather than the grounding render",
						machine, key, tr.Next, tr.Action)
				}
			}
			if !found {
				t.Errorf("%s: no rejoin transition for %s", machine, key)
			}
		}
	}
}
