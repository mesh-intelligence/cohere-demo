// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// exporterTestRoot is a repository root with the two inputs the exporter tag
// derives from.
func exporterTestRoot(t *testing.T, runtimeTag, recipe string) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"helm", "images"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writePackageTestFile(t, filepath.Join(root, "helm", "values.yaml"), "image:\n  tag: "+runtimeTag+"\n")
	writePackageTestFile(t, filepath.Join(root, "images", "exporter.Dockerfile"), recipe)
	return root
}

// The exporter tag names what the image is built from: the agent-core
// release and the recipe. Either changing gives a new tag; the same inputs
// give the same one.
func TestExporterReleaseTagFollowsReleaseAndRecipe(t *testing.T) {
	shape := regexp.MustCompile(`^v0\.20261003\.0-[0-9a-f]{12}$`)
	base := exporterTestRoot(t, "v0.20261003.0", "FROM x\n")
	tag, err := exporterReleaseTag(base)
	if err != nil || !shape.MatchString(tag) {
		t.Fatalf("tag = %q, %v", tag, err)
	}
	again, _ := exporterReleaseTag(exporterTestRoot(t, "v0.20261003.0", "FROM x\n"))
	recipe, _ := exporterReleaseTag(exporterTestRoot(t, "v0.20261003.0", "FROM x\nRUN true\n"))
	pin, _ := exporterReleaseTag(exporterTestRoot(t, "v0.20261004.0", "FROM x\n"))
	if again != tag || recipe == tag || pin == tag || !strings.HasPrefix(pin, "v0.20261004.0-") {
		t.Errorf("tags: same=%s recipe=%s pin=%s base=%s", again, recipe, pin, tag)
	}
	if _, err := exporterReleaseTag(exporterTestRoot(t, "2a261fc99773", "FROM x\n")); err == nil {
		t.Error("a non-release agent-core tag produced an exporter tag")
	}
}

const twoPlatforms = "Name: ghcr.io/x\nManifests:\n  Name: a\n  Platform:    linux/amd64\n  Name: b\n  Platform:    linux/arm64\n"

// Publish builds both platforms over the pinned agent-core release and
// pushes; an already published tag is left alone; a push that lacks a
// platform fails.
func TestPublishExporterBuildsBothPlatformsOverTheRelease(t *testing.T) {
	root := exporterTestRoot(t, "v0.20261003.0", "FROM x\n")
	tag, _ := exporterReleaseTag(root)
	image := exporterPublishedRepository + ":" + tag

	var calls []string
	published := false
	docker := func(args ...string) ([]byte, error) {
		line := strings.Join(args, " ")
		calls = append(calls, line)
		switch {
		case strings.HasPrefix(line, "buildx imagetools inspect"):
			if !published {
				return []byte("ERROR: not found"), errors.New("exit status 1")
			}
			return []byte(twoPlatforms), nil
		case strings.HasPrefix(line, "buildx inspect"):
			return nil, errors.New("no builder")
		case strings.HasPrefix(line, "buildx build"):
			published = true
		}
		return nil, nil
	}
	if err := publishExporter(docker, root); err != nil {
		t.Fatal(err)
	}
	var build string
	for _, call := range calls {
		if strings.HasPrefix(call, "buildx build") {
			build = call
		}
	}
	for _, want := range []string{
		"--platform linux/amd64,linux/arm64", "--push", "-t " + image,
		"RUNTIME_IMAGE=ghcr.io/mesh-intelligence/declarative-agents/agent-core:v0.20261003.0",
		"--builder " + exporterPublishBuilder,
	} {
		if !strings.Contains(build, want) {
			t.Errorf("build lacks %q: %s", want, build)
		}
	}
	if !strings.Contains(strings.Join(calls, "\n"), "buildx create --name "+exporterPublishBuilder) {
		t.Error("the multi-platform builder was not created when absent")
	}

	calls = nil
	if err := publishExporter(docker, root); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "buildx build") {
			t.Error("an already published tag was rebuilt")
		}
	}

	oneArch := func(args ...string) ([]byte, error) {
		if strings.HasPrefix(strings.Join(args, " "), "buildx imagetools inspect") {
			return []byte("Platform: linux/arm64\n"), nil
		}
		return nil, nil
	}
	if err := publishExporter(oneArch, root); err == nil || !strings.Contains(err.Error(), "linux/amd64") {
		t.Errorf("a single-platform push = %v", err)
	}
}

// A deploy asks the ghcr.io remote repository for the exporter's manifest
// and refuses, naming exporter:publish, when it is not there.
func TestRequireExporterPublishedNamesTheVerb(t *testing.T) {
	registries := gcpRegistries{Ghcr: "us-central1-docker.pkg.dev/p/cohere-demo-ghcr", Dockerhub: "d"}
	var asked string
	found := func(url string) (int, error) { asked = url; return http.StatusOK, nil }
	if err := requireExporterPublished(found, registries, "v0.1-abc"); err != nil {
		t.Fatal(err)
	}
	want := "https://us-central1-docker.pkg.dev/v2/p/cohere-demo-ghcr/mesh-intelligence/cohere-demo/exporter/manifests/v0.1-abc"
	if asked != want {
		t.Errorf("asked %s, want %s", asked, want)
	}
	missing := func(string) (int, error) { return http.StatusNotFound, nil }
	if err := requireExporterPublished(missing, registries, "v0.1-abc"); err == nil ||
		!strings.Contains(err.Error(), "mage exporter:publish") {
		t.Errorf("an unpublished exporter = %v", err)
	}
}
