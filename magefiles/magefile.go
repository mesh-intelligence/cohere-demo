// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

// The chatbot-mesh application self-governs its specification corpus with the
// catalog specification critic, driven by the agent-core runtime it depends on as a platform.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mesh-intelligence/declarative-agents/applications/catalog/catalogroot"
	"gopkg.in/yaml.v3"
)

const (
	demoConfigFile   = "demo.yaml"
	agentCoreDirName = "agent-core"

	specificationCriticProfileRel = "agents/specification-critic/profile.yaml"
)

// demoConfig carries the optional, declarative overrides the chatbot-mesh
// magefiles read from demo.yaml. Every field is optional: an absent file or an
// unset field falls back to the monorepo default. Overriding a value means
// editing this declaration, never an environment variable (GH-1251).
type demoConfig struct {
	CatalogRoot       string `yaml:"catalog_root"`
	CoreRoot          string `yaml:"core_root"`
	HelmDist          string `yaml:"helm_dist"`
	SpecCriticProfile string `yaml:"spec_critic_profile"`

	// Collector ingress endpoints: where the observability targets bind the
	// collector and where the integration targets dial it.
	OTELGRPCPort         string `yaml:"otel_grpc_port"`
	CollectorControlPort string `yaml:"collector_control_port"`
	CollectorMonitorPort string `yaml:"collector_monitor_port"`
	CollectorQueryPort   string `yaml:"collector_query_port"`
	CollectorBindHost    string `yaml:"collector_bind_host"`

	// InferenceTimeout bounds one model call in the integration tracers, as a
	// Go duration string ("45s", "3m"). Empty means the shipped default.
	InferenceTimeout string `yaml:"inference_timeout"`

	// ChromaIngestTimeout bounds the complete canonical corpus-ingest agent run,
	// as a Go duration string ("15m", "20m"). It is distinct from the
	// per-request inference bound. Empty means the shipped default.
	ChromaIngestTimeout string `yaml:"chroma_ingest_timeout"`

	// ChromaIntegrationChatModel is the bounded evidence model passed to the
	// canonical corpus-ingest child. It does not change that profile's default.
	ChromaIntegrationChatModel string `yaml:"chroma_integration_chat_model"`

	// IntegrationOTLPEndpoint is where integration launches export live
	// telemetry when the target does not choose an endpoint itself. Empty
	// keeps those launches file-only.
	IntegrationOTLPEndpoint string `yaml:"integration_otlp_endpoint"`
}

// chromaIngestTimeoutDefault allows the two-document fixture's three legal
// 300-second model calls to complete, with five minutes left for startup,
// embedding, Chroma writes, and terminal verification.
const (
	chromaIngestTimeoutDefault        = 20 * time.Minute
	chromaIntegrationChatModelDefault = "qwen2.5:3b"
)

// loadDemoConfig reads demo.yaml from the application root. A missing file is the
// zero-configuration path and yields an empty config, not an error.
func loadDemoConfig(applicationRoot string) (demoConfig, error) {
	var config demoConfig
	data, err := os.ReadFile(filepath.Join(applicationRoot, demoConfigFile))
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}
		return config, fmt.Errorf("read %s: %w", demoConfigFile, err)
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("parse %s: %w", demoConfigFile, err)
	}
	return config, nil
}

// loadDemoConfigOrEmpty returns the parsed demo.yaml, or an empty config when the
// file is absent or unreadable. It suits the resolver helpers below, which fall
// back to the monorepo default rather than threading an error through every
// magefile call site.
func loadDemoConfigOrEmpty(applicationRoot string) demoConfig {
	config, _ := loadDemoConfig(applicationRoot)
	return config
}

// demoChromaIngestTimeout resolves the whole canonical ingest budget from the
// application-owned demo configuration.
func demoChromaIngestTimeout(applicationRoot string) time.Duration {
	return chromaIngestTimeoutFrom(loadDemoConfigOrEmpty(applicationRoot))
}

