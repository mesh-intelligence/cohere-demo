// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A corpus case (srd018 R1). The build and the seed already take a root and find
// everything under it, so a second domain needs no path parameters: it is a root
// under cases/<name> holding a corpus/ and a data/ in the layout the defense corpus
// uses at the repository root. With no case selected, the root is the repository
// and every target behaves as it did before cases existed.

const (
	corpusCasesDir = "cases"
	corpusCaseEnv  = "CORPUS_CASE"
	// A case's source data sits in its own root, because a case carries only
	// tiers that can be tracked (srd018 R2.5).
	corpusCaseDataDir = "data"
)

// corpusCaseDefinition is what a case supplies beyond its files: how its tiers
// load, what window and selection it declares, and which tiers it recognizes.
type corpusCaseDefinition struct {
	Label       string
	ValuesFile  string
	SourceTypes []string
	Slice       func() corpusSliceParameters
	Selection   func() corpusEmbedSelection
	Load        func(dataRoot string, params corpusSliceParameters, manifest corpusManifest) ([]corpusEnvelope, error)
}

// corpusCaseDefinitions holds every case this build knows, by directory name.
var corpusCaseDefinitions = map[string]corpusCaseDefinition{}

// corpusCaseValuesFile names the overlay app:up layers. A case supplies its own
// beside the defense overlay, because the deploy runner takes one values path.
func corpusCaseValuesFile() string {
	//nolint:forbidigo // the case is an explicit operator choice, like the dataset root.
	name := strings.TrimSpace(os.Getenv(corpusCaseEnv))
	if definition, known := corpusCaseDefinitions[name]; known && definition.ValuesFile != "" {
		return definition.ValuesFile
	}
	return chatbotDemoValuesFile
}

// corpusCaseRoot resolves the root the corpus targets operate on. An unknown name
// fails rather than falling back, because a fallback builds or seeds the defense
// corpus when the operator asked for something else.
func corpusCaseRoot(repositoryRoot string) (string, error) {
	//nolint:forbidigo // the case is an explicit operator choice, like the dataset root.
	name := strings.TrimSpace(os.Getenv(corpusCaseEnv))
	if name == "" {
		return repositoryRoot, nil
	}
	if _, known := corpusCaseDefinitions[name]; !known {
		return "", fmt.Errorf("%s names %q, which is not a known case; known cases: %s",
			corpusCaseEnv, name, strings.Join(corpusKnownCaseNames(), ", "))
	}
	root := filepath.Join(repositoryRoot, corpusCasesDir, name)
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(corpusManifestPath))); err != nil {
		return "", fmt.Errorf("case %q has no manifest at %s: %w", name, root, err)
	}
	return root, nil
}

func corpusKnownCaseNames() []string {
	names := make([]string, 0, len(corpusCaseDefinitions))
	for name := range corpusCaseDefinitions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// corpusRootIsCase reports whether a root sits under a cases directory. It decides
// by location rather than by the registry, so a case root is denied the private
// checkout even if its definition is missing (srd018 R1.3).
func corpusRootIsCase(root string) bool {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	return filepath.Base(filepath.Dir(absolute)) == corpusCasesDir
}

// corpusCaseDefinitionFor returns the definition of a case root, and false for the
// repository root.
func corpusCaseDefinitionFor(root string) (corpusCaseDefinition, bool) {
	if !corpusRootIsCase(root) {
		return corpusCaseDefinition{}, false
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return corpusCaseDefinition{}, false
	}
	definition, known := corpusCaseDefinitions[filepath.Base(absolute)]
	return definition, known
}

// corpusSliceFor is the window a root's build converts.
func corpusSliceFor(root string) corpusSliceParameters {
	if definition, known := corpusCaseDefinitionFor(root); known {
		return definition.Slice()
	}
	return defaultCorpusSliceParameters()
}

// corpusEmbedSelectionFor is the selection a root's build embeds and seed writes.
func corpusEmbedSelectionFor(root string) corpusEmbedSelection {
	if definition, known := corpusCaseDefinitionFor(root); known {
		return definition.Selection()
	}
	return defaultCorpusEmbedSelection()
}

// corpusBuildDataRoot is where a root's source data lives.
func corpusBuildDataRoot(root string) string {
	if corpusRootIsCase(root) {
		return filepath.Join(root, corpusCaseDataDir)
	}
	return corpusDataRoot(root)
}

// corpusRecognizedSourceType reports whether any corpus this build knows declares
// a tier.
func corpusRecognizedSourceType(name string) bool {
	for _, declared := range corpusSourceTypes() {
		if declared == name {
			return true
		}
	}
	for _, definition := range corpusCaseDefinitions {
		for _, declared := range definition.SourceTypes {
			if declared == name {
				return true
			}
		}
	}
	return false
}

// encodeCorpusYAMLFor renders a record under the header naming the corpus it
// belongs to. The repository root keeps the header it always wrote, so a rebuild
// of the defense corpus stays byte-identical.
func encodeCorpusYAMLFor(root string, value interface{}) ([]byte, error) {
	if definition, known := corpusCaseDefinitionFor(root); known {
		return encodeCorpusYAMLNamed(definition.Label, value)
	}
	return encodeCorpusYAML(value)
}
