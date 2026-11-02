// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSnapshotModuleFilesRestoresAFailedPin is GH-744: a pin that fails part
// way must leave go.mod and go.sum as they were, not at the versions the
// failed go get calls left behind.
func TestSnapshotModuleFilesRestoresAFailedPin(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example\n\nrequire a v1.0.0\n")
	write("go.sum", "a v1.0.0 h1:x\n")

	restore, err := snapshotModuleFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	write("go.mod", "module example\n\nrequire a v0.9.0\n")
	write("go.sum", "a v0.9.0 h1:y\n")
	if err := restore(); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		"go.mod": "module example\n\nrequire a v1.0.0\n",
		"go.sum": "a v1.0.0 h1:x\n",
	} {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s after restore = %q, want %q", name, got, want)
		}
	}
}

func TestSnapshotModuleFilesRequiresBothFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotModuleFiles(root); err == nil {
		t.Fatal("snapshot without go.sum succeeded; a restore could not put it back")
	}
}

// The release comment deps:pin records is what names the pinned release, and
// the kit test holds the UIs to it.
func TestPinnedDeclarativeAgentsReleaseReadsTheGoModComment(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(
		"module example.com/app\n\n// declarative-agents v0.20260929.1 (d05063b49e79); the shipped UIs are built\nrequire x v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tag, commit, err := pinnedDeclarativeAgentsRelease(root)
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.20260929.1" || commit != "d05063b49e79" {
		t.Errorf("pin = %s (%s), want v0.20260929.1 (d05063b49e79)", tag, commit)
	}

	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := pinnedDeclarativeAgentsRelease(root); err == nil {
		t.Error("a go.mod with no release comment named a pin")
	}
}
