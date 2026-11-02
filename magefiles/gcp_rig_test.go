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

// gcpTestRoot is a repository root with the two environment directories the
// settings validate against, and the gcp.yaml the test writes.
func gcpTestRoot(t *testing.T, gcpYAML string) string {
	t.Helper()
	root := t.TempDir()
	for _, env := range []string{"dev", "prod"} {
		if err := os.MkdirAll(filepath.Join(root, gcpTerraformRoot, "envs", env), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if gcpYAML != "" {
		writePackageTestFile(t, filepath.Join(root, "gcp.yaml"), gcpYAML)
	}
	return root
}

// gcp.yaml selects the environment; defaults are literal, an unknown
// environment and a production run aimed at the development project are
// refused.
func TestGcpSettingsSelectTheEnvironment(t *testing.T) {
	settings, err := loadGcpSettings(gcpTestRoot(t, "project: p\n"))
	if err != nil {
		t.Fatal(err)
	}
	if settings.Environment != "dev" || settings.Region != "us-central1" || settings.project() != "p" {
		t.Errorf("defaults = %+v", settings)
	}
	if _, err := loadGcpSettings(gcpTestRoot(t, "")); err == nil || !strings.Contains(err.Error(), "no project") {
		t.Errorf("a missing project = %v", err)
	}
	if _, err := loadGcpSettings(gcpTestRoot(t, "project: p\nenvironment: staging\n")); err == nil ||
		!strings.Contains(err.Error(), "envs/staging") {
		t.Errorf("an environment with no root = %v", err)
	}
	for _, yaml := range []string{"project: p\nenvironment: prod\n", "project: p\nenvironment: prod\nprod_project: p\n"} {
		if _, err := loadGcpSettings(gcpTestRoot(t, yaml)); err == nil || !strings.Contains(err.Error(), "prod_project") {
			t.Errorf("prod into the dev project (%q) = %v", yaml, err)
		}
	}
	prod, err := loadGcpSettings(gcpTestRoot(t, "project: p\nenvironment: prod\nprod_project: q\n"))
	if err != nil || prod.project() != "q" || prod.stateBucket() != "q-tfstate" {
		t.Errorf("prod = %+v, %v", prod, err)
	}
}

// The environment root keeps its state in the bootstrap bucket under its own
// prefix and takes every setting as a variable; no credential is a variable.
// The bootstrap root has no backend.
func TestGcpRootsCarryTheirBackendAndNoCredential(t *testing.T) {
	root := gcpTestRoot(t, "project: p\n")
	settings, err := loadGcpSettings(root)
	if err != nil {
		t.Fatal(err)
	}
	environment := gcpEnvironmentRoot(root, settings, "octocat")
	if environment.Dir != filepath.Join(root, "infra", "gcp", "envs", "dev") {
		t.Errorf("dir = %s", environment.Dir)
	}
	if environment.Backend["bucket"] != "p-tfstate" || environment.Backend["prefix"] != "cohere-demo/dev" {
		t.Errorf("backend = %v", environment.Backend)
	}
	if environment.Vars["project"] != "p" || environment.Vars["ghcr_username"] != "octocat" ||
		environment.Vars["region"] != "us-central1" {
		t.Errorf("vars = %v", environment.Vars)
	}
	if _, set := environment.Vars["cluster"]; set {
		t.Error("an unset cluster overrode the root's default")
	}
	for name := range environment.Vars {
		lower := strings.ToLower(name)
		if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "key") ||
			strings.Contains(lower, "password") {
			t.Errorf("variable %q looks like a credential; values reach Secret Manager through gcp:secrets only", name)
		}
	}
	for _, moved := range []string{"host", "iap_members"} {
		if _, set := environment.Vars[moved]; set {
			t.Errorf("the environment root still takes %s; the exposure root owns it (#885)", moved)
		}
	}
	if bootstrap := gcpBootstrapRoot(root, settings); len(bootstrap.Backend) != 0 ||
		!strings.HasSuffix(bootstrap.Dir, filepath.Join("infra", "gcp", "bootstrap")) {
		t.Errorf("bootstrap = %+v", bootstrap)
	}
}

// The exposure root keeps its state beside the environment's under its own
// prefix and takes the cluster, the bootstrap address, the access settings,
// and the OAuth client id; the client secret never becomes a variable (#885).
func TestGcpExposureRootCarriesClusterAddressAndAccess(t *testing.T) {
	root := gcpTestRoot(t, "project: p\n")
	settings, err := loadGcpSettings(root)
	if err != nil {
		t.Fatal(err)
	}
	access := gcpAccess{Host: "demo.example.com", IAPMembers: []string{"user:owner@example.com"}}
	exposure := gcpExposureRoot(root, settings, access, "cohere-demo-dev", "34.1.2.3", "client.apps.googleusercontent.com")
	if exposure.Dir != filepath.Join(root, "infra", "gcp", "envs", "dev", "exposure") {
		t.Errorf("dir = %s", exposure.Dir)
	}
	if exposure.Backend["bucket"] != "p-tfstate" || exposure.Backend["prefix"] != "cohere-demo/dev-exposure" {
		t.Errorf("backend = %v", exposure.Backend)
	}
	want := map[string]any{
		"project": "p", "region": "us-central1", "cluster": "cohere-demo-dev",
		"address_name": gcpIngressAddress, "address": "34.1.2.3", "host": "demo.example.com",
		"iap_client_id": "client.apps.googleusercontent.com",
	}
	for name, value := range want {
		if exposure.Vars[name] != value {
			t.Errorf("%s = %v, want %v", name, exposure.Vars[name], value)
		}
	}
	if members, _ := exposure.Vars["iap_members"].([]string); len(members) != 1 || members[0] != "user:owner@example.com" {
		t.Errorf("iap_members = %v", exposure.Vars["iap_members"])
	}
	for name := range exposure.Vars {
		lower := strings.ToLower(name)
		if strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "password") {
			t.Errorf("variable %q looks like a credential", name)
		}
	}
}

