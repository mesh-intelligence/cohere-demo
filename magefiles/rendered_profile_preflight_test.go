// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// renderedConfigMap is the profiles ConfigMap as helm renders it: one data key
// per staged profile file, the path separators written as double underscores.
type renderedConfigMap struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Data map[string]string `yaml:"data"`
}

// renderChartProfiles renders the staged chart with the given --set overrides,
// writes the profiles ConfigMap back out as a directory tree, and returns its
// root. That tree is what a pod mounts, which is the thing this file exists to
// check: the source tree under agents/ and the rendered ConfigMap are different
// artifacts, and only the rendered one reflects co-generation.
func renderChartProfiles(t *testing.T, staged string, sets ...string) string {
	t.Helper()
	args := []string{"template", "rel", staged, "--namespace", "nsy"}
	for _, set := range sets {
		args = append(args, "--set", set)
	}
	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	root := t.TempDir()
	var wrote int
	decoder := yaml.NewDecoder(strings.NewReader(string(out)))
	for {
		var doc renderedConfigMap
		if err := decoder.Decode(&doc); err != nil {
			break
		}
		// Every profiles group object (GH-311): the preflight loads the merged
		// tree exactly as a pod that mounted every group would see it.
		if doc.Kind != "ConfigMap" || !strings.Contains(doc.Metadata.Name, "-profiles-") {
			continue
		}
		for key, body := range doc.Data {
			path := filepath.Join(root, filepath.Join(strings.Split(key, "__")...))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("create %s: %v", filepath.Dir(path), err)
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
			wrote++
		}
	}
	if wrote == 0 {
		t.Fatal("the rendered chart carries no profiles ConfigMap data; the check would pass vacuously")
	}
	return root
}

// preflightRenderedProfiles builds the agent and runs the same --validate-config
// load every profile takes at pod start, over the rendered tree.
func preflightRenderedProfiles(t *testing.T, root string) error {
	t.Helper()
	chartDir := findChartDir(t)
	applicationRoot := filepath.Dir(chartDir)
	coreRoot := demoCoreRoot(applicationRoot)
	if !agentCoreAvailable(coreRoot) {
		t.Skipf("agent-core checkout not found at %s", coreRoot)
	}
	binary, cleanupAgent, err := buildAgent(coreRoot)
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	defer cleanupAgent()
	profiles, err := meshProfiles(root)
	if err != nil {
		t.Fatalf("collect rendered profiles: %v", err)
	}
	return bootSmokeProfiles(defaultSmokeRun, binary, coreRoot, profiles)
}

// TestRenderedProfilesPreflight closes the gap that let a chart whose own
// default cannot start stay green through every gate.
//
// The mesh boot smoke in `mage audit` preflights the profiles under agents/,
// where rest.yaml declares every client unconditionally. A pod mounts the
// profiles ConfigMap instead, and three of the chatbot's keys are co-generated
// from values -- so a word the machine dispatches can lose the client it binds
// without anything under agents/ changing. That is not hypothetical: at chart
// defaults the rendered chatbot refused to load with
//
//	action "embed_query_cohere" REST authority: REST client "cohere" is not defined
//
// while audit, the boot smoke, and the whole test suite stayed green (GH-223).
func TestRenderedProfilesPreflight(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	staged, cleanup, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
	if err != nil {
		t.Fatalf("stage chart: %v", err)
	}
	defer cleanup()

	// The schema requires a credential reference whenever the tier is on. The
	// name is never dereferenced here: nothing in this test starts a pod.
	root := renderChartProfiles(t, staged,
		"cohere.enabled=true", "cohere.existingSecret=cohere-key", "cohere.secretKey=api-key")
	if err := preflightRenderedProfiles(t, root); err != nil {
		t.Errorf("the rendered chart cannot start with the Cohere tier enabled: %v", err)
	}
}

// TestRenderedProfilesPreflightWithProviderDisabled is the same check at the
// chart's own default, which srd009 R6.3 requires to run the turn on the Ollama
// tier with the pre-provider behavior.
//
// This was skipped until GH-224: cohere_rerank_rag and invoke_cohere_command bind the Cohere
// client and were dispatched whatever the tier, so the rendered profile refused
// to load. The pre-provider machine and chat-LLM vocabulary are now staged
// beside the closure as recorded tier variants, and packaging projects them
// under the canonical keys when the provider is disabled. The assertion stands
// as written.
func TestRenderedProfilesPreflightWithProviderDisabled(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	staged, cleanup, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
	if err != nil {
		t.Fatalf("stage chart: %v", err)
	}
	defer cleanup()

	root := renderChartProfiles(t, staged, "cohere.enabled=false")
	if err := preflightRenderedProfiles(t, root); err != nil {
		t.Errorf("the rendered chart cannot start at its own defaults: %v", err)
	}
}

