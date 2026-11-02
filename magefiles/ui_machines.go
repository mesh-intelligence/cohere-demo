// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"strings"

	"gopkg.in/yaml.v3"
)

// The chatbot UI's trace view shows each call's declared description (GH-426);
// it ships the descriptions alone, in its own tree, under the same
// regeneration-sync guarantee.
const declaredDescriptionsJSONPath = "agents/chatbot/ui/app/src/declaredDescriptions.json"

// declaredDescriptionGlobs name the declarations files whose word descriptions
// the trace view shows beside each call (GH-426). One line per word, straight
// from the declaration the executor runs.
var declaredDescriptionGlobs = []string{
	"agents/chatbot/*declarations*.yaml",
}

// collectDeclaredDescriptions reads every declarations file the globs name and
// returns each word's declared description keyed by word name; map iteration
// is keyed, and the JSON encoder sorts keys, so regeneration stays
// byte-identical.
func collectDeclaredDescriptions(root string) (map[string]string, error) {
	descriptions := map[string]string{}
	for _, glob := range declaredDescriptionGlobs {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(glob)))
		if err != nil {
			return nil, err
		}
		for _, path := range matches {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read declarations %s: %w", path, err)
			}
			words, err := declaredWordsIn(path, data, demoCoreRoot(root))
			if err != nil {
				return nil, err
			}
			for _, word := range words {
				if word.Name == "" || word.Description == "" {
					continue
				}
				descriptions[word.Name] = strings.TrimSpace(word.Description)
			}
		}
	}
	return descriptions, nil
}

type describedWord struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// declaredWordsIn returns the words a declarations file declares: its own, the
// ones the units it imports declare, and the ones each fragment it instantiates
// produces under the arguments it gives. The chatbot's lifecycle words arrive
// from agent-core's serve-lifecycle fragment (GH-738), and the trace view still
// shows their descriptions.
func declaredWordsIn(path string, data []byte, coreRoot string) ([]describedWord, error) {
	var doc struct {
		Tools       []describedWord `yaml:"tools"`
		Imports     []string        `yaml:"imports"`
		Instantiate []struct {
			Fragment string         `yaml:"fragment"`
			Args     map[string]any `yaml:"args"`
		} `yaml:"expand"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse declarations %s: %w", path, err)
	}
	words := doc.Tools
	for _, ref := range doc.Imports {
		imported, err := unitWords(unitPath(path, ref, coreRoot), nil)
		if err != nil {
			return nil, fmt.Errorf("%s imports %s: %w", path, ref, err)
		}
		words = append(words, imported...)
	}
	for _, instance := range doc.Instantiate {
		produced, err := unitWords(unitPath(path, instance.Fragment, coreRoot), instance.Args)
		if err != nil {
			return nil, fmt.Errorf("%s instantiates %s: %w", path, instance.Fragment, err)
		}
		words = append(words, produced...)
	}
	return words, nil
}

// unitPath resolves a unit reference the way the loader does: an agent-core
// prefix maps to the core checkout, anything else is relative to the file.
func unitPath(from, ref, coreRoot string) string {
	if rest, ok := strings.CutPrefix(ref, "/opt/agent-core/"); ok {
		return filepath.Join(coreRoot, filepath.FromSlash(rest))
	}
	return filepath.Join(filepath.Dir(from), filepath.FromSlash(ref))
}

var paramRef = regexp.MustCompile(`\$param\(([A-Za-z_][A-Za-z0-9_]*)\)`)

// unitWords reads a unit's words after substituting arguments, and then
// parameter defaults, into its scalar values (srd052 R2). A unit with no
// params is read as written.
func unitWords(path string, args map[string]any) ([]describedWord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	var header struct {
		Params []struct {
			Name    string `yaml:"name"`
			Default any    `yaml:"default"`
		} `yaml:"params"`
	}
	if err := root.Decode(&header); err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, param := range header.Params {
		if param.Default != nil {
			values[param.Name] = fmt.Sprint(param.Default)
		}
	}
	for name, value := range args {
		values[name] = fmt.Sprint(value)
	}
	substituteParams(&root, values)
	var doc struct {
		Tools []describedWord `yaml:"tools"`
	}
	if err := root.Decode(&doc); err != nil {
		return nil, err
	}
	return doc.Tools, nil
}

func substituteParams(node *yaml.Node, values map[string]string) {
	if node.Kind == yaml.ScalarNode {
		node.Value = paramRef.ReplaceAllStringFunc(node.Value, func(ref string) string {
			if value, ok := values[paramRef.FindStringSubmatch(ref)[1]]; ok {
				return value
			}
			return ref
		})
	}
	for _, child := range node.Content {
		substituteParams(child, values)
	}
}

// renderDeclaredDescriptionsJSON renders the chatbot UI's descriptions file.
func renderDeclaredDescriptionsJSON(root string) ([]byte, error) {
	descriptions, err := collectDeclaredDescriptions(root)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(map[string]any{"descriptions": descriptions}); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// Machines regenerates the declared-descriptions JSON the chatbot UI's trace
// view imports. The observer reads declared machines and declared tool
// records live from each agent's monitor (GH-428, GH-433), so nothing else is
// generated any more.
func (UI) Machines() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	rendered, err := renderDeclaredDescriptionsJSON(root)
	if err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(declaredDescriptionsJSONPath))
	if err := os.WriteFile(path, rendered, 0o644); err != nil {
		return err
	}
	fmt.Printf("ui:machines wrote %s (%d bytes)\n", declaredDescriptionsJSONPath, len(rendered))
	return nil
}
