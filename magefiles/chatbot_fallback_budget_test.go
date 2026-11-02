// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"path/filepath"
	"testing"
	"time"
)

// TestChatbotFallbackFitsTheRequestBudget pins the arithmetic that lets a
// stalled provider degrade instead of failing the turn.
//
// The Answering state maps every Cohere failure to the invoke_llm_fast
// fallback, but a provider that stalls rather than refusing holds the turn for
// the Cohere client's whole limit first. With the limit equal to the request
// budget, the fallback designed for provider failure could never run on the
// failure mode it exists for: the endpoint expired while invoke_llm_fast was
// still allowed to be answering (GH-225). So the chat endpoint must contain one
// worst-case stall plus the whole fallback answer, strictly, and the machine
// envelope must sit above the endpoint.
func TestChatbotFallbackFitsTheRequestBudget(t *testing.T) {
	var rest struct {
		Rest struct {
			Limits map[string]struct {
				Timeout string `yaml:"timeout"`
			} `yaml:"limits"`
		} `yaml:"rest"`
	}
	readIntakeYAML(t, filepath.Join(agentDir(t, "chatbot"), "rest.yaml"), &rest)
	cohere, ok := rest.Rest.Limits["cohere_provider"]
	if !ok {
		t.Fatal("the chatbot declares no cohere_provider limits")
	}
	stall, err := time.ParseDuration(cohere.Timeout)
	if err != nil {
		t.Fatalf("cohere_provider timeout %q: %v", cohere.Timeout, err)
	}
	// A revision turn reads the prior brief's cited records back before the
	// rerank (GH-604). The reads run in parallel, so they add one read window,
	// not one per unit.
	reads, ok := rest.Rest.Limits["local_rag_reads"]
	if !ok {
		t.Fatal("the chatbot declares no local_rag_reads limits")
	}
	revisionRead, err := time.ParseDuration(reads.Timeout)
	if err != nil {
		t.Fatalf("local_rag_reads timeout %q: %v", reads.Timeout, err)
	}

	// A revision also searches on its own proposals (GH-611): one proposal call
	// and one batched embed under the agentic limits, then one parallel read
	// window per proposed query, at most three.
	agentic, ok := rest.Rest.Limits["cohere_agentic"]
	if !ok {
		t.Fatal("the chatbot declares no cohere_agentic limits")
	}
	agenticCall, err := time.ParseDuration(agentic.Timeout)
	if err != nil {
		t.Fatalf("cohere_agentic timeout %q: %v", agentic.Timeout, err)
	}
	const maxProposals = 3
	search := 2*agenticCall + maxProposals*revisionRead

	fallback := invokeWordMaxTime(t, "invoke_llm_fast")
	endpoint := endpointBudget(t, "chatbot", "chat")

	if need := stall + fallback + revisionRead + search; endpoint <= need {
		t.Errorf("the chat endpoint allows %s while a provider stall (%s) plus the"+
			" fallback answer (%s) plus a revision's record read (%s) and search (%s)"+
			" needs %s: the fallback cannot run on the failure it exists for",
			endpoint, stall, fallback, revisionRead, search, need)
	}

	for _, machine := range []string{"request-machine.yaml", "request-machine-ollama.yaml"} {
		var spec struct {
			Budget struct {
				CommandTimeout string `yaml:"command_timeout"`
			} `yaml:"budget"`
		}
		readIntakeYAML(t, filepath.Join(agentDir(t, "chatbot"), machine), &spec)
		envelope, err := time.ParseDuration(spec.Budget.CommandTimeout)
		if err != nil {
			t.Fatalf("%s command_timeout %q: %v", machine, spec.Budget.CommandTimeout, err)
		}
		if envelope <= endpoint {
			t.Errorf("%s budget %s does not contain the %s endpoint bound", machine, envelope, endpoint)
		}
	}
}

// invokeWordMaxTime reads one invoke_llm word's max_time (seconds) from the
// request declarations.
func invokeWordMaxTime(t *testing.T, word string) time.Duration {
	t.Helper()
	var doc struct {
		Tools []struct {
			Name   string `yaml:"name"`
			Config struct {
				MaxTime int `yaml:"max_time"`
			} `yaml:"config"`
		} `yaml:"tools"`
	}
	readIntakeYAML(t, filepath.Join(agentDir(t, "chatbot"), "request-declarations.yaml"), &doc)
	for _, tool := range doc.Tools {
		if tool.Name == word && tool.Config.MaxTime > 0 {
			return time.Duration(tool.Config.MaxTime) * time.Second
		}
	}
	t.Fatalf("word %s declares no max_time", word)
	return 0
}
