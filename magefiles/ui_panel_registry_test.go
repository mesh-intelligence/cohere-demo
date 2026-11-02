// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// This is the check that would have caught GH-723. ui.yaml declared three panel
// paths and the SPA implemented none of them: it held the active panel in
// component state, so every declared path rendered the chat panel. Each
// artifact read alone looks correct -- the config lists panels, the app renders
// panels -- and only the two read together show the declared surface is not
// served.
//
// Since GH-737 ui.yaml is version 2 and the app mounts panels from the registry
// in app/src/panels.ts, so the property is sharper: adding a panel is one entry
// in each file, and a panel in one and not the other fails here.

type uiPanelsDoc struct {
	Version int `yaml:"version"`
	Panels  []struct {
		ID           string `yaml:"id"`
		Package      string `yaml:"package"`
		Export       string `yaml:"export"`
		Route        string `yaml:"route"`
		Label        string `yaml:"label"`
		Hidden       bool   `yaml:"hidden"`
		SidebarGroup string `yaml:"sidebar_group"`
	} `yaml:"panels"`
	Sidebar struct {
		Groups map[string]struct{} `yaml:"groups"`
	} `yaml:"sidebar"`
	MonitoredAgents []struct {
		Name string `yaml:"name"`
	} `yaml:"monitored_agents"`
	TraceBackend struct {
		Name string `yaml:"name"`
	} `yaml:"trace_backend"`
}

func meshUIRoot(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// The magefiles package runs from the application root's magefiles.
	return filepath.Join(filepath.Dir(root), "agents", "chatbot", "ui")
}

func declaredUIPanels(t *testing.T) uiPanelsDoc {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(meshUIRoot(t), "ui.yaml"))
	if err != nil {
		t.Fatalf("read ui.yaml: %v", err)
	}
	var doc uiPanelsDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse ui.yaml: %v", err)
	}
	if doc.Version != 2 {
		t.Fatalf("ui.yaml declares version %d; the shell reads version 2 panels", doc.Version)
	}
	if len(doc.Panels) == 0 {
		t.Fatal("ui.yaml declares no panels; the cross-check would pass vacuously")
	}
	return doc
}

// registryPanelRE matches the keys of the registry map in app/src/panels.ts.
var registryPanelRE = regexp.MustCompile(`(?m)^\s{2}([a-z][a-z0-9-]*):\s`)

func registryPanels(t *testing.T) map[string]bool {
	t.Helper()
	path := filepath.Join(meshUIRoot(t), "app", "src", "panels.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read panels.ts: %v", err)
	}
	body := string(data)
	start := strings.Index(body, "export const panels")
	if start < 0 {
		t.Fatalf("%s declares no panels registry; the guard went blind", path)
	}
	entries := map[string]bool{}
	for _, match := range registryPanelRE.FindAllStringSubmatch(body[start:], -1) {
		entries[match[1]] = true
	}
	if len(entries) == 0 {
		t.Fatalf("no registry entries parsed from %s; the map shape changed and this guard went blind", path)
	}
	return entries
}

// TestChatbotUIPanelsMatchTheRegistry fails when ui.yaml and the SPA disagree,
// in either direction: a panel declared with no registry entry renders nothing,
// and an entry no panel declares is a surface the co-generated config does not
// know about. Kit panels are exempt from the registry: the shell mounts those
// by the manifest id their export names.
func TestChatbotUIPanelsMatchTheRegistry(t *testing.T) {
	t.Parallel()
	doc := declaredUIPanels(t)
	registry := registryPanels(t)

	declared := map[string]bool{}
	for _, panel := range doc.Panels {
		declared[panel.ID] = true
		if panel.Package == "@declarative-agents/ui-kit" {
			if panel.Export == "" {
				t.Errorf("kit panel %q names no export; the shell mounts a kit panel by its manifest id", panel.ID)
			}
			continue
		}
		if !registry[panel.ID] {
			t.Errorf("ui.yaml declares panel %q that app/src/panels.ts does not mount", panel.ID)
		}
		if !strings.HasPrefix(panel.Route, "/") || strings.Count(panel.Route, "/") != 1 {
			t.Errorf("panel %q route is %q; the shell takes one lower-case segment", panel.ID, panel.Route)
		}
		if panel.SidebarGroup != "" {
			if _, ok := doc.Sidebar.Groups[panel.SidebarGroup]; !ok {
				t.Errorf("panel %q sits in sidebar group %q, which sidebar.groups does not declare",
					panel.ID, panel.SidebarGroup)
			}
		}
	}
	var extra []string
	for id := range registry {
		if !declared[id] {
			extra = append(extra, id)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("app/src/panels.ts mounts panels ui.yaml does not declare: %v", extra)
	}
}

// The panel props the shell passes come from ui.yaml too, so a deployment that
// renames an agent or its trace backend moves the UI without a rebuild. An
// empty list would render a fleet with no agents and read as "nothing
// deployed".
func TestChatbotUIDeclaresItsMonitoredSurface(t *testing.T) {
	t.Parallel()
	doc := declaredUIPanels(t)
	if len(doc.MonitoredAgents) == 0 {
		t.Error("ui.yaml declares no monitored agents; the fleet panel would render empty")
	}
	if doc.TraceBackend.Name == "" {
		t.Error("ui.yaml declares no trace backend; the trace panel would have nothing to read")
	}
}

// The SPA reads its route table from ui.yaml and nothing else. A second table
// in the app is what GH-723 was, and a local copy of the kit's panel path
// helpers or clients is what the kit's own duplication rule forbids (the
// ui-panels guide, Rules).
func TestChatbotUIKeepsNoSecondRouteTableOrClientCopy(t *testing.T) {
	t.Parallel()
	appSrc := filepath.Join(meshUIRoot(t), "app", "src")
	forbidden := map[string]string{
		"PANEL_ROUTES":     "a second route table; ui.yaml is the only one",
		"splitPanelPath":   "a local copy of the kit's panel path helpers",
		"/monitor-proxy/":  "a hand-spelled proxy path; the kit client addresses the proxy",
		"/trace-proxy/":    "a hand-spelled trace proxy path",
		"new EventSource(": "a raw EventSource; the kit client opens streams",
	}
	err := filepath.WalkDir(appSrc, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		switch filepath.Ext(path) {
		case ".ts", ".tsx":
		default:
			return nil
		}
		if strings.HasSuffix(path, ".test.ts") || strings.HasSuffix(path, ".test.tsx") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for needle, why := range forbidden {
			if strings.Contains(string(data), needle) {
				t.Errorf("%s carries %q: %s", filepath.Base(path), needle, why)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// One raw fetch is one call that cannot be pointed at another origin.
	rawFetch := regexp.MustCompile(`(?m)(^|[^.\w])fetch\(`)
	for _, name := range []string{"api.ts", "documentsApi.ts", "provisioningApi.ts", "chatHistory.ts"} {
		data, readErr := os.ReadFile(filepath.Join(appSrc, name))
		if readErr != nil {
			continue
		}
		if rawFetch.Match(data) {
			t.Errorf("%s calls fetch directly; every call goes through the kit client (srd004 R6.1)", name)
		}
	}
}
