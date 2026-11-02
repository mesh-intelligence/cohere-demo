// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The binding names derive from the project and application exactly as the
// environment root (infra/gcp/modules/telemetry) creates them.
func TestGcpConfigForApplicationMatchesTheTerraformNames(t *testing.T) {
	application := gcpConfig{Project: "p", Region: "r", Cluster: "c"}.ForApplication("cohere-demo")
	if application.Bucket != "p-cohere-demo-telemetry" || application.BucketURL() != "gs://p-cohere-demo-telemetry" ||
		application.GSAEmail() != "cohere-demo-objectstore@p.iam.gserviceaccount.com" ||
		application.Namespace != "app-cohere-demo" || application.KSA != "default" ||
		application.KSAAnnotation() != application.GSAEmail() || application.Cluster != "c" {
		t.Errorf("binding = %+v", application)
	}
}

// Preflight names the remedy for a missing gcloud, a missing project, no
// active account, and an unreachable project.
func TestGcpPreflightNamesEachRemedy(t *testing.T) {
	restore := gcpLookPath
	defer func() { gcpLookPath = restore }()
	gcpLookPath = func(string) (string, error) { return "", errors.New("not found") }
	if err := gcpPreflight(nil, gcpConfig{Project: "p"}); err == nil || !strings.Contains(err.Error(), "install the Google Cloud CLI") {
		t.Errorf("no gcloud = %v", err)
	}
	gcpLookPath = func(string) (string, error) { return "/bin/gcloud", nil }
	if err := gcpPreflight(nil, gcpConfig{}); err == nil || !strings.Contains(err.Error(), gcpConfigFile) {
		t.Errorf("no project = %v", err)
	}
	noAccount := func(string, ...string) ([]byte, error) { return nil, nil }
	if err := gcpPreflight(noAccount, gcpConfig{Project: "p"}); err == nil || !strings.Contains(err.Error(), "gcloud auth login") {
		t.Errorf("no account = %v", err)
	}
	unreachable := func(name string, args ...string) ([]byte, error) {
		if args[0] == "auth" {
			return []byte("owner@example.com\n"), nil
		}
		return []byte("PERMISSION_DENIED"), errors.New("exit status 1")
	}
	if err := gcpPreflight(unreachable, gcpConfig{Project: "p"}); err == nil || !strings.Contains(err.Error(), `"p"`) {
		t.Errorf("unreachable project = %v", err)
	}
	ok := func(name string, args ...string) ([]byte, error) { return []byte("owner@example.com\n"), nil }
	if err := gcpPreflight(ok, gcpConfig{Project: "p"}); err != nil {
		t.Errorf("a ready workstation was refused: %v", err)
	}
}

// The KSA annotation names the run's kubeconfig, the application namespace,
// and the Google service account.
func TestGcpAnnotateKSAPointsTheKSAAtTheServiceAccount(t *testing.T) {
	var got string
	run := func(name string, args ...string) ([]byte, error) {
		got = name + " " + strings.Join(args, " ")
		return nil, nil
	}
	application := gcpConfig{Project: "p"}.ForApplication("cohere-demo")
	if err := gcpAnnotateKSA(run, application, "/tmp/kc"); err != nil {
		t.Fatal(err)
	}
	want := "kubectl --kubeconfig /tmp/kc --namespace app-cohere-demo annotate serviceaccount default " +
		"iam.gke.io/gcp-service-account=cohere-demo-objectstore@p.iam.gserviceaccount.com --overwrite"
	if got != want {
		t.Errorf("annotate = %s", got)
	}
}

// A namespace is created and labelled for apprig, reused when apprig already
// owns it, and refused when another manager does.
func TestEnsureGcpNamespaceCreatesReusesAndRefuses(t *testing.T) {
	var calls []string
	absent := func(name string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return nil, nil
	}
	created, err := ensureGcpNamespace(absent, "app-cohere-demo")
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if len(calls) != 3 || !strings.HasPrefix(calls[1], "create namespace app-cohere-demo") ||
		!strings.Contains(calls[2], "app.kubernetes.io/managed-by=apprig") {
		t.Errorf("calls = %q", calls)
	}
	owned := func(string, ...string) ([]byte, error) {
		return []byte(`{"metadata":{"labels":{"app.kubernetes.io/managed-by":"apprig"}}}`), nil
	}
	if created, err := ensureGcpNamespace(owned, "app-cohere-demo"); err != nil || created {
		t.Errorf("an owned namespace: created=%v err=%v", created, err)
	}
	foreign := func(string, ...string) ([]byte, error) {
		return []byte(`{"metadata":{"labels":{"app.kubernetes.io/managed-by":"Helm"}}}`), nil
	}
	if _, err := ensureGcpNamespace(foreign, "app-cohere-demo"); err == nil || !strings.Contains(err.Error(), "refusing adoption") {
		t.Errorf("a foreign namespace = %v", err)
	}
	if _, err := ensureGcpNamespace(absent, "App_Cohere"); err == nil {
		t.Error("a namespace that is not a DNS label was accepted")
	}
}

