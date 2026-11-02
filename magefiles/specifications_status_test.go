// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// specificationsStatusIndex is the part of SPECIFICATIONS.yaml that restates a
// status the corpus already records somewhere else: the roadmap summary per
// release, and the use-case and test-suite indexes per document.
type specificationsStatusIndex struct {
	RoadmapSummary []struct {
		Version string `yaml:"version"`
		Status  string `yaml:"status"`
	} `yaml:"roadmap_summary"`
	UseCaseIndex []struct {
		ID      string `yaml:"id"`
		Release string `yaml:"release"`
		Status  string `yaml:"status"`
		Path    string `yaml:"path"`
	} `yaml:"use_case_index"`
	CoverageGaps []struct {
		Release string `yaml:"release"`
		Status  string `yaml:"status"`
		Gap     string `yaml:"gap"`
	} `yaml:"coverage_gaps"`
}

func readSpecificationsStatusIndex(t *testing.T) (specificationsStatusIndex, string) {
	t.Helper()
	root := filepath.Dir(filepath.Dir(agentDir(t, "creator")))
	path := filepath.Join(root, "docs", "SPECIFICATIONS.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var index specificationsStatusIndex
	if err := yaml.Unmarshal(data, &index); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return index, root
}

// An index entry restates the status of a document that carries its own. When
// the two disagree, one of them is wrong and nothing says which, so a reader
// takes whichever they happened to open. rel07.0-uc001 sat this way: its
// use-case document read implemented and the index entry indexing that very
// document read partial, through every audit (GH-199).
func TestUseCaseIndexStatusMatchesTheDocument(t *testing.T) {
	index, root := readSpecificationsStatusIndex(t)
	if len(index.UseCaseIndex) == 0 {
		t.Fatal("SPECIFICATIONS.yaml carries no use_case_index; the scan would pass vacuously")
	}

	for _, entry := range index.UseCaseIndex {
		if entry.Path == "" {
			t.Errorf("use_case_index %s names no path, so its status answers to nothing", entry.ID)
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Path))
		if err != nil {
			t.Errorf("use_case_index %s: read %s: %v", entry.ID, entry.Path, err)
			continue
		}
		var document struct {
			Status string `yaml:"status"`
		}
		if err := yaml.Unmarshal(data, &document); err != nil {
			t.Errorf("use_case_index %s: parse %s: %v", entry.ID, entry.Path, err)
			continue
		}
		if document.Status == "" {
			t.Errorf("%s carries no status of its own, so the index entry cannot be checked",
				entry.Path)
			continue
		}
		if entry.Status != document.Status {
			t.Errorf("use_case_index %s reads %q and %s reads %q; the index contradicts"+
				" the document it indexes",
				entry.ID, entry.Status, entry.Path, document.Status)
		}
	}
}

// The roadmap summary counts a release done while the use-case index calls that
// release's only use case partial. Both restate the road map, so they cannot
// disagree with each other either.
func TestUseCaseIndexStatusMatchesTheRoadmapSummary(t *testing.T) {
	index, _ := readSpecificationsStatusIndex(t)

	summary := map[string]string{}
	for _, release := range index.RoadmapSummary {
		summary[release.Version] = release.Status
	}
	if len(summary) == 0 {
		t.Fatal("SPECIFICATIONS.yaml carries no roadmap_summary")
	}

	// A release whose every use case is implemented cannot itself be partial, and
	// a release recorded implemented cannot hold a use case that is not.
	perRelease := map[string][]string{}
	for _, entry := range index.UseCaseIndex {
		perRelease[entry.Release] = append(perRelease[entry.Release], entry.Status)
	}
	for release, statuses := range perRelease {
		declared, ok := summary[release]
		if !ok {
			t.Errorf("release %s appears in use_case_index and not in roadmap_summary", release)
			continue
		}
		if declared != "implemented" {
			continue
		}
		for _, status := range statuses {
			if status != "implemented" {
				t.Errorf("roadmap_summary records release %s implemented while a use case"+
					" in it reads %q", release, status)
			}
		}
	}
}

// The constitution settles one vocabulary for every status field -- planned,
// partial, implemented -- and TestRoadmapStatusVocabulary holds the road map to
// it. Nothing held the documents, so rel00.0-uc001 carried `specified`, a word
// the vocabulary does not contain, while both of its covering suite cases were
// implemented against live integration targets (GH-90, GH-199).
func TestUseCaseDocumentStatusDrawsFromTheVocabulary(t *testing.T) {
	index, root := readSpecificationsStatusIndex(t)
	allowed := map[string]bool{"planned": true, "partial": true, "implemented": true}

	for _, entry := range index.UseCaseIndex {
		if entry.Path == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Path))
		if err != nil {
			continue
		}
		var document struct {
			Status string `yaml:"status"`
		}
		if err := yaml.Unmarshal(data, &document); err != nil {
			continue
		}
		if document.Status != "" && !allowed[document.Status] {
			t.Errorf("%s reads status %q, which is not one of planned, partial, or"+
				" implemented; a fourth spelling breaks every counter that matches"+
				" the other three", entry.Path, document.Status)
		}
	}
}

// A coverage gap is a claim about a release, and it outlives the gap unless
// something checks it. The rel07.0 entry stayed after its listener lifecycle
// proof landed, so the corpus advertised a gap it had already closed.
func TestCoverageGapsNameReleasesThatAreNotImplemented(t *testing.T) {
	index, _ := readSpecificationsStatusIndex(t)

	summary := map[string]string{}
	for _, release := range index.RoadmapSummary {
		summary[release.Version] = release.Status
	}
	for _, gap := range index.CoverageGaps {
		declared, ok := summary[gap.Release]
		if !ok {
			continue
		}
		if declared == "implemented" {
			t.Errorf("coverage_gaps records release %s as %q (%s) while roadmap_summary"+
				" records it implemented", gap.Release, gap.Status, gap.Gap)
		}
	}
}
