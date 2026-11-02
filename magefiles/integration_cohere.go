// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	// cohereCredentialEnv is the environment channel a Cohere credential takes
	// into the mesh, and the override when the cohereCredentialFile operator
	// store also holds one. The value is operator-supplied and read from the
	// environment by the runtime's credential resolver; nothing tracked in the
	// repository holds it.
	cohereCredentialEnv = "COHERE_API_KEY"

	// cohereTasking routes to the Cohere word through the shipped tier prompt,
	// which sends a request for a brief or an assessment there.
	cohereTasking = "Prepare a command-staff brief on what the corpus describes."

	// cohereUnroutableBase is outside the configured allowlist host, and fails
	// instantly if anything ever dials it. A name that does not resolve would
	// also be outside the allowlist, but a client that failed to refuse it would
	// then sit in DNS resolution until the operation timeout, which reads as a
	// hung shutdown rather than as the policy finding it is.
	cohereUnroutableBase = "http://127.0.0.1:1"

	// cohereRejectedCredential is well-formed and wrong, so the provider answers
	// with an authentication failure rather than the client refusing to build a
	// request. That is the difference the R1.6 outcome check depends on.
	cohereRejectedCredential = "not-a-valid-cohere-key"
)

// Cohere proves the Cohere Command composition tier against the real provider
// (srd009 R1, R3; rel10.0-uc001). The scenario critics in
// agents/chatbot/tests/cohere-command and cohere-fallback are evidence about
// the request the machine constructs and the signals it routes on; they cannot
// be evidence that the API behaves as the declarations assume, because a mock
// returns whatever shape its fixture declares. This target is where that gap
// closes, the way integration:applierLive closes it for the applier.
//
// It proves four things a mock cannot:
//
//   - a tasking routed to the Cohere tier is composed by Command over the
//     retrieved documents, and the turn's trace names invoke_cohere_command as the
//     word that answered rather than an Ollama word that would return an
//     equally fluent answer;
//   - a credential the provider rejects surfaces as a distinguishable outcome
//     and stops the turn at the embed, which is where one credential covering
//     the whole client now refuses first (srd009 R2, and GH-127 for why this
//     scenario cannot also prove the R3.4 composition fallback);
//   - a base URL outside the configured host is refused by the client before a
//     request leaves, rather than by the machine that invoked it;
//   - the credential appears in no output the target produces.
//
// It skips (does not fail) when the credential, Docker, or Ollama with the
// configured models is unavailable, naming what is missing, matching
// Integration.RagServer and Integration.Chatbot. integration:all therefore stays
// green on a machine with no Cohere access.
func (Integration) Cohere() error {
	applicationRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	coreRoot := demoCoreRoot(applicationRoot)
	if !agentCoreAvailable(coreRoot) {
		fmt.Printf("SKIP integration:cohere: agent-core checkout not found at %s\n", coreRoot)
		return nil
	}

	// The declaration checks read the shipped profile and need no credential,
	// no network, and no cluster, so they run before any skip: a profile that
	// stopped pinning its method, path, auth, or egress is a finding worth
	// reporting on a machine that cannot reach the provider at all.
	if err := assertCohereClientPinned(applicationRoot); err != nil {
		return err
	}

	// The embed shape probe needs the credential and nothing else -- no Chroma,
	// no Ollama, no rag tier -- so it runs on its own skip rather than behind the
	// mesh's. A dependency an assertion does not use should not decide whether
	// that assertion runs, and this one establishes the response shape rel10.3's
	// declaration is written against (srd009 R2.1).
	if reason := cohereCredentialSkipReason(); reason != "" {
		fmt.Printf("SKIP integration:cohere embed shape: %s\n", reason)
	} else if _, err := assertCohereEmbedShape(); err != nil {
		return fmt.Errorf("embed shape: %w", err)
	}

	if reason := cohereSkipReason(applicationRoot); reason != "" {
		fmt.Printf("SKIP integration:cohere: %s\n", reason)
		return nil
	}
	return runCohereIntegration(applicationRoot, coreRoot)
}

// cohereCredentialSkipReason reports why a credential-only assertion cannot run,
// or "" when it can. It is the first clause of cohereSkipReason, named
// separately so an assertion needing no mesh is not skipped by a missing Chroma.
func cohereCredentialSkipReason() string {
	if cohereCredential() == "" {
		return cohereCredentialEnv + " is not set and " + cohereCredentialFile +
			" holds no cohere key; supply the operator credential through either to run the live tier"
	}
	return ""
}

// cohereSkipReason reports why the live tier cannot run, or "" when it can. The
// credential is named explicitly: an operator who exported nothing should read
// which variable to set, not that "a dependency" was missing.
func cohereSkipReason(profilesRoot string) string {
	if reason := cohereCredentialSkipReason(); reason != "" {
		return reason
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return "docker not found on PATH, and the mesh needs a Chroma container"
	}
	return chatbotOllamaSkipReason(profilesRoot)
}

