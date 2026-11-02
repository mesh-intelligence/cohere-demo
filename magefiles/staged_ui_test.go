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

// These cover what the chatbot UI contributes to the packaged chart (GH-702). Every file
// staged into profiles/ becomes a ConfigMap key and a projected mount item in
// every agent pod, so the staged set has to be what the chart consumes rather
// than whatever happens to sit in the source tree.

// configMapKeyRE matches the ConfigMap data keys in a rendered chart. Keys are
// the staged paths with "/" encoded as "__" (ConfigMap keys cannot contain "/").
var configMapKeyRE = regexp.MustCompile(`(?m)^  ([a-zA-Z0-9_.-]+):`)

// renderedProfileKeys stages the chart through the production packaging path and
// returns the profiles ConfigMap keys helm renders from it.
func renderedProfileKeys(t *testing.T) []string {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	staged, cleanup, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
	if err != nil {
		t.Fatalf("stage chart: %v", err)
	}
	defer cleanup()

	out, err := exec.Command("helm", "template", "relx", staged, "--namespace", "nsy").CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	var keys []string
	for _, m := range configMapKeyRE.FindAllStringSubmatch(string(out), -1) {
		if strings.HasPrefix(m[1], "agents__") || strings.HasPrefix(m[1], "applications__") {
			keys = append(keys, m[1])
		}
	}
	if len(keys) == 0 {
		t.Fatal("no profile keys in the rendered ConfigMap; the staging or the key encoding changed")
	}
	return keys
}

// TestStagedChatbotUICarriesOnlyWhatTheChartServes proves the chatbot UI contributes its
// descriptor and its built bundle and nothing else. The panel sources, the
// tsconfig, and package-lock.json are build inputs; node_modules is worse than
// noise, because esbuild's binary is over helm's 5 MiB per-file limit and fails
// the render outright, so a developer who had run npm install could not render
// the chart at all.
func TestStagedChatbotUICarriesOnlyWhatTheChartServes(t *testing.T) {
	for _, key := range renderedProfileKeys(t) {
		if !strings.HasPrefix(key, "agents__chatbot__ui__app__") {
			continue
		}
		if !strings.HasPrefix(key, "agents__chatbot__ui__app__dist__") {
			t.Errorf("rendered ConfigMap carries %s; only the built bundle under agents/chatbot/ui/app/dist belongs in a pod", key)
		}
	}
}

// TestStagedChatbotUICarriesTheServedBundle proves the counterpart: the chatbot's
// static_assets binding serves agents/chatbot/ui/app/dist off the profile mount, so cutting the
// staged tree must not cut the bundle with it. The panel would 404 at /ui.
func TestStagedChatbotUICarriesTheServedBundle(t *testing.T) {
	keys := renderedProfileKeys(t)
	var index, assets bool
	for _, key := range keys {
		if key == "agents__chatbot__ui__app__dist__index.html" {
			index = true
		}
		if strings.HasPrefix(key, "agents__chatbot__ui__app__dist__assets__") {
			assets = true
		}
	}
	if !index {
		t.Error("rendered ConfigMap has no agents__chatbot__ui__app__dist__index.html; the chatbot's /ui would 404")
	}
	if !assets {
		t.Error("rendered ConfigMap has no agents__chatbot__ui__app__dist__assets__* key; the SPA would load without its bundle")
	}

	// The descriptor key is co-generated from ragUnits, so it is emitted whether
	// or not the packaging step placed the file -- assert it is served all the same.
	var descriptor bool
	for _, key := range keys {
		if key == "agents__chatbot__ui__ui.yaml" {
			descriptor = true
		}
	}
	if !descriptor {
		t.Error("rendered ConfigMap has no agents__chatbot__ui__ui.yaml; the UI descriptor is unserved")
	}
}

// The observer serves the bundle compiled into agent-core (GH-737), so the
// chart ships no observer UI at all: no dedicated ConfigMap, no key in the
// profiles projection, and no mount on its Deployment. A leftover here is a
// release paying for files the pod never reads.
func TestObserverChartShipsNoUIBundle(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	staged, cleanup, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
	if err != nil {
		t.Fatalf("stage chart: %v", err)
	}
	defer cleanup()
	out, err := exec.Command("helm", "template", "relx", staged, "--namespace", "nsy").CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	render := string(out)
	for _, absent := range []string{
		"agents__observer__ui__dist__",
		"name: relx-chatbot-mesh-observer-ui",
		"OBSERVER_UI_ROOT",
		"stage-observer-ui",
	} {
		if strings.Contains(render, absent) {
			t.Errorf("the render still carries %q; the observer UI is the agent-core bundle now", absent)
		}
	}
	// The co-generated monitor surface is what the observer actually mounts, and
	// it selects the bundle.
	var observerProfiles string
	for _, doc := range strings.Split(render, "\n---") {
		// Deployments name the same ConfigMap in their volumes, so match the
		// object itself rather than every mention of it.
		if strings.Contains(doc, "kind: ConfigMap") &&
			strings.Contains(doc, "name: relx-chatbot-mesh-profiles-applications") {
			observerProfiles = doc
		}
	}
	if !strings.Contains(observerProfiles, "applications__chatbot-mesh__observer__monitor-rest.yaml") {
		t.Error("the applications profiles ConfigMap carries no co-generated observer monitor-rest.yaml")
	}
	if !strings.Contains(observerProfiles, "bundle: observer") {
		t.Error("the co-generated observer monitor surface does not select the embedded bundle")
	}
}

