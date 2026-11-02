// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// applyRejectionClients is the shape both hops share: one client, its
// operations, and each operation's declared success and failure statuses.
type applyRejectionClients struct {
	Clients map[string]struct {
		Operations map[string]struct {
			Success struct {
				Status []int  `yaml:"status"`
				Signal string `yaml:"signal"`
			} `yaml:"success"`
			Failures []struct {
				Status []int  `yaml:"status"`
				Signal string `yaml:"signal"`
			} `yaml:"failures"`
		} `yaml:"operations"`
	} `yaml:"clients"`
}

func readApplyRejectionClients(t *testing.T, agent string) applyRejectionClients {
	t.Helper()
	var doc struct {
		REST applyRejectionClients `yaml:"rest"`
	}
	readIntakeYAML(t, filepath.Join(agentDir(t, agent), "rest.yaml"), &doc)
	return doc.REST
}

// failureSignalFor reports the signal an operation maps a status to, and whether
// it maps that status at all.
func failureSignalFor(
	t *testing.T, clients applyRejectionClients, client, operation string, status int,
) (string, bool) {
	t.Helper()
	declared, ok := clients.Clients[client]
	if !ok {
		t.Fatalf("no %s client is declared", client)
	}
	op, ok := declared.Operations[operation]
	if !ok {
		t.Fatalf("the %s client declares no %s operation", client, operation)
	}
	for _, failure := range op.Failures {
		for _, declaredStatus := range failure.Status {
			if declaredStatus == status {
				return failure.Signal, true
			}
		}
	}
	return "", false
}

// applyRejectionTools is the tool half of the pairing: which REST operation a
// builtin word drives, and the signals it declares it can emit.
type applyRejectionTools struct {
	Tools []struct {
		Name   string      `yaml:"name"`
		Emits  signalNames `yaml:"emits"`
		Config struct {
			RestRef   string `yaml:"rest_ref"`
			Operation string `yaml:"operation"`
		} `yaml:"config"`
	} `yaml:"tools"`
}

// A REST operation may only signal what the word driving it declares it emits.
// agent-core refuses the profile otherwise, and it refuses at boot -- after the
// image is built, the cluster is up, and the chart is installed. Neither the
// specification critic nor the boot smoke catches it, so adding a failure
// mapping without its emits entry cost a full live run before this test
// existed (GH-196).
func TestDeclaredSignalsCoverEveryMappedStatus(t *testing.T) {
	for _, agent := range []string{"creator", "provisioning-workflow-orchestrator"} {
		clients := readApplyRejectionClients(t, agent)

		var tools applyRejectionTools
		readIntakeYAML(t,
			filepath.Join(agentDir(t, agent), "request-declarations.yaml"), &tools)

		for _, tool := range tools.Tools {
			if tool.Config.Operation == "" {
				continue
			}
			client, ok := clients.Clients[tool.Config.RestRef]
			if !ok {
				continue
			}
			operation, ok := client.Operations[tool.Config.Operation]
			if !ok {
				t.Errorf("%s: %s drives %s, which the %s client does not declare",
					agent, tool.Name, tool.Config.Operation, tool.Config.RestRef)
				continue
			}

			declared := map[string]bool{}
			for _, signal := range tool.Emits {
				declared[signal] = true
			}
			signals := []string{operation.Success.Signal}
			for _, failure := range operation.Failures {
				signals = append(signals, failure.Signal)
			}
			for _, signal := range signals {
				if signal != "" && !declared[signal] {
					t.Errorf("%s: operation %s emits %s but %s declares emits %v;"+
						" agent-core refuses the profile at boot",
						agent, tool.Config.Operation, signal, tool.Name, tool.Emits)
				}
			}
		}
	}
}

// The applier answers 400 when its dry-run rejected the values document against
// the chart's own values.schema.json. Unmapped, agent-core failed the dispatch
// with "status 400 is not mapped" and the intent answered command_error, which
// reads as a broken boundary rather than as values the chart refuses.
func TestCreatorMapsARejectedValuesDocument(t *testing.T) {
	signal, ok := failureSignalFor(
		t, readApplyRejectionClients(t, "creator"), "deployment_api", "apply_instance", 400)
	if !ok {
		t.Fatal("apply_instance does not map 400, so a rejected values document" +
			" dies as an unmapped status and surfaces as command_error")
	}
	if signal == "CommandError" {
		t.Errorf("apply_instance maps 400 to %s, which is the same signal a failed"+
			" boundary emits; a rejected document would stay indistinguishable", signal)
	}
}