// chromaIngestTimeoutFrom parses chroma_ingest_timeout and falls back to the
// bounded shipped default when the field is absent or unusable.
func chromaIngestTimeoutFrom(config demoConfig) time.Duration {
	raw := strings.TrimSpace(config.ChromaIngestTimeout)
	if raw == "" {
		return chromaIngestTimeoutDefault
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return chromaIngestTimeoutDefault
	}
	return parsed
}

func demoChromaIntegrationChatModel(applicationRoot string) string {
	model := strings.TrimSpace(
		loadDemoConfigOrEmpty(applicationRoot).ChromaIntegrationChatModel)
	if model == "" {
		return chromaIntegrationChatModelDefault
	}
	return model
}

// configuredCoreRoot returns the demo.yaml core_root override, made absolute,
// or "" when the key is unset. The override exists for a checkout the Go
// toolchain cannot be pointed at; the shipped configuration leaves it unset.
func configuredCoreRoot(applicationRoot string) string {
	coreRoot := strings.TrimSpace(loadDemoConfigOrEmpty(applicationRoot).CoreRoot)
	switch {
	case coreRoot == "":
		return ""
	case filepath.IsAbs(coreRoot):
		return filepath.Clean(coreRoot)
	default:
		return filepath.Join(applicationRoot, coreRoot)
	}
}

// demoCoreRoot resolves the agent-core module directory: the demo.yaml
// core_root override when set, otherwise wherever the Go toolchain resolved the
// module — the sibling checkout under go.work, the read-only module cache copy
// under GOWORK=off. Resolving through the toolchain rather than by sibling
// convention is what keeps the magefiles building against the same agent-core
// the compiler does.
//
// The signature stays total because every caller already checks
// agentCoreAvailable and reports the path it could not use. When the toolchain
// cannot answer — no module cache and no workspace — the sibling path remains
// the last resort, preserving the behaviour a developer had before the pin. The
// audit gate and demo:up call resolveAgentCore instead, which reports why.
func demoCoreRoot(applicationRoot string) string {
	if coreRoot := configuredCoreRoot(applicationRoot); coreRoot != "" {
		return coreRoot
	}
	if resolution, err := resolveModule(applicationRoot, agentCoreModule); err == nil {
		return resolution.Dir
	}
	return siblingPath(applicationRoot, agentCoreDirName)
}

// demoHelmDist resolves the chart package output directory: demo.yaml helm_dist
// when set (relative values anchored at the application root), otherwise
// helm/dist under the application root.
func demoHelmDist(applicationRoot string) string {
	if helmDist := loadDemoConfigOrEmpty(applicationRoot).HelmDist; helmDist != "" {
		if filepath.IsAbs(helmDist) {
			return filepath.Clean(helmDist)
		}
		return filepath.Join(applicationRoot, helmDist)
	}
	return filepath.Join(applicationRoot, "helm", "dist")
}

// demoSpecCriticProfile resolves the specification-critic validator profile:
// demo.yaml spec_critic_profile when set (relative values anchored at the
// application root), otherwise the canonical profile under the catalog root.
func demoSpecCriticProfile(applicationRoot, catalogRoot string) string {
	if profile := loadDemoConfigOrEmpty(applicationRoot).SpecCriticProfile; profile != "" {
		if filepath.IsAbs(profile) {
			return filepath.Clean(profile)
		}
		return filepath.Join(applicationRoot, profile)
	}
	return filepath.Join(catalogRoot, filepath.FromSlash(specificationCriticProfileRel))
}

