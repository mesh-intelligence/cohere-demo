// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/magefile/mage/mg"
	"github.com/mesh-intelligence/declarative-agents/magefiles/apprig"
	"github.com/mesh-intelligence/declarative-agents/magefiles/kindrig"
	"gopkg.in/yaml.v3"
)

// cohere-demo's identity on the shared da-platform (GH-788): one release in
// its own app- namespace, reached through the platform ingress on hosts of its
// own, so it runs beside upstream chatbot-mesh without taking a host port or a
// host name from it.
const (
	cohereApplicationRelease      = "cohere-demo"
	cohereApplicationNamespace    = "app-cohere-demo"
	cohereApplicationHost         = "cohere-demo.localhost"
	cohereObserverHost            = "observer.cohere-demo.localhost"
	cohereCollectorHost           = "telemetry.cohere-demo.localhost"
	cohereIngressClass            = "traefik"
	cohereApplicationPurgeProfile = "agents/application-purge/profile.yaml"
)

// App is cohere-demo's lifecycle on the shared da-platform. Each verb is a
// thin call to upstream's apprig runner, which deploys and undeploys through
// the catalog applier; this module supplies configuration and the
// Cohere-specific preparation only.
type App mg.Namespace

// Up deploys cohere-demo into app-cohere-demo on a running da-platform. It
// never creates the platform; run mage platform:up first.
func (App) Up() error {
	runner, err := cohereApplicationRunner()
	if err != nil {
		return err
	}
	if err := requirePlatform(runner.Binding.Cluster, platformExists); err != nil {
		return err
	}
	if err := refuseTierDowngrade(runner); err != nil {
		return err
	}
	if _, err := reportAgentCoreProvenance(runner.Binding.ApplicationRoot); err != nil {
		return err
	}
	// The release reuses the host Ollama and its cached models (GH-1321), so a
	// missing model is a deploy-time refusal rather than a failing turn.
	models, err := demoRequiredModels(applicationChartDir(runner.Binding.ApplicationRoot))
	if err != nil {
		return err
	}
	if reason := chromaOllamaSkipReasonForModels(models); reason != "" {
		return fmt.Errorf("app:up requires host Ollama with the chart models installed: %s", reason)
	}
	if cohereCredential() == "" {
		fmt.Println("app: no Cohere credential resolved; deploying the Ollama tier")
	}
	return runner.Up()
}

// Status prints the read-only health report as JSON.
func (App) Status() error {
	runner, err := cohereApplicationRunner()
	if err != nil {
		return err
	}
	report, err := runner.Status()
	if err != nil {
		return err
	}
	return printApplicationStatus(report)
}

// Down removes the release and its namespace, keeping the telemetry bucket and
// leaving the platform and every other application running.
func (App) Down() error {
	runner, err := cohereApplicationRunner()
	if err != nil {
		return err
	}
	if err := requirePlatform(runner.Binding.Cluster, platformExists); err != nil {
		return err
	}
	report, err := runner.Down()
	if err != nil {
		return err
	}
	return printApplicationStatus(report)
}

// Diagnose captures the platform's canonical evidence for the release.
func (App) Diagnose() error {
	runner, err := cohereApplicationRunner()
	if err != nil {
		return err
	}
	return runner.Diagnose()
}

// Purge deletes the application's telemetry objects through the catalog's
// application-purge agent; the confirmation must be exactly purge:cohere-demo.
func (App) Purge(confirmation string) error {
	runner, err := cohereApplicationRunner()
	if err != nil {
		return err
	}
	return runner.PurgeData(confirmation)
}

