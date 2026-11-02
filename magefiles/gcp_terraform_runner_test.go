// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type tfCall struct {
	env  []string
	args []string
}

type exitError int

func (e exitError) Error() string { return "exit status " + string(rune('0'+int(e))) }
func (e exitError) ExitCode() int { return int(e) }

func tfRecorder(calls *[]tfCall, out string, err error) tfRunner {
	return func(env []string, args ...string) ([]byte, error) {
		*calls = append(*calls, tfCall{env: env, args: args})
		return []byte(out), err
	}
}

func tfTestRig(t *testing.T, run tfRunner) (tfTerraform, tfRoot) {
	t.Helper()
	work := t.TempDir()
	return tfTerraform{Run: run, WorkDir: work, CacheDir: filepath.Join(work, "cache")},
		tfRoot{Dir: "/repo/infra/gcp/rig", Vars: map[string]any{"project": "p-1"},
			Backend: map[string]string{"prefix": "rig", "bucket": "p-1-tfstate"}}
}

// eng11: init runs in the root with the committed lock file, sorted backend
// settings, and the CLI configuration that sets the plugin cache.
func TestTerraformRunnerInitRunsInTheRootAgainstTheLockFile(t *testing.T) {
	var calls []tfCall
	tf, root := tfTestRig(t, tfRecorder(&calls, "", nil))
	if err := tf.Init(root); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(calls[0].args, " ")
	want := "-chdir=/repo/infra/gcp/rig init -input=false -lockfile=readonly -backend-config=bucket=p-1-tfstate -backend-config=prefix=rig"
	if got != want {
		t.Fatalf("init args\n got %s\nwant %s", got, want)
	}
	config := strings.TrimPrefix(calls[0].env[0], "TF_CLI_CONFIG_FILE=")
	data, err := os.ReadFile(config)
	if err != nil || !strings.Contains(string(data), `plugin_cache_dir = "`+tf.CacheDir+`"`) {
		t.Fatalf("CLI config %s = %q, %v", config, data, err)
	}
}

// eng11: settings arrive only as the rendered tfvars file, and plan's exit 2
// reports changes rather than an error.
func TestTerraformRunnerPlanRendersVarsAndReadsTheDetailedExitCode(t *testing.T) {
	var calls []tfCall
	tf, root := tfTestRig(t, tfRecorder(&calls, "", exitError(2)))
	plan, changes, err := tf.Plan(root, false)
	if err != nil || !changes {
		t.Fatalf("Plan = %v, %v; want changes", changes, err)
	}
	vars := filepath.Join(tf.WorkDir, "rig", "rig.tfvars.json")
	data, _ := os.ReadFile(vars)
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil || decoded["project"] != "p-1" {
		t.Fatalf("tfvars = %s, %v", data, err)
	}
	args := strings.Join(calls[0].args, " ")
	for _, want := range []string{"plan -input=false -detailed-exitcode", "-var-file=" + vars, "-out=" + plan} {
		if !strings.Contains(args, want) {
			t.Fatalf("plan args %q lack %q", args, want)
		}
	}

	calls = nil
	tf.Run = tfRecorder(&calls, "", nil)
	if _, changes, err := tf.Plan(root, true); err != nil || changes {
		t.Fatalf("destroy Plan = %v, %v; want no changes", changes, err)
	}
	if !strings.HasSuffix(strings.Join(calls[0].args, " "), "-destroy") {
		t.Fatalf("destroy plan args %v", calls[0].args)
	}
}

// eng11: a failure names the root and the step and keeps terraform's text.
func TestTerraformRunnerFailureNamesTheRootAndTheStep(t *testing.T) {
	var calls []tfCall
	tf, root := tfTestRig(t, tfRecorder(&calls, "Error: googleapi: 403 Forbidden", exitError(1)))
	_, _, err := tf.Plan(root, false)
	if err == nil || !strings.Contains(err.Error(), "terraform plan rig") || !strings.Contains(err.Error(), "403 Forbidden") {
		t.Fatalf("Plan error = %v", err)
	}
	if err := tf.Apply(root, "/work/plan.tfplan"); err == nil || !strings.Contains(err.Error(), "terraform apply rig") {
		t.Fatalf("Apply error = %v", err)
	}
}

