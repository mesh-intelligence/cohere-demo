// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Reading an agent's listener ports out of its profile is what lets a target
// say "18080 is held by this process" instead of waiting thirty seconds and
// reporting that the control port on 18081 never answered -- which was true,
// and was not the problem.
//
// The addresses are written two ways in this repository and both have to work:
// literal, as the chatbot writes them, and environment-defaulted, as the
// rag-server writes them so a deployment can bind 0.0.0.0.

// writeProfileTree lays out a profile and the REST definition it names.
func writeProfileTree(t *testing.T, profileBody, restBody string) (string, string) {
	t.Helper()
	root := t.TempDir()
	agent := filepath.Join(root, "agents", "subject")
	if err := os.MkdirAll(agent, 0o755); err != nil {
		t.Fatalf("create agent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agent, "profile.yaml"), []byte(profileBody), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	if restBody != "" {
		if err := os.WriteFile(filepath.Join(agent, "rest.yaml"), []byte(restBody), 0o600); err != nil {
			t.Fatalf("write rest: %v", err)
		}
	}
	return root, filepath.Join("agents", "subject", "profile.yaml")
}

const listenerProfile = "name: subject\nrest_definitions:\n  - rest.yaml\n"

func TestListenerPortsAreReadFromLiteralAddresses(t *testing.T) {
	root, profile := writeProfileTree(t, listenerProfile, `
rest:
  servers:
    requests:
      address: 127.0.0.1:18080
    control:
      address: 127.0.0.1:18081
    monitor:
      address: 127.0.0.1:18082
`)

	ports, err := agentListenerPorts(root, profile, nil)
	if err != nil {
		t.Fatalf("agentListenerPorts: %v", err)
	}
	want := []string{"18080", "18081", "18082"}
	if len(ports) != len(want) {
		t.Fatalf("ports = %v, want %v", ports, want)
	}
	for index, port := range want {
		if ports[index] != port {
			t.Errorf("ports = %v, want %v", ports, want)
			break
		}
	}
}

func TestListenerPortsReadThroughAnEnvironmentDefaultedHost(t *testing.T) {
	// The rag-server writes its bind host as a reference so a deployment can
	// bind 0.0.0.0; the port after it is still literal and still has to be seen.
	root, profile := writeProfileTree(t, listenerProfile, `
rest:
  servers:
    requests:
      address: ${RAG_BIND_HOST:-127.0.0.1}:18085
`)

	ports, err := agentListenerPorts(root, profile, nil)
	if err != nil {
		t.Fatalf("agentListenerPorts: %v", err)
	}
	if len(ports) != 1 || ports[0] != "18085" {
		t.Errorf("ports = %v, want [18085]", ports)
	}
}

func TestAnEnvironmentDefaultedPortResolvesFromTheChildEnvironment(t *testing.T) {
	// The launch's own additions are what the child will see, so they decide the
	// port to check. Preflighting the default while the child binds the override
	// would check a port nothing was going to use.
	root, profile := writeProfileTree(t, listenerProfile, `
rest:
  servers:
    requests:
      address: 127.0.0.1:${SUBJECT_PORT:-19000}
`)

	ports, err := agentListenerPorts(root, profile, []string{"SUBJECT_PORT=19500"})
	if err != nil {
		t.Fatalf("agentListenerPorts: %v", err)
	}
	if len(ports) != 1 || ports[0] != "19500" {
		t.Errorf("ports = %v, want [19500]: the child's environment decides the port", ports)
	}
}

func TestAnEnvironmentDefaultedPortFallsBackToItsDeclaredDefault(t *testing.T) {
	root, profile := writeProfileTree(t, listenerProfile, `
rest:
  servers:
    requests:
      address: 127.0.0.1:${SUBJECT_PORT_UNSET:-19000}
`)

	ports, err := agentListenerPorts(root, profile, nil)
	if err != nil {
		t.Fatalf("agentListenerPorts: %v", err)
	}
	if len(ports) != 1 || ports[0] != "19000" {
		t.Errorf("ports = %v, want [19000]", ports)
	}
}

func TestAProfileWithNoReadableDefinitionsYieldsNoPorts(t *testing.T) {
	// Container-absolute paths do not resolve in a checkout, and a profile that
	// declares no servers has nothing to preflight. Neither is an error: the
	// preflight is a diagnosis, and refusing a run it cannot analyse would turn
	// a helper into an obstacle.
	root, profile := writeProfileTree(t,
		"name: subject\nrest_definitions:\n  - /opt/agent-core/tools/builtin/absent.yaml\n", "")

	ports, err := agentListenerPorts(root, profile, nil)
	if err != nil {
		t.Fatalf("agentListenerPorts: %v", err)
	}
	if len(ports) != 0 {
		t.Errorf("ports = %v, want none", ports)
	}
}

func TestTheShippedChatbotProfileDeclaresItsThreeListeners(t *testing.T) {
	// The profile this actually runs against. Reading the shipped file is what
	// keeps the parser honest about the two address forms the repository uses.
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	ports, err := agentListenerPorts(filepath.Dir(root), chatbotProfile, nil)
	if err != nil {
		t.Fatalf("agentListenerPorts: %v", err)
	}
	found := map[string]bool{}
	for _, port := range ports {
		found[port] = true
	}
	// 18080 is the one the misleading error never mentioned.
	for _, want := range []string{"18080", "18081", "18082"} {
		if !found[want] {
			t.Errorf("the chatbot listeners are %v, missing %s", ports, want)
		}
	}
}

func TestAPreflightOnAHeldPortNamesThatPort(t *testing.T) {
	// The whole point: the error has to name the port that is taken. A listener
	// is opened on an ephemeral port and the profile is pointed at it, so the
	// test never depends on a fixed port being free or busy on the host.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot open a listener to hold a port: %v", err)
	}
	defer func() { _ = listener.Close() }()
	_, held, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("read the held port: %v", err)
	}

	root, profile := writeProfileTree(t, listenerProfile,
		"rest:\n  servers:\n    requests:\n      address: 127.0.0.1:"+held+"\n")

	err = preflightAgentListeners(root, profile, nil)
	if err == nil {
		t.Fatal("a held listener port passed the preflight; the agent would start, fail to bind," +
			" and the caller would time out on a different port")
	}
	if !strings.Contains(err.Error(), held) {
		t.Errorf("the preflight error is %q, which does not name the held port %s", err, held)
	}
}
