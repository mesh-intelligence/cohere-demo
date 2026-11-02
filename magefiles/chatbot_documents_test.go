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

// fanoutWords reads the request fan-out declarations as a name-keyed map, so a
// test can assert on one word without depending on its position in the file.
func fanoutWords(t *testing.T) map[string]map[string]any {
	t.Helper()
	path := filepath.Join("..", "agents", "chatbot", "request-fanout-declarations.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var file struct {
		Tools []map[string]any `yaml:"tools"`
	}
	if err := yaml.Unmarshal(data, &file); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	words := make(map[string]map[string]any, len(file.Tools))
	for _, tool := range file.Tools {
		if name, _ := tool["name"].(string); name != "" {
			words[name] = tool
		}
	}
	return words
}

func wordConfig(t *testing.T, words map[string]map[string]any, name string) map[string]any {
	t.Helper()
	word, ok := words[name]
	if !ok {
		t.Fatalf("the fan-out declarations declare no %s word", name)
	}
	config, ok := word["config"].(map[string]any)
	if !ok {
		t.Fatalf("%s declares no config block", name)
	}
	return config
}

// Command cites the documents it is given, so the document identity decides the
// finest attribution the release can offer. One document per source can only be
// cited as that source; srd009 R4.2 needs a reader to open the passage that
// carried the assertion, which is the chunk (GH-126).
func TestDocumentsAreRenderedOnePerChunkKeyedByRecordID(t *testing.T) {
	words := fanoutWords(t)
	config := wordConfig(t, words, "render_documents")

	// Since rel10.2 the reranked rows are what Command composes from. The
	// unranked render still reads the flattened rows, and both emit the same
	// per-chunk shape, so the degraded path changes the order and nothing else.
	items, _ := config["items"].(string)
	if items != "$from(reorder_candidates).body.items" {
		t.Errorf("render_documents renders %q, want the reranked rows", items)
	}
	unranked := wordConfig(t, words, "render_documents_unranked")
	if got, _ := unranked["items"].(string); got != "$from(flatten_chunks).body.items" {
		t.Errorf("render_documents_unranked renders %q, want the flattened chunk rows;"+
			" the degraded path composes what the fan-out returned", got)
	}
	if unranked["item_template"] != config["item_template"] {
		t.Error("the reranked and unranked renders emit different document shapes;" +
			" a degradation must change the order and nothing else")
	}

	template, _ := config["item_template"].(string)
	if !strings.Contains(template, `"id": {{ json id }}`) {
		t.Errorf("the document id is not the chunk's record id:\n%s", template)
	}
	for _, absent := range []string{"input.name", "record_ids", "mapped.documents"} {
		if strings.Contains(template, absent) {
			t.Errorf("the document template still carries %q, which is the per-source shape:\n%s",
				absent, template)
		}
	}
	// The source stays reachable. A citation names a chunk, and a reader still
	// wants to know which unit served it.
	if !strings.Contains(template, `"source": {{ json source }}`) {
		t.Errorf("the document no longer carries its source name:\n%s", template)
	}
}

// The zip is only correct if the two arrays describe the same chunks in the
// same order. A rag-server returning mismatched ids and documents must fault
// rather than pair a chunk with another chunk's identity, which would produce
// citations that resolve to the wrong passage and look entirely healthy.
func TestFlattenChunksZipsIdsWithChunksAndCarriesTheSource(t *testing.T) {
	words := fanoutWords(t)
	word := words["flatten_chunks"]
	if word == nil {
		t.Fatal("the fan-out declarations declare no flatten_chunks word")
	}
	if init, _ := word["init"].(string); init != "flat_map" {
		t.Errorf("flatten_chunks init = %q, want flat_map; the equal-length requirement"+
			" that makes a mismatched rag-server response a fault comes from that word", init)
	}

	config := wordConfig(t, words, "flatten_chunks")
	if items, _ := config["items"].(string); items != "$from(partition_embedding_models).body.matched" {
		t.Errorf("flatten_chunks flattens %q, want the model-compatible partition", items)
	}

	element, ok := config["element_fields"].(map[string]any)
	if !ok {
		t.Fatal("flatten_chunks declares no element_fields, so nothing is zipped")
	}
	for field, want := range map[string]string{
		"id":   "result.structured_output.mapped.ids.0",
		"text": "result.structured_output.mapped.documents.0",
	} {
		got, _ := element[field].(string)
		if got != want {
			t.Errorf("element field %q addresses %q, want %q — the arrays arrive one list"+
				" per query and the fan-out sends one query", field, got, want)
		}
	}

	carry, ok := config["carry_fields"].(map[string]any)
	if !ok {
		t.Fatal("flatten_chunks carries no fields, so a chunk row loses its source")
	}
	if got, _ := carry["source"].(string); got != "input.name" {
		t.Errorf("carry field source = %q, want input.name", got)
	}
}

