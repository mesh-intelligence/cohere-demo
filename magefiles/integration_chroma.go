// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	chromaCorpusFixture        = "testdata/integration/chroma-corpus"
	chromaIngestProfile        = "agents/corpus-ingest/profile.yaml"
	corpusRestAsset            = "agents/corpus-ingest/corpus-rest.yaml"
	corpusEmbedAsset           = "agents/corpus-ingest/embed-rest.yaml"
	corpusIngestLibraryProfile = "agents/knowledge-manager/corpus-ingest/profile.yaml"

	chromaImage = "chromadb/chroma:1.5.3"

	chromaHeartbeatURL = "http://127.0.0.1:8000/api/v2/heartbeat"

	// chromaReuseProbeTimeout bounds the "is a Chroma already there?" question.
	// It is short because the answer is local and immediate, and every run with
	// no Chroma present pays it before starting its own.
	chromaReuseProbeTimeout = 2 * time.Second
	ollamaVersionURL        = "http://127.0.0.1:11434/api/version"
	ollamaTagsURL           = "http://127.0.0.1:11434/api/tags"
	ollamaProcessesURL      = "http://127.0.0.1:11434/api/ps"
)

// Chroma proves the corpus-ingest profile against a live Chroma server run from
// the chromadb/chroma Docker container and a live Ollama provider: ingest loads
// the corpus fixture and the collection count verifies documents were written.
// The seeded collection is what the rag-server and chatbot targets query. The
// target skips (does not fail) when Docker or Ollama with the configured chat and
// embedding models is unavailable, so the group stays usable without them.
func (Integration) Chroma() error {
	profilesRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	coreRoot := demoCoreRoot(profilesRoot)
	if err := requireProfilePaths(profilesRoot, chromaIngestProfile, corpusRestAsset); err != nil {
		return err
	}
	chatModel := demoChromaIntegrationChatModel(profilesRoot)
	requiredModels, err := chromaRequiredModelsForChat(profilesRoot, chatModel)
	if err != nil {
		return fmt.Errorf("invalid shipped Chroma model config: %w", err)
	}
	if reason := chromaOllamaSkipReasonForModels(requiredModels); reason != "" {
		fmt.Printf("SKIP chroma: %s\n", reason)
		return nil
	}
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Println("SKIP chroma: docker not found on PATH")
		return nil
	}
	return runChromaIntegration(profilesRoot, coreRoot, chatModel)
}

// Seed loads the application corpus into a running Chroma through the mesh wrapper
// around the canonical corpus-ingest profile, so a developer can populate the collection the rag-server
// and chatbot serve. It embeds through Ollama and writes to Chroma at the declared
// local ports. Skips cleanly when agent-core, Ollama with the configured models,
// or Chroma is unavailable.
func Seed() error {
	return seedWithEmbedding(demoSeedEmbeddingProvider{})
}

// seedWithEmbedding is Seed with the embedding provider chosen by the caller.
// The host-side seed embeds through Ollama, which is the shipped default;
// app:seed passes the provider the deployed release configures, so the vectors
// written here carry the same embedding identity the deployed chatbot declares
// for its query vector (srd002 R3.3, rel10.3-uc001 F6).
func seedWithEmbedding(embedding demoSeedEmbeddingProvider) error {
	profilesRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	coreRoot := demoCoreRoot(profilesRoot)
	if !agentCoreAvailable(coreRoot) {
		fmt.Printf("SKIP seed: agent-core checkout not found at %s (set core_root in demo.yaml)\n", coreRoot)
		return nil
	}
	if err := requireProfilePaths(profilesRoot, chromaIngestProfile, corpusRestAsset); err != nil {
		return err
	}
	requiredModels, err := chromaSeedRequiredModels(profilesRoot, embedding)
	if err != nil {
		return fmt.Errorf("invalid shipped Chroma model config: %w", err)
	}
	if reason := chromaOllamaSkipReasonForModels(requiredModels); reason != "" {
		fmt.Printf("SKIP seed: %s\n", reason)
		return nil
	}
	if err := waitHTTPStatus(chromaHeartbeatURL, http.StatusOK, 2*time.Second); err != nil {
		fmt.Printf("SKIP seed: Chroma not reachable at %s: %v\n", chromaHeartbeatURL, err)
		return nil
	}
	binary, cleanupAgent, err := buildAgent(coreRoot)
	if err != nil {
		return err
	}
	defer cleanupAgent()
	defer func() { _ = os.Remove(binary) }()
	if err := runChromaIngestWithEmbedding(
		binary, profilesRoot, coreRoot, "", embedding); err != nil {
		return err
	}
	fmt.Println("seed: corpus ingested into Chroma")
	return nil
}

