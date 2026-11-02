// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/magefile/mage/mg"
	"github.com/mesh-intelligence/declarative-agents/magefiles/apprig"
	"gopkg.in/yaml.v3"
)

// gcpValuesFile is the overlay that makes the unmodified chart install on GKE
// (eng08), layered where app:up layers kind-values.yaml.
const gcpValuesFile = "gcp-values.yaml"

// gcpPulledImage is one third-party image the chart renders, pulled by the
// nodes through the Docker Hub remote repository (#868). Path is its values
// path; Source is its path on Docker Hub. Its tag comes from the overlay.
type gcpPulledImage struct {
	Path   []string
	Source string
}

// gcpPulledImages are the chart's non-runtime images. Nothing is built,
// pushed, or copied at deploy time: Artifact Registry fetches each on first
// pull and caches it, and nodes reach neither Docker Hub nor ghcr.io (eng08).
var gcpPulledImages = []gcpPulledImage{
	{Path: []string{"chroma", "image"}, Source: "chromadb/chroma"},
	{Path: []string{"dolt", "image"}, Source: "dolthub/dolt-sql-server"},
	{Path: []string{"observer", "proxy", "image"}, Source: "rancher/kubectl"},
	{Path: []string{"osint", "fixture", "image"}, Source: "library/busybox"},
}

// gcpRuntimeImageSource is agent-core's path on ghcr.io, pulled through the
// ghcr.io remote repository at the release tag deps:verify pins in
// helm/values.yaml, so GKE runs the published release artifact.
const gcpRuntimeImageSource = "mesh-intelligence/declarative-agents/agent-core"

// gcpCLIDonorSource is the applier's CLI donor on Docker Hub; the overlay
// keeps its digest pin.
const gcpCLIDonorSource = "alpine/k8s"

// Gcp deploys cohere-demo to the GKE cluster gcp.yaml names.
type Gcp mg.Namespace

