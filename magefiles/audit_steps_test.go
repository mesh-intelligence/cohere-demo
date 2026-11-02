// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The gate runs the same binary twice over the same corpus. A failure that does
// not name its step sends the reader to the wrong evidence, which is what
// happened twice while diagnosing GH-115: an evidence-resolver failure reported
// as a corpus finding points at docs/specs, where nothing is wrong.
func TestAuditStepsFailDistinguishably(t *testing.T) {
	specs := filepath.Join("/repo", "docs", "specs")
	corpus := fmt.Sprintf("audit: "+corpusCriticStep.failure, specs)
	evidence := fmt.Sprintf("audit: "+evidenceResolverStep.failure, specs)

	if corpus == evidence {
		t.Fatal("both audit steps produce the same failure message")
	}
	if !strings.Contains(corpus, "corpus") {
		t.Errorf("corpus critic failure %q does not say the corpus is at fault", corpus)
	}
	if !strings.Contains(evidence, "go_test") {
		t.Errorf("evidence resolver failure %q does not name the evidence it could not resolve", evidence)
	}
	// The corpus-critic message points at docs/specs because that is what it
	// read. The resolver's must not imply the same, or it sends the reader to
	// files it never evaluated.
	if strings.Contains(evidence, "found errors in the application corpus at") {
		t.Errorf("evidence resolver failure %q reads as a corpus finding", evidence)
	}
}

func TestAuditStepsAreNamedDistinctly(t *testing.T) {
	if corpusCriticStep.name == evidenceResolverStep.name {
		t.Fatalf("both steps are named %q", corpusCriticStep.name)
	}
	for _, step := range []auditStep{corpusCriticStep, evidenceResolverStep} {
		if strings.TrimSpace(step.name) == "" {
			t.Error("an audit step has no name")
		}
		if !strings.Contains(step.failure, "%s") {
			t.Errorf("step %q has no place for the corpus path in %q", step.name, step.failure)
		}
	}
}

// The evidence resolver reads the module under test with `go list -m` and
// expects one module path. A Go workspace makes that print one line per
// workspace module, and every declared package then resolves to nothing --
// which is how a committed go.work silently turned the whole gate red (GH-115).
func TestSpecificationCriticRunsWithTheWorkspaceOff(t *testing.T) {
	cmd := specificationCriticCommand("/bin/agent", "/catalog/profile.yaml", "/repo", "/core")
	var seen string
	for _, entry := range cmd.Env {
		key, value, _ := strings.Cut(entry, "=")
		if key == "GOWORK" {
			seen = value
		}
	}
	if seen != "off" {
		t.Errorf("critic child runs with GOWORK=%q, want off;"+
			" a workspace makes `go list -m` multi-line and the evidence resolver matches no package", seen)
	}
}

// The child still has to be told where the corpus and the runtime are; turning
// the workspace off must not disturb the arguments the gate depends on.
func TestSpecificationCriticCommandPassesItsRootsAndProfile(t *testing.T) {
	cmd := specificationCriticCommand("/bin/agent", "/catalog/profile.yaml", "/repo", "/core")
	args := strings.Join(cmd.Args, " ")
	for _, want := range []string{
		"--profile /catalog/profile.yaml",
		"--directory /repo",
		"--core-root /core",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("critic args %q omit %q", args, want)
		}
	}
	if cmd.Dir != "/repo" {
		t.Errorf("critic runs in %q, want the application root /repo", cmd.Dir)
	}
}
