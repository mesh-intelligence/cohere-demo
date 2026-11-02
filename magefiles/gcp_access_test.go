// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The access section reads from gcp.yaml beside the root; an absent file is
// the empty default, and the allowlist must name IAM principals.
func TestGcpAccessReadsTheAllowlistFromGcpYaml(t *testing.T) {
	root := t.TempDir()
	if access, err := loadGcpAccess(root); err != nil || access.Host != "" || len(access.IAPMembers) != 0 {
		t.Fatalf("an absent gcp.yaml = %+v, %v", access, err)
	}
	writePackageTestFile(t, filepath.Join(root, "gcp.yaml"),
		"project: p\ncohere_demo:\n  iap_members: [user:owner@example.com]\n")
	access, err := loadGcpAccess(root)
	if err != nil || len(access.IAPMembers) != 1 || access.IAPMembers[0] != "user:owner@example.com" {
		t.Fatalf("access = %+v, %v", access, err)
	}
	if err := requireIAPMembers(access); err != nil {
		t.Errorf("a user principal was refused: %v", err)
	}
	if err := requireIAPMembers(gcpAccess{}); err == nil || !strings.Contains(err.Error(), "iap_members") {
		t.Errorf("an empty allowlist = %v", err)
	}
	if err := requireIAPMembers(gcpAccess{IAPMembers: []string{"owner@example.com"}}); err == nil {
		t.Error("a bare email was accepted as an IAM principal")
	}
}

// With no configured host, the chatbot is reached at the address's sslip.io
// name, which needs no DNS record.
func TestGcpExposureHostDefaultsToSslip(t *testing.T) {
	if host := gcpExposureHost(gcpAccess{}, "34.1.2.3"); host != "34-1-2-3.sslip.io" {
		t.Errorf("default host = %s", host)
	}
	if host := gcpExposureHost(gcpAccess{Host: "demo.example.com"}, "34.1.2.3"); host != "demo.example.com" {
		t.Errorf("configured host = %s", host)
	}
}

// The GKE exposure is gated off by default: the kind control-plane render
// keeps all three hosts and /provisioning, and renders no GKE resource.
func TestKindRenderCarriesNoGkeExposure(t *testing.T) {
	chartDir := findChartDir(t)
	out, err := exec.Command("helm", "template", cohereApplicationRelease, chartDir,
		"--namespace", cohereApplicationNamespace,
		"--values", filepath.Join(chartDir, "ci", "kind-control-plane-values.yaml")).CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v\n%s", err, firstRunes(string(out), 800))
	}
	output := string(out)
	for _, absent := range []string{"kind: ManagedCertificate", "kind: BackendConfig",
		"cloud.google.com/backend-config", "kubernetes.io/ingress.class: gce"} {
		if strings.Contains(output, absent) {
			t.Errorf("the kind render carries %s", absent)
		}
	}
	for _, want := range []string{"path: /provisioning", `host: "observer.localhost"`, `host: "telemetry.localhost"`} {
		if !strings.Contains(output, want) {
			t.Errorf("the kind render lost %s", want)
		}
	}
}
