// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/magefile/mage/mg"
)

// The runtime image is compiled and published privately by declarative-agents.
// The GH-928 decision publishes the binary and never the source: runtime:mirror
// derives a public image from the pinned private release, adds the /licenses
// layer a binary redistribution owes (declarative-agents is dual-licensed:
// Nokia-origin files BSD-3-Clause, post-fork files MIT), and pushes it under
// this repository's own namespace at the pinned tag. Only demo-pinned tags are
// ever published, and declarative-agents' own packages never change
// visibility.
const (
	runtimeMirrorRepository = "ghcr.io/mesh-intelligence/cohere-demo/agent-core"
	runtimeMirrorDockerfile = "agent-core-public.Dockerfile"
	// runtimePrivateSource is the private release image the mirror derives
	// from; everything else in this repository builds over the mirror.
	runtimePrivateSource = "ghcr.io/mesh-intelligence/declarative-agents/agent-core"
)

// runtimeLicenseFiles maps each tracked copy under images/licenses/ to its
// path in the declarative-agents repository at the pinned commit.
var runtimeLicenseFiles = map[string]string{
	"LICENSE":          "LICENSE",
	"nokia-origin.txt": "docs/provenance/nokia-origin.txt",
}

// Runtime groups the targets that publish the public runtime image.
type Runtime mg.Namespace

// Mirror derives the public runtime image from the pinned private agent-core
// release -- images/agent-core-public.Dockerfile adds the /licenses layer --
// and pushes one linux/amd64+linux/arm64 index to
// ghcr.io/mesh-intelligence/cohere-demo/agent-core at the pinned release tag.
// A tag already published is left alone. Pushing needs a docker login to
// ghcr.io with write:packages; the verb reads no credential itself.
func (Runtime) Mirror() error {
	root, err := cohereApplicationRoot()
	if err != nil {
		return err
	}
	return publishRuntimeMirror(exporterDockerRun, root)
}

// Licenses rewrites the tracked copies under images/licenses/ from the pinned
// declarative-agents commit, for the pin bump that moves them.
func (Runtime) Licenses() error {
	root, err := cohereApplicationRoot()
	if err != nil {
		return err
	}
	_, commit, err := pinnedDeclarativeAgentsRelease(root)
	if err != nil {
		return err
	}
	repo, err := runtimeCoreRepository(root)
	if err != nil {
		return err
	}
	for name, source := range runtimeLicenseFiles {
		content, err := runtimeGitShow(repo, commit, source)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, "images", "licenses", name), content, 0o644); err != nil {
			return err
		}
		fmt.Printf("runtime:licenses: wrote images/licenses/%s from %s:%s\n", name, commit, source)
	}
	return nil
}

func publishRuntimeMirror(docker exporterRunner, root string) error {
	tag, _, err := pinnedDeclarativeAgentsRelease(root)
	if err != nil {
		return err
	}
	if err := runtimeLicensesCurrent(root); err != nil {
		return err
	}
	image := runtimeMirrorRepository + ":" + tag
	if out, err := docker("buildx", "imagetools", "inspect", image); err == nil {
		if missing := missingPlatforms(string(out)); len(missing) == 0 {
			fmt.Printf("runtime:mirror: %s is already published\n", image)
			return nil
		}
	}
	if _, err := docker("buildx", "inspect", exporterPublishBuilder); err != nil {
		if out, err := docker("buildx", "create", "--name", exporterPublishBuilder,
			"--driver", "docker-container"); err != nil {
			return fmt.Errorf("create buildx builder %s: %w: %s", exporterPublishBuilder, err, tailOfOutput(string(out)))
		}
	}
	// The context is the images directory: the recipe copies images/licenses/.
	if out, err := docker("buildx", "build", "--builder", exporterPublishBuilder,
		"--platform", strings.Join(exporterPublishPlatforms, ","),
		"-f", filepath.Join(root, "images", runtimeMirrorDockerfile),
		"--build-arg", "RUNTIME_IMAGE="+runtimePrivateSource+":"+tag,
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
	fmt.Printf("runtime:mirror: published %s for %s\n", image, strings.Join(exporterPublishPlatforms, ", "))
	return nil
}

// runtimeLicensesCurrent compares the tracked copies under images/licenses/
// with the pinned commit's files when a declarative-agents git checkout is
// reachable, so the published layer never carries a stale licence. Without a
// reachable checkout -- a module-cache resolution, or no sibling -- the
// tracked copies are taken as written.
func runtimeLicensesCurrent(root string) error {
	_, commit, err := pinnedDeclarativeAgentsRelease(root)
	if err != nil {
		return err
	}
	repo, err := runtimeCoreRepository(root)
	if err != nil {
		return nil
	}
	for name, source := range runtimeLicenseFiles {
		want, err := runtimeGitShow(repo, commit, source)
		if err != nil {
			return nil
		}
		got, err := os.ReadFile(filepath.Join(root, "images", "licenses", name))
		if err != nil {
			return fmt.Errorf("runtime:mirror: %w; run mage runtime:licenses", err)
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("runtime:mirror: images/licenses/%s differs from the pinned %s:%s;"+
				" run mage runtime:licenses", name, commit, source)
		}
	}
	return nil
}

// runtimeCoreRepository locates the declarative-agents git repository behind
// the resolved agent-core directory.
func runtimeCoreRepository(root string) (string, error) {
	coreRoot := demoCoreRoot(root)
	repo := filepath.Dir(coreRoot)
	if out, err := exec.Command("git", "-C", repo, "rev-parse", "--git-dir").CombinedOutput(); err != nil {
		return "", fmt.Errorf("%s is not a git checkout: %s", repo, tailOfOutput(string(out)))
	}
	return repo, nil
}

func runtimeGitShow(repo, commit, path string) ([]byte, error) {
	out, err := exec.Command("git", "-C", repo, "show", commit+":"+path).Output()
	if err != nil {
		return nil, fmt.Errorf("git -C %s show %s:%s: %w", repo, commit, path, err)
	}
	return out, nil
}
