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
	"strings"

	"github.com/magefile/mage/mg"
)

// The brief exporter (srd013) is the one image this repository builds that
// no other repository publishes. demo:up builds it locally for kind;
// exporter:publish builds it for both node architectures and pushes it to
// ghcr.io, so a cluster pulls the published artifact (#869). GKE pulls it
// through the environment's ghcr.io remote repository like agent-core.
const (
	exporterPublishedRepository = "ghcr.io/mesh-intelligence/cohere-demo/exporter"
	// exporterGhcrPath is the published repository's path on ghcr.io, as the
	// ghcr.io remote repository serves it.
	exporterGhcrPath       = "mesh-intelligence/cohere-demo/exporter"
	exporterRuntimeSource  = "ghcr.io/mesh-intelligence/declarative-agents/agent-core"
	exporterPublishBuilder = "cohere-demo-publish"
)

// exporterPublishPlatforms are the node architectures the published index
// carries: GKE nodes are amd64, Apple Silicon kind nodes arm64.
var exporterPublishPlatforms = []string{"linux/amd64", "linux/arm64"}

// Exporter publishes the brief exporter image.
type Exporter mg.Namespace

// exporterReleaseTag names the exporter image by what it is built from: the
// agent-core release it extends (helm/values.yaml image.tag, which
// deps:verify pins) and a digest of the recipe. A pin bump or a recipe change
// gives a new tag, and the same inputs always give the same one, so publish
// and deploy agree without a hand-maintained version.
func exporterReleaseTag(root string) (string, error) {
	runtimeTag, err := gcpRuntimeTag(filepath.Join(root, "helm", "values.yaml"))
	if err != nil {
		return "", err
	}
	recipe, err := os.ReadFile(filepath.Join(root, "images", "exporter.Dockerfile"))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(recipe)
	return runtimeTag + "-" + hex.EncodeToString(sum[:])[:12], nil
}

// Publish builds images/exporter.Dockerfile over the pinned agent-core
// release for linux/amd64 and linux/arm64 and pushes one index to
// ghcr.io/mesh-intelligence/cohere-demo/exporter at its release tag. A tag
// already published is left alone. Pushing needs a docker login to ghcr.io
// with write:packages; the verb reads no credential itself.
func (Exporter) Publish() error {
	root, err := cohereApplicationRoot()
	if err != nil {
		return err
	}
	return publishExporter(exporterDockerRun, root)
}

// exporterRunner runs one docker command and returns its combined output;
// tests substitute a recorder.
type exporterRunner func(args ...string) ([]byte, error)

func exporterDockerRun(args ...string) ([]byte, error) {
	return exec.Command("docker", args...).CombinedOutput()
}

func publishExporter(docker exporterRunner, root string) error {
	tag, err := exporterReleaseTag(root)
	if err != nil {
		return err
	}
	runtimeTag, err := gcpRuntimeTag(filepath.Join(root, "helm", "values.yaml"))
	if err != nil {
		return err
	}
	image := exporterPublishedRepository + ":" + tag
	if out, err := docker("buildx", "imagetools", "inspect", image); err == nil {
		if missing := missingPlatforms(string(out)); len(missing) == 0 {
			fmt.Printf("exporter:publish: %s is already published\n", image)
			return nil
		}
	}
	if _, err := docker("buildx", "inspect", exporterPublishBuilder); err != nil {
		if out, err := docker("buildx", "create", "--name", exporterPublishBuilder,
			"--driver", "docker-container"); err != nil {
			return fmt.Errorf("create buildx builder %s: %w: %s", exporterPublishBuilder, err, tailOfOutput(string(out)))
		}
	}
	// The context is the images directory alone, as demo:up builds it: the
	// recipe copies nothing from context.
	if out, err := docker("buildx", "build", "--builder", exporterPublishBuilder,
		"--platform", strings.Join(exporterPublishPlatforms, ","),
		"-f", filepath.Join(root, "images", "exporter.Dockerfile"),
		"--build-arg", "RUNTIME_IMAGE="+exporterRuntimeSource+":"+runtimeTag,
		"-t", image, "--push", filepath.Join(root, "images")); err != nil {
		return fmt.Errorf("build and push %s: %w: %s", image, err, tailOfOutput(string(out)))
	}
	out, err := docker("buildx", "imagetools", "inspect", image)
	if err != nil {
		return fmt.Errorf("inspect %s after push: %w: %s", image, err, tailOfOutput(string(out)))
	}
	if missing := missingPlatforms(string(out)); len(missing) > 0 {
		return fmt.Errorf("published %s lacks %s", image, strings.Join(missing, ", "))
	}
	fmt.Printf("exporter:publish: published %s for %s\n", image, strings.Join(exporterPublishPlatforms, ", "))
	return nil
}

// missingPlatforms names the publish platforms an imagetools inspect listing
// does not carry.
func missingPlatforms(listing string) []string {
	present := map[string]bool{}
	for _, line := range strings.Split(listing, "\n") {
		if _, value, found := strings.Cut(line, "Platform:"); found {
			present[strings.TrimSpace(value)] = true
		}
	}
	var missing []string
	for _, platform := range exporterPublishPlatforms {
		if !present[platform] {
			missing = append(missing, platform)
		}
	}
	return missing
}
