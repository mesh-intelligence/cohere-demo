// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/magefile/mage/mg"
	"gopkg.in/yaml.v3"
)

// decodeSpanStream reads an OTel span log into values of T.
//
// It decodes the file as a stream of JSON values rather than splitting it into
// lines: json.Decoder consumes one value at a time whatever whitespace lies
// between them, so it reads the exporter's output whether spans are written one
// per line or indented across several. A line-splitting reader that skips the
// lines it cannot parse turns an indented log into an empty slice and reports
// no error, and an empty span list is indistinguishable from a turn that never
// ran the word being asserted (GH-85).
//
// Zero spans is an error rather than an empty slice for that same reason. An
// assertion of the form "the trace must contain X" fails correctly on an empty
// slice, but one scanning for a violation finds none and passes, and the
// difference is invisible at the call site.
//
// A malformed value ends the read with an error rather than truncating
// silently. These traces are read after the agent exits, so a value that does
// not decode means a corrupt log, not one still being written.
func decodeSpanStream[T any](tracePath string) ([]T, error) {
	file, err := os.Open(tracePath)
	if err != nil {
		return nil, fmt.Errorf("read trace %s: %w", tracePath, err)
	}
	defer func() { _ = file.Close() }()
	var spans []T
	decoder := json.NewDecoder(file)
	for {
		var span T
		if decodeErr := decoder.Decode(&span); decodeErr != nil {
			if errors.Is(decodeErr, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode trace %s after %d span(s): %w",
				tracePath, len(spans), decodeErr)
		}
		spans = append(spans, span)
	}
	if len(spans) == 0 {
		return nil, fmt.Errorf(
			"trace %s decoded no spans; every assertion over it would read as absent", tracePath)
	}
	return spans, nil
}

// integrationHTTPRequestTimeout bounds a probe: "is this service up?", a
// question a reachable service answers immediately and an unreachable one must
// fail fast on. It is not a bound on model work. Inference calls run on
// integrationInferenceTimeout instead, because sharing one constant conflates
// two different questions and only breaks under load, which is exactly when a
// tracer runs (GH-709).
const integrationHTTPRequestTimeout = 2 * time.Second

// integrationInferenceTimeoutDefault bounds one model call. Embedding an 8B
// model measures 0.15s warm but 1.91s under the tracer's own ingest load on an
// M2 Max, and a cold 4.7GB load on a fresh machine is slower still, so the
// bound is sized for model work rather than for a healthy round trip.
const integrationInferenceTimeoutDefault = 120 * time.Second

var integrationHTTPClient = &http.Client{Timeout: integrationHTTPRequestTimeout}

var integrationInferenceClient = &http.Client{Timeout: integrationInferenceTimeout()}

// integrationInferenceTimeout returns the configured inference bound: demo.yaml
// inference_timeout when set, so a slower host or a larger model does not
// require a code change, otherwise the shipped default.
func integrationInferenceTimeout() time.Duration {
	root, err := os.Getwd()
	if err != nil {
		return integrationInferenceTimeoutDefault
	}
	return inferenceTimeoutFrom(loadDemoConfigOrEmpty(root))
}

// inferenceTimeoutFrom parses the demo.yaml inference bound, falling back to
// the default when the field is unset or unparseable. A bad value must not
// silently become a 2s bound and resurrect GH-709.
func inferenceTimeoutFrom(config demoConfig) time.Duration {
	raw := strings.TrimSpace(config.InferenceTimeout)
	if raw == "" {
		return integrationInferenceTimeoutDefault
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return integrationInferenceTimeoutDefault
	}
	return parsed
}

// Integration groups the application's end-to-end tracer targets. Each starts real
// services (a Chroma container, the mesh agents, an external Ollama) and skips
// cleanly (does not fail) when the toolchain or a configured model is
// unavailable, so the group stays runnable in a checkout without them.
type Integration mg.Namespace

// requireProfilePaths returns an error naming the first relative profile path
// under root that does not exist, so a target fails loudly on a bad repoint
// rather than skipping for the wrong reason.
func requireProfilePaths(root string, rels ...string) error {
	for _, rel := range rels {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			return fmt.Errorf("required profile path %s: %w", rel, err)
		}
	}
	return nil
}

// startDetachedAgent launches an agent profile as a long-running subprocess with
// its OTel spans written to tracePath, and returns a stop function. stop(kill=false)
// waits up to 15s for a graceful exit after the caller has requested a lifecycle
// exit; stop(kill=true) force-kills. The trace file is the caller's to read and
// remove, so an integration can assert each agent's spans after its graceful exit
// flushes them.
func startDetachedAgent(binary, profilesRoot, coreRoot, profile, tracePath string) (func(kill bool) error, error) {
	return startDetachedAgentWithTimeout(binary, profilesRoot, coreRoot, profile, tracePath, 15*time.Second)
}

