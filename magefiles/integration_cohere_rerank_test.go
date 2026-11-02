// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"strings"
	"testing"
)

// The rerank case rel10.2 declares says the operation is one shared
// declaration, read unexpanded from the provider library the chatbot
// instantiates it from (srd058 R1.3).
func TestCohereRerankOperationIsPinned(t *testing.T) {
	client, err := cohereRerankerDeclaration(coreRootForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := assertCohereRerankOperationPinned(client); err != nil {
		t.Fatalf("pinned Cohere library: %v", err)
	}
}

func TestCohereRerankOperationRejectsARuntimeAuthority(t *testing.T) {
	client := restClientDeclaration{Operations: map[string]restOperationDeclaration{
		cohereRerankOperation: {
			Method: "POST", Path: cohereRerankPath, BaseURLSource: "trusted_topology",
		},
	}}
	err := assertCohereRerankOperationPinned(client)
	if err == nil {
		t.Fatal("a rerank operation taking its authority at runtime was accepted")
	}
	if !strings.Contains(err.Error(), "base_url_source") {
		t.Errorf("error %q does not name the field that made it fail", err)
	}
}

func TestCohereRerankOperationRejectsAMovedPath(t *testing.T) {
	client := restClientDeclaration{Operations: map[string]restOperationDeclaration{
		cohereRerankOperation: {Method: "POST", Path: "/v1/rerank"},
	}}
	if err := assertCohereRerankOperationPinned(client); err == nil {
		t.Fatal("a rerank operation on an unpinned path was accepted")
	}
}

func TestCohereRerankOperationRejectsAnAbsentDeclaration(t *testing.T) {
	client := restClientDeclaration{Operations: map[string]restOperationDeclaration{}}
	err := assertCohereRerankOperationPinned(client)
	if err == nil {
		t.Fatal("a client declaring no rerank operation was accepted")
	}
	if !strings.Contains(err.Error(), cohereRerankOperation) {
		t.Errorf("error %q does not name the missing operation", err)
	}
}
