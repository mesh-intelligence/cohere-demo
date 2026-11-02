// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// The document read boundary is the presentation half of srd016 R5, and it is
// the place this release could most easily introduce an access-control failure:
// a second path to the same documents that does not run the filter the first
// one runs. These tests hold the two paths together at the declaration level,
// where a divergence is visible without a cluster.

type ragRESTDoc struct {
	Rest struct {
		Clients map[string]struct {
			Operations map[string]struct {
				Method string `yaml:"method"`
				Path   string `yaml:"path"`
				Params struct {
					BodySchema struct {
						Required   []string                          `yaml:"required"`
						Properties map[string]map[string]interface{} `yaml:"properties"`
					} `yaml:"body_schema"`
					InputMapping map[string]string `yaml:"input_mapping"`
				} `yaml:"params"`
				Body map[string]interface{} `yaml:"body"`
			} `yaml:"operations"`
		} `yaml:"clients"`
		Servers map[string]struct {
			Endpoints map[string]struct {
				Method  string `yaml:"method"`
				Path    string `yaml:"path"`
				Binding string `yaml:"binding"`
				Request struct {
					BodySchema struct {
						Required   []string                          `yaml:"required"`
						Properties map[string]map[string]interface{} `yaml:"properties"`
					} `yaml:"body_schema"`
				} `yaml:"request"`
				MachineRequest struct {
					Machine  string `yaml:"machine"`
					Response struct {
						TerminalSignals map[string]struct {
							Status int                    `yaml:"status"`
							Body   map[string]interface{} `yaml:"body"`
						} `yaml:"terminal_signals"`
					} `yaml:"response"`
				} `yaml:"machine_request"`
			} `yaml:"endpoints"`
		} `yaml:"servers"`
	} `yaml:"rest"`
}

type ragMachineDoc struct {
	Name        string `yaml:"name"`
	Transitions []struct {
		State  string `yaml:"state"`
		Signal string `yaml:"signal"`
		Next   string `yaml:"next"`
		Action string `yaml:"action"`
	} `yaml:"transitions"`
	TerminalStates []string `yaml:"terminal_states"`
}

func ragServerRoot(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(filepath.Dir(root), "agents", "rag-server")
}

func readRagREST(t *testing.T) ragRESTDoc {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ragServerRoot(t), "rest.yaml"))
	if err != nil {
		t.Fatalf("read rag-server rest.yaml: %v", err)
	}
	var doc ragRESTDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse rag-server rest.yaml: %v", err)
	}
	return doc
}

func readRagMachine(t *testing.T, name string) ragMachineDoc {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ragServerRoot(t), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var doc ragMachineDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return doc
}

// clearanceWordOrder returns the clearance words a machine runs, in order. The
// query path and both read paths must run the same ones in the same sequence;
// that identity is the property, not the individual words.
func clearanceWordOrder(machine ragMachineDoc) []string {
	wanted := map[string]bool{
		"declare_clearance_windows":   true,
		"declare_requested_clearance": true,
		"resolve_clearance_window":    true,
		"render_clearance_window":     true,
		"compose_clearance_filter":    true,
		"compose_document_filter":     true,
	}
	var order []string
	for _, transition := range machine.Transitions {
		if wanted[transition.Action] {
			order = append(order, transition.Action)
		}
	}
	return order
}

// TestDocumentReadRunsTheSameClearanceWordsAsTheQuery is the check that would
// catch the failure srd016 E2 describes: a serving path that reaches documents
// the retrieval filter withholds. Both read machines run the query machine's
// clearance sequence, and only the final compose differs -- the filter read
// conjoins the caller's constraint, the identifier read does not have one.
func TestDocumentReadRunsTheSameClearanceWordsAsTheQuery(t *testing.T) {
	t.Parallel()
	query := clearanceWordOrder(readRagMachine(t, "request-machine.yaml"))
	if len(query) == 0 {
		t.Fatal("the query machine runs no clearance words; this guard went blind")
	}

	byID := clearanceWordOrder(readRagMachine(t, "document-machine.yaml"))
	if len(byID) != len(query) {
		t.Fatalf("the identifier read runs %v, the query runs %v", byID, query)
	}
	for i := range query {
		if byID[i] != query[i] {
			t.Errorf("clearance word %d: query runs %q, identifier read runs %q", i, query[i], byID[i])
		}
	}

	byFilter := clearanceWordOrder(readRagMachine(t, "document-filter-machine.yaml"))
	if len(byFilter) != len(query) {
		t.Fatalf("the filter read runs %v, the query runs %v", byFilter, query)
	}
	for i := range query[:len(query)-1] {
		if byFilter[i] != query[i] {
			t.Errorf("clearance word %d: query runs %q, filter read runs %q", i, query[i], byFilter[i])
		}
	}
	if last := byFilter[len(byFilter)-1]; last != "compose_document_filter" {
		t.Errorf("the filter read composes with %q; it must conjoin the caller constraint"+
			" with the clearance window", last)
	}
}

