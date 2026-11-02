// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// The chat and observer origins the verify gates address. They name the kind
// ingress hosts; the gcp:verify verbs repoint them at port-forwards to the GKE
// release (GH-880).
var (
	demoChatOrigin     = "http://" + cohereApplicationHost
	demoObserverOrigin = "http://" + cohereObserverHost
)

// demoVerifyURL is the chat turn endpoint on the current chat origin.
func demoVerifyURL() string { return demoChatOrigin + "/api/v1/chat" }

const (
	// demoVerifyTasking names entities the seeded corpus carries, so a mesh whose
	// corpus is queryable retrieves for it and one whose rag-server still holds
	// an empty collection does not. It is the primary tasking corpus.yaml
	// declares the corpus is authored to hold conditions for.
	demoVerifyTasking = "Describe Russian strikes on Ukrainian infrastructure in " +
		"December 2022 and the air defence response to them."

	// demoVerifyBriefTasking names the same corpus entities and asks for a
	// brief, which is what routes the tier selector to the Cohere word. The
	// plain tasking above is a description request, and a selector that sent it
	// to an Ollama word would be behaving correctly -- gating the Cohere tier on
	// it would fail a healthy mesh.
	demoVerifyBriefTasking = "Prepare a command-staff brief on Russian strikes on " +
		"Ukrainian infrastructure in December 2022 and the air defence response."

	// demoVerifyBodyLimit bounds how much of an unexpected response body a
	// failure carries. Enough to recognize an ingress error page or a mapped
	// degradation, short of pasting a whole answer into the terminal.
	demoVerifyBodyLimit = 400

	// demoCohereTier is the value metadata.answered_by carries when the Cohere
	// Command word composed the brief.
	demoCohereTier = "cohere"
)

// Verify serves one tasking against the running demo mesh and asserts the
// answer composed from retrieved documents (rel09.9-uc001-baseline-demo S4,
// S5; srd002 AC3). It reads attribution from the chat response's
// metadata.sources.composed, which is the server's own account of which
// retrieved chunks reached the answer prompt, and requires those sources to
// carry documents rather than merely to appear. The SPA panel decides the same
// question by scraping bracket tokens out of the answer text, and small answer
// models do not reliably emit them, so a correctly composed turn renders there
// as unattributed; a gate must not inherit that.
//
// Run it after Demo.Up and Demo.Seed. A turn is the longest inference chain in
// the mesh -- embed, select tier, fan out, compose -- so it takes the
// inference bound rather than the probe bound.
func (App) Verify() error {
	// Read the deployed release first: which tier should answer decides which
	// tasking to send, and sending a description request to a Cohere-enabled
	// mesh would route to an Ollama word and fail a healthy deployment.
	release := demoCohereReleaseValues()
	cohereEnabled := release.Enabled
	tasking := demoVerifyTasking
	if cohereEnabled {
		tasking = demoVerifyBriefTasking
	}
	body := fmt.Sprintf(`{"message":%q,"history":[]}`, tasking)
	data, status, err := requestInference(
		http.MethodPost, demoVerifyURL(), body, "app:verify chat turn")
	if err != nil {
		return fmt.Errorf(
			"app:verify: %w (is the application up? run mage app:up first)", err)
	}
	composed, err := demoVerifyComposedSources(data, status)
	if err != nil {
		return fmt.Errorf("app:verify: %w", err)
	}
	tier, err := demoVerifyAnsweringTier(data, cohereEnabled)
	if err != nil {
		return fmt.Errorf("app:verify: %w", err)
	}
	fmt.Printf("app:verify: the %s tier answered, composing %d source(s): %s\n",
		tier, len(composed), strings.Join(composed, ", "))
	if !cohereEnabled {
		fmt.Println("app:verify PASS - the mesh answered a tasking from its corpus and reported the sources it composed from")
		return nil
	}
	embedModel, err := demoVerifyQueryEmbedding(data, release)
	if err != nil {
		return fmt.Errorf("app:verify: %w", err)
	}
	if err := demoVerifyNoEmbeddingExclusion(data); err != nil {
		return fmt.Errorf("app:verify: %w", err)
	}
	cited, err := demoVerifyCitations(data)
	if err != nil {
		return fmt.Errorf("app:verify: %w", err)
	}
	if err := demoVerifyRerank(data); err != nil {
		return fmt.Errorf("app:verify: %w", err)
	}
	fmt.Printf("app:verify: the tasking was embedded by Cohere with %s and no source was excluded"+
		" by the embedding-model comparison\n", embedModel)
	fmt.Printf("app:verify: the brief carries %d citation(s), each resolving to a composed chunk: %s\n",
		len(cited), strings.Join(cited, ", "))
	fmt.Println("app:verify: the composed documents were ordered by the Cohere rerank pass")
	fmt.Println("app:verify PASS - the mesh answered a Cohere-embedded tasking from its reseeded corpus")
	return nil
}