// cohereApplicationRunner binds this repository to the platform.
func cohereApplicationRunner() (apprig.Runner, error) {
	root, err := cohereApplicationRoot()
	if err != nil {
		return apprig.Runner{}, err
	}
	catalogRoot, err := resolveCatalogRoot("cohere-demo application", root)
	if err != nil {
		return apprig.Runner{}, err
	}
	coreRoot := demoCoreRoot(root)
	runner := apprig.Runner{
		ManifestPath: filepath.Join(root, "agents", "application.yaml"),
		Binding:      cohereApplicationBinding(root, platformCluster()),
		CatalogRoot:  catalogRoot,
		CoreRoot:     coreRoot,
	}
	runner.Prepare = func(resolved apprig.Resolved) (apprig.Preparation, error) {
		return prepareCohereApplication(runner, root, resolved)
	}
	// The shared rollout check first; then the three browser surfaces answer
	// through the platform ingress.
	runner.Verify = func(resolved apprig.Resolved) error {
		if err := runner.DefaultVerify(resolved); err != nil {
			return err
		}
		for _, endpoint := range cohereApplicationEndpoints() {
			if err := waitHTTPStatus(endpoint, http.StatusOK, 30*time.Second); err != nil {
				return err
			}
		}
		return nil
	}
	runner.Purge = func(resolved apprig.Resolved, confirmation string) error {
		agent, err := apprig.CanonicalAgent(coreRoot, catalogRoot, cohereApplicationPurgeProfile)
		if err != nil {
			return err
		}
		return apprig.RunApplicationPurge(resolved, confirmation,
			apprig.PurgeBinding{
				Endpoint:       kindrig.FakeGCSHostEndpoint,
				AuditDirectory: filepath.Join(root, "build", "purge"),
			},
			apprig.PurgeAgent{
				Binary: agent.Binary, Cleanup: agent.Cleanup,
				CoreRoot: agent.CoreRoot, Profile: agent.Profile,
			})
	}
	return runner, nil
}

func cohereApplicationBinding(root, platform string) apprig.PlatformBinding {
	return apprig.PlatformBinding{
		Cluster:           platform,
		NamespacePrefix:   "app-",
		IngressHostSuffix: "localhost",
		BucketURLTemplate: "gs://%s-telemetry",
		ChartName:         "chatbot-mesh",
		ChartPath:         filepath.Join(root, "helm"),
		ValuesPath:        filepath.Join(root, "helm", "ci", chatbotDemoValuesFile),
		Timeout:           helmLLMInstallTimeout.String(),
		ApplicationRoot:   root,
	}
}

// cohereApplicationEndpoints are the browser surfaces app:up verifies.
func cohereApplicationEndpoints() []string {
	return []string{
		"http://" + cohereApplicationHost + "/",
		"http://" + cohereObserverHost + "/",
		"http://" + cohereCollectorHost + "/query/traces?page_size=1",
	}
}

// cohereStage is what one app:up staged and must release: the chart it
// deploys and the out-of-release archives it provisioned.
type cohereStage struct {
	chart    string
	archive  string
	cleanups []func()
}

func (s *cohereStage) cleanup() {
	for index := len(s.cleanups) - 1; index >= 0; index-- {
		s.cleanups[index]()
	}
	s.cleanups = nil
}

// prepareCohereApplication wraps the default Prepare: apprig creates the
// bucket, leases and loads the runtime image, and creates the namespace; the
// Extras hook then stages the chart, provisions the out-of-release archives,
// the exporter and dependency images, and the credential Secrets inside the
// namespace, and returns the values that select them. The release budget is
// gated on the final overrides before anything reaches the applier.
func prepareCohereApplication(
	runner apprig.Runner, root string, resolved apprig.Resolved,
) (apprig.Preparation, error) {
	stage := &cohereStage{}
	preparation, err := runner.DefaultPrepare(resolved, apprig.PrepareHooks{
		Extras: func(state apprig.PrepareState) (map[string]any, error) {
			return stage.extras(root, runner.Binding.Cluster, state)
		},
		PackageChart: func(apprig.Resolved) (string, error) { return stage.chart, nil },
	})
	if err != nil {
		stage.cleanup()
		return preparation, err
	}
	if err := measureApplicationBudget(resolved, preparation, stage.archive); err != nil {
		stage.cleanup()
		if preparation.Cleanup != nil {
			err = errors.Join(err, preparation.Cleanup())
		}
		return apprig.Preparation{}, err
	}
	release := preparation.Cleanup
	preparation.Cleanup = func() error {
		stage.cleanup()
		if release != nil {
			return release()
		}
		return nil
	}
	return preparation, nil
}

