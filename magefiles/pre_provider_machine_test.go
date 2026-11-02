// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"path/filepath"
	"testing"
)

// machineSpec reads the fields the tracking check compares.
type machineSpec struct {
	States []struct {
		Name    string `yaml:"name"`
		Meaning string `yaml:"meaning"`
	} `yaml:"states"`
	Signals []struct {
		Name    string `yaml:"name"`
		Trigger string `yaml:"trigger"`
	} `yaml:"signals"`
	Transitions []struct {
		State  string `yaml:"state"`
		Signal string `yaml:"signal"`
		Next   string `yaml:"next"`
		Action string `yaml:"action"`
		Label  string `yaml:"label"`
	} `yaml:"transitions"`
}

func readMachineSpec(t *testing.T, name string) machineSpec {
	t.Helper()
	var spec machineSpec
	readIntakeYAML(t, filepath.Join(agentDir(t, "chatbot"), name), &spec)
	return spec
}

// preProviderActionFor maps a Cohere-machine action to its pre-provider
// counterpart where the two turns differ by provider. Everything else must
// match verbatim.
var preProviderActionFor = map[string]string{
	"embed_query_cohere":          "embed_query",
	"capture_query_vector_cohere": "capture_query_vector_ollama",
	// The tier is policy on the Cohere machine and a classification on the
	// pre-provider one (GH-425): with the Command tier deployed there is one
	// right composition word, while the pre-provider turn still chooses
	// between fast and deep.
	"declare_cohere_tier_choice": "select_tier",
}

// preProviderOnly is the one chain the pre-provider machine adds rather than
// drops (GH-746). compose_response reads rerank_status, osint_rerank_status,
// fusion_status, and composition_ids on every turn, the loader refuses a
// selector whose label no transition publishes, and only the Cohere tier's
// chains publish those four. So between the verdict and compose_response this
// tier publishes each one empty, which is what its response carried for them
// before the loader resolved selectors. Nothing else may be added.
var preProviderOnly = struct{ states, signals map[string]bool }{
	states: map[string]bool{
		"DeclaringRerankAbsent": true, "DeclaringOsintRerankAbsent": true,
		"DeclaringFusionAbsent": true, "DeclaringCompositionAbsent": true,
	},
	signals: map[string]bool{"LegAbsenceDeclared": true, "CompositionUnreported": true},
}