// demoVerifyRerank asserts the deployed turn composed from the reranked ordering
// rather than the fan-out's (srd009 R5.1, R5.4).
//
// The answer cannot show this. A degraded turn returns 200 with a composed brief
// over the same chunks in a different order, so the response field is the only
// place the pass is visible -- which is why the release added it. On a deployed
// mesh there is no trace to read, unlike the local rig.
func demoVerifyRerank(data []byte) error {
	var resp chatResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("decode chat response: %w", err)
	}
	switch resp.Metadata.Rerank {
	case "applied":
		return nil
	case "":
		return fmt.Errorf(
			"the deployed response carries no rerank outcome; the pass did not reach the pod." +
				" Check that the chart staged agents/chatbot/request-fanout-declarations.yaml")
	default:
		return fmt.Errorf(
			"the deployed turn reports rerank=%q; it composed in the fan-out order."+
				" A degraded turn answers exactly like a reranked one, so the answer cannot show this."+
				" Check the chatbot pod log for a provider refusal",
			resp.Metadata.Rerank)
	}
}

// demoVerifyCitations asserts the deployed brief carries structured citations
// that resolve to chunks the turn composed from (srd009 R4.1, R4.2), and
// returns the record ids they name.
//
// This is the deployed half of what the scenario critics prove hermetically and
// integration:cohere proves against the live provider. It is worth asserting
// again here because the panel a reader actually opens is served by this mesh,
// and the citation chain reaches it through the chart's staged declarations
// rather than through the profile a local rig loads -- a staging step that
// dropped the chain would leave every other gate green.
//
// A turn that surfaced no citation is a failure rather than an uncited outcome:
// the tasking is answered from the seeded corpus and the provider cites a grounded
// brief, so nothing legitimately produces an empty list here. The uncited
// branch is proven where it can be scripted.
func demoVerifyCitations(data []byte) ([]string, error) {
	var resp chatResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}
	if len(resp.Metadata.Citations) == 0 {
		return nil, fmt.Errorf(
			"the deployed brief carries no citations; the provider cites a grounded brief," +
				" so an empty list here means the surfacing chain did not reach the pod." +
				" Check that the chart staged agents/chatbot/request-fanout-declarations.yaml")
	}
	composed := map[string]bool{}
	for _, source := range resp.Metadata.Sources.Composed {
		for _, query := range source.IDs {
			for _, id := range query {
				composed[id] = true
			}
		}
	}
	ids := make([]string, 0, len(resp.Metadata.Citations))
	for index, citation := range resp.Metadata.Citations {
		if citation.RecordID == "" {
			return nil, fmt.Errorf(
				"deployed citation %d names no record id, so a reader cannot open it (srd009 R4.2)", index)
		}
		if len(composed) > 0 && !composed[citation.RecordID] {
			return nil, fmt.Errorf(
				"deployed citation %d names record %q, which the turn did not compose from;"+
					" an unresolvable citation must be dropped and reported rather than surfaced (srd009 R4.4)",
				index, citation.RecordID)
		}
		ids = append(ids, citation.RecordID)
	}
	if dropped := len(resp.Metadata.DroppedCitations); dropped > 0 {
		fmt.Printf("app:verify: %d cited source(s) were reported as unresolvable and dropped\n", dropped)
	}
	return ids, nil
}

// demoVerifyNoEmbeddingExclusion holds the assertion rel10.3 exists for: after
// the repoint, every source survives the srd002 R3.3 comparison. A mesh whose
// query was repointed but whose collections were never reseeded still answers,
// still returns 200, and still reads as fluent prose — every source silently
// excluded and the answer drawn from model priors. The composed-source count
// alone does not catch it, because an excluded source never reaches that list
// (rel10.3-uc001 F6, S5).
func demoVerifyNoEmbeddingExclusion(data []byte) error {
	var resp chatResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("decode chat response: %w", err)
	}
	excluded := demoVerifyOutcomeNames(resp.Metadata.Sources.EmbeddingModelExcluded)
	if len(excluded) == 0 {
		return nil
	}
	return fmt.Errorf(
		"%d source(s) were excluded for embedding-model mismatch (%s):"+
			" the query embedding was repointed but those collections still hold vectors"+
			" from another embedding family. Run mage app:seed to reseed them",
		len(excluded), strings.Join(excluded, ", "))
}