// extras runs inside the default Prepare once the namespace exists and the
// runtime image is loaded.
func (s *cohereStage) extras(root, platform string, state apprig.PrepareState) (map[string]any, error) {
	chartDir := applicationChartDir(root)
	assets, profiles, err := s.stage(root, state.Resolved.Release)
	if err != nil {
		return nil, err
	}
	staged := s.chart

	_, tag := apprig.SplitImage(state.Image)
	exporterImage := exporterImageRepository + ":" + tag
	if err := buildExporterImage(root, state.Image, exporterImage); err != nil {
		return nil, err
	}
	dependencies, err := smokeDependencyImages(chartDir)
	if err != nil {
		return nil, err
	}
	if err := pullIntegrationDependencyImages("app:up", dependencies, runHelmSmokeCommand); err != nil {
		return nil, err
	}

	commands, cleanupCommands, err := kindrig.ClusterCommands(kindrig.CaptureRun, platform)
	if err != nil {
		return nil, err
	}
	defer cleanupCommands()
	if output, err := commands.Run("kubectl", "config", "set-context",
		"--current", "--namespace", state.Resolved.Namespace); err != nil {
		return nil, fmt.Errorf("bind application namespace: %w: %s", err, output)
	}
	if err := loadKindImageWithCommands(commands, platform, exporterImage); err != nil {
		return nil, err
	}
	for _, image := range dependencies {
		if err := loadSmokeDependencyImageWithCommands(commands, platform, image); err != nil {
			return nil, err
		}
	}
	if err := provisionCohereArchives(commands.Run, assets, profiles); err != nil {
		return nil, err
	}
	cohere := cohereCredential()
	if err := ensureApplicationSecret(commands.Run, demoCohereSecret, demoCohereSecretKey, cohere); err != nil {
		return nil, err
	}
	serpapi := serpapiCredential()
	if err := ensureApplicationSecret(commands.Run, demoSerpapiSecret, demoSerpapiSecretKey, serpapi); err != nil {
		return nil, err
	}
	profileValues, err := externalProfileValues(profiles)
	if err != nil {
		return nil, err
	}
	units, err := chartRagUnits(filepath.Join(staged, "ci", chatbotDemoValuesFile))
	if err != nil {
		return nil, err
	}
	return cohereApplicationExtras(cohereExtrasInput{
		ExporterImage: exporterImage,
		Assets:        assets,
		ProfileValues: profileValues,
		CohereEnabled: cohere != "",
		EmbedModel:    demoCohereEmbedModel(staged),
		RagUnits:      units,
		SerpapiEngine: os.Getenv("DEMO_OSINT_ENGINE") == "serpapi" && serpapi != "", //nolint:forbidigo // the per-deploy opt-in lever for the metered engine (GH-457).
	}), nil
}

// stage copies the chart and moves out of the release what would project its
// Secret past the Kubernetes limit (GH-1475, GH-785): the shipped UIs, the
// verbatim profiles, and the applier archive the budget measures. app:up and
// gcp:deploy stage the same release.
func (s *cohereStage) stage(root, release string) ([]externalUIAsset, profilesExternalizationResult, error) {
	var profiles profilesExternalizationResult
	staged, cleanupStaged, err := stageSmokeChart(applicationChartDir(root), root)
	if err != nil {
		return nil, profiles, err
	}
	s.chart = staged
	s.cleanups = append(s.cleanups, cleanupStaged)
	assets, cleanupAssets, err := externalizeUIAssets(staged, release)
	if err != nil {
		return nil, profiles, err
	}
	s.cleanups = append(s.cleanups, cleanupAssets)
	profiles, cleanupProfiles, err := externalizeProfiles(staged, release)
	if err != nil {
		return nil, profiles, err
	}
	s.cleanups = append(s.cleanups, cleanupProfiles)
	archive, cleanupArchive, err := packageApplierChart(staged)
	if err != nil {
		return nil, profiles, err
	}
	s.archive = archive
	s.cleanups = append(s.cleanups, cleanupArchive)
	return assets, profiles, nil
}

// provisionCohereArchives creates the out-of-release ConfigMaps in the
// namespace run's kubeconfig is bound to. The credential Secrets are each
// path's own: app:up writes them from the workstation, gcp:deploy syncs them
// from Secret Manager.
func provisionCohereArchives(
	run helmLLMCommandRunner, assets []externalUIAsset, profiles profilesExternalizationResult,
) error {
	if err := provisionExternalUIAssets(run, assets); err != nil {
		return err
	}
	return provisionExternalProfiles(run, profiles)
}