// runCohereIntegration stands the mesh up once and drives the chatbot three
// times against it, restarting only the chatbot between runs. The rag-servers
// and their Chroma are the expensive part and are identical across all three,
// so rebuilding them per case would triple the runtime and prove nothing.
func runCohereIntegration(profilesRoot, coreRoot string) error {
	binary, cleanupAgent, err := buildAgent(coreRoot)
	if err != nil {
		return err
	}
	defer cleanupAgent()
	dataDir, err := os.MkdirTemp("", "cohere-mesh-data-*")
	if err != nil {
		return fmt.Errorf("create chroma data dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dataDir) }()
	containerID, err := startRequiredChromaContainer(dataDir, ensureChromaServer)
	if err != nil {
		return fmt.Errorf("cohere dependency startup: %w", err)
	}
	defer stopChromaContainer(containerID)

	// Both corpora are embedded at Cohere, because the chatbot under test embeds
	// its query there. rel10.3 repointed the query embedding, and srd002 R3.3
	// excludes any source whose seeded identity differs from the query vector's,
	// so an Ollama-seeded corpus answers this target's live composition from
	// nothing while returning 200 and fluent prose. integration:chatbot moved to
	// the Cohere seeder for the same reason (GH-107); this target did not, and
	// its live composition had been failing behind an earlier scenario (GH-127).
	//
	// This target seeds inline rather than through the corpus-ingest agent: it
	// writes a disjoint in-memory corpus alongside the fixture (below), which is
	// not a corpus directory the agent ingests, and the inline write keeps the
	// two collections deterministic without standing up the agent's Cohere
	// ingest. app:seed does use the agent now that upstream normalizes the
	// provider row (pre-move upstream declarative-agents#1863, GH-135), and
	// integration:chroma proves that agent's own ingest path.
	embedModel := envOrDefault("COHERE_EMBED_MODEL", cohereEmbedDefaultModel)
	// One identity for the whole mesh, set once rather than passed to each
	// child. The chatbot reads it for the embed request and for
	// declare_query_model; each rag-server reports it as the identity its
	// collection was seeded with. srd002 R3.3 compares the two, so they agree
	// here or every source is excluded from every turn.
	// The process environment is the channel because this identity has to
	// reach two kinds of reader at once: the detached agent children started
	// below, and the in-process seeding this target does itself. Passing it
	// on each child command would leave the in-process half reading a
	// different value, which is the divergence srd002 R3.3 turns into an
	// empty answer.
	restoreEnv, err := setTargetEnv(map[string]string{"COHERE_EMBED_MODEL": embedModel, "RAG_EMBEDDING_MODEL": embedModel})
	if err != nil {
		return err
	}
	defer restoreEnv()
	if err := seedCohereCorpusFixture(profilesRoot, embedModel, "corpus"); err != nil {
		return fmt.Errorf("seed corpus fixture at Cohere: %w", err)
	}
	if err := seedCohereDisjointCorpus(embedModel); err != nil {
		return fmt.Errorf("seed disjoint corpus at Cohere: %w", err)
	}
	if err := seedFixtureDocumentCollection(profilesRoot); err != nil {
		return fmt.Errorf("seed the fixture document collection: %w", err)
	}

	rag1Profile, rag1Cleanup, err := generateRag1Variant(profilesRoot)
	if err != nil {
		return err
	}
	defer rag1Cleanup()

	stopRags, err := startCohereRagTier(binary, profilesRoot, coreRoot, rag1Profile)
	if err != nil {
		return err
	}
	defer stopRags()

	// Ordered cheapest-signal-first: a client that will not refuse an
	// unroutable host is a policy finding, and there is no reason to spend a
	// live composition to learn it.
	if err := assertCohereEgressRefused(binary, profilesRoot, coreRoot); err != nil {
		return fmt.Errorf("egress allowlist: %w", err)
	}
	if err := assertCohereRejectedCredentialStopsTheTurn(binary, profilesRoot, coreRoot); err != nil {
		return fmt.Errorf("rejected credential: %w", err)
	}
	if err := assertCohereLiveComposition(binary, profilesRoot, coreRoot); err != nil {
		return fmt.Errorf("live composition: %w", err)
	}
	if err := assertCohereRevisionRegroundsCarriedClaims(binary, profilesRoot, coreRoot); err != nil {
		return fmt.Errorf("revision re-grounding: %w", err)
	}
	fmt.Println("integration:cohere PASS - the mesh composed a brief with Cohere Command, re-grounded a revision on the records its draft cited, stopped the turn on a rejected credential, and refused an unallowlisted host")
	return nil
}

// startCohereRagTier brings both rag-servers up and returns one stop for the
// pair, so the chatbot can be restarted around them.
func startCohereRagTier(binary, profilesRoot, coreRoot, rag1Profile string) (func(), error) {
	rag0Trace, rag0Cleanup, err := chromaTraceFile("cohere-rag0")
	if err != nil {
		return nil, err
	}
	stopRag0, err := startDetachedAgent(binary, profilesRoot, coreRoot, ragServerProfile, rag0Trace)
	if err != nil {
		rag0Cleanup()
		return nil, err
	}
	if err := waitHTTPStatus(ragControlHealth, http.StatusOK, 30*time.Second); err != nil {
		_ = stopRag0(true)
		rag0Cleanup()
		return nil, fmt.Errorf("rag0 control health never came up: %w", err)
	}

	rag1Trace, rag1Cleanup, err := chromaTraceFile("cohere-rag1")
	if err != nil {
		_ = stopRag0(true)
		rag0Cleanup()
		return nil, err
	}
	stopRag1, err := startDetachedAgent(binary, profilesRoot, coreRoot, rag1Profile, rag1Trace)
	if err != nil {
		_ = stopRag0(true)
		rag0Cleanup()
		rag1Cleanup()
		return nil, err
	}
	if err := waitHTTPStatus(rag1ControlHealth, http.StatusOK, 30*time.Second); err != nil {
		_ = stopRag1(true)
		_ = stopRag0(true)
		rag0Cleanup()
		rag1Cleanup()
		return nil, fmt.Errorf("rag1 control health never came up: %w", err)
	}
	return func() {
		_ = stopRag1(true)
		_ = stopRag0(true)
		rag0Cleanup()
		rag1Cleanup()
	}, nil
}

// withCohereChatbot starts the chatbot with the given Cohere environment, runs
// the body, then stops it and hands the body's caller the exported trace. The
// trace is read after exit because request-scoped spans flush on shutdown.
func withCohereChatbot(
	binary, profilesRoot, coreRoot, label string,
	env []string,
	body func() error,
) (string, func(), error) {
	trace, cleanup, err := chromaTraceFile(label)
	if err != nil {
		return "", nil, err
	}
	stop, err := startDetachedAgentWithEnv(agentLaunch{
		Binary: binary, ProfilesRoot: profilesRoot, CoreRoot: coreRoot,
		Profile: chatbotProfile, TracePath: trace, Env: env,
		GracefulWait: 45 * time.Second,
	})
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if err := waitHTTPStatus(chatbotControlHealth, http.StatusOK, 30*time.Second); err != nil {
		_ = stop(true)
		cleanup()
		return "", nil, fmt.Errorf("chatbot control health never came up: %w", err)
	}
	bodyErr := body()
	// The agent exits on request, and stop(false) only waits for it to go. Ask
	// first, or the wait times out and the process is killed with its spans
	// unflushed -- and an empty trace reads exactly like a turn that never ran
	// the word under test, which is the one failure these assertions must never
	// confuse with a real one.
	if _, status, err := requestHTTP(http.MethodPost, chatbotControlExit,
		`{"reason":"cohere integration case complete"}`); err != nil || status/100 != 2 {
		_ = stop(true)
		cleanup()
		return "", nil, fmt.Errorf("ask chatbot to exit: status %d: %v", status, err)
	}
	stopErr := stop(false)
	if bodyErr != nil {
		cleanup()
		return "", nil, bodyErr
	}
	if stopErr != nil {
		cleanup()
		return "", nil, fmt.Errorf("chatbot did not stop gracefully, so its trace was not flushed: %w", stopErr)
	}
	return trace, cleanup, nil
}

// assertCohereClientPinned reads the shipped profile and confirms the transport
// a security reviewer asks about is fixed declared configuration (srd009 R1.1,
// R1.3, R1.4, R1.5). It needs no credential and no network, so it runs even
// where the live tier skips.
func assertCohereClientPinned(profilesRoot string) error {
	client, err := cohereClientDeclaration(profilesRoot)
	if err != nil {
		return err
	}
	if client.AuthRef != "cohere_bearer" {
		return fmt.Errorf("cohere client auth_ref is %q, want cohere_bearer (srd009 R1.3)", client.AuthRef)
	}
	operation, ok := client.Operations["compose_brief"]
	if !ok {
		return fmt.Errorf("cohere client declares no compose_brief operation (srd009 R1.1)")
	}
	if operation.Method != "POST" || operation.Path != "/v2/chat" {
		return fmt.Errorf("compose_brief is %s %s, want POST /v2/chat (srd009 R1.1, R1.5)",
			operation.Method, operation.Path)
	}
	embedder, err := cohereQueryEmbedderDeclaration(demoCoreRoot(profilesRoot))
	if err != nil {
		return err
	}
	if err := assertCohereEmbedOperationPinned(embedder); err != nil {
		return err
	}
	// A runtime-supplied authority is exactly what R1.5 forbids. The rag client
	// declares base_url_source because its authority comes from the trusted
	// topology; this client must not, or a request could be redirected to a host
	// the operator never configured.
	if operation.BaseURLSource != "" {
		return fmt.Errorf("compose_brief declares base_url_source %q; the Cohere authority is fixed configuration (srd009 R1.5)",
			operation.BaseURLSource)
	}
	if err := assertCohereFailureSignalsDistinct(operation); err != nil {
		return err
	}
	return assertCohereCredentialByReferenceOnly(profilesRoot)
}

// assertCohereFailureSignalsDistinct confirms a provider failure is separable
// from an empty result and from another provider failure (srd009 R1.6). One
// signal for every status would satisfy "handled" while telling an operator
// reading a trace nothing about which failure occurred.
func assertCohereFailureSignalsDistinct(operation restOperationDeclaration) error {
	if len(operation.Failures) == 0 {
		return fmt.Errorf("compose_brief maps no failure statuses; a provider failure would be indistinguishable from a transport fault (srd009 R1.6)")
	}
	bySignal := map[string]bool{}
	for _, failure := range operation.Failures {
		if failure.Signal == "" {
			return fmt.Errorf("compose_brief maps statuses %v to an empty signal (srd009 R1.6)", failure.Status)
		}
		bySignal[failure.Signal] = true
	}
	if len(bySignal) < 2 {
		return fmt.Errorf("compose_brief maps every failure status to one signal %v; authentication, rate limit, and outage are not separable (srd009 R1.6)",
			sortedModelKeys(bySignal))
	}
	return nil
}

// assertCohereCredentialByReferenceOnly confirms the credential reaches the
// request through token_ref alone. A value in a profile is the failure this
// guards, and it is worth checking mechanically rather than by review: a
// credential pasted into YAML during debugging looks exactly like configuration.
func assertCohereCredentialByReferenceOnly(profilesRoot string) error {
	paths, err := filepath.Glob(filepath.Join(profilesRoot, "agents", "chatbot", "*.yaml"))
	if err != nil {
		return err
	}
	extra, err := filepath.Glob(filepath.Join(profilesRoot, "agents", "chatbot", "tests", "*", "*.yaml"))
	if err != nil {
		return err
	}
	for _, path := range append(paths, extra...) {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, cohereCredentialEnv) {
				continue
			}
			trimmed := strings.TrimSpace(line)
			// A reference names the variable; a scenario supplies a literal test
			// value through its own env block. Neither carries a real credential,
			// and any other shape is one worth looking at.
			if strings.HasPrefix(trimmed, "token_ref:") ||
				strings.HasPrefix(trimmed, "- "+cohereCredentialEnv+"=") ||
				strings.HasPrefix(trimmed, "#") {
				continue
			}
			return fmt.Errorf("%s carries %s outside a token_ref or a scenario env entry: %q (srd009 R1.3)",
				path, cohereCredentialEnv, trimmed)
		}
	}
	return nil
}

