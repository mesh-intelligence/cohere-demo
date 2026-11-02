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
	"strings"
	"time"
)

const (
	// cohereEmbedPath is the operation rel10.3 declares. It is named here rather
	// than read from the profile because the probe establishes the response
	// shape the declaration is authored against, and so must run whether or not
	// that declaration is in the profile yet.
	cohereEmbedPath = "/v2/embed"

	// cohereEmbedOperation and cohereQueryEmbedClient are the query-side
	// operation the Cohere provider library declares, which the chatbot's
	// embed-rest.yaml instantiates from its providers root (srd058 R4.2).
	cohereEmbedOperation   = "embed_query"
	cohereQueryEmbedClient = "query_embedder"

	// cohereEmbedWord is the chatbot word the Cohere request machine runs over
	// that operation; spans carry it as command.name.
	cohereEmbedWord = "embed_query_cohere"

	// cohereDocumentEmbedClient and cohereDocumentEmbedOperation are the corpus
	// side, which the Cohere provider library declares (srd058 R4.2). It embeds
	// as search_document against the query side's search_query, and the two
	// differing is what keeps retrieval honest.
	cohereDocumentEmbedClient    = "document_embedder"
	cohereDocumentEmbedOperation = "embed_document"

	// cohereEmbedDocumentInputType is the corpus-side value.
	cohereEmbedDocumentInputType = "search_document"

	// cohereEmbedDefaultBase and cohereEmbedDefaultModel mirror the rel10.3
	// declaration's defaults. An operator pointing at a private in-boundary
	// deployment overrides both, the same pair srd009 R1.2 keeps movable
	// together.
	cohereEmbedDefaultBase  = "https://api.cohere.com"
	cohereEmbedDefaultModel = "embed-v4.0"

	// cohereEmbedInputType is the query-side value. The document side embeds as
	// search_document, and the two differing is what keeps retrieval honest --
	// a mismatch produces well-formed vectors of the right dimension on both
	// sides and degrades only quality, which no assertion here can see.
	cohereEmbedInputType = "search_query"

	// cohereEmbedProbeText is one text, because the query side embeds one
	// tasking. The row count this yields is the assumption the normalization
	// word rests on, so the probe sends exactly what a turn sends.
	cohereEmbedProbeText = "What does the corpus describe?"

	cohereEmbedProbeTimeout = 60 * time.Second
)

// cohereEmbedObservation is what the probe learned about the provider's
// response. It is the artifact the declaration is written from: rel10.3's
// normalization word cannot be authored correctly without RowCount and
// Dimension, and no scenario mock can supply them, because a fixture returns
// whatever shape it declares and would confirm the declaration against itself.
type cohereEmbedObservation struct {
	TopLevelKeys   []string
	EmbeddingsKeys []string
	RowsAreNested  bool
	RowCount       int
	Dimension      int
	ElementsNumber bool
}