// The bootstrap root adopts the reserved address only when it exists and
// local state does not hold it: gcp:deploy reserved it with gcloud before
// Terraform owned it.
func TestGcpAddressImportsOnlyWhenUnheld(t *testing.T) {
	held := map[string]bool{gcpBootstrapIngressAddress: true}
	cases := []struct {
		held   map[string]bool
		exists bool
		want   bool
	}{
		{nil, true, true},
		{map[string]bool{gcpBootstrapBucketAddress: true}, true, true},
		{held, true, false},
		{nil, false, false},
	}
	for _, c := range cases {
		if got := needsGcpAddressImport(c.held, c.exists); got != c.want {
			t.Errorf("held=%v exists=%v: import=%v, want %v", c.held, c.exists, got, c.want)
		}
	}
}

// gcp:deploy reads the address Terraform created and never creates one.
func TestGcpDescribeAddressOnlyReads(t *testing.T) {
	var calls []string
	run := func(name string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return []byte("34.1.2.3\n"), nil
	}
	if address, err := gcpDescribeAddress(run, "p"); err != nil || address != "34.1.2.3" {
		t.Fatalf("address = %q, %v", address, err)
	}
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "compute addresses describe "+gcpIngressAddress) ||
		!strings.Contains(calls[0], "--project p") {
		t.Errorf("calls = %q", calls)
	}
	missing := func(name string, args ...string) ([]byte, error) {
		return []byte("ERROR: NOT_FOUND"), errors.New("exit status 1")
	}
	if _, err := gcpDescribeAddress(missing, "p"); err == nil || !strings.Contains(err.Error(), gcpIngressAddress) {
		t.Errorf("a missing address = %v", err)
	}
}

// A destroy needs the exact token for its environment; anything else is
// refused before any cloud call.
func TestGcpDownTokensAreExact(t *testing.T) {
	if err := requireGcpConfirmation("delete-cohere-demo-dev", gcpDownConfirmationPrefix+"dev"); err != nil {
		t.Errorf("the dev token was refused: %v", err)
	}
	for _, wrong := range []string{"", "delete-cohere-demo-prod", "delete-cohere-demo", "yes"} {
		if err := requireGcpConfirmation(wrong, gcpDownConfirmationPrefix+"dev"); err == nil {
			t.Errorf("token %q was accepted for dev", wrong)
		}
	}
	if err := requireGcpConfirmation("delete-cohere-demo-dev", gcpBootstrapConfirmation); err == nil {
		t.Error("an environment token removed the state bucket")
	}
}