// restOperationDeclaration is the slice of a declared REST operation these
// assertions read. It is deliberately partial: the runtime owns the full
// schema, and a struct that mirrored it would fail to decode every time
// agent-core added a field.
// assertCohereEmbedOperationPinned confirms the embeddings operation is fixed
// declared configuration on the same client the chat operation uses (srd009
// R1.1, R1.5, R1.6, R2.1). Sharing the client is the point: a second client
// would be a second egress surface, a second credential reference, and a second
// base URL for a reviewer to audit and a deployment to keep in step.
func assertCohereEmbedOperationPinned(client restClientDeclaration) error {
	operation, ok := client.Operations[cohereEmbedOperation]
	if !ok {
		return fmt.Errorf("cohere client declares no %s operation (srd009 R1.1, R2.1)", cohereEmbedOperation)
	}
	if operation.Method != "POST" || operation.Path != "/v2/embed" {
		return fmt.Errorf("cohere %s is %s %s, want POST /v2/embed (srd009 R1.1, R1.5)",
			cohereEmbedOperation, operation.Method, operation.Path)
	}
	// The rag client declares base_url_source because its authority comes from
	// the trusted topology. This one must not: a runtime-supplied authority on a
	// call carrying ministry data is exactly what R1.5 forbids.
	if operation.BaseURLSource != "" {
		return fmt.Errorf("cohere %s declares base_url_source %q; the Cohere authority is fixed configuration (srd009 R1.5)",
			cohereEmbedOperation, operation.BaseURLSource)
	}
	if len(operation.Failures) == 0 {
		return fmt.Errorf("cohere %s maps no failure statuses; an embedding failure would be indistinguishable from an empty retrieval (srd009 R1.6)", cohereEmbedOperation)
	}
	bySignal := map[string]bool{}
	for _, failure := range operation.Failures {
		if failure.Signal == "" {
			return fmt.Errorf("cohere %s maps statuses %v to an empty signal (srd009 R1.6)", cohereEmbedOperation, failure.Status)
		}
		bySignal[failure.Signal] = true
	}
	if len(bySignal) < 2 {
		return fmt.Errorf("cohere %s maps every failure status to one signal %v; authentication, rate limit, and outage are not separable (srd009 R1.6)",
			cohereEmbedOperation, sortedModelKeys(bySignal))
	}
	return nil
}

