// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// The labeled-tasking corpus (srd014 R1). These pin the labels to the store
// they label: every relevant document exists at its stated source type and at
// a clearance the tasking's level can retrieve, and each condition tag states
// a property the labels actually hold -- so a corpus edit that breaks a
// declared condition is a visible failure rather than a silent regression.

func readEvaluationCorpus(t *testing.T) evaluationCorpus {
	t.Helper()
	corpus, err := loadEvaluationCorpus("..")
	if err != nil {
		t.Fatalf("load evaluation corpus: %v", err)
	}
	return corpus
}

type storeEnvelope struct {
	DocumentID string `yaml:"document_id"`
	SourceType string `yaml:"source_type"`
	Clearance  string `yaml:"clearance"`
}

func readStoreEnvelopes(t *testing.T) map[string]storeEnvelope {
	t.Helper()
	// The labels reference the whole corpus, and the keep-private tiers live in
	// the private data checkout (srd017 R9.7). Without it beside the clone the
	// labels cannot resolve, which is a missing input rather than a defect.
	private := corpusPrivateStoreDocumentsDir("..")
	if private == "" {
		t.Skipf("the keep-private store tiers are not here; clone %s beside this"+
			" repository to run the evaluation-corpus cases", corpusPrivateDataRepo)
	}
	locations := []string{filepath.Join("..", "corpus", "store", "documents"), private}
	envelopes := map[string]storeEnvelope{}
	for _, root := range locations {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatalf("read store: %v", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, entry.Name(), "00-envelope.yaml"))
			if err != nil {
				continue
			}
			var envelope storeEnvelope
			if err := yaml.Unmarshal(data, &envelope); err != nil {
				t.Fatalf("parse envelope %s: %v", entry.Name(), err)
			}
			envelopes[envelope.DocumentID] = envelope
		}
	}
	if len(envelopes) == 0 {
		t.Fatal("the document store holds no envelopes")
	}
	return envelopes
}

// TestEvaluationLabelsResolveAgainstTheStore is srd014 R1.1 and the
// unit-level half of suite case 8: every labeled document exists, at its
// stated source type, at a clearance the tasking's own level can retrieve --
// a label above the tasking's level could never appear in a composed set and
// would silently depress recall.
func TestEvaluationLabelsResolveAgainstTheStore(t *testing.T) {
	t.Parallel()
	corpus := readEvaluationCorpus(t)
	if corpus.Version == "" || corpus.RetrievalCorpusVersion == "" {
		t.Fatal("the evaluation corpus must name its version and the retrieval corpus version (srd014 R1.4)")
	}
	if len(corpus.Taskings) == 0 {
		t.Fatal("the evaluation corpus holds no taskings")
	}
	envelopes := readStoreEnvelopes(t)
	for _, tasking := range corpus.Taskings {
		if len(tasking.Relevant) == 0 {
			t.Errorf("%s labels no relevant documents", tasking.ID)
		}
		for _, relevant := range tasking.Relevant {
			envelope, ok := envelopes[relevant.ID]
			if !ok {
				t.Errorf("%s labels %s, which is not in the store", tasking.ID, relevant.ID)
				continue
			}
			if envelope.SourceType != relevant.Source {
				t.Errorf("%s labels %s as %s; the store says %s",
					tasking.ID, relevant.ID, relevant.Source, envelope.SourceType)
			}
			// clearanceRank is the shared helper over the declared ordered set.
			if clearanceRank(envelope.Clearance) > clearanceRank(tasking.Clearance) {
				t.Errorf("%s (at %s) labels %s at %s; an over-clearance label can never be retrieved",
					tasking.ID, tasking.Clearance, relevant.ID, envelope.Clearance)
			}
		}
	}
}

// TestEvaluationConditionsHoldAsTagged is srd014 R1.3: each condition tag
// states a property of the labels, the three conditions are each carried by
// at least one tasking, and the taskings span more than one clearance level.
// The below-cutoff bound is the declared composition budget the rerank
// operation carries.
func TestEvaluationConditionsHoldAsTagged(t *testing.T) {
	t.Parallel()
	const compositionBudget = 8
	corpus := readEvaluationCorpus(t)
	coverage := map[string]int{}
	runLevels := map[string]bool{}
	for _, tasking := range corpus.Taskings {
		runLevels[tasking.Clearance] = true
		sources := map[string]bool{}
		levels := map[string]bool{}
		envelopes := readStoreEnvelopes(t)
		for _, relevant := range tasking.Relevant {
			sources[relevant.Source] = true
			if envelope, ok := envelopes[relevant.ID]; ok {
				levels[envelope.Clearance] = true
			}
		}
		tagged := map[string]bool{}
		for _, condition := range tasking.Conditions {
			tagged[condition] = true
			coverage[condition]++
		}
		if tagged["multi_source"] != (len(sources) > 1) {
			t.Errorf("%s: multi_source tag disagrees with the labels (%d source types)", tasking.ID, len(sources))
		}
		if tagged["multi_clearance"] != (len(levels) > 1) {
			t.Errorf("%s: multi_clearance tag disagrees with the labels (%d levels)", tasking.ID, len(levels))
		}
		if tagged["below_cutoff"] != (len(tasking.Relevant) > compositionBudget) {
			t.Errorf("%s: below_cutoff tag disagrees with the labels (%d relevant, budget %d)",
				tasking.ID, len(tasking.Relevant), compositionBudget)
		}
	}
	for _, condition := range []string{"multi_source", "multi_clearance", "below_cutoff"} {
		if coverage[condition] == 0 {
			t.Errorf("no tasking carries the %s condition", condition)
		}
	}
	if len(runLevels) < 2 {
		t.Error("the taskings run at fewer than two clearance levels (srd014 R1.3)")
	}
}
