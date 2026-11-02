// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The property that makes the fix work is that two checkouts never name the
// same cache directory. Everything else about GH-179 follows from the sharing:
// same directory, shared entries, //nolint directives resolved against another
// checkout's paths.

func TestScopedLintCacheDiffersPerCheckout(t *testing.T) {
	main, err := scopedLintCacheDir("/Users/someone/GITHUB/cohere-demo")
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := scopedLintCacheDir("/Users/someone/GITHUB/gh-179-scoped-lint-cache")
	if err != nil {
		t.Fatal(err)
	}
	if main == worktree {
		t.Fatalf("a checkout and a worktree share the cache directory %s;"+
			" that sharing is what resolves //nolint directives against another"+
			" checkout's paths (GH-179)", main)
	}
}

func TestScopedLintCacheIsStableForOneCheckout(t *testing.T) {
	// A directory that changed between runs would be a cold cache every time,
	// which is the cost this scoping exists to avoid paying.
	first, err := scopedLintCacheDir("/Users/someone/GITHUB/cohere-demo")
	if err != nil {
		t.Fatal(err)
	}
	second, err := scopedLintCacheDir("/Users/someone/GITHUB/cohere-demo")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("the same checkout named %s then %s", first, second)
	}
}

func TestScopedLintCacheNormalizesEquivalentPaths(t *testing.T) {
	// One checkout reached by two spellings is one checkout, and giving it two
	// caches would halve the hit rate for no reason.
	plain, err := scopedLintCacheDir("/Users/someone/GITHUB/cohere-demo")
	if err != nil {
		t.Fatal(err)
	}
	noisy, err := scopedLintCacheDir("/Users/someone/GITHUB/./cohere-demo/")
	if err != nil {
		t.Fatal(err)
	}
	if plain != noisy {
		t.Errorf("equivalent paths named %s and %s", plain, noisy)
	}
}

func TestScopedLintCacheSitsOutsideTheCheckout(t *testing.T) {
	// A cache inside the checkout would need a gitignore entry, and a missing
	// one would put analysis output in a commit.
	root := "/Users/someone/GITHUB/cohere-demo"
	cache, err := scopedLintCacheDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(cache, root+string(filepath.Separator)) {
		t.Errorf("cache %s sits inside the checkout %s", cache, root)
	}
	if !strings.HasPrefix(cache, filepath.Clean(os.TempDir())) {
		t.Errorf("cache %s is not under the temporary directory %s", cache, os.TempDir())
	}
}
