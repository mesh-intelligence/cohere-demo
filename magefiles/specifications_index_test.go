// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mesh-intelligence/declarative-agents/agent-core/pkg/spec"
	"gopkg.in/yaml.v3"
)

// The specification critic walks the corpus from docs/SPECIFICATIONS.yaml
// outward: it verifies that the path an index entry declares exists and that
// its traces resolve. Nothing walks the other way, so a corpus file that no
// entry declares is a file the gate never reads. rel09.0's use case and test
// suite sat outside both indexes from the moment the tree was vendored until a
// person happened to read the summary paragraph (GH-63). These tests close
// that direction.
//
// Only the three index sections that map one-to-one onto a corpus directory
// are reconcilable this way. role_alignment_index, foundation_document_index,
// config_format_index, and semantic_model_index point at scattered paths with
// no directory to enumerate, so they are outside this check.

type specificationsIndexEntry struct {
	ID   string `yaml:"id"`
	Path string `yaml:"path"`
}

type specificationsIndex struct {
	SRDs       []specificationsIndexEntry `yaml:"srd_index"`
	UseCases   []specificationsIndexEntry `yaml:"use_case_index"`
	TestSuites []specificationsIndexEntry `yaml:"test_suite_index"`
}

// indexedCorpusSection pairs one index section with the directory whose files
// it must account for.
type indexedCorpusSection struct {
	name      string
	directory string
	entries   func(specificationsIndex) []specificationsIndexEntry
}

var indexedCorpusSections = []indexedCorpusSection{
	{
		name:      "srd_index",
		directory: "docs/specs/software-requirements",
		entries:   func(index specificationsIndex) []specificationsIndexEntry { return index.SRDs },
	},
	{
		name:      "use_case_index",
		directory: "docs/specs/use-cases",
		entries:   func(index specificationsIndex) []specificationsIndexEntry { return index.UseCases },
	},
	{
		name:      "test_suite_index",
		directory: "docs/specs/test-suites",
		entries:   func(index specificationsIndex) []specificationsIndexEntry { return index.TestSuites },
	},
}

// specificationsRootForTest resolves the application root the way Audit does,
// from the working directory. Tests run at magefiles/, so the root is its
// parent. docs/ is application-local, unlike the catalog and agent-core
// checkouts that catalog_path_test.go resolves through demo.yaml.
func specificationsRootForTest(t *testing.T) string {
	t.Helper()
	return filepath.Clean("..")
}

