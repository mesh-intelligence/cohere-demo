// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// A proof that stands a mock where the provider calls go must move every
// library expansion there, or the calls it does not pin reach the hosted API
// with the proof's stand-in credential (srd058 R1.6). The other arguments ride
// through unchanged.
func TestPinChatbotProviderEndpointPinsEveryExpansion(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "chatbot")
	if _, err := copyChatbotProfile("..", dir, func(content string) string { return content }); err != nil {
		t.Fatal(err)
	}
	if err := pinChatbotProviderEndpoint(dir, "http://127.0.0.1:38120"); err != nil {
		t.Fatal(err)
	}

	pinned := 0
	for _, name := range chatbotProviderRestFiles {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var file struct {
			Expand []struct {
				Fragment string            `yaml:"fragment"`
				Args     map[string]string `yaml:"args"`
			} `yaml:"expand"`
		}
		if err := yaml.Unmarshal(data, &file); err != nil {
			t.Fatalf("parse pinned %s: %v", name, err)
		}
		for _, expansion := range file.Expand {
			pinned++
			want := map[string]string{"allow_scheme": "http", "allow_host": "127.0.0.1", "allow_port": "38120"}
			for key, value := range want {
				if expansion.Args[key] != value {
					t.Errorf("%s %s: %s = %q, want %q", name, expansion.Fragment, key, expansion.Args[key], value)
				}
			}
			if expansion.Args["body_source"] != "command_state" {
				t.Errorf("%s %s lost its body_source argument: %v", name, expansion.Fragment, expansion.Args)
			}
		}
	}
	if pinned != 3 {
		t.Errorf("pinned %d expansions, want the query embedding, the rerank, and the document embedding", pinned)
	}
}
