// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// The exporter workload (srd013 R3.2, R3.3, R6.1): its own Deployment,
// Service, and NetworkPolicy, with egress denied outright and ingress scoped
// to the chatbot's export calls and the observer's monitor read. Suite cases
// 8 and 10's rendered halves.

func renderExporterChart(t *testing.T, extraArgs ...string) string {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chart := findChartDir(t)
	args := append([]string{"template", "relx", chart, "--namespace", "nsy"}, extraArgs...)
	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	return string(out)
}

// TestExporterPolicyDeniesEgressAndScopesIngress pins the isolation boundary:
// policyTypes carries Egress with no egress rule, so the rendering toolchain
// has no outbound path whatever its input contains, and the only admitted
// callers are the chatbot on the export port and the observer on the monitor
// port.
func TestExporterPolicyDeniesEgressAndScopesIngress(t *testing.T) {
	t.Parallel()
	render := renderExporterChart(t)
	idx := strings.Index(render, "name: relx-chatbot-mesh-exporter\n  labels:")
	if idx < 0 {
		t.Fatal("the render carries no exporter objects")
	}
	policyIdx := strings.LastIndex(render, "kind: NetworkPolicy")
	for policyIdx >= 0 {
		section := render[policyIdx:]
		if end := strings.Index(section[15:], "\n---"); end > 0 {
			section = section[:end+15]
		}
		if strings.Contains(section, "component: exporter") {
			if !strings.Contains(section, "policyTypes: [Ingress, Egress]") {
				t.Error("the exporter policy does not declare both directions")
			}
			if strings.Contains(section, "\n  egress:") {
				t.Error("the exporter policy carries an egress rule; srd013 R3.2 denies all egress")
			}
			if !strings.Contains(section, "component: chatbot") {
				t.Error("the export port does not admit the chatbot caller")
			}
			if !strings.Contains(section, "component: observer") {
				t.Error("the monitor port does not admit the observer")
			}
			return
		}
		policyIdx = strings.LastIndex(render[:policyIdx], "kind: NetworkPolicy")
	}
	t.Fatal("the render carries no exporter NetworkPolicy")
}

// TestExporterWorkloadRendersByToggle pins R6.1's own-workload shape in both
// directions: enabled renders the Deployment, Service, and NetworkPolicy on
// the exporter image with the profiles and work mounts; disabled renders no
// exporter object at all.
func TestExporterWorkloadRendersByToggle(t *testing.T) {
	t.Parallel()
	render := renderExporterChart(t)
	for _, want := range []string{
		"app.kubernetes.io/component: exporter",
		"agents/exporter/profile.yaml",
		"name: EXPORTER_WORK_DIR",
		"containerPort: 18120",
	} {
		if !strings.Contains(render, want) {
			t.Errorf("the enabled render is missing %q", want)
		}
	}
	disabled := renderExporterChart(t, "--set", "exporter.enabled=false")
	if strings.Contains(disabled, "app.kubernetes.io/component: exporter") {
		t.Error("the disabled render still carries exporter objects")
	}
}
