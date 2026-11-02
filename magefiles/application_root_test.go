// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestChatbotCompositionResolvesFromAWorktreeCheckout pins the upstream
// behaviour this repository depends on: the shared resolver takes the
// application identity from the manifest's application field rather than from
// the root directory's base name, so a checkout named for an issue -- which is
// what every gh-* worktree is -- resolves with nothing staged to give it
// another name.
//
// The repository carried a compositionRoot helper that staged such a name until
// upstream GH-1881 removed the check (pinned at v0.20260827.2). This test is
// what stops a later repin from reverting that silently: without it, a pin
// moving back before the fix would fail every composition caller from a
// worktree and the reason would be an upstream change nothing here reported.
// The link stands in for the worktree so the proof needs no second checkout.
func TestChatbotCompositionResolvesFromAWorktreeCheckout(t *testing.T) {
	applicationRoot, err := filepath.Abs(filepath.Clean(".."))
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(t.TempDir(), "gh-160-worktree-application-root")
	if err := os.Symlink(applicationRoot, worktree); err != nil {
		t.Fatal(err)
	}
	catalogRoot, err := resolveCatalogRoot("composition worktree test", applicationRoot)
	if err != nil {
		t.Fatal(err)
	}
	composition, err := resolveChatbotComposition(worktree, catalogRoot)
	if err != nil {
		t.Fatalf("resolve composition from %s: %v", worktree, err)
	}
	if composition.manifest.Application != chatbotApplicationName {
		t.Errorf("application = %q, want %q",
			composition.manifest.Application, chatbotApplicationName)
	}
	if len(composition.inventory.Files) == 0 {
		t.Error("composition inventory is empty")
	}
	// A checkout location that leaked into the closure would show up as an
	// absolute source, which is what keeps the packaged provenance reproducible
	// across the machines that build it.
	for _, file := range composition.inventory.Files {
		if filepath.IsAbs(file.Source) {
			t.Errorf("inventory source %q is absolute", file.Source)
		}
	}
}