func startDetachedAgentWithTimeout(binary, profilesRoot, coreRoot, profile, tracePath string, gracefulWait time.Duration) (func(kill bool) error, error) {
	return startDetachedAgentWithEnv(agentLaunch{
		Binary: binary, ProfilesRoot: profilesRoot, CoreRoot: coreRoot,
		Profile: profile, TracePath: tracePath, GracefulWait: gracefulWait,
	})
}

// agentLaunch is one detached agent invocation. Workdir and Env exist for
// integrations that must place the agent's workspace somewhere they control or
// point its declared environment at test doubles -- the applier tracer runs the
// shipped profile against fake helm and kubectl on PATH (GH-731).
type agentLaunch struct {
	Binary       string
	ProfilesRoot string
	CoreRoot     string
	Profile      string
	TracePath    string
	OTLPEndpoint string
	ServiceName  string
	Target       string
	RunID        string
	GitCommit    string
	Workdir      string   // defaults to os.TempDir()
	Env          []string // appended to the parent environment
	GracefulWait time.Duration
}

func startDetachedAgentWithEnv(launch agentLaunch) (func(kill bool) error, error) {
	profile := launch.Profile
	profilesRoot := launch.ProfilesRoot
	gracefulWait := launch.GracefulWait
	profilePath := profile
	if !filepath.IsAbs(profilePath) {
		profilePath = filepath.Join(profilesRoot, profile)
	}
	// Preflight the listeners before starting anything. An agent that cannot
	// bind a port does not report that itself in a way the caller sees: the
	// caller waits on the agent's control health, times out, and names the
	// control port -- which is usually the one that was free. Naming the taken
	// port and the process holding it turns a thirty-second wait and an
	// investigation of the wrong port into an immediate answer.
	if err := preflightAgentListeners(profilesRoot, profile, launch.Env); err != nil {
		return nil, err
	}
	workdir := launch.Workdir
	if workdir == "" {
		workdir = os.TempDir()
	}
	args := []string{
		"--profile", profilePath,
		"--directory", workdir,
		"--core-root", launch.CoreRoot,
		"--otel-log-file", launch.TracePath,
	}
	endpoint := firstNonEmpty(launch.OTLPEndpoint, demoIntegrationOTLPEndpoint())
	serviceName := firstNonEmpty(launch.ServiceName, profileServiceName(profile))
	args = append(args, integrationTelemetryArgs(endpoint, serviceName)...)
	cmd := exec.Command(launch.Binary, args...)
	cmd.Dir = profilesRoot
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	target := firstNonEmpty(launch.Target, profile)
	runID := strings.TrimSpace(launch.RunID)
	if runID == "" {
		runID = generatedRunID(target)
	}
	commit := strings.TrimSpace(launch.GitCommit)
	if commit == "" {
		commit = gitCommit(profilesRoot)
	}
	resourceAttrs := integrationResourceAttributes(target, runID, commit)
	cmd.Env = append(append(os.Environ(), launch.Env...),
		"OTEL_RESOURCE_ATTRIBUTES="+resourceAttrs)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", profile, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return func(kill bool) error {
		if kill {
			if err := cmd.Process.Kill(); err != nil {
				return fmt.Errorf("kill %s: %w", profile, err)
			}
			<-done // a signal exit is expected after an explicit force-kill.
			return nil
		}
		select {
		case err := <-done:
			if err != nil {
				return fmt.Errorf("%s exited during graceful shutdown: %w", profile, err)
			}
			return nil
		case <-time.After(gracefulWait):
			killErr := cmd.Process.Kill()
			waitErr := <-done
			if killErr != nil {
				return fmt.Errorf("%s did not stop within %s; kill failed: %w", profile, gracefulWait, killErr)
			}
			if waitErr != nil {
				return fmt.Errorf("%s did not stop within %s (forced process exit: %v)", profile, gracefulWait, waitErr)
			}
			return fmt.Errorf("%s did not stop within %s", profile, gracefulWait)
		}
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func profileServiceName(profile string) string {
	clean := filepath.Clean(profile)
	if filepath.Base(clean) == "profile.yaml" {
		return filepath.Base(filepath.Dir(clean))
	}
	return strings.TrimSuffix(filepath.Base(clean), filepath.Ext(clean))
}

func generatedRunID(target string) string {
	return fmt.Sprintf("%s-%d-%d", profileServiceName(target), time.Now().UTC().UnixNano(), os.Getpid())
}

func gitCommit(dir string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func integrationResourceAttributes(target, runID, commit string) string {
	attrs := [][2]string{
		{"test.repository", "mesh-intelligence/declarative-agents"},
		{"test.module", "applications/chatbot-mesh"},
		{"test.target", target},
		{"vcs.ref.head.revision", commit},
		{"test.run.id", runID},
	}
	encoded := make([]string, 0, len(attrs))
	for _, attr := range attrs {
		encoded = append(encoded, attr[0]+"="+url.QueryEscape(attr[1]))
	}
	return strings.Join(encoded, ",")
}

func integrationTelemetryArgs(endpoint, serviceName string) []string {
	if endpoint == "" {
		return nil
	}
	return []string{
		"--otel-otlp-endpoint", endpoint,
		"--otel-service-name", serviceName,
	}
}

func hostIntegrationTelemetry(target, serviceName, repoRoot string) ([]string, string) {
	endpoint := demoIntegrationOTLPEndpoint()
	runID := generatedRunID(target)
	commit := gitCommit(repoRoot)
	return integrationTelemetryArgs(endpoint, serviceName),
		"OTEL_RESOURCE_ATTRIBUTES=" + integrationResourceAttributes(target, runID, commit)
}

func waitHTTPStatus(url string, want int, timeout time.Duration) error {
	return waitHTTPStatusWithClient(integrationHTTPClient, url, want, timeout)
}

func waitHTTPStatusWithClient(client *http.Client, url string, want int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		ctx, cancel := context.WithTimeout(context.Background(), min(integrationHTTPRequestTimeout, remaining))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err == nil {
			var resp *http.Response
			resp, err = client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == want {
					cancel()
					return nil
				}
				lastErr = fmt.Errorf("status %d", resp.StatusCode)
			}
		}
		cancel()
		if err == nil {
			remaining = time.Until(deadline)
			if remaining > 0 {
				time.Sleep(min(100*time.Millisecond, remaining))
			}
			continue
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = context.DeadlineExceeded
	}
	return fmt.Errorf("wait for %s status %d: %w", url, want, lastErr)
}

func requestHTTP(method, url, body string) ([]byte, int, error) {
	return requestHTTPWithClient(integrationHTTPClient, method, url, body)
}

func requestHTTPWithClient(client *http.Client, method, url, body string) ([]byte, int, error) {
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	return data, resp.StatusCode, err
}

// requestInference issues a call that runs model inference, on the inference
// bound rather than the probe bound. work names the model or operation so a
// timeout reports which work exceeded its bound, at which endpoint, and how
// long it waited: without that, a slow model and a dead service produce the
// same bare "context deadline exceeded" (GH-709 R3).
func requestInference(method, url, body, work string) ([]byte, int, error) {
	started := time.Now()
	data, status, err := requestHTTPWithClient(integrationInferenceClient, method, url, body)
	if err != nil {
		return nil, 0, fmt.Errorf("%s at %s failed after %s (inference timeout %s, set inference_timeout in %s to change it): %w",
			work, url, time.Since(started).Round(time.Millisecond),
			integrationInferenceTimeout(), demoConfigFile, err)
	}
	return data, status, nil
}

func readIntegrationYAML(path, label string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", label, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("parse %s: %w", label, err)
	}
	return nil
}