func chromaOllamaSkipReasonForModels(required []string) string {
	if err := waitHTTPStatus(ollamaVersionURL, http.StatusOK, 2*time.Second); err != nil {
		return fmt.Sprintf("Ollama not reachable at %s: %v", ollamaVersionURL, err)
	}
	names, err := fetchChromaOllamaModels()
	if err != nil {
		return fmt.Sprintf("Ollama /api/tags preflight failed: %v", err)
	}
	for _, model := range required {
		if !chromaModelInstalled(names, model) {
			return fmt.Sprintf("Ollama model %q not installed; available: %s", model, strings.Join(names, ", "))
		}
	}
	return ""
}

// chromaRequiredModels returns the distinct Ollama model names the ingest profile
// uses: the embedding model from the REST asset and the invoke_llm chat model from
// the profile's declarations. Reading them from the config keeps it the single
// source of truth for the skip gate.
func chromaRequiredModels(profilesRoot string) ([]string, error) {
	return chromaRequiredModelsForChat(profilesRoot, "")
}

// chromaSeedRequiredModels names the host Ollama models a seed needs. Embedding
// through Cohere drops the Ollama embedding model from that list: requiring a
// model the run will not call would skip a seed the provider can serve. The
// corpus-ingest chat model stays required either way — the ingest reasons over
// each document regardless of who computes the vector.
func chromaSeedRequiredModels(
	profilesRoot string, embedding demoSeedEmbeddingProvider,
) ([]string, error) {
	if !embedding.cohere {
		return chromaRequiredModels(profilesRoot)
	}
	chat, err := chromaChatModelFromConfig(profilesRoot, "corpus-ingest")
	if err != nil {
		return nil, err
	}
	return []string{chat}, nil
}

func chromaRequiredModelsForChat(
	profilesRoot, selectedChatModel string,
) ([]string, error) {
	set := map[string]bool{}
	embed, err := chromaEmbedModelFromConfig(profilesRoot)
	if err != nil {
		return nil, err
	}
	set[embed] = true
	chat := strings.TrimSpace(selectedChatModel)
	if chat == "" {
		chat, err = chromaChatModelFromConfig(profilesRoot, "corpus-ingest")
		if err != nil {
			return nil, err
		}
	}
	set[chat] = true
	models := make([]string, 0, len(set))
	for model := range set {
		models = append(models, model)
	}
	sort.Strings(models)
	return models, nil
}

// chromaEmbedModelFromConfig reads the embedding model the corpus-ingest
// embed-rest.yaml passes to the provider library's embed-document fragment
// (srd058 R4.2).
func chromaEmbedModelFromConfig(profilesRoot string) (string, error) {
	var cfg struct {
		Instantiate []struct {
			Args struct {
				Model string `yaml:"model"`
			} `yaml:"args"`
		} `yaml:"expand"`
	}
	path := filepath.Join(profilesRoot, corpusEmbedAsset)
	if err := readIntegrationYAML(path, "corpus embed asset", &cfg); err != nil {
		return "", err
	}
	var model string
	if len(cfg.Instantiate) > 0 {
		model = resolveModelReference(cfg.Instantiate[0].Args.Model)
	}
	if model == "" {
		return "", fmt.Errorf("no embed model in %s", path)
	}
	return model, nil
}

// chromaChatModelFromConfig reads the invoke_llm chat model from a profile's
// declarations.yaml.
func chromaChatModelFromConfig(profilesRoot, profile string) (string, error) {
	var cfg struct {
		Tools []struct {
			Name   string `yaml:"name"`
			Config struct {
				Model string `yaml:"model"`
			} `yaml:"config"`
		} `yaml:"tools"`
	}
	path := filepath.Join(profilesRoot, "agents", profile, "declarations.yaml")
	if profile == "corpus-ingest" {
		catalogRoot, err := corpusIngestLibraryRoot(profilesRoot)
		if err != nil {
			return "", err
		}
		path = filepath.Join(
			catalogRoot,
			"agents", "knowledge-manager", "corpus-ingest", "declarations.yaml")
	}
	if err := readIntegrationYAML(path, "chroma declarations", &cfg); err != nil {
		return "", err
	}
	for _, tool := range cfg.Tools {
		if tool.Name == "invoke_llm" {
			model := resolveModelReference(tool.Config.Model)
			if model == "" {
				return "", fmt.Errorf("invoke_llm has no model in %s", path)
			}
			return model, nil
		}
	}
	return "", fmt.Errorf("no invoke_llm tool in %s", path)
}

