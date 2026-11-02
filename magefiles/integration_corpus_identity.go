// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import "fmt"

// corpusQueryEmbedder returns the query-side embedder for one stored identity,
// or the reason it cannot be built on this machine.
//
// The identity has to match the one the collection was seeded under: srd002
// R3.3 excludes any source whose reported embedding identity differs from the
// query vector's, and embedding at the other family would compare vectors from
// two spaces and report nonsense with confidence.
func corpusQueryEmbedder(held corpusStoreIdentity) (func(string) ([]float64, error), string) {
	switch held.Provider {
	case "ollama":
		if reason := chromaOllamaSkipReasonForModels([]string{held.Identity}); reason != "" {
			return nil, reason
		}
		return func(text string) ([]float64, error) {
			return ollamaEmbedQuery(held.Identity, text)
		}, ""
	case "cohere":
		if cohereCredential() == "" {
			return nil, fmt.Sprintf("the store holds the %s identity and neither %s nor %s"+
				" supplies the Cohere credential",
				held.Identity, cohereCredentialEnv, cohereCredentialFile)
		}
		// The corpus side embedded as search_document; a query embeds as
		// search_query, and the two differing is what keeps retrieval honest.
		return func(text string) ([]float64, error) {
			return cohereEmbedText(held.Identity, cohereEmbedInputType, text)
		}, ""
	default:
		return nil, fmt.Sprintf("the store records provider %q for identity %q, which no"+
			" declared client can embed a query through", held.Provider, held.Identity)
	}
}

// selectCorpusStoreIdentity names the one family the seed, the retrieval probe,
// and the served turn all use, and reports why a query cannot be embedded at it
// when that is so.
//
// It prefers an identity this machine can reach, because the phases downstream
// of the seed embed live queries and a store holding two families would
// otherwise strand them behind whichever one sorted first. The seed itself
// reads stored vectors and calls no provider, so an unreachable family is still
// a legitimate selection: the reason travels with it and the phases that need a
// provider skip on it rather than the seed failing.
//
// Ties break on the index's own order, which the build sorts by identity name.
// Nothing here prefers a provider by name; a machine holding the Cohere
// credential reaches the Cohere family and selects it, and a machine without
// one falls through to the family it can actually embed at.
func selectCorpusStoreIdentity(
	index corpusStoreIndex,
	embedderFor func(corpusStoreIdentity) (func(string) ([]float64, error), string),
) (corpusStoreIdentity, string, error) {
	if len(index.Identities) == 0 {
		return corpusStoreIdentity{}, "", fmt.Errorf("the store names no identities")
	}
	for _, held := range index.Identities {
		if _, reason := embedderFor(held); reason == "" {
			return held, "", nil
		}
	}
	first := index.Identities[0]
	_, reason := embedderFor(first)
	return first, reason, nil
}
