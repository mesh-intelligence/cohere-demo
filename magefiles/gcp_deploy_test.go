// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mesh-intelligence/declarative-agents/magefiles/apprig"
	"gopkg.in/yaml.v3"
)

// The two pull-through repositories a test environment resolves to.
const (
	gcpTestGhcr      = "us-central1-docker.pkg.dev/p/cohere-demo-ghcr"
	gcpTestDockerhub = "us-central1-docker.pkg.dev/p/cohere-demo-dockerhub"
)

const gcpTestHost = "34-1-2-3.sslip.io"

func gcpTestApplication() gcpConfig {
	config := gcpConfig{Project: "p", Region: "us-central1", Cluster: "cohere-demo-dev"}
	return config.ForApplication(cohereApplicationRelease)
}

// renderGcpRelease renders the chart the way gcp:deploy deploys it: the GCP
// overlay in place of kind-values.yaml, then the decided overrides.
func renderGcpRelease(t *testing.T) (apprig.Resolved, string) {
	t.Helper()
	runner, err := cohereApplicationRunner()
	if err != nil {
		t.Fatal(err)
	}
	application := gcpTestApplication()
	runner.Binding = gcpCohereBinding(runner.Binding.ApplicationRoot, application, "unused")
	resolved, err := applicationResolved(runner)
	if err != nil {
		t.Fatal(err)
	}
	chartDir := findChartDir(t)
	overlay := filepath.Join(chartDir, "ci", gcpValuesFile)
	tag, err := gcpRuntimeTag(filepath.Join(chartDir, "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	exporterTag, err := exporterReleaseTag(filepath.Dir(chartDir))
	if err != nil {
		t.Fatal(err)
	}
	images := gcpDeployImages{
		Registries: gcpRegistries{Ghcr: gcpTestGhcr, Dockerhub: gcpTestDockerhub},
		RuntimeTag: tag, ExporterTag: exporterTag, Host: gcpTestHost,
	}
	overrides, err := gcpCohereOverrides(resolved, images,
		[]externalUIAsset{{Component: "chatbot", ConfigMapName: "ui-chatbot", Checksum: strings.Repeat("a", 64)}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "overrides.yaml")
	writePackageTestFile(t, file, overrides)
	out, err := exec.Command("helm", "template", resolved.Release, chartDir,
		"--namespace", resolved.Namespace, "--values", overlay, "-f", file).CombinedOutput()
	if err != nil {
		t.Fatalf("the chart rejects the gcp:deploy values: %v\n%s", err, firstRunes(string(out), 1200))
	}
	return resolved, string(out)
}

// Every image the GKE release renders is pulled from the project registry,
// the release renders no in-cluster model tier, and the exporter runs from its
// published image at the tag exporter:publish derives (#869).
func TestGcpReleaseRendersArtifactRegistryImagesOnly(t *testing.T) {
	resolved, output := renderGcpRelease(t)
	if resolved.Namespace != "app-cohere-demo" || resolved.BucketURL != "gs://p-cohere-demo-telemetry" {
		t.Fatalf("resolved %s in %s", resolved.BucketURL, resolved.Namespace)
	}
	images := 0
	for _, line := range strings.Split(output, "\n") {
		field := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if !strings.HasPrefix(field, "image:") {
			continue
		}
		images++
		image := strings.Trim(strings.TrimSpace(strings.TrimPrefix(field, "image:")), `"'`)
		if !strings.HasPrefix(image, gcpTestGhcr+"/") && !strings.HasPrefix(image, gcpTestDockerhub+"/") {
			t.Errorf("image %q is not pulled through the environment's remote repositories", image)
		}
	}
	if images == 0 {
		t.Fatal("the render named no images")
	}
	for _, absent := range []string{"-ollama", "wait-for-llm-models"} {
		if strings.Contains(output, absent) {
			t.Errorf("the GKE render carries %q", absent)
		}
	}
	exporterTag, err := exporterReleaseTag(filepath.Dir(findChartDir(t)))
	if err != nil {
		t.Fatal(err)
	}
	exporterImage := gcpTestGhcr + "/" + exporterGhcrPath + ":" + exporterTag
	exporterDeployed := false
	for _, document := range strings.Split(output, "\n---") {
		if strings.Contains(document, "kind: Deployment") && strings.Contains(document, "-chatbot-mesh-exporter\n") {
			exporterDeployed = strings.Contains(document, exporterImage)
		}
	}
	if !exporterDeployed {
		t.Errorf("the GKE render does not deploy the exporter from %s", exporterImage)
	}
	for _, want := range []string{
		`{name: COLLECTOR_OBJECT_BUCKET_URL, value: "gs://p-cohere-demo-telemetry"}`,
		`{name: COLLECTOR_OBJECT_ENDPOINT, value: ""}`,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("the GKE render lacks %s", want)
		}
	}
}

// The credential reaches the release only as a Secret reference: the chart
// renders no Secret carrying it, and the overrides cannot name one.
func TestGcpReleaseCarriesNoCredential(t *testing.T) {
	_, output := renderGcpRelease(t)
	if !strings.Contains(output, demoCohereSecret) {
		t.Errorf("the Cohere tier does not reference Secret %q", demoCohereSecret)
	}
	for _, document := range strings.Split(output, "\n---") {
		if strings.Contains(document, "kind: Secret") && strings.Contains(document, "name: "+demoCohereSecret) {
			t.Errorf("the chart renders the credential Secret itself:\n%s", firstRunes(document, 400))
		}
	}
}

// The checked-in overlay cannot deploy on its own: its runtime tag is a
// fail-closed sentinel and its repositories a placeholder registry.
func TestGcpOverlayFailsClosedWithoutOverrides(t *testing.T) {
	var overlay struct {
		Image struct {
			Repository string `yaml:"repository"`
			Tag        string `yaml:"tag"`
		} `yaml:"image"`
	}
	if err := readIntegrationYAML(filepath.Join(findChartDir(t), "ci", gcpValuesFile), "GCP overlay", &overlay); err != nil {
		t.Fatal(err)
	}
	if overlay.Image.Tag != "must-be-overridden-with-release-tag" ||
		!strings.HasPrefix(overlay.Image.Repository, "region-docker.pkg.dev/project/") {
		t.Errorf("overlay image = %+v, want the placeholder registry and the sentinel tag", overlay.Image)
	}
}

// Each pulled image keeps the chart's version, so the cloud and kind paths run
// the same dependency (dolt is the deliberate exception, pinned where the chart
// floats on latest), and the overlay's placeholder path ends in the Docker Hub
// source the overrides pull it from.
func TestGcpPulledImagesTrackTheChart(t *testing.T) {
	chartDir := findChartDir(t)
	read := func(path string) map[string]any {
		var values map[string]any
		if err := readIntegrationYAML(path, "values", &values); err != nil {
			t.Fatal(err)
		}
		return values
	}
	chart := read(filepath.Join(chartDir, "values.yaml"))
	overlay := read(filepath.Join(chartDir, "ci", gcpValuesFile))
	at := func(values map[string]any, path []string) map[string]any {
		var node any = values
		for _, key := range path {
			node = node.(map[string]any)[key]
		}
		return node.(map[string]any)
	}
	for _, image := range gcpPulledImages {
		name := strings.Join(image.Path, ".")
		pinned, ours := at(overlay, image.Path), at(chart, image.Path)
		if repository := fmt.Sprint(pinned["repository"]); !strings.HasSuffix(repository, "/cohere-demo-dockerhub/"+image.Source) {
			t.Errorf("%s overlay repository %s does not end in /cohere-demo-dockerhub/%s", name, repository, image.Source)
		}
		if image.Path[0] == "dolt" {
			if fmt.Sprint(pinned["tag"]) == "latest" {
				t.Error("the GCP overlay pulls dolt at the moving latest tag")
			}
			continue
		}
		if fmt.Sprint(pinned["tag"]) != fmt.Sprint(ours["tag"]) {
			t.Errorf("%s pulls tag %v, the chart runs %v", name, pinned["tag"], ours["tag"])
		}
	}
}

// GKE runs agent-core at the release tag helm/values.yaml pins (the tag
// deps:verify holds to the release), through the ghcr.io repository; a tag
// that is not a release is refused.
func TestGcpRuntimeImageIsThePublishedRelease(t *testing.T) {
	chartDir := findChartDir(t)
	tag, err := gcpRuntimeTag(filepath.Join(chartDir, "values.yaml"))
	if err != nil || !strings.HasPrefix(tag, "v0.") {
		t.Fatalf("chart runtime tag = %q, %v", tag, err)
	}
	_, output := renderGcpRelease(t)
	want := "image: \"" + gcpTestGhcr + "/" + gcpRuntimeImageSource + ":" + tag + "\""
	if !strings.Contains(output, want) && !strings.Contains(output, strings.Trim(want, "\"")) {
		t.Errorf("the GKE render does not run %s:%s through the ghcr.io repository", gcpRuntimeImageSource, tag)
	}
	for _, bad := range []string{"must-be-overridden", "latest"} {
		if strings.Contains(output, gcpRuntimeImageSource+":"+bad) {
			t.Errorf("the GKE render runs agent-core at %q", bad)
		}
	}
	branch := filepath.Join(t.TempDir(), "values.yaml")
	writePackageTestFile(t, branch, "image:\n  tag: 2a261fc99773\n")
	if _, err := gcpRuntimeTag(branch); err == nil {
		t.Error("a commit-revision tag was accepted; GKE runs release images only")
	}
}

// A deploy waits for both repositories, and names the ghcr.io one's remedy:
// the registry token, gcp:secrets, then gcp:up.
func TestGcpRegistriesRequireBoth(t *testing.T) {
	if err := (gcpRegistries{Ghcr: "g", Dockerhub: "d"}).require(); err != nil {
		t.Errorf("both present = %v", err)
	}
	if err := (gcpRegistries{Dockerhub: "d"}).require(); err == nil || !strings.Contains(err.Error(), "ghcr_pat") ||
		!strings.Contains(err.Error(), "gcp:secrets") {
		t.Errorf("no ghcr.io repository = %v", err)
	}
	if err := (gcpRegistries{Ghcr: "g"}).require(); err == nil || !strings.Contains(err.Error(), "gcp:up") {
		t.Errorf("no Docker Hub repository = %v", err)
	}
}

// The chart renders none of the chatbot's edge on GKE: no Ingress, no
// managed certificate, no BackendConfig, and no backend-config annotation.
// The exposure root's Gateway owns it, because only a route can strip the
// Google front end and IAP headers the agent refuses (#885). Nothing routes
// the control plane, the observer, or the collector either.
func TestGcpReleaseRendersNoIngressLayer(t *testing.T) {
	_, output := renderGcpRelease(t)
	for _, document := range strings.Split(output, "\n---") {
		for _, kind := range []string{"Ingress", "ManagedCertificate", "BackendConfig"} {
			if strings.Contains(document, "\nkind: "+kind+"\n") {
				t.Errorf("the GKE render carries a %s:\n%s", kind, firstRunes(document, 400))
			}
		}
	}
	for _, absent := range []string{"cloud.google.com/backend-config", "kubernetes.io/ingress.class: gce",
		"path: /provisioning", `host: "observer.`, `host: "telemetry.`} {
		if strings.Contains(output, absent) {
			t.Errorf("the GKE render carries %s", absent)
		}
	}
}

// Every route on the chatbot's public chat port declares the headers the
// Google front end and IAP add, because the REST server refuses an undeclared
// header and the load balancer cannot remove x-goog-* ones (found live in
// #883: every request behind IAP answered 400). The kind render declares none.
func TestGcpChatbotRoutesDeclareTheEdgeHeaders(t *testing.T) {
	chatEndpoints := func(render string) map[string]any {
		t.Helper()
		var rest map[string]any
		if err := yaml.Unmarshal([]byte(configMapKeyBlock(render, "agents__chatbot__rest.yaml")), &rest); err != nil {
			t.Fatal(err)
		}
		servers, _ := rest["rest"].(map[string]any)["servers"].(map[string]any)
		chat, _ := servers["chatbot_chat"].(map[string]any)
		endpoints, _ := chat["endpoints"].(map[string]any)
		if len(endpoints) == 0 {
			t.Fatal("the render has no chatbot_chat endpoints")
		}
		return endpoints
	}
	declared := func(endpoint any) map[string]any {
		request, _ := endpoint.(map[string]any)["request"].(map[string]any)
		headers, _ := request["headers"].(map[string]any)
		return headers
	}
	_, gke := renderGcpRelease(t)
	for name, endpoint := range chatEndpoints(gke) {
		headers := declared(endpoint)
		for _, header := range gcpEdgeHeaders {
			if _, ok := headers[header]; !ok {
				t.Errorf("GKE route %s does not declare %s", name, header)
			}
		}
	}
	kind, err := exec.Command("helm", "template", cohereApplicationRelease, findChartDir(t),
		"--values", filepath.Join(findChartDir(t), "ci", "kind-values.yaml")).CombinedOutput()
	if err != nil {
		t.Fatalf("kind render: %v\n%s", err, firstRunes(string(kind), 600))
	}
	for name, endpoint := range chatEndpoints(string(kind)) {
		for header := range declared(endpoint) {
			for _, edge := range gcpEdgeHeaders {
				if header == edge {
					t.Errorf("kind route %s declares the GKE edge header %s", name, header)
				}
			}
		}
	}
}

// The collector's WAL is a GCE persistent disk, which mounts root-owned; the
// GKE render gives the collector pod the agent group so the disk is writable
// (found live: spool_collector_spans was denied /work/wal). The agent-services
// library renders it for a persistent WAL (declarative-agents GH-492), so the
// GKE overlay sets no podSecurityContext. The kind render carries no fsGroup.
func TestGcpCollectorJoinsTheAgentGroup(t *testing.T) {
	_, output := renderGcpRelease(t)
	var collector string
	for _, document := range strings.Split(output, "\n---") {
		if strings.Contains(document, "\nkind: Deployment\n") && strings.Contains(document, "name: cohere-demo-chatbot-mesh-collector\n") {
			collector = document
		}
	}
	if !strings.Contains(collector, "fsGroup: 101") || !strings.Contains(collector, "fsGroupChangePolicy: OnRootMismatch") {
		t.Errorf("the GKE collector pod does not join the agent group:\n%s", firstRunes(collector, 1500))
	}
	kind, err := exec.Command("helm", "template", cohereApplicationRelease, findChartDir(t),
		"--values", filepath.Join(findChartDir(t), "ci", "kind-values.yaml")).CombinedOutput()
	if err != nil {
		t.Fatalf("kind render: %v\n%s", err, firstRunes(string(kind), 600))
	}
	if strings.Contains(string(kind), "fsGroup") {
		t.Error("the kind render carries an fsGroup")
	}
}