// dumpedServer is a REST server as --dump-config resolves it.
type dumpedServer struct {
	Address   string                    `yaml:"address"`
	Endpoints map[string]map[string]any `yaml:"endpoints"`
}

func dumpServers(t *testing.T, binary, coreRoot, profile string) map[string]dumpedServer {
	t.Helper()
	out, err := exec.Command(binary, "--profile", profile, "--core-root", coreRoot, "--dump-config").Output()
	if err != nil {
		t.Fatalf("dump %s: %v", profile, err)
	}
	var dump struct {
		Rest struct {
			Servers map[string]dumpedServer `yaml:"servers"`
		} `yaml:"rest"`
	}
	if err := yaml.Unmarshal(out, &dump); err != nil {
		t.Fatalf("parse dump of %s: %v", profile, err)
	}
	return dump.Rest.Servers
}

// TestRenderedChatbotServesThePackagedLifecycleSurface is GH-746: once the
// packaged chatbot took its monitor server from agent-core's fragment, the
// chart's hand-written copy made the rendered profile declare "monitor" twice,
// and that copy had already drifted (an extra control_exit route, and a control
// exit route agent-core now injects). Both servers now match the packaged
// ones, and only their addresses may differ.
func TestRenderedChatbotServesThePackagedLifecycleSurface(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	applicationRoot := filepath.Dir(chartDir)
	coreRoot := demoCoreRoot(applicationRoot)
	if !agentCoreAvailable(coreRoot) {
		t.Skipf("agent-core checkout not found at %s", coreRoot)
	}
	staged, cleanup, err := stageSmokeChart(chartDir, applicationRoot)
	if err != nil {
		t.Fatalf("stage chart: %v", err)
	}
	defer cleanup()
	root := renderChartProfiles(t, staged, "cohere.enabled=false")
	binary, cleanupAgent, err := buildAgent(coreRoot)
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	defer cleanupAgent()

	rendered := dumpServers(t, binary, coreRoot, filepath.Join(root, "agents", "chatbot", "profile.yaml"))
	packaged := dumpServers(t, binary, coreRoot, filepath.Join(applicationRoot, "agents", "chatbot", "profile.yaml"))

	if views := len(packaged["monitor"].Endpoints); views != 8 {
		t.Fatalf("packaged chatbot monitor serves %d views, want the fragment's eight", views)
	}
	for _, name := range []string{"monitor", "chatbot_control"} {
		if !reflect.DeepEqual(rendered[name].Endpoints, packaged[name].Endpoints) {
			t.Errorf("rendered chatbot %s endpoints differ from the packaged ones:\nrendered: %v\npackaged: %v",
				name, rendered[name].Endpoints, packaged[name].Endpoints)
		}
		if !strings.HasPrefix(rendered[name].Address, "0.0.0.0:") {
			t.Errorf("rendered chatbot %s binds %q, want 0.0.0.0 so the Service routes to the pod", name, rendered[name].Address)
		}
	}
}