var modelReference = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*):-([^}]*)\}$`)

// resolveModelReference mirrors the runtime's ${VAR:-default} selection for
// integration preflights that read unexpanded profile YAML.
func resolveModelReference(model string) string {
	match := modelReference.FindStringSubmatch(model)
	if match == nil {
		return model
	}
	//nolint:forbidigo // This preflight mirrors srd013 R5.6/R5.7 expansion before runtime loading.
	if value, ok := os.LookupEnv(match[1]); ok {
		return value
	}
	return match[2]
}

// chromaModelInstalled matches a configured model against the installed model
// names, tolerating the optional ":latest" tag Ollama omits in /api/tags.
func chromaModelInstalled(names []string, model string) bool {
	for _, name := range names {
		if name == model || name == model+":latest" || strings.TrimSuffix(name, ":latest") == model {
			return true
		}
	}
	return false
}

func fetchChromaOllamaModels() ([]string, error) {
	data, status, err := requestHTTP(http.MethodGet, ollamaTagsURL, "")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("/api/tags returned status %d", status)
	}
	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(result.Models))
	for _, model := range result.Models {
		if model.Name != "" {
			names = append(names, model.Name)
		}
	}
	return names, nil
}

// ingestDefaultClearance is the level the agent-driven ingest writes, and it is
// the highest in the declared ordered set (corpus/corpus.yaml clearance_levels).
// It moves only with that set.
const ingestDefaultClearance = "secret"

func runChromaIntegration(profilesRoot, coreRoot, chatModel string) error {
	binary, cleanupAgent, err := buildAgent(coreRoot)
	if err != nil {
		return err
	}
	defer cleanupAgent()
	dataDir, err := os.MkdirTemp("", "chatbot-mesh-chroma-data-*")
	if err != nil {
		return fmt.Errorf("create chroma data dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dataDir) }()
	containerID, err := startRequiredChromaContainer(dataDir, ensureChromaServer)
	if err != nil {
		return err
	}
	defer stopChromaContainer(containerID)
	if err := runChromaIngest(binary, profilesRoot, coreRoot, chatModel); err != nil {
		return err
	}
	if err := assertIngestClearanceTagging(); err != nil {
		return err
	}
	fmt.Println("integration:chroma PASS - ingest loaded the corpus, every record carries a clearance tag, and the collection count verified documents were written")
	return nil
}

// assertIngestClearanceTagging is the rel11.0 suite's ingest case (S4, srd010
// AC1) on the agent-driven write path.
//
// It reads the records back rather than trusting the request that wrote them,
// because the failure it exists to catch is silent: a record with no clearance
// field matches no window the rag-server can build, so it is unreachable at
// every level and the corpus reads as empty rather than as mis-tagged.
//
// The level asserted is the highest declared one, and that is R1.3 rather than
// a weaker check. The canonical ingest machine carries a document's text and id
// and nothing else, so on this path a declared classification is absent, and an
// absent level is tagged highest. A graded corpus comes from the store-seed
// path (srd015 R5.3), which carries the levels the corpus build copied off each
// document's declared front matter.
func assertIngestClearanceTagging() error {
	base := "http://127.0.0.1:8000/api/v2/tenants/default_tenant/databases/default_database/collections"
	data, status, err := requestHTTP(http.MethodPost, base, `{"name":"corpus","get_or_create":true}`)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return fmt.Errorf("resolve corpus collection: status %d: %s", status, data)
	}
	var collection struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &collection); err != nil {
		return fmt.Errorf("decode collection id: %w", err)
	}
	recordData, recordStatus, err := requestHTTP(http.MethodPost,
		base+"/"+collection.ID+"/get", `{"include":["metadatas"]}`)
	if err != nil {
		return err
	}
	if recordStatus != http.StatusOK {
		return fmt.Errorf("read ingested records: status %d: %s", recordStatus, recordData)
	}
	var records struct {
		IDs       []string                 `json:"ids"`
		Metadatas []map[string]interface{} `json:"metadatas"`
	}
	if err := json.Unmarshal(recordData, &records); err != nil {
		return fmt.Errorf("decode ingested records: %w", err)
	}
	if len(records.IDs) == 0 {
		return fmt.Errorf("the ingest wrote no records, so the clearance tagging assertion has nothing to read")
	}
	if len(records.Metadatas) != len(records.IDs) {
		return fmt.Errorf("the collection holds %d record(s) and %d metadata entr(ies);"+
			" a record with no metadata carries no clearance and is unreachable at every level",
			len(records.IDs), len(records.Metadatas))
	}
	for index, metadata := range records.Metadatas {
		id := ""
		if index < len(records.IDs) {
			id = records.IDs[index]
		}
		level, ok := metadata["clearance"].(string)
		if !ok || level == "" {
			return fmt.Errorf("ingested record %q carries no clearance metadata;"+
				" the rag-server filter would match it at no level, so the corpus reads as empty"+
				" rather than as mis-tagged (srd010 R1.2, R1.5)", id)
		}
		if level != ingestDefaultClearance {
			return fmt.Errorf("ingested record %q is tagged %q, want %q:"+
				" the canonical ingest machine carries no declared classification, so every record"+
				" on this path takes the highest declared level (srd010 R1.3)",
				id, level, ingestDefaultClearance)
		}
	}
	fmt.Printf("integration:chroma: all %d ingested record(s) carry clearance %q, the highest declared level\n",
		len(records.IDs), ingestDefaultClearance)
	return nil
}

func startRequiredChromaContainer(dataDir string, start func(string) (string, error)) (string, error) {
	containerID, err := start(dataDir)
	if err != nil {
		return "", fmt.Errorf("start required Chroma container: %w", err)
	}
	return containerID, nil
}

// ensureChromaServer reuses a Chroma already serving the port and otherwise
// starts one. It returns a container id only when this run created it, so the
// empty id is the created-by-me guard: stopChromaContainer ignores it, and a
// Chroma the operator started by hand survives the run. This mirrors the kind
// helper's reuse-and-do-not-delete rule (GH-589) and makes the target compose
// with a pre-existing Chroma that previously killed it on a port conflict
// (GH-708).
func ensureChromaServer(dataDir string) (string, error) {
	if waitHTTPStatus(chromaHeartbeatURL, http.StatusOK, chromaReuseProbeTimeout) == nil {
		fmt.Printf("chroma: reusing the server already answering %s; it will not be removed. "+
			"Its image and data are not this target's %s and %s, so a corpus collection left "+
			"there at a different embedding dimension will fail the query\n",
			chromaHeartbeatURL, chromaImage, dataDir)
		return "", nil
	}
	return startChromaContainer(dataDir)
}

// startChromaContainer runs the chromadb/chroma image detached with the
// persistence directory bind-mounted at /data and the v2 API published on
// 127.0.0.1:8000, then waits for the heartbeat. A missing Docker daemon, an
// unpullable image, or a heartbeat that never arrives is returned as an error
// so the caller can skip rather than fail.
func startChromaContainer(dataDir string) (string, error) {
	image := chromaImage
	out, err := exec.Command("docker", "run", "-d", "--rm",
		"-p", "127.0.0.1:8000:8000",
		"-v", dataDir+":/data",
		image,
	).CombinedOutput()
	if err != nil {
		return "", chromaLaunchError(image, err, strings.TrimSpace(string(out)))
	}
	containerID := strings.TrimSpace(string(out))
	if err := waitHTTPStatus(chromaHeartbeatURL, http.StatusOK, 60*time.Second); err != nil {
		stopChromaContainer(containerID)
		return "", fmt.Errorf("chroma container served no heartbeat: %w", err)
	}
	return containerID, nil
}

// chromaLaunchError names the cause when the port is held by something that is
// not a Chroma. Reuse already handled a healthy one, so reaching here means the
// occupant never answered the heartbeat, and the bare docker "exit status 125"
// sends the reader to the daemon rather than to the process holding the port.
func chromaLaunchError(image string, err error, detail string) error {
	for _, marker := range []string{"port is already allocated", "address already in use"} {
		if strings.Contains(detail, marker) {
			return fmt.Errorf(
				"port 8000 is held by a process that does not answer the Chroma heartbeat at %s. "+
					"Stop it, or start a Chroma there and this target will reuse it, then re-run "+
					"(docker run %s: %v: %s)",
				chromaHeartbeatURL, image, err, detail)
		}
	}
	return fmt.Errorf("docker run %s: %v: %s", image, err, detail)
}

func stopChromaContainer(containerID string) {
	if containerID == "" {
		return
	}
	_ = exec.Command("docker", "rm", "-f", containerID).Run()
}

func runChromaIngest(binary, profilesRoot, coreRoot, chatModel string) error {
	return runChromaIngestWithEmbedding(
		binary, profilesRoot, coreRoot, chatModel, demoSeedEmbeddingProvider{})
}

func runChromaIngestWithEmbedding(
	binary, profilesRoot, coreRoot, chatModel string,
	embedding demoSeedEmbeddingProvider,
) error {
	corpusDir := filepath.Join(profilesRoot, chromaCorpusFixture, "corpus")
	ingestTimeout := demoChromaIngestTimeout(profilesRoot)
	runtimeRoot, cleanupRuntime, err := stageCorpusIngestRuntime(profilesRoot)
	if err != nil {
		return err
	}
	defer cleanupRuntime()
	profile := filepath.Join(runtimeRoot, chromaIngestProfile)
	return runWithChromaIngestTrace(func(trace string) error {
		if err := runChromaAgent(
			binary, runtimeRoot, coreRoot, profile, corpusDir, trace, ingestTimeout,
			chatModel, embedding); err != nil {
			model := strings.TrimSpace(chatModel)
			if model == "" {
				model = "profile default"
			}
			return fmt.Errorf("chroma ingest run failed with chat model %q: %w\n%s",
				model, err, chromaOllamaProcessDiagnostics())
		}
		if err := assertChromaIngestTrace(trace); err != nil {
			return err
		}
		count, err := chromaCollectionCount()
		if err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("ingest added no documents to the corpus collection")
		}
		return nil
	})
}

// runWithChromaIngestTrace creates the ingest --otel-log-file and deletes it
// only after a successful run. Failures and panics leave the file in place and
// name its path on the returned error so a release-lane miss stays diagnosable.
func runWithChromaIngestTrace(run func(trace string) error) error {
	trace, cleanup, err := chromaTraceFile("ingest")
	if err != nil {
		return err
	}
	retain := true
	defer func() {
		if !retain {
			cleanup()
		}
	}()
	if err := run(trace); err != nil {
		return retainChromaIngestTrace(trace, err)
	}
	retain = false
	return nil
}

func retainChromaIngestTrace(trace string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w\ningest trace retained at %s", err, trace)
}

func corpusIngestLibraryRoot(meshRoot string) (string, error) {
	if info, err := os.Stat(filepath.Join(
		meshRoot, filepath.FromSlash(corpusIngestLibraryProfile))); err == nil && !info.IsDir() {
		return meshRoot, nil
	}
	return resolveCatalogRoot("chatbot-mesh corpus ingest", meshRoot)
}

func stageCorpusIngestRuntime(meshRoot string) (string, func(), error) {
	stage, err := os.MkdirTemp("", "chatbot-mesh-corpus-ingest-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(stage) }
	if err := copyDirContents(
		filepath.Join(meshRoot, "agents", "corpus-ingest"),
		filepath.Join(stage, "agents", "corpus-ingest")); err != nil {
		cleanup()
		return "", nil, err
	}
	libraryRoot, err := corpusIngestLibraryRoot(meshRoot)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if err := copyDirContents(
		filepath.Join(libraryRoot, "agents", "knowledge-manager", "corpus-ingest"),
		filepath.Join(stage, "agents", "knowledge-manager", "corpus-ingest")); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("stage canonical corpus-ingest profile: %w", err)
	}
	// The canonical declarations import the catalog's shared type units by a
	// path relative to themselves, so the stage carries that directory too or
	// the closure cannot resolve it.
	if err := copyDirContents(
		filepath.Join(libraryRoot, "agents", "units"),
		filepath.Join(stage, "agents", "units")); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("stage canonical corpus-ingest units: %w", err)
	}
	return stage, cleanup, nil
}

func runChromaAgent(
	binary, profilesRoot, coreRoot, profile, directory, tracePath string,
	timeout time.Duration,
	chatModel string,
	embedding demoSeedEmbeddingProvider,
) error {
	args := []string{
		"--profile", profile,
		"--directory", directory,
		"--core-root", coreRoot,
		"--verbose-trace",
		"--otel-log-file", tracePath,
	}
	args = append(args, embedding.libraryArgs(coreRoot)...)
	telemetryArgs, resourceEnv := hostIntegrationTelemetry("integration:chroma", "corpus-ingest", profilesRoot)
	return runChromaAgentWithTimeout(
		timeout,
		func(ctx context.Context) ([]byte, error) {
			cmd := exec.CommandContext(ctx, binary, append(args, telemetryArgs...)...)
			cmd.Dir = profilesRoot
			cmd.Env = chromaChildEnvironment(
				os.Environ(), resourceEnv, strings.TrimSpace(chatModel),
				embedding.environment()...)
			// CombinedOutput calls Wait after CommandContext kills the child, so
			// timeout reporting cannot race process reaping or runtime cleanup.
			return cmd.CombinedOutput()
		},
	)
}

// chromaChildEnvironment builds the ingest child's environment: the inherited
// one with the caller's overrides replacing any same-named entry, so a value
// set here wins over whatever the operator exported. Extra entries are
// "KEY=value" pairs, used to select the embedding provider.
func chromaChildEnvironment(
	inherited []string,
	resourceEnv, chatModel string,
	extra ...string,
) []string {
	overrides := []string{resourceEnv}
	if chatModel != "" {
		overrides = append(overrides, "CORPUS_CHAT_MODEL="+chatModel)
	}
	overrides = append(overrides, extra...)
	replaced := make(map[string]bool, len(overrides))
	for _, entry := range overrides {
		key, _, _ := strings.Cut(entry, "=")
		replaced[key] = true
	}
	environment := make([]string, 0, len(inherited)+len(overrides))
	for _, entry := range inherited {
		key, _, _ := strings.Cut(entry, "=")
		if replaced[key] {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, overrides...)
}

func chromaOllamaProcessDiagnostics() string {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(ollamaProcessesURL)
	if err != nil {
		return fmt.Sprintf("Ollama loaded-model diagnostics unavailable: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Sprintf("Ollama loaded-model diagnostics returned %s",
			response.Status)
	}
	var result struct {
		Models []struct {
			Name          string `json:"name"`
			Model         string `json:"model"`
			ContextLength int    `json:"context_length"`
			SizeVRAM      int64  `json:"size_vram"`
		} `json:"models"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return fmt.Sprintf("Ollama loaded-model diagnostics could not decode: %v",
			err)
	}
	if len(result.Models) == 0 {
		return "Ollama loaded models: none"
	}
	details := make([]string, 0, len(result.Models))
	for _, model := range result.Models {
		name := model.Name
		if name == "" {
			name = model.Model
		}
		details = append(details, fmt.Sprintf(
			"%s(context=%d,vram=%d)", name, model.ContextLength, model.SizeVRAM))
	}
	sort.Strings(details)
	return "Ollama loaded models: " + strings.Join(details, ", ")
}

