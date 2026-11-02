// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func repoRootForUIMachines(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(wd)
}

// Every state carries at least one tag and every tag a state carries is
// declared with a label, so no view is partial and no chip is unlabelled.
// The committed descriptions are a copy of the declarations, and a copy that
// can drift is worse than none: the trace view would caption a call with a
// sentence its word no longer declares. Byte equality with regeneration is the
// guarantee (GH-426); the machines file that used to sit beside it is gone --
// the observer reads declared machines live from the monitor (GH-428).
func TestDeclaredDescriptionsJSONMatchesTheDeclarations(t *testing.T) {
	root := repoRootForUIMachines(t)
	rendered, err := renderDeclaredDescriptionsJSON(root)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	committed, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(declaredDescriptionsJSONPath)))
	if err != nil {
		t.Fatalf("read committed JSON: %v", err)
	}
	if !bytes.Equal(rendered, committed) {
		t.Error("declaredDescriptions.json does not match regeneration from the" +
			" declarations; run `mage ui:machines` and commit the result")
	}
}

func TestDeclaredMachineTagsAreCompleteAndDeclared(t *testing.T) {
	root := repoRootForUIMachines(t)
	// The chatbot's request machine is the one the observer draws; the tags
	// are authored in the profile and served live (GH-428), so authoring
	// completeness is still guarded here at the source.
	for service, source := range map[string]string{
		"chatbot": "agents/chatbot/request-machine.yaml",
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(source)))
		if err != nil {
			t.Fatalf("read %s: %v", source, err)
		}
		var machine struct {
			ViewTags []struct {
				Tag   string `yaml:"tag"`
				Label string `yaml:"label"`
			} `yaml:"view_tags"`
			States []struct {
				Name string   `yaml:"name"`
				Tags []string `yaml:"tags"`
			} `yaml:"states"`
		}
		if err := yaml.Unmarshal(data, &machine); err != nil {
			t.Fatalf("parse %s: %v", source, err)
		}
		declared := make(map[string]bool)
		for _, tag := range machine.ViewTags {
			if tag.Tag == "" || tag.Label == "" {
				t.Errorf("%s declares a view tag without tag or label: %+v", service, tag)
			}
			declared[tag.Tag] = true
		}
		if len(declared) == 0 {
			t.Fatalf("%s declares no view_tags", service)
		}
		for _, state := range machine.States {
			if len(state.Tags) == 0 {
				t.Errorf("%s state %s carries no tag, so no view shows it",
					service, state.Name)
			}
			for _, tag := range state.Tags {
				if !declared[tag] {
					t.Errorf("%s state %s carries undeclared tag %q",
						service, state.Name, tag)
				}
			}
		}
	}
}
