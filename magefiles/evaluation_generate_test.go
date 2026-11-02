// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func generatedTestRecords() map[string]corpusChunkRecord {
	records := map[string]corpusChunkRecord{}
	add := func(chunkID, clearance string, embedded bool) {
		record := corpusChunkRecord{ChunkID: chunkID, Text: "prose for " + chunkID}
		record.Metadata.Clearance = clearance
		if embedded {
			record.Vectors = []corpusChunkVector{{}}
		}
		records[chunkID] = record
	}
	for _, day := range []string{"05", "16", "20", "22", "29"} {
		add("isw-2022-12-"+day+"-medium-900-0001", "unclassified", true)
		add("isw-2022-12-"+day+"-medium-900-0002", "restricted", true)
	}
	add("tg-rian_ru-187503-medium-900-0001", "unclassified", true)
	add("tg-rian_ru-187847-medium-900-0001", "unclassified", true)
	add("tg-rian_ru-188649-medium-900-0001", "unclassified", true)
	// Never embedded: must not appear in any sampled window.
	add("humint-2022-12-18-medium-900-0001", "secret", false)
	return records
}

func generatedTestManifest() corpusManifest {
	var manifest corpusManifest
	manifest.ClearanceLevels = []string{"unclassified", "restricted", "secret"}
	return manifest
}

func TestSampleGeneratedReferenceSetsFixesReferencesDeterministically(t *testing.T) {
	records := generatedTestRecords()
	manifest := generatedTestManifest()
	first, embedded := sampleGeneratedReferenceSets(records, manifest, 4, rand.New(rand.NewSource(13)))
	second, _ := sampleGeneratedReferenceSets(records, manifest, 4, rand.New(rand.NewSource(13)))
	if embedded != 13 {
		t.Fatalf("embedded population = %d, want 13 (the unembedded humint chunk excluded)", embedded)
	}
	if len(first) == 0 || len(first) != len(second) {
		t.Fatalf("sampling is not reproducible: %d vs %d sets", len(first), len(second))
	}
	for index := range first {
		if strings.Join(first[index].Documents, "|") != strings.Join(second[index].Documents, "|") {
			t.Fatalf("set %d differs across identical seeds", index)
		}
	}
}

func TestSampleGeneratedReferenceSetsClustersWithinATier(t *testing.T) {
	sets, _ := sampleGeneratedReferenceSets(
		generatedTestRecords(), generatedTestManifest(), 6, rand.New(rand.NewSource(7)))
	for _, set := range sets {
		if len(set.Documents) < generatedMinDocsPerSet {
			t.Fatalf("set %v is below the minimum window", set.Documents)
		}
		for _, doc := range set.Documents {
			if documentTier(doc) != set.Tier {
				t.Fatalf("set %v mixes tiers: %s is not %s", set.Documents, doc, set.Tier)
			}
			if strings.HasPrefix(doc, "humint-") {
				t.Fatalf("set %v sampled the unembedded humint document", set.Documents)
			}
		}
	}
}

func TestSampleGeneratedReferenceSetsRecordsTheFloorClearance(t *testing.T) {
	sets, _ := sampleGeneratedReferenceSets(
		generatedTestRecords(), generatedTestManifest(), 6, rand.New(rand.NewSource(7)))
	for _, set := range sets {
		if set.Tier == "isw" && set.Clearance != "restricted" {
			t.Fatalf("isw window carries restricted chunks; set clearance = %q", set.Clearance)
		}
		if set.Tier == "tg-rian_ru" && set.Clearance != "unclassified" {
			t.Fatalf("tg window is unclassified throughout; set clearance = %q", set.Clearance)
		}
	}
}

func TestGeneratedTaskingLeaksRejectsNamedReferences(t *testing.T) {
	set := generatedReferenceSet{Documents: []string{"isw-2022-12-16", "isw-2022-12-20"}}
	leaks := []string{
		"Assess the strikes reported in isw-2022-12-16.",
		"Summarize excerpt 2 on interceptor expenditure.",
		"What does the chunk say about drone waves?",
	}
	for _, tasking := range leaks {
		if !generatedTaskingLeaks(tasking, set) {
			t.Fatalf("leaking tasking accepted: %q", tasking)
		}
	}
	clean := "Assess the December strike waves against Ukrainian infrastructure and the air defence response."
	if generatedTaskingLeaks(clean, set) {
		t.Fatalf("clean tasking rejected: %q", clean)
	}
}

func TestDocumentTierStopsAtTheFirstNumericToken(t *testing.T) {
	cases := map[string]string{
		"isw-2022-12-16":     "isw",
		"tg-rian_ru-187503":  "tg-rian_ru",
		"missile-2022-12-20": "missile",
		"humint-2022-12-18":  "humint",
	}
	for doc, want := range cases {
		if got := documentTier(doc); got != want {
			t.Fatalf("documentTier(%q) = %q, want %q", doc, got, want)
		}
	}
}