// cohereExtrasInput is what the values overrides depend on.
type cohereExtrasInput struct {
	ExporterImage string
	Assets        []externalUIAsset
	ProfileValues map[string]any
	CohereEnabled bool
	EmbedModel    string
	RagUnits      []map[string]any
	SerpapiEngine bool
}

// cohereApplicationExtras are the values layered over apprig's image and
// collector-storage overrides: every image pinned to the kind-loaded copy, the
// three hosts on the platform ingress, the exporter, the out-of-release
// archives, and the Cohere tier.
//
// Enabling the provider repoints every RAG unit's declared embedding identity
// to the Cohere model, because srd002 R3.3 excludes any source whose reported
// identity differs from the query vector's (rel10.3-uc001 F6). The credential
// never travels through values; the chart takes the Secret's name.
func cohereApplicationExtras(in cohereExtrasInput) map[string]any {
	exporterRepository, exporterTag := apprig.SplitImage(in.ExporterImage)
	loaded := func() map[string]any { return map[string]any{"pullPolicy": "Never"} }
	values := map[string]any{
		"image":    loaded(),
		"chroma":   map[string]any{"image": loaded()},
		"dolt":     map[string]any{"image": loaded()},
		"observer": map[string]any{"proxy": map[string]any{"image": loaded()}},
		"ingress": map[string]any{
			"enabled": true, "className": cohereIngressClass, "host": cohereApplicationHost,
			"observerHost": cohereObserverHost, "collectorHost": cohereCollectorHost,
		},
		"collector": map[string]any{"externalOTLPEndpoint": ""},
		"exporter": map[string]any{
			"enabled": true,
			"image": map[string]any{
				"repository": exporterRepository, "tag": exporterTag, "pullPolicy": "Never",
			},
		},
	}
	for _, asset := range in.Assets {
		component, ok := values[asset.Component].(map[string]any)
		if !ok {
			component = map[string]any{}
			values[asset.Component] = component
		}
		component["uiArchiveConfigMap"] = asset.ConfigMapName
		component["uiArchiveChecksum"] = asset.Checksum
	}
	mergeApplicationValues(values, in.ProfileValues)
	if in.CohereEnabled {
		values["cohere"] = map[string]any{"enabled": true, "existingSecret": demoCohereSecret}
		if in.EmbedModel != "" && len(in.RagUnits) > 0 {
			units := make([]any, 0, len(in.RagUnits))
			for _, unit := range in.RagUnits {
				repointed := make(map[string]any, len(unit))
				for key, value := range unit {
					repointed[key] = value
				}
				repointed["embeddingModel"] = in.EmbedModel
				units = append(units, repointed)
			}
			values["ragUnits"] = units
		}
	}
	if in.SerpapiEngine {
		values["osint"] = map[string]any{
			"engine": "serpapi", "serpapi": map[string]any{"existingSecret": demoSerpapiSecret},
		}
	}
	return values
}

// mergeApplicationValues merges extra into base, mapping by mapping.
func mergeApplicationValues(base, extra map[string]any) {
	for key, value := range extra {
		nested, isMap := value.(map[string]any)
		existing, baseIsMap := base[key].(map[string]any)
		if isMap && baseIsMap {
			mergeApplicationValues(existing, nested)
			continue
		}
		base[key] = value
	}
}

// externalProfileValues reads the values file the profiles externalization
// wrote, which names each group's out-of-release ConfigMap.
func externalProfileValues(result profilesExternalizationResult) (map[string]any, error) {
	if result.ValuesFile == "" {
		return nil, nil
	}
	data, err := os.ReadFile(result.ValuesFile)
	if err != nil {
		return nil, err
	}
	var values map[string]any
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("read external profile values: %w", err)
	}
	return values, nil
}

// chartRagUnits reads the RAG units a values file declares, whole, so the
// Cohere tier can repoint their embedding identity without dropping a field.
func chartRagUnits(valuesPath string) ([]map[string]any, error) {
	var values struct {
		RagUnits []map[string]any `yaml:"ragUnits"`
	}
	if err := readIntegrationYAML(valuesPath, "application values", &values); err != nil {
		return nil, err
	}
	return values.RagUnits, nil
}

