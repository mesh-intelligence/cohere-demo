// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The verdict is decided by document-identifier prefix, mirroring the Verdict
// rows in data/*/README.md. The synthetic humint tier is the publishable one.
func TestKeepPrivateVerdictFollowsTheDocumentPrefix(t *testing.T) {
	keep := []string{"isw-2022-12-01", "tg-nytimes-1880", "losses-2022-12",
		"missile-2022-12-05", "ISW-2022-12-01"}
	for _, id := range keep {
		if !corpusDocumentKeepPrivate(id) {
			t.Errorf("%s should be keep-private", id)
		}
	}
	publish := []string{"hr-22-008", "hr-22-030"}
	for _, id := range publish {
		if corpusDocumentKeepPrivate(id) {
			t.Errorf("%s should be publishable", id)
		}
	}
}

// A keep-private document resolves into the private checkout and a publishable
// one into the tracked store; with no private checkout present, resolving a
// keep-private document is an error naming the private repository rather than a
// fallback into the tracked tree.
func TestDocumentDirSplitsByVerdict(t *testing.T) {
	root := t.TempDir()
	private := t.TempDir()
	t.Setenv(corpusPrivateRootEnv, private)

	dir, err := corpusDocumentDir(root, "isw-2022-12-01")
	if err != nil {
		t.Fatalf("keep-private resolution: %v", err)
	}
	if want := filepath.Join(private, "store", "documents", "isw-2022-12-01"); dir != want {
		t.Errorf("keep-private document resolved to %s, want %s", dir, want)
	}

	dir, err = corpusDocumentDir(root, "hr-22-008")
	if err != nil {
		t.Fatalf("publishable resolution: %v", err)
	}
	if want := filepath.Join(root, filepath.FromSlash(corpusStoreDocumentsDir),
		"hr-22-008"); dir != want {
		t.Errorf("publishable document resolved to %s, want %s", dir, want)
	}

	t.Setenv(corpusPrivateRootEnv, "")
	if _, err := corpusDocumentDir(root, "isw-2022-12-01"); err == nil {
		t.Error("resolving a keep-private document with no private checkout" +
			" should fail rather than fall back into the tracked store")
	} else if !strings.Contains(err.Error(), corpusPrivateDataRepo) {
		t.Errorf("the error should name %s: %v", corpusPrivateDataRepo, err)
	}
}

// The union loader reads both locations as one store and refuses a document
// present in both, naming the two locations.
func TestUnionStoreLoadsBothLocationsAndRefusesDuplicates(t *testing.T) {
	root := t.TempDir()
	private := t.TempDir()
	t.Setenv(corpusPrivateRootEnv, private)

	envelopes := storeFixtureEnvelopes()
	tracked, kept := envelopes[0], envelopes[1]
	writeSplitFixtureDocument(t,
		filepath.Join(root, filepath.FromSlash(corpusStoreDocumentsDir)), tracked)
	writeSplitFixtureDocument(t,
		filepath.Join(private, "store", "documents"), kept)

	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		t.Fatalf("union load: %v", err)
	}
	for _, id := range []string{tracked.DocumentID, kept.DocumentID} {
		if _, held := documents[id]; !held {
			t.Errorf("union load is missing %s", id)
		}
	}

	writeSplitFixtureDocument(t,
		filepath.Join(private, "store", "documents"), tracked)
	if _, err := loadCorpusDocumentStore(root); err == nil {
		t.Error("a document stored in both locations should refuse the load")
	}
}

// Missing keep-private tiers are reported by name, and a store that holds them
// all reports nothing.
func TestMissingKeepPrivateTiersAreNamed(t *testing.T) {
	documents := map[string]corpusStoredDocument{
		"hr-22-008":      {DocumentID: "hr-22-008"},
		"isw-2022-12-01": {DocumentID: "isw-2022-12-01"},
	}
	missing := corpusMissingKeepPrivateTiers(documents)
	if len(missing) != 3 {
		t.Fatalf("want the three absent tiers named, got %v", missing)
	}
	if err := requireFullCorpusStore(t.TempDir(), documents); err == nil {
		t.Error("a partial store should fail the full-corpus guard")
	}
	for _, id := range []string{"tg-nytimes-1880", "losses-2022-12", "missile-2022-12-05"} {
		documents[id] = corpusStoredDocument{DocumentID: id}
	}
	if missing := corpusMissingKeepPrivateTiers(documents); len(missing) != 0 {
		t.Errorf("a full store should report nothing missing, got %v", missing)
	}
	if err := requireFullCorpusStore(t.TempDir(), documents); err != nil {
		t.Errorf("a full store should pass the guard: %v", err)
	}
}

// writeSplitFixtureDocument writes one fixture envelope with one chunk into an
// explicit documents directory, bypassing verdict routing so the test chooses
// the location.
func writeSplitFixtureDocument(t *testing.T, documentsDir string, envelope corpusEnvelope) {
	t.Helper()
	dir := filepath.Join(documentsDir, strings.ToLower(envelope.DocumentID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	encoded, err := encodeCorpusYAML(envelope)
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, corpusEnvelopeRecordName), encoded, 0o644); err != nil {
		t.Fatalf("write envelope: %v", err)
	}
}

// prepareSplitSibling creates an empty private checkout beside a fixture root,
// following the sibling convention corpusPrivateRoot resolves, so verdict
// routing has somewhere to put keep-private fixture documents. Fixtures reuse
// real tier identifiers, which is what exercises the routing.
func prepareSplitSibling(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "..", "cohere-demo-data", "store", "documents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create the private sibling %s: %v", dir, err)
	}
}
