// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The source datasets are held outside version control and are 343MB. They are
// ignored rather than merely untracked, because `git add -A` is what the
// working practice runs at the end of every unit and it would otherwise stage
// the whole of them.
//
// The rule is load-bearing and one careless edit to .gitignore removes it, so
// it is asserted rather than trusted. The per-tier READMEs are the exception
// that has to keep working: they record each tier's source, licence, and
// retrieval date (srd017 R9.2), which travels with the repository rather than
// with the data.
func TestDatasetPayloadIsIgnoredAndItsReadmesAreNot(t *testing.T) {
	ignored := []string{
		"data/ukraine_news/news.csv",
		"data/isw/isw_dailies.jsonl",
		"data/missile_attacks/missile_attacks_daily.csv",
		"data/war_losses/russia_losses_personnel.csv",
		"data/humint/humint_reports.jsonl",
		"data/humint/gen_humint.py",
	}
	for _, path := range ignored {
		if !gitIgnores(t, path) {
			t.Errorf("%s is not ignored; `git add -A` would stage the dataset payload", path)
		}
	}
	readmes := []string{
		"data/isw/README.md",
		"data/humint/README.md",
		"data/ukraine_news/README.md",
	}
	for _, path := range readmes {
		if gitIgnores(t, path) {
			t.Errorf("%s is ignored; the per-tier licence record has to be committable"+
				" without -f (srd017 R9.2)", path)
		}
	}
}

// gitIgnores reports whether git would ignore a path. check-ignore answers for
// paths that do not exist, so the case states the rule rather than depending on
// the datasets being present.
func gitIgnores(t *testing.T, path string) bool {
	t.Helper()
	command := exec.Command("git", "check-ignore", "-q", path)
	command.Dir = datasetIgnoreRoot(t)
	err := command.Run()
	if err == nil {
		return true
	}
	var exit *exec.ExitError
	if ok := asExitError(err, &exit); ok && exit.ExitCode() == 1 {
		return false
	}
	t.Skipf("git check-ignore is unavailable here: %v", err)
	return false
}

// datasetIgnoreRoot resolves the repository root from the package directory the
// test runs in, the same way the corpus cases resolve it.
func datasetIgnoreRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for _, candidate := range []string{working + "/..", working} {
		if _, err := os.Stat(candidate + "/.gitignore"); err == nil {
			return candidate
		}
	}
	t.Skipf("no .gitignore found from %s", working)
	return ""
}

func asExitError(err error, target **exec.ExitError) bool {
	exit, ok := err.(*exec.ExitError)
	if ok {
		*target = exit
	}
	return ok
}

// The rule names the dataset directory rather than every file in it, so a tier
// added later is ignored by default rather than by remembering to extend a
// list.
func TestDatasetIgnoreRuleCoversATierAddedLater(t *testing.T) {
	if !gitIgnores(t, "data/a_tier_nobody_has_added_yet/records.jsonl") {
		t.Error("a dataset tier added later would not be ignored")
	}
	if gitIgnores(t, "data/a_tier_nobody_has_added_yet/README.md") {
		t.Error("a dataset tier added later could not carry a committed README")
	}
}

// The keep-private store tiers (srd017 R9.7) are versioned in the private data
// repository, never here. Two layers hold that: the .gitignore rules asserted
// first, so a misdirected build output cannot be staged by `git add -A`, and
// the tracked-file sweep asserted second, so a file that somehow got committed
// anyway fails the suite naming itself.
func TestKeepPrivateStoreTiersAreNeitherIgnorableNorTracked(t *testing.T) {
	ignored := []string{
		"corpus/store/documents/isw-2022-12-01/00-envelope.yaml",
		"corpus/store/documents/tg-nytimes-1880/00-envelope.yaml",
		"corpus/store/documents/losses-2022-12/00-envelope.yaml",
		"corpus/store/documents/missile-2022-12-05/00-envelope.yaml",
	}
	for _, path := range ignored {
		if !gitIgnores(t, path) {
			t.Errorf("%s is not ignored; a misdirected corpus:build run would leave"+
				" keep-private chunks where `git add -A` stages them (srd017 R9.7)", path)
		}
	}
	tracked := []string{
		"corpus/store/documents/hr-22-008/00-envelope.yaml",
		"corpus/store/store.yaml",
	}
	for _, path := range tracked {
		if gitIgnores(t, path) {
			t.Errorf("%s is ignored; the publishable store has to stay a committable"+
				" repository artifact (srd015 R6.1)", path)
		}
	}

	command := exec.Command("git", "ls-files", "corpus/store/documents", "data")
	command.Dir = datasetIgnoreRoot(t)
	output, err := command.Output()
	if err != nil {
		t.Skipf("git ls-files is unavailable here: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		relative := strings.TrimPrefix(line, "corpus/store/documents/")
		if relative != line && corpusDocumentKeepPrivate(relative) {
			t.Errorf("%s is tracked; keep-private store tiers belong to %s"+
				" (srd017 R9.7)", line, corpusPrivateDataRepo)
		}
		if strings.HasPrefix(line, "data/") && !strings.HasSuffix(line, "README.md") {
			t.Errorf("%s is tracked; data/ carries READMEs alone (srd017 R9.2, R9.7)", line)
		}
	}
}
