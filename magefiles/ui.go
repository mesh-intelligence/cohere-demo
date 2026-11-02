// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/magefile/mage/mg"
)

// The chatbot SPA ships as a built bundle: agents/chatbot/ui/app/dist is
// committed and agents/application.yaml stages it into the chart, so that
// directory is what a pod serves. Nothing connected the bundle to the sources
// it came from, and the gap was not theoretical. From the tree's vendoring in
// b2b60b3 until GH-64, the SPA could not be built here at all -- App.css
// imported the catalog design tokens by a monorepo-relative path that resolved
// against nothing once the application was copied out beside a sibling
// declarative-agents checkout -- and nobody noticed, because the committed
// bundle was the working artifact built before the copy.
//
// A stale bundle has the same signature: the demo serves old behavior, every
// test passes, and the diff looks complete. So the check rebuilds and compares
// rather than hashing the sources. Hashing detects a source edit that was never
// rebuilt; only running the build catches a build that cannot run.
//
// The vite build is byte-deterministic -- two builds of identical sources
// produce identical files, content-hashed asset names included -- which is what
// makes a byte comparison the right assertion rather than an approximation.

type UI mg.Namespace

const (
	chatbotUIAppPath = "agents/chatbot/ui/app"
	chatbotUIDistDir = "dist"
)

// Test runs the SPA's vitest suite, installing any declared dependency the
// checkout is missing first.
func (UI) Test() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	app := chatbotUIAppDir(root)
	if reason := chatbotUISkipReason(app); reason != "" {
		fmt.Printf("SKIP ui:test: %s\n", reason)
		return nil
	}
	if err := ensureChatbotUIDependencies(app); err != nil {
		return err
	}
	if err := runChatbotUITests(app); err != nil {
		return err
	}
	fmt.Println("ui:test PASS - the SPA suite passed")
	return nil
}

// Bundle proves the committed dist was built from the committed sources, by
// building into a temporary directory and comparing byte for byte.
func (UI) Bundle() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	app := chatbotUIAppDir(root)
	if reason := chatbotUISkipReason(app); reason != "" {
		fmt.Printf("SKIP ui:bundle: %s\n", reason)
		return nil
	}
	if err := ensureChatbotUIDependencies(app); err != nil {
		return err
	}
	return verifyChatbotUIBundle(app)
}

// chatbotUIAppDir resolves the SPA directory from the application root, the way
// the other targets resolve application-local paths.
func chatbotUIAppDir(applicationRoot string) string {
	return filepath.Join(applicationRoot, filepath.FromSlash(chatbotUIAppPath))
}

// chatbotUISkipReason reports why the SPA checks cannot run, or "" when they
// can. A machine without node is not a failing machine; it is one that cannot
// answer the question, and it should say so rather than fail the gate.
func chatbotUISkipReason(app string) string {
	if info, err := os.Stat(app); err != nil || !info.IsDir() {
		return fmt.Sprintf("SPA directory not found at %s", app)
	}
	if _, err := exec.LookPath("npm"); err != nil {
		return "npm not found on PATH"
	}
	return ""
}

// ensureChatbotUIDependencies installs from the lockfile when any declared
// package is missing. node_modules is gitignored, so a fresh clone always needs
// this; a warm checkout pays only when the manifest has gained a package it
// does not have, which is the case GH-249 exists for.
func ensureChatbotUIDependencies(app string) error {
	missing, err := missingChatbotUIPackages(app)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	fmt.Printf("ui: installing SPA dependencies from the lockfile (%s missing)\n", strings.Join(missing, ", "))
	if out, err := runChatbotUICommand(app, "npm", "ci"); err != nil {
		return fmt.Errorf("npm ci in %s: %w\n%s", app, err, out)
	}
	return nil
}

// missingChatbotUIPackages names the declared packages absent from node_modules.
//
// The presence of the directory is not the question. A checkout that ran an
// install before a dependency was added has a node_modules that exists and is
// missing exactly the package the new target needs -- which is the ordinary
// case after a pull, and worse than the fresh-clone case, which installs
// because it has nothing at all. GH-245 added puppeteer-core and every existing
// checkout kept failing with a module-not-found until this looked inside.
//
// Presence, not versions: npm ci is the authority on drift, and running it
// unconditionally would add its cost to every ui:test. This catches the
// failure that actually happens -- a lockfile that gained an entry the tree
// does not have.
func missingChatbotUIPackages(app string) ([]string, error) {
	modules := filepath.Join(app, "node_modules")
	if info, err := os.Stat(modules); err != nil || !info.IsDir() {
		return []string{"node_modules"}, nil
	}
	manifest, err := os.ReadFile(filepath.Join(app, "package.json"))
	if err != nil {
		return nil, fmt.Errorf("read the SPA package manifest: %w", err)
	}
	var declared struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(manifest, &declared); err != nil {
		return nil, fmt.Errorf("parse the SPA package manifest: %w", err)
	}
	var missing []string
	for _, set := range []map[string]string{declared.Dependencies, declared.DevDependencies} {
		for name := range set {
			// A scoped name is a nested path under node_modules, which
			// filepath.Join handles because npm lays it out that way on disk.
			if _, err := os.Stat(filepath.Join(modules, filepath.FromSlash(name))); err != nil {
				missing = append(missing, name)
			}
		}
	}
	sort.Strings(missing)
	return missing, nil
}