// eng11: no TF_VAR_* reaches the child, and the overlay replaces what it names.
func TestTerraformRunnerChildEnvironmentDropsTFVarsAndReplacesTheOverlay(t *testing.T) {
	got := tfChildEnvironment(
		[]string{"PATH=/bin", "TF_VAR_project=leak", "TF_CLI_CONFIG_FILE=/old"},
		[]string{"TF_CLI_CONFIG_FILE=/new"})
	if strings.Join(got, ",") != "PATH=/bin,TF_CLI_CONFIG_FILE=/new" {
		t.Fatalf("child environment = %v", got)
	}
}

func TestTerraformRunnerPreflightRefusesAMissingBinaryAndReadsTheVersion(t *testing.T) {
	restore := tfLookPath
	t.Cleanup(func() { tfLookPath = restore })
	tfLookPath = func(string) (string, error) { return "", errors.New("not found") }
	if _, err := tfPreflight(nil); err == nil || !strings.Contains(err.Error(), "brew install hashicorp/tap/terraform") {
		t.Fatalf("missing binary: %v", err)
	}
	tfLookPath = func(string) (string, error) { return "/usr/bin/terraform", nil }
	var calls []tfCall
	version, err := tfPreflight(tfRecorder(&calls, `{"terraform_version":"1.16.4"}`, nil))
	if err != nil || version != "1.16.4" {
		t.Fatalf("Preflight = %q, %v", version, err)
	}
}

// eng11 end to end: a local-only root (terraform_data needs no provider
// download) inits, plans changes, applies its plan, reads its output, and
// destroys. Skipped, naming the tool, on a machine without terraform.
func TestTerraformRunnerLocalRootRunsEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform is not installed")
	}
	dir := t.TempDir()
	hcl := "variable \"greeting\" { type = string }\n" +
		"resource \"terraform_data\" \"note\" { input = var.greeting }\n" +
		"output \"note\" { value = terraform_data.note.output }\n"
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	tf := tfTerraform{Run: tfDefaultRun, WorkDir: work, CacheDir: filepath.Join(work, "cache")}
	root := tfRoot{Dir: dir, Vars: map[string]any{"greeting": "hello"}}
	if err := tf.Init(root); err != nil {
		t.Fatal(err)
	}
	plan, changes, err := tf.Plan(root, false)
	if err != nil || !changes {
		t.Fatalf("Plan = %v, %v", changes, err)
	}
	if err := tf.Apply(root, plan); err != nil {
		t.Fatal(err)
	}
	outputs, err := tf.Output(root)
	if err != nil || string(outputs["note"]) != `"hello"` {
		t.Fatalf("Output = %s, %v", outputs["note"], err)
	}
	if _, changes, err := tf.Plan(root, false); err != nil || changes {
		t.Fatalf("second Plan = %v, %v; want no changes", changes, err)
	}
	plan, _, err = tf.Plan(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := tf.Apply(root, plan); err != nil {
		t.Fatal(err)
	}
}

// eng11: state list reads addresses, and import runs with the rendered vars.
func TestTerraformRunnerStateListAndImport(t *testing.T) {
	var calls []tfCall
	tf, root := tfTestRig(t, tfRecorder(&calls, "google_storage_bucket.application[\"a\"]\n\n", nil))
	addresses, err := tf.StateList(root)
	if err != nil || !addresses[`google_storage_bucket.application["a"]`] || len(addresses) != 1 {
		t.Fatalf("StateList = %v, %v", addresses, err)
	}
	if err := tf.Import(root, `google_storage_bucket.application["a"]`, "p-1/bucket-a"); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(calls[1].args, " ")
	if !strings.Contains(args, "import -input=false -var-file=") || !strings.HasSuffix(args, `google_storage_bucket.application["a"] p-1/bucket-a`) {
		t.Fatalf("import args %q", args)
	}
}
