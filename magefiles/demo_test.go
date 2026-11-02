// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The demo is the application on the shared platform (GH-788): no path keeps a
// cluster of its own, a Traefik install, or a direct Helm lifecycle call, the
// third install path srd022 R6 forbids.
func TestNoPathKeepsADedicatedDemoClusterOrDirectHelmInstall(t *testing.T) {
	if _, err := os.Stat(filepath.Join("..", "helm", "ci", "kind-demo-config.yaml")); !os.IsNotExist(err) {
		t.Errorf("helm/ci/kind-demo-config.yaml still exists (err %v)", err)
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, banned := range []string{"da-chatbot-mesh-demo", "kindrig.DemoUp", "kindrig.DemoDown", "kindrig.InstallIngress"} {
			if strings.Contains(string(source), banned) {
				t.Errorf("%s still names %s", name, banned)
			}
		}
	}
}

// Reading the release is how app:seed learns the embedding identity to write
// vectors under and how app:verify learns whether the Cohere tier should
// answer. Unpinned, helm answers from whatever context the operator last used:
// the seed fails claiming the application is down, and the verify reports the
// provider disabled and skips the assertions it exists to make (GH-209).
func TestApplicationHelmValuesArgsPinTheKubeconfigAndNamespace(t *testing.T) {
	args := demoHelmValuesArgs("/tmp/platform.yaml")
	for flag, want := range map[string]string{
		"--kubeconfig": "/tmp/platform.yaml",
		"--namespace":  cohereApplicationNamespace,
	} {
		index := slices.Index(args, flag)
		if index < 0 || index == len(args)-1 {
			t.Fatalf("helm %v does not pin %s", args, flag)
		}
		if got := args[index+1]; got != want {
			t.Errorf("%s = %q, want %q", flag, got, want)
		}
	}
	if got := slices.Index(args, cohereApplicationRelease); got < 0 {
		t.Errorf("helm %v does not name the release %q", args, cohereApplicationRelease)
	}
	for _, want := range []string{"get", "values", "--all", "--output", "json"} {
		if !slices.Contains(args, want) {
			t.Errorf("helm %v omits %q", args, want)
		}
	}
}

// The pin is only worth as much as its adoption: a second reader that builds
// its own argv reintroduces the split where kubectl addresses the demo cluster
// and helm addresses the ambient one. Both readers go through the helper, so
// the pin cannot come back on one and not the other.
func TestDemoReleaseReadersDoNotBuildTheirOwnHelmArgv(t *testing.T) {
	for _, name := range []string{"demo_seed.go", "demo_verify.go"} {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(source), `exec.Command("helm", "`) {
			t.Errorf("%s runs helm with an inline argv;"+
				" call demoHelmValuesArgs so the kube context stays pinned", name)
		}
	}
}

// The kind CI values turn the exporter off because only app:up builds its
// image, and app:up layers those same values. Unless it turns it back on,
// app:up deploys no exporter and integration:exporter skips instead of
// proving the release (GH-759).
func TestAppReenablesTheExporterTheCIValuesDisable(t *testing.T) {
	for _, file := range []string{"kind-values.yaml", "kind-llm-values.yaml"} {
		var values struct {
			Exporter struct {
				Enabled *bool `yaml:"enabled"`
			} `yaml:"exporter"`
		}
		if err := readIntegrationYAML(
			filepath.Join("..", "helm", "ci", file), file, &values); err != nil {
			t.Fatal(err)
		}
		if values.Exporter.Enabled == nil || *values.Exporter.Enabled {
			t.Errorf("%s does not disable the exporter; its image is built only by app:up", file)
		}
	}
	// The overrides layer over the CI values file (apprig passes it first), so
	// enabling the exporter there is what turns it back on.
	exporter := cohereApplicationExtras(cohereExtrasInput{ExporterImage: "cohere-demo/exporter:test"})["exporter"].(map[string]any)
	if exporter["enabled"] != true {
		t.Errorf("the app:up overrides do not re-enable the exporter: %v", exporter)
	}
}

func TestChatbotDemoRequiresTheChartModelsFromHostOllama(t *testing.T) {
	models, err := demoRequiredModels(filepath.Join("..", "helm"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"qwen3-embedding:8b", "qwen2.5:3b", "ornith:9b"}
	if !slices.Equal(models, want) {
		t.Fatalf("demo models = %v, want %v", models, want)
	}
}