// demoVerifyComposedSources decides the assertion from a response body and its
// status, so the contract is testable without a cluster. It returns each
// composed source with the number of documents it contributed.
func demoVerifyComposedSources(data []byte, status int) ([]string, error) {
	if status != http.StatusOK {
		return nil, fmt.Errorf("chat turn returned status %d, want 200: %s",
			status, demoVerifyExcerpt(data))
	}
	var resp chatResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode chat response: %w: %s", err, demoVerifyExcerpt(data))
	}
	if len(resp.Metadata.Sources.Composed) == 0 {
		return nil, fmt.Errorf(
			"the turn answered but composed no sources, so the answer is unattributed%s",
			demoVerifyDiagnosis(resp))
	}
	names := make([]string, 0, len(resp.Metadata.Sources.Composed))
	retrieved := 0
	for index, source := range resp.Metadata.Sources.Composed {
		name := strings.TrimSpace(source.Name)
		if name == "" {
			name = fmt.Sprintf("source %d", index)
		}
		documents := demoVerifyDocumentCount(source)
		retrieved += documents
		names = append(names, fmt.Sprintf("%s (%d document(s))", name, documents))
		// The retrieved chunk identifiers, so a reader of the verify sees which
		// documents and tiers composition drew on rather than only how many.
		// The citations printed later name what the answer quoted, which
		// understates the composed set when the model quotes one tier of a
		// multi-tier retrieval (GH-286).
		ids := make([]string, 0, documents)
		for _, query := range source.IDs {
			ids = append(ids, query...)
		}
		if len(ids) > 0 {
			fmt.Printf("app:verify: %s composed: %s\n", name, strings.Join(ids, ", "))
		}
	}
	// A composed entry means the source took part in composition, not that it
	// had anything to contribute: a rag-server bound to an empty collection
	// answers the query and composes zero chunks. Asserting only that the
	// composed list is non-empty passes on an unseeded cluster, which is the
	// one failure this target exists to catch.
	if retrieved == 0 {
		return nil, fmt.Errorf(
			"the turn composed %d source(s) but retrieved no documents from any of them,"+
				" so the answer came from model priors rather than the corpus"+
				" (composed: %s)%s",
			len(names), strings.Join(names, ", "), demoVerifyDiagnosis(resp))
	}
	return names, nil
}

// demoVerifyDocumentCount counts the chunks one composed source contributed.
// The mapped documents arrive as one list per query.
func demoVerifyDocumentCount(source chatbotSourceOutcome) int {
	count := 0
	for _, query := range source.Documents {
		count += len(query)
	}
	return count
}

// demoVerifyDiagnosis names the source outcomes that are populated when none
// composed. Which list holds them separates an unseeded corpus from an
// embedding-model mismatch from a RAG that failed to answer, and a gate that
// reports only "not composed" sends the operator back to logs that, for the
// unseeded case, say nothing at all.
func demoVerifyDiagnosis(resp chatResponse) string {
	var found []string
	if names := demoVerifyOutcomeNames(resp.Metadata.Sources.EmbeddingModelExcluded); len(names) > 0 {
		found = append(found, fmt.Sprintf(
			"excluded for embedding-model mismatch: %s", strings.Join(names, ", ")))
	}
	if names := demoVerifyOutcomeNames(resp.Metadata.Sources.QueryFailed); len(names) > 0 {
		found = append(found, fmt.Sprintf("query failed: %s", strings.Join(names, ", ")))
	}
	if len(resp.Metadata.Sources.NotSelected) > 0 {
		found = append(found, fmt.Sprintf(
			"not selected by the source router: %s",
			strings.Join(resp.Metadata.Sources.NotSelected, ", ")))
	}
	if message := strings.TrimSpace(resp.Error + " " + resp.Message); message != "" {
		found = append(found, "response reported: "+message)
	}
	if len(found) == 0 {
		return " (no source reported a fault, which is what an unseeded corpus looks like:" +
			" run mage app:seed, which also restarts each rag-server so it rebinds the seeded collection)"
	}
	return " (" + strings.Join(found, "; ") + ")"
}

