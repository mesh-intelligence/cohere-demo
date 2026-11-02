// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"net/http"
	"strings"
	"testing"
)

// ingestedTurn is what a correct run returns: rag0 composed over a document
// carrying the evidence only the ingested fixture has, at the identity the
// query was embedded with, and nothing excluded.
func ingestedTurn() chatResponse {
	var resp chatResponse
	resp.Answer = "The knowledge base describes specification-driven development as writing the specification first."
	resp.Trace.Status = "succeeded"
	resp.Metadata.QueryEmbeddingModel = "embed-v4.0"
	resp.Metadata.Sources.Composed = []chatbotSourceOutcome{
		composedSource("rag0", "The repository practices specification-driven development."),
	}
	return resp
}

func TestControlPlaneLiveTurnAcceptsAGroundedAnswer(t *testing.T) {
	if err := assertControlPlaneLiveTurnRetrieved(ingestedTurn(), http.StatusOK); err != nil {
		t.Fatalf("a turn composed over the ingested documents was rejected: %v", err)
	}
}

// The case the whole target exists for. A model that has read about the subject
// writes a plausible answer over an empty retrieval, so the evidence has to be
// in what a source composed from rather than in the prose.
func TestControlPlaneLiveTurnRejectsAnAnswerWithoutTheIngestedDocuments(t *testing.T) {
	resp := ingestedTurn()
	resp.Metadata.Sources.Composed = []chatbotSourceOutcome{
		composedSource("rag0", "The corpus describes strikes on Ukrainian infrastructure."),
	}
	err := assertControlPlaneLiveTurnRetrieved(resp, http.StatusOK)
	if err == nil {
		t.Fatal("a fluent answer composed from the pre-existing corpus was accepted")
	}
	if !strings.Contains(err.Error(), "newly ingested") {
		t.Errorf("error %q does not say the ingested documents were absent", err)
	}
}

// An excluded source means the ingest embedded at one family and the query at
// another, so the documents this target just wrote are unreachable.
func TestControlPlaneLiveTurnRejectsAnExcludedSource(t *testing.T) {
	resp := ingestedTurn()
	resp.Metadata.Sources.EmbeddingModelExcluded = []chatbotSourceOutcome{
		composedSource("rag0", ""),
	}
	err := assertControlPlaneLiveTurnRetrieved(resp, http.StatusOK)
	if err == nil {
		t.Fatal("a turn that excluded rag0 on embedding identity was accepted")
	}
	if !strings.Contains(err.Error(), "rag0") {
		t.Errorf("error %q does not name the excluded source", err)
	}
}

func TestControlPlaneLiveTurnRejectsAnEmptyOrFailedAnswer(t *testing.T) {
	empty := ingestedTurn()
	empty.Answer = "  "
	if err := assertControlPlaneLiveTurnRetrieved(empty, http.StatusOK); err == nil {
		t.Error("a 200 with an empty brief was accepted")
	}
	if err := assertControlPlaneLiveTurnRetrieved(
		ingestedTurn(), http.StatusInternalServerError); err == nil {
		t.Error("a non-200 turn was accepted")
	}
}

// The ingest leg is judged on the count the creator reported and the collection
// it actually wrote, not on the intent having been accepted.
func TestControlPlaneLiveIngestRequiresDocumentsWritten(t *testing.T) {
	report := controlPlaneLiveReport{Count: "2", Collection: controlPlaneLiveCollection}
	report.Trace.Status = "succeeded"
	if err := assertControlPlaneLiveIngested(report); err != nil {
		t.Fatalf("a report of two ingested documents was rejected: %v", err)
	}

	none := report
	none.Count = "0"
	err := assertControlPlaneLiveIngested(none)
	if err == nil {
		t.Fatal("an ingest that wrote nothing was accepted; the turn would compose from the old corpus")
	}
	if !strings.Contains(err.Error(), "pre-existing") {
		t.Errorf("error %q does not say why a zero count invalidates the turn", err)
	}

	// The intent carries the judged count again now that creator_rollout forwards
	// it, so an absent one is a broken contract rather than the shape of it.
	absent := report
	absent.Count = ""
	if err := assertControlPlaneLiveIngested(absent); err == nil {
		t.Error("a report carrying no count was accepted; the ingest leg reported nothing")
	}
}