// The bootstrap root imports the state bucket only when the bucket exists
// and local state does not already hold it.
func TestGcpBootstrapImportsOnlyAnUnheldBucket(t *testing.T) {
	held := map[string]bool{gcpBootstrapBucketAddress: true}
	cases := []struct {
		held   map[string]bool
		exists bool
		want   bool
	}{
		{nil, true, true},
		{held, true, false},
		{nil, false, false},
		{held, false, false},
	}
	for _, c := range cases {
		if got := needsGcpBootstrapImport(c.held, c.exists); got != c.want {
			t.Errorf("held=%v exists=%v: import=%v, want %v", c.held, c.exists, got, c.want)
		}
	}

	// A fresh machine has no local state file, and terraform state list fails
	// there instead of printing nothing (found on the first live gcp:up).
	fresh := errors.New("terraform state list bootstrap: exit status 1\nNo state file was found!")
	if held, err := gcpHeldOrEmpty(nil, fresh); err != nil || len(held) != 0 {
		t.Errorf("no state file = %v, %v; want empty and no error", held, err)
	}
	broken := errors.New("terraform state list bootstrap: exit status 1\nError: Failed to load state")
	if _, err := gcpHeldOrEmpty(nil, broken); err == nil {
		t.Error("an unreadable state was treated as empty")
	}
}

// The state bucket is shared with any other root that keeps state in it, so
// removal sees every live object, not only cohere-demo's.
func TestGcpStateObjectsSeeEveryPrefix(t *testing.T) {
	listing := "gs://p-tfstate/applications/default.tfstate\ngs://p-tfstate/cohere-demo/dev/default.tfstate\n" +
		"gs://p-tfstate/cohere-demo/dev-exposure/default.tfstate\ngs://p-tfstate/rig/\n"
	run := func(name string, args ...string) ([]byte, error) { return []byte(listing), nil }
	objects, err := gcpStateObjects(run, "p-tfstate")
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 3 || objects[0] != "applications/default.tfstate" || objects[1] != "cohere-demo/dev-exposure/default.tfstate" ||
		objects[2] != "cohere-demo/dev/default.tfstate" {
		t.Errorf("objects = %q", objects)
	}
	empty := func(name string, args ...string) ([]byte, error) {
		return []byte("ERROR: (gcloud.storage.ls) One or more URLs matched no objects."), errors.New("exit status 1")
	}
	if objects, err := gcpStateObjects(empty, "p-tfstate"); err != nil || len(objects) != 0 {
		t.Errorf("an empty bucket = %q, %v", objects, err)
	}
	denied := func(name string, args ...string) ([]byte, error) {
		return []byte("ERROR: 403 does not have storage.objects.list access"), errors.New("exit status 1")
	}
	if _, err := gcpStateObjects(denied, "p-tfstate"); err == nil {
		t.Error("an unreadable bucket was reported empty")
	}
}

// An apply that collides with an existing name explains the one-time
// release from upstream's state; any other failure passes through.
func TestGcpCollisionNamesTheReleaseSteps(t *testing.T) {
	settings := gcpSettings{Project: "p", Environment: "dev"}
	collision := errors.New("Error: Error creating Bucket: googleapi: Error 409: Your previous request to create the named bucket succeeded and you already own it")
	err := explainGcpCollision(collision, settings)
	for _, want := range []string{"terraform state rm", "gs://p-tfstate", "cohere-demo#870"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("collision explanation lacks %q:\n%v", want, err)
		}
	}
	other := errors.New("Error: quota exceeded")
	if explained := explainGcpCollision(other, settings); explained != other {
		t.Errorf("an unrelated failure was rewritten: %v", explained)
	}
}

