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

// The rel11.0 suite's declared-configuration case (S10, srd010 AC6). It reads
// the shipped profile unexpanded, so it needs neither corpus nor network: what
// it settles is that the clearance filter is configuration the rag-server
// declares rather than something application code applies to a result set.
//
// The bare-token assertion is the load-bearing one. Embedded in a larger string
// the where map renders through Go's default formatting rather than as JSON,
// and Chroma cannot evaluate that -- a failure that surfaces as an unfiltered
// or rejected query at run time and as nothing at all in review.

// ragServerRest reads the rag-server REST surface unexpanded.
func ragServerRest(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join("..", "agents", "rag-server", "rest.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var file struct {
		REST map[string]any `yaml:"rest"`
	}
	if err := yaml.Unmarshal(data, &file); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file.REST
}

// ragQueryOperation returns the outbound Chroma query operation.
func ragQueryOperation(t *testing.T) map[string]any {
	t.Helper()
	rest := ragServerRest(t)
	clients, ok := rest["clients"].(map[string]any)
	if !ok {
		t.Fatal("the rag-server REST surface declares no clients block")
	}
	chroma, ok := clients["chroma"].(map[string]any)
	if !ok {
		t.Fatal("the rag-server declares no chroma client")
	}
	operations, ok := chroma["operations"].(map[string]any)
	if !ok {
		t.Fatal("the chroma client declares no operations")
	}
	operation, ok := operations["rag_query"].(map[string]any)
	if !ok {
		t.Fatal("the chroma client declares no rag_query operation")
	}
	return operation
}