func readSpecificationsIndex(t *testing.T, root string) specificationsIndex {
	t.Helper()
	path := filepath.Join(root, "docs", "SPECIFICATIONS.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var index specificationsIndex
	if err := yaml.Unmarshal(data, &index); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return index
}

// readCorpusDocumentID returns the top-level id of one corpus document.
func readCorpusDocumentID(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corpus document %s: %v", path, err)
	}
	var document struct {
		ID string `yaml:"id"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse corpus document %s: %v", path, err)
	}
	if document.ID == "" {
		t.Fatalf("corpus document %s declares no id", path)
	}
	return document.ID
}

// TestSpecificationsIndexCoversEveryCorpusFile fails when a document exists in
// a corpus directory that its index does not account for. That document is
// invisible to the critic, so nothing validates its traces, its status, or
// even that it parses.
func TestSpecificationsIndexCoversEveryCorpusFile(t *testing.T) {
	root := specificationsRootForTest(t)
	index := readSpecificationsIndex(t, root)
	for _, section := range indexedCorpusSections {
		t.Run(section.name, func(t *testing.T) {
			indexed := make(map[string]bool)
			for _, entry := range section.entries(index) {
				indexed[entry.ID] = true
			}
			if len(indexed) == 0 {
				t.Fatalf("%s indexes nothing; the section is missing or renamed", section.name)
			}
			pattern := filepath.Join(root, filepath.FromSlash(section.directory), "*.yaml")
			files, err := filepath.Glob(pattern)
			if err != nil {
				t.Fatalf("glob %s: %v", pattern, err)
			}
			if len(files) == 0 {
				t.Fatalf("no corpus documents under %s", section.directory)
			}
			for _, file := range files {
				if id := readCorpusDocumentID(t, file); !indexed[id] {
					t.Errorf("%s is absent from %s, so the gate never reads it; add an entry with id %s",
						filepath.ToSlash(filepath.Join(section.directory, filepath.Base(file))),
						section.name, id)
				}
			}
		})
	}
}

// TestSpecificationsIndexEntriesResolveToTheirDocuments fails when an entry
// names a path that does not exist, or a path whose document carries a
// different id. The critic checks that the path resolves; it does not check
// that the document there is the one the entry claims, so an entry repointed
// during a rename passes today while indexing the wrong document.
func TestSpecificationsIndexEntriesResolveToTheirDocuments(t *testing.T) {
	root := specificationsRootForTest(t)
	index := readSpecificationsIndex(t, root)
	for _, section := range indexedCorpusSections {
		t.Run(section.name, func(t *testing.T) {
			for _, entry := range section.entries(index) {
				if entry.Path == "" {
					t.Errorf("%s entry %s declares no path", section.name, entry.ID)
					continue
				}
				path := filepath.Join(root, filepath.FromSlash(entry.Path))
				if _, err := os.Stat(path); err != nil {
					t.Errorf("%s entry %s names a missing document %s", section.name, entry.ID, entry.Path)
					continue
				}
				if id := readCorpusDocumentID(t, path); id != entry.ID {
					t.Errorf("%s entry %s points at %s, which declares id %s",
						section.name, entry.ID, entry.Path, id)
				}
			}
		})
	}
}

// roadmapSummaryRow is one release line in the SPECIFICATIONS roadmap summary,
// the section a reader scans for release status without opening the road map.
type roadmapSummaryRow struct {
	Version       string `yaml:"version"`
	Name          string `yaml:"name"`
	UseCasesDone  int    `yaml:"use_cases_done"`
	UseCasesTotal int    `yaml:"use_cases_total"`
	Status        string `yaml:"status"`
}

type roadmapSummaryDocument struct {
	Rows []roadmapSummaryRow `yaml:"roadmap_summary"`
}

type roadmapRelease struct {
	Version  string `yaml:"version"`
	Name     string `yaml:"name"`
	Status   string `yaml:"status"`
	UseCases []struct {
		ID     string `yaml:"id"`
		Status string `yaml:"status"`
	} `yaml:"use_cases"`
	TestSuites []struct {
		ID     string `yaml:"id"`
		Status string `yaml:"status"`
	} `yaml:"test_suites"`
}

type roadmapDocument struct {
	Releases []roadmapRelease `yaml:"releases"`
}

func readRoadmapSummary(t *testing.T, root string) []roadmapSummaryRow {
	t.Helper()
	var document roadmapSummaryDocument
	readCorpusYAML(t, filepath.Join(root, "docs", "SPECIFICATIONS.yaml"), &document)
	if len(document.Rows) == 0 {
		t.Fatal("roadmap_summary is empty or missing")
	}
	return document.Rows
}

// TestPlannedPointersAreWithheldNotResolved pins the resolver behaviour this
// repository's evidence-pointer rule now rests on. A case with status `planned`
// withholds its go_test claim: the resolver skips inventory resolution, so the
// case can name the test it intends before that test exists, which is the order
// specification-driven development writes them in. The same pointer on a case
// that claims the test exists still has to resolve.
//
// The property lives upstream (agent-core pkg/spec, withheldClaim), so this
// repository does not implement it -- it depends on it. Before GH-150 the local
// rule was that planned cases declare no go_test at all, because the resolver
// validated every pointer regardless of status and a planned pointer failed the
// gate by construction. That rule was retired once the pin carried the
// withholding behaviour, and this test is what stops a later repin from
// reverting it silently: without the check, the corpus would go back to failing
// the moment a planned case named its intended evidence, and the reason would
// be an upstream change nothing here reported.
func TestPlannedPointersAreWithheldNotResolved(t *testing.T) {
	const (
		module  = "github.com/petar-djukic/cohere-demo"
		pkg     = module + "/magefiles"
		absent  = "TestNothingDeclaresThisYet"
		present = "TestPlannedPointersAreWithheldNotResolved"
	)
	inventory, err := spec.ParseGoTestInventory(
		module,
		pkg+"\n",
		fmt.Sprintf("{\"Action\":\"output\",\"Package\":%q,\"Output\":%q}\n", pkg, present+"\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	pointer := fmt.Sprintf("go test ./magefiles -run '^%s$'", absent)
	for _, testCase := range []struct {
		status      string
		wantFinding bool
		reason      string
	}{
		{status: "planned", wantFinding: false,
			reason: "a planned case names evidence it intends, which cannot resolve yet"},
		{status: "implemented", wantFinding: true,
			reason: "an implemented case claims the test exists, so the pointer must resolve"},
	} {
		suites := map[string]spec.TestSuite{
			"test-suite": {
				ID: "test-suite",
				TestCases: []spec.TestCase{
					{Name: "a case", Status: testCase.status, GoTest: pointer},
				},
			},
		}
		findings := spec.ValidateGoTestEvidence(inventory, suites)
		if got := len(findings) > 0; got != testCase.wantFinding {
			t.Errorf("status %q: finding = %v, want %v (%s); findings = %v",
				testCase.status, got, testCase.wantFinding, testCase.reason, findings)
		}
	}
}

// TestPlannedPointersStayWellFormed covers what the resolver still checks on a
// withheld claim. A bare Test-prefixed name that is not a valid top-level test
// identifier is reported whatever the status, so a planned pointer records an
// intention the resolver can hold the implementation to rather than an
// unparseable string nobody notices until promotion.
func TestPlannedPointersStayWellFormed(t *testing.T) {
	const (
		module  = "github.com/petar-djukic/cohere-demo"
		pkg     = module + "/magefiles"
		present = "TestPlannedPointersStayWellFormed"
	)
	inventory, err := spec.ParseGoTestInventory(
		module,
		pkg+"\n",
		fmt.Sprintf("{\"Action\":\"output\",\"Package\":%q,\"Output\":%q}\n", pkg, present+"\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	suites := map[string]spec.TestSuite{
		"test-suite": {
			ID: "test-suite",
			TestCases: []spec.TestCase{
				{Name: "a case", Status: "planned", GoTest: "Test not a valid identifier"},
			},
		},
	}
	if findings := spec.ValidateGoTestEvidence(inventory, suites); len(findings) == 0 {
		t.Error("malformed planned pointer produced no finding")
	}
}

func readRoadmapReleases(t *testing.T, root string) []roadmapRelease {
	t.Helper()
	var document roadmapDocument
	readCorpusYAML(t, filepath.Join(root, "docs", "road-map.yaml"), &document)
	if len(document.Releases) == 0 {
		t.Fatal("road-map.yaml declares no releases")
	}
	return document.Releases
}

func readCorpusYAML(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

// TestRoadmapStatusVocabulary holds every road-map status to the set the design
// constitution states (roadmap.status_values). One word means complete, and it
// is `implemented`.
//
// The vocabulary split silently before this test existed: rel08.0 alone said
// `done` while twenty other releases said `implemented` (GH-90). Nothing failed,
// because the summary mirrors whatever the road map says. The exposure is the
// use-case counter in TestRoadmapSummaryCoversEveryRelease, which counts a use
// case as done only when its status is exactly `implemented` -- so a use case
// marked with a synonym would understate its release while every test passed.
// A stated vocabulary is only worth stating if something enforces it.
func TestRoadmapStatusVocabulary(t *testing.T) {
	allowed := map[string]bool{"planned": true, "partial": true, "implemented": true}
	permitted := "planned, partial, implemented"

	for _, release := range readRoadmapReleases(t, specificationsRootForTest(t)) {
		if !allowed[release.Status] {
			t.Errorf("release %s status is %q; the road-map vocabulary is %s",
				release.Version, release.Status, permitted)
		}
		for _, useCase := range release.UseCases {
			if !allowed[useCase.Status] {
				t.Errorf("use case %s status is %q; the road-map vocabulary is %s",
					useCase.ID, useCase.Status, permitted)
			}
		}
		for _, suite := range release.TestSuites {
			if !allowed[suite.Status] {
				t.Errorf("test suite %s status is %q; the road-map vocabulary is %s",
					suite.ID, suite.Status, permitted)
			}
		}
	}
}

// TestRoadmapSummaryCoversEveryRelease reconciles the summary against the road
// map in both directions. The summary drifted twice before this test existed:
// GH-63 for the corpus indexes and GH-82 here, where eleven releases had no row
// and two more carried a status the road map had since changed. Both drifts ran
// the same direction, understating what is built, which is the misleading one
// for a repository someone else evaluates.
//
// The corpus-file check above cannot cover this section: it maps to road-map
// entries rather than to a directory of documents.
//
// Note that road-map.yaml does not list releases in version order -- 09.0 sits
// before 08.0 -- so this compares sets and fields, never sequence.
func TestRoadmapSummaryCoversEveryRelease(t *testing.T) {
	root := specificationsRootForTest(t)
	rows := readRoadmapSummary(t, root)
	releases := readRoadmapReleases(t, root)

	summarized := make(map[string]roadmapSummaryRow, len(rows))
	for _, row := range rows {
		if _, duplicate := summarized[row.Version]; duplicate {
			t.Errorf("roadmap_summary lists release %s twice", row.Version)
		}
		summarized[row.Version] = row
	}

	planned := make(map[string]roadmapRelease, len(releases))
	for _, release := range releases {
		planned[release.Version] = release
		row, ok := summarized[release.Version]
		if !ok {
			t.Errorf("release %s (%s) has no roadmap_summary row, so the summary understates the plan",
				release.Version, release.Name)
			continue
		}
		if row.Status != release.Status {
			t.Errorf("release %s status: roadmap_summary says %q, road-map.yaml says %q",
				release.Version, row.Status, release.Status)
		}
		if row.Name != release.Name {
			t.Errorf("release %s name: roadmap_summary says %q, road-map.yaml says %q",
				release.Version, row.Name, release.Name)
		}
		if want := len(release.UseCases); row.UseCasesTotal != want {
			t.Errorf("release %s use_cases_total = %d, road-map.yaml declares %d use case(s)",
				release.Version, row.UseCasesTotal, want)
		}
		done := 0
		for _, useCase := range release.UseCases {
			if useCase.Status == "implemented" {
				done++
			}
		}
		if row.UseCasesDone != done {
			t.Errorf("release %s use_cases_done = %d, road-map.yaml marks %d use case(s) implemented",
				release.Version, row.UseCasesDone, done)
		}
	}

	for version := range summarized {
		if _, ok := planned[version]; !ok {
			t.Errorf("roadmap_summary names release %s, which road-map.yaml does not declare", version)
		}
	}
}
