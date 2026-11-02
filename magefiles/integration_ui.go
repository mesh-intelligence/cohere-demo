// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// The browser gate for the chatbot SPA (rel02.0-uc001 S1).
//
// Everything else covering this UI reads it without rendering it: ui:test runs
// vitest over pure functions, and the scenario critic rig drives the agents'
// HTTP surfaces. Two defects reached deployment through that gap -- a permanent
// error panel for a RAG unit the release does not deploy (GH-233), and, after
// that fix, a 404 reissued every three seconds behind a panel that looked
// correct (GH-243).
//
// The assertions live in agents/chatbot/ui/app/browser-check.mjs, next to the
// SPA whose selectors they depend on. This target's job is the part Go is
// better at: deciding whether the run is possible, and saying why when it is
// not.

const (
	// The deployed SPA, which is where the panel behaviour is observable. A
	// static serve would exercise the routes but not a grounded turn, and the
	// turn is half of what this gate is for.
	chatbotUIDefaultBase = "http://" + cohereApplicationHost
	browserCheckScript   = "browser-check.mjs"
	// Bounds the whole browser run: each turn is allowed five minutes against
	// live providers, the rel11.5 tracer runs three of them paced apart to
	// stay under provider rate limits, an uncited turn earns bounded paced
	// retries, and the dwell, the walk, and the route loads sit on top.
	browserCheckTimeout = 30 * time.Minute
)

// chromeCandidates are the browsers puppeteer-core can drive, most specific
// first. puppeteer-core ships no browser of its own, which is the point: the
// gate uses the one already installed rather than downloading a second copy.
var chromeCandidates = []string{
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
	"/usr/bin/google-chrome",
	"/usr/bin/chromium",
	"/usr/bin/chromium-browser",
}

// UI drives the deployed chatbot SPA in a real browser and asserts the
// observability panels, a grounded turn, and every route, plus the request
// rates a rendered page cannot show (rel02.0-uc001 S1).
//
// It skips rather than fails when the browser or the deployed mesh is missing,
// matching the other live targets: a developer without a cluster should see a
// named skip, not a red run.
func (Integration) UI() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	app := chatbotUIAppDir(root)
	if reason := chatbotUISkipReason(app); reason != "" {
		fmt.Printf("SKIP integration:ui: %s\n", reason)
		return nil
	}
	if _, err := os.Stat(filepath.Join(app, browserCheckScript)); err != nil {
		return fmt.Errorf("the browser check is missing from %s: %w", app, err)
	}
	chrome := resolveChrome()
	if chrome == "" {
		fmt.Printf("SKIP integration:ui: no Chrome or Chromium found at any of %v;"+
			" set CHROME_PATH to drive a browser installed elsewhere\n", chromeCandidates)
		return nil
	}
	base := envOrDefault("CHATBOT_UI_BASE", chatbotUIDefaultBase)
	if err := chatbotUIReachable(base); err != nil {
		fmt.Printf("SKIP integration:ui: %s does not serve the SPA (%v);"+
			" run mage app:up and mage app:seed first\n", base, err)
		return nil
	}
	if err := ensureChatbotUIDependencies(app); err != nil {
		return err
	}
	if err := runBrowserCheck(app, chrome, base); err != nil {
		return err
	}
	fmt.Println("integration:ui PASS - the panels render one entry per deployed agent," +
		" a turn answers grounded in a source at two clearance levels, a faceted turn" +
		" fills the INTSUM form and walks a citation to its source, every route renders," +
		" and no endpoint is probed on a loop")
	return nil
}

// resolveChrome returns the browser to drive: CHROME_PATH when set, otherwise
// the first candidate present on this host.
func resolveChrome() string {
	if configured := envOrDefault("CHROME_PATH", ""); configured != "" {
		if _, err := os.Stat(configured); err == nil {
			return configured
		}
		return ""
	}
	for _, candidate := range chromeCandidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// chatbotUIReachable confirms the deployed SPA answers before a browser is
// started, so an absent mesh reads as a skip with its cause rather than as a
// browser timeout several minutes later.
func chatbotUIReachable(base string) error {
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Get(base + "/ui/")
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s/ui/ returned status %d", base, response.StatusCode)
	}
	return nil
}

// runBrowserCheck runs the checks and streams their per-check lines, so a
// failure names the assertion that failed rather than only the exit code.
func runBrowserCheck(app, chrome, base string) error {
	command := exec.Command("node", browserCheckScript)
	command.Dir = app
	command.Env = append(os.Environ(),
		"CHROME_PATH="+chrome,
		"CHATBOT_UI_BASE="+base,
	)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	done := make(chan error, 1)
	if err := command.Start(); err != nil {
		return fmt.Errorf("start the browser check: %w", err)
	}
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("browser checks failed: %w", err)
		}
		return nil
	case <-time.After(browserCheckTimeout):
		_ = command.Process.Kill()
		return fmt.Errorf("the browser check did not finish within %s", browserCheckTimeout)
	}
}
