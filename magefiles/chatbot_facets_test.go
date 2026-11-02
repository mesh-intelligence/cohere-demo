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

// The facet contract (srd016 R4): facets reach retrieval as pre-search
// constraints through a faceted boundary beside the plain one, the unfaceted
// turn keeps its exact pre-facet shape, and nothing anywhere in the path
// reports a withheld count (srd010 R5.3, srd016 R7.3).

func readAgentYAML(t *testing.T, parts ...string) []byte {
	t.Helper()
	path := filepath.Join(append([]string{"..", "agents"}, parts...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func yamlDoc(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse yaml: %v", err)
	}
	return doc
}

// walk resolves a dotted path of map keys, failing the test when a step is
// absent or not a map.
func walk(t *testing.T, doc map[string]any, path ...string) map[string]any {
	t.Helper()
	current := doc
	for _, key := range path {
		next, ok := current[key].(map[string]any)
		if !ok {
			t.Fatalf("path %s: %q is absent or not a map", strings.Join(path, "."), key)
		}
		current = next
	}
	return current
}

// The unfaceted turn is unchanged (srd016 R4.4): the plain query operation
// carries no facet field, no document filter, and dispatches against the plain
// boundary, and the plain filter compose renders the bare clearance window
// with no conjunction wrapper.
func TestPlainQueryLegKeepsItsPreFacetShape(t *testing.T) {
	rest := yamlDoc(t, readAgentYAML(t, "chatbot", "rest.yaml"))
	query := walk(t, rest, "rest", "clients", "rag", "operations", "query")
	body, ok := query["body"].(map[string]any)
	if !ok {
		t.Fatal("plain query operation declares no body")
	}
	for _, forbidden := range []string{"facet_where", "keyword", "where", "where_document"} {
		if _, present := body[forbidden]; present {
			t.Errorf("plain query body carries %q; the unfaceted turn must stay byte-for-byte", forbidden)
		}
	}
	if path, _ := query["path"].(string); path != "/api/v1/rag/query" {
		t.Errorf("plain query path = %q, want the plain boundary", path)
	}

	ragRest := string(readAgentYAML(t, "rag-server", "rest.yaml"))
	ragDecl := string(readAgentYAML(t, "rag-server", "request-declarations.yaml"))
	if !strings.Contains(ragDecl,
		`template: '{"where": {"clearance": {"$in": {{ json allowed }}}}}'`) {
		t.Error("the plain clearance filter no longer renders the bare window; the unfaceted query changed shape")
	}
	plain := walk(t, yamlDoc(t, []byte(ragRest)),
		"rest", "clients", "chroma", "operations", "rag_query", "body")
	if _, present := plain["where_document"]; present {
		t.Error("the plain chroma query gained a document filter; the unfaceted turn must stay byte-for-byte")
	}
}

// The faceted filter is the caller's constraint conjoined under the resolved
// clearance window, clearance conjunct first and from the window alone, so no
// facet can widen what the unfaceted turn admits (srd016 R4.2, R4.3).
func TestFacetedFilterConjoinsUnderTheClearanceWindow(t *testing.T) {
	decl := string(readAgentYAML(t, "rag-server", "request-declarations.yaml"))
	want := `template: '{"where": {"$and": [{"clearance": {"$in": {{ json allowed }}}}, {{ json facet_where }}]}}'`
	if !strings.Contains(decl, want) {
		t.Fatalf("compose_faceted_query_filter does not conjoin the facet constraint under the clearance window:\nwant %s", want)
	}
	if !strings.Contains(decl, "allowed: $from(render_clearance_window).body.allowed") {
		t.Error("the faceted filter's window must come from the resolved declaration, never from the request")
	}
	if !strings.Contains(decl, "facet_where: $from(rag_resolve).body.carried.facet_where") {
		t.Error("the faceted filter's constraint must come from the carried request body")
	}
}

// The faceted chroma query binds the conjoined filter as a standalone token,
// the keyword as the store's document filter, and asks for metadatas
// (srd016 R4.2, R3.4).
func TestFacetedChromaQueryBindsWhereDocumentAndMetadatas(t *testing.T) {
	rest := yamlDoc(t, readAgentYAML(t, "rag-server", "rest.yaml"))
	op := walk(t, rest, "rest", "clients", "chroma", "operations", "rag_query_faceted")
	body, _ := op["body"].(map[string]any)
	if body == nil {
		t.Fatal("rag_query_faceted declares no body")
	}
	if where, _ := body["where"].(string); where != "{{ params.where }}" {
		t.Errorf("faceted where = %q, want the standalone single-token passthrough", where)
	}
	doc, _ := body["where_document"].(map[string]any)
	if doc == nil {
		t.Fatal("rag_query_faceted binds no where_document")
	}
	if contains, _ := doc["$contains"].(string); contains != "{{ params.keyword }}" {
		t.Errorf("where_document $contains = %q, want the keyword param", contains)
	}
	include, _ := body["include"].([]any)
	found := false
	for _, entry := range include {
		if entry == "metadatas" {
			found = true
		}
	}
	if !found {
		t.Error("rag_query_faceted include omits metadatas")
	}
}

// Both request machines route the turn through one facet decision to two legs
// fanning over the same selected topology under the same labels, so
// everything downstream reads one shape (srd016 R4.4).
func TestBothMachinesCarryTheSameFacetBranch(t *testing.T) {
	for _, name := range []string{"request-machine.yaml", "request-machine-ollama.yaml"} {
		machine := string(readAgentYAML(t, "chatbot", name))
		for _, want := range []string{
			// The facet decision follows the vector parse since the embed moved
			// behind source selection (GH-635); the fan still fans one embedding
			// over the selected topology.
			"signal: QueryVectorParsed, next: CheckingFacets, action: check_facets",
			"signal: FacetsAbsent",
			"signal: FacetsPresent",
			"action: rag_query_faceted",
		} {
			if !strings.Contains(machine, want) {
				t.Errorf("%s is missing %q", name, want)
			}
		}
		// Five legs since GH-730: plain (tiers absent), plain (fallback
		// selection parsed nothing), proposed-tier faceted, windowed faceted,
		// analyst faceted.
		if got := strings.Count(machine, "items: $from(selected_sources).body.selected"); got != 5 {
			t.Errorf("%s fans out over the selected topology %d times, want 5 (two plain, three faceted)", name, got)
		}
		if got := strings.Count(machine, "label: rag_query"); got != 5 {
			t.Errorf("%s labels %d fan-out legs rag_query, want all five", name, got)
		}
	}
}

// The presence check reads the carried facets through the shared embed label
// and treats null -- the seeded value of an absent field -- as absent, so a
// caller predating the facet contract lands on the plain leg.
func TestCheckFacetsTreatsNullAsAbsent(t *testing.T) {
	words := fanoutWords(t)
	config := wordConfig(t, words, "check_facets")
	if left, _ := config["left"].(string); left != "$from(query_embedding).body.carried.facets" {
		t.Errorf("check_facets left = %q, want the shared embed carry", left)
	}
	if op, _ := config["op"].(string); op != "empty" {
		t.Errorf("check_facets op = %q, want empty (null and {} both route to the plain leg)", op)
	}
	if config["satisfied"] != "FacetsAbsent" || config["unsatisfied"] != "FacetsPresent" {
		t.Errorf("check_facets signals = %v/%v, want FacetsAbsent/FacetsPresent",
			config["satisfied"], config["unsatisfied"])
	}
}

// The chat boundary accepts facets as an optional body field whose two members
// are required together, and forwards them in the body while the clearance
// stays a header (srd010 R3.1, srd016 R4.4).
func TestChatEndpointCarriesFacetsInBodyAndClearanceInHeader(t *testing.T) {
	rest := yamlDoc(t, readAgentYAML(t, "chatbot", "rest.yaml"))
	endpoint := walk(t, rest, "rest", "servers", "chatbot_chat", "endpoints", "chat")
	schema := walk(t, endpoint, "request", "body_schema")
	required, _ := schema["required"].([]any)
	for _, entry := range required {
		if entry == "facets" {
			t.Error("facets is required at the chat boundary; it must stay additive (srd016 R4.4)")
		}
	}
	facets := walk(t, schema, "properties", "facets")
	members, _ := facets["required"].([]any)
	if len(members) != 2 || members[0] != "facet_where" || members[1] != "keyword" {
		t.Errorf("facets requires %v, want both members so the faceted path composes one shape", members)
	}
	request := walk(t, endpoint, "machine_request", "request")
	body, _ := request["body"].(map[string]any)
	if body["facets"] != "$.facets" {
		t.Error("the chat machine request does not forward facets from the body")
	}
	if _, present := body["max_clearance"]; present {
		t.Error("the clearance must never become a body field a client composes (srd010 R3.1)")
	}
}

// The composed source outcomes carry each chunk's declared metadata for the
// source-evaluation table and the classification banner (srd016 R3.4), and the
// response templates report no withheld count and no pair of figures that
// yields one by subtraction (srd010 R5.3, srd016 R7.3).
func TestComposedSourcesCarryMetadatasAndNoWithheldCount(t *testing.T) {
	words := fanoutWords(t)
	render := wordConfig(t, words, "render_composed_sources")
	template, _ := render["item_template"].(string)
	if !strings.Contains(template, `"metadatas": {{ json result.structured_output.mapped.metadatas }}`) {
		t.Error("render_composed_sources drops the chunk metadata the interface projects (srd016 R3.4)")
	}
	compose := wordConfig(t, words, "compose_response")
	response, _ := compose["template"].(string)
	for _, forbidden := range []string{"withheld", "suppressed", "matched_total"} {
		if strings.Contains(response, forbidden) {
			t.Errorf("compose_response reports %q; srd010 R5.3 bars the withheld count from the response", forbidden)
		}
	}

	ragRest := string(readAgentYAML(t, "rag-server", "rest.yaml"))
	for _, want := range []string{"metadatas: $.mapped.metadatas"} {
		if got := strings.Count(ragRest, want); got < 2 {
			t.Errorf("rag-server serves %q on %d endpoints, want both query boundaries", want, got)
		}
	}
}

// The faceted rag-server boundary requires both facet operands, the same
// doctrine as the document filter: the store rejects an $and carrying an empty
// operand, so the stated absences are a no-op conjunct and the empty string
// rather than fields a machine branches around.
func TestFacetedEndpointRequiresBothFacetOperands(t *testing.T) {
	rest := yamlDoc(t, readAgentYAML(t, "rag-server", "rest.yaml"))
	endpoint := walk(t, rest, "rest", "servers", "chroma_rag_requests", "endpoints", "query_faceted")
	schema := walk(t, endpoint, "request", "body_schema")
	required, _ := schema["required"].([]any)
	want := map[string]bool{"facet_where": false, "keyword": false, "max_clearance": false}
	for _, entry := range required {
		if name, _ := entry.(string); name != "" {
			if _, tracked := want[name]; tracked {
				want[name] = true
			}
		}
	}
	for name, present := range want {
		if !present {
			t.Errorf("the faceted query boundary does not require %q", name)
		}
	}
	machineRequest := walk(t, endpoint, "machine_request")
	if machine, _ := machineRequest["machine"].(string); machine != "query-faceted-machine.yaml" {
		t.Errorf("faceted endpoint machine = %q, want query-faceted-machine.yaml", machine)
	}
}