// Deploy installs or upgrades cohere-demo on the cluster gcp:up created for
// the environment gcp.yaml selects, through the same canonical applier deploy
// machine app:up runs. It uploads no image: the nodes pull agent-core at the
// release tag and every other image through the environment's Artifact
// Registry remote repositories. It points the namespace KSA at the
// application's Google service account and stages the release app:up stages
// with gcp-values.yaml in place of kind-values.yaml.
func (Gcp) Deploy() error {
	run, err := newGcpRun("gcp:deploy")
	if err != nil {
		return err
	}
	root := run.root
	config, registries, err := run.deployConfig()
	if err != nil {
		return err
	}
	runtimeTag, err := gcpRuntimeTag(filepath.Join(root, "helm", "values.yaml"))
	if err != nil {
		return err
	}
	exporterTag, err := exporterReleaseTag(root)
	if err != nil {
		return err
	}
	if err := requireExporterPublished(gcpManifestStatus, registries, exporterTag); err != nil {
		return err
	}
	application := config.ForApplication(cohereApplicationRelease)
	if err := requireGcpSecretVersions(gcpDefaultRun, application); err != nil {
		return err
	}
	access, err := loadGcpAccess(root)
	if err != nil {
		return err
	}
	if err := requireIAPMembers(access); err != nil {
		return err
	}
	iapClientID, err := operatorKey(gcpIAPClientIDKeysField)
	if err != nil {
		return err
	}
	address, err := gcpDescribeAddress(gcpDefaultRun, config.Project)
	if err != nil {
		return fmt.Errorf("%w; mage gcp:up creates it in the bootstrap root", err)
	}
	host := gcpExposureHost(access, address)
	kubeconfigDir, err := os.MkdirTemp("", "cohere-demo-gcp-kubeconfig-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(kubeconfigDir) }()
	kubeconfig := filepath.Join(kubeconfigDir, "config")
	if err := writeGcpKubeconfig(config, kubeconfig); err != nil {
		return err
	}
	runner, err := cohereApplicationRunner()
	if err != nil {
		return err
	}
	runner.Binding = gcpCohereBinding(root, application, kubeconfig)
	runner.Verify = runner.DefaultVerify
	runner.Prepare = func(resolved apprig.Resolved) (apprig.Preparation, error) {
		return prepareGcpCohere(runner, root, config, application, gcpDeployImages{
			Registries: registries, RuntimeTag: runtimeTag, ExporterTag: exporterTag, Host: host,
		}, resolved)
	}
	fmt.Printf("gcp:deploy: %s -> cluster %s (%s), https://%s\n",
		cohereApplicationRelease, config.Cluster, config.Region, host)
	started := time.Now()
	if err := gcpLogOutcome(config.Cluster, "deploy", "applied", started, runner.Up()); err != nil {
		return err
	}
	// The exposure root plans against the namespace and the chatbot Service
	// the release creates, so it follows the deploy (#885).
	exposure := gcpExposureRoot(root, run.settings, access, config.Cluster, address, iapClientID)
	if err := applyGcpRoot(run.tf, exposure, config.Cluster); err != nil {
		return err
	}
	fmt.Printf("gcp:deploy: the chatbot serves at https://%s once its certificate is active"+
		" (about 15 minutes on a new host)\n", host)
	return nil
}

// gcpCohereBinding is the app:up binding pointed at GKE: the cluster's own
// kubeconfig, the application's bucket, and the GCP overlay.
func gcpCohereBinding(root string, application gcpConfig, kubeconfig string) apprig.PlatformBinding {
	binding := cohereApplicationBinding(root, application.Cluster)
	binding.KubeconfigPath = kubeconfig
	binding.BucketURLTemplate = "gs://" + application.Project + "-%s-telemetry"
	binding.ValuesPath = filepath.Join(root, "helm", "ci", gcpValuesFile)
	return binding
}

// gcpDeployImages is what a GKE deploy resolves before Prepare: the two
// pull-through repositories, the release tag agent-core runs at, and the
// public host.
type gcpDeployImages struct {
	Registries  gcpRegistries
	RuntimeTag  string
	ExporterTag string
	Host        string
}

// gcpRuntimeTag is the agent-core release tag helm/values.yaml pins, which
// deps:verify holds to the declarative-agents release. A tag that is not a
// release is refused: GKE runs published artifacts, never a branch build or
// latest (eng01).
func gcpRuntimeTag(valuesPath string) (string, error) {
	var values struct {
		Image struct {
			Tag string `yaml:"tag"`
		} `yaml:"image"`
	}
	if err := readIntegrationYAML(valuesPath, "chart values", &values); err != nil {
		return "", err
	}
	tag := strings.TrimSpace(values.Image.Tag)
	if !strings.HasPrefix(tag, "v0.") {
		return "", fmt.Errorf("gcp:deploy: %s image.tag %q is not a declarative-agents release tag;"+
			" GKE runs the published release image", valuesPath, tag)
	}
	return tag, nil
}

// prepareGcpCohere readies the release on GKE: the namespace and its Workload
// Identity annotation, the staged chart with its out-of-release ConfigMaps,
// the credential Secrets synced from Secret Manager, and the overrides that
// point every image at the pull-through repositories. Nothing is built or
// uploaded. The release budget is gated as app:up gates it.
func prepareGcpCohere(
	runner apprig.Runner, root string, config, application gcpConfig, images gcpDeployImages,
	resolved apprig.Resolved,
) (preparation apprig.Preparation, result error) {
	if resolved.Namespace != application.Namespace || resolved.BucketURL != application.BucketURL() {
		return preparation, fmt.Errorf("gcp:deploy resolved %s in %s, but the cloud binding is %s in %s",
			resolved.BucketURL, resolved.Namespace, application.BucketURL(), application.Namespace)
	}
	// Every cluster command binds the run's own kubeconfig; no ambient context.
	cluster := gcpKubeconfigRun(runner.Binding.KubeconfigPath)
	created, err := ensureGcpNamespace(cluster, resolved.Namespace)
	if err != nil {
		return preparation, err
	}
	defer func() {
		if result != nil && created {
			result = errors.Join(result, deleteGcpNamespace(cluster, resolved.Namespace))
		}
	}()
	// The namespace exists only now, so the KSA half of Workload Identity is
	// applied here; without it the collector is denied its bucket.
	if err := gcpAnnotateKSA(gcpDefaultRun, application, runner.Binding.KubeconfigPath); err != nil {
		return preparation, err
	}

	stage := &cohereStage{}
	defer func() {
		if result != nil {
			stage.cleanup()
		}
	}()
	assets, profiles, err := stage.stage(root, resolved.Release)
	if err != nil {
		return preparation, err
	}
	if output, err := cluster("kubectl", "config", "set-context",
		"--current", "--namespace", resolved.Namespace); err != nil {
		return preparation, fmt.Errorf("bind application namespace: %w: %s", err, output)
	}
	if err := provisionCohereArchives(helmLLMCommandRunner(cluster), assets, profiles); err != nil {
		return preparation, err
	}
	if err := syncGcpSecrets(helmLLMCommandRunner(cluster), application); err != nil {
		return preparation, err
	}
	profileValues, err := externalProfileValues(profiles)
	if err != nil {
		return preparation, err
	}
	overrides, err := gcpCohereOverrides(resolved, images, assets, profileValues)
	if err != nil {
		return preparation, err
	}
	preparation = apprig.Preparation{
		ChartPath: stage.chart, ValuesPath: filepath.Join(stage.chart, "ci", gcpValuesFile),
		Overrides: overrides, OwnsNamespace: created,
	}
	if err := measureApplicationBudget(resolved, preparation, stage.archive); err != nil {
		return apprig.Preparation{}, err
	}
	preparation.Cleanup = func() error { stage.cleanup(); return nil }
	return preparation, nil
}

// gcpCohereOverrides is the decided values document for a GKE deploy:
// agent-core at the release tag through the ghcr.io repository, every
// third-party image and the applier's CLI donor through the Docker Hub
// repository (tags and the donor digest stay as the overlay pins them), the
// collector on the application's bucket with no endpoint override, the
// public host, and the out-of-release archives app:up names.
func gcpCohereOverrides(
	resolved apprig.Resolved, images gcpDeployImages, assets []externalUIAsset, profileValues map[string]any,
) (string, error) {
	runtime := map[string]any{
		"repository": images.Registries.Ghcr + "/" + gcpRuntimeImageSource,
		"tag":        images.RuntimeTag,
		"pullPolicy": "IfNotPresent",
	}
	values := map[string]any{
		"image": runtime,
		"collector": map[string]any{"storage": map[string]any{
			"backend":     "object",
			"bucketURL":   resolved.BucketURL,
			"prefix":      resolved.ObjectPrefix,
			"application": resolved.Application,
			"namespace":   resolved.Namespace,
		}},
		"applier": map[string]any{
			"image": runtime,
			"cliDonor": map[string]any{"image": map[string]any{
				"repository": images.Registries.Dockerhub + "/" + gcpCLIDonorSource,
			}},
		},
		"ingress": map[string]any{"host": images.Host},
		"exporter": map[string]any{"image": map[string]any{
			"repository": images.Registries.Ghcr + "/" + exporterGhcrPath,
			"tag":        images.ExporterTag,
			"pullPolicy": "IfNotPresent",
		}},
	}
	for _, image := range gcpPulledImages {
		node := values
		for _, key := range image.Path {
			next, ok := node[key].(map[string]any)
			if !ok {
				next = map[string]any{}
				node[key] = next
			}
			node = next
		}
		node["repository"] = images.Registries.Dockerhub + "/" + image.Source
	}
	for _, asset := range assets {
		component, ok := values[asset.Component].(map[string]any)
		if !ok {
			component = map[string]any{}
			values[asset.Component] = component
		}
		component["uiArchiveConfigMap"] = asset.ConfigMapName
		component["uiArchiveChecksum"] = asset.Checksum
	}
	mergeApplicationValues(values, profileValues)
	data, err := yaml.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("render GCP overrides: %w", err)
	}
	return string(data), nil
}

