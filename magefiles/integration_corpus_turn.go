// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// corpusTurnCollection is the collection the turn phase stands its rag-servers
// over. It is the served retrieval collection the corpus build seeds.
//
// The functions in this file are the turn half of a corpus tracer: they stand
// the mesh over a seeded collection, assert the answer composed from retrieved
// documents rather than from its prose, and count the turn's single embedding
// request. rel10.4's tracer drove them over the synthetic corpus; that corpus
// is retired, and mage integration:demoCorpus drives them over the Ukraine
// corpus (GH-268). They are kept rather than rewritten because the assertions
// are what a tracer needs whichever corpus it runs over.
const corpusTurnCollection = "corpus"

// corpusTurnExpectedEmbedRequests is what one correct turn spends: the chatbot
// embeds the tasking once and fans the vector out to every RAG source, so the
// count does not grow with the number of sources (rel10.4-uc001 S2).
const corpusTurnExpectedEmbedRequests = 1

// runCorpusTurn serves one tasking against the collection the seed phase filled
// from the chunk store, closing the half of the rel10.4 tracer the build and
// the seed cannot reach (rel10.4-uc001 S1, S2, S5; srd015 R5.5).
//
// It runs inside the seed phase rather than as a phase of its own because the
// collection it queries is the one the seed just wrote, and the Chroma
// container holding it is released when that phase returns. Standing the mesh
// up here reuses the seeded collection; standing it up afterwards would mean
// seeding a second one and proving something about that instead.
//
// The turn is served over the store-seeded collection by pointing both declared
// RAG sources at it, so whichever ones the source selector scopes the tasking
// to answer from the vectors the build computed. That is the claim: a brief
// composed from documents this store holds, not a fluent one composed from
// nothing.
func runCorpusTurn(root, coreRoot string, held corpusStoreIdentity, embedSkipReason string) error {
	if reason := corpusTurnSkipReason(root, held, embedSkipReason); reason != "" {
		fmt.Printf("SKIP integration:corpusBuild turn phase: %s\n", reason)
		return nil
	}
	manifest, err := readCorpusManifest(root)
	if err != nil {
		return err
	}
	tasking := strings.TrimSpace(manifest.RetrievalConditions.PrimaryTasking)
	if tasking == "" {
		return fmt.Errorf("the corpus declares no primary tasking to serve a turn against")
	}

	binary, cleanupAgent, err := buildAgent(coreRoot)
	if err != nil {
		return err
	}
	defer cleanupAgent()
	work, err := os.MkdirTemp("", "cohere-demo-corpus-turn-*")
	if err != nil {
		return fmt.Errorf("create the turn workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	// The proxy is armed before any child starts, because a child reads the
	// endpoint once at launch. Its lifetime is the turn and nothing else: the
	// seed's counter measures a different claim over a different window, and the
	// two must not overlap.
	proxy, stopProxy, err := startCorpusTurnEmbedProxy()
	if err != nil {
		return err
	}
	defer stopProxy()

	stopAgents, err := startCorpusTurnMesh(binary, root, coreRoot, work, held, proxy.baseURL())
	defer stopAgents()
	if err != nil {
		return err
	}

	resp, status, err := postChatTurn(tasking, "[]")
	if err != nil {
		return fmt.Errorf("serve the primary tasking: %w", err)
	}
	if err := assertCorpusTurnGrounded(resp, status, held.Identity); err != nil {
		return err
	}
	if err := assertCorpusTurnEmbedRequests(proxy.embedCount()); err != nil {
		return err
	}

	fmt.Printf("integration:corpusBuild: the primary tasking was answered from %d source(s)"+
		" over the store-seeded collection at identity %q, with %d embedding request(s)"+
		" counted at the client and an empty embedding-excluded partition\n",
		len(resp.Metadata.Sources.Composed), resp.Metadata.QueryEmbeddingModel,
		proxy.embedCount())
	return nil
}

// corpusTurnSkipReason reports why a turn cannot be served, or "" when it can.
// A machine missing a dependency is not evidence of a broken mesh, so each
// reason names what to supply.
func corpusTurnSkipReason(root string, held corpusStoreIdentity, embedSkipReason string) string {
	if embedSkipReason != "" {
		return embedSkipReason
	}
	// The chatbot's only embed transition is the Cohere one, so a collection
	// seeded at any other family is not a degraded source for this turn -- it is
	// an excluded one, and the turn would answer 200 from nothing. Skipping says
	// that plainly rather than reporting the empty answer as a defect.
	if held.Provider != "cohere" {
		return fmt.Sprintf("the seeded collection holds the %s identity from provider %q,"+
			" and the chatbot embeds its query at Cohere; a turn over it would exclude"+
			" every source", held.Identity, held.Provider)
	}
	required, err := chatbotRequiredModels(root)
	if err != nil {
		return fmt.Sprintf("invalid shipped chatbot model config: %v", err)
	}
	return chatbotOllamaSkipReasonForModels(required)
}

// startCorpusTurnMesh brings up the two declared RAG sources and the chatbot,
// each pointed at the store-seeded collection and told the identity it was
// seeded under. The returned stop is safe to call whatever went wrong, so the
// caller defers it before checking the error.
func startCorpusTurnMesh(
	binary, root, coreRoot, work string, held corpusStoreIdentity, cohereBase string,
) (func(), error) {
	var stops []func(kill bool) error
	stopAll := func() {
		for i := len(stops) - 1; i >= 0; i-- {
			_ = stops[i](true)
		}
	}

	// Both declared sources serve the store-seeded collection. Leaving rag1 down
	// would put it in the query-failed partition and make this a degraded turn,
	// which is a different case than the tracer's; pointing both at the seeded
	// collection means any source the selector scopes to answers from the store.
	rag1Profile, rag1Cleanup, err := generateRag1Variant(root)
	if err != nil {
		return stopAll, err
	}
	stops = append(stops, func(bool) error { rag1Cleanup(); return nil })

	sources := []struct {
		name    string
		profile string
		health  string
	}{
		{"rag0", ragServerProfile, ragControlHealth},
		{"rag1", rag1Profile, rag1ControlHealth},
	}
	for _, source := range sources {
		stop, err := startDetachedAgentWithEnv(agentLaunch{
			Binary: binary, ProfilesRoot: root, CoreRoot: coreRoot,
			Profile:   source.profile,
			TracePath: filepath.Join(work, source.name+".otel.json"),
			Workdir:   work,
			// The collection and the identity move together: RAG_COLLECTION is
			// the collection this source queries and RAG_EMBEDDING_MODEL is the
			// identity it reports for it. srd002 R3.3 compares that report
			// against the query vector's identity, so naming the collection
			// without naming the identity would exclude the source it just
			// pointed at the right vectors.
			Env: []string{
				"RAG_COLLECTION=" + corpusTurnCollection,
				"RAG_EMBEDDING_MODEL=" + held.Identity,
			},
			GracefulWait: 15 * time.Second,
		})
		if err != nil {
			return stopAll, fmt.Errorf("start %s: %w", source.name, err)
		}
		stops = append(stops, stop)
		if err := waitHTTPStatus(source.health, http.StatusOK, 30*time.Second); err != nil {
			return stopAll, fmt.Errorf("%s control health never came up: %w", source.name, err)
		}
	}

	proxyURL, err := url.Parse(cohereBase)
	if err != nil {
		return stopAll, fmt.Errorf("parse the proxy base URL: %w", err)
	}
	// The provider library clients read no environment, so the copy names the
	// proxy as their allow arguments (srd058 R1.6); it forwards every call and
	// counts only the embeddings.
	proxiedDir := filepath.Join(work, "chatbot-embed-proxied")
	proxiedProfile, err := copyChatbotProfile(root, proxiedDir, func(content string) string { return content })
	if err != nil {
		return stopAll, err
	}
	if err := pinChatbotProviderEndpoint(proxiedDir, cohereBase); err != nil {
		return stopAll, err
	}
	stopChat, err := startDetachedAgentWithEnv(agentLaunch{
		Binary: binary, ProfilesRoot: root, CoreRoot: coreRoot,
		Profile:   proxiedProfile,
		TracePath: filepath.Join(work, "chatbot.otel.json"),
		Workdir:   work,
		// The endpoint and its allowlist move together, which is the declared
		// way to point the client at an in-boundary deployment (srd009 R1.2).
		// That is what lets the counting proxy stand where the declared client
		// points, rather than the turn reaching past the policy to be measured.
		//
		// The credential is passed explicitly because the child resolves it from
		// its own environment, and this process may have read it from the
		// operator's key file rather than from an exported variable.
		Env: []string{
			"COHERE_EMBED_MODEL=" + held.Identity,
			"COHERE_BASE_URL=" + cohereBase,
			"COHERE_SCHEME=http",
			"COHERE_HOST=" + proxyURL.Hostname(),
			"COHERE_PORT=" + proxyURL.Port(),
			cohereCredentialEnv + "=" + cohereCredential(),
		},
		GracefulWait: 15 * time.Second,
	})
	if err != nil {
		return stopAll, fmt.Errorf("start chatbot: %w", err)
	}
	stops = append(stops, stopChat)
	if err := waitHTTPStatus(chatbotControlHealth, http.StatusOK, 30*time.Second); err != nil {
		return stopAll, fmt.Errorf("chatbot control health never came up: %w", err)
	}
	return stopAll, nil
}

// assertCorpusTurnGrounded is the tracer's second half and the identity
// agreement that makes it mean anything (S1, S5).
//
// The two travel together on purpose. A 200 carrying fluent prose satisfies S1
// on its own and is exactly what a mesh whose sources were all excluded
// returns, so the answer is evidence only alongside the partitions that say
// where it came from.
func assertCorpusTurnGrounded(resp chatResponse, status int, storeIdentity string) error {
	if status != http.StatusOK {
		return fmt.Errorf("the tasking answered with status %d, want 200: error=%q message=%q",
			status, resp.Error, resp.Message)
	}
	if strings.TrimSpace(resp.Answer) == "" {
		return fmt.Errorf("the tasking answered 200 with an empty brief")
	}
	if resp.Trace.Status != "succeeded" {
		return fmt.Errorf("the turn's trace reports %q, want succeeded", resp.Trace.Status)
	}

	if excluded := corpusTurnSourceNames(resp.Metadata.Sources.EmbeddingModelExcluded); len(excluded) > 0 {
		return fmt.Errorf(
			"the turn excluded %s on embedding identity while composing an answer;"+
				" the store was seeded at %q and the query embedded at %q, so the brief"+
				" was composed from nothing (srd002 R3.3)",
			strings.Join(excluded, ", "), storeIdentity, resp.Metadata.QueryEmbeddingModel)
	}
	if failed := corpusTurnSourceNames(resp.Metadata.Sources.QueryFailed); len(failed) > 0 {
		return fmt.Errorf("the turn's sources %s failed their query;"+
			" the tracer serves every declared source from the seeded collection",
			strings.Join(failed, ", "))
	}
	if resp.Metadata.QueryEmbeddingModel != storeIdentity {
		return fmt.Errorf(
			"the turn embedded its query at %q and the store recorded %q;"+
				" the identity the collection was seeded under and the one the query"+
				" was embedded at are one value or retrieval compares two spaces",
			resp.Metadata.QueryEmbeddingModel, storeIdentity)
	}

	composed := resp.Metadata.Sources.Composed
	if len(composed) == 0 {
		return fmt.Errorf("no source reached composition, so the brief cites nothing the store holds")
	}
	for _, outcome := range composed {
		if err := requireComposedSourceEvidence(composed, outcome.Name, ""); err != nil {
			return err
		}
	}
	return nil
}

// assertCorpusTurnEmbedRequests is what separates this release from the seeding
// path it replaces, which produces an identical brief. Reading the answer
// cannot establish the difference; counting the requests can (S2).
func assertCorpusTurnEmbedRequests(count int) error {
	if count != corpusTurnExpectedEmbedRequests {
		return fmt.Errorf(
			"the turn issued %d embedding request(s), want %d; a cycle that re-embeds"+
				" the corpus at turn time spends more, and one that embedded nothing"+
				" answered from a vector it did not compute",
			count, corpusTurnExpectedEmbedRequests)
	}
	return nil
}

// corpusTurnSourceNames names the sources in one partition, so a failure
// message reports which ones rather than how many.
func corpusTurnSourceNames(outcomes []chatbotSourceOutcome) []string {
	names := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		names = append(names, outcome.Name)
	}
	return names
}

// corpusTurnEmbedProxy counts the embedding requests a turn issues and forwards
// every request to the real provider.
//
// The seed phase's counter answers with a fabricated vector, which is right for
// a claim that no request happens and useless for a turn that has to retrieve
// something. Counting a live turn means standing in the path rather than in
// place of it: the count is read at the same declared client the seed's zero is
// read at, and the turn still gets the provider's real answers.
type corpusTurnEmbedProxy struct {
	mutex    sync.Mutex
	embeds   int
	address  string
	upstream *url.URL
	proxy    *httputil.ReverseProxy
}

func (p *corpusTurnEmbedProxy) baseURL() string {
	return "http://" + p.address
}

func (p *corpusTurnEmbedProxy) embedCount() int {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.embeds
}

// ServeHTTP counts only the embed operation. The same base URL carries the
// rerank and composition calls a turn also makes, and counting those would
// report a number that says nothing about how often the corpus was embedded.
func (p *corpusTurnEmbedProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == cohereEmbedPath {
		p.mutex.Lock()
		p.embeds++
		p.mutex.Unlock()
	}
	r.Host = p.upstream.Host
	p.proxy.ServeHTTP(w, r)
}

// startCorpusTurnEmbedProxy stands the proxy up on a local port pointed at the
// declared provider base. The caller passes its address to the chatbot child
// together with the allowlist entries that admit it, so nothing here touches
// the process environment: the seed phase's counter owns that channel, and two
// redirects of one variable over one run would race.
func startCorpusTurnEmbedProxy() (*corpusTurnEmbedProxy, func(), error) {
	upstream, err := url.Parse(envOrDefault("COHERE_BASE_URL", cohereEmbedDefaultBase))
	if err != nil {
		return nil, nil, fmt.Errorf("parse the declared Cohere base URL: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("listen for the embedding proxy: %w", err)
	}
	counting := &corpusTurnEmbedProxy{
		address:  listener.Addr().String(),
		upstream: upstream,
		proxy:    httputil.NewSingleHostReverseProxy(upstream),
	}
	server := &http.Server{Handler: counting, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	return counting, func() { _ = server.Close() }, nil
}
