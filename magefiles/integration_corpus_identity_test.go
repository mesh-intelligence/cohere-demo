// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"strings"
	"testing"
)

// twoFamilyIndex is the store this release produces: both families over the
// same records, in the order the build sorts them.
func twoFamilyIndex() corpusStoreIndex {
	return corpusStoreIndex{
		Identities: []corpusStoreIdentity{
			{Identity: "embed-v4.0", Provider: "cohere", Dimension: 1536, Chunks: 36},
			{Identity: "qwen3-embedding:8b", Provider: "ollama", Dimension: 4096, Chunks: 36},
		},
	}
}

// reachableOnly builds an embedder stand-in that reports every identity
// unreachable except the named one, so a test states which provider the machine
// has without depending on whether this machine has it.
func reachableOnly(name string) func(corpusStoreIdentity) (func(string) ([]float64, error), string) {
	return func(held corpusStoreIdentity) (func(string) ([]float64, error), string) {
		if held.Identity == name {
			return func(string) ([]float64, error) { return []float64{0}, nil }, ""
		}
		return nil, fmt.Sprintf("%s is not reachable from this machine", held.Identity)
	}
}

// A machine with no credential must keep the coverage it has today: the seed
// and the probe run on the family it can embed at, rather than selecting the
// family that happens to sort first and skipping the probe.
func TestSelectCorpusStoreIdentityPrefersAReachableFamily(t *testing.T) {
	held, reason, err := selectCorpusStoreIdentity(twoFamilyIndex(),
		reachableOnly("qwen3-embedding:8b"))
	if err != nil {
		t.Fatal(err)
	}
	if held.Identity != "qwen3-embedding:8b" {
		t.Errorf("selected %q, want the reachable family rather than the first sorted one",
			held.Identity)
	}
	if reason != "" {
		t.Errorf("a reachable selection carries the skip reason %q, want none", reason)
	}
}

// With the credential in hand the Cohere family is reachable and sorts first,
// which is the selection the served turn needs: the chatbot embeds its query at
// Cohere whatever else the store holds.
func TestSelectCorpusStoreIdentityTakesTheCohereFamilyWhenReachable(t *testing.T) {
	held, reason, err := selectCorpusStoreIdentity(twoFamilyIndex(), reachableOnly("embed-v4.0"))
	if err != nil {
		t.Fatal(err)
	}
	if held.Identity != "embed-v4.0" || held.Provider != "cohere" {
		t.Errorf("selected %q (provider %q), want the reachable Cohere family",
			held.Identity, held.Provider)
	}
	if reason != "" {
		t.Errorf("a reachable selection carries the skip reason %q, want none", reason)
	}
}

// The seed reads stored vectors and calls no provider, so a machine that can
// reach neither family still seeds. The reason travels with the selection and
// the phases that need a provider skip on it.
func TestSelectCorpusStoreIdentitySelectsAnUnreachableFamilyWithItsReason(t *testing.T) {
	held, reason, err := selectCorpusStoreIdentity(twoFamilyIndex(), reachableOnly("none"))
	if err != nil {
		t.Fatal(err)
	}
	if held.Identity != "embed-v4.0" {
		t.Errorf("selected %q, want the first identity when none is reachable", held.Identity)
	}
	if !strings.Contains(reason, "embed-v4.0") {
		t.Errorf("skip reason %q does not name the identity it was produced for", reason)
	}
}

// A store naming no identity is a store nothing can be seeded from, and saying
// so beats indexing an empty slice.
func TestSelectCorpusStoreIdentityRejectsAnEmptyIndex(t *testing.T) {
	if _, _, err := selectCorpusStoreIdentity(corpusStoreIndex{},
		reachableOnly("embed-v4.0")); err == nil {
		t.Fatal("an index naming no identities was accepted")
	}
}

// The credential clause is what an operator reads to learn the run is not
// broken, so it names both channels the credential can arrive through.
func TestCorpusQueryEmbedderNamesBothCredentialChannels(t *testing.T) {
	t.Setenv(cohereCredentialEnv, "")
	pinAbsentCredentialFile(t)
	embed, reason := corpusQueryEmbedder(corpusStoreIdentity{
		Identity: "embed-v4.0", Provider: "cohere",
	})
	if embed != nil {
		t.Error("an embedder was returned with no credential available")
	}
	for _, want := range []string{cohereCredentialEnv, cohereCredentialFile} {
		if !strings.Contains(reason, want) {
			t.Errorf("skip reason %q does not name %q", reason, want)
		}
	}
}

// A provider the store records but no client implements has to say so by name.
// Silently returning no embedder would read as an unreachable provider and send
// someone looking at their Ollama.
func TestCorpusQueryEmbedderRejectsAnUnknownProvider(t *testing.T) {
	_, reason := corpusQueryEmbedder(corpusStoreIdentity{
		Identity: "some-model", Provider: "unheard-of",
	})
	for _, want := range []string{"unheard-of", "some-model"} {
		if !strings.Contains(reason, want) {
			t.Errorf("skip reason %q does not name %q", reason, want)
		}
	}
}

// A build at one family must not strip the provenance of the others. Before
// this carried forward, a Cohere build over an Ollama store left the Ollama
// entry with an empty provider, and the integration target then had no way to
// tell which client could embed at it.
func TestCorpusStoreIdentitiesCarryForeignProvenanceForward(t *testing.T) {
	records := map[string]corpusChunkRecord{
		"doc-0001": {Vectors: []corpusChunkVector{
			{Identity: "qwen3-embedding:8b", Dimension: 4096, Values: make([]float64, 4096)},
			{Identity: "embed-v4.0", Dimension: 1536, Values: make([]float64, 1536)},
		}},
	}
	built := corpusEmbeddingIdentity{
		identity: "embed-v4.0", provider: "cohere", inputType: cohereEmbedDocumentInputType,
	}
	previous := []corpusStoreIdentity{
		{Identity: "qwen3-embedding:8b", Provider: "ollama", Dimension: 4096, Chunks: 1},
	}

	byName := make(map[string]corpusStoreIdentity)
	for _, entry := range corpusStoreIdentities(records, built, previous) {
		byName[entry.Identity] = entry
	}
	if got := byName["qwen3-embedding:8b"].Provider; got != "ollama" {
		t.Errorf("the family this build did not write records provider %q, want ollama", got)
	}
	if got := byName["embed-v4.0"].Provider; got != "cohere" {
		t.Errorf("the built family records provider %q, want cohere", got)
	}
	if got := byName["embed-v4.0"].InputType; got != cohereEmbedDocumentInputType {
		t.Errorf("the built family records input type %q, want %q",
			got, cohereEmbedDocumentInputType)
	}
}
