// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const caseFixtureName = "fixture-case"

// caseFixtureRoot registers a throwaway case and creates its root under a
// repository-shaped temporary directory, with a private checkout beside it so a
// leak would be observable.
func caseFixtureRoot(t *testing.T) (repository, root string) {
	t.Helper()
	parent := t.TempDir()
	repository = filepath.Join(parent, "repo")
	root = filepath.Join(repository, corpusCasesDir, caseFixtureName)
	if err := os.MkdirAll(filepath.Join(root, "corpus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(corpusManifestPath)), []byte("id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "cohere-demo-data", "store", "documents"), 0o755); err != nil {
		t.Fatal(err)
	}
	corpusCaseDefinitions[caseFixtureName] = corpusCaseDefinition{
		Label:       "the fixture case",
		SourceTypes: []string{"fixture_tier"},
		Slice:       func() corpusSliceParameters { return corpusSliceParameters{WindowStart: "2024-01-01"} },
		Selection:   func() corpusEmbedSelection { return corpusEmbedSelection{Ceiling: 7} },
	}
	t.Cleanup(func() { delete(corpusCaseDefinitions, caseFixtureName) })
	return repository, root
}

func TestCaseSelectionResolvesTheCaseRoot(t *testing.T) {
	repository, root := caseFixtureRoot(t)
	t.Setenv(corpusCaseEnv, caseFixtureName)
	got, err := corpusCaseRoot(repository)
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Errorf("resolved %s, want %s", got, root)
	}
	if corpusSliceFor(root).WindowStart != "2024-01-01" || corpusEmbedSelectionFor(root).Ceiling != 7 {
		t.Error("a case root should take the case's declared window and selection")
	}
	if want := filepath.Join(root, corpusCaseDataDir); corpusBuildDataRoot(root) != want {
		t.Errorf("data root %s, want %s", corpusBuildDataRoot(root), want)
	}
}

// With nothing selected every target sees the repository root and its defaults.
func TestCaseNoSelectionResolvesTheRepositoryRoot(t *testing.T) {
	repository, _ := caseFixtureRoot(t)
	t.Setenv(corpusCaseEnv, "")
	got, err := corpusCaseRoot(repository)
	if err != nil {
		t.Fatal(err)
	}
	if got != repository {
		t.Errorf("resolved %s, want the repository root %s", got, repository)
	}
	if corpusSliceFor(repository).WindowStart != sliceWindowStart {
		t.Error("the repository root should keep the defense window")
	}
	if corpusRootIsCase(repository) {
		t.Error("the repository root is not a case")
	}
}

// An unknown name fails naming what exists, because falling back would build the
// defense corpus when the operator asked for something else.
func TestCaseUnknownNameFails(t *testing.T) {
	repository, _ := caseFixtureRoot(t)
	t.Setenv(corpusCaseEnv, "no-such-case")
	_, err := corpusCaseRoot(repository)
	if err == nil || !strings.Contains(err.Error(), caseFixtureName) {
		t.Fatalf("want a failure naming the known cases, got %v", err)
	}
}

// A case root never sees the private checkout, whether the override is set or a
// sibling sits where the convention would find it (srd018 R1.3).
func TestCaseRootNeverReadsThePrivateCheckout(t *testing.T) {
	_, root := caseFixtureRoot(t)
	t.Setenv(corpusPrivateRootEnv, t.TempDir())
	if got := corpusPrivateRoot(root); got != "" {
		t.Errorf("a case root resolved the private checkout at %s", got)
	}
	t.Setenv(corpusPrivateRootEnv, "")
	if got := corpusPrivateRoot(root); got != "" {
		t.Errorf("a case root resolved the sibling checkout at %s", got)
	}
	if got := corpusPrivateStoreDocumentsDir(root); got != "" {
		t.Errorf("a case root resolved private documents at %s", got)
	}
}

// A document in a case stays in the case root even when its identifier carries a
// keep-private prefix: verdict routing belongs to the defense corpus.
func TestCaseDocumentsStayInTheCaseRoot(t *testing.T) {
	_, root := caseFixtureRoot(t)
	dir, err := corpusDocumentDir(root, "missile-note-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dir, root) {
		t.Errorf("document resolved outside the case root: %s", dir)
	}
}

// A case record names its case in the header, and the repository root keeps the
// header it always wrote so a rebuild of the defense corpus stays byte-identical.
func TestCaseRecordHeaderNamesTheCase(t *testing.T) {
	repository, root := caseFixtureRoot(t)
	inCase, err := encodeCorpusYAMLFor(root, map[string]string{"a": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(inCase), "over the fixture case.") {
		t.Errorf("case header missing:\n%s", inCase)
	}
	atRepository, err := encodeCorpusYAMLFor(repository, map[string]string{"a": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(atRepository), "over the Ukraine 2022 corpus.") {
		t.Errorf("repository header changed:\n%s", atRepository)
	}
}

// A case's declared tier passes envelope validation, and a tier nobody declares
// still fails.
func TestCaseSourceTypesAreRecognized(t *testing.T) {
	caseFixtureRoot(t)
	if !corpusRecognizedSourceType("fixture_tier") || !corpusRecognizedSourceType(sourceTypeHumint) {
		t.Error("declared tiers should be recognized")
	}
	if corpusRecognizedSourceType("undeclared") {
		t.Error("an undeclared tier should fail")
	}
}