// The documents have to land in the collection rag0 serves. Anywhere else and
// the turn cannot retrieve them, however healthy the ingest looked.
func TestControlPlaneLiveIngestRequiresTheServedCollection(t *testing.T) {
	report := controlPlaneLiveReport{Count: "2", Collection: "somewhere-else"}
	report.Trace.Status = "succeeded"
	err := assertControlPlaneLiveIngested(report)
	if err == nil {
		t.Fatal("an ingest into another collection was accepted")
	}
	for _, want := range []string{"somewhere-else", controlPlaneLiveCollection} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// A count that is not a number means the ingest leg reported no judged count,
// which is a different failure from having written nothing.
func TestControlPlaneLiveIngestRejectsAnUnreadableCount(t *testing.T) {
	report := controlPlaneLiveReport{Count: "not-a-number", Collection: controlPlaneLiveCollection}
	report.Trace.Status = "succeeded"
	if err := assertControlPlaneLiveIngested(report); err == nil {
		t.Fatal("a report carrying an unreadable count was accepted")
	}
}

func TestControlPlaneLiveIngestRejectsAFailedTrace(t *testing.T) {
	report := controlPlaneLiveReport{Count: "2", Collection: controlPlaneLiveCollection}
	report.Trace.Status = "failed"
	if err := assertControlPlaneLiveIngested(report); err == nil {
		t.Fatal("a failed trace was accepted")
	}
}

// The rollout leg is judged on the release moving. An intent that reported a
// health it read without helm ever running would otherwise pass the target.
func TestControlPlaneLiveRolloutRequiresTheReleaseToMove(t *testing.T) {
	if err := assertControlPlaneLiveRolledOut(3, 4); err != nil {
		t.Fatalf("a release that moved from revision 3 to 4 was rejected: %v", err)
	}
	for _, revisions := range [][2]int{{4, 4}, {4, 3}} {
		err := assertControlPlaneLiveRolledOut(revisions[0], revisions[1])
		if err == nil {
			t.Fatalf("a release still at revision %d after the intent was accepted", revisions[1])
		}
		if !strings.Contains(err.Error(), "rollout") {
			t.Errorf("error %q does not name the leg that did not run", err)
		}
	}
}

// The applier reads the chart it runs helm against from the ConfigMap the
// release names. Provisioning one under a different name stands the applier up
// with nothing at /chart, and every apply then fails on `path "/chart" not
// found` -- reported as a rejected patch, which is what sent GH-196 looking at
// the values document instead of the mount.
func TestControlPlaneLiveInstallMountsTheChartItProvisions(t *testing.T) {
	args := controlPlaneLiveInstallArgs("registry/runtime:tag", "registry/applier:tag", "/chart/path")
	want := "applier.chartArchiveConfigMap=" + controlPlaneLiveChartConfigMap
	if !containsArg(args, want) {
		t.Fatalf("the install does not set %s, so the applier mounts no chart:\n%s",
			want, strings.Join(args, " "))
	}
	if !strings.HasPrefix(controlPlaneLiveChartConfigMap, controlPlaneLiveRelease) {
		t.Errorf("the chart ConfigMap %q is not named for this release, so a second"+
			" scenario on the same cluster would read it",
			controlPlaneLiveChartConfigMap)
	}
}

// Both live failures on this target were the same defect: values naming an
// out-of-release ConfigMap that nothing created. The chart archive left the
// applier with no /chart; the UI assets left the collector and observer in
// PodInitializing, which only the rollout's --atomic --wait ever noticed. The
// install has to provision every ConfigMap its values name.
func TestControlPlaneLiveProvisionsEveryConfigMapItNames(t *testing.T) {
	var provisioned []string
	run := func(name string, args ...string) ([]byte, error) {
		if name == "kubectl" && len(args) > 2 && args[0] == "create" && args[1] == "configmap" {
			provisioned = append(provisioned, args[2])
		}
		return nil, nil
	}
	assets := []externalUIAsset{{
		Component:     "collector",
		ConfigMapName: "cplive-collector-ui",
		Archive:       t.TempDir() + "/assets.tgz",
	}}
	profiles := profilesExternalizationResult{
		Groups: []externalProfileGroup{{
			Group:         "chatbot",
			ConfigMapName: "cplive-profiles-chatbot",
			Dir:           t.TempDir(),
			Keys:          []string{"agents__chatbot__profile.yaml"},
		}},
	}
	if err := helmInstallControlPlaneLive(
		run, t.TempDir(), t.TempDir()+"/chart.tgz", "runtime:tag", "applier:tag",
		assets, profiles); err != nil {
		t.Fatalf("install: %v", err)
	}

	named := map[string]bool{}
	for _, arg := range append(
		controlPlaneLiveInstallArgs("runtime:tag", "applier:tag", t.TempDir()),
		externalUIAssetValueArgs(assets)...) {
		if _, value, found := strings.Cut(arg, "="); found {
			named[value] = true
		}
	}
	for _, group := range profiles.Groups {
		named[group.ConfigMapName] = true
	}

	for _, want := range []string{
		controlPlaneLiveChartConfigMap, "cplive-collector-ui", "cplive-profiles-chatbot"} {
		if !named[want] {
			t.Errorf("the install names no ConfigMap %q", want)
		}
		if !containsArg(provisioned, want) {
			t.Errorf("the install names ConfigMap %q but never creates it;"+
				" the workloads mounting it cannot start", want)
		}
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func TestImageTagOfSplitsTheReference(t *testing.T) {
	for reference, want := range map[string]string{
		"declarative-agents/applier:abc123": "abc123",
		"repo/name:v1.2.3":                  "v1.2.3",
		"bare-name":                         "latest",
	} {
		if got := imageTagOf(reference); got != want {
			t.Errorf("imageTagOf(%q) = %q, want %q", reference, got, want)
		}
	}
}