// assertCohereEmbedShape issues one live embedding call and establishes the four
// facts rel10.3 rests on: that the embeddings arrive nested rather than flat,
// that one text yields one row, that the configured model accepts the query
// input type, and what dimension the vector carries.
//
// It needs no mesh -- no Chroma, no Ollama, no rag tier -- so the caller runs it
// on the credential alone rather than behind the full dependency skip. A
// dependency that the assertion does not use should not decide whether the
// assertion runs.
func assertCohereEmbedShape() (cohereEmbedObservation, error) {
	var observed cohereEmbedObservation

	baseURL := envOrDefault("COHERE_BASE_URL", cohereEmbedDefaultBase)
	model := envOrDefault("COHERE_EMBED_MODEL", cohereEmbedDefaultModel)

	payload, err := json.Marshal(map[string]interface{}{
		"model":           model,
		"input_type":      cohereEmbedInputType,
		"embedding_types": []string{"float"},
		"texts":           []string{cohereEmbedProbeText},
	})
	if err != nil {
		return observed, fmt.Errorf("encode embed request: %w", err)
	}

	request, err := http.NewRequest(http.MethodPost,
		strings.TrimSuffix(baseURL, "/")+cohereEmbedPath, bytes.NewReader(payload))
	if err != nil {
		return observed, fmt.Errorf("build embed request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+cohereCredential())

	client := &http.Client{Timeout: cohereEmbedProbeTimeout}
	response, err := client.Do(request)
	if err != nil {
		return observed, fmt.Errorf("POST %s: %w", cohereEmbedPath, err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return observed, fmt.Errorf("read embed response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		// The body can name a rejected model or a rejected input_type, and both
		// are findings this probe exists to surface. It carries no credential.
		return observed, fmt.Errorf(
			"POST %s returned %d for model %q with input_type %q: %s (srd009 R2.1)",
			cohereEmbedPath, response.StatusCode, model, cohereEmbedInputType,
			strings.TrimSpace(string(body)))
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return observed, fmt.Errorf("decode embed response: %w", err)
	}
	return readCohereEmbedObservation(decoded, model)
}

// readCohereEmbedObservation walks a decoded embed response and reports what it
// found. It is separate from the HTTP call so the shape assertions are testable
// against recorded payloads without a credential; the live call is what makes
// the recorded payloads trustworthy in the first place.
func readCohereEmbedObservation(
	decoded map[string]interface{}, model string,
) (cohereEmbedObservation, error) {
	observed := cohereEmbedObservation{TopLevelKeys: sortedInterfaceKeys(decoded)}

	embeddings, ok := decoded["embeddings"]
	if !ok {
		return observed, fmt.Errorf(
			"embed response carries no embeddings field; top-level keys are %v (srd009 R2.1)",
			observed.TopLevelKeys)
	}

	// A flat array here would mean the response is keyed by nothing and the
	// declaration's $.embeddings.float selector has no path to walk. Reporting
	// which of the two arrived is the point of the probe.
	byType, ok := embeddings.(map[string]interface{})
	if !ok {
		return observed, fmt.Errorf(
			"embeddings is %T, want an object keyed by embedding type;"+
				" the declared response mapping walks maps only (srd009 R2.1)", embeddings)
	}
	observed.EmbeddingsKeys = sortedInterfaceKeys(byType)

	floats, ok := byType["float"]
	if !ok {
		return observed, fmt.Errorf(
			"embeddings carries no float key; present keys are %v (srd009 R2.1)",
			observed.EmbeddingsKeys)
	}
	rows, ok := floats.([]interface{})
	if !ok {
		return observed, fmt.Errorf("embeddings.float is %T, want an array (srd009 R2.1)", floats)
	}
	observed.RowCount = len(rows)
	if len(rows) == 0 {
		return observed, fmt.Errorf("embeddings.float is empty for one text (srd009 R2.1)")
	}

	// Nested is what the normalization word unwraps. A flat array of numbers
	// here means one text returns the vector directly and no unwrap is needed,
	// which would make rel10.3's capture word wrong rather than merely
	// unnecessary -- render_each faults when its items selector is not an array
	// of items.
	first, nested := rows[0].([]interface{})
	observed.RowsAreNested = nested
	if !nested {
		return observed, fmt.Errorf(
			"embeddings.float[0] is %T, want an array; the response is flat and the"+
				" declared unwrap would fault (srd009 R2.1)", rows[0])
	}
	observed.Dimension = len(first)
	if observed.Dimension == 0 {
		return observed, fmt.Errorf("embeddings.float[0] is an empty vector (srd009 R2.1)")
	}

	observed.ElementsNumber = true
	for i, element := range first {
		if _, isNumber := element.(float64); !isNumber {
			observed.ElementsNumber = false
			return observed, fmt.Errorf(
				"embeddings.float[0][%d] is %T, want a number; the declared vector"+
					" schema rejects it (srd009 R2.1)", i, element)
		}
	}

	if observed.RowCount != 1 {
		return observed, fmt.Errorf(
			"one text yielded %d rows, want 1; the declared normalization assumes a"+
				" single row and would concatenate %d of them (srd009 R2.1)",
			observed.RowCount, observed.RowCount)
	}

	fmt.Printf("integration:cohere embed shape (model %s, input_type %s):"+
		" top-level %v, embeddings keyed by %v, %d row of %d numbers\n",
		model, cohereEmbedInputType, observed.TopLevelKeys,
		observed.EmbeddingsKeys, observed.RowCount, observed.Dimension)
	return observed, nil
}

// cohereCredentialFile is the operator store the tooling falls back to when
// COHERE_API_KEY is not exported: strict JSON holding the key under "cohere".
// In this repository the path is a gitignored symlink to the operator's own
// store. A variable rather than a constant so absence tests can point it at a
// path that cannot exist; a checkout holding a real key must not turn them
// live.
var cohereCredentialFile = filepath.Join(".secrets", "keys.json")

// cohereCredential reads the operator-supplied credential: COHERE_API_KEY when
// exported, else the "cohere" field of cohereCredentialFile. One accessor so a
// leak has a single line to audit rather than every call site, and every caller
// goes through it for that reason.
//
// The environment stays the override and the only channel into the mesh: the
// chart accepts a Secret name and has no values field for a credential, child
// processes receive the resolved value through their environment, and nothing
// tracked in the repository holds it. The file is the operator's local store,
// and a missing or malformed file resolves to empty exactly as an unset
// variable does.
//
// The relative path misses from a linked worktree -- the store lives in the
// main checkout only, so every gh-* worktree run silently resolved no
// credential and downgraded the deployment to the Ollama tier (GH-318). When
// the default path is unreadable the accessor retries against the main
// checkout, found through the shared git common dir. The retry runs only
// while cohereCredentialFile still holds its default: an absence test that
// points the variable at an impossible path must stay dead even in a
// checkout holding a real key.
//
//nolint:forbidigo // COHERE_API_KEY is operator-supplied; this is the single audited read of either channel.
func cohereCredential() string {
	if value := strings.TrimSpace(os.Getenv(cohereCredentialEnv)); value != "" {
		return value
	}
	data, err := os.ReadFile(cohereCredentialFile)
	if err != nil && cohereCredentialFile == filepath.Join(".secrets", "keys.json") {
		if root := gitMainCheckoutRoot(); root != "" {
			data, err = os.ReadFile(filepath.Join(root, ".secrets", "keys.json"))
		}
	}
	if err != nil {
		return ""
	}
	var store struct {
		Cohere string `json:"cohere"`
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return ""
	}
	return strings.TrimSpace(store.Cohere)
}

// gitMainCheckoutRoot resolves the main checkout's directory from any linked
// worktree: the shared git common dir lives under the main checkout, so its
// parent is where the operator's gitignored store sits. Empty when git is
// absent or the directory is not a repository, and the caller treats that as
// a plain miss.
func gitMainCheckoutRoot() string {
	out, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return ""
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
}

// envOrDefault reads an operator override, falling back to the declared
// default. The pair it serves -- COHERE_BASE_URL and COHERE_EMBED_MODEL -- is
// the srd009 R1.2 configuration an operator moves together to point at a
// private in-boundary deployment, so the override is declared behaviour rather
// than ambient configuration leaking in.
//
//nolint:forbidigo // The srd009 R1.2 operator override; the default is the declared value and this is the single read of it.
func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

// sortedInterfaceKeys reports a decoded object's keys in a stable order, so a
// failure message names the same set every run.
func sortedInterfaceKeys(object map[string]interface{}) []string {
	keys := make(map[string]bool, len(object))
	for key := range object {
		keys[key] = true
	}
	return sortedModelKeys(keys)
}