// A word the machine never reaches is a word that does not run. The state has
// to sit between the grounding render and the document render, because
// render_documents now reads its rows.
func TestFlatteningChunksStateRunsBeforeRenderingDocuments(t *testing.T) {
	path := filepath.Join("..", "agents", "chatbot", "request-machine.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var machine struct {
		States []struct {
			Name string `yaml:"name"`
		} `yaml:"states"`
		Signals []struct {
			Name string `yaml:"name"`
		} `yaml:"signals"`
		Transitions []struct {
			State  string `yaml:"state"`
			Signal string `yaml:"signal"`
			Next   string `yaml:"next"`
			Action string `yaml:"action"`
		} `yaml:"transitions"`
	}
	if err := yaml.Unmarshal(data, &machine); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	declared := func(names []struct {
		Name string `yaml:"name"`
	}, want string) bool {
		for _, n := range names {
			if n.Name == want {
				return true
			}
		}
		return false
	}
	if !declared(machine.States, "FlatteningChunks") {
		t.Error("the machine declares no FlatteningChunks state")
	}
	if !declared(machine.Signals, "ChunksFlattened") {
		t.Error("the machine declares no ChunksFlattened signal")
	}

	var reachesFlatten, entersPriorCheck, entersSearchCheck, entersMode, entersRerank, faults bool
	for _, tr := range machine.Transitions {
		switch {
		case tr.Action == "flatten_chunks" && tr.Next == "FlatteningChunks":
			reachesFlatten = true
		// Since rel10.2 the rerank pass runs between the chunk rows and the
		// document render; since rel12.1 the cascade-mode check sits in front
		// of it (srd011 R6.4), so the rows feed the mode check and the
		// default three-pass leg feeds the candidate list.
		// Since GH-604 the prior-sources check sits between them: a turn that
		// carries no cited record ids goes straight on to the mode check, and a
		// revision rejoins it once its carried rows are joined.
		case tr.State == "FlatteningChunks" && tr.Signal == "ChunksFlattened" && tr.Action == "check_prior_sources":
			entersPriorCheck = true
		// Since GH-611 the revision-search check follows it: a turn that is not a
		// revision goes on to the mode check.
		case tr.State == "CheckingPriorSources" && tr.Signal == "PriorSourcesAbsent" && tr.Action == "check_revision_search":
			entersSearchCheck = true
		case tr.State == "CheckingRevisionSearch" && tr.Signal == "RevisionSearchSkipped" && tr.Action == "check_cascade_mode":
			entersMode = true
		case tr.State == "CheckingRagCascadeMode" && tr.Signal == "CascadeThreePass" && tr.Action == "marshal_rag_candidates":
			entersRerank = true
		case tr.State == "FlatteningChunks" && tr.Signal == "CommandError" && tr.Next == "Failed":
			faults = true
		}
	}
	if !entersPriorCheck || !entersSearchCheck || !entersMode {
		t.Error("the flattened rows do not reach the cascade-mode check")
	}
	if !reachesFlatten {
		t.Error("no transition runs flatten_chunks into FlatteningChunks")
	}
	if !entersRerank {
		t.Error("the flattened rows do not reach the rerank pass, so nothing consumes them")
	}
	if !faults {
		t.Error("a flatten fault has no transition to Failed; a mismatched rag-server response would strand the turn")
	}
}

