// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// The public mirror images live under this repository's own namespace, outside
// the prefix reusedeps.Verify enforces, so deps:verify would stop holding the
// chart to the release the moment the values point at the mirror (GH-934).
// This check closes that gap: every chart image under the mirror prefix must
// carry the pinned release's tag -- exactly for agent-core, as a prefix for
// the derived images (the exporter's tag is the pin plus a recipe digest).
const mirrorImagePrefix = "ghcr.io/mesh-intelligence/cohere-demo/"

// verifyMirrorTags walks the helm values files and returns one error naming
// every mirror image whose tag does not follow the pinned release.
func verifyMirrorTags(root string) error {
	tag, _, err := pinnedDeclarativeAgentsRelease(root)
	if err != nil {
		return err
	}
	var problems []string
	err = filepath.WalkDir(filepath.Join(root, "helm"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(content, &doc); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		for _, mismatch := range mirrorTagMismatches(&doc, tag) {
			problems = append(problems, fmt.Sprintf("%s: %s", filepath.ToSlash(rel), mismatch))
		}
		return nil
	})
	if err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return fmt.Errorf("deps:verify: mirror images do not follow the pinned release %s:\n  %s",
			tag, strings.Join(problems, "\n  "))
	}
	return nil
}

// mirrorTagMismatches walks every mapping that names a mirror repository and a
// tag, wherever it sits in the values tree.
func mirrorTagMismatches(node *yaml.Node, tag string) []string {
	var mismatches []string
	if node.Kind == yaml.MappingNode {
		repository, imageTag := mirrorScalarValue(node, "repository"), mirrorScalarValue(node, "tag")
		if strings.HasPrefix(repository, mirrorImagePrefix) && imageTag != "" {
			if strings.TrimPrefix(repository, mirrorImagePrefix) == "agent-core" {
				if imageTag != tag {
					mismatches = append(mismatches, fmt.Sprintf("image %s has tag %q, want %s", repository, imageTag, tag))
				}
			} else if imageTag != tag && !strings.HasPrefix(imageTag, tag+"-") {
				mismatches = append(mismatches, fmt.Sprintf("image %s has tag %q, want %s or %s-<digest>", repository, imageTag, tag, tag))
			}
		}
	}
	for _, child := range node.Content {
		mismatches = append(mismatches, mirrorTagMismatches(child, tag)...)
	}
	return mismatches
}

func mirrorScalarValue(mapping *yaml.Node, key string) string {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key && mapping.Content[i+1].Kind == yaml.ScalarNode {
			return mapping.Content[i+1].Value
		}
	}
	return ""
}