// Audit runs the specification critic over this application's specification corpus, so the application
// self-governs: load_corpus reads docs/SPECIFICATIONS.yaml, docs/specs, and
// agents; validate_specs runs the corpus consistency checks; a single error
// finding (a broken index path, touchpoint, or citation) fails the target. The
// outcome is read from the specification critic's report rather than its exit code: a failing
// corpus makes the specification critic reach a failed terminal, which exits 2 (agent-core
// srd018 R6.1/R6.2, GH-683), and agentRunCompleted accepts that as a completed
// run because the report -- not the exit status -- names which checks failed.
// Audit is the self-governance gate: it fails clearly when the agent-core
// runtime or the specification-critic validator profile is not reachable, rather than
// skipping, so a copied-out application without the platform tools reports an
// honest failure instead of a false green.
//
// The gate has five steps, each answering a question the one before it cannot:
// lint holds the formatting and the forbidigo rules, the specification critic
// validates the corpus, the boot smoke proves every profile starts, the
// evidence resolver proves each named test exists, and the evidence run
// proves the tests a suite claims as implemented actually pass.
//
// Lint runs here because it ran nowhere else. A target no gate invokes drifts
// until its output is a standing count nobody can separate their own findings
// from, which is what happened before GH-162: a gofmt drift and eleven
// forbidigo findings accumulated across releases while every change reported
// green.
func Audit() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := Lint(); err != nil {
		return err
	}
	catalogRoot, err := resolveCatalogRoot("chatbot-mesh audit", root)
	if err != nil {
		return err
	}
	if _, err := resolveChatbotComposition(root, catalogRoot); err != nil {
		return fmt.Errorf("audit application composition: %w", err)
	}
	coreRoot, specificationCriticProfile, err := resolveAuditTools(root, catalogRoot)
	if err != nil {
		return err
	}
	binary, cleanupAgent, err := buildAgent(coreRoot)
	if err != nil {
		return err
	}
	defer cleanupAgent()
	if err := runSpecificationCritic(
		corpusCriticStep, binary, specificationCriticProfile, root, coreRoot); err != nil {
		return err
	}
	// The specification critic validates the specification corpus, not whether an agent can
	// start, so preflight every mesh profile through the runtime's own load path.
	profiles, err := meshProfiles(root)
	if err != nil {
		return err
	}
	corpusRuntime, cleanupCorpusRuntime, err := stageCorpusIngestRuntime(root)
	if err != nil {
		return err
	}
	defer cleanupCorpusRuntime()
	sourceCorpusProfile := filepath.Join(root, filepath.FromSlash(chromaIngestProfile))
	applierChart, cleanupApplierRuntime, err := stagePackageChart(
		filepath.Join(root, "helm"), root, catalogRoot)
	if err != nil {
		return err
	}
	defer cleanupApplierRuntime()
	// A wrapper around a catalog agent names its canonical closure through the
	// staged layout, so the smoke preflights the staged profile. Both wrappers
	// come from the one chart staged above.
	stagedWrappers := map[string]string{
		filepath.Join(root, "agents", "applier", "profile.yaml"): filepath.Join(
			applierChart, "profiles", "applications", "chatbot-mesh", "applier", "profile.yaml"),
		filepath.Join(root, "agents", "observer", "profile.yaml"): filepath.Join(
			applierChart, "profiles", "applications", "chatbot-mesh", "observer", "profile.yaml"),
	}
	for index, profile := range profiles {
		if filepath.Clean(profile) == sourceCorpusProfile {
			profiles[index] = filepath.Join(
				corpusRuntime, filepath.FromSlash(chromaIngestProfile))
		}
		if staged, ok := stagedWrappers[filepath.Clean(profile)]; ok {
			profiles[index] = staged
		}
	}
	if err := bootSmokeProfiles(defaultSmokeRun, binary, coreRoot, profiles); err != nil {
		return err
	}
	// A Cohere tier rebinds the corpus child's embedder root (srd058 R2.2), and
	// the default-bound smoke above never loads that library's fragment.
	cohereCorpusArgs := append([]string{"--validate-config", "--profile",
		filepath.Join(corpusRuntime, filepath.FromSlash(chromaIngestProfile)), "--core-root", coreRoot},
		demoSeedEmbeddingProvider{cohere: true}.libraryArgs(coreRoot)...)
	if out, err := defaultSmokeRun(binary, cohereCorpusArgs...); err != nil {
		return fmt.Errorf("mesh boot smoke failed for corpus-ingest bound to the Cohere library: %w\n%s",
			err, strings.TrimSpace(string(out)))
	}
	evidenceProfile := filepath.Join(filepath.Dir(specificationCriticProfile), "audit-profile.yaml")
	if _, err := os.Stat(evidenceProfile); err != nil {
		return fmt.Errorf("audit: specification-critic test-evidence profile not found at %s: %w", evidenceProfile, err)
	}
	if err := runSpecificationCritic(
		evidenceResolverStep, binary, evidenceProfile, root, coreRoot); err != nil {
		return err
	}
	// The chatbot SPA ships as a committed bundle the chart stages, so the gate
	// also proves that bundle reproduces from the sources beside it. Unlike the
	// agent-core runtime and the validator profile above, node is optional: this
	// step skips with a stated reason rather than failing a machine that cannot
	// answer the question.
	return verifyChatbotUI(root)
}

