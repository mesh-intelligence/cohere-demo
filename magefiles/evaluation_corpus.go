// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// The labeled-tasking corpus the srd014 evaluation runs over. The loader is
// production code because the harness is the corpus's consumer; the corpus
// tests read through the same types so the labels the tests validate are the
// labels the harness measures with.

type evaluationCorpus struct {
	ID                     string `yaml:"id"`
	Version                string `yaml:"version"`
	RetrievalCorpus        string `yaml:"retrieval_corpus"`
	RetrievalCorpusVersion string `yaml:"retrieval_corpus_version"`
	Synthetic              string `yaml:"synthetic"`
	SyntheticNotice        string `yaml:"synthetic_notice"`
	Assumptions            struct {
		FirstDraftBaselineMinutes int    `yaml:"first_draft_baseline_minutes"`
		FirstDraftBaselineBasis   string `yaml:"first_draft_baseline_basis"`
		AnalystEditMinutes        int    `yaml:"analyst_edit_minutes"`
		AnalystEditBasis          string `yaml:"analyst_edit_basis"`
	} `yaml:"assumptions"`
	Taskings []evaluationTasking `yaml:"taskings"`
}

type evaluationTasking struct {
	ID         string   `yaml:"id"`
	Clearance  string   `yaml:"clearance"`
	Conditions []string `yaml:"conditions"`
	Text       string   `yaml:"text"`
	Relevant   []struct {
		ID     string `yaml:"id"`
		Source string `yaml:"source"`
	} `yaml:"relevant"`
}

func loadEvaluationCorpus(root string) (evaluationCorpus, error) {
	var corpus evaluationCorpus
	data, err := os.ReadFile(filepath.Join(root, "evaluation", "corpus.yaml"))
	if err != nil {
		return corpus, fmt.Errorf("read evaluation corpus: %w", err)
	}
	if err := yaml.Unmarshal(data, &corpus); err != nil {
		return corpus, fmt.Errorf("parse evaluation corpus: %w", err)
	}
	if corpus.Version == "" || len(corpus.Taskings) == 0 {
		return corpus, fmt.Errorf("evaluation corpus is unversioned or empty")
	}
	return corpus, nil
}

// chunkProfileSuffix matches the store's chunk identity suffix. A composed id
// like isw-2022-12-16-medium-900-0002 belongs to document isw-2022-12-16; the
// profiles are the store's declared chunking profiles.
var chunkProfileSuffix = regexp.MustCompile(`(?i)-(atomic|medium-900|large-1800)-\d+$`)

// parentDocumentID maps a composed chunk id to the document the labels are
// written at. An id without a chunk suffix -- an external search result's URL
// identity, for instance -- is its own document.
func parentDocumentID(chunkID string) string {
	return chunkProfileSuffix.ReplaceAllString(chunkID, "")
}
