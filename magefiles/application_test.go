// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mesh-intelligence/declarative-agents/magefiles/apprig"
	"gopkg.in/yaml.v3"
)

// app:up resolves one release in its own app- namespace on the pinned
// platform, with its own host and telemetry bucket (GH-788 R1, R2, R4).
func TestApplicationRunnerTargetsSharedPlatformCoordinates(t *testing.T) {
	runner, err := cohereApplicationRunner()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := applicationResolved(runner)
	if err != nil {
		t.Fatal(err)
	}
	if runner.Binding.Cluster != platformCluster() {
		t.Errorf("cluster = %q, want the pinned platform %q", runner.Binding.Cluster, platformCluster())
	}
	for field, got := range map[string][2]string{
		"namespace": {resolved.Namespace, cohereApplicationNamespace},
		"release":   {resolved.Release, cohereApplicationRelease},
		"host":      {resolved.Host, cohereApplicationHost},
		"bucket":    {resolved.BucketURL, "gs://cohere-demo-telemetry"},
	} {
		if got[0] != got[1] {
			t.Errorf("%s = %q, want %q", field, got[0], got[1])
		}
	}
}

// cohere-demo and upstream chatbot-mesh run on one platform, so no namespace,
// release, host, bucket, or collector may coincide (R2).
func TestApplicationDoesNotCollideWithUpstreamChatbotMesh(t *testing.T) {
	runner, err := cohereApplicationRunner()
	if err != nil {
		t.Fatal(err)
	}
	cohere, err := applicationResolved(runner)
	if err != nil {
		t.Fatal(err)
	}
	upstreamManifest, err := apprig.LoadManifest(runner.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	upstreamManifest.Application = "chatbot-mesh"
	upstreamBinding := runner.Binding
	upstreamBinding.ApplicationRoot = filepath.Join(t.TempDir(), "chatbot-mesh")
	upstream, err := apprig.Resolve(upstreamManifest, upstreamBinding)
	if err != nil {
		t.Fatal(err)
	}
	if collisions := apprig.DetectCollisions([]apprig.Resolved{cohere, upstream}); len(collisions) > 0 {
		t.Errorf("cohere-demo collides with chatbot-mesh: %+v", collisions)
	}
}

// The overrides pin every image to the kind-loaded copy, put the three hosts
// on the platform ingress, and switch the Cohere tier with the credential:
// on, the provider is enabled by Secret name and every RAG unit is repointed
// to the Cohere embedding identity (srd002 R3.3); off, neither appears.
func TestApplicationExtrasSwitchTheCohereTierWithTheCredential(t *testing.T) {
	input := cohereExtrasInput{
		ExporterImage: "cohere-demo/exporter:abc123",
		Assets:        []externalUIAsset{{Component: "observer", ConfigMapName: "ui-observer", Checksum: "sum"}},
		ProfileValues: map[string]any{"profiles": map[string]any{"external": map[string]any{"chatbot": "cm"}}},
		EmbedModel:    "embed-v4.0",
		RagUnits:      []map[string]any{{"name": "rag0", "collection": "corpus", "embeddingModel": "qwen3-embedding:8b"}},
	}
	off := cohereApplicationExtras(input)
	if _, set := off["cohere"]; set {
		t.Error("the Ollama tier enables the Cohere provider")
	}
	if _, set := off["ragUnits"]; set {
		t.Error("the Ollama tier repoints the RAG units")
	}
	ingress := off["ingress"].(map[string]any)
	for key, want := range map[string]string{
		"host": cohereApplicationHost, "observerHost": cohereObserverHost,
		"collectorHost": cohereCollectorHost, "className": cohereIngressClass,
	} {
		if ingress[key] != want {
			t.Errorf("ingress.%s = %v, want %s", key, ingress[key], want)
		}
	}
	exporter := off["exporter"].(map[string]any)["image"].(map[string]any)
	if exporter["repository"] != "cohere-demo/exporter" || exporter["tag"] != "abc123" || exporter["pullPolicy"] != "Never" {
		t.Errorf("exporter image = %v", exporter)
	}
	if off["observer"].(map[string]any)["uiArchiveConfigMap"] != "ui-observer" {
		t.Error("the observer does not name its out-of-release UI archive")
	}
	if off["profiles"] == nil {
		t.Error("the external profile values were dropped")
	}

	input.CohereEnabled = true
	on := cohereApplicationExtras(input)
	cohere := on["cohere"].(map[string]any)
	if cohere["enabled"] != true || cohere["existingSecret"] != demoCohereSecret {
		t.Errorf("cohere = %v", cohere)
	}
	units := on["ragUnits"].([]any)
	unit := units[0].(map[string]any)
	if unit["embeddingModel"] != "embed-v4.0" || unit["collection"] != "corpus" {
		t.Errorf("repointed unit = %v", unit)
	}
	if input.RagUnits[0]["embeddingModel"] != "qwen3-embedding:8b" {
		t.Error("repointing mutated the values the units were read from")
	}
}

// The overrides apprig renders from these extras validate against the chart,
// with the collector on object storage in the application bucket.
func TestApplicationOverridesRenderAgainstTheChart(t *testing.T) {
	runner, err := cohereApplicationRunner()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := applicationResolved(runner)
	if err != nil {
		t.Fatal(err)
	}
	extras := cohereApplicationExtras(cohereExtrasInput{
		ExporterImage: "cohere-demo/exporter:abc123", CohereEnabled: true, EmbedModel: "embed-v4.0",
		RagUnits: []map[string]any{{"name": "rag0", "description": "d", "collection": "corpus",
			"embeddingModel": "qwen3-embedding:8b", "replicas": 1}},
	})
	overrides, err := apprig.ValuesOverrides(resolved, "ghcr.io/example/agent-core:abc123", extras)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(overrides), &parsed); err != nil {
		t.Fatalf("apprig rendered unparseable overrides: %v", err)
	}
	file := filepath.Join(t.TempDir(), "overrides.yaml")
	writePackageTestFile(t, file, overrides)
	chartDir := findChartDir(t)
	out, err := exec.Command("helm", "template", resolved.Release, chartDir,
		"--namespace", resolved.Namespace,
		"--values", filepath.Join(chartDir, "ci", chatbotDemoValuesFile),
		"-f", file).CombinedOutput()
	if err != nil {
		t.Fatalf("the chart rejects the app:up overrides: %v\n%s", err, firstRunes(string(out), 1200))
	}
	output := string(out)
	for _, want := range []string{
		`{name: COLLECTOR_OBJECT_BUCKET_URL, value: "gs://cohere-demo-telemetry"}`,
		`host: "` + cohereApplicationHost + `"`,
		`host: "` + cohereObserverHost + `"`,
		`host: "` + cohereCollectorHost + `"`,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("the app:up render lacks %s", want)
		}
	}
}

func TestRequirePlatformRefusesAMissingPlatform(t *testing.T) {
	if err := requirePlatform("da-platform-x", func(string) bool { return false }); err == nil ||
		!strings.Contains(err.Error(), "platform:up") {
		t.Errorf("a missing platform = %v, want a refusal naming platform:up", err)
	}
	if err := requirePlatform("da-platform-x", func(string) bool { return true }); err != nil {
		t.Errorf("a running platform was refused: %v", err)
	}
}

// Purge fails closed without the catalog agent rather than deleting anything
// imperatively, and the runner binds it (R4).
func TestApplicationRunnerBindsPurge(t *testing.T) {
	runner, err := cohereApplicationRunner()
	if err != nil {
		t.Fatal(err)
	}
	if runner.Purge == nil {
		t.Fatal("app:purge is unbound")
	}
	runner.Purge = nil
	if err := runner.PurgeData("cohere-demo"); !errors.Is(err, apprig.ErrPurgeUnavailable) {
		t.Errorf("an unbound purge = %v, want ErrPurgeUnavailable", err)
	}
}