// TestDocumentReadRefusalIsSeparableFromFailure holds srd010 R4.2 on the new
// path: a level the declared set does not recognize refuses the read without
// issuing it, and that is a terminal of its own rather than a fall-through to
// the boundary-failure terminal. A caller degrading on a refusal and a caller
// retrying on a failure need to tell them apart.
func TestDocumentReadRefusalIsSeparableFromFailure(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"document-machine.yaml", "document-filter-machine.yaml"} {
		machine := readRagMachine(t, name)

		var refusalTerminal string
		for _, transition := range machine.Transitions {
			if transition.Signal == "ClearanceRefused" {
				refusalTerminal = transition.Next
			}
		}
		if refusalTerminal == "" {
			t.Errorf("%s: no transition handles ClearanceRefused, so an unrecognized level"+
				" falls through to whatever comes next", name)
			continue
		}
		if refusalTerminal == "Failed" {
			t.Errorf("%s: a refused clearance lands in Failed, which a caller cannot tell"+
				" from a broken boundary", name)
		}

		terminal := map[string]bool{}
		for _, state := range machine.TerminalStates {
			terminal[state] = true
		}
		if !terminal[refusalTerminal] {
			t.Errorf("%s: %q is not a terminal state", name, refusalTerminal)
		}

		// The refusal must precede the read: a machine that refuses after
		// reading has already read.
		for _, transition := range machine.Transitions {
			if transition.State == refusalTerminal && transition.Action != "" {
				t.Errorf("%s: %q runs %q, so the refusal is not terminal",
					name, refusalTerminal, transition.Action)
			}
		}
	}
}

// TestDocumentReadsBindTheFilterFromTheClearanceWords keeps the store call
// honest about where its filter comes from. A read whose where-clause is bound
// from the request rather than from the clearance words is a read the caller
// can widen, which is the access-control bypass srd016 R5.3 exists to prevent.
func TestDocumentReadsBindTheFilterFromTheClearanceWords(t *testing.T) {
	t.Parallel()
	operations := readRagREST(t).Rest.Clients["chroma"].Operations

	byIDs, ok := operations["rag_get_by_ids"]
	if !ok {
		t.Fatal("the chroma client declares no rag_get_by_ids operation")
	}
	if got := byIDs.Params.InputMapping["where"]; got != "$from(compose_clearance_filter).body.where" {
		t.Errorf("the identifier read binds where from %q; it must come from the clearance words", got)
	}
	if got := byIDs.Params.InputMapping["ids"]; got != "$from(rag_resolve_documents).body.carried.record_ids" {
		t.Errorf("the identifier read binds ids from %q", got)
	}

	byFilter, ok := operations["rag_get_by_filter"]
	if !ok {
		t.Fatal("the chroma client declares no rag_get_by_filter operation")
	}
	if got := byFilter.Params.InputMapping["where"]; got != "$from(compose_document_filter).body.where" {
		t.Errorf("the filter read binds where from %q; it must come from the conjoining compose", got)
	}
	// An empty id list is not an absent restriction: the store reads it as zero
	// identifiers and returns nothing. The filter read must therefore carry no
	// ids field at all rather than an empty one.
	if _, present := byFilter.Body["ids"]; present {
		t.Error("the filter read sends an ids field; an empty id list returns nothing" +
			" rather than lifting the restriction")
	}

	// The where token has to stand alone in the body, or the map renders through
	// Go's default formatting instead of JSON and the store cannot evaluate it
	// (srd010 R2.2).
	for name, operation := range map[string]string{
		"rag_get_by_ids":    asString(byIDs.Body["where"]),
		"rag_get_by_filter": asString(byFilter.Body["where"]),
	} {
		if operation != "{{ params.where }}" {
			t.Errorf("%s renders where as %q; the token must stand alone", name, operation)
		}
	}
}

func asString(value interface{}) string {
	text, _ := value.(string)
	return text
}