func TestGeneratedCorpusRoundTripsThroughItsLoader(t *testing.T) {
	corpus := generatedEvaluationCorpus{
		ID: "evaluation-generated-test", Version: "1.0",
		RetrievalCorpus: "ukraine-2022", RetrievalCorpusVersion: "1.0",
		Synthetic: "mixed", SyntheticNotice: "generated for a test",
		Generator: generatedProvenance{Model: "command-a-03-2025", PromptVersion: "1.0", Seed: 13, SampledFrom: 13},
		Taskings: []generatedTasking{{
			ID: "gen-001", Clearance: "unclassified",
			Text: "Assess the December strike waves.", Report: "A short report.",
		}},
	}
	corpus.Taskings[0].Relevant = append(corpus.Taskings[0].Relevant, struct {
		ID     string `yaml:"id"`
		Source string `yaml:"source"`
	}{ID: "isw-2022-12-16", Source: "isw"})

	data, err := yaml.Marshal(corpus)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "evaluation"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(generatedCorpusPath)), data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadGeneratedEvaluationCorpus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Taskings[0].Relevant[0].ID != "isw-2022-12-16" || loaded.Generator.Seed != 13 {
		t.Fatalf("round trip lost fields: %+v", loaded)
	}
}

func TestSelectedEvaluationCorporaKeepsDenominatorsSeparate(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "evaluation"), 0o755); err != nil {
		t.Fatal(err)
	}
	analyst := "id: analyst\nversion: \"1.2\"\ntaskings:\n  - id: t-001\n    clearance: unclassified\n    text: a tasking\n    relevant: [{id: isw-2022-12-16, source: isw}]\n"
	generated := "id: generated\nversion: \"1.0\"\ntaskings:\n  - id: gen-001\n    clearance: unclassified\n    text: a generated tasking\n    relevant: [{id: isw-2022-12-20, source: isw}]\n"
	if err := os.WriteFile(filepath.Join(root, "evaluation", "corpus.yaml"), []byte(analyst), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "evaluation", "generated-corpus.yaml"), []byte(generated), 0o644); err != nil {
		t.Fatal(err)
	}
	both, err := selectedEvaluationCorpora(root, "both")
	if err != nil {
		t.Fatal(err)
	}
	if len(both) != 2 || both[0].ID != "analyst" || both[1].ID != "generated" {
		t.Fatalf("both = %+v", both)
	}
	if len(both[0].Taskings)+len(both[1].Taskings) != 2 {
		t.Fatalf("taskings leaked between corpora")
	}
	if _, err := selectedEvaluationCorpora(root, "neither"); err == nil {
		t.Fatal("invalid selector accepted")
	}
}

func TestApplyEvaluationSliceIsFirstNAndDeclared(t *testing.T) {
	corpus := evaluationCorpus{Taskings: make([]evaluationTasking, 5)}
	for i := range corpus.Taskings {
		corpus.Taskings[i].ID = string(rune('a' + i))
	}
	sliced := applyEvaluationSlice(corpus, 2)
	if len(sliced.Taskings) != 2 || sliced.Taskings[0].ID != "a" || sliced.Taskings[1].ID != "b" {
		t.Fatalf("slice is not first-n: %+v", sliced.Taskings)
	}
	if len(applyEvaluationSlice(corpus, 0).Taskings) != 5 || len(applyEvaluationSlice(corpus, 9).Taskings) != 5 {
		t.Fatalf("out-of-range slice altered the corpus")
	}
}

// The SPA's evalCorpus.json is a projection of the committed generated corpus;
// this is the sync gate that keeps the two from drifting (the /eval bubble
// must report the same taskings and references the harness scores).
func TestEvalUICorpusMatchesTheGeneratedCorpus(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Dir(root)
	corpus, err := loadGeneratedEvaluationCorpus(root)
	if err != nil {
		t.Skipf("no generated corpus at this checkout: %v", err)
	}
	want, err := json.MarshalIndent(projectEvalUICorpus(corpus), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(evalUICorpusPath)))
	if err != nil {
		t.Fatalf("read %s: %v (run mage evaluation:project)", evalUICorpusPath, err)
	}
	if string(got) != string(want)+"\n" {
		t.Fatalf("%s is out of sync with evaluation/generated-corpus.yaml: run mage evaluation:project", evalUICorpusPath)
	}
}

// A report is composed from keep-private excerpts, so it stays out of the tracked
// file while its copy in memory, which writes the private record, is untouched.
func TestGeneratedReportsNeverReachTheTrackedCorpus(t *testing.T) {
	corpus := generatedEvaluationCorpus{Taskings: []generatedTasking{{ID: "a", Text: "t", Report: "derived text"}}}
	data, err := yaml.Marshal(withoutGeneratedReports(corpus))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "derived text") || strings.Contains(string(data), "report") {
		t.Errorf("the tracked form carries a report:\n%s", data)
	}
	if corpus.Taskings[0].Report != "derived text" {
		t.Error("stripping altered the caller's corpus")
	}
}

// The committed file holds to the same rule.
func TestCommittedGeneratedCorpusCarriesNoReports(t *testing.T) {
	corpus, err := loadGeneratedEvaluationCorpus("..")
	if err != nil {
		t.Skipf("no generated corpus at this checkout: %v", err)
	}
	for _, tasking := range corpus.Taskings {
		if tasking.Report != "" {
			t.Errorf("%s carries a report; reports live in the private checkout (srd017 R9.7)", tasking.ID)
		}
	}
}
