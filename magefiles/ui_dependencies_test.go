// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The SPA dependency check decides whether a target that needs node_modules can
// run without installing. It used to ask only whether the directory existed,
// which is true of every checkout that ever ran an install and says nothing
// about whether the package a new target needs is in there.
//
// GH-245 added puppeteer-core and every existing checkout kept failing with a
// module-not-found, because the directory was present and the package was not.
// A fresh clone worked. The broken case was the ordinary one -- pull, run the
// target -- which is why these tests are about a tree that exists and is
// incomplete rather than one that is absent.

// writeUIApp lays out a minimal SPA app directory: a manifest declaring the
// given packages, and a node_modules holding the installed ones.
func writeUIApp(t *testing.T, manifest string, installed ...string) string {
	t.Helper()
	app := t.TempDir()
	if err := os.WriteFile(filepath.Join(app, "package.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if installed != nil {
		for _, name := range installed {
			dir := filepath.Join(app, "node_modules", filepath.FromSlash(name))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("create %s: %v", dir, err)
			}
		}
	}
	return app
}

func TestADeclaredPackageAbsentFromTheTreeIsReportedMissing(t *testing.T) {
	// The GH-245 shape exactly: the tree exists, and the dependency the new
	// target needs is the one thing not in it.
	app := writeUIApp(t,
		`{"devDependencies": {"vite": "^6", "puppeteer-core": "^24"}}`,
		"vite")

	missing, err := missingChatbotUIPackages(app)
	if err != nil {
		t.Fatalf("missingChatbotUIPackages: %v", err)
	}
	if len(missing) != 1 || missing[0] != "puppeteer-core" {
		t.Errorf("missing = %v, want [puppeteer-core]:"+
			" a tree that predates an added dependency must install rather than be taken as complete", missing)
	}
}

func TestACompleteTreeInstallsNothing(t *testing.T) {
	// The common case, and the one that must stay fast: npm ci is the authority
	// on version drift, and paying its cost on every ui:test would be the wrong
	// trade for a check that runs before every target.
	app := writeUIApp(t,
		`{"dependencies": {"react": "^19"}, "devDependencies": {"vite": "^6"}}`,
		"react", "vite")

	missing, err := missingChatbotUIPackages(app)
	if err != nil {
		t.Fatalf("missingChatbotUIPackages: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want none: a complete tree must not reinstall", missing)
	}
}

func TestAnAbsentTreeInstalls(t *testing.T) {
	app := writeUIApp(t, `{"devDependencies": {"vite": "^6"}}`)

	missing, err := missingChatbotUIPackages(app)
	if err != nil {
		t.Fatalf("missingChatbotUIPackages: %v", err)
	}
	if len(missing) == 0 {
		t.Error("an app with no node_modules reported nothing missing; a fresh clone must install")
	}
}

func TestBothDependencySetsAreChecked(t *testing.T) {
	// A runtime dependency and a dev dependency fail the same way at run time,
	// so checking only one set would leave half the manifest unenforced.
	app := writeUIApp(t,
		`{"dependencies": {"react": "^19"}, "devDependencies": {"vite": "^6"}}`)

	missing, err := missingChatbotUIPackages(writeUIAppWithModules(t, app))
	if err != nil {
		t.Fatalf("missingChatbotUIPackages: %v", err)
	}
	var sawRuntime, sawDev bool
	for _, name := range missing {
		switch name {
		case "react":
			sawRuntime = true
		case "vite":
			sawDev = true
		}
	}
	if !sawRuntime || !sawDev {
		t.Errorf("missing = %v, want both react and vite: both sets are needed at run time", missing)
	}
}

// writeUIAppWithModules gives an app an empty node_modules, so the check reads
// the manifest rather than short-circuiting on the absent directory.
func writeUIAppWithModules(t *testing.T, app string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(app, "node_modules"), 0o755); err != nil {
		t.Fatalf("create node_modules: %v", err)
	}
	return app
}

func TestAScopedPackageIsFoundAtItsNestedPath(t *testing.T) {
	// npm lays a scoped name out as two directories, so a check that treated the
	// name as one path component would report every scoped package missing and
	// reinstall on every run.
	app := writeUIApp(t,
		`{"devDependencies": {"@vitejs/plugin-react": "^4"}}`,
		"@vitejs/plugin-react")

	missing, err := missingChatbotUIPackages(app)
	if err != nil {
		t.Fatalf("missingChatbotUIPackages: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want none: a scoped package lives at its nested path", missing)
	}
}