// chromaAgentRun is the context-aware command boundary for the canonical ingest.
// Tests substitute a deterministic runner to exercise deadline and reaping
// behavior without starting an agent or waiting out the production budget.
type chromaAgentRun func(context.Context) ([]byte, error)

func runChromaAgentWithTimeout(timeout time.Duration, run chromaAgentRun) error {
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	out, err := run(ctx)
	elapsed := time.Since(started).Round(time.Millisecond)
	if contextErr := ctx.Err(); contextErr != nil {
		if detail := strings.TrimSpace(string(out)); detail != "" {
			return fmt.Errorf(
				"chroma ingest exceeded its %s whole-run timeout after %s (deadline %s; set chroma_ingest_timeout in %s to change it): %w\n%s",
				timeout, elapsed, deadline.UTC().Format(time.RFC3339Nano), demoConfigFile, contextErr, detail)
		}
		return fmt.Errorf(
			"chroma ingest exceeded its %s whole-run timeout after %s (deadline %s; set chroma_ingest_timeout in %s to change it): %w",
			timeout, elapsed, deadline.UTC().Format(time.RFC3339Nano), demoConfigFile, contextErr)
	}
	if err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	// The exit code above already rules out a failed terminal, which exits 2
	// (agent-core srd018 R6.1/R6.2, GH-683). This asserts the terminal state
	// itself because a suspended run also exits 0 -- a deliberate pause with a
	// persisted checkpoint is not a failure (R6.4) -- and a tracer that ingested
	// nothing because its machine parked mid-run has not proven the flow.
	if !strings.Contains(string(out), "status=succeeded") {
		return fmt.Errorf("agent did not reach a succeeded terminal state:\n%s", out)
	}
	return nil
}

