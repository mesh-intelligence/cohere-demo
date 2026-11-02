// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The external-content trust boundary (srd011 R7): search results reach the
// model only as documents in the composition set, carry no clearance, and
// have no declared route into source selection, tier selection, or the
// clearance chain. Suite cases 7 and 9's static halves -- the closure below
// is what makes "cannot influence" a reviewable fact rather than an observed
// absence.

// osintOutputConsumers is the complete set of words allowed to read the
// search branch's outputs, by the label or command each selector names. A
// word outside this set reading search output is a new route for
// attacker-influenced text and fails the closure.
var osintOutputConsumers = map[string]map[string]bool{
	// The raw search result: the snippet partition alone -- everything
	// downstream reads the partitioned rows, never the raw response.
	"$from(osint_search)": {
		"partition_search_results": true,
	},
	// The snippet-bearing rows: presence check, the search pass's candidate
	// marshal and reorder, and the unranked render.
	"$from(partition_search_results)": {
		"check_osint_results":             true,
		"marshal_osint_candidates":        true,
		"reorder_osint_results":           true,
		"render_osint_documents_unranked": true,
	},
	// The harvest queries fan (GH-612): the joined per-query searches flatten
	// once, and the flattened rows partition once.
	"$from(osint_searches)": {
		"flatten_search_rows": true,
	},
	"$from(search_rows)": {
		"partition_search_rows": true,
	},
	// The reranked, trimmed rows: the ordered document render alone.
	"$from(reorder_osint_results)": {
		"render_osint_documents": true,
	},
	// The search pass's ordering: its own render alone; the reorder reads the
	// same rows through the osint_search entry above.
	"$from(cohere_rerank_search)": {
		"check_osint_rerank_order": true,
		"reorder_osint_results":    true,
	},
	// The search pass outcome constant: response metadata alone.
	"$from(osint_rerank_status)": {
		"compose_response": true,
	},
	// The parsed union of both branches: the fusion's candidate render and
	// reorder alone -- still inside the composition set (srd011 R7.2).
	"$from(parse_union_documents)": {
		"marshal_fused_candidates": true,
		"reorder_fused_documents":  true,
	},
	// The fusion's ordering: its own render and the reorder alone.
	"$from(cohere_rerank_fused)": {
		"check_fused_rerank_order": true,
		"reorder_fused_documents":  true,
	},
	// The fused top-N rows: the final document render alone.
	"$from(reorder_fused_documents)": {
		"render_fused_documents": true,
	},
	// The fusion outcome constant: response metadata alone.
	"$from(fusion_status)": {
		"compose_response": true,
	},
	// The normalized documents: the presence check and the two compose
	// variants that put them into the documents list -- the one place srd011
	// R7.2 allows external content to reach the model.
	"$from(osint_documents)": {
		"check_osint_documents":        true,
		"compose_documents_augmented":  true,
		"compose_documents_osint_only": true,
	},
	// The branch outcome constant: response metadata alone.
	"$from(osint_status)": {
		"compose_response": true,
	},
}

type declaredTool struct {
	Name   string         `yaml:"name"`
	Config map[string]any `yaml:"config"`
}

func chatbotDeclaredTools(t *testing.T) map[string]string {
	t.Helper()
	dir := filepath.Join("..", "agents", "chatbot")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	tools := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		var doc struct {
			Tools []declaredTool `yaml:"tools"`
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		if err := yaml.Unmarshal(data, &doc); err != nil {
			// Files that are not tool declarations (machines, profiles, rest)
			// either fail to decode into this shape or carry no tools list;
			// both leave the map untouched.
			continue
		}
		for _, tool := range doc.Tools {
			if tool.Name == "" || tool.Config == nil {
				continue
			}
			rendered, err := yaml.Marshal(tool.Config)
			if err != nil {
				t.Fatalf("marshal %s config: %v", tool.Name, err)
			}
			tools[tool.Name] = string(rendered)
		}
	}
	if len(tools) == 0 {
		t.Fatal("no declared chatbot tools found")
	}
	return tools
}

