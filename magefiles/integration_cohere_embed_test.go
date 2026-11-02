// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// liveEmbedResponse is the shape the live provider returned on 2026-08-26 for
// one text at embed-v4.0 with input_type search_query, trimmed to two elements.
// It is recorded from an observed call rather than composed from the API
// documentation, which is the distinction the probe exists to enforce: a
// payload written from the same reading that produced the declaration would
// confirm the declaration against itself.
const liveEmbedResponse = `{
  "id": "probe",
  "texts": ["What does the corpus describe?"],
  "embeddings": {"float": [[0.0123, -0.0456]]},
  "meta": {"api_version": {"version": "2"}},
  "response_type": "embeddings_by_type"
}`

func observationFrom(t *testing.T, payload string) (cohereEmbedObservation, error) {
	t.Helper()
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	return readCohereEmbedObservation(decoded, "embed-v4.0")
}

func TestCohereEmbedObservationReadsTheLiveShape(t *testing.T) {
	observed, err := observationFrom(t, liveEmbedResponse)
	if err != nil {
		t.Fatalf("the recorded live response did not read: %v", err)
	}
	if !observed.RowsAreNested {
		t.Error("rows read as flat; the declared unwrap has nothing to unwrap")
	}
	if observed.RowCount != 1 {
		t.Errorf("row count is %d, want 1 for one text", observed.RowCount)
	}
	if observed.Dimension != 2 {
		t.Errorf("dimension is %d, want 2 for this trimmed fixture", observed.Dimension)
	}
	if !observed.ElementsNumber {
		t.Error("elements did not read as numbers; the declared vector schema would reject them")
	}
}

// A flat response is the assumption's opposite, and it must fail loudly rather
// than pass with RowsAreNested false. render_each faults when its items are not
// arrays, so a probe that tolerated this would green-light a declaration that
// cannot run.
func TestCohereEmbedObservationRejectsAFlatResponse(t *testing.T) {
	_, err := observationFrom(t, `{"embeddings": {"float": [0.1, 0.2, 0.3]}}`)
	if err == nil {
		t.Fatal("a flat embeddings.float was accepted; the declared unwrap would fault at runtime")
	}
	if !strings.Contains(err.Error(), "flat") {
		t.Errorf("error %q does not name the flat response as the finding", err)
	}
}

// More rows than texts is the condition the normalization word cannot survive:
// its item template wraps each row, so two rows concatenate into malformed
// JSON. The probe is where that is caught, not the scenario critic.
func TestCohereEmbedObservationRejectsMultipleRowsForOneText(t *testing.T) {
	_, err := observationFrom(t, `{"embeddings": {"float": [[0.1, 0.2], [0.3, 0.4]]}}`)
	if err == nil {
		t.Fatal("two rows for one text were accepted; the declared normalization assumes one")
	}
	if !strings.Contains(err.Error(), "yielded 2 rows") {
		t.Errorf("error %q does not name the row count", err)
	}
}

func TestCohereEmbedObservationRejectsNonNumericElements(t *testing.T) {
	_, err := observationFrom(t, `{"embeddings": {"float": [["a", "b"]]}}`)
	if err == nil {
		t.Fatal("string elements were accepted; the declared vector schema rejects them")
	}
	if !strings.Contains(err.Error(), "want a number") {
		t.Errorf("error %q does not name the element type", err)
	}
}

// An embeddings object keyed by something other than float is a configuration
// the declared selector cannot walk, and the failure must name the keys that
// were present so an operator can see what to point at.
func TestCohereEmbedObservationNamesThePresentEmbeddingKeys(t *testing.T) {
	_, err := observationFrom(t, `{"embeddings": {"int8": [[1, 2]]}}`)
	if err == nil {
		t.Fatal("a response with no float key was accepted")
	}
	if !strings.Contains(err.Error(), "int8") {
		t.Errorf("error %q does not name the keys that were present", err)
	}
}

