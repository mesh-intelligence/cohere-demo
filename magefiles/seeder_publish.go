// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/magefile/mage/mg"
)

// The seeder is a binary redistribution like the runtime mirror: compiled from
// the private modules on an org-access machine, published for anyone to run.
// Its tag names what it carries -- the pinned release plus a digest of the
// recipe and the baked case content -- so a store rebuild or a recipe change
// gives a new tag; a code-only change to the seed path does not, and wants an
// explicit republish after deleting the stale tag.
const (
	seederPublishedRepository = "ghcr.io/mesh-intelligence/cohere-demo/seeder"
	seederDockerfile          = "seeder.Dockerfile"
	seederBakedCase           = "investment-research"
)

// Seeder groups the targets that publish the public seeder image.
type Seeder mg.Namespace

// Publish compiles the magefiles binary for linux/amd64 and linux/arm64,
// stages it with the publishable case store and the licence notices, and
// pushes one index to ghcr.io/mesh-intelligence/cohere-demo/seeder at the
// content-derived tag. A tag already published is left alone. Pushing needs a
// docker login to ghcr.io with write:packages; the verb reads no credential
// itself.
func (Seeder) Publish() error {
	root, err := cohereApplicationRoot()
	if err != nil {
		return err
	}
	return publishSeeder(exporterDockerRun, root)
}

// seederReleaseTag derives the published tag from the pin, the recipe, and the
// baked case content.
func seederReleaseTag(root string) (string, error) {
	tag, _, err := pinnedDeclarativeAgentsRelease(root)
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	recipe, err := os.ReadFile(filepath.Join(root, "images", seederDockerfile))
	if err != nil {
		return "", err
	}
	sum.Write(recipe)
	caseCorpus := filepath.Join(root, corpusCasesDir, seederBakedCase, "corpus")
	if err := hashDirectoryContent(sum, caseCorpus); err != nil {
		return "", err
	}
	return tag + "-" + hex.EncodeToString(sum.Sum(nil))[:12], nil
}

// hashDirectoryContent feeds every regular file under root into the digest in
// path order, name then content, so the digest follows the staged bytes.
func hashDirectoryContent(sum io.Writer, root string) error {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, path := range files {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(sum, filepath.ToSlash(relative)+"\n"); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(sum, file)
		_ = file.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func publishSeeder(docker exporterRunner, root string) error {
	tag, err := seederReleaseTag(root)
	if err != nil {
		return err
	}
	image := seederPublishedRepository + ":" + tag
	if out, err := docker("buildx", "imagetools", "inspect", image); err == nil {
		if missing := missingPlatforms(string(out)); len(missing) == 0 {
			fmt.Printf("seeder:publish: %s is already published\n", image)
			return nil
		}
	}
	context, err := stageSeederContext(root)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(context) }()
	if _, err := docker("buildx", "inspect", exporterPublishBuilder); err != nil {
		if out, err := docker("buildx", "create", "--name", exporterPublishBuilder,
			"--driver", "docker-container"); err != nil {
			return fmt.Errorf("create buildx builder %s: %w: %s", exporterPublishBuilder, err, tailOfOutput(string(out)))
		}
	}
	if out, err := docker("buildx", "build", "--builder", exporterPublishBuilder,
		"--platform", strings.Join(exporterPublishPlatforms, ","),
		"-f", filepath.Join(root, "images", seederDockerfile),
		"-t", image, "--push", context); err != nil {
		return fmt.Errorf("build and push %s: %w: %s", image, err, tailOfOutput(string(out)))
	}
	out, err := docker("buildx", "imagetools", "inspect", image)
	if err != nil {
		return fmt.Errorf("inspect %s after push: %w: %s", image, err, tailOfOutput(string(out)))
	}
	if missing := missingPlatforms(string(out)); len(missing) > 0 {
		return fmt.Errorf("published %s lacks %s", image, strings.Join(missing, ", "))
	}
	fmt.Printf("seeder:publish: published %s for %s\n", image, strings.Join(exporterPublishPlatforms, ", "))
	return nil
}

// stageSeederContext assembles the build context: the cross-compiled binaries,
// the baked case's corpus tree, and the licence notices. Nothing else enters
// the context, which is what keeps Go source and the keep-private store
// families out of the published artifact.
func stageSeederContext(root string) (string, error) {
	context, err := os.MkdirTemp("", "cohere-demo-seeder-*")
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) {
		_ = os.RemoveAll(context)
		return "", err
	}
	for _, platform := range exporterPublishPlatforms {
		architecture := strings.TrimPrefix(platform, "linux/")
		binary := filepath.Join(context, "seeder-linux-"+architecture)
		// mage -compile takes the target platform as flags; GOOS/GOARCH in the
		// environment do not reach the build it runs.
		compile := exec.Command("mage", "-compile", binary, "-goos", "linux", "-goarch", architecture)
		compile.Dir = root
		compile.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
		if out, err := compile.CombinedOutput(); err != nil {
			return fail(fmt.Errorf("compile the seeder for %s: %w: %s", platform, err, tailOfOutput(string(out))))
		}
	}
	source := filepath.Join(root, corpusCasesDir, seederBakedCase, "corpus")
	if err := copyDirectoryTree(source, filepath.Join(context, "case", "corpus")); err != nil {
		return fail(err)
	}
	if err := copyDirectoryTree(filepath.Join(root, "images", "licenses"), filepath.Join(context, "licenses")); err != nil {
		return fail(err)
	}
	return context, nil
}

func copyDirectoryTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