// requireExporterPublished refuses a deploy whose exporter tag ghcr.io does
// not carry, before anything changes in the cluster: a missing image would
// otherwise surface as an exporter pod stuck in ImagePullBackOff. The check
// asks the ghcr.io remote repository, so it uses the environment's own
// credentials and no GitHub token on the workstation.
func requireExporterPublished(status func(url string) (int, error), registries gcpRegistries, tag string) error {
	host, path, _ := strings.Cut(registries.Ghcr, "/")
	url := "https://" + host + "/v2/" + path + "/" + exporterGhcrPath + "/manifests/" + tag
	code, err := status(url)
	if err != nil {
		return fmt.Errorf("gcp:deploy: check exporter %s:%s: %w", exporterPublishedRepository, tag, err)
	}
	if code != http.StatusOK {
		return fmt.Errorf("gcp:deploy: %s:%s is not published (HTTP %d through %s); run mage exporter:publish",
			exporterPublishedRepository, tag, code, registries.Ghcr)
	}
	return nil
}

// gcpManifestStatus asks an Artifact Registry Docker endpoint for a
// manifest with the operator's gcloud access token and returns the status.
// A remote repository fetches the manifest from upstream on this request.
func gcpManifestStatus(url string) (int, error) {
	token, err := exec.Command("gcloud", "auth", "print-access-token").Output()
	if err != nil {
		return 0, fmt.Errorf("gcloud auth print-access-token: %w", err)
	}
	request, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	request.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, err
	}
	_ = response.Body.Close()
	return response.StatusCode, nil
}