// renderedObject is one rendered manifest, read for the fields that decide
// what a workload's pod sees: ConfigMap data, and a pod template's container
// args and projected volumes.
type renderedObject struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Data map[string]string `yaml:"data"`
	Spec struct {
		Template struct {
			Spec struct {
				Containers []struct {
					Args []string `yaml:"args"`
				} `yaml:"containers"`
				Volumes []struct {
					Name      string `yaml:"name"`
					Projected *struct {
						Sources []struct {
							ConfigMap *struct {
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

// profilesMount is where every workload mounts its profiles volume
// (values.yaml profiles.mountPath).
const profilesMount = "/profiles/"

// TestEveryWorkloadMountsTheClosureOfItsProfile is GH-750. Each workload
// projects only the profile groups it names, so a profile can load from the
// merged ConfigMap tree TestRenderedProfilesPreflight validates and still fail
// in its own pod: the collector's query declarations import a unit from the
// units group it did not mount, and the observer named a group that no longer
// existed once its profile moved under applications/. This rebuilds, per
// workload, exactly the tree its projected volume mounts and validates the
// profile its container starts with against that tree alone.
func TestEveryWorkloadMountsTheClosureOfItsProfile(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	applicationRoot := filepath.Dir(chartDir)
	coreRoot := demoCoreRoot(applicationRoot)
	if !agentCoreAvailable(coreRoot) {
		t.Skipf("agent-core checkout not found at %s", coreRoot)
	}
	staged, cleanup, err := stageSmokeChart(chartDir, applicationRoot)
	if err != nil {
		t.Fatalf("stage chart: %v", err)
	}
	defer cleanup()
	// The control plane renders too: its creator runs the corpus-ingest child
	// in its own pod, and a default render has no creator to check.
	out, err := exec.Command("helm", "template", "rel", staged, "--namespace", "nsy",
		"--set", "cohere.enabled=true", "--set", "cohere.existingSecret=cohere-key",
		"--set", "cohere.secretKey=api-key", "--set", "controlPlane.enabled=true").CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}

	var objects []renderedObject
	decoder := yaml.NewDecoder(strings.NewReader(string(out)))
	for {
		var object renderedObject
		if err := decoder.Decode(&object); err != nil {
			break
		}
		objects = append(objects, object)
	}
	configMaps := map[string]map[string]string{}
	for _, object := range objects {
		if object.Kind == "ConfigMap" {
			configMaps[object.Metadata.Name] = object.Data
		}
	}

	var profiles []string
	for _, object := range objects {
		if object.Kind != "Deployment" && object.Kind != "StatefulSet" {
			continue
		}
		profile := startedProfile(object)
		if profile == "" {
			continue
		}
		root := filepath.Join(t.TempDir(), object.Metadata.Name)
		mounted := false
		for _, volume := range object.Spec.Template.Spec.Volumes {
			if volume.Name != "profiles" || volume.Projected == nil {
				continue
			}
			mounted = true
			for _, source := range volume.Projected.Sources {
				if source.ConfigMap == nil {
					continue
				}
				data, exists := configMaps[source.ConfigMap.Name]
				if !exists {
					t.Errorf("%s projects ConfigMap %s, which the chart does not render",
						object.Metadata.Name, source.ConfigMap.Name)
					continue
				}
				for _, item := range source.ConfigMap.Items {
					body, ok := data[item.Key]
					if !ok {
						t.Errorf("%s projects key %s, absent from %s",
							object.Metadata.Name, item.Key, source.ConfigMap.Name)
						continue
					}
					path := filepath.Join(root, filepath.FromSlash(item.Path))
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		if !mounted {
			t.Errorf("%s starts %s but mounts no projected profiles volume", object.Metadata.Name, profile)
			continue
		}
		profiles = append(profiles, filepath.Join(root, filepath.FromSlash(profile)))
		for _, child := range childProfiles(object) {
			profiles = append(profiles, filepath.Join(root, filepath.FromSlash(child)))
		}
	}
	if len(profiles) < 5 {
		t.Fatalf("found %d agent workloads; the check would pass vacuously", len(profiles))
	}
	binary, cleanupAgent, err := buildAgent(coreRoot)
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	defer cleanupAgent()
	if err := bootSmokeProfiles(defaultSmokeRun, binary, coreRoot, profiles); err != nil {
		t.Errorf("a workload cannot load its profile from the tree its own pod mounts: %v", err)
	}
}

// childProfiles returns the profiles a workload's agent runs as children inside
// its own pod, relative to the profiles mount. Those load from the same
// projected tree the parent's pod mounts, so they belong to its closure too
// (GH-760): the creator's corpus-ingest child imports a unit from the units
// group, and a creator that did not mount it failed every ingest intent. The
// path is the default of CORPUS_INGEST_PROFILE in
// agents/creator/request-declarations.yaml.
func childProfiles(object renderedObject) []string {
	if strings.HasSuffix(object.Metadata.Name, "-chatbot-mesh-creator") {
		return []string{"agents/corpus-ingest/profile.yaml"}
	}
	return nil
}

// startedProfile returns the profile a workload's agent container starts
// with, relative to the profiles mount, or "" for a workload that runs no
// agent profile.
func startedProfile(object renderedObject) string {
	for _, container := range object.Spec.Template.Spec.Containers {
		for index, arg := range container.Args {
			if arg == "--profile" && index+1 < len(container.Args) {
				if rest, ok := strings.CutPrefix(container.Args[index+1], profilesMount); ok {
					return rest
				}
			}
		}
	}
	return ""
}