// TestDocumentEndpointsRequireAClearanceLevel holds the read contract to the
// query contract: a read carrying no level is malformed and turned away before
// a run starts, rather than running at some default the endpoint chooses.
func TestDocumentEndpointsRequireAClearanceLevel(t *testing.T) {
	t.Parallel()
	endpoints := readRagREST(t).Rest.Servers["chroma_rag_requests"].Endpoints

	for _, name := range []string{"documents_by_id", "documents_by_filter"} {
		endpoint, ok := endpoints[name]
		if !ok {
			t.Errorf("the rag-server declares no %s endpoint", name)
			continue
		}
		required := map[string]bool{}
		for _, field := range endpoint.Request.BodySchema.Required {
			required[field] = true
		}
		if !required["max_clearance"] {
			t.Errorf("%s does not require max_clearance, so a read without one runs anyway", name)
		}
		if endpoint.Binding != "machine_request" {
			t.Errorf("%s binds %q", name, endpoint.Binding)
		}
		signals := endpoint.MachineRequest.Response.TerminalSignals
		if refusal, ok := signals["ClearanceRefused"]; !ok {
			t.Errorf("%s maps no ClearanceRefused response", name)
		} else if refusal.Status != http.StatusForbidden {
			t.Errorf("%s maps ClearanceRefused to %d, want 403", name, refusal.Status)
		}
		// Lineage reaches the caller parsed rather than as the store's encoded
		// metadata field (srd016 R5.6).
		if read, ok := signals["DocumentsRead"]; !ok {
			t.Errorf("%s maps no DocumentsRead response", name)
		} else if _, carried := read.Body["metadatas"]; !carried {
			t.Errorf("%s returns no metadatas, so the caller cannot recover lineage"+
				" without rejoining against the store", name)
		}
	}

	// The filter endpoint's constraint must be non-empty: the store rejects a
	// conjunction carrying an empty operand, and a filter defaulted to {} is a
	// different read from the one the caller asked for.
	filter := endpoints["documents_by_filter"]
	constraint := filter.Request.BodySchema.Properties["document_filter"]
	if constraint["minProperties"] == nil {
		t.Error("documents_by_filter accepts an empty document_filter; the store rejects" +
			" a conjunction with an empty operand")
	}
}

// TestChromaGetSemanticsTheBoundaryDependsOn pins the three store behaviours the
// declarations are built on, against a live Chroma. Each was verified by hand
// while designing the boundary, and each would be invisible in the declarations
// if it changed: the endpoints would still be well-formed and the reads would
// return the wrong thing.
func TestChromaGetSemanticsTheBoundaryDependsOn(t *testing.T) {
	if os.Getenv("CHROMA_SEMANTICS") == "" {
		t.Skip("needs a reachable Chroma holding the document collection; set CHROMA_SEMANTICS=1")
	}
	collection, err := chromaResolveCollection(corpusDocumentsCollection)
	if err != nil {
		t.Fatalf("resolve the document collection: %v", err)
	}
	url := chromaCollectionsURL + "/" + collection + "/get"

	read := func(body string) (int, []string) {
		t.Helper()
		data, status, err := requestHTTP(http.MethodPost, url, body)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var response struct {
			IDs []string `json:"ids"`
		}
		if status == http.StatusOK {
			if err := json.Unmarshal(data, &response); err != nil {
				t.Fatalf("decode: %v", err)
			}
		}
		return status, response.IDs
	}

	// An empty id list is zero identifiers, not an absent restriction. This is
	// why the filter read is its own operation rather than the identifier read
	// with the field omitted.
	if _, ids := read(`{"ids":[],"where":{"clearance":{"$in":["unclassified"]}},"limit":2}`); len(ids) != 0 {
		t.Errorf("an empty id list returned %d records; the filter read could then reuse"+
			" the identifier operation", len(ids))
	}

	// A conjunction with an empty operand is rejected, which is why the filter
	// endpoint requires a non-empty caller constraint.
	if status, _ := read(`{"where":{"$and":[{"clearance":{"$in":["unclassified"]}},{}]},"limit":1}`); status == http.StatusOK {
		t.Error("a conjunction with an empty operand was accepted; the endpoint's" +
			" minProperties constraint is guarding nothing")
	}

	// An identifier read under a filter that excludes it returns an empty id
	// list rather than an error. This is what makes an uncleared identifier
	// answer as absent rather than refused (srd016 R5.7) without the boundary
	// doing anything itself.
	status, ids := read(`{"ids":["isw-2022-12-01#envelope"],"where":{"clearance":{"$in":["secret"]}}}`)
	if status != http.StatusOK {
		t.Errorf("an excluded identifier read returned %d; it must answer as absent, not refuse", status)
	}
	if len(ids) != 0 {
		t.Errorf("an excluded identifier returned %d records", len(ids))
	}
}
