// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

// chatEndpointFields reads the chat endpoint's declared body fields and the
// fields its machine_request forwards, from one chatbot rest.yaml.
func chatEndpointFields(t *testing.T, label string, data []byte) (declared, forwarded []string) {
	t.Helper()
	var rest struct {
		Rest struct {
			Servers map[string]struct {
				Endpoints map[string]struct {
					Request struct {
						BodySchema struct {
							Properties map[string]any `yaml:"properties"`
						} `yaml:"body_schema"`
					} `yaml:"request"`
					MachineRequest struct {
						Request struct {
							Body map[string]any `yaml:"body"`
						} `yaml:"request"`
					} `yaml:"machine_request"`
				} `yaml:"endpoints"`
			} `yaml:"servers"`
		} `yaml:"rest"`
	}
	if err := yaml.Unmarshal(data, &rest); err != nil {
		t.Fatalf("parse %s: %v", label, err)
	}
	for _, server := range rest.Rest.Servers {
		chat, ok := server.Endpoints["chat"]
		if !ok {
			continue
		}
		for field := range chat.Request.BodySchema.Properties {
			declared = append(declared, field)
		}
		for field := range chat.MachineRequest.Request.Body {
			forwarded = append(forwarded, field)
		}
		sort.Strings(declared)
		sort.Strings(forwarded)
		return declared, forwarded
	}
	t.Fatalf("%s declares no chat endpoint", label)
	return nil, nil
}

// A chart deployment serves the chatbot's rest.yaml co-generated from
// _chatbot-rest.tpl, not the committed file. A request field added to the
// committed chat endpoint alone is accepted by a deployment and silently never
// reaches the machine -- the revision re-grounding's prior_record_ids shipped
// that way and never fired outside the local rig (GH-765).
func TestRenderedChatEndpointCarriesTheCommittedRequestFields(t *testing.T) {
	committed, err := os.ReadFile(filepath.Join("..", "agents", "chatbot", "rest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	wantDeclared, wantForwarded := chatEndpointFields(t, "committed rest.yaml", committed)

	for _, sets := range [][]string{nil, {"cohere.enabled=true", "cohere.existingSecret=cohere-credential"}} {
		out, ok := renderChart(t, sets...)
		if !ok {
			t.Fatalf("render %v failed:\n%s", sets, out)
		}
		block := configMapKeyBlock(out, "agents__chatbot__rest.yaml")
		if block == "" {
			t.Fatalf("render %v carries no co-generated agents__chatbot__rest.yaml", sets)
		}
		gotDeclared, gotForwarded := chatEndpointFields(t, "rendered rest.yaml", []byte(block))
		for _, field := range wantDeclared {
			if !containsString(gotDeclared, field) {
				t.Errorf("render %v: the chat endpoint does not declare %q, which the committed one does", sets, field)
			}
		}
		for _, field := range wantForwarded {
			if !containsString(gotForwarded, field) {
				t.Errorf("render %v: the chat endpoint does not forward %q to the machine, which the committed one does", sets, field)
			}
		}
	}
}

// restClientOperations reads every client's operation names from one chatbot
// rest.yaml.
func restClientOperations(t *testing.T, label string, data []byte) map[string][]string {
	t.Helper()
	var rest struct {
		Rest struct {
			Clients map[string]struct {
				Operations map[string]any `yaml:"operations"`
			} `yaml:"clients"`
		} `yaml:"rest"`
	}
	if err := yaml.Unmarshal(data, &rest); err != nil {
		t.Fatalf("parse %s: %v", label, err)
	}
	clients := map[string][]string{}
	for name, client := range rest.Rest.Clients {
		for operation := range client.Operations {
			clients[name] = append(clients[name], operation)
		}
	}
	return clients
}

// The same drift for the words' boundaries: an operation declared only in the
// committed rest.yaml is a word that fails to resolve its rest_ref in every
// chart deployment. With the provider enabled, the render carries every client
// and operation the committed file declares (GH-611).
func TestRenderedRestCarriesTheCommittedClientOperations(t *testing.T) {
	committed, err := os.ReadFile(filepath.Join("..", "agents", "chatbot", "rest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := restClientOperations(t, "committed rest.yaml", committed)
	out, ok := renderChart(t, "cohere.enabled=true", "cohere.existingSecret=cohere-credential")
	if !ok {
		t.Fatalf("render failed:\n%s", out)
	}
	got := restClientOperations(t, "rendered rest.yaml", []byte(configMapKeyBlock(out, "agents__chatbot__rest.yaml")))
	for client, operations := range want {
		for _, operation := range operations {
			if !containsString(got[client], operation) {
				t.Errorf("the rendered chatbot rest.yaml lacks %s.%s, which the committed one declares", client, operation)
			}
		}
	}
}