// cohereQueryEmbedderDeclaration reads the Cohere provider library's query
// embedding client (srd058 R4.2), the operation the chatbot's embed-rest.yaml
// instantiates from its providers root, unexpanded.
func cohereQueryEmbedderDeclaration(coreRoot string) (restClientDeclaration, error) {
	var cfg struct {
		Rest struct {
			Clients map[string]restClientDeclaration `yaml:"clients"`
		} `yaml:"rest"`
	}
	path := cohereQueryFragmentPath(coreRoot)
	if err := readIntegrationYAML(path, "cohere embed-query fragment", &cfg); err != nil {
		return restClientDeclaration{}, err
	}
	client, ok := cfg.Rest.Clients[cohereQueryEmbedClient]
	if !ok {
		return restClientDeclaration{}, fmt.Errorf("%s declares no %s client (srd009 R2.1)", path, cohereQueryEmbedClient)
	}
	return client, nil
}

// cohereEmbedBodyDeclaration reads the query-side embed operation's request
// body from the Cohere provider library, unexpanded.
func cohereEmbedBodyDeclaration(coreRoot string) (map[string]interface{}, error) {
	var cfg struct {
		Rest struct {
			Clients map[string]struct {
				Operations map[string]struct {
					Body map[string]interface{} `yaml:"body"`
				} `yaml:"operations"`
			} `yaml:"clients"`
		} `yaml:"rest"`
	}
	path := cohereQueryFragmentPath(coreRoot)
	if err := readIntegrationYAML(path, "cohere embed-query fragment", &cfg); err != nil {
		return nil, err
	}
	operation, ok := cfg.Rest.Clients[cohereQueryEmbedClient].Operations[cohereEmbedOperation]
	if !ok {
		return nil, fmt.Errorf("%s declares no %s %s operation (srd009 R2.1)", path, cohereQueryEmbedClient, cohereEmbedOperation)
	}
	return operation.Body, nil
}

// cohereQueryFragmentPath is the Cohere library's query-embedding fragment
// under an agent-core checkout.
func cohereQueryFragmentPath(coreRoot string) string {
	return filepath.Join(coreRoot, "tools", "providers", "cohere", "embed-query-fragment.yaml")
}

// cohereEmbedInputTypes reads the query-side and document-side embedding input
// types from the Cohere provider library, whose fragments the chatbot and the
// corpus child instantiate (srd009 R2.2, srd058 R4.2). It is a structural read of
// two declarations rather than a behavioural check, and deliberately so: a
// mismatch fails nothing. Both calls return 200, both return well-formed
// vectors of the same dimension, and only a labeled-tasking recall measurement
// (srd014) sees the loss. This is the cheapest check that catches the two
// values being set from one place and being wrong for one of them.
func cohereEmbedInputTypes(coreRoot string) (query, document string, err error) {
	query, err = cohereOperationInputType(
		cohereQueryFragmentPath(coreRoot), cohereQueryEmbedClient, cohereEmbedOperation)
	if err != nil {
		return "", "", err
	}
	document, err = cohereOperationInputType(
		cohereDocumentFragmentPath(coreRoot), cohereDocumentEmbedClient, cohereDocumentEmbedOperation)
	if err != nil {
		return "", "", err
	}
	return query, document, nil
}

// cohereDocumentFragmentPath is the Cohere library's document-embedding
// fragment under an agent-core checkout.
func cohereDocumentFragmentPath(coreRoot string) string {
	return filepath.Join(coreRoot, "tools", "providers", "cohere", "embed-document-fragment.yaml")
}

// cohereOperationInputType reads one Cohere operation's declared input_type,
// unexpanded.
func cohereOperationInputType(path, clientName, operationName string) (string, error) {
	var cfg struct {
		Rest struct {
			Clients map[string]struct {
				Operations map[string]struct {
					Body map[string]interface{} `yaml:"body"`
				} `yaml:"operations"`
			} `yaml:"clients"`
		} `yaml:"rest"`
	}
	if err := readIntegrationYAML(path, "rest asset", &cfg); err != nil {
		return "", err
	}
	client, ok := cfg.Rest.Clients[clientName]
	if !ok {
		return "", fmt.Errorf("%s declares no %s client (srd009 R1.1)", path, clientName)
	}
	operation, ok := client.Operations[operationName]
	if !ok {
		return "", fmt.Errorf("%s declares no %s %s operation (srd009 R2)", path, clientName, operationName)
	}
	inputType, _ := operation.Body["input_type"].(string)
	if inputType == "" {
		return "", fmt.Errorf("%s: cohere %s declares no input_type; the provider's retrieval"+
			" models distinguish a query from a document (srd009 R2.2)", path, operationName)
	}
	return inputType, nil
}