// Deletion waits for the namespace to go and runs no kind data-plane check:
// GKE Autopilot has no kube-proxy pods to wait for (declarative-agents#470).
func TestDeleteGcpNamespaceEndsAtTheWait(t *testing.T) {
	var calls []string
	owned := func(name string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "get" {
			return []byte(`{"metadata":{"labels":{"app.kubernetes.io/managed-by":"apprig"}}}`), nil
		}
		return nil, nil
	}
	if err := deleteGcpNamespace(owned, "app-cohere-demo"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "delete namespace app-cohere-demo") || !strings.Contains(joined, "wait --for=delete namespace/app-cohere-demo") {
		t.Errorf("calls = %q", calls)
	}
	if strings.Contains(joined, "kube-proxy") || strings.Contains(joined, "kube-system") {
		t.Errorf("deletion ran a kind data-plane check: %q", calls)
	}
	gone := func(string, ...string) ([]byte, error) { return nil, nil }
	if err := deleteGcpNamespace(gone, "app-cohere-demo"); err != nil {
		t.Errorf("an absent namespace = %v", err)
	}
	foreign := func(string, ...string) ([]byte, error) {
		return []byte(`{"metadata":{"labels":{"app.kubernetes.io/managed-by":"Helm"}}}`), nil
	}
	if err := deleteGcpNamespace(foreign, "app-cohere-demo"); err == nil {
		t.Error("a foreign namespace was deleted")
	}
}

// A kubeconfig-bound run replaces any ambient KUBECONFIG in the child only.
func TestGcpKubeconfigRunReplacesTheAmbientKubeconfig(t *testing.T) {
	kept := gcpWithoutKubeconfig([]string{"PATH=/bin", "KUBECONFIG=/home/me/.kube/config", "HOME=/home/me"})
	if len(kept) != 2 || strings.Contains(strings.Join(kept, " "), "KUBECONFIG") {
		t.Errorf("kept = %q", kept)
	}
}

// No GCP code imports declarative-agents' GCP, Terraform, or kind rig
// packages (#877); only apprig, the application deploy machine, remains.
func TestGcpCodeImportsNoUpstreamRig(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "gcp_*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("gcp files = %v, %v", files, err)
	}
	files = append(files, filepath.Join(dir, "exporter_publish.go"))
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, banned := range []string{"magefiles/gcprig\"", "magefiles/tfrig\"", "magefiles/kindrig\""} {
			if strings.Contains(string(data), banned) {
				t.Errorf("%s imports declarative-agents/%s", filepath.Base(file), strings.TrimSuffix(banned, "\""))
			}
		}
	}
}

// Each phase line lands in the day's log under build/gcp with its time and
// verb; nothing is written before a verb opens the log (#881).
func TestGcpPhaseLogAppendsDatedLines(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "build", "gcp")
	now := time.Date(2026, 10, 4, 9, 30, 0, 0, time.UTC)
	for _, line := range []string{"phase target=c name=dev elapsed=1s outcome=applied", "phase target=c name=deploy elapsed=2s outcome=failed"} {
		if err := appendGcpPhaseLog(dir, "gcp:up", now, line); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "2026-10-04.log"))
	if err != nil {
		t.Fatal(err)
	}
	want := "2026-10-04T09:30:00Z gcp:up phase target=c name=dev elapsed=1s outcome=applied\n" +
		"2026-10-04T09:30:00Z gcp:up phase target=c name=deploy elapsed=2s outcome=failed\n"
	if string(data) != want {
		t.Errorf("log =\n%s\nwant\n%s", data, want)
	}
	if err := appendGcpPhaseLog("", "gcp:up", now, "x"); err != nil {
		t.Errorf("an unopened log = %v", err)
	}
}

func TestGcpLogOutcomeWritesThroughTheOpenedLog(t *testing.T) {
	saved := gcpPhaseLog
	t.Cleanup(func() { gcpPhaseLog = saved })
	root := t.TempDir()
	gcpOpenPhaseLog(root, "gcp:verify")
	failure := errors.New("no grounded answer")
	if err := gcpLogOutcome("cohere-demo-dev", "verify", "passed", time.Now(), failure); err != failure {
		t.Errorf("gcpLogOutcome returned %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "build", "gcp", time.Now().Format(time.DateOnly)+".log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), " gcp:verify phase target=cohere-demo-dev name=verify ") ||
		!strings.HasSuffix(string(data), "outcome=failed\n") {
		t.Errorf("log = %q", data)
	}
}