// The drain skips a cluster that is absent or not running, empties every
// application namespace while sparing the system ones, and waits for the
// volumes to go.
func TestDrainGcpVolumesSparesSystemNamespaces(t *testing.T) {
	absent := func(name string, args ...string) ([]byte, error) {
		return []byte("ERROR: (gcloud.container.clusters.describe) ResponseError: code=404, message=Not found"), errors.New("exit status 1")
	}
	if err := drainGcpVolumes(absent, gcpConfig{Cluster: "c"}); err != nil {
		t.Errorf("an absent cluster = %v", err)
	}

	restore := gcpWriteKubeconfig
	gcpWriteKubeconfig = func(gcpConfig, string) error { return nil }
	defer func() { gcpWriteKubeconfig = restore }()
	var deleted []string
	volumeChecks := 0
	running := func(name string, args ...string) ([]byte, error) {
		line := strings.Join(args, " ")
		switch {
		case name == "gcloud":
			return []byte("RUNNING\n"), nil
		case strings.Contains(line, "get namespaces"):
			return []byte("namespace/kube-system\nnamespace/gke-managed-system\nnamespace/app-cohere-demo\nnamespace/default\n"), nil
		case strings.Contains(line, "delete"):
			deleted = append(deleted, args[3])
			return nil, nil
		case strings.Contains(line, "persistentvolumes"):
			volumeChecks++
			if volumeChecks == 1 {
				return []byte("pvc-1\n"), nil
			}
			return nil, nil
		}
		return nil, nil
	}
	restoreSleep := gcpDrainSleep
	gcpDrainSleep = func(time.Duration) {}
	defer func() { gcpDrainSleep = restoreSleep }()
	if err := drainGcpVolumes(running, gcpConfig{Cluster: "c"}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(deleted, " ")
	if strings.Contains(joined, "kube-system") || strings.Contains(joined, "gke-managed-system") ||
		!strings.Contains(joined, "app-cohere-demo") || !strings.Contains(joined, "default") || volumeChecks != 2 {
		t.Errorf("drained %q with %d volume checks", deleted, volumeChecks)
	}
}

// The ghcr.io repository waits for the token: Artifact Registry reads the
// token version when it creates that repository (found on the first live
// gcp:up), so gcp:up passes the version check into the root.
func TestGcpGhcrTokenReadyNeedsAnEnabledVersion(t *testing.T) {
	stored := func(string, ...string) ([]byte, error) { return []byte("1\n"), nil }
	empty := func(string, ...string) ([]byte, error) {
		return []byte("WARNING: The following filter keys were not present in any resource : state\n"), nil
	}
	absent := func(string, ...string) ([]byte, error) {
		return []byte("NOT_FOUND: Secret [cohere-demo-ghcr-pull] not found"), errors.New("exit status 1")
	}
	if !gcpGhcrTokenReady(stored, "p") || gcpGhcrTokenReady(empty, "p") || gcpGhcrTokenReady(absent, "p") {
		t.Error("ghcr_token_ready must be true only for a stored version")
	}
}

// After a destroy the environment's state object is empty; removing it is
// what lets gcp:bootstrapDown retire the bucket (found after the first live
// gcp:down left cohere-demo/dev/default.tfstate behind).
func TestRemoveGcpStateObjectNamesTheEnvironmentPrefix(t *testing.T) {
	var removed []string
	run := func(name string, args ...string) ([]byte, error) {
		removed = append(removed, strings.Join(args, " "))
		return nil, nil
	}
	backend := map[string]string{"bucket": "p-tfstate", "prefix": "cohere-demo/dev"}
	if err := removeGcpStateObject(run, backend); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "storage rm gs://p-tfstate/cohere-demo/dev/default.tfstate" {
		t.Errorf("removed %q", removed)
	}
	gone := func(string, ...string) ([]byte, error) {
		return []byte("ERROR: (gcloud.storage.rm) The following URLs matched no objects or files"), errors.New("exit status 1")
	}
	if err := removeGcpStateObject(gone, backend); err != nil {
		t.Errorf("an already-removed state object = %v; want no error", err)
	}
}