// TestChatbotUIUsesDedicatedConfigMapAtPreservedPath is the observer test's twin
// for the chatbot bundle (GH-131), and it asserts the move end to end rather
// than only its effect on the shared object's size.
//
// Removing the bundle from the shared ConfigMap shrinks that object whether or
// not anything still serves the panel, so a size assertion alone passes on a
// broken deployment. These checks pair the removal with the ConfigMap that now
// carries the bytes, the mount that delivers them, the root that points at it,
// and the items mapping that restores the real file layout. Each can be wrong
// while every size check stays green and the panel 404s.
func TestChatbotUIUsesDedicatedConfigMapAtPreservedPath(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	staged, cleanup, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
	if err != nil {
		t.Fatalf("stage chart: %v", err)
	}
	defer cleanup()
	out, err := exec.Command("helm", "template", "relx", staged, "--namespace", "nsy").CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	var shared, chatbotUI, chatbotDeployment string
	for _, doc := range strings.Split(string(out), "\n---") {
		switch {
		case strings.Contains(doc, "kind: Deployment") &&
			strings.Contains(doc, "name: relx-chatbot-mesh-chatbot"):
			chatbotDeployment = doc
		case strings.Contains(doc, "name: relx-chatbot-mesh-profiles-chatbot"):
			shared = doc
		case strings.Contains(doc, "name: relx-chatbot-mesh-chatbot-ui"):
			chatbotUI = doc
		}
	}
	if strings.Contains(shared, "agents__chatbot__ui__app__dist__") {
		t.Error("the chatbot profiles ConfigMap still carries the chatbot SPA bundle")
	}
	if !strings.Contains(chatbotUI, "agents__chatbot__ui__app__dist__index.html") {
		t.Error("chatbot-only ConfigMap is missing the SPA index")
	}
	for _, contract := range []string{
		"name: chatbot-ui",
		"mountPath: \"/chatbot-ui\"",
		"name: relx-chatbot-mesh-chatbot-ui",
		"name: CHATBOT_UI_ROOT",
		"value: \"/chatbot-ui\"",
		// The items mapping restores the real layout from the flattened keys.
		// Without it every file lands under its double-underscore key name and
		// the panel 404s while the ConfigMap still looks correct.
		"path: index.html",
	} {
		if !strings.Contains(chatbotDeployment, contract) {
			t.Errorf("chatbot Deployment missing dedicated UI mount contract %q", contract)
		}
	}
	// CHATBOT_UI_ROOT already pointed at this path before the bundle moved, so
	// the move needs no new env entry -- and adding one renders a Deployment the
	// API server rejects with "duplicate entries for key". A Contains check
	// cannot see that, because the string it looks for is present either way.
	// The first attempt at GH-131 declared the duplicate and every static check
	// passed; the helm upgrade failed. Count instead.
	if occurrences := strings.Count(chatbotDeployment, "name: CHATBOT_UI_ROOT"); occurrences != 1 {
		t.Errorf("chatbot Deployment declares CHATBOT_UI_ROOT %d times, want exactly 1; "+
			"a duplicate env key renders a Deployment the API server refuses to apply", occurrences)
	}
}