// ensureApplicationSecret replaces a credential Secret in the bound
// namespace. The value reaches kubectl through a file only this user can read,
// never on a command line (srd009 R1.3). An empty credential leaves the Secret
// absent, and the chart then leaves the provider off.
func ensureApplicationSecret(run helmLLMCommandRunner, name, key, value string) error {
	if value == "" {
		return nil
	}
	file, err := os.CreateTemp("", "cohere-demo-secret-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.WriteString(value); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if output, err := run("kubectl", "delete", "secret", name, "--ignore-not-found"); err != nil {
		return fmt.Errorf("clear Secret %s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	if output, err := run("kubectl", "create", "secret", "generic", name,
		"--from-file="+key+"="+file.Name()); err != nil {
		return fmt.Errorf("create Secret %s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	fmt.Printf("app: credential staged in Secret %q\n", name)
	return nil
}

// measureApplicationBudget gates the projected release Secret on the final
// overrides before the applier sees them (GH-1475).
func measureApplicationBudget(resolved apprig.Resolved, preparation apprig.Preparation, archive string) error {
	overrides, err := os.CreateTemp("", "cohere-demo-overrides-*.yaml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(overrides.Name()) }()
	if _, err := overrides.WriteString(preparation.Overrides); err != nil {
		_ = overrides.Close()
		return err
	}
	if err := overrides.Close(); err != nil {
		return err
	}
	measured, err := measureHelmReleaseBudget(resolved.Release, preparation.ChartPath, archive,
		[]string{"--namespace", resolved.Namespace,
			"--values", preparation.ValuesPath, "-f", overrides.Name()})
	if err != nil {
		return err
	}
	fmt.Printf("app:up release budget PASS - %s\n", measured.String())
	return nil
}

// refuseTierDowngrade keeps the GH-318 guard: a running Cohere-tier release
// and a run that resolved no credential would silently flip the mesh to the
// Ollama tier against Cohere-seeded collections, so every turn degrades while
// looking healthy.
func refuseTierDowngrade(runner apprig.Runner) error {
	if cohereCredential() != "" {
		return nil
	}
	resolved, err := applicationResolved(runner)
	if err != nil {
		return err
	}
	if !applicationCohereEnabled(runner.Binding.Cluster, resolved) {
		return nil
	}
	return fmt.Errorf("app:up resolved no Cohere credential but the running release" +
		" has cohere.enabled=true; deploying would silently downgrade the mesh to the" +
		" Ollama tier against Cohere-seeded collections (GH-318). Export COHERE_API_KEY" +
		" (or run from a checkout that can read .secrets/keys.json), or run mage app:down" +
		" first if the downgrade is intended")
}

// applicationResolved resolves the release coordinates from the manifest and
// binding, as the runner does.
func applicationResolved(runner apprig.Runner) (apprig.Resolved, error) {
	manifest, err := apprig.LoadManifest(runner.ManifestPath)
	if err != nil {
		return apprig.Resolved{}, err
	}
	return apprig.Resolve(manifest, runner.Binding)
}

// applicationCohereEnabled reports whether the running release enables the
// Cohere provider. An absent or unreadable release reports false.
func applicationCohereEnabled(platform string, resolved apprig.Resolved) bool {
	commands, cleanup, err := kindrig.ClusterCommands(kindrig.CaptureRun, platform)
	if err != nil {
		return false
	}
	defer cleanup()
	output, err := commands.Run("helm", "get", "values", resolved.Release,
		"--namespace", resolved.Namespace, "--all", "--output", "json")
	if err != nil {
		return false
	}
	var values struct {
		Cohere struct {
			Enabled bool `json:"enabled"`
		} `json:"cohere"`
	}
	return json.Unmarshal(output, &values) == nil && values.Cohere.Enabled
}

func platformExists(platform string) bool {
	return kindrig.Exists(kindrig.DefaultRun, platform)
}

// requirePlatform refuses a lifecycle verb when da-platform is not running;
// bringing it up is platform:up's job, not app:up's.
func requirePlatform(platform string, exists func(string) bool) error {
	if exists(platform) {
		return nil
	}
	return fmt.Errorf("%s is not running; run mage platform:up first", platform)
}

func cohereApplicationRoot() (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if filepath.Base(root) == "magefiles" {
		root = filepath.Dir(root)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "application.yaml")); err != nil {
		return "", fmt.Errorf("cohere-demo application root %s: %w", root, err)
	}
	return root, nil
}

func printApplicationStatus(report apprig.StatusReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
