// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func yamlDocument(data []byte) string {
	return strings.TrimPrefix(string(data), "# Copyright (c) 2026 Mesh Intelligence Inc.\n# SPDX-License-Identifier: MIT-0\n")
}

func TestCollectorIntakeFilterScenario(t *testing.T) {
	applicationRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	catalogRoot, err := resolveCatalogRoot("collector intake test", applicationRoot)
	if err != nil {
		t.Fatal(err)
	}
	coreRoot := demoCoreRoot(applicationRoot)
	if !agentCoreAvailable(coreRoot) {
		t.Skipf("agent-core checkout not found at %s", coreRoot)
	}
	binary, cleanupAgent, err := buildAgent(coreRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupAgent()
	workDir := t.TempDir()
	if err := runCollectorIntakeScenario(binary, coreRoot, catalogRoot, workDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workDir, "collector.ndjson")); err != nil {
		t.Fatalf("collector spool evidence: %v", err)
	}
}

func TestCollectorLifecycleRebindAndTerminalState(t *testing.T) {
	applicationRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	catalogRoot, err := resolveCatalogRoot("collector lifecycle test", applicationRoot)
	if err != nil {
		t.Fatal(err)
	}
	coreRoot := demoCoreRoot(applicationRoot)
	if !agentCoreAvailable(coreRoot) {
		t.Skipf("agent-core checkout not found at %s", coreRoot)
	}
	binary, cleanupAgent, err := buildAgent(coreRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupAgent()
	workDir := t.TempDir()
	result, err := runCollectorLifecycleScenario(binary, coreRoot, catalogRoot, workDir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.MonitorReachable {
		t.Error("monitor was not reachable while collector was running")
	}
	if result.TerminalState != "succeeded" {
		t.Errorf("terminal state = %q, want %q", result.TerminalState, "succeeded")
	}
	if !result.AllAddrsRebind {
		t.Error("not all listener addresses could rebind after exit")
	}
}

func TestStageRigRuntimeUsesCatalogScenarioCritic(t *testing.T) {
	applicationRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	catalogRoot := catalogRootForTest(t)
	stage, cleanup, err := stageRigRuntime(applicationRoot, catalogRoot, "127.0.0.1:4317")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, path := range []string{
		"agents/scenario-critic/machine.yaml",
		"agents/scenario-critic/tools.yaml",
		"testdata/rig/declarations.yaml",
	} {
		if _, err := os.Stat(filepath.Join(stage, filepath.FromSlash(path))); err != nil {
			t.Errorf("staged rig missing %s: %v", path, err)
		}
	}
	profile, err := os.ReadFile(filepath.Join(stage, filepath.FromSlash(rigProfile)))
	if err != nil {
		t.Fatal(err)
	}
	if yamlDocument(profile) != "kind: agent-profile\nschema_version: 1\nname: scenario-critic\nmachine: ../../agents/scenario-critic/machine.yaml\ntools:\n  - ../../agents/scenario-critic/tools.yaml\ntool_declarations:\n  - declarations.yaml\nrest_definitions:\n  - rest.yaml\n" {
		t.Fatalf("staged rig profile does not select catalog scenario critic:\n%s", profile)
	}
	declarations, err := os.ReadFile(filepath.Join(stage, "testdata", "rig", "declarations.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(declarations), `otlp_endpoint: "127.0.0.1:4317"`) {
		t.Fatalf("staged rig declarations do not configure the validator OTLP endpoint:\n%s", declarations)
	}
}

func TestMeshScenarioCriticIdentities(t *testing.T) {
	tests := []struct {
		scenario, identity string
	}{
		{"agents/chatbot/tests/single-turn", "chatbot-turn-critic"},
		{"agents/chatbot/tests/degraded-rag", "chatbot-turn-critic"},
		{"agents/rag-server/tests/query", "rag-query-critic"},
	}
	for _, test := range tests {
		t.Run(test.scenario, func(t *testing.T) {
			for _, file := range []string{"profile.yaml", "machine.yaml"} {
				data, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(test.scenario), file))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains("\n"+yamlDocument(data), "\nname: "+test.identity+"\n") {
					t.Fatalf("%s/%s does not declare %q:\n%s", test.scenario, file, test.identity, data)
				}
			}
		})
	}
}

// TestRigMockPortsMatchScenarioFixtures guards the preflight against drift in
// both directions: every port the preflight refuses to run over is pinned by
// some scenario's mock fixture, and every port a scenario pins is one the
// preflight guards. Repin a fixture without updating the preflight and this
// fails, before a developer rediscovers GH-1229 the hard way.
//
// The reconciliation is over the union rather than the cross product. Scenarios
// no longer share one fixture set — the Cohere scenarios stand a provider mock
// the Ollama-only ones have no use for — so requiring every scenario to pin
// every guarded port would force each to declare fixtures it never starts.
// Scenarios are discovered rather than listed, so a new one is covered the
// moment it exists.
func TestRigMockPortsMatchScenarioFixtures(t *testing.T) {
	scenarios, err := filepath.Glob(filepath.Join("..", "agents", "*", "tests", "*", "scenario.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) == 0 {
		t.Fatal("no scenario manifests discovered under agents/*/tests/*")
	}
	guarded := make(map[string]string, len(rigMockFixturePorts))
	for _, mock := range rigMockFixturePorts {
		guarded[mock.port] = mock.name
	}
	pinnedBy := map[string][]string{}
	for _, scenario := range scenarios {
		data, err := os.ReadFile(scenario)
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			FixtureAddress map[string]string `yaml:"fixture_address"`
		}
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			t.Fatalf("%s: %v", scenario, err)
		}
		name := filepath.Base(filepath.Dir(scenario))
		for fixture, address := range manifest.FixtureAddress {
			_, port, err := net.SplitHostPort(address)
			if err != nil {
				t.Errorf("%s fixture %q address %q is not host:port", name, fixture, address)
				continue
			}
			if _, ok := guarded[port]; !ok {
				t.Errorf("scenario %s pins fixture %q to port %s, which the rig preflight does not guard;"+
					" a local service holding it would silently answer in the mock's place", name, fixture, port)
			}
			pinnedBy[port] = append(pinnedBy[port], name)
		}
	}
	for port, name := range guarded {
		if len(pinnedBy[port]) == 0 {
			t.Errorf("the rig preflight guards port %s (%s), which no scenario pins a fixture to", port, name)
		}
	}
}

// TestRigMockPortSkipReason confirms the preflight skips when a fixture port is
// held and clears once it is free — the non-blocking behaviour GH-1229 asked
// for. It injects a self-bound port so the check is hermetic and never depends
// on whether a real Ollama happens to be running on the host.
func TestRigMockPortSkipReason(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	saved := rigMockFixturePorts
	defer func() { rigMockFixturePorts = saved }()
	rigMockFixturePorts = []struct{ name, port string }{{"test mock fixture", port}}

	if reason := rigMockPortSkipReason(); reason == "" {
		t.Fatalf("expected a skip reason while port %s is held", port)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if reason := rigMockPortSkipReason(); reason != "" {
		t.Fatalf("expected no skip reason once port %s is free, got: %s", port, reason)
	}
}