// A response whose embeddings is an array rather than an object has no path for
// the declared $.embeddings.float selector, which walks maps only.
func TestCohereEmbedObservationRejectsUnkeyedEmbeddings(t *testing.T) {
	_, err := observationFrom(t, `{"embeddings": [[0.1, 0.2]]}`)
	if err == nil {
		t.Fatal("an unkeyed embeddings array was accepted; the declared mapping walks maps only")
	}
	if !strings.Contains(err.Error(), "keyed by embedding type") {
		t.Errorf("error %q does not name the expected shape", err)
	}
}

func TestCohereEmbedObservationReportsTopLevelKeysWhenEmbeddingsAreAbsent(t *testing.T) {
	_, err := observationFrom(t, `{"id": "x", "message": "no embeddings here"}`)
	if err == nil {
		t.Fatal("a response with no embeddings field was accepted")
	}
	for _, want := range []string{"id", "message"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not report the top-level key %q", err, want)
		}
	}
}

// pinAbsentCredentialFile points the file fallback at a path that cannot
// exist. An absence test that left the ambient path in place would turn live
// on a checkout whose .secrets/keys.json holds a real key.
func pinAbsentCredentialFile(t *testing.T) {
	t.Helper()
	previous := cohereCredentialFile
	cohereCredentialFile = filepath.Join(t.TempDir(), "missing", "keys.json")
	t.Cleanup(func() { cohereCredentialFile = previous })
}

// pinCredentialFile writes an operator store holding the given content and
// points the fallback at it.
func pinCredentialFile(t *testing.T, content string) {
	t.Helper()
	previous := cohereCredentialFile
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cohereCredentialFile = path
	t.Cleanup(func() { cohereCredentialFile = previous })
}

func TestCohereCredentialPrefersTheEnvironment(t *testing.T) {
	t.Setenv(cohereCredentialEnv, " env-key ")
	pinCredentialFile(t, `{"cohere": "file-key"}`)
	if got := cohereCredential(); got != "env-key" {
		t.Errorf("cohereCredential() = %q, want the trimmed exported value over the file's", got)
	}
}

func TestCohereCredentialFallsBackToTheFile(t *testing.T) {
	t.Setenv(cohereCredentialEnv, "")
	pinCredentialFile(t, `{"_comment": "operator store", "cohere": " file-key "}`)
	if got := cohereCredential(); got != "file-key" {
		t.Errorf("cohereCredential() = %q, want the trimmed cohere field of the store", got)
	}
}

func TestCohereCredentialIsEmptyWhenFileIsMissing(t *testing.T) {
	t.Setenv(cohereCredentialEnv, "")
	pinAbsentCredentialFile(t)
	if got := cohereCredential(); got != "" {
		t.Errorf("cohereCredential() = %q with no variable and no store, want empty", got)
	}
}

func TestCohereCredentialIsEmptyOnAMalformedStore(t *testing.T) {
	t.Setenv(cohereCredentialEnv, "")
	pinCredentialFile(t, "cohere: not-json\n")
	if got := cohereCredential(); got != "" {
		t.Errorf("cohereCredential() = %q from a store that does not parse, want empty", got)
	}
}

// The credential-only skip exists so the shape probe is not gated on Chroma or
// Ollama, which it does not use. Sharing the clause with cohereSkipReason keeps
// the two from drifting into different messages for the same missing variable.
func TestCohereCredentialSkipReasonNamesTheMissingCredential(t *testing.T) {
	t.Setenv(cohereCredentialEnv, "")
	pinAbsentCredentialFile(t)
	reason := cohereCredentialSkipReason()
	if !strings.Contains(reason, cohereCredentialEnv) {
		t.Errorf("skip reason %q does not name %s", reason, cohereCredentialEnv)
	}
}

func TestCohereCredentialSkipReasonIsEmptyWhenSet(t *testing.T) {
	t.Setenv(cohereCredentialEnv, "a-value")
	if reason := cohereCredentialSkipReason(); reason != "" {
		t.Errorf("skip reason is %q with a credential set, want empty", reason)
	}
}