// TestOSINTOutputReachesOnlyTheCompositionSet is srd011 R7.2 and R7.5 as a
// selector closure over every declared chatbot word: the search branch's
// outputs are read only by the declared consumer set, so no prompt
// composition, source selector, tier selector, or clearance word has a route
// to external content. The prompts the classifier and the grounding read
// (compose_source_selection_prompt, compose_prompt) are in the scanned set,
// and their absence from the consumer lists is the assertion.
func TestOSINTOutputReachesOnlyTheCompositionSet(t *testing.T) {
	t.Parallel()
	tools := chatbotDeclaredTools(t)
	for selector, allowed := range osintOutputConsumers {
		for name, config := range tools {
			if !strings.Contains(config, selector) {
				continue
			}
			if !allowed[name] {
				t.Errorf("word %s reads %s; external search output has no declared route there (srd011 R7.2, R7.5)",
					name, selector)
			}
		}
		for name := range allowed {
			config, ok := tools[name]
			if !ok {
				t.Errorf("declared consumer %s of %s does not exist", name, selector)
				continue
			}
			if !strings.Contains(config, selector) {
				t.Errorf("declared consumer %s no longer reads %s; update the closure", name, selector)
			}
		}
	}
}

// TestOSINTGroundingComposesFromRAGAlone is the prompt half of srd011 R7.2:
// the grounding text every tier's prompt is composed from renders the RAG
// partition and nothing else, so external content reaches the Cohere tier as
// documents and reaches no tier as prose outside them.
func TestOSINTGroundingComposesFromRAGAlone(t *testing.T) {
	t.Parallel()
	tools := chatbotDeclaredTools(t)
	grounding, ok := tools["render_grounding"]
	if !ok {
		t.Fatal("render_grounding is not declared")
	}
	if !strings.Contains(grounding, "$from(partition_embedding_models).body.matched") {
		t.Error("render_grounding no longer renders the RAG partition; re-establish where external content can reach a prompt")
	}
	prompt := tools["compose_prompt"]
	for _, banned := range []string{"osint", "$from(rag_documents)", "$from(composed_documents)"} {
		if strings.Contains(prompt, banned) {
			t.Errorf("compose_prompt reads %q; the grounding prompt must compose from the RAG partition alone", banned)
		}
	}
}

// TestOSINTDocumentsCarryNoClearance is srd011 R7.4: the normalized external
// document carries provenance and content, never a clearance field, so
// nothing downstream can mistake it for clearance-filtered internal material.
func TestOSINTDocumentsCarryNoClearance(t *testing.T) {
	t.Parallel()
	tools := chatbotDeclaredTools(t)
	render, ok := tools["render_osint_documents"]
	if !ok {
		t.Fatal("render_osint_documents is not declared")
	}
	for _, banned := range []string{"clearance", "max_clearance"} {
		if strings.Contains(render, banned) {
			t.Errorf("render_osint_documents carries %q; external documents sit outside the clearance model", banned)
		}
	}
	if !strings.Contains(render, `"source": "osint"`) {
		t.Error("render_osint_documents no longer marks the osint source; provenance is the R7.3 contract")
	}
}

// TestOSINTSelectionAndTierRunBeforeTheSearch pins the machine ordering that
// makes suite case 9's "cannot influence source selection" structural on the
// selection side: both machines run the source classifier and the fan-out
// before the Searching state, so no search result exists when sources are
// chosen. Tier selection runs after composition by design and is protected by
// the selector closure instead.
func TestOSINTSelectionAndTierRunBeforeTheSearch(t *testing.T) {
	t.Parallel()
	for _, machine := range []string{"request-machine.yaml", "request-machine-ollama.yaml"} {
		spec := readMachineSpec(t, machine)
		position := map[string]int{}
		for index, state := range spec.States {
			position[state.Name] = index
		}
		for _, before := range []string{"SelectingSources", "Retrieving"} {
			if position[before] >= position["Searching"] {
				t.Errorf("%s: state %s is not declared ahead of Searching", machine, before)
			}
		}
		// The search dispatches from the derivation states GH-457 inserted
		// behind the toggle; every one of them is reachable only through
		// CheckingOsintEnabled's enabled leg, so the gate is unchanged.
		allowedDispatch := map[string]bool{
			"CheckingOsintEnabled": true,
			"DerivingSearchTerms":  true,
			"SeedingSearchTerms":   true,
		}
		for _, tr := range spec.Transitions {
			if tr.Action == "osint_search" && !allowedDispatch[tr.State] {
				t.Errorf("%s: osint_search dispatched from %s; the branch runs only behind the toggle",
					machine, tr.State)
			}
		}
	}
}
