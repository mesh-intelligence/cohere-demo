// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

// The Terraform runner the gcp:* verbs apply their roots with (#877): init
// with the committed lock file, plan to a file, apply that plan, read outputs
// and state, and import by name. Every command runs with -chdir and
// -input=false, settings arrive only as a rendered tfvars file, and a failure
// names the root and the step with Terraform's own diagnostic. Carried from
// declarative-agents' tfrig so no GCP code depends on that repository.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// tfRunner executes terraform with args and the child's environment overlay;
// tests substitute a recorder.
type tfRunner func(env []string, args ...string) ([]byte, error)

// tfDefaultRun executes terraform with the overlay appended to the inherited
// environment, after dropping what the overlay replaces and every TF_VAR_*
// entry: no root takes settings from the environment (eng11).
func tfDefaultRun(env []string, args ...string) ([]byte, error) {
	cmd := exec.Command("terraform", args...)
	cmd.Env = tfChildEnvironment(os.Environ(), env)
	return cmd.CombinedOutput()
}

func tfChildEnvironment(inherited, overlay []string) []string {
	replaced := map[string]bool{}
	for _, entry := range overlay {
		name, _, _ := strings.Cut(entry, "=")
		replaced[name] = true
	}
	out := make([]string, 0, len(inherited)+len(overlay))
	for _, entry := range inherited {
		name, _, _ := strings.Cut(entry, "=")
		if replaced[name] || strings.HasPrefix(name, "TF_VAR_") {
			continue
		}
		out = append(out, entry)
	}
	return append(out, overlay...)
}

// tfRoot is one Terraform root and what a run gives it.
type tfRoot struct {
	// Dir is the root's directory, absolute.
	Dir string
	// Vars are rendered to the root's tfvars file.
	Vars map[string]any
	// Backend holds -backend-config entries, such as the GCS bucket and
	// prefix; empty for a root with local state.
	Backend map[string]string
}

func (r tfRoot) name() string { return filepath.Base(r.Dir) }

// tfTerraform runs roots, keeping its plans, tfvars, and CLI configuration in
// WorkDir and sharing one plugin cache across roots.
type tfTerraform struct {
	Run      tfRunner
	WorkDir  string
	CacheDir string
}

// Init initializes a root against its committed lock file; a provider the
// lock does not pin fails rather than updating the lock (eng11).
func (t tfTerraform) Init(root tfRoot) error {
	args := []string{"init", "-input=false", "-lockfile=readonly"}
	keys := make([]string, 0, len(root.Backend))
	for key := range root.Backend {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "-backend-config="+key+"="+root.Backend[key])
	}
	_, err := t.run(root, "init", args...)
	return err
}

// Plan writes the root's plan to its work directory and reports whether it
// changes anything (-detailed-exitcode: 0 none, 2 changes).
func (t tfTerraform) Plan(root tfRoot, destroy bool) (string, bool, error) {
	vars, err := t.writeVars(root)
	if err != nil {
		return "", false, err
	}
	plan := filepath.Join(t.rootWorkDir(root), "plan.tfplan")
	args := []string{"plan", "-input=false", "-detailed-exitcode", "-var-file=" + vars, "-out=" + plan}
	if destroy {
		args = append(args, "-destroy")
	}
	_, err = t.run(root, "plan", args...)
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) && exit.ExitCode() == 2 {
		return plan, true, nil
	}
	return plan, false, err
}

// Apply applies a plan Plan wrote, so what was shown is what runs.
func (t tfTerraform) Apply(root tfRoot, plan string) error {
	_, err := t.run(root, "apply", "apply", "-input=false", plan)
	return err
}

// Output reads the root's outputs as JSON values.
func (t tfTerraform) Output(root tfRoot) (map[string]json.RawMessage, error) {
	out, err := t.run(root, "output", "output", "-json")
	if err != nil {
		return nil, err
	}
	var outputs map[string]struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(out, &outputs); err != nil {
		return nil, fmt.Errorf("terraform output %s: %w", root.name(), err)
	}
	values := make(map[string]json.RawMessage, len(outputs))
	for name, output := range outputs {
		values[name] = output.Value
	}
	return values, nil
}