func TestCohereSkipReasonDelegatesTheCredentialClause(t *testing.T) {
	t.Setenv(cohereCredentialEnv, "   ")
	pinAbsentCredentialFile(t)
	if cohereSkipReason("..") != cohereCredentialSkipReason() {
		t.Error("the mesh skip and the credential skip report different reasons for the same unset variable")
	}
}

func TestEnvOrDefaultPrefersTheOverride(t *testing.T) {
	t.Setenv("COHERE_EMBED_MODEL", " embed-english-v3.0 ")
	if got := envOrDefault("COHERE_EMBED_MODEL", cohereEmbedDefaultModel); got != "embed-english-v3.0" {
		t.Errorf("envOrDefault returned %q, want the trimmed override", got)
	}
}

func TestEnvOrDefaultFallsBackOnBlank(t *testing.T) {
	t.Setenv("COHERE_EMBED_MODEL", "   ")
	if got := envOrDefault("COHERE_EMBED_MODEL", cohereEmbedDefaultModel); got != cohereEmbedDefaultModel {
		t.Errorf("envOrDefault returned %q for a blank override, want the default", got)
	}
}

// The query side embeds as search_query. The document side embeds as
// search_document, and #103 asserts the two differ once that declaration
// exists. Pinning the query value here is what gives that assertion something
// stable to compare against.
func TestCohereEmbedInputTypeIsTheQuerySide(t *testing.T) {
	if cohereEmbedInputType != "search_query" {
		t.Errorf("query input type is %q, want search_query", cohereEmbedInputType)
	}
}

// The declaration assertions below read the shipped profile. They need no
// credential and no network, so they run on a machine that cannot reach the
// provider at all -- a profile that stopped pinning its method, path, or
// authority is a finding worth reporting there.

