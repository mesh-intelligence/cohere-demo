// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runtimeTestRoot is a repository root with the inputs the mirror reads: a
// go.mod release comment and the tracked licence copies. No core checkout is
// reachable from a temp dir, so the licence currency check takes the copies
// as written.
func runtimeTestRoot(t *testing.T, tag string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "images", "licenses"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePackageTestFile(t, filepath.Join(root, "go.mod"),
		"module example\n\n// declarative-agents "+tag+" (0123456789ab)\nrequire ()\n")
	writePackageTestFile(t, filepath.Join(root, "images", "licenses", "LICENSE"), "dual licence\n")
	writePackageTestFile(t, filepath.Join(root, "images", "licenses", "nokia-origin.txt"), "files\n")
	writePackageTestFile(t, filepath.Join(root, "images", runtimeMirrorDockerfile), "ARG RUNTIME_IMAGE\nFROM x\n")
	return root
}

// Mirror publishes the pinned tag under the demo namespace, built over the
// pinned private release with the images directory as context; an already
// published tag is left alone; a push that lacks a platform fails.
func TestPublishRuntimeMirrorBuildsThePinnedTag(t *testing.T) {
	root := runtimeTestRoot(t, "v0.20261003.0")
	image := runtimeMirrorRepository + ":v0.20261003.0"

	var calls [][]string
	published := false
	docker := func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		switch {
		case args[0] == "buildx" && args[1] == "imagetools":
			if published {
				return []byte(twoPlatforms), nil
			}
			return nil, os.ErrNotExist
		case args[0] == "buildx" && args[1] == "inspect":
			return []byte("ok"), nil
		case args[0] == "buildx" && args[1] == "build":
			published = true
			return []byte("pushed"), nil
		}
		return nil, os.ErrInvalid
	}
	if err := publishRuntimeMirror(docker, root); err != nil {
		t.Fatalf("publish: %v", err)
	}
	var build []string
	for _, call := range calls {
		if call[0] == "buildx" && call[1] == "build" {
			build = call
		}
	}
	joined := strings.Join(build, " ")
	for _, want := range []string{
		"--platform linux/amd64,linux/arm64",
		"-t " + image,
		"RUNTIME_IMAGE=" + runtimePrivateSource + ":v0.20261003.0",
		filepath.Join(root, "images", runtimeMirrorDockerfile),
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("build call lacks %q: %s", want, joined)
		}
	}
	if !strings.HasSuffix(joined, filepath.Join(root, "images")) {
		t.Errorf("build context is not the images directory: %s", joined)
	}

	// A second publish of the same tag is left alone.
	calls = nil
	if err := publishRuntimeMirror(docker, root); err != nil {
		t.Fatalf("republish: %v", err)
	}
	for _, call := range calls {
		if call[0] == "buildx" && call[1] == "build" {
			t.Error("an already published tag was rebuilt")
		}
	}
}

// A push whose index lacks a platform fails rather than publishing a partial
// mirror.
func TestPublishRuntimeMirrorRequiresBothPlatforms(t *testing.T) {
	root := runtimeTestRoot(t, "v0.20261003.0")
	docker := func(args ...string) ([]byte, error) {
		switch {
		case args[0] == "buildx" && args[1] == "imagetools":
			return []byte("Platform:    linux/amd64\n"), nil
		case args[0] == "buildx" && args[1] == "inspect":
			return []byte("ok"), nil
		case args[0] == "buildx" && args[1] == "build":
			return []byte("pushed"), nil
		}
		return nil, os.ErrInvalid
	}
	err := publishRuntimeMirror(docker, root)
	if err == nil || !strings.Contains(err.Error(), "linux/arm64") {
		t.Fatalf("err = %v, want the missing platform named", err)
	}
}

// A missing go.mod release comment is the error deps:pin repairs, not a build.
func TestPublishRuntimeMirrorRequiresTheReleaseComment(t *testing.T) {
	root := runtimeTestRoot(t, "v0.20261003.0")
	writePackageTestFile(t, filepath.Join(root, "go.mod"), "module example\n")
	err := publishRuntimeMirror(func(args ...string) ([]byte, error) {
		t.Errorf("docker ran: %v", args)
		return nil, os.ErrInvalid
	}, root)
	if err == nil || !strings.Contains(err.Error(), "deps:pin") {
		t.Fatalf("err = %v, want the deps:pin remedy", err)
	}
}

// The mirror check holds every chart image under the demo namespace to the
// pinned release: agent-core exactly, derived images by prefix.
func TestVerifyMirrorTagsHoldsTheChartToThePin(t *testing.T) {
	root := runtimeTestRoot(t, "v0.20261003.0")
	helm := filepath.Join(root, "helm")
	writePackageTestFile(t, filepath.Join(helm, "values.yaml"),
		"image:\n  repository: "+runtimeMirrorRepository+"\n  tag: v0.20261003.0\n"+
			"exporter:\n  image:\n    repository: "+exporterPublishedRepository+"\n    tag: v0.20261003.0-abcdef123456\n"+
			"other:\n  image: {repository: chromadb/chroma, tag: \"1.5.3\"}\n")
	if err := verifyMirrorTags(root); err != nil {
		t.Fatalf("aligned values failed: %v", err)
	}

	writePackageTestFile(t, filepath.Join(helm, "values.yaml"),
		"image:\n  repository: "+runtimeMirrorRepository+"\n  tag: v0.20261001.0\n")
	err := verifyMirrorTags(root)
	if err == nil || !strings.Contains(err.Error(), "v0.20261001.0") || !strings.Contains(err.Error(), "want v0.20261003.0") {
		t.Fatalf("err = %v, want the drifted agent-core tag named", err)
	}

	writePackageTestFile(t, filepath.Join(helm, "values.yaml"),
		"exporter:\n  image:\n    repository: "+exporterPublishedRepository+"\n    tag: v0.20261001.0-abcdef123456\n")
	if err := verifyMirrorTags(root); err == nil {
		t.Fatal("a derived image on another release passed")
	}
}

// Licence copies that drifted from the pin refuse the mirror when a checkout
// is reachable to compare against; the test repo's own sibling serves as one.
func TestRuntimeLicensesCurrentAcceptsAbsentCheckout(t *testing.T) {
	root := runtimeTestRoot(t, "v0.20261003.0")
	if err := runtimeLicensesCurrent(root); err != nil {
		t.Fatalf("no reachable checkout should pass: %v", err)
	}
}
