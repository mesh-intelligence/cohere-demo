// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// golangciLintCacheEnv is the directory golangci-lint keeps its analysis cache
// in. It defaults to one shared location per user, which is the problem this
// file exists to solve.
const golangciLintCacheEnv = "GOLANGCI_LINT_CACHE"

// lintWithScopedCache runs golangci-lint against a cache directory derived from
// this checkout's absolute path, rather than the one location every checkout on
// the machine otherwise shares.
//
// Sharing that cache silently breaks every //nolint directive in the
// repository. golangci-lint resolves a directive by re-reading the source file
// at the path its cached entry recorded; two checkouts holding the same package
// content share entries, so a run from one checkout can resolve directives
// against the other's paths. When the other checkout was a gh-* worktree that
// has since been removed, the read fails, no suppression applies, and every
// annotated line is reported -- against a path that no longer exists, with the
// explanation buried in a warning above the findings.
//
// The failure is reproducible in one cycle: lint inside a worktree, remove the
// worktree, lint from the main checkout. It reported nine findings, which is
// exactly the number of //nolint directives the repository carries. Pointing
// the two runs at separate cache directories reports none, and pointing the
// second run at the first's cache reproduces all nine, so the sharing is the
// cause rather than anything about the source (GH-179).
//
// This matters because `mage audit` runs lint, and this repository's workflow
// creates and removes a gh-* worktree per issue. The trigger is the ordinary
// path, and the failure accuses code that is correctly annotated.
//
// Cleaning the cache before each run would also work -- a cold run here costs
// about a second -- but `golangci-lint cache clean` empties the cache every
// other Go project on the machine is using. Scoping leaves them alone.
//
// It does mean this target no longer delegates to the orchestrator's
// Builder.Lint, which runs the same command without the scoping. The fix
// belongs upstream in cobbler-scaffold; until it lands there, a red gate here
// is the more pressing problem.
func lintWithScopedCache() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	cache, err := scopedLintCacheDir(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return fmt.Errorf("create lint cache directory %s: %w", cache, err)
	}

	fmt.Printf("lint: running golangci-lint with %s=%s\n", golangciLintCacheEnv, cache)
	cmd := exec.Command("golangci-lint", "run", "./...")
	cmd.Dir = root
	// Passed on the child rather than set on this process, which is what the
	// forbidigo rule in .golangci.yml asks for and what keeps concurrent mage
	// targets from reading each other's value.
	cmd.Env = append(os.Environ(), golangciLintCacheEnv+"="+cache)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("golangci-lint: %w", err)
	}
	fmt.Println("lint: done")
	return nil
}

// scopedLintCacheDir names a cache directory unique to one checkout. The digest
// of the absolute path keys it, so two checkouts never collide and one checkout
// keeps a warm cache across runs; the temporary directory holds it, so no
// checkout grows an untracked directory that has to be ignored.
func scopedLintCacheDir(root string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve checkout path %s: %w", root, err)
	}
	digest := sha256.Sum256([]byte(absolute))
	return filepath.Join(os.TempDir(),
		"cohere-demo-golangci-cache-"+hex.EncodeToString(digest[:])[:16]), nil
}