func TestCohereEmbedOperationIsPinned(t *testing.T) {
	client, err := cohereQueryEmbedderDeclaration(coreRootForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := assertCohereEmbedOperationPinned(client); err != nil {
		t.Fatalf("pinned Cohere library: %v", err)
	}
}

// The query embedding comes from the provider library the providers root binds
// (srd058 R4.2), not a hand-written client: the chatbot's embed-rest.yaml
// instantiates it, both chatbot profiles load that file, and neither rest.yaml
// declares a query embedding of its own.
func TestCohereEmbedOperationComesFromTheProvidersLibrary(t *testing.T) {
	var embed struct {
		Instantiate []struct {
			Fragment string `yaml:"fragment"`
		} `yaml:"expand"`
	}
	if err := readIntegrationYAML("../agents/chatbot/embed-rest.yaml", "chatbot embed asset", &embed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, instance := range embed.Instantiate {
		found = found || instance.Fragment == "/opt/providers/embed-query-fragment.yaml"
	}
	if !found {
		t.Error("embed-rest.yaml does not instantiate the providers root's embed-query fragment")
	}
	for _, profile := range []string{"profile.yaml", "request-profile.yaml"} {
		if !strings.Contains(readAgentFile(t, "chatbot", profile), "  - embed-rest.yaml\n") {
			t.Errorf("%s does not load embed-rest.yaml; its embed word would name an operation it cannot resolve", profile)
		}
	}
	client, err := cohereClientDeclaration("..")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := client.Operations["embed_query_cohere"]; ok {
		t.Error("rest.yaml still hand-writes embed_query_cohere beside the library operation")
	}
}

func TestCohereEmbedOperationRejectsARuntimeAuthority(t *testing.T) {
	client := restClientDeclaration{Operations: map[string]restOperationDeclaration{
		cohereEmbedOperation: {
			Method: "POST", Path: "/v2/embed", BaseURLSource: "command_state",
			Failures: []struct {
				Status []int  `yaml:"status"`
				Signal string `yaml:"signal"`
			}{{Status: []int{401}, Signal: "ProviderUnauthorized"}, {Status: []int{429}, Signal: "ProviderThrottled"}},
		},
	}}
	err := assertCohereEmbedOperationPinned(client)
	if err == nil {
		t.Fatal("a runtime-supplied base URL was accepted; srd009 R1.5 forbids it")
	}
	if !strings.Contains(err.Error(), "base_url_source") {
		t.Errorf("error %q does not name the runtime authority", err)
	}
}

func TestCohereEmbedOperationRejectsOneFailureSignal(t *testing.T) {
	client := restClientDeclaration{Operations: map[string]restOperationDeclaration{
		cohereEmbedOperation: {
			Method: "POST", Path: "/v2/embed",
			Failures: []struct {
				Status []int  `yaml:"status"`
				Signal string `yaml:"signal"`
			}{{Status: []int{401}, Signal: "CommandError"}, {Status: []int{429}, Signal: "CommandError"}},
		},
	}}
	err := assertCohereEmbedOperationPinned(client)
	if err == nil {
		t.Fatal("one signal for every failure was accepted; an operator cannot tell which occurred (srd009 R1.6)")
	}
	if !strings.Contains(err.Error(), "not separable") {
		t.Errorf("error %q does not name the collapsed signals", err)
	}
}

// The model must reach the request as an environment reference. A resolved
// literal here would pin every deployment to one model identity, and srd002
// R3.3 compares that identity against what each collection was seeded with.
func TestCohereEmbedModelIsAnEnvironmentReference(t *testing.T) {
	var embed struct {
		Instantiate []struct {
			Args struct {
				Model string `yaml:"model"`
			} `yaml:"args"`
		} `yaml:"expand"`
	}
	if err := readIntegrationYAML("../agents/chatbot/embed-rest.yaml", "chatbot embed asset", &embed); err != nil {
		t.Fatal(err)
	}
	if len(embed.Instantiate) == 0 || !strings.Contains(embed.Instantiate[0].Args.Model, "COHERE_EMBED_MODEL") {
		t.Errorf("embed model is %+v; srd009 R1.5 requires a declared environment reference", embed.Instantiate)
	}
}

// The query side embeds as search_query. #103 adds the corpus-ingest document
// side and the assertion that the two differ; until that declaration exists
// there is no document side to compare against, so this pins the query value
// so that comparison has a stable anchor.
func TestCohereEmbedDeclaresTheQueryInputType(t *testing.T) {
	body, err := cohereEmbedBodyDeclaration(coreRootForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := body["input_type"].(string); got != cohereEmbedInputType {
		t.Errorf("embed input_type is %q, want %q", got, cohereEmbedInputType)
	}
}

func TestCohereEmbedRequestsFloatEmbeddings(t *testing.T) {
	body, err := cohereEmbedBodyDeclaration(coreRootForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	types, ok := body["embedding_types"].([]interface{})
	if !ok || len(types) == 0 {
		t.Fatalf("embedding_types is %v; v2 requires the type list and float is what the vector search consumes", body["embedding_types"])
	}
	if first, _ := types[0].(string); first != "float" {
		t.Errorf("embedding_types[0] is %q, want float", first)
	}
}

// The Ollama embedding path stays declared and selectable after the Cohere
// operation is added (srd009 R2.3). The repoint is a configuration choice, and
// a deployment with no local embedding has no answer to a provider outage.
func TestOllamaEmbeddingPathRemainsDeclared(t *testing.T) {
	// The chart rebinds the providers root to the Ollama library with the
	// provider off (TestChatbotRebindsProvidersToOllamaOnlyWithCohereOff), so
	// the path stays declared as long as that library ships the fragment.
	fragment := filepath.Join(coreRootForTest(t), "tools", "providers", "ollama", "embed-query-fragment.yaml")
	if _, err := os.Stat(fragment); err != nil {
		t.Fatalf("the pinned Ollama library ships no embed-query fragment; srd009 R2.3 keeps the Ollama path selectable: %v", err)
	}
}

// The corpus side embeds as search_document while the query side embeds as
// search_query. The provider's retrieval models distinguish the two, and
// embedding both alike is the one failure in this lane that no assertion can
// see at runtime: both calls return 200, both return well-formed vectors of the
// same dimension, and only a labeled-tasking recall measurement (srd014, and
// rel13.0) shows the loss. These checks are structural and weaker than a test,
// which is why they are worth having and worth labelling as weak.

func TestCohereEmbedInputTypesDiffer(t *testing.T) {
	query, document, err := cohereEmbedInputTypes(coreRootForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	if query == document {
		t.Errorf("the query and corpus sides both embed as %q; the provider distinguishes a"+
			" query from a document, and embedding both alike degrades recall without"+
			" failing anything (srd009 R2.2)", query)
	}
}

func TestCohereEmbedInputTypesAreTheDeclaredPair(t *testing.T) {
	query, document, err := cohereEmbedInputTypes(coreRootForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	if query != cohereEmbedInputType {
		t.Errorf("query input_type is %q, want %q", query, cohereEmbedInputType)
	}
	if document != cohereEmbedDocumentInputType {
		t.Errorf("corpus input_type is %q, want %q", document, cohereEmbedDocumentInputType)
	}
}

// The corpus child embeds through the Cohere library's own operation (srd058
// R4.2): the embed-rest.yaml it loads instantiates whichever library its
// embedder root binds, and the Cohere one has to be the pinned endpoint with the
// credential by reference. Both sides naming one embedding family is the
// chart's job, which TestRenderedEmbeddingIdentitiesAgree holds.
func TestCohereEmbedDocumentOperationIsPinned(t *testing.T) {
	var embed struct {
		Instantiate []struct {
			Fragment string `yaml:"fragment"`
		} `yaml:"expand"`
	}
	if err := readIntegrationYAML("../"+corpusEmbedAsset, "corpus embed asset", &embed); err != nil {
		t.Fatal(err)
	}
	if len(embed.Instantiate) != 1 || embed.Instantiate[0].Fragment != "/opt/embedder/embed-document-fragment.yaml" {
		t.Fatalf("embed-rest.yaml instantiates %+v, want the embedder root's embed-document fragment", embed.Instantiate)
	}

	var cfg struct {
		Rest struct {
			Auth map[string]struct {
				TokenRef string `yaml:"token_ref"`
			} `yaml:"auth"`
			Clients map[string]restClientDeclaration `yaml:"clients"`
		} `yaml:"rest"`
	}
	path := cohereDocumentFragmentPath(coreRootForTest(t))
	if err := readIntegrationYAML(path, "cohere embed-document fragment", &cfg); err != nil {
		t.Fatal(err)
	}
	client, ok := cfg.Rest.Clients[cohereDocumentEmbedClient]
	if !ok {
		t.Fatalf("%s declares no %s client (srd009 R2.2)", path, cohereDocumentEmbedClient)
	}
	if ref := cfg.Rest.Auth[client.AuthRef].TokenRef; ref != "COHERE_API_KEY" {
		t.Errorf("the Cohere document client resolves its credential through %q, want token_ref COHERE_API_KEY (srd009 R1.3)", ref)
	}
	operation, ok := client.Operations[cohereDocumentEmbedOperation]
	if !ok {
		t.Fatalf("%s declares no %s operation", path, cohereDocumentEmbedOperation)
	}
	if operation.Method != "POST" || operation.Path != "/v2/embed" {
		t.Errorf("corpus %s is %s %s, want POST /v2/embed (srd009 R1.1, R1.5)",
			cohereDocumentEmbedOperation, operation.Method, operation.Path)
	}
}

// The Ollama ingest path stays the profile's own binding, the corpus-side
// counterpart of srd009 R2.3. An ingest that cannot fall back to a local
// embedding has no answer to a provider outage mid-seed.
func TestOllamaIngestPathRemainsDeclared(t *testing.T) {
	var profile struct {
		Libraries map[string]string `yaml:"libraries"`
	}
	if err := readIntegrationYAML("../"+chromaIngestProfile, "corpus-ingest profile", &profile); err != nil {
		t.Fatal(err)
	}
	if got := profile.Libraries["embedder"]; got != "/opt/agent-core/tools/providers/ollama" {
		t.Errorf("the corpus-ingest embedder root binds %q, want the Ollama library", got)
	}
	fragment := filepath.Join(coreRootForTest(t), "tools", "providers", "ollama", "embed-document-fragment.yaml")
	if _, err := os.Stat(fragment); err != nil {
		t.Errorf("the pinned Ollama library ships no embed-document fragment: %v", err)
	}
}

// No corpus-ingest file carries a credential. The provider library holds the
// reference (srd009 R1.3), and a literal in this application's profile is the
// failure worth checking mechanically rather than by review.
func TestCorpusCohereCredentialIsByReferenceOnly(t *testing.T) {
	for _, name := range []string{"corpus-rest.yaml", "embed-rest.yaml", "profile.yaml"} {
		raw, err := os.ReadFile(filepath.Join("..", "agents", "corpus-ingest", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "token:") || strings.HasPrefix(trimmed, "api_key:") {
				t.Errorf("%s carries a credential value at %q; the profile holds"+
					" the reference and nothing else (srd009 R1.3)", name, trimmed)
			}
		}
	}
}

// The scenario critic drives one refusal status. The operation maps three, each
// to its own signal (srd009 R1.6), and a signal with no transition out of
// Embedding leaves the turn on the generic error path -- the outcome the
// distinct mappings exist to avoid. This is a static assertion over the
// declaration and is weaker than the scenario: it proves the transitions are
// declared, not that the runtime takes them. Only cohere-embed-refused proves
// that, and only for ProviderThrottled.
func TestCohereEmbedRefusalsReachTheMachine(t *testing.T) {
	var machine intakeMachine
	readIntakeYAML(t, filepath.Join(agentDir(t, "chatbot"), "request-machine.yaml"), &machine)

	declared := map[string]string{}
	for _, tr := range machine.Transitions {
		if tr.State == "Embedding" {
			declared[tr.Signal] = tr.Next
		}
	}
	for _, signal := range []string{"ProviderUnauthorized", "ProviderThrottled", "ProviderUnavailable"} {
		next, ok := declared[signal]
		if !ok {
			t.Errorf("no transition for %s out of Embedding; a mapped refusal would fall through to the generic error path", signal)
			continue
		}
		if next != "Failed" {
			t.Errorf("%s out of Embedding goes to %q, want Failed; a refusal must stop the turn rather than continue into the fan-out", signal, next)
		}
	}
}

// TestCohereCredentialResolvesTheMainCheckoutFromAWorktree is GH-318: the
// operator store lives in the main checkout only, so a run from a linked
// worktree must resolve it through the shared git common dir rather than
// silently reading no credential and downgrading the deployed tier.
func TestCohereCredentialResolvesTheMainCheckoutFromAWorktree(t *testing.T) {
	t.Setenv(cohereCredentialEnv, "")
	main := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = dir
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run(main, "init", "-b", "main")
	run(main, "commit", "--allow-empty", "-m", "seed")
	if err := os.MkdirAll(filepath.Join(main, ".secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(main, ".secrets", "keys.json"),
		[]byte(`{"cohere": "worktree-resolved-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(t.TempDir(), "wt")
	run(main, "worktree", "add", worktree, "-b", "wt-branch")
	t.Chdir(worktree)
	if got := cohereCredential(); got != "worktree-resolved-key" {
		t.Errorf("cohereCredential() from a worktree = %q, want the main checkout's store", got)
	}
}

// TestCohereCredentialFallbackStaysDeadWhenRepointed pins the absence-test
// property the GH-318 fallback must not break: a test that repoints
// cohereCredentialFile at an impossible path resolves no credential even in
// a checkout whose real store holds a key, because the main-checkout retry
// runs only while the variable holds its default.
func TestCohereCredentialFallbackStaysDeadWhenRepointed(t *testing.T) {
	t.Setenv(cohereCredentialEnv, "")
	pinAbsentCredentialFile(t)
	if got := cohereCredential(); got != "" {
		t.Errorf("cohereCredential() = %q, want empty when the file is repointed at an absent path", got)
	}
}