// chromaCollectionCount resolves the corpus collection and reads its item count
// directly from Chroma, so the ingest assertion checks that documents were
// actually written rather than only that the flow ran.
func chromaCollectionCount() (int, error) {
	base := "http://127.0.0.1:8000/api/v2/tenants/default_tenant/databases/default_database/collections"
	data, status, err := requestHTTP(http.MethodPost, base, `{"name":"corpus","get_or_create":true}`)
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return 0, fmt.Errorf("resolve corpus collection: status %d: %s", status, data)
	}
	var collection struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &collection); err != nil {
		return 0, fmt.Errorf("decode collection id: %w", err)
	}
	countData, countStatus, err := requestHTTP(http.MethodGet, base+"/"+collection.ID+"/count", "")
	if err != nil {
		return 0, err
	}
	if countStatus != http.StatusOK {
		return 0, fmt.Errorf("read collection count: status %d: %s", countStatus, countData)
	}
	var count int
	if err := json.Unmarshal(countData, &count); err != nil {
		return 0, fmt.Errorf("decode collection count: %w", err)
	}
	return count, nil
}

func chromaTraceFile(label string) (string, func(), error) {
	f, err := os.CreateTemp("", "chatbot-mesh-chroma-"+label+"-*.ndjson")
	if err != nil {
		return "", nil, fmt.Errorf("create %s trace file: %w", label, err)
	}
	path := f.Name()
	_ = f.Close()
	return path, func() { _ = os.Remove(path) }, nil
}