// freeLoopbackAddr binds an ephemeral loopback port and returns its address, so a
// tracer can hand a real free address to a subprocess it launches.
func freeLoopbackAddr() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().String(), nil
}

// containsString reports whether values contains want.
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// preflightAgentListeners fails when a port the profile's listeners declare is
// already held, naming the port and its owner through the same portAvailable
// the hermetic rig uses for its mock fixtures.
//
// A profile whose listeners cannot be read is not an error here. This is a
// diagnosis that runs before the real work, and a parse it cannot do is a
// reason to let the agent start and report for itself rather than to refuse a
// run that might have succeeded.
func preflightAgentListeners(profilesRoot, profile string, env []string) error {
	ports, err := agentListenerPorts(profilesRoot, profile, env)
	if err != nil {
		return nil
	}
	for _, port := range ports {
		if err := portAvailable(profileServiceName(profile)+" listener", port); err != nil {
			return err
		}
	}
	return nil
}

// agentListenerPorts returns the TCP ports the profile's REST servers bind.
//
// The addresses come from the profile's own REST definitions rather than from a
// field every call site would have to keep in step with the YAML it duplicates.
// They are written either literally (127.0.0.1:18080) or with an environment
// default (${RAG_BIND_HOST:-127.0.0.1}:18085), so the port is the segment after
// the last colon, resolved through the environment the child will inherit.
func agentListenerPorts(profilesRoot, profile string, env []string) ([]string, error) {
	profilePath := profile
	if !filepath.IsAbs(profilePath) {
		profilePath = filepath.Join(profilesRoot, profile)
	}
	data, err := os.ReadFile(profilePath)
	if err != nil {
		return nil, err
	}
	var manifest struct {
		RESTDefinitions []string `yaml:"rest_definitions"`
	}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var ports []string
	for _, definition := range manifest.RESTDefinitions {
		path := definition
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(profilePath), definition)
		}
		restData, err := os.ReadFile(path)
		if err != nil {
			// A definition outside the checkout (the container's /opt paths) is
			// not readable here and declares no local listener.
			continue
		}
		var rest struct {
			Instantiate []struct {
				Args map[string]any `yaml:"args"`
			} `yaml:"expand"`
			REST struct {
				Servers map[string]struct {
					Address string `yaml:"address"`
				} `yaml:"servers"`
			} `yaml:"rest"`
		}
		if err := yaml.Unmarshal(restData, &rest); err != nil {
			continue
		}
		var addresses []string
		for _, server := range rest.REST.Servers {
			addresses = append(addresses, server.Address)
		}
		// A server a fragment produces binds the address its instantiation
		// passes, as the monitor server fragment's does.
		for _, instance := range rest.Instantiate {
			if address, ok := instance.Args["address"].(string); ok {
				addresses = append(addresses, address)
			}
		}
		for _, address := range addresses {
			port := listenerPort(address, env)
			if port == "" || seen[port] {
				continue
			}
			seen[port] = true
			ports = append(ports, port)
		}
	}
	sort.Strings(ports)
	return ports, nil
}

