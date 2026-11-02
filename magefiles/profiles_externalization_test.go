// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// profilesProjection is what a pod's projected profiles volume resolves to:
// the ConfigMaps it names and the paths their items contribute, keyed by
// workload. Two renders that agree on the paths load the same profile tree
// whatever half of the projection a file came from; a name with no object
// behind it never mounts at all.
type profilesProjection struct {
	ConfigMaps []string
	Paths      []string
}

func projectedProfiles(t *testing.T, render string) map[string]profilesProjection {
	t.Helper()
	type projectedItem struct {
		Key  string `yaml:"key"`
		Path string `yaml:"path"`
	}
	type projectedSource struct {
		ConfigMap *struct {
			Name  string          `yaml:"name"`
			Items []projectedItem `yaml:"items"`
		} `yaml:"configMap"`
	}
	type volume struct {
		Name      string `yaml:"name"`
		Projected *struct {
			Sources []projectedSource `yaml:"sources"`
		} `yaml:"projected"`
	}
	type workload struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Spec struct {
			Template struct {
				Spec struct {
					Volumes []volume `yaml:"volumes"`
				} `yaml:"spec"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}
	projection := map[string]profilesProjection{}
	for _, doc := range strings.Split(render, "\n---") {
		var parsed workload
		if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
			// The documents that are not workloads -- the ConfigMaps
			// themselves -- do not parse into this shape.
			continue
		}
		if parsed.Kind != "Deployment" && parsed.Kind != "StatefulSet" {
			continue
		}
		for _, vol := range parsed.Spec.Template.Spec.Volumes {
			if vol.Name != "profiles" || vol.Projected == nil {
				continue
			}
			var view profilesProjection
			for _, source := range vol.Projected.Sources {
				if source.ConfigMap == nil {
					continue
				}
				view.ConfigMaps = append(view.ConfigMaps, source.ConfigMap.Name)
				for _, item := range source.ConfigMap.Items {
					view.Paths = append(view.Paths, item.Path)
				}
			}
			sort.Strings(view.Paths)
			projection[parsed.Kind+"/"+parsed.Metadata.Name] = view
		}
	}
	if len(projection) == 0 {
		t.Fatal("the render projects no profiles volume at all")
	}
	return projection
}

func renderChartAtPath(t *testing.T, releaseName, chart string, valueArgs ...string) string {
	t.Helper()
	args := append([]string{"template", releaseName, chart, "--namespace", "nsy"}, valueArgs...)
	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("render %s: %v\n%s", chart, err, out)
	}
	return string(out)
}

// The acceptance the saving has to clear: no profile a pod reads goes missing.
// The externalized render must project the same paths into the same workloads
// as the release-resident one, or an agent loads a short closure and refuses at
// boot with a word whose rest_ref is undefined.
func TestExternalProfilesProjectTheSameClosure(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	const releaseName = "closure-fixture"
	chartDir := findChartDir(t)

	inRelease, cleanupInRelease, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupInRelease()
	before := projectedProfiles(t, renderChartAtPath(t, releaseName, inRelease))

	externalized, cleanupExternalized, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupExternalized()
	profiles, cleanupProfiles, err := externalizeProfiles(externalized, releaseName)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupProfiles()
	after := projectedProfiles(t, renderChartAtPath(
		t, releaseName, externalized, externalProfileValueArgs(profiles)...))

	if len(before) != len(after) {
		t.Fatalf("workloads projecting profiles: before=%d after=%d", len(before), len(after))
	}
	for workload, want := range before {
		got, ok := after[workload]
		if !ok {
			t.Errorf("%s projects no profiles after externalization", workload)
			continue
		}
		missing := setDifference(want.Paths, got.Paths)
		added := setDifference(got.Paths, want.Paths)
		if len(missing) > 0 {
			t.Errorf("%s loses %d profiles: %v", workload, len(missing), missing)
		}
		if len(added) > 0 {
			t.Errorf("%s gains profiles it did not have: %v", workload, added)
		}
	}
}

// The failure this misses in unit tests and pays for on a cluster: a projected
// source naming a ConfigMap nothing renders and nothing provisions leaves every
// pod mounting it in PodInitializing, reported only as a rollout timeout. Every
// name the projection carries has to be either rendered by the release or
// provisioned beside it.
func TestEveryProjectedProfilesConfigMapExists(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	const releaseName = "existence-fixture"
	chartDir := findChartDir(t)
	staged, cleanupChart, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupChart()
	profiles, cleanupProfiles, err := externalizeProfiles(staged, releaseName)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupProfiles()
	render := renderChartAtPath(
		t, releaseName, staged, externalProfileValueArgs(profiles)...)

	provisioned := map[string]bool{}
	for _, group := range profiles.Groups {
		provisioned[group.ConfigMapName] = true
	}
	for _, doc := range strings.Split(render, "\n---") {
		var parsed struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
		}
		if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
			continue
		}
		if parsed.Kind == "ConfigMap" {
			provisioned[parsed.Metadata.Name] = true
		}
	}
	for workload, view := range projectedProfiles(t, render) {
		for _, name := range view.ConfigMaps {
			if !provisioned[name] {
				t.Errorf("%s projects ConfigMap %q, which neither the release renders"+
					" nor the install provisions", workload, name)
			}
		}
	}
}

func setDifference(from, minus []string) []string {
	remove := map[string]bool{}
	for _, value := range minus {
		remove[value] = true
	}
	var rest []string
	for _, value := range from {
		if !remove[value] {
			rest = append(rest, value)
		}
	}
	return rest
}

// What stays in the chart and what is dropped from it. A file the chart reads
// with Files.Get is an input to a render and has to stay, whether or not its own
// key also travels as a projected entry; a file whose key is co-generated is
// supplied by that render, so unless the render also reads it, the packaged copy
// is dead weight and is deleted rather than externalized.
func TestExternalProfilesKeepRenderedSourcesAndDropCoGeneratedCopies(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	staged, cleanupChart, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupChart()
	cogenerated, err := coGeneratedProfileKeys(staged)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := chartRenderedProfileSources(staged)
	if err != nil {
		t.Fatal(err)
	}
	if len(rendered) == 0 {
		t.Fatal("the chart reads no staged profile with Files.Get")
	}
	profiles, cleanupProfiles, err := externalizeProfiles(staged, "exclusion-fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupProfiles()

	for _, group := range profiles.Groups {
		for _, key := range group.Keys {
			if cogenerated[key] {
				t.Errorf("%s externalizes co-generated key %q, which the render supplies", group.Group, key)
			}
		}
	}
	for source := range rendered {
		if _, err := os.Stat(filepath.Join(staged, filepath.FromSlash(source))); err != nil {
			t.Errorf("the chart no longer carries %s, which a template renders from: %v", source, err)
		}
	}
	for key := range cogenerated {
		path := filepath.Join(staged, "profiles", filepath.FromSlash(strings.ReplaceAll(key, "__", "/")))
		if rendered["profiles/"+strings.ReplaceAll(key, "__", "/")] {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			t.Errorf("the chart still packages %s, whose content the render supplies", key)
		}
	}
}

// Group membership has to match the split the chart's own glob makes, or a
// projected source names keys the group's ConfigMap does not hold.
func TestExternalProfileGroupsMatchTheChartSplit(t *testing.T) {
	for relative, want := range map[string]string{
		"agents/chatbot/request-machine.yaml":                      "chatbot",
		"agents/rag-server/rest.yaml":                              "rag-server",
		"applications/chatbot-mesh/observer/profile.yaml":          "applications",
		"applications/chatbot-mesh/applier/exec-declarations.yaml": "applications",
	} {
		if got := profileGroupOf(relative); got != want {
			t.Errorf("profileGroupOf(%q) = %q, want %q", relative, got, want)
		}
	}
}

// The keys ride in values because the files are no longer in the chart to
// enumerate. Every group has to name its ConfigMap and the keys projected from
// it, and each key has to be a file the provisioned directory holds.
func TestExternalProfileValuesNameEveryGroupAndKey(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	staged, cleanupChart, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupChart()
	const releaseName = "values-fixture"
	profiles, cleanupProfiles, err := externalizeProfiles(staged, releaseName)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupProfiles()

	var document struct {
		Profiles struct {
			External map[string]struct {
				ConfigMap string   `yaml:"configMap"`
				Checksum  string   `yaml:"checksum"`
				Keys      []string `yaml:"keys"`
			} `yaml:"external"`
		} `yaml:"profiles"`
	}
	data, err := os.ReadFile(profiles.ValuesFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Profiles.External) != len(profiles.Groups) {
		t.Fatalf("values name %d groups, externalization produced %d",
			len(document.Profiles.External), len(profiles.Groups))
	}
	for _, group := range profiles.Groups {
		valued, ok := document.Profiles.External[group.Group]
		if !ok {
			t.Errorf("values name no group %q", group.Group)
			continue
		}
		if valued.ConfigMap != group.ConfigMapName || valued.Checksum != group.Checksum {
			t.Errorf("group %q values = %+v, want %s/%s",
				group.Group, valued, group.ConfigMapName, group.Checksum)
		}
		if !strings.HasPrefix(group.ConfigMapName, releaseName+"-profiles-"+group.Group+"-") {
			t.Errorf("group %q ConfigMap %q is not named for this release and group",
				group.Group, group.ConfigMapName)
		}
		if len(valued.Keys) != len(group.Keys) {
			t.Errorf("group %q values name %d keys, want %d",
				group.Group, len(valued.Keys), len(group.Keys))
		}
		for _, key := range group.Keys {
			if _, err := os.Stat(filepath.Join(group.Dir, key)); err != nil {
				t.Errorf("group %q names key %q that its directory does not hold: %v",
					group.Group, key, err)
			}
		}
	}
	if args := externalProfileValueArgs(profiles); len(args) != 2 ||
		args[0] != "--values" || args[1] != profiles.ValuesFile {
		t.Errorf("value args = %v, want --values %s", args, profiles.ValuesFile)
	}
	if args := externalProfileValueArgs(profilesExternalizationResult{}); args != nil {
		t.Errorf("an empty externalization contributes %v, want no flag", args)
	}
}

// The ConfigMap name carries the content checksum, so an install that changed a
// profile writes a new object rather than mutating one a running pod mounts.
// Identical content has to produce an identical name, or every install rolls
// every workload.
func TestExternalProfilesAreDeterministic(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	const releaseName = "determinism-fixture"
	stage := func() (profilesExternalizationResult, func()) {
		staged, cleanupChart, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
		if err != nil {
			t.Fatal(err)
		}
		profiles, cleanupProfiles, err := externalizeProfiles(staged, releaseName)
		if err != nil {
			cleanupChart()
			t.Fatal(err)
		}
		return profiles, func() {
			cleanupProfiles()
			cleanupChart()
		}
	}
	first, cleanupFirst := stage()
	defer cleanupFirst()
	second, cleanupSecond := stage()
	defer cleanupSecond()
	if len(first.Groups) != len(second.Groups) {
		t.Fatalf("group count changed across identical staging: %d then %d",
			len(first.Groups), len(second.Groups))
	}
	for index, group := range first.Groups {
		other := second.Groups[index]
		if group.Group != other.Group || group.Checksum != other.Checksum ||
			group.ConfigMapName != other.ConfigMapName {
			t.Errorf("group %d changed across identical staging:\nfirst=%+v\nsecond=%+v",
				index, group, other)
		}
	}

	changed, cleanupChanged := stage()
	defer cleanupChanged()
	target := changed.Groups[0]
	path := filepath.Join(target.Dir, target.Keys[0])
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	checksum, err := profileGroupChecksum(&target)
	if err != nil {
		t.Fatal(err)
	}
	if checksum == target.Checksum {
		t.Error("a changed profile produced the same group checksum, so the install would" +
			" reuse the ConfigMap a running pod has mounted")
	}
}

// Every group's object is created from its own directory, under the name the
// values give the chart. A group named but not provisioned leaves its pods in
// PodInitializing, which only an --atomic rollout ever reports.
func TestProvisionExternalProfilesCreatesEveryGroup(t *testing.T) {
	profiles := profilesExternalizationResult{
		Groups: []externalProfileGroup{
			{Group: "chatbot", ConfigMapName: "fixture-profiles-chatbot-abc", Dir: t.TempDir()},
			{Group: "applications", ConfigMapName: "fixture-profiles-applications-def", Dir: t.TempDir()},
		},
	}
	var commands []string
	err := provisionExternalProfiles(func(name string, args ...string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil, nil
	}, profiles)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range profiles.Groups {
		for _, want := range []string{
			"kubectl delete configmap " + group.ConfigMapName + " --ignore-not-found",
			"kubectl create configmap " + group.ConfigMapName + " --from-file=" + group.Dir,
		} {
			found := false
			for _, command := range commands {
				found = found || command == want
			}
			if !found {
				t.Errorf("the install never runs %q:\n%s", want, strings.Join(commands, "\n"))
			}
		}
	}
}

// The rollout annotation has to keep covering the profiles it no longer holds.
// A workload rolls when its own mounted profiles change (GH-311), and the
// checksum the deployment measured is the only view of externalized content the
// render has left.
func TestExternalProfileChecksumDrivesTheRolloutAnnotation(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	staged, cleanupChart, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupChart()
	const releaseName = "rollout-fixture"
	profiles, cleanupProfiles, err := externalizeProfiles(staged, releaseName)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupProfiles()

	annotationOf := func(render string) string {
		t.Helper()
		for _, doc := range strings.Split(render, "\n---") {
			if !strings.Contains(doc, "kind: Deployment") ||
				!strings.Contains(doc, "name: "+releaseName+"-chatbot-mesh-chatbot\n") {
				continue
			}
			for _, line := range strings.Split(doc, "\n") {
				if _, value, found := strings.Cut(strings.TrimSpace(line), "checksum/profiles: "); found {
					return value
				}
			}
		}
		t.Fatal("the chatbot Deployment carries no checksum/profiles annotation")
		return ""
	}

	base := annotationOf(renderChartAtPath(
		t, releaseName, staged, externalProfileValueArgs(profiles)...))
	changed := annotationOf(renderChartAtPath(
		t, releaseName, staged,
		append(externalProfileValueArgs(profiles),
			"--set-string", "profiles.external.chatbot.checksum=00000000")...))
	if base == changed {
		t.Error("the rollout annotation ignores the externalized profiles' checksum," +
			" so a profile edit would leave the workload running the old closure")
	}
}

// A key the ConfigMap serves from a rewritten packaged file cannot travel
// verbatim: the applier's exec declarations carry placeholder coordinates that
// only the render replaces (GH-484), so externalizing them would install an
// applier that upgrades a release it does not belong to.
func TestRewrittenProfileKeysStayInRelease(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	staged, cleanupChart, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupChart()
	rewritten, err := chartProfileKeyList(staged, "chatbot-mesh.renderedProfileKeys")
	if err != nil {
		t.Fatal(err)
	}
	const releaseName = "rewrite-fixture"
	profiles, cleanupProfiles, err := externalizeProfiles(staged, releaseName)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupProfiles()

	for _, group := range profiles.Groups {
		for _, key := range group.Keys {
			if rewritten[key] {
				t.Errorf("%s externalizes %q verbatim, losing the render that rewrites it",
					group.Group, key)
			}
		}
	}
	for key := range rewritten {
		path := filepath.Join(staged, "profiles",
			filepath.FromSlash(strings.ReplaceAll(key, "__", "/")))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the chart no longer carries %s, which its own ConfigMap renders: %v", key, err)
		}
	}
	render := renderChartAtPath(t, releaseName, staged,
		append(externalProfileValueArgs(profiles), "--set", "applier.enabled=true")...)
	if strings.Contains(render, "deployment/chatbot-mesh-chatbot") {
		t.Error("the externalized render carries a baked release coordinate," +
			" so the applier would target another release (GH-484)")
	}
	if !strings.Contains(render, "deployment/"+releaseName+"-chatbot-mesh-chatbot") {
		t.Error("the externalized render does not rewrite the applier exec coordinate to this release")
	}
}