func demoVerifyOutcomeNames(outcomes []chatbotSourceOutcome) []string {
	names := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		if name := strings.TrimSpace(outcome.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func demoVerifyExcerpt(data []byte) string {
	body := strings.TrimSpace(string(data))
	if body == "" {
		return "(empty body)"
	}
	if len(body) > demoVerifyBodyLimit {
		return body[:demoVerifyBodyLimit] + "..."
	}
	return body
}

// demoVerifyAnsweringTier reads which model tier wrote the answer, and requires
// the Cohere tier when the deployed release enables the provider.
//
// The check exists because the answer alone cannot show it. A turn that fell
// back to Ollama returns a well-formed brief-shaped answer composed from the
// same retrieved chunks, so a provider that was never reached looks exactly
// like one that worked. On a deployed mesh there is no trace to read, which is
// how integration:cohere settles the same question locally; the response has to
// carry it instead.
func demoVerifyAnsweringTier(data []byte, cohereEnabled bool) (string, error) {
	var resp chatResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("decode chat response: %w", err)
	}
	tier := strings.TrimSpace(resp.Metadata.AnsweredBy)
	if tier == "" {
		return "", fmt.Errorf(
			"the response carries no metadata.answered_by;" +
				" the deployed profile predates the answering-tier field and this gate cannot tell which tier answered")
	}
	if cohereEnabled && tier != demoCohereTier {
		return "", fmt.Errorf(
			"the deployed release enables the Cohere provider but the %s tier answered;"+
				" the turn degraded or the tier selector routed away from the Cohere word."+
				" Check the chatbot pod log for a provider failure", tier)
	}
	return tier, nil
}

// demoCohereRelease is what the deployed release says about the Cohere
// provider: whether it is enabled, and which embedding model a turn asks the
// provider for.
type demoCohereRelease struct {
	Enabled    bool
	EmbedModel string
}

// demoCohereReleaseValues reads the deployed release rather than the local
// values file: what matters is the mesh under test, and an operator who
// installed with --set would otherwise be gated against a default they never
// deployed.
//
// A release that cannot be read reports the provider disabled rather than
// inventing an answer. The tier and embedding assertions are skipped; the
// composed-source assertions, which need no release, still run.
func demoCohereReleaseValues() demoCohereRelease {
	out, err := readApplicationValues()
	if err != nil {
		return demoCohereRelease{}
	}
	var values struct {
		Cohere struct {
			Enabled    bool   `json:"enabled"`
			EmbedModel string `json:"embedModel"`
		} `json:"cohere"`
	}
	if err := json.Unmarshal(out, &values); err != nil {
		return demoCohereRelease{}
	}
	return demoCohereRelease{
		Enabled:    values.Cohere.Enabled,
		EmbedModel: strings.TrimSpace(values.Cohere.EmbedModel),
	}
}

// demoVerifyQueryEmbedding asserts the turn's query vector came from the Cohere
// embeddings operation, by comparing the model the response says it asked for
// against the one the deployed release configures.
//
// The composed-source assertions cannot settle this. A mesh whose corpus and
// query both embed through the host Ollama composes sources and retrieves
// documents exactly as a Cohere-embedded one does — the repoint is invisible in
// the answer, in the source list, and in the document counts. srd002 R3.3 only
// compares the two identities to each other, so it is equally satisfied when
// both are Ollama. Naming the expected model is what separates the release
// under test from the one it replaced (srd009 R2.2, rel10.3-uc001 F6).
func demoVerifyQueryEmbedding(data []byte, release demoCohereRelease) (string, error) {
	var resp chatResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("decode chat response: %w", err)
	}
	declared := strings.TrimSpace(resp.Metadata.QueryEmbeddingModel)
	if declared == "" {
		return "", fmt.Errorf(
			"the response carries no metadata.query_embedding_model;" +
				" the deployed profile predates the query-embedding-identity field" +
				" and this gate cannot tell which provider embedded the tasking")
	}
	if release.EmbedModel == "" {
		// The release enables Cohere but names no embedding model, so there is
		// nothing to compare against. Report what the turn declared rather than
		// passing silently or failing a deployment the chart allows.
		return declared, nil
	}
	if declared != release.EmbedModel {
		return "", fmt.Errorf(
			"the turn embedded its tasking with %q but the deployed release configures %q;"+
				" the query vector did not come from the Cohere embeddings operation this release repoints to",
			declared, release.EmbedModel)
	}
	return declared, nil
}