type restOperationDeclaration struct {
	Method        string `yaml:"method"`
	Path          string `yaml:"path"`
	BaseURLSource string `yaml:"base_url_source"`
	Failures      []struct {
		Status []int  `yaml:"status"`
		Signal string `yaml:"signal"`
	} `yaml:"failures"`
}

type restClientDeclaration struct {
	BaseURL    string                              `yaml:"base_url"`
	AuthRef    string                              `yaml:"auth_ref"`
	LimitsRef  string                              `yaml:"limits_ref"`
	Operations map[string]restOperationDeclaration `yaml:"operations"`
}

// cohereClientDeclaration reads the shipped chatbot rest.yaml. It reads the file
// unexpanded, which is why the assertions above check structure rather than
// resolved values: ${COHERE_HOST:-api.cohere.com} is the declaration, and what
// it resolves to is the deployment's business.
func cohereClientDeclaration(profilesRoot string) (restClientDeclaration, error) {
	var cfg struct {
		Rest struct {
			Clients map[string]restClientDeclaration `yaml:"clients"`
		} `yaml:"rest"`
	}
	path := filepath.Join(profilesRoot, "agents", "chatbot", "rest.yaml")
	if err := readIntegrationYAML(path, "chatbot rest asset", &cfg); err != nil {
		return restClientDeclaration{}, err
	}
	client, ok := cfg.Rest.Clients["cohere"]
	if !ok {
		return restClientDeclaration{}, fmt.Errorf("%s declares no cohere client (srd009 R1.1)", path)
	}
	return client, nil
}

// assertCohereEgressRefused points the client at a host outside the configured
// allowlist and confirms the refusal comes from the client rather than from the
// machine that invoked it (srd009 R1.4). The distinction is the requirement: a
// machine that merely failed to reach an unreachable host would look the same
// from outside, and would stop looking the same the day the host answered.
//
// The refusal is read from the turn's own response rather than from a dispatch
// span, because the client rejects the authority before the word runs. There is
// no invoke_cohere_command span to inspect, and that is the correct behaviour: a host
// the operator never configured is refused without a request being built.
func assertCohereEgressRefused(binary, profilesRoot, coreRoot string) error {
	var status int
	var body string
	_, cleanup, err := withCohereChatbot(binary, profilesRoot, coreRoot, "cohere-egress",
		[]string{
			// The allowlist stays at its default while the base URL moves off it,
			// which is exactly the misconfiguration R1.4 exists to catch.
			cohereCredentialEnv + "=" + cohereCredential(),
			"COHERE_BASE_URL=" + cohereUnroutableBase,
		},
		func() error {
			var err error
			status, body, err = postChatTurnRaw(cohereTasking)
			return err
		})
	if err != nil {
		return err
	}
	defer cleanup()

	if status == http.StatusOK {
		return fmt.Errorf("a base URL outside the configured allowlist answered HTTP 200;"+
			" the client allowed %s (srd009 R1.4)", cohereUnroutableBase)
	}
	// The client's network policy names what it rejected. A dial or DNS failure
	// names a syscall instead, and would mean the request left the process
	// before any policy looked at it.
	if !strings.Contains(body, "not allowed") && !strings.Contains(body, "127.0.0.1") {
		return fmt.Errorf("the turn failed with HTTP %d and %q, which does not read as a client allowlist refusal;"+
			" srd009 R1.4 requires the client to refuse the host rather than the machine to fail reaching it",
			status, firstRunes(body, 200))
	}
	if credential := cohereCredential(); credential != "" &&
		strings.Contains(body, credential) {
		return fmt.Errorf("the refusal response carries the credential value (srd009 R1.3)")
	}
	return nil
}