// ragServerWords reads the request declarations as a name-keyed map.
func ragServerWords(t *testing.T) map[string]map[string]any {
	t.Helper()
	path := filepath.Join("..", "agents", "rag-server", "request-declarations.yaml")
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

func TestClearanceFilterIsDeclaredInTheQueryBodySchema(t *testing.T) {
	operation := ragQueryOperation(t)
	params, ok := operation["params"].(map[string]any)
	if !ok {
		t.Fatal("rag_query declares no params block")
	}
	schema, ok := params["body_schema"].(map[string]any)
	if !ok {
		t.Fatal("rag_query declares no body_schema")
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("the rag_query body_schema declares no properties")
	}
	where, ok := properties["where"].(map[string]any)
	if !ok {
		t.Fatal("the rag_query body_schema declares no where property; the filter would arrive undeclared")
	}
	// Typed as an object, which is what makes the renderer hand Chroma the map
	// rather than a string rendering of it.
	if kind, _ := where["type"].(string); kind != "object" {
		t.Fatalf("the where property is declared as %q, want object: a scalar type renders the filter as text Chroma cannot evaluate", kind)
	}
	required, ok := schema["required"].([]any)
	if !ok {
		t.Fatal("the rag_query body_schema declares no required list")
	}
	var found bool
	for _, item := range required {
		if name, _ := item.(string); name == "where" {
			found = true
		}
	}
	if !found {
		t.Error("where is not required by the rag_query body_schema; an omitted filter would issue an unfiltered query (srd010 R2.3)")
	}
}

// The two refusals are different outcomes and both serve nothing. An absent
// level violates the query contract and is turned away before a run starts; a
// level the declared set does not carry is a domain refusal the caller degrades
// on. This pins the first, which is the one a schema change would silently
// relax into an unfiltered query.
func TestTheInboundQueryContractRequiresAClearanceLevel(t *testing.T) {
	rest := ragServerRest(t)
	servers, ok := rest["servers"].(map[string]any)
	if !ok {
		t.Fatal("the rag-server declares no servers block")
	}
	requests, ok := servers["chroma_rag_requests"].(map[string]any)
	if !ok {
		t.Fatal("the rag-server declares no chroma_rag_requests server")
	}
	endpoints, _ := requests["endpoints"].(map[string]any)
	query, ok := endpoints["query"].(map[string]any)
	if !ok {
		t.Fatal("the request server declares no query endpoint")
	}
	request, _ := query["request"].(map[string]any)
	schema, ok := request["body_schema"].(map[string]any)
	if !ok {
		t.Fatal("the query endpoint declares no request body_schema")
	}
	required, _ := schema["required"].([]any)
	var found bool
	for _, item := range required {
		if name, _ := item.(string); name == "max_clearance" {
			found = true
		}
	}
	if !found {
		t.Error("the query endpoint does not require max_clearance; a query with no level would start a run the filter cannot bind (srd010 R2.1, R4.2)")
	}
}

func TestClearanceFilterIsSelectedFromCommandState(t *testing.T) {
	operation := ragQueryOperation(t)
	params, _ := operation["params"].(map[string]any)
	if source, _ := params["body_source"].(string); source != "command_state" {
		t.Fatalf("rag_query reads its body from %q, want command_state: the clearance words run between the resolve and the query, so a positional selector reads the wrong result", source)
	}
	mapping, ok := params["input_mapping"].(map[string]any)
	if !ok {
		t.Fatal("rag_query declares no input_mapping")
	}
	where, _ := mapping["where"].(string)
	if where != "$from(compose_clearance_filter).body.where" {
		t.Errorf("the where input is selected as %q, want $from(compose_clearance_filter).body.where", where)
	}
	// The enforced level is reported back (R2.5), and it is read from the
	// rendered window rather than echoed from the request -- so the response
	// says what was enforced and not what was asked for. Reading it from the
	// request would report a level the filter may never have applied.
	enforced, _ := mapping["clearance_enforced_at"].(string)
	if enforced != "$from(render_clearance_window).body.level" {
		t.Errorf("the enforced level is selected as %q, want $from(render_clearance_window).body.level", enforced)
	}
	if strings.Contains(enforced, "rag_resolve") {
		t.Error("the enforced level is echoed from the carried request; it must come from the resolved window (srd010 R2.5)")
	}
}

func TestClearanceFilterRendersAsABareSingleToken(t *testing.T) {
	operation := ragQueryOperation(t)
	body, ok := operation["body"].(map[string]any)
	if !ok {
		t.Fatal("rag_query declares no body")
	}
	where, ok := body["where"].(string)
	if !ok {
		t.Fatalf("the rag_query body renders where as %T, want the bare parameter token as a string", body["where"])
	}
	if strings.TrimSpace(where) != "{{ params.where }}" {
		t.Errorf("the where body value is %q, want exactly \"{{ params.where }}\": a token embedded in a larger string renders the map through Go's default formatting rather than as JSON, which Chroma cannot evaluate", where)
	}
}

func TestClearanceFilterLeavesChromaTransportAuthorityUnchanged(t *testing.T) {
	operation := ragQueryOperation(t)
	if method, _ := operation["method"].(string); method != "POST" {
		t.Errorf("the rag_query method is %q, want POST unchanged", method)
	}
	wantPath := "/api/v2/tenants/default_tenant/databases/default_database/collections/{collection}/query"
	if path, _ := operation["path"].(string); path != wantPath {
		t.Errorf("the rag_query path is %q, want %q unchanged", path, wantPath)
	}
	rest := ragServerRest(t)
	clients, _ := rest["clients"].(map[string]any)
	chroma, _ := clients["chroma"].(map[string]any)
	if auth, _ := chroma["auth_ref"].(string); auth != "none" {
		t.Errorf("the chroma client auth_ref is %q, want none unchanged", auth)
	}
	if limits, _ := chroma["limits_ref"].(string); limits != "local_rag_client" {
		t.Errorf("the chroma client limits_ref is %q, want local_rag_client unchanged: the egress allowlist rides on it", limits)
	}
	// No runtime-supplied base URL: the client's base_url stays the declared
	// env-defaulted value rather than becoming a mapped input.
	params, _ := operation["params"].(map[string]any)
	mapping, _ := params["input_mapping"].(map[string]any)
	for name := range mapping {
		switch name {
		case "base_url", "url", "host", "scheme", "port", "method", "path":
			t.Errorf("rag_query maps %q as a runtime input; the operation's transport authority must stay declared (srd010 R6.3)", name)
		}
	}
}

func TestClearanceWindowsDeclareTheOrderedSetAsAtOrBelowWindows(t *testing.T) {
	words := ragServerWords(t)
	word, ok := words["declare_clearance_windows"]
	if !ok {
		t.Fatal("the rag-server declares no declare_clearance_windows word")
	}
	config, ok := word["config"].(map[string]any)
	if !ok {
		t.Fatal("declare_clearance_windows declares no config")
	}
	// The set is trusted profile configuration, so the word takes no inputs.
	inputs, _ := config["inputs"].(map[string]any)
	if len(inputs) != 0 {
		t.Errorf("declare_clearance_windows reads %d input(s); the level set is trusted configuration and must not be request-derived", len(inputs))
	}
	template, _ := config["template"].(string)
	// Each declared level's window must be the level and everything below it.
	// Reading the rendered table is what catches a window that includes a level
	// above its own -- the one defect that would widen rather than narrow.
	var declared struct {
		Names []string `yaml:"names"`
		Items []struct {
			Level     string   `yaml:"level"`
			AtOrBelow []string `yaml:"at_or_below"`
		} `yaml:"items"`
	}
	if err := yaml.Unmarshal([]byte(template), &declared); err != nil {
		t.Fatalf("the declared window table is not parseable: %v", err)
	}
	if len(declared.Items) != len(declared.Names) {
		t.Fatalf("the table declares %d level name(s) and %d window(s); every declared level needs exactly one window",
			len(declared.Names), len(declared.Items))
	}
	position := make(map[string]int, len(declared.Names))
	for index, name := range declared.Names {
		position[name] = index
	}
	for _, item := range declared.Items {
		own, ok := position[item.Level]
		if !ok {
			t.Errorf("the table declares a window for %q, which is not in the declared ordered set", item.Level)
			continue
		}
		if len(item.AtOrBelow) != own+1 {
			t.Errorf("the %q window holds %d level(s), want %d: a window is the level and everything below it",
				item.Level, len(item.AtOrBelow), own+1)
		}
		for _, allowed := range item.AtOrBelow {
			at, ok := position[allowed]
			if !ok {
				t.Errorf("the %q window admits %q, which is not in the declared ordered set", item.Level, allowed)
				continue
			}
			if at > own {
				t.Errorf("the %q window admits %q, which is above it; the filter would widen rather than narrow", item.Level, allowed)
			}
		}
	}
}

func TestAnUnrecognizedClearanceLevelRefusesRatherThanDefaulting(t *testing.T) {
	words := ragServerWords(t)
	word, ok := words["resolve_clearance_window"]
	if !ok {
		t.Fatal("the rag-server declares no resolve_clearance_window word")
	}
	config, ok := word["config"].(map[string]any)
	if !ok {
		t.Fatal("resolve_clearance_window declares no config")
	}
	// Every non-matching outcome refuses. A level the declared set does not
	// carry must not fall through to a default window, which is the one shape
	// of this bug that serves more rather than less (srd010 R4.2).
	for _, outcome := range []string{"partial", "empty"} {
		if signal, _ := config[outcome].(string); signal != "ClearanceRefused" {
			t.Errorf("the %s outcome emits %q, want ClearanceRefused: an unrecognized level must refuse rather than resolve to a default", outcome, signal)
		}
	}
	if signal, _ := config["all_matched"].(string); signal != "ClearanceResolved" {
		t.Errorf("the all_matched outcome emits %q, want ClearanceResolved", signal)
	}
	// The vocabulary is the declaration, so the window is copied from trusted
	// configuration rather than from anything the request carried.
	if vocabulary, _ := config["vocabulary"].(string); vocabulary != "$from(declare_clearance_windows).body.items" {
		t.Errorf("the window vocabulary is %q, want the declared table", vocabulary)
	}
}

func TestTheRagServerAppliesNoPostQueryClearanceFilter(t *testing.T) {
	path := filepath.Join("..", "agents", "rag-server", "request-machine.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var machine struct {
		Transitions []struct {
			State  string `yaml:"state"`
			Signal string `yaml:"signal"`
			Next   string `yaml:"next"`
			Action string `yaml:"action"`
		} `yaml:"transitions"`
		TerminalStates []string `yaml:"terminal_states"`
	}
	if err := yaml.Unmarshal(data, &machine); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	// Nothing runs between the query answering and the terminal. A word there
	// would be a post-retrieval filter, which srd010 R2.3 forbids however
	// correct its output looked.
	for _, transition := range machine.Transitions {
		if transition.State == "Querying" && transition.Action != "" {
			t.Errorf("the machine runs %q after the query answers; a post-retrieval step cannot satisfy R2.3, which requires the restricted document never to have been a candidate",
				transition.Action)
		}
	}
	var refused bool
	for _, state := range machine.TerminalStates {
		if state == "ClearanceRefused" {
			refused = true
		}
	}
	if !refused {
		t.Error("the machine declares no ClearanceRefused terminal; a refusal would have to fall through to a generic failure a caller cannot distinguish (srd010 R5.1)")
	}
}
