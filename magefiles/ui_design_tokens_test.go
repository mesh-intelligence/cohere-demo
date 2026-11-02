// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kitUIApps are the UIs that depend on the ui-kit release (GH-780). The
// observer is not among them: it serves the bundle compiled into agent-core and
// ships no UI sources of its own (GH-737).
var kitUIApps = []string{
	"agents/chatbot/ui/app",
}

// kitPackage is the registry package the kit ships as with every
// declarative-agents release, versioned as the release without its leading v
// (declarative-agents UPGRADING v0.20260926.1). The SPAs import it under the
// @declarative-agents/ui-kit alias.
const kitPackage = "@mesh-intelligence/ui-kit"

// kitRegistryScope is the scoped registry line each kit UI's .npmrc carries,
// so npm resolves the kit from GitHub Packages and everything else from npmjs.
const kitRegistryScope = "@mesh-intelligence:registry=https://npm.pkg.github.com"

// The design tokens and the kit panels come from the ui-kit published with
// the declarative-agents release the Go modules pin (GH-780, GH-846). The
// go.mod pin comment names that release; every UI must depend on and lock
// exactly it. A pin bump that forgets the kit, or a kit bump that forgets the
// pin, fails here rather than shipping panels built against another contract.
func TestDesignTokensComeFromTheKitRelease(t *testing.T) {
	release, _, err := pinnedDeclarativeAgentsRelease("..")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimPrefix(release, "v")
	for _, app := range kitUIApps {
		dir := filepath.Join("..", filepath.FromSlash(app))
		var manifest struct {
			Dependencies map[string]string `json:"dependencies"`
		}
		readJSONFile(t, filepath.Join(dir, "package.json"), &manifest)
		if source, spec := manifest.Dependencies["@declarative-agents/ui-kit"], "npm:"+kitPackage+"@"+want; source != spec {
			t.Errorf("%s depends on the kit through %q, want %q (the kit published with %s)", app, source, spec, release)
		}
		var lock struct {
			Packages map[string]struct {
				Name     string `json:"name"`
				Version  string `json:"version"`
				Resolved string `json:"resolved"`
			} `json:"packages"`
		}
		readJSONFile(t, filepath.Join(dir, "package-lock.json"), &lock)
		locked := lock.Packages["node_modules/@declarative-agents/ui-kit"]
		if locked.Name != kitPackage || locked.Version != want {
			t.Errorf("%s locks ui-kit %s@%s, want %s@%s; run npm install and commit the lockfile", app, locked.Name, locked.Version, kitPackage, want)
		}
		if !strings.HasPrefix(locked.Resolved, "https://npm.pkg.github.com/") {
			t.Errorf("%s resolves the kit from %q, want the GitHub npm registry", app, locked.Resolved)
		}
		npmrc, err := os.ReadFile(filepath.Join(dir, ".npmrc"))
		if err != nil || !strings.Contains(string(npmrc), kitRegistryScope) {
			t.Errorf("%s/.npmrc does not scope the kit to GitHub Packages (%q)", app, kitRegistryScope)
		}
	}
}

// No UI declares its own design-token block: the tokens are the kit's, imported
// as @declarative-agents/ui-kit/tokens.css, so a second :root block is a fork
// that drifts silently (GH-772, GH-780).
func TestNoUIForksTheDesignTokens(t *testing.T) {
	for _, app := range kitUIApps {
		root := filepath.Join("..", filepath.FromSlash(app), "src")
		imported := false
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || filepath.Ext(path) != ".css" {
				return err
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			body := string(data)
			imported = imported || strings.Contains(body, `@import "@declarative-agents/ui-kit/tokens.css"`)
			if strings.Contains(body, ":root") && strings.Contains(body, "--bg-primary") {
				t.Errorf("%s declares its own design-token block; import @declarative-agents/ui-kit/tokens.css instead", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !imported {
			t.Errorf("%s never imports @declarative-agents/ui-kit/tokens.css", app)
		}
	}
}

func readJSONFile(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

// No UI source imports a path outside the repository. A relative import that
// escapes the checkout resolves against whatever the neighbouring directories
// hold that day, which is the GH-772 failure shape whatever file it names next.
func TestUISourceImportsStayInsideTheRepository(t *testing.T) {
	for _, root := range []string{"../agents/chatbot/ui/app/src"} {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			switch filepath.Ext(entry.Name()) {
			case ".css", ".ts", ".tsx", ".js", ".jsx":
			default:
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for i, line := range strings.Split(string(data), "\n") {
				if !strings.Contains(line, "import") || !strings.Contains(line, "..") {
					continue
				}
				// Count how far the import climbs; the UI source roots sit at
				// most six levels below the repository root.
				if strings.Count(line, "../") >= 5 {
					t.Errorf("%s:%d imports a path that leaves the repository: %s",
						path, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