// postChatTurnRaw posts a turn and returns the status and undecoded body. The
// egress case needs it because a refused authority is reported as an error
// document rather than as a chat response, and decoding it would discard the
// text the assertion is about.
func postChatTurnRaw(message string) (int, string, error) {
	payload, err := json.Marshal(map[string]string{"message": message})
	if err != nil {
		return 0, "", err
	}
	response, err := http.Post(chatbotChatURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return 0, "", fmt.Errorf("post chat turn: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, "", fmt.Errorf("read chat response: %w", err)
	}
	return response.StatusCode, string(body), nil
}

// assertCohereRejectedCredentialStopsTheTurn drives a turn whose credential the
// provider rejects, and requires the turn to stop at the embed rather than
// answer.
//
// One credential covers the whole client, so a rejected one refuses every
// operation on it. Since rel10.3 repointed the query embedding to Cohere, the
// first operation a turn reaches is embed_query_cohere, and srd009 R2 stops a
// turn whose tasking could not be embedded: a turn that embedded nothing and a
// turn that retrieved nothing are otherwise identical at the response
// (rel10.3-uc001 S7). Composition is never reached, so this scenario cannot
// also prove the R3.4 composition fallback, and asserting degradation here
// started failing once the repoint landed (GH-127).
//
// What it still proves, and what nothing else proves live: a real provider's
// 401 maps to ProviderUnauthorized rather than to a generic CommandError
// (srd009 R1.6), and the rejected credential reaches no exported span.
//
// The R3.4 fallback keeps its proof in the cohere-fallback scenario, whose mock
// serves /v2/embed a 200 and refuses /v2/chat -- the embed succeeds there, so
// the turn reaches composition and degrades. Splitting the two along the
// operation rather than the credential is what lets each be proven at all.
func assertCohereRejectedCredentialStopsTheTurn(binary, profilesRoot, coreRoot string) error {
	var status int
	var body string
	trace, cleanup, err := withCohereChatbot(binary, profilesRoot, coreRoot, "cohere-rejected",
		[]string{cohereCredentialEnv + "=" + cohereRejectedCredential},
		func() error {
			var err error
			status, body, err = postChatTurnRaw(cohereTasking)
			return err
		})
	if err != nil {
		return err
	}
	defer cleanup()

	if status == http.StatusOK {
		return fmt.Errorf("a rejected credential returned HTTP 200 with %q;"+
			" srd009 R2 requires a tasking that could not be embedded to stop the turn,"+
			" because an answer composed from nothing reads exactly like a grounded one",
			firstRunes(body, 300))
	}
	spans, err := cohereSpans(trace)
	if err != nil {
		return err
	}
	attempted, signal := cohereEmbedOutcome(spans)
	if !attempted {
		return fmt.Errorf("no %s span in the trace; the turn never reached the provider,"+
			" so its failure says nothing about how a rejected credential is reported",
			cohereEmbedWord)
	}
	// The signal is the requirement. A generic CommandError would mean an
	// operator reading the trace cannot tell a rejected credential from a
	// malformed request or an outage.
	if signal != "ProviderUnauthorized" {
		return fmt.Errorf("%s emitted %q for a rejected credential, want ProviderUnauthorized;"+
			" srd009 R1.6 requires an authentication failure to be distinguishable",
			cohereEmbedWord, signal)
	}
	// Nothing downstream of the embed may run. A turn that fanned out on a
	// vector it never obtained would query against whatever command state held,
	// which is the silent emptying rel10.3 exists to prevent.
	if _, composed := cohereDispatchOutcome(spans); composed != "" {
		return fmt.Errorf("invoke_cohere_command emitted %q after the embed was refused;"+
			" the turn continued past a failure that should have stopped it", composed)
	}
	return assertCohereCredentialAbsentFromSpans(spans, cohereRejectedCredential)
}

// cohereEmbedOutcome reports whether the Cohere embed ran and the signal it
// emitted, the embedding-side counterpart of cohereDispatchOutcome.
func cohereEmbedOutcome(spans []cohereSpanDetail) (attempted bool, signal string) {
	for _, span := range spans {
		if span.attr("command.name") != cohereEmbedWord {
			continue
		}
		attempted = true
		if value := span.attr("command.signal"); value != "" {
			signal = value
		}
	}
	return attempted, signal
}

// assertCohereLiveComposition drives the turn that this whole target exists for:
// a real credential, a real provider, and a brief composed by Command over the
// documents the fan-out retrieved.
func assertCohereLiveComposition(binary, profilesRoot, coreRoot string) error {
	var status int
	var body string
	trace, cleanup, err := withCohereChatbot(binary, profilesRoot, coreRoot, "cohere-live",
		// Passed explicitly rather than inherited: cohereCredential() also reads
		// the operator's key store, and a key found there is in this process
		// only, so an inherited environment starts the agent without one.
		[]string{cohereCredentialEnv + "=" + cohereCredential()},
		func() error {
			var err error
			status, body, err = postChatTurnRaw(cohereTasking)
			return err
		})
	if err != nil {
		return err
	}
	defer cleanup()

	if status != http.StatusOK {
		return fmt.Errorf("live Cohere turn returned HTTP %d with %q, want 200", status, firstRunes(body, 300))
	}
	resp, err := decodeChatBody(body)
	if err != nil {
		return err
	}
	if strings.TrimSpace(resp.Answer) == "" {
		return fmt.Errorf("live Cohere turn returned an empty answer")
	}
	// A serialized REST result would be a populated answer field too. The brief
	// is text, and an answer that opens with a JSON object means capture_brief
	// stopped normalizing the content blocks.
	if strings.HasPrefix(strings.TrimSpace(resp.Answer), "{") {
		return fmt.Errorf("live Cohere answer begins with %q, which reads as an unnormalized REST result rather than a composed brief",
			firstRunes(resp.Answer, 40))
	}
	composed := 0
	for _, outcome := range resp.Metadata.Sources.Composed {
		if demoVerifyDocumentCount(outcome) > 0 {
			composed++
		}
	}
	if composed == 0 {
		return fmt.Errorf("live Cohere turn composed no source that retrieved documents;"+
			" the brief was not built from the corpus (answer: %q)", firstRunes(resp.Answer, 80))
	}

	spans, err := cohereSpans(trace)
	if err != nil {
		return err
	}
	attempted, signal := cohereDispatchOutcome(spans)
	if !attempted {
		return fmt.Errorf("no invoke_cohere_command span in the trace; an Ollama word answered the turn." +
			" The answer alone cannot show this, which is why the trace is asserted (srd009 R3.1)")
	}
	if signal != "BriefComposed" {
		return fmt.Errorf("invoke_cohere_command emitted %q against the live provider, want BriefComposed", signal)
	}
	for _, span := range spans {
		if word := span.attr("command.name"); word == "invoke_llm_fast" || word == "invoke_llm_deep" {
			return fmt.Errorf("an Ollama chat word (%s) also ran in the live Cohere turn;"+
				" the tier selection or the fallback fired when it should not have", word)
		}
	}
	if err := assertCohereCitationsResolve(resp); err != nil {
		return err
	}
	// A degraded turn composes the same chunks in the fan-out's order and
	// answers just as fluently, so the response is the only place the rerank
	// pass is visible (srd009 R5.4).
	if resp.Metadata.Rerank != "applied" {
		return fmt.Errorf(
			"the live turn reports rerank=%q, want applied; the pass degraded to the pre-rerank"+
				" order or never ran, and neither is visible in the answer",
			resp.Metadata.Rerank)
	}
	return assertCohereCredentialAbsentFromSpans(spans, cohereCredential())
}

// cohereRevisionInstruction asks for material only rag1's disjoint corpus
// holds, so the source router scopes the instruction's retrieval away from the
// rag0 records the first brief cited: those can reach the revision only through
// the re-grounding read.
const cohereRevisionInstruction = "Add a section on the Solar Ridge photovoltaic project and its battery."

// assertCohereRevisionRegroundsCarriedClaims drives a brief and then a revision
// of it carrying the records the brief cited (GH-604). The answer cannot show
// the join: a revision composed only over the instruction's retrieval reads
// just as fluently. The trace can, so the leg asserts the machine path -- the
// cited ids were present, each unit was read, and the carried rows joined the
// instruction's before the rerank.
func assertCohereRevisionRegroundsCarriedClaims(binary, profilesRoot, coreRoot string) error {
	var cited []string
	var status int
	var body string
	trace, cleanup, err := withCohereChatbot(binary, profilesRoot, coreRoot, "cohere-revision",
		[]string{cohereCredentialEnv + "=" + cohereCredential()},
		func() error {
			firstStatus, firstBody, err := postChatTurnRaw(cohereTasking)
			if err != nil {
				return err
			}
			if firstStatus != http.StatusOK {
				return fmt.Errorf("the brief to revise answered %d: %s", firstStatus, firstRunes(firstBody, 300))
			}
			first, err := decodeChatBody(firstBody)
			if err != nil {
				return err
			}
			cited = corpusCitedRecordIDs(first)
			if len(cited) == 0 {
				return fmt.Errorf("the brief to revise cited no corpus record, so there is nothing to carry")
			}
			status, body, err = postChatTurnJSON(map[string]any{
				"message":          cohereRevisionInstruction,
				"history":          []any{},
				"prior_brief":      first.Answer,
				"prior_record_ids": cited,
			})
			return err
		})
	if err != nil {
		return err
	}
	defer cleanup()
	if status != http.StatusOK {
		return fmt.Errorf("the revision answered %d, want 200: %s", status, firstRunes(body, 300))
	}
	spans, err := cohereSpans(trace)
	if err != nil {
		return err
	}
	if err := assertRevisionJoinedCarriedRows(spans); err != nil {
		return fmt.Errorf("%w (carried ids %v)", err, cited)
	}
	passes, err := assertRevisionSearchedItsProposals(spans)
	if err != nil {
		return err
	}
	fmt.Printf("integration:cohere revision re-grounded on %d cited record(s) %v and searched %d proposed quer(ies)\n",
		len(cited), cited, passes)
	return nil
}

// assertRevisionSearchedItsProposals holds a revision's trace to the agentic
// search (GH-611): Command proposed queries through the forced tool call, the
// proposal validated, and each proposed query ran through the mesh's own
// /search surface. It returns how many passes searched. Whether a pass joined
// new rows depends on what the turn already held, so that is not asserted.
func assertRevisionSearchedItsProposals(spans []cohereSpanDetail) (int, error) {
	signals := map[string][]string{}
	var searchStatus []string
	for _, span := range spans {
		if word := span.attr("command.name"); word != "" {
			signals[word] = append(signals[word], span.attr("command.signal"))
			if word == "search_proposed_query" {
				searchStatus = append(searchStatus, span.Status.Code+" "+span.Status.Description)
			}
		}
	}
	for _, want := range []struct{ word, signal string }{
		{"check_revision_search", "RevisionSearchRequested"},
		{"propose_searches_via_tool", "LLMResponded"},
		{"parse_proposed_searches", "ProposedSearchesParsed"},
		{"check_proposal_1", "ProposalPresent"},
	} {
		if !slices.Contains(signals[want.word], want.signal) {
			return 0, fmt.Errorf("the revision's agentic search stopped before %s emitted %s (signals %v)",
				want.word, want.signal, signals[want.word])
		}
	}
	// One search_proposed_query dispatch per pass, on the shared word: the pass
	// count is how many self searches answered.
	passes := 0
	for _, signal := range signals["search_proposed_query"] {
		if signal == "ProposalSearched" {
			passes++
		}
	}
	if passes == 0 {
		return 0, fmt.Errorf("no proposed query was searched (search_proposed_query %v; status %v)",
			signals["search_proposed_query"], searchStatus)
	}
	return passes, nil
}

// corpusCitedRecordIDs returns the corpus records a turn cited, once each and in
// citation order, leaving out external search results -- the same set the UI
// sends back on a revision (revisionFields).
func corpusCitedRecordIDs(resp chatResponse) []string {
	seen := map[string]bool{}
	var ids []string
	for _, citation := range resp.Metadata.Citations {
		id := strings.TrimSpace(citation.RecordID)
		if id == "" || seen[id] || strings.HasPrefix(id, "http://") || strings.HasPrefix(id, "https://") ||
			strings.HasPrefix(id, "osint-") {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// assertRevisionJoinedCarriedRows holds a revision's trace to the re-grounding
// path: the cited ids were present, at least one unit's read answered, and the
// carried rows parsed into the union the rerank reads. A revision whose
// instruction already retrieved every cited record legitimately stops at
// PriorIdsAlreadyRetrieved, but the instruction this leg sends is scoped away
// from them, so that outcome is a failure here.
func assertRevisionJoinedCarriedRows(spans []cohereSpanDetail) error {
	signals := map[string][]string{}
	for _, span := range spans {
		if word := span.attr("command.name"); word != "" {
			signals[word] = append(signals[word], span.attr("command.signal"))
		}
	}
	if !slices.Contains(signals["check_prior_sources"], "PriorSourcesPresent") {
		return fmt.Errorf("check_prior_sources never saw the cited ids (signals %v)", signals["check_prior_sources"])
	}
	if !slices.Contains(signals["read_documents"], "DocumentsRead") {
		return fmt.Errorf("no unit answered the re-grounding read (read_documents signals %v)", signals["read_documents"])
	}
	if slices.Contains(signals["select_new_prior_ids"], "PriorIdsAlreadyRetrieved") {
		return fmt.Errorf("the instruction's own retrieval already held every cited record, so nothing was joined;" +
			" the leg's instruction is meant to be scoped away from them")
	}
	if !slices.Contains(signals["parse_chunk_union"], "ChunkUnionParsed") {
		return fmt.Errorf("the carried rows never joined the instruction's rows"+
			" (select_new_prior_rows %v, parse_chunk_union %v)",
			signals["select_new_prior_rows"], signals["parse_chunk_union"])
	}
	return nil
}

// postChatTurnJSON posts one chat turn carrying body fields postChatTurnRaw's
// message-only request cannot.
func postChatTurnJSON(payload map[string]any) (int, string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, "", err
	}
	response, err := http.Post(chatbotChatURL, "application/json", bytes.NewReader(data))
	if err != nil {
		return 0, "", fmt.Errorf("post chat turn: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, "", fmt.Errorf("read chat response: %w", err)
	}
	return response.StatusCode, string(body), nil
}

// assertCohereCitationsResolve checks the surfaced citation metadata against the
// documents the turn actually composed from (srd009 R4.1, R4.2).
//
// The answer's bracket tokens cannot stand in for this. Those are written by the
// model into its prose and a turn can carry provider citations while writing
// none, so counting them would pass a turn that surfaced nothing. What is
// asserted is that every surfaced span names a record id the composed set
// contains, which is what makes a citation openable rather than opaque.
func assertCohereCitationsResolve(resp chatResponse) error {
	if len(resp.Metadata.Citations) == 0 {
		return fmt.Errorf("the live Cohere turn surfaced no citations;" +
			" Command returns citation spans for a grounded brief and the surfacing chain" +
			" carries them to the response (srd009 R4.1)")
	}
	composed := map[string]bool{}
	for _, source := range resp.Metadata.Sources.Composed {
		for _, query := range source.IDs {
			for _, id := range query {
				composed[id] = true
			}
		}
	}
	for index, citation := range resp.Metadata.Citations {
		if citation.RecordID == "" {
			return fmt.Errorf("citation %d names no record id, so a reader cannot open it (srd009 R4.2)", index)
		}
		if citation.End <= citation.Start {
			return fmt.Errorf("citation %d spans [%d,%d), which covers no assertion",
				index, citation.Start, citation.End)
		}
		if len(composed) > 0 && !composed[citation.RecordID] {
			return fmt.Errorf("citation %d names record %q, which is not in the composed document set;"+
				" an unresolvable citation must be dropped and reported rather than surfaced (srd009 R4.4)",
				index, citation.RecordID)
		}
	}
	return nil
}

// cohereDispatchOutcome reports whether invoke_cohere_command ran and the signal it
// emitted. The signal is what separates a composed brief, a rejected
// credential, a rate limit, and an outage from one another.
func cohereDispatchOutcome(spans []cohereSpanDetail) (attempted bool, signal string) {
	for _, span := range spans {
		if span.attr("command.name") != "invoke_cohere_command" {
			continue
		}
		attempted = true
		if value := span.attr("command.signal"); value != "" {
			signal = value
		}
	}
	return attempted, signal
}

// assertCohereCredentialAbsentFromSpans scans every exported span for the
// credential. srd009 R1.3 puts spans in scope by name, and a bearer that leaks
// into telemetry is exactly as disclosed as one written to a log.
func assertCohereCredentialAbsentFromSpans(spans []cohereSpanDetail, credential string) error {
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return nil
	}
	encoded, err := json.Marshal(spans)
	if err != nil {
		return err
	}
	if strings.Contains(string(encoded), credential) {
		return fmt.Errorf("the credential appears in an exported span (srd009 R1.3)")
	}
	return nil
}

// firstRunes bounds a quoted excerpt so a failure message stays readable when
// the value it is reporting is a whole composed brief.
func firstRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "..."
}

// cohereSpanDetail decodes the span fields the shared chromaSpan type omits.
// The refusal a client records lands in the span status or in a recorded event
// rather than in an attribute, and reading it is the only way to tell a policy
// refusal from a dial failure.
type cohereSpanDetail struct {
	Name       string          `json:"Name"`
	Attributes []chromaTraceKV `json:"Attributes"`
	Status     struct {
		Code        string `json:"Code"`
		Description string `json:"Description"`
	} `json:"Status"`
	Events []struct {
		Name       string          `json:"Name"`
		Attributes []chromaTraceKV `json:"Attributes"`
	} `json:"Events"`
}

func (s cohereSpanDetail) attr(key string) string {
	for _, attribute := range s.Attributes {
		if attribute.Key == key {
			if value, ok := attribute.Value.Value.(string); ok {
				return value
			}
		}
	}
	return ""
}

// cohereSpans decodes every span in a trace with a streaming decoder rather
// than by splitting lines. The exporter pretty-prints, so a line-oriented read
// silently returns nothing: every line fails to parse on its own and is
// skipped, and an assertion looking for a span it cannot see reports that the
// word never ran. That failure mode is indistinguishable from the defect these
// assertions exist to catch, which is why this reader exists.
func cohereSpans(tracePath string) ([]cohereSpanDetail, error) {
	return decodeSpanStream[cohereSpanDetail](tracePath)
}

// cohereRefusalMessage returns the failure text recorded against invoke_cohere_command
// and whether the word was dispatched at all. It decodes with a streaming
// decoder rather than by splitting lines, so it reads the exporter's output
// whether the spans are written one per line or pretty-printed.
func cohereRefusalMessage(tracePath string) (message string, dispatched bool, err error) {
	spans, err := cohereSpans(tracePath)
	if err != nil {
		return "", false, err
	}
	for _, span := range spans {
		if span.attr("command.name") != "invoke_cohere_command" {
			continue
		}
		dispatched = true
		if span.Status.Description != "" {
			message = span.Status.Description
		}
		for _, event := range span.Events {
			for _, attribute := range event.Attributes {
				if value, ok := attribute.Value.Value.(string); ok && value != "" {
					if attribute.Key == "reason" || attribute.Key == "error.message" ||
						attribute.Key == "exception.message" {
						message = value
					}
				}
			}
		}
		if message == "" {
			message = span.attr("error.message")
		}
	}
	return message, dispatched, nil
}

// cohereAnswerWordsInTrace lists the chat words a turn actually ran. It exists
// for the failure messages: the live tier selector is a small model making a
// real choice, so a missing Cohere dispatch is as likely to be a routing
// decision as a defect, and an operator should not have to guess which.
func cohereAnswerWordsInTrace(tracePath string) ([]string, error) {
	spans, err := cohereSpans(tracePath)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, span := range spans {
		switch name := span.attr("command.name"); name {
		case "invoke_cohere_command", "invoke_llm_fast", "invoke_llm_deep":
			seen[name] = true
		}
	}
	return sortedModelKeys(seen), nil
}

// decodeChatBody decodes a turn that answered. The raw body is kept by the
// callers so a non-200 reports what the mesh actually said rather than a decode
// error about a document that was never a chat response.
func decodeChatBody(body string) (chatResponse, error) {
	var resp chatResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return chatResponse{}, fmt.Errorf("decode chat response %q: %w", firstRunes(body, 200), err)
	}
	return resp, nil
}