// TestPreProviderMachineTracksTheCohereMachine is the objection to two
// committed machines, answered. The pre-provider turn (srd009 R6.3) is a
// separate file because it differs from the Cohere machine in shape -- no
// rerank cascade, no Command composition, no citation chain -- and a change to
// the shared part of one machine that does not land in the other is exactly the
// drift a parallel copy invites. So every state, signal, and transition of the
// pre-provider machine must exist in the Cohere machine, verbatim, modulo the
// two provider actions and the one rejoin transition where the dropped chains
// used to sit (GH-224).
//
// The containment is deliberately one-directional: the Cohere machine carries
// chains the pre-provider machine must not, so a subset check is the invariant
// and an equality check would be wrong.
func TestPreProviderMachineTracksTheCohereMachine(t *testing.T) {
	cohere := readMachineSpec(t, "request-machine.yaml")
	ollama := readMachineSpec(t, "request-machine-ollama.yaml")

	// States the two tiers deliberately describe differently: the Cohere
	// machine's source selection is a forced native tool call (GH-607) and its
	// search-terms derivation exists only on that tier (GH-457), so the shared
	// state names carry tier-specific meanings.
	meaningDivergences := map[string]bool{
		"SelectingSources":   true,
		"ParsingSources":     true,
		"SeedingSearchTerms": true,
	}
	meanings := map[string]string{}
	for _, state := range cohere.States {
		meanings[state.Name] = state.Meaning
	}
	for _, state := range ollama.States {
		if meaningDivergences[state.Name] || preProviderOnly.states[state.Name] {
			continue
		}
		want, shared := meanings[state.Name]
		if !shared {
			t.Errorf("pre-provider state %s does not exist in the Cohere machine", state.Name)
			continue
		}
		if state.Meaning != want {
			t.Errorf("state %s meaning diverged:\n  cohere: %s\n  ollama: %s",
				state.Name, want, state.Meaning)
		}
	}

	triggers := map[string]string{}
	for _, signal := range cohere.Signals {
		triggers[signal.Name] = signal.Trigger
	}
	for _, signal := range ollama.Signals {
		if preProviderOnly.signals[signal.Name] {
			continue
		}
		want, shared := triggers[signal.Name]
		if !shared {
			t.Errorf("pre-provider signal %s does not exist in the Cohere machine", signal.Name)
			continue
		}
		if signal.Trigger != want {
			t.Errorf("signal %s trigger diverged:\n  cohere: %s\n  ollama: %s",
				signal.Name, want, signal.Trigger)
		}
	}

	type key struct{ state, signal string }
	cohereTransitions := map[key]string{}
	for _, tr := range cohere.Transitions {
		cohereTransitions[key{tr.State, tr.Signal}] = tr.Next + "/" + tr.Action + "/" + tr.Label
	}
	// The places the pre-provider machine rewires rather than drops, each
	// because a rerank chain the Cohere machine runs binds a client this
	// deployment does not declare: rendered grounding goes straight to prompt
	// composition past the RAG cascade and the Command documents array, and
	// the search branch's results render in engine order past the search
	// rerank pass (srd011 R3).
	rewires := map[key]string{
		{"RenderingDocuments", "GroundingRendered"}:     "SelectingTier/compose_prompt/",
		{"CheckingOsintResults", "OsintResultsPresent"}: "RenderingOsintDocuments/render_osint_documents_unranked/osint_documents",
		// GH-607: the Cohere machine wraps the prompt and forces a native tool
		// call; the pre-provider tier keeps the classifier path.
		{"ComposingSourceSelection", "SourceSelectionComposed"}: "SelectingSources/select_sources/",
		{"SelectingSources", "LLMResponded"}:                    "ParsingSources/parse_source_selection/parsed_selection",
		// GH-457: the search-terms derivation runs only on the Cohere tier;
		// the pre-provider machine seeds the raw question directly.
		{"CheckingOsintEnabled", "OsintEnabled"}: "SeedingSearchTerms/declare_search_terms_fallback/search_terms",
		// GH-746: the verdict enters the absent-leg chain instead of going
		// straight to compose_response.
		{"DeclaringConformanceVerdict", "ConformanceVerdictDeclared"}: "DeclaringRerankAbsent/declare_leg_absent/rerank_status",
	}
	var sharedTransitions int
	rejoin := cohereTransitions[key{"DeclaringConformanceVerdict", "ConformanceVerdictDeclared"}]
	rejoined := false
	for _, tr := range ollama.Transitions {
		k := key{tr.State, tr.Signal}
		if preProviderOnly.states[tr.State] {
			if !preProviderOnly.states[tr.Next] {
				if got := tr.Next + "/" + tr.Action + "/" + tr.Label; got != rejoin {
					t.Errorf("the absent-leg chain leaves at %s, want the Cohere verdict's own %s", got, rejoin)
				}
				rejoined = true
			}
			continue
		}
		if want, rewired := rewires[k]; rewired {
			if tr.Next+"/"+tr.Action+"/"+tr.Label != want {
				t.Errorf("the rewire transition changed: %s/%s -> %s action %s label %s, want %s",
					tr.State, tr.Signal, tr.Next, tr.Action, tr.Label, want)
			}
			continue
		}
		action := tr.Action
		for cohereAction, ollamaAction := range preProviderActionFor {
			if action == ollamaAction {
				action = cohereAction
			}
		}
		want, shared := cohereTransitions[k]
		if !shared {
			t.Errorf("pre-provider transition %s/%s does not exist in the Cohere machine",
				tr.State, tr.Signal)
			continue
		}
		if got := tr.Next + "/" + action + "/" + tr.Label; got != want {
			t.Errorf("transition %s/%s diverged:\n  cohere: %s\n  ollama: %s",
				tr.State, tr.Signal, want, got)
			continue
		}
		sharedTransitions++
	}
	if !rejoined {
		t.Error("the absent-leg chain never rejoins the Cohere machine's path")
	}
	if sharedTransitions < 50 {
		t.Fatalf("only %d shared transitions compared; the subset check would pass vacuously",
			sharedTransitions)
	}
}
