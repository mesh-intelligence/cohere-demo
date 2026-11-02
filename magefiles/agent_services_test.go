// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mesh-intelligence/declarative-agents/magefiles/helmlib"
	"gopkg.in/yaml.v3"
)

// Vendoring takes the library chart the pinned magefiles module embeds, so it
// runs with no network, no git, and no cache (GH-851): with an empty PATH and
// a fresh cache directory, it still replaces a stale vendored copy with
// exactly the embedded chart.
func TestVendorAgentServicesNeedsNoNetwork(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	chartDir := t.TempDir()
	vendored := filepath.Join(chartDir, "charts", helmlib.Name)
	if err := os.MkdirAll(vendored, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(vendored, "stale.yaml")
	if err := os.WriteFile(stale, []byte("stale: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := vendorAgentServices(chartDir); err != nil {
		t.Fatalf("vendor with no network: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("vendoring kept a file the embedded chart does not carry")
	}
	embedded := helmlib.Embedded()
	files := 0
	err := fs.WalkDir(embedded, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		files++
		want, err := fs.ReadFile(embedded, path)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(vendored, filepath.FromSlash(path)))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("vendored %s differs from the embedded chart (%v)", path, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var chart struct {
		Name string `yaml:"name"`
	}
	data, err := os.ReadFile(filepath.Join(vendored, "Chart.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &chart); err != nil || chart.Name != helmlib.Name || files < 2 {
		t.Errorf("vendored chart is %q with %d files (%v), want the %s library", chart.Name, files, err, helmlib.Name)
	}
}

// The collector renders through the library, so the storage values
// apprig.ValuesOverrides writes (srd042 R10/R11) validate and wire the
// application bucket, and a chart given none keeps the NDJSON spool.
func TestCollectorTakesTheApplicationBucketFromStorageValues(t *testing.T) {
	storage := []string{
		"collector.storage.backend=object",
		"collector.storage.bucketName=cohere-demo-telemetry",
		"collector.storage.endpoint=http://fake-gcs.da-platform:4443",
		"collector.storage.prefix=cohere-demo",
		"collector.storage.application=cohere-demo",
	}
	objectRender, ok := renderChart(t, storage...)
	if !ok {
		t.Fatalf("render with object storage failed:\n%s", objectRender)
	}
	for _, want := range []string{
		`{name: COLLECTOR_STORAGE_BACKEND, value: "object"}`,
		`{name: COLLECTOR_OBJECT_BUCKET_URL, value: "gs://cohere-demo-telemetry"}`,
		`{name: COLLECTOR_OBJECT_ENDPOINT, value: "http://fake-gcs.da-platform:4443"}`,
		"kind: PersistentVolumeClaim",
	} {
		if !strings.Contains(objectRender, want) {
			t.Errorf("object-storage render lacks %s", want)
		}
	}

	spoolRender, ok := renderChart(t)
	if !ok {
		t.Fatalf("default render failed:\n%s", spoolRender)
	}
	if strings.Contains(spoolRender, `COLLECTOR_STORAGE_BACKEND, value: "object"`) {
		t.Error("the default render wires object storage")
	}
	if !strings.Contains(spoolRender, `{name: COLLECTOR_SPOOL_PATH, value: "/work/traces/collector.ndjson"}`) {
		t.Error("the default render lost the NDJSON spool")
	}
}

// The collector's Deployment and Service carry the mesh.cohere-demo/role text
// the observer's topology tooltip shows (GH-376), through the library's
// deploymentAnnotations and serviceAnnotations inputs for the agent collector
// (GH-840) and directly for the contrib gateway.
func TestCollectorCarriesTheRoleAnnotation(t *testing.T) {
	const role = "Receives OTLP traces and metrics from every agent and serves the query surface the topology figure reads."
	for _, implementation := range []string{"agent", "contrib"} {
		out := helmTemplateOutput(t, "collector.implementation="+implementation)
		found := map[string]bool{}
		for _, chunk := range strings.Split(out, "\n---") {
			var doc struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Name        string            `yaml:"name"`
					Annotations map[string]string `yaml:"annotations"`
				} `yaml:"metadata"`
			}
			if err := yaml.Unmarshal([]byte(chunk), &doc); err != nil {
				continue
			}
			if doc.Metadata.Name != "rel-chatbot-mesh-collector" || (doc.Kind != "Deployment" && doc.Kind != "Service") {
				continue
			}
			if got := doc.Metadata.Annotations["mesh.cohere-demo/role"]; got != role {
				t.Errorf("%s collector %s role annotation = %q, want %q", implementation, doc.Kind, got, role)
			}
			found[doc.Kind] = true
		}
		for _, kind := range []string{"Deployment", "Service"} {
			if !found[kind] {
				t.Errorf("%s render has no collector %s", implementation, kind)
			}
		}
	}
}