// StateList names the resource addresses the root's state holds.
func (t tfTerraform) StateList(root tfRoot) (map[string]bool, error) {
	out, err := t.run(root, "state list", "state", "list")
	if err != nil {
		return nil, err
	}
	addresses := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			addresses[line] = true
		}
	}
	return addresses, nil
}

// Import brings one existing object under the root's state. Callers run it
// only on an explicit operator verb: adoption is never implicit (eng11).
func (t tfTerraform) Import(root tfRoot, address, id string) error {
	vars, err := t.writeVars(root)
	if err != nil {
		return err
	}
	_, err = t.run(root, "import", "import", "-input=false", "-var-file="+vars, address, id)
	return err
}

func (t tfTerraform) run(root tfRoot, step string, args ...string) ([]byte, error) {
	env, err := t.cliEnvironment()
	if err != nil {
		return nil, err
	}
	out, err := t.Run(env, append([]string{"-chdir=" + root.Dir}, args...)...)
	if err != nil && !(step == "plan" && tfExitCode(err) == 2) {
		return out, fmt.Errorf("terraform %s %s: %w\n%s", step, root.name(), err, strings.TrimSpace(string(out)))
	}
	return out, err
}

func tfExitCode(err error) int {
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

func (t tfTerraform) rootWorkDir(root tfRoot) string {
	return filepath.Join(t.WorkDir, root.name())
}

// writeVars renders the root's settings as its tfvars file in the work
// directory, the only way settings reach a root (eng11).
func (t tfTerraform) writeVars(root tfRoot) (string, error) {
	dir := t.rootWorkDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	vars := root.Vars
	if vars == nil {
		vars = map[string]any{}
	}
	data, err := json.MarshalIndent(vars, "", "  ")
	if err != nil {
		return "", fmt.Errorf("render tfvars for %s: %w", root.name(), err)
	}
	path := filepath.Join(dir, "rig.tfvars.json")
	return path, os.WriteFile(path, append(data, '\n'), 0o644)
}

// cliEnvironment writes the CLI configuration that sets the shared plugin
// cache and points Terraform at it: TF_CLI_CONFIG_FILE is the tool's own
// contract, set on the child as the GCP helpers set KUBECONFIG (eng11).
func (t tfTerraform) cliEnvironment() ([]string, error) {
	if err := os.MkdirAll(t.CacheDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(t.WorkDir, 0o755); err != nil {
		return nil, err
	}
	config := filepath.Join(t.WorkDir, "terraform.rc")
	body := "plugin_cache_dir = " + strconv.Quote(t.CacheDir) + "\n"
	if err := os.WriteFile(config, []byte(body), 0o644); err != nil {
		return nil, err
	}
	return []string{"TF_CLI_CONFIG_FILE=" + config, "TF_IN_AUTOMATION=1"}, nil
}

// tfLookPath is swapped in tests so preflight is provable on a machine with
// terraform and one without.
var tfLookPath = exec.LookPath

// tfPreflight refuses a missing terraform with its install pointer and reports
// the installed version. Each root's required_version is Terraform's to
// enforce at init, naming both versions.
func tfPreflight(run tfRunner) (string, error) {
	if _, err := tfLookPath("terraform"); err != nil {
		return "", fmt.Errorf("terraform is not installed; install it: brew install hashicorp/tap/terraform " +
			"(https://developer.hashicorp.com/terraform/install)")
	}
	out, err := run(nil, "version", "-json")
	if err != nil {
		return "", fmt.Errorf("terraform version: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var version struct {
		Version string `json:"terraform_version"`
	}
	if err := json.Unmarshal(out, &version); err != nil || version.Version == "" {
		return "", fmt.Errorf("terraform version -json printed no version: %s", strings.TrimSpace(string(out)))
	}
	return version.Version, nil
}

// tfDefaultTerraform runs roots with the default runner, keeping plans and
// tfvars under workDir and the plugin cache under the user cache.
func tfDefaultTerraform(workDir string) (tfTerraform, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return tfTerraform{}, fmt.Errorf("user cache directory: %w", err)
	}
	return tfTerraform{Run: tfDefaultRun, WorkDir: workDir,
		CacheDir: filepath.Join(cache, "cohere-demo", "terraform", "plugins")}, nil
}
