// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// gcpTerraformRoots are the roots gcp:up applies, in order, the drafted
// production root, and each environment's exposure root gcp:deploy applies.
var gcpTerraformRoots = []string{"bootstrap", "envs/dev", "envs/prod", "envs/dev/exposure", "envs/prod/exposure"}

func gcpTerraformDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "infra", "gcp")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no infra/gcp above the working directory")
		}
		dir = parent
	}
}

func gcpTerraformFiles(t *testing.T, under string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(under, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == ".terraform" {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".tf") {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// Every root validates against the committed lock file. The working
// directory goes to a temporary TF_DATA_DIR so the test leaves no .terraform
// behind, and providers come from a shared cache after the first run.
func TestGcpTerraformRootsValidate(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform is not installed")
	}
	base := gcpTerraformDir(t)
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	pluginCache := filepath.Join(cache, "cohere-demo", "terraform-plugins")
	if err := os.MkdirAll(pluginCache, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, root := range gcpTerraformRoots {
		t.Run(root, func(t *testing.T) {
			env := append(os.Environ(), "TF_DATA_DIR="+t.TempDir(), "TF_PLUGIN_CACHE_DIR="+pluginCache,
				"TF_IN_AUTOMATION=1")
			for _, args := range [][]string{
				{"init", "-backend=false", "-input=false", "-lockfile=readonly", "-no-color"},
				{"validate", "-no-color"},
			} {
				cmd := exec.Command("terraform", append([]string{"-chdir=" + filepath.Join(base, root)}, args...)...)
				cmd.Env = env
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("terraform %s in %s: %v\n%s", args[0], root, err, firstRunes(string(out), 1500))
				}
			}
		})
	}
}

// Modules hold resource shapes and environments hold choices: a lifecycle
// knob in a module is a variable, never a literal. prevent_destroy cannot be
// a variable in Terraform, so its one literal sits in a resource whose
// for_each is gated on var.prevent_destroy.
func TestGcpTerraformModulesTakeLifecycleKnobsAsVariables(t *testing.T) {
	knob := regexp.MustCompile(`(?m)^\s*(force_destroy|deletion_protection)\s*=\s*(\S+)`)
	for path, text := range gcpTerraformFiles(t, filepath.Join(gcpTerraformDir(t), "modules")) {
		for _, match := range knob.FindAllStringSubmatch(text, -1) {
			if !strings.HasPrefix(match[2], "var.") {
				t.Errorf("%s sets %s = %s; a module takes it as a variable", path, match[1], match[2])
			}
		}
		for _, block := range strings.Split(text, "\nresource \"")[1:] {
			if strings.Contains(block, "prevent_destroy = true") && !strings.Contains(block, "var.prevent_destroy") {
				t.Errorf("%s: a resource sets prevent_destroy without gating on var.prevent_destroy:\n%s",
					path, firstRunes(block, 300))
			}
		}
	}
}

// No committed Terraform names a project, and no secret value can reach
// state, because no secret version resource exists anywhere.
func TestGcpTerraformNamesNoProjectAndHoldsNoSecretValue(t *testing.T) {
	projectLiteral := regexp.MustCompile(`(?m)(^\s*project(_id)?\s*=\s*"|projects/[a-z][-a-z0-9]{4,28}[a-z0-9]/|project-[0-9a-f]{8})`)
	for path, text := range gcpTerraformFiles(t, gcpTerraformDir(t)) {
		if strings.Contains(text, "google_secret_manager_secret_version") {
			t.Errorf("%s declares a secret version; values belong to gcp:secrets, never to state", path)
		}
		if match := projectLiteral.FindString(text); match != "" {
			t.Errorf("%s names a project literally (%q); take it from var.project", path, match)
		}
	}
}

// Production is a different composition of the same modules, not a copy:
// with the header and the knob lines set aside, the two roots are identical.
func TestGcpEnvironmentRootsDifferOnlyInKnobs(t *testing.T) {
	knobLine := regexp.MustCompile(`^\s*(environment|cluster_deletion_protection|cluster_release_channel|telemetry_force_destroy|telemetry_versioning|telemetry_retention_days|secrets_prevent_destroy)\s*=`)
	body := func(env string) string {
		data, err := os.ReadFile(filepath.Join(gcpTerraformDir(t), "envs", env, "main.tf"))
		if err != nil {
			t.Fatal(err)
		}
		var kept []string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "#") || knobLine.MatchString(line) {
				continue
			}
			kept = append(kept, line)
		}
		return strings.Join(kept, "\n")
	}
	if dev, prod := body("dev"), body("prod"); dev != prod {
		t.Errorf("envs/dev/main.tf and envs/prod/main.tf differ outside the knob lines")
	}
	if dev, prod := body("dev/exposure"), body("prod/exposure"); dev != prod {
		t.Errorf("envs/dev/exposure/main.tf and envs/prod/exposure/main.tf differ outside the header")
	}
}

// gcpEdgeHeaders are the headers the Google front end and IAP add to every
// request, which the agent-core REST server refuses unless a route declares
// them.
var gcpEdgeHeaders = []string{
	"via",
	"x-cloud-trace-context",
	"x-goog-iap-jwt-assertion",
	"x-goog-authenticated-user-email",
	"x-goog-authenticated-user-id",
}

// The exposure module's HTTPRoute rewrites no headers: Google's load balancer
// refuses to remove an x-goog-* header ("Cannot have prefix x-goog- in header
// name"), which left the Gateway unprogrammed in #883's live run, and IAP adds
// its headers after the route runs anyway. The chatbot's routes declare them
// instead (TestGcpChatbotRoutesDeclareTheEdgeHeaders).
func TestGcpExposureRouteRewritesNoHeaders(t *testing.T) {
	for path, text := range gcpTerraformFiles(t, gcpTerraformDir(t)) {
		for _, refused := range []string{"RequestHeaderModifier", "requestHeaderModifier", "stripped_headers"} {
			if strings.Contains(text, refused) {
				t.Errorf("%s carries %s; the load balancer cannot remove the edge's x-goog-* headers", path, refused)
			}
		}
		for _, header := range gcpEdgeHeaders[1:] {
			if strings.Contains(text, `"`+header+`"`) {
				t.Errorf("%s names %s; the chart's ingress.gke.transportHeaders owns the list", path, header)
			}
		}
	}
	main, err := os.ReadFile(filepath.Join(gcpTerraformDir(t), "modules", "exposure", "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`kind       = "HTTPRoute"`, `gatewayClassName = "gke-l7-global-external-managed"`, `kind       = "GCPBackendPolicy"`} {
		if !strings.Contains(string(main), want) {
			t.Errorf("modules/exposure/main.tf lacks %s", want)
		}
	}
}

// The Terraform grants and the Go binding name the same two KSAs: the
// telemetry bucket's Workload Identity member is the annotated KSA, and the
// Secret Manager reader is the sync KSA the deploy creates (#882).
func TestGcpEnvironmentKSAsMatchTheBinding(t *testing.T) {
	application := gcpTestApplication()
	for _, environment := range []string{"dev", "prod"} {
		data, err := os.ReadFile(filepath.Join("..", gcpTerraformRoot, "envs", environment, "main.tf"))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			`(?m)^\s*ksa\s*=\s*"` + application.KSA + `"$`,
			`(?m)^\s*secret_sync_ksa\s*=\s*"` + application.SyncKSA + `"$`,
			`(?m)^\s*ksa\s*=\s*local\.secret_sync_ksa$`,
		} {
			if !regexp.MustCompile(want).Match(data) {
				t.Errorf("envs/%s/main.tf does not match %s", environment, want)
			}
		}
	}
}
