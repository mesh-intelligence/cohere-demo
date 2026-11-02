// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// catalogRootForTest resolves the catalog checkout the way the mage targets do
// — through resolveCatalogRoot, which reads catalog_root from demo.yaml. Tests
// run with the working directory at magefiles/, so the application root is its
// parent. This replaces hardcoded ../../catalog test paths: those assumed the
// monorepo layout (where the catalog is a sibling under applications/) and
// resolve to a nonexistent directory once the application is copied out beside
// a sibling declarative-agents checkout.
func catalogRootForTest(t *testing.T) string {
	t.Helper()
	root, err := resolveCatalogRoot("cohere-demo test catalog resolution", filepath.Clean(".."))
	if err != nil {
		t.Fatalf("resolve catalog root from demo.yaml: %v", err)
	}
	return root
}

// catalogAgentDir returns the catalog directory for one agent (for example
// "applier"), resolved through catalogRootForTest.
func catalogAgentDir(t *testing.T, agent string) string {
	t.Helper()
	return filepath.Join(catalogRootForTest(t), "agents", agent)
}

// coreRootForTest resolves the agent-core checkout the way the mage targets do,
// through demoCoreRoot reading core_root from demo.yaml. It replaces hardcoded
// ../../../agent-core test paths that assumed the monorepo layout.
func coreRootForTest(t *testing.T) string {
	t.Helper()
	return demoCoreRoot(filepath.Clean(".."))
}

// resolveUndoContractPath rewrites a monorepo-relative declaration path from a
// test data table to the sibling-checkout location. App-local paths pass
// through unchanged.
func resolveUndoContractPath(t *testing.T, file string) string {
	t.Helper()
	switch {
	case strings.HasPrefix(file, "../../catalog/"):
		return filepath.Join(catalogRootForTest(t), strings.TrimPrefix(file, "../../catalog/"))
	case strings.HasPrefix(file, "../../../agent-core/"):
		return filepath.Join(coreRootForTest(t), strings.TrimPrefix(file, "../../../agent-core/"))
	default:
		return file
	}
}
