// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// fromSelector matches how one word addresses another word's result.
var fromSelector = regexp.MustCompile(`\$from\(([A-Za-z0-9_]+)\)`)

// wordDeclaration matches the declaring line of a word in a declarations file.
var wordDeclaration = regexp.MustCompile(`^\s*-\s+name:\s+([A-Za-z0-9_]+)\s*$`)

// machineLabel matches the two ways a machine names a result that is not a
// word: `label:` on a transition, which names the result a branch produced
// whichever action ran, and `as:` on a for_each, which names the current item.
// Both are addressed with $from and neither is declared as a word.
var machineLabel = regexp.MustCompile(`(?:^|[\s,{])(?:label|as):\s+([A-Za-z0-9_]+)`)

// TestFromSelectorsNameDeclaredWords proves every $from(word) selector in an
// agent names a word that agent declares.
//
// Nothing else catches this. `agent --validate-config` -- the check the mesh
// boot smoke runs -- accepts $from(no_such_word) and reports the profile valid,
// and at runtime an unresolved selector does not fail: a compose word renders
// the value as empty and the machine reaches its terminal state reporting
// success. A misspelled word therefore ships a turn that answers with an empty
// prompt, green all the way through audit (GH-221).
func TestFromSelectorsNameDeclaredWords(t *testing.T) {
	agentsRoot := filepath.Dir(agentDir(t, "chatbot"))
	entries, err := os.ReadDir(agentsRoot)
	if err != nil {
		t.Fatalf("read the agents directory: %v", err)
	}

	var scanned int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		agent := entry.Name()
		declared, files := declaredWordsAndFiles(t, filepath.Join(agentsRoot, agent))
		if len(declared) == 0 {
			continue
		}
		for path, body := range files {
			for i, line := range strings.Split(body, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "#") {
					continue
				}
				for _, match := range fromSelector.FindAllStringSubmatch(line, -1) {
					scanned++
					if declared[match[1]] {
						continue
					}
					// "seed" is the runtime's synthetic entry for the trusted
					// request input (srd030 R3.2), addressable from any word
					// without a declaration.
					if match[1] == "seed" {
						continue
					}
					t.Errorf(
						"%s/%s:%d selects $from(%s), which %s declares no word named."+
							" validate-config accepts this and the runtime resolves it to"+
							" nothing, so the turn would answer on an empty value: %s",
						agent, filepath.Base(path), i+1, match[1], agent, strings.TrimSpace(line))
				}
			}
		}
	}
	if scanned == 0 {
		t.Fatal("no $from selector found in any agent; the scan would pass vacuously")
	}
}

// declaredWordsAndFiles returns the set of word names an agent declares and the
// contents of its top-level YAML files. Words and the selectors that address
// them are spread across an agent's declaration files, so both halves are
// collected per agent rather than per file.
func declaredWordsAndFiles(t *testing.T, dir string) (map[string]bool, map[string]string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	declared := map[string]bool{}
	files := map[string]string{}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		files[path] = string(body)
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if match := wordDeclaration.FindStringSubmatch(line); match != nil {
				declared[match[1]] = true
				names = append(names, match[1])
			}
			for _, match := range machineLabel.FindAllStringSubmatch(line, -1) {
				declared[match[1]] = true
				names = append(names, match[1])
			}
		}
	}
	sort.Strings(names)
	for name := range wrappedMachineBindings(t, dir) {
		declared[name] = true
	}
	return declared, files
}

// wrappedMachineBindings returns the labels and for_each item names declared by
// the machine a wrapper profile points at. A wrapper (the applier, the observer)
// declares where it binds and what it serves, while the machine that names the
// results its REST operations select lives in the catalog, so the names a
// wrapper's selectors resolve against are not in its own directory.
func wrappedMachineBindings(t *testing.T, dir string) map[string]bool {
	t.Helper()
	bindings := map[string]bool{}
	profile, err := os.ReadFile(filepath.Join(dir, "profile.yaml"))
	if err != nil {
		return bindings
	}
	var declaration struct {
		Machine string `yaml:"machine"`
	}
	if err := yaml.Unmarshal(profile, &declaration); err != nil {
		t.Fatalf("parse %s/profile.yaml: %v", dir, err)
	}
	const catalogPrefix = "../../catalog/"
	if !strings.HasPrefix(declaration.Machine, catalogPrefix) {
		return bindings
	}
	// The catalog is the pinned module, not a directory in this repository; the
	// packaging step is what stages it beside the wrappers.
	catalogRoot, err := resolveCatalogRoot("from-selector check", filepath.Dir(filepath.Dir(dir)))
	if err != nil {
		t.Skipf("catalog root not resolvable: %v", err)
	}
	relative := strings.TrimPrefix(declaration.Machine, catalogPrefix)
	machine, err := os.ReadFile(filepath.Join(catalogRoot, "agents", filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read the catalog machine %s wraps: %v", dir, err)
	}
	for _, line := range strings.Split(string(machine), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, match := range machineLabel.FindAllStringSubmatch(line, -1) {
			bindings[match[1]] = true
		}
	}
	return bindings
}