// A citation names an assertion span once and the documents it was computed
// against as an array, and that array can hold more than one document --
// verified live, where two documents asserting the same fact produced a single
// citation naming both. Reading one source per citation would surface one and
// silently drop the rest (GH-125).
func TestCitationsAreFlattenedPerSourceCarryingTheSpan(t *testing.T) {
	words := fanoutWords(t)
	config := wordConfig(t, words, "flatten_citations")

	if items, _ := config["items"].(string); items != "$from(invoke_cohere_command).body.mapped.citations" {
		t.Errorf("flatten_citations flattens %q, want the citations the composition returned", items)
	}
	element, ok := config["element_fields"].(map[string]any)
	if !ok {
		t.Fatal("flatten_citations declares no element_fields, so nothing fans out per source")
	}
	if got, _ := element["source"].(string); got != "sources" {
		t.Errorf("element field source = %q, want sources; the array itself is what fans out,"+
			" and an indexed path such as sources.0 would surface only the first", got)
	}
	carry, ok := config["carry_fields"].(map[string]any)
	if !ok {
		t.Fatal("flatten_citations carries no fields, so a row loses the span it belongs to")
	}
	for _, field := range []string{"start", "end", "text"} {
		if _, present := carry[field]; !present {
			t.Errorf("carry field %q is absent; every row a citation produces needs its span", field)
		}
	}
}

// A surfaced citation promises the reader can open what it names. One that the
// turn cannot resolve renders identically to one that resolves, so it is
// dropped and reported rather than surfaced (srd009 R4.4).
func TestUnresolvableCitedSourcesArePartitionedOutAndReported(t *testing.T) {
	words := fanoutWords(t)
	config := wordConfig(t, words, "partition_cited_sources")
	if items, _ := config["items"].(string); items != "$from(flatten_citations).body.items" {
		t.Errorf("partition_cited_sources reads %q, want the flattened citation rows", items)
	}
	if field, _ := config["field"].(string); field != "source.type" {
		t.Errorf("partition field = %q, want source.type", field)
	}

	// The surfaced list takes the resolvable partition, and the reported list
	// takes the rest. Rendering both from the same rows would surface a drop.
	surfaced := wordConfig(t, words, "render_citations")
	if items, _ := surfaced["items"].(string); items != "$from(flatten_merged_citations).body.items" {
		t.Errorf("render_citations renders %q, want the merged lanes -- admitted internal"+
			" citations then the externals the reader filter exempts (GH-378)", items)
	}
	dropped := wordConfig(t, words, "render_dropped_citations")
	if items, _ := dropped["items"].(string); items != "$from(partition_cited_sources).body.unmatched" {
		t.Errorf("render_dropped_citations renders %q, want the unresolvable partition", items)
	}
}

// An uncited answer and an all-dropped answer produce the same empty citation
// list and mean opposite things: the provider cited nothing, or it cited only
// sources the turn could not resolve. The response carries both lists so the
// two stay distinguishable (srd009 R4.5).
func TestResponseCarriesCitationsAndDropsSeparately(t *testing.T) {
	words := fanoutWords(t)
	config := wordConfig(t, words, "compose_response")
	inputs, ok := config["inputs"].(map[string]any)
	if !ok {
		t.Fatal("compose_response declares no inputs")
	}
	for field, want := range map[string]string{
		"citations":         "$from(citations_state).body.citations",
		"dropped_citations": "$from(citations_state).body.dropped_citations",
	} {
		if got, _ := inputs[field].(string); got != want {
			t.Errorf("response input %q reads %q, want %q", field, got, want)
		}
	}
	template, _ := config["template"].(string)
	for _, want := range []string{`"citations": {{ json citations }}`, `"dropped_citations": {{ json dropped_citations }}`} {
		if !strings.Contains(template, want) {
			t.Errorf("the response template omits %q:\n%s", want, template)
		}
	}
}
