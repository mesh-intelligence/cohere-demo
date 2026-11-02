// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"strings"
	"testing"
)

// Each direction is one gcloud storage rsync whose argv is fixed: up mirrors
// the checkout into the bucket and prunes what the checkout no longer has,
// restore fills the checkout path and deletes nothing, and git internals plus
// the decompressed Telegram CSV never travel.
func TestSyncDataCommandsAreTheDocumentedRsyncs(t *testing.T) {
	up, err := gcpSyncDataCommand("up", "/work/cohere-demo-data", "demo-corpus")
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	joined := strings.Join(up, " ")
	for _, want := range []string{"storage rsync", "--recursive",
		"--delete-unmatched-destination-objects",
		"/work/cohere-demo-data gs://demo-corpus", gcpSyncDataExcludes} {
		if !strings.Contains(joined, want) {
			t.Errorf("up argv %q is missing %q", joined, want)
		}
	}

	restore, err := gcpSyncDataCommand("restore", "/work/cohere-demo-data", "demo-corpus")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	joined = strings.Join(restore, " ")
	if !strings.Contains(joined, "gs://demo-corpus /work/cohere-demo-data") {
		t.Errorf("restore argv %q does not copy bucket to checkout", joined)
	}
	if strings.Contains(joined, "--delete-unmatched-destination-objects") {
		t.Errorf("restore argv %q deletes; a restore fills and leaves the rest alone", joined)
	}

	if _, err := gcpSyncDataCommand("sideways", "/work", "demo-corpus"); err == nil {
		t.Error("an unknown direction should be refused")
	}
	if _, err := gcpSyncDataCommand("up", " ", "demo-corpus"); err == nil {
		t.Error("an empty checkout path should be refused")
	}
}

// The corpus bucket follows the state bucket's naming convention.
func TestCorpusBucketFollowsTheProjectNaming(t *testing.T) {
	settings := gcpSettings{Project: "demo-project"}
	if got := settings.corpusBucket(); got != "demo-project-corpus" {
		t.Errorf("corpus bucket is %q, want demo-project-corpus", got)
	}
}