// resolveAuditTools locates the agent-core runtime checkout and the specification-critic
// validator profile the self-governance audit requires. Both are mandatory: the
// gate cannot validate the corpus without the runtime that executes the specification critic
// or the validator profile itself, so a missing tool is a clear failure, not a
// skip. Skip behavior is reserved for explicitly optional integration targets.
func resolveAuditTools(root, catalogRoot string) (coreRoot, specificationCriticProfile string, err error) {
	coreRoot = demoCoreRoot(root)
	if !agentCoreAvailable(coreRoot) {
		return "", "", fmt.Errorf("audit: agent-core checkout not found at %s (set core_root in %s); the self-governance gate requires the agent-core runtime", coreRoot, demoConfigFile)
	}
	specificationCriticProfile = demoSpecCriticProfile(root, catalogRoot)
	if _, statErr := os.Stat(specificationCriticProfile); statErr != nil {
		return "", "", fmt.Errorf("audit: specification-critic validator profile not found at %s (set spec_critic_profile in %s); the self-governance gate requires its validator", specificationCriticProfile, demoConfigFile)
	}
	return coreRoot, specificationCriticProfile, nil
}

// resolveCatalogRoot resolves the catalog source root: the declared demo.yaml
// catalog_root when set, otherwise wherever the Go toolchain resolved the
// catalog module, otherwise repository discovery. Asking the toolchain before
// falling back to discovery is what lets the application build against the
// pinned catalog rather than whichever sibling checkout happens to be nearby.
// It never changes process CWD; child commands continue to run from the
// chatbot-mesh root.
func resolveCatalogRoot(owner, startupCWD string) (string, error) {
	startupCWD, err := filepath.Abs(filepath.Clean(startupCWD))
	if err != nil {
		return "", fmt.Errorf("%s: resolve startup working directory: %w", owner, err)
	}
	declared := strings.TrimSpace(loadDemoConfigOrEmpty(startupCWD).CatalogRoot)
	if declared == "" {
		if resolution, resolveErr := resolveModule(startupCWD, catalogModule); resolveErr == nil {
			declared = resolution.Dir
		}
	}
	resolution, err := catalogroot.Resolve(
		owner,
		startupCWD,
		declared,
		catalogroot.DiscoveryCandidates(startupCWD)...,
	)
	if err != nil {
		return "", err
	}
	return resolution.Path, nil
}

// auditStep names one of the gate's critic invocations. Both run the same
// binary over the same corpus and fail the same way, so a failure that does not
// say which step produced it sends the reader to the wrong evidence: an
// evidence-resolver failure reported as a corpus finding points at docs/specs,
// where nothing is wrong (GH-115).
type auditStep struct {
	name    string
	failure string
}