// The signal has to land somewhere. A mapped status whose signal no transition
// accepts is a machine that stalls rather than a rejection the caller reads.
func TestCreatorRejectedApplyReachesATerminalState(t *testing.T) {
	clients := readApplyRejectionClients(t, "creator")
	signal, ok := failureSignalFor(t, clients, "deployment_api", "apply_instance", 400)
	if !ok {
		t.Skip("apply_instance maps no 400; TestCreatorMapsARejectedValuesDocument covers that")
	}

	var machine intakeMachine
	readIntakeYAML(t, filepath.Join(agentDir(t, "creator"), "request-machine.yaml"), &machine)

	var next string
	for _, transition := range machine.Transitions {
		if transition.State == "Applying" && transition.Signal == signal {
			next = transition.Next
		}
	}
	if next == "" {
		t.Fatalf("no transition takes %s out of Applying, so a rejected document"+
			" stalls the machine", signal)
	}
	terminal := false
	for _, state := range machine.TerminalStates {
		if state == next {
			terminal = true
		}
	}
	if !terminal {
		t.Errorf("%s leads to %s, which is not terminal; the request would never answer",
			signal, next)
	}
	if next == "Failed" {
		t.Errorf("%s leads to Failed, the same terminal a failed boundary reaches;"+
			" a rejected document would answer 500 rather than as a rejection", signal)
	}
}

// The creator answers the rejection as a client error. A 500 would tell the
// panel to retry a document the chart will refuse every time, and a 200 would
// report a rollout that never happened.
func TestCreatorAnswersARejectedValuesDocumentAsAClientError(t *testing.T) {
	signal, ok := failureSignalFor(
		t, readApplyRejectionClients(t, "creator"), "deployment_api", "apply_instance", 400)
	if !ok {
		t.Skip("apply_instance maps no 400; TestCreatorMapsARejectedValuesDocument covers that")
	}

	var rest intakeRest
	readIntakeYAML(t, filepath.Join(agentDir(t, "creator"), "rest.yaml"), &rest)

	var endpoint intakeEndpoint
	var found bool
	for _, server := range rest.Rest.Servers {
		for _, declared := range server.Endpoints {
			if declared.Path == "/api/v1/instance" {
				endpoint, found = declared, true
			}
		}
	}
	if !found {
		t.Fatal("the creator declares no /api/v1/instance endpoint")
	}

	response, ok := endpoint.MachineRequest.Response.TerminalSignals[signal]
	if !ok {
		t.Fatalf("the instance endpoint declares no response for %s, so a rejected"+
			" document falls through to the unmapped answer", signal)
	}
	if response.Status != 422 {
		t.Errorf("a rejected values document answers %d, want 422: the caller has to"+
			" read it as their own fault rather than as a retryable failure",
			response.Status)
	}
	if !strings.Contains(readAgentFile(t, "creator", "rest.yaml"), "values_rejected") {
		t.Error("the rejection carries no distinct error code, so an operator reading" +
			" a trace cannot tell it from a failed boundary")
	}
}

// The rejection has to survive the last hop too. The orchestrator is what the
// panel talks to, and an unmapped 422 there would collapse the distinction the
// creator just drew.
func TestOrchestratorMapsTheCreatorsValuesRejection(t *testing.T) {
	clients := readApplyRejectionClients(t, "provisioning-workflow-orchestrator")
	for _, operation := range []string{"creator_rollout", "creator_rollout_values"} {
		signal, ok := failureSignalFor(t, clients, "creator_provisioning", operation, 422)
		if !ok {
			t.Errorf("%s does not map the creator's 422, so a rejected values"+
				" document reaches the panel as command_error", operation)
			continue
		}
		if signal != "Rejected" {
			t.Errorf("%s maps 422 to %s, want Rejected", operation, signal)
		}
	}

	var machine intakeMachine
	readIntakeYAML(t,
		filepath.Join(agentDir(t, "provisioning-workflow-orchestrator"), "request-machine.yaml"),
		&machine)
	rejected := false
	for _, transition := range machine.Transitions {
		if transition.State == "Reconfiguring" && transition.Signal == "Rejected" {
			rejected = transition.Next == "Rejected"
		}
	}
	if !rejected {
		t.Error("no transition takes Rejected out of Reconfiguring, so the mapped" +
			" rejection stalls the orchestrator instead of answering")
	}
}