// TestStagedProfilesFitTheConfigMapLimit proves every rendered profiles group
// ConfigMap stays inside Kubernetes' 1 MiB limit with room to grow, and that
// the per-agent split (GH-311) serves every staged profile exactly once.
//
// The history is the argument for the shape. The single shared object crossed
// its three-quarter threshold twice: at rel10.2 the chatbot SPA bundle pushed
// it to 797,064 bytes until GH-131 moved the bundle to a dedicated ConfigMap,
// and at rel11.5 the read-boundary and facet declarations pushed it to
// 801,455 bytes, which GH-311 answers by splitting the projection per agent.
// Each object now carries what its own workload reads, a declaration edit
// rolls only the pods that mount it, and the three-quarter threshold holds
// per object with real margin behind it.
//
// The completeness half is what keeps the split honest: membership is derived
// twice, in profiles-configmap.yaml and in the profilesVolume helper, and a
// staged file falling through both would vanish from every pod without a
// render error. Every staged profiles/** key (minus the UI-bundle and
// co-generation exclusions the templates document) must appear in exactly one
// group object.
func TestStagedProfilesFitTheConfigMapLimit(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	staged, cleanup, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
	if err != nil {
		t.Fatalf("stage chart: %v", err)
	}
	defer cleanup()

	out, err := exec.Command("helm", "template", "relx", staged, "--namespace", "nsy").CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	groupSizes := map[string]int{}
	keyCounts := map[string]int{}
	for _, doc := range strings.Split(string(out), "\n---") {
		if !strings.Contains(doc, "kind: ConfigMap") {
			continue
		}
		nameMatch := regexp.MustCompile(`name: relx-chatbot-mesh-profiles-([a-z-]+)`).FindStringSubmatch(doc)
		if nameMatch == nil {
			continue
		}
		groupSizes[nameMatch[1]] = len(doc)
		for _, keyMatch := range regexp.MustCompile(`(?m)^  ([A-Za-z0-9_.-]+__[A-Za-z0-9_.-]+(?:__[A-Za-z0-9_.-]+)*): \|-`).FindAllStringSubmatch(doc, -1) {
			keyCounts[keyMatch[1]]++
		}
	}
	if len(groupSizes) < 2 {
		t.Fatalf("rendered chart has %d profiles group ConfigMaps, want the per-agent split", len(groupSizes))
	}

	const configMapLimit = 1 << 20
	const safetyThreshold = configMapLimit * 3 / 4
	for group, size := range groupSizes {
		t.Logf("profiles-%s: %d bytes, %.1f%% of the %d-byte limit (threshold %d)",
			group, size, float64(size)*100/float64(configMapLimit), configMapLimit, safetyThreshold)
		if size > safetyThreshold {
			t.Errorf("profiles-%s is %d bytes, over the %d-byte safety threshold for a %d-byte ConfigMap",
				group, size, safetyThreshold, configMapLimit)
		}
	}

	// Completeness: every staged key lands in exactly one group object.
	stagedKeys := stagedProfileKeys(t, staged)
	for key, count := range keyCounts {
		if count != 1 {
			t.Errorf("profile key %s is served by %d group ConfigMaps, want exactly one", key, count)
		}
	}
	for _, key := range stagedKeys {
		if keyCounts[key] == 0 {
			t.Errorf("staged profile %s is served by no group ConfigMap; the split dropped it", key)
		}
	}
}

// stagedProfileKeys lists the "__"-encoded keys the staged chart's profiles
// tree should serve, applying the same exclusions the templates document: the
// served UI bundles travel in their agents' own ConfigMaps, and the
// co-generated chatbot keys replace their packaged files under the same names.
func stagedProfileKeys(t *testing.T, staged string) []string {
	t.Helper()
	var keys []string
	root := filepath.Join(staged, "profiles")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key := strings.ReplaceAll(filepath.ToSlash(rel), "/", "__")
		if strings.HasPrefix(key, "agents__observer__ui__dist__") ||
			strings.HasPrefix(key, "agents__chatbot__ui__app__dist__") {
			return nil
		}
		keys = append(keys, key)
		return nil
	})
	if err != nil {
		t.Fatalf("walk staged profiles: %v", err)
	}
	if len(keys) == 0 {
		t.Fatal("staged chart has no profile files; the completeness check would pass vacuously")
	}
	return keys
}

// TestStagedProfilesExcludeTestFixtures proves the agent rig fixtures do not
// reach a pod (GH-729). They are mock LLM and RAG definitions, scenarios, and
// their own profiles: test doubles with no runtime role. Every staged file
// becomes a ConfigMap key and a projected mount item in every agent pod, so
// shipping them means production pods mount mock service definitions and the
// ConfigMap grows with the test suite rather than with the product.
//
// This is the coupling GH-702 removed for the chatbot UI tree, reintroduced by fixtures
// that arrived after it. The assertion exists so the next fixture directory
// cannot re-enter silently.
func TestStagedProfilesExcludeTestFixtures(t *testing.T) {
	for _, key := range renderedProfileKeys(t) {
		if strings.Contains(key, "__tests__") {
			t.Errorf("rendered ConfigMap carries %s; agent test fixtures do not belong in a pod", key)
		}
	}
}

// TestStagedProfilesKeepWhatAnAgentRuns is the counterpart: pruning fixtures must
// not take a profile an agent needs. TestStagedProfilesCoverEnabledDeployments
// guards the staging list, and this guards what survives the prune -- the
// distinction matters because a prune runs after that list is satisfied.
func TestStagedProfilesKeepWhatAnAgentRuns(t *testing.T) {
	keys := renderedProfileKeys(t)
	for _, agent := range []string{"chatbot", "rag-server", "provisioning-workflow-orchestrator", "creator", "applier"} {
		want := "agents__" + agent + "__profile.yaml"
		if agent == "applier" {
			want = "applications__chatbot-mesh__applier__profile.yaml"
		}
		var found bool
		for _, key := range keys {
			if key == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no %s key in the rendered ConfigMap; the prune took a profile an agent starts with", want)
		}
	}
}