var (
	corpusCriticStep = auditStep{
		name:    "specification critic",
		failure: "the specification critic found errors in the application corpus at %s",
	}
	evidenceResolverStep = auditStep{
		name: "test-evidence resolver",
		failure: "the test-evidence resolver found errors: a test suite names go_test evidence" +
			" that does not resolve to a package or a passing test. The corpus at %s is not" +
			" what it reported on",
	}
)

// specificationCriticCommand builds one critic invocation. It is separate from
// running it so the environment and arguments the gate depends on can be
// asserted without an agent binary.
func specificationCriticCommand(binary, specificationCriticProfile, root, coreRoot string) *exec.Cmd {
	cmd := exec.Command(binary,
		"--profile", specificationCriticProfile,
		"--directory", root,
		"--core-root", coreRoot,
	)
	cmd.Dir = root
	// The evidence resolver shells out to `go list -m` to read the module under
	// test and expects one module path. A Go workspace makes that command print
	// one line per workspace module, and the resolver then matches no declared
	// package at all. Turning the workspace off also makes the gate evaluate
	// what go.mod pins rather than whichever checkout go.work points at, which
	// is the state a governance gate should be reporting on (GH-115). Upstream
	// v0.20260827.0 (pre-move upstream declarative-agents#1864) now runs the
	// critic's own `go_module` tool under GOWORK=off, but this child-level
	// setting stays: it also covers the package and test inventory steps, which
	// that change did not touch.
	cmd.Env = append(os.Environ(), "GOWORK=off")
	return cmd
}

func runSpecificationCritic(
	step auditStep, binary, specificationCriticProfile, root, coreRoot string,
) error {
	cmd := specificationCriticCommand(binary, specificationCriticProfile, root, coreRoot)
	// os/exec drains stdout and stderr concurrently. Keep separate buffers:
	// bytes.Buffer is not safe for concurrent writes, and sharing one here can
	// lose the terminal marker while the same marker is still forwarded live.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &stdout)
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	// A corpus with findings drives the specification critic to a failure terminal, which
	// exits non-zero; that is a completed run reporting failure, not a broken
	// invocation (srd018-cli-flag-contract R6). Only a run the binary could
	// not complete is an error here; the outcome itself comes from the report.
	if runErr := cmd.Run(); !agentRunCompleted(runErr) {
		return fmt.Errorf("%s run failed: %w", step.name, runErr)
	}
	ok, err := specificationCriticSucceeded(combinedAgentReport(stdout.String(), stderr.String()))
	switch {
	case err != nil:
		return fmt.Errorf("audit: %s: %w; see the report above", step.name, err)
	case !ok:
		return fmt.Errorf("audit: "+step.failure, filepath.Join(root, "docs", "specs"))
	default:
		fmt.Printf("audit: %s (%s) completed with no errors\n",
			step.name, filepath.Base(specificationCriticProfile))
		return nil
	}
}

func combinedAgentReport(stdout, stderr string) string {
	return stdout + "\n" + stderr
}

// specificationCriticSucceeded reads a clean/failing outcome from a specification-critic report. The
// outcome is taken from the terminal state line rather than the exit code:
// the exit code now distinguishes a failed terminal from a failed invocation
// (srd018 R6), but the report is what names which checks failed. A report with
// neither terminal marker is an indeterminate run and returns an error.
func specificationCriticSucceeded(report string) (bool, error) {
	switch {
	case strings.Contains(report, "terminal state: failed") || strings.Contains(report, "status=failed"):
		return false, nil
	case strings.Contains(report, "terminal state: succeeded"):
		return true, nil
	default:
		return false, fmt.Errorf("the specification critic did not reach a terminal state")
	}
}

// buildAgent builds the production agent binary from the agent-core checkout, the
// same binary the runtime image ships and the integration tests drive. Each call
// builds into a directory of its own, because concurrent runs on one host (and
// other repositories sharing this code) overwrote and deleted a shared path
// under each other. The caller defers cleanup once every process it started from
// the binary has stopped.
func buildAgent(coreRoot string) (binary string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "chatbot-mesh-agent-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create agent build directory: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	binary, err = buildAgentInto(coreRoot, dir)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	return binary, cleanup, nil
}

