// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// observerMonitorRest is the slice of the observer's monitor surface these
// assertions read: the endpoints it serves and, for the fleet UI, the bundle it
// selects and the config it declares.
type observerMonitorRest struct {
	Rest struct {
		Servers map[string]struct {
			Endpoints map[string]struct {
				Method       string `yaml:"method"`
				Path         string `yaml:"path"`
				Binding      string `yaml:"binding"`
				StaticAssets struct {
					Bundle string `yaml:"bundle"`
					Root   string `yaml:"root"`
					SPA    bool   `yaml:"spa"`
					Config struct {
						Title           string `yaml:"title"`
						MonitoredAgents []struct {
							Name  string `yaml:"name"`
							Label string `yaml:"label"`
						} `yaml:"monitored_agents"`
						TraceBackend string `yaml:"trace_backend"`
					} `yaml:"config"`
				} `yaml:"static_assets"`
				MonitorProxy struct {
					Upstreams map[string]string `yaml:"upstreams"`
				} `yaml:"monitor_proxy"`
			} `yaml:"endpoints"`
		} `yaml:"servers"`
	} `yaml:"rest"`
}

func readObserverMonitorRest(t *testing.T, path string) observerMonitorRest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rest observerMonitorRest
	if err := yaml.Unmarshal(data, &rest); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return rest
}

// The observer serves the fleet UI from the bundle compiled into agent-core
// (srd004 R9, GH-737), so this mesh ships no observer UI files at all. The
// binding names the bundle, and the config it declares is what the bundle reads
// back as ui-config.json.
func TestObserverServesTheEmbeddedBundle(t *testing.T) {
	rest := readObserverMonitorRest(t, filepath.Join("..", "agents", "observer", "monitor-rest.yaml"))
	endpoints := rest.Rest.Servers["observer_monitor"].Endpoints
	for _, want := range []string{"machine_spec", "current_state", "fleet", "monitor_proxy", "fleet_ui", "root_redirect"} {
		if _, ok := endpoints[want]; !ok {
			t.Errorf("the observer monitor surface declares no %s endpoint", want)
		}
	}
	ui := endpoints["fleet_ui"]
	if ui.Binding != "static_assets" || ui.Path != "/ui/{path...}" {
		t.Errorf("fleet_ui is %s %s, want a static_assets binding at /ui/{path...}", ui.Binding, ui.Path)
	}
	if ui.StaticAssets.Bundle != "observer" {
		t.Errorf("fleet_ui selects bundle %q, want the observer bundle compiled into agent-core", ui.StaticAssets.Bundle)
	}
	if ui.StaticAssets.Root != "" {
		t.Errorf("fleet_ui still serves a filesystem root (%q); the bundle replaces it", ui.StaticAssets.Root)
	}
	if !ui.StaticAssets.SPA {
		t.Error("fleet_ui does not declare spa: true, so a deep link would 404 rather than reach the shell")
	}
	if ui.StaticAssets.Config.Title == "" || ui.StaticAssets.Config.TraceBackend == "" {
		t.Errorf("fleet_ui config declares title %q and trace backend %q; the bundle reads both",
			ui.StaticAssets.Config.Title, ui.StaticAssets.Config.TraceBackend)
	}
}

// Every agent the UI is told to monitor must be reachable through the proxy
// beside it, and the trace backend too. The UI reads each agent at
// /monitor-proxy/<name>/, so a name with no upstream renders a card that never
// loads (srd004 R2.1, R9.2).
func TestObserverProxyReachesEveryAgentItsUINames(t *testing.T) {
	rest := readObserverMonitorRest(t, filepath.Join("..", "agents", "observer", "monitor-rest.yaml"))
	endpoints := rest.Rest.Servers["observer_monitor"].Endpoints
	upstreams := endpoints["monitor_proxy"].MonitorProxy.Upstreams
	if endpoints["monitor_proxy"].Path != "/monitor-proxy/{agent}/{path...}" {
		t.Errorf("the proxy serves %q; the embedded bundle reads /monitor-proxy/<agent>/",
			endpoints["monitor_proxy"].Path)
	}
	config := endpoints["fleet_ui"].StaticAssets.Config
	for _, agent := range config.MonitoredAgents {
		if _, ok := upstreams[agent.Name]; !ok {
			t.Errorf("the UI monitors %q, which the proxy declares no upstream for", agent.Name)
		}
		if agent.Label == "" {
			t.Errorf("monitored agent %q carries no label", agent.Name)
		}
	}
	if _, ok := upstreams[config.TraceBackend]; !ok {
		t.Errorf("the UI reads traces from %q, which the proxy declares no upstream for", config.TraceBackend)
	}
}

// The observer directory holds declarations and nothing else: no UI sources, no
// bundle, no package manifest. The bundle it serves ships inside agent-core.
func TestObserverDirectoryCarriesNoUIFiles(t *testing.T) {
	root := filepath.Join("..", "agents", "observer")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
		if entry.IsDir() {
			t.Errorf("agents/observer holds the directory %s; the observer ships declarations only", entry.Name())
		}
		if !strings.HasSuffix(entry.Name(), ".yaml") {
			t.Errorf("agents/observer holds %s, which is not a declaration", entry.Name())
		}
	}
	for _, want := range []string{"profile.yaml", "rest.yaml", "monitor-rest.yaml"} {
		if !slices.Contains(names, want) {
			t.Errorf("agents/observer is missing %s", want)
		}
	}
}