func runChatbotUITests(app string) error {
	if out, err := runChatbotUICommand(app, "npm", "test"); err != nil {
		return fmt.Errorf("SPA test suite failed:\n%s", out)
	}
	return nil
}

// verifyChatbotUIBundle builds the SPA into a temporary directory and compares
// the result with the committed bundle. The build runs through vite directly
// with an overridden output directory, never through npm run build, so a
// verification can never overwrite the artifact it is checking.
func verifyChatbotUIBundle(app string) error {
	staged, err := os.MkdirTemp("", "cohere-demo-ui-bundle-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staged) }()

	if out, err := runChatbotUICommand(app, "npx", "tsc"); err != nil {
		return fmt.Errorf("SPA typecheck failed:\n%s", out)
	}
	out, err := runChatbotUICommand(app, "npx", "vite", "build", "--outDir", staged, "--emptyOutDir")
	if err != nil {
		return fmt.Errorf("SPA build failed, so the committed bundle cannot be reproduced:\n%s", out)
	}

	committed := filepath.Join(app, chatbotUIDistDir)
	differences, err := compareBundleTrees(committed, staged)
	if err != nil {
		return err
	}
	if len(differences) > 0 {
		return fmt.Errorf(
			"the committed bundle does not match a build of the committed sources:\n  %s\n"+
				"rebuild it with: (cd %s && npm run build) and commit the result",
			strings.Join(differences, "\n  "), chatbotUIAppPath)
	}
	fmt.Println("ui:bundle PASS - the committed bundle reproduces from the committed sources")
	return nil
}

// compareBundleTrees reports every way the two directories differ, as sentences
// naming the file. It returns all of them rather than the first, because a
// rebuild usually changes several files at once and one at a time is a poor way
// to learn that.
func compareBundleTrees(committed, built string) ([]string, error) {
	committedFiles, err := bundleFileSet(committed)
	if err != nil {
		return nil, fmt.Errorf("read committed bundle %s: %w", committed, err)
	}
	builtFiles, err := bundleFileSet(built)
	if err != nil {
		return nil, fmt.Errorf("read rebuilt bundle %s: %w", built, err)
	}

	names := map[string]bool{}
	for name := range committedFiles {
		names[name] = true
	}
	for name := range builtFiles {
		names[name] = true
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)

	var differences []string
	for _, name := range ordered {
		switch {
		case !builtFiles[name]:
			differences = append(differences,
				fmt.Sprintf("%s is committed but a rebuild does not produce it", name))
		case !committedFiles[name]:
			differences = append(differences,
				fmt.Sprintf("%s is produced by a rebuild but is not committed", name))
		default:
			same, err := sameFileContents(
				filepath.Join(committed, filepath.FromSlash(name)),
				filepath.Join(built, filepath.FromSlash(name)))
			if err != nil {
				return nil, err
			}
			if !same {
				differences = append(differences,
					fmt.Sprintf("%s differs from the rebuilt file", name))
			}
		}
	}
	return differences, nil
}

// bundleFileSet lists a bundle's files as slash-separated paths relative to its
// root, so the two trees compare by name rather than by location.
func bundleFileSet(root string) (map[string]bool, error) {
	files := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func sameFileContents(left, right string) (bool, error) {
	leftData, err := os.ReadFile(left)
	if err != nil {
		return false, err
	}
	rightData, err := os.ReadFile(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftData, rightData), nil
}

func runChatbotUICommand(app, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = app
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// verifyChatbotUI runs both SPA checks as one gate step, skipping once rather
// than twice when the machine cannot run them.
func verifyChatbotUI(applicationRoot string) error {
	app := chatbotUIAppDir(applicationRoot)
	if reason := chatbotUISkipReason(app); reason != "" {
		fmt.Printf("SKIP ui: %s\n", reason)
		return nil
	}
	if err := ensureChatbotUIDependencies(app); err != nil {
		return err
	}
	if err := runChatbotUITests(app); err != nil {
		return err
	}
	fmt.Println("ui: the SPA suite passed")
	return verifyChatbotUIBundle(app)
}