// buildAgentInto builds the agent binary into dir, for a process that must
// outlive the mage invocation and so cannot take a directory removed on return.
func buildAgentInto(coreRoot, dir string) (string, error) {
	binary := filepath.Join(dir, "agent")
	cmd := exec.Command("go", "build", "-tags", "production", "-o", binary, "./cmd/agent")
	cmd.Dir = coreRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("building agent binary from %s...\n", coreRoot)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build agent: %w", err)
	}
	return binary, nil
}

// agentCoreAvailable reports whether coreRoot looks like an agent-core module
// checkout buildAgent can compile from.
func agentCoreAvailable(coreRoot string) bool {
	info, err := os.Stat(filepath.Join(coreRoot, "go.mod"))
	return err == nil && !info.IsDir()
}

// siblingPath resolves rel against the repository root, two levels above the
// applications/chatbot-mesh owner root.
func siblingPath(applicationRoot, rel string) string {
	return filepath.Clean(filepath.Join(applicationRoot, "..", "..", rel))
}

// Collector ingress defaults. OTLP gRPC and the query surface are shared with
// the agent-architecture demo's local collector, so the two cannot run at once
// unless one is repointed in demo.yaml.
const (
	otelGRPCPortDefault         = "4317"
	collectorControlPortDefault = "18191"
	collectorMonitorPortDefault = "18192"
	collectorQueryPortDefault   = "18193"
	collectorBindHostDefault    = "0.0.0.0"
)

// observabilitySettings names the collector ingress endpoints the magefiles
// use: the ports and bind host the observability targets hand the collector
// child process, and the loopback ports the integration targets dial.
type observabilitySettings struct {
	OTELGRPCPort string
	ControlPort  string
	MonitorPort  string
	QueryPort    string
	BindHost     string
}

// observabilitySettingsFrom resolves the collector ingress endpoints from a
// demo.yaml config, filling unset fields with the shipped defaults.
func observabilitySettingsFrom(config demoConfig) observabilitySettings {
	return observabilitySettings{
		OTELGRPCPort: firstNonEmpty(config.OTELGRPCPort, otelGRPCPortDefault),
		ControlPort:  firstNonEmpty(config.CollectorControlPort, collectorControlPortDefault),
		MonitorPort:  firstNonEmpty(config.CollectorMonitorPort, collectorMonitorPortDefault),
		QueryPort:    firstNonEmpty(config.CollectorQueryPort, collectorQueryPortDefault),
		BindHost:     firstNonEmpty(config.CollectorBindHost, collectorBindHostDefault),
	}
}

// demoObservability resolves the collector ingress endpoints from demo.yaml in
// the mage invocation directory, the application root every target runs from.
func demoObservability() observabilitySettings {
	root, err := os.Getwd()
	if err != nil {
		return observabilitySettingsFrom(demoConfig{})
	}
	return observabilitySettingsFrom(loadDemoConfigOrEmpty(root))
}

// demoIntegrationOTLPEndpoint resolves the live-export endpoint for
// integration launches whose target does not choose one: demo.yaml
// integration_otlp_endpoint, or empty, which keeps the launch file-only.
func demoIntegrationOTLPEndpoint() string {
	root, err := os.Getwd()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(loadDemoConfigOrEmpty(root).IntegrationOTLPEndpoint)
}

// exitMachineFailed is the agent's exit code for a run whose machine reached a
// failure terminal, as distinct from 1, which means the binary could not
// complete a run at all (srd018-cli-flag-contract R6).
const exitMachineFailed = 2

// agentRunCompleted reports whether an agent invocation completed a run,
// including one whose machine reached a failure terminal.
func agentRunCompleted(runErr error) bool {
	if runErr == nil {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode() == exitMachineFailed
	}
	return false
}