// listenerPort extracts the port from one declared server address, resolving an
// environment reference when the port itself is one. It returns "" for an
// address whose port cannot be decided, so an unparseable entry is skipped
// rather than guessed at.
func listenerPort(address string, env []string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return ""
	}
	// The port is the last colon-separated segment, except that a ${VAR:-default}
	// reference carries a colon of its own -- splitting on the last one would
	// cut that reference in half and yield "-default}". When the address ends in
	// a reference, that whole reference is the port.
	var token string
	if strings.HasSuffix(address, "}") {
		if open := strings.LastIndex(address, "${"); open > 0 && address[open-1] == ':' {
			token = address[open:]
		}
	}
	if token == "" {
		index := strings.LastIndex(address, ":")
		if index < 0 {
			return ""
		}
		token = address[index+1:]
	}
	port := expandLaunchEnv(strings.TrimSpace(token), env)
	for _, r := range port {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return port
}

// expandLaunchEnv resolves a ${NAME:-default} reference against the environment
// the child will inherit: the launch's own additions first, because those
// override the parent's, then the parent's, then the declared default.
func expandLaunchEnv(value string, env []string) string {
	if !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
		return value
	}
	body := value[2 : len(value)-1]
	name, fallback := body, ""
	if cut := strings.Index(body, ":-"); cut >= 0 {
		name, fallback = body[:cut], body[cut+2:]
	}
	for index := len(env) - 1; index >= 0; index-- {
		if suffix, ok := strings.CutPrefix(env[index], name+"="); ok {
			if suffix != "" {
				return suffix
			}
		}
	}
	if resolved := envOrDefault(name, ""); resolved != "" {
		return resolved
	}
	return fallback
}

// setTargetEnv sets process environment a target shares with the agent
// children it starts and with its own in-process seeding, and returns the
// restore the target defers. integration:all runs targets in one process, so a
// value left behind is inherited by every later target's children: the
// clearance target's RAG_COLLECTION and Cohere embedding identity pointed
// ragServer's rag-server at an empty collection it never seeded (GH-760).
func setTargetEnv(values map[string]string) (func(), error) {
	type previous struct {
		value string
		had   bool
	}
	saved := make(map[string]previous, len(values))
	restore := func() {
		for name, old := range saved {
			if old.had {
				//nolint:forbidigo // Restores the value the target found.
				_ = os.Setenv(name, old.value)
			} else {
				//nolint:forbidigo // Same restore.
				_ = os.Unsetenv(name)
			}
		}
	}
	for name, value := range values {
		//nolint:forbidigo // Reads the value the restore puts back.
		old, had := os.LookupEnv(name)
		saved[name] = previous{value: old, had: had}
		//nolint:forbidigo // One value shared by this process and the agent children it starts.
		if err := os.Setenv(name, value); err != nil {
			restore()
			return nil, fmt.Errorf("set %s: %w", name, err)
		}
	}
	return restore, nil
}