// assertChromaIngestTrace proves the ingest precondition, the embed stage, and
// the terminal count verification ran: the Chroma readiness word, the stage's
// embed and normalize words (srd058 R4.2), and chroma_count each recorded a
// dispatch span.
func assertChromaIngestTrace(tracePath string) error {
	spans, err := readChromaSpans(tracePath)
	if err != nil {
		return err
	}
	present := chromaCommandSet(spans)
	for _, want := range []string{"chroma_ready", "embed_document", "normalize_document_embedding", "chroma_count"} {
		if !present[want] {
			return fmt.Errorf("ingest trace missing %q dispatch; saw %v", want, sortedKeys(present))
		}
	}
	return nil
}

func chromaCommandSet(spans []chromaSpan) map[string]bool {
	present := make(map[string]bool)
	for _, span := range spans {
		if name := span.commandName(); name != "" {
			present[name] = true
		}
	}
	return present
}

func readChromaSpans(tracePath string) ([]chromaSpan, error) {
	spans, err := decodeSpanStream[chromaSpan](tracePath)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(spans, func(i, j int) bool {
		return spans[i].start().Before(spans[j].start())
	})
	return spans, nil
}

type chromaSpan struct {
	Name        string          `json:"Name"`
	StartTime   string          `json:"StartTime"`
	Attributes  []chromaTraceKV `json:"Attributes"`
	SpanContext chromaSpanRef   `json:"SpanContext"`
	Parent      chromaSpanRef   `json:"Parent"`
}

// chromaSpanRef is the id pair the OTel file exporter writes for a span and its
// parent, so a connected cross-agent trace can be asserted across span logs.
type chromaSpanRef struct {
	TraceID string `json:"TraceID"`
	SpanID  string `json:"SpanID"`
}

type chromaTraceKV struct {
	Key   string `json:"Key"`
	Value struct {
		Value interface{} `json:"Value"`
	} `json:"Value"`
}

func (s chromaSpan) start() time.Time {
	t, err := time.Parse(time.RFC3339Nano, s.StartTime)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (s chromaSpan) commandName() string {
	name, _ := s.stringAttr("command.name")
	return name
}

func (s chromaSpan) stringAttr(key string) (string, bool) {
	for _, attr := range s.Attributes {
		if attr.Key == key {
			if value, ok := attr.Value.Value.(string); ok {
				return value, true
			}
		}
	}
	return "", false
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
