// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestChatbotFanOutCoGeneratedForNRags locks the source-count-independent
// program and the values-driven topology data. Three RAG units change only the
// declare_rag_topology items array; the packaged machine retains one for_each
// and the fan-out declarations retain one rag_query.
func TestChatbotFanOutCoGeneratedForNRags(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chart := findChartDir(t)
	args := []string{"template", "t", chart}
	for i, name := range []string{"alpha", "bravo", "charlie"} {
		args = append(args,
			"--set", fmt.Sprintf("ragUnits[%d].name=%s", i, name),
			"--set", fmt.Sprintf("ragUnits[%d].description=%s corpus", i, name),
			"--set", fmt.Sprintf("ragUnits[%d].collection=c%d", i, i),
			"--set", fmt.Sprintf("ragUnits[%d].embeddingModel=m", i),
		)
	}
	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	topology := configMapKeyBlock(string(out), "agents__chatbot__request-topology-declarations.yaml")
	if topology == "" {
		t.Fatal("co-generated request-topology-declarations.yaml key not found")
	}
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if !strings.Contains(topology, `"name": "`+name+`"`) ||
			!strings.Contains(topology, `"description": "`+name+` corpus"`) ||
			!strings.Contains(topology, "t-chatbot-mesh-"+name+":18085") {
			t.Errorf("topology does not declare %s with its description and selected authority", name)
		}
	}
	if strings.Contains(topology, `"catalog":`) &&
		(strings.Contains(configMapCatalog(topology), "http://") || strings.Contains(configMapCatalog(topology), "collection")) {
		t.Error("source classifier catalog exposes trusted target/configuration data")
	}
	if strings.Count(topology, "name: declare_rag_topology") != 1 {
		t.Error("topology must contain exactly one declaration word")
	}
	var rendered struct {
		Tools []struct {
			Reversibility map[string]any `yaml:"reversibility"`
			Undo          map[string]any `yaml:"undo"`
		} `yaml:"tools"`
	}
	if err := yaml.Unmarshal([]byte(topology), &rendered); err != nil {
		t.Fatalf("parse co-generated topology declarations: %v", err)
	}
	if len(rendered.Tools) != 1 {
		t.Fatalf("co-generated topology declarations contain %d tools, want 1", len(rendered.Tools))
	}
	if _, nested := rendered.Tools[0].Reversibility["undo"]; nested {
		t.Error("co-generated topology nests undo inside reversibility")
	}
	if strategy := rendered.Tools[0].Undo["strategy"]; strategy != "noop" {
		t.Errorf("co-generated topology undo strategy = %v, want noop", strategy)
	}

	root := filepath.Join(chart, "..", "agents", "chatbot")
	machine, err := os.ReadFile(filepath.Join(root, "request-machine.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	fanout, err := os.ReadFile(filepath.Join(root, "request-fanout-declarations.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// Five query for_each blocks, one per facet leg (srd016 R4.4), all over the
	// same trusted selected topology, and one revision read of the cited records
	// over the declared topology (GH-604). The proposed-query passes fan through
	// the mesh's own /search surface instead of declaring reads of their own
	// (GH-611).
	// The guard is source-count independence, not the leg count -- the
	// indexed-name loop below is what catches a per-source machine.
	if strings.Count(string(machine), "for_each:") != 6 ||
		strings.Count(string(machine), "items: $from(selected_sources).body.selected") != 5 ||
		strings.Count(string(machine), "items: $from(declare_rag_topology).body.items") != 1 {
		t.Error("packaged machine must contain the five facet-leg for_each blocks over trusted selected topology entries (GH-730) and the cited-record read over the declared topology (GH-604); the proposed-query passes read the self /search surface (GH-611)")
	}
	if strings.Count(string(fanout), "name: rag_query\n") != 1 {
		t.Error("fan-out declarations must contain one rag_query word")
	}
	if strings.Count(string(fanout), "name: rag_query_faceted\n") != 1 {
		t.Error("fan-out declarations must contain one rag_query_faceted word")
	}
	for _, indexed := range []string{"rag_query0", "rag_query1", "Retrieving0", "Retrieving1", "compare_model0", "keep_chunks0"} {
		if strings.Contains(string(machine), indexed) || strings.Contains(string(fanout), indexed) {
			t.Errorf("source-indexed fan-out name remains: %s", indexed)
		}
	}
}

func TestRenderedProfileProjectionKeysExistInConfigMap(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chart := findChartDir(t)
	staged, cleanup, err := stageSmokeChart(chart, filepath.Dir(chart))
	if err != nil {
		t.Fatalf("stage chart: %v", err)
	}
	defer cleanup()

	out, err := exec.Command("helm", "template", "t", staged).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	render := string(out)
	for _, legacy := range []string{
		"agents__chatbot__request-fanout.yaml",
		"agents/chatbot/request-fanout.yaml",
	} {
		if strings.Contains(render, legacy) {
			t.Errorf("rendered chart retains legacy profile reference %q", legacy)
		}
	}

	type manifest struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Data map[string]string `yaml:"data"`
		Spec struct {
			Template struct {
				Spec struct {
					Volumes []struct {
						Name      string `yaml:"name"`
						Projected struct {
							Sources []struct {
								ConfigMap struct {
									Name  string `yaml:"name"`
									Items []struct {
										Key  string `yaml:"key"`
										Path string `yaml:"path"`
									} `yaml:"items"`
								} `yaml:"configMap"`
							} `yaml:"sources"`
						} `yaml:"projected"`
					} `yaml:"volumes"`
				} `yaml:"spec"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}

	var docs []manifest
	// One data map per profiles group object (GH-311): a projected key must
	// exist in the object its own source names, not merely somewhere.
	profilesData := map[string]map[string]string{}
	for _, chunk := range strings.Split(render, "\n---") {
		var doc manifest
		if err := yaml.Unmarshal([]byte(chunk), &doc); err != nil {
			continue
		}
		docs = append(docs, doc)
		if doc.Kind == "ConfigMap" && strings.Contains(doc.Metadata.Name, "-profiles-") {
			profilesData[doc.Metadata.Name] = doc.Data
		}
	}
	if len(profilesData) < 2 {
		t.Fatalf("rendered chart has %d profiles group ConfigMaps, want the per-agent split", len(profilesData))
	}

	const (
		fanoutKey  = "agents__chatbot__request-fanout-declarations.yaml"
		fanoutPath = "agents/chatbot/request-fanout-declarations.yaml"
	)
	var projected, fanoutProjected bool
	for _, doc := range docs {
		for _, volume := range doc.Spec.Template.Spec.Volumes {
			if volume.Name != "profiles" {
				continue
			}
			for _, source := range volume.Projected.Sources {
				data, ok := profilesData[source.ConfigMap.Name]
				if !ok {
					t.Errorf("%s projects from %s, which the chart does not render",
						doc.Metadata.Name, source.ConfigMap.Name)
					continue
				}
				for _, item := range source.ConfigMap.Items {
					projected = true
					if _, ok := data[item.Key]; !ok {
						t.Errorf("%s projects key %s, which is absent from %s data",
							doc.Metadata.Name, item.Key, source.ConfigMap.Name)
					}
					if item.Key == fanoutKey {
						fanoutProjected = true
						if item.Path != fanoutPath {
							t.Errorf("%s projects %s at %s, want %s",
								doc.Metadata.Name, fanoutKey, item.Path, fanoutPath)
						}
					}
				}
			}
		}
	}
	if !projected {
		t.Fatal("rendered workloads project no profiles ConfigMap items")
	}
	if !fanoutProjected {
		t.Errorf("rendered workloads do not project %s", fanoutKey)
	}
}

// TestChatbotRestCoGeneratedFromRagUnits locks one selected-target RAG operation.
// ragUnits still generates the network allowlist and topology authorities while
// the REST operation count remains one.
func TestChatbotRestCoGeneratedFromRagUnits(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chart := findChartDir(t)

	units := []struct{ name, collection string }{
		{"alpha", "ca"}, {"bravo", "cb"}, {"charlie", "cc"},
	}
	args := []string{"template", "t", chart}
	for i, u := range units {
		args = append(args,
			"--set", fmt.Sprintf("ragUnits[%d].name=%s", i, u.name),
			"--set", fmt.Sprintf("ragUnits[%d].description=%s corpus", i, u.name),
			"--set", fmt.Sprintf("ragUnits[%d].collection=%s", i, u.collection),
			"--set", fmt.Sprintf("ragUnits[%d].embeddingModel=m", i),
		)
	}
	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	rest := configMapKeyBlock(string(out), "agents__chatbot__rest.yaml")
	if rest == "" {
		t.Fatal("co-generated agents__chatbot__rest.yaml key not found in render")
	}

	if strings.Count(rest, "\n    rag:") != 1 ||
		strings.Count(rest, "\n        query:") != 1 {
		t.Error("co-generated rest.yaml must contain one generic RAG client and operation")
	}
	for _, selected := range []string{
		"base_url_source: command_state",
		"base_url_selector: $from(rag_unit).body.base_url",
	} {
		if !strings.Contains(rest, selected) {
			t.Errorf("generic RAG operation missing %q", selected)
		}
	}
	for _, u := range units {
		host := "t-chatbot-mesh-" + u.name
		if !strings.Contains(rest, "- "+host) {
			t.Errorf("co-generated network allowlist missing host %q", host)
		}
		upstream := fmt.Sprintf("%s: http://t-chatbot-mesh-%s:18087", u.name, u.name)
		if !strings.Contains(rest, upstream) {
			t.Errorf("co-generated rest.yaml missing monitor_proxy upstream %q", upstream)
		}
	}
	// No per-source client or operation may reappear.
	for _, alias := range []string{"alpha:", "bravo:", "charlie:", "alpha_query:", "bravo_query:", "charlie_query:"} {
		if strings.Contains(rest, "\n    "+alias) || strings.Contains(rest, "\n        "+alias) {
			t.Errorf("co-generated rest.yaml has per-source REST entry %q", alias)
		}
	}
	if strings.Contains(rest, "http://127.0.0.1:18095") {
		t.Error("packaged loopback RAG client leaked into the co-generated rest.yaml")
	}
	// Servers must bind 0.0.0.0 so Services route to the pod.
	if !strings.Contains(rest, "address: 0.0.0.0:18080") {
		t.Error("co-generated chat server does not bind 0.0.0.0")
	}
}

// TestChatbotUIMonitoredAgentsCoGenerated locks the monitored-agents surface to
// the same ragUnits list (srd003 R2).
func TestChatbotUIMonitoredAgentsCoGenerated(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chart := findChartDir(t)
	out, err := exec.Command("helm", "template", "t", chart,
		"--set", "ragUnits[0].name=only", "--set", "ragUnits[0].description=only corpus",
		"--set", "ragUnits[0].collection=c", "--set", "ragUnits[0].embeddingModel=m",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	ux := configMapKeyBlock(string(out), "agents__chatbot__ui__ui.yaml")
	if ux == "" {
		t.Fatal("co-generated ui.yaml key not found")
	}
	if !strings.Contains(ux, "name: only") {
		t.Error("ux monitored_agents missing the sole rag unit")
	}
	if strings.Contains(ux, "name: rag1") {
		t.Error("packaged rag1 monitored-agent leaked into the co-generated ui.yaml")
	}
}

func configMapCatalog(topology string) string {
	start := strings.Index(topology, `"catalog":`)
	if start < 0 {
		return ""
	}
	rest := topology[start:]
	end := strings.Index(rest, `"items":`)
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// configMapKeyBlock returns the indented block value of a "  <key>: |-" entry in
// a rendered ConfigMap, dedented, up to the next same-level key.
func configMapKeyBlock(render, key string) string {
	lines := strings.Split(render, "\n")
	var block []string
	inBlock := false
	for _, line := range lines {
		if !inBlock {
			if strings.TrimSpace(line) == key+": |-" {
				inBlock = true
			}
			continue
		}
		if strings.HasPrefix(line, "    ") {
			block = append(block, strings.TrimPrefix(line, "    "))
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		break // dedent to a sibling key ends the block
	}
	return strings.Join(block, "\n")
}

// vendorChartOnce vendors the agent-services library into helm/charts once
// per test binary, so a render of the raw chart resolves its dependency.
var (
	vendorChartOnce sync.Once
	vendorChartErr  error
)

func findChartDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "helm", "Chart.yaml")
		if _, err := os.Stat(candidate); err == nil {
			chartDir := filepath.Dir(candidate)
			vendorChartOnce.Do(func() { vendorChartErr = vendorAgentServices(chartDir) })
			if vendorChartErr != nil {
				t.Fatalf("vendor the agent-services library chart: %v", vendorChartErr)
			}
			return chartDir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("chatbot-mesh chart not found walking up from the test directory")
		}
		dir = parent
	}
}
