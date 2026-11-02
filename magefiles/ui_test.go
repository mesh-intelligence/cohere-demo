// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBundleTree materializes a bundle layout from a path-to-content map, so a
// comparison case reads as the two trees it is about.
func writeBundleTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCompareBundleTreesAcceptsAnIdenticalRebuild(t *testing.T) {
	files := map[string]string{
		"index.html":           "<!doctype html><script src=/assets/index-abc.js></script>",
		"assets/index-abc.js":  "console.log(1)",
		"assets/index-abc.css": ":root{--x:1}",
		"favicon.svg":          "<svg/>",
	}
	differences, err := compareBundleTrees(writeBundleTree(t, files), writeBundleTree(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 0 {
		t.Fatalf("identical trees reported differences: %v", differences)
	}
}

// The everyday case: sources changed, the bundle did not.
func TestCompareBundleTreesReportsChangedContent(t *testing.T) {
	committed := writeBundleTree(t, map[string]string{"assets/index-abc.js": "console.log(1)"})
	built := writeBundleTree(t, map[string]string{"assets/index-abc.js": "console.log(2)"})
	differences, err := compareBundleTrees(committed, built)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 1 || !strings.Contains(differences[0], "assets/index-abc.js differs") {
		t.Fatalf("differences = %v, want one naming assets/index-abc.js", differences)
	}
}

// Vite names assets by content hash, so a source change usually renames a file
// rather than editing one. Both halves of that rename must be reported.
func TestCompareBundleTreesReportsRenamedAssets(t *testing.T) {
	committed := writeBundleTree(t, map[string]string{"assets/index-old.js": "console.log(1)"})
	built := writeBundleTree(t, map[string]string{"assets/index-new.js": "console.log(2)"})
	differences, err := compareBundleTrees(committed, built)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 2 {
		t.Fatalf("differences = %v, want two", differences)
	}
	joined := strings.Join(differences, "\n")
	if !strings.Contains(joined, "assets/index-new.js is produced by a rebuild but is not committed") {
		t.Errorf("differences do not report the new asset: %v", differences)
	}
	if !strings.Contains(joined, "assets/index-old.js is committed but a rebuild does not produce it") {
		t.Errorf("differences do not report the stale asset: %v", differences)
	}
}

func TestCompareBundleTreesReportsEveryDifferenceInPathOrder(t *testing.T) {
	committed := writeBundleTree(t, map[string]string{
		"assets/a.js": "one",
		"assets/b.js": "two",
		"index.html":  "html",
	})
	built := writeBundleTree(t, map[string]string{
		"assets/a.js": "ONE",
		"assets/b.js": "TWO",
		"index.html":  "html",
	})
	differences, err := compareBundleTrees(committed, built)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 2 {
		t.Fatalf("differences = %v, want two", differences)
	}
	if !strings.HasPrefix(differences[0], "assets/a.js") || !strings.HasPrefix(differences[1], "assets/b.js") {
		t.Fatalf("differences are not in path order: %v", differences)
	}
}

func TestCompareBundleTreesFailsOnAnUnreadableTree(t *testing.T) {
	committed := writeBundleTree(t, map[string]string{"index.html": "html"})
	if _, err := compareBundleTrees(committed, filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("comparison against a missing rebuilt tree succeeded")
	}
}

func TestChatbotUISkipReasonNamesAMissingApplication(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "no-such-app")
	reason := chatbotUISkipReason(absent)
	if !strings.Contains(reason, "SPA directory not found") {
		t.Fatalf("skip reason = %q, want it to name the missing directory", reason)
	}
}

// The real SPA directory resolves from the application root, and with npm
// installed the checks have no reason to skip. This is what keeps a silent skip
// from passing for a passing gate on a developer machine.
func TestChatbotUISkipReasonIsEmptyForThisCheckout(t *testing.T) {
	app := chatbotUIAppDir(filepath.Clean(".."))
	if info, err := os.Stat(app); err != nil || !info.IsDir() {
		t.Fatalf("SPA directory not resolved from the application root: %s", app)
	}
	reason := chatbotUISkipReason(app)
	if reason != "" && !strings.Contains(reason, "npm not found") {
		t.Fatalf("skip reason = %q, want empty or an npm-not-installed reason", reason)
	}
}

func TestBundleFileSetListsNestedFilesBySlashPath(t *testing.T) {
	root := writeBundleTree(t, map[string]string{
		"index.html":            "html",
		"assets/nested/deep.js": "js",
	})
	files, err := bundleFileSet(root)
	if err != nil {
		t.Fatal(err)
	}
	if !files["assets/nested/deep.js"] || !files["index.html"] || len(files) != 2 {
		t.Fatalf("bundle file set = %v", files)
	}
}
