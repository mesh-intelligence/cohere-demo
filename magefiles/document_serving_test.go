// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The rel10.5 document-serving suite. It proves the read API resolves a cited
// chunk to its parent and its versions, and that the requester's clearance
// governs that path exactly as it governs retrieval.
//
// The clearance cases are the ones that matter. rel11.0 filters the retrieval
// collection before the vector search so an over-clearance document is never a
// candidate; reading by identifier reaches exactly those documents. A
// presentation path returning what retrieval withheld would undo that release
// while looking like a feature.

// documentCollectionStub is a Chroma stand-in holding rows in memory. It
// answers the two shapes the reader uses -- get by ids and get by where -- so
// the clearance rule is proven without standing a database up.
type documentCollectionStub struct {
	rows    []stubRow
	server  *httptest.Server
	queries []map[string]interface{}
}

type stubRow struct {
	id       string
	text     string
	metadata map[string]interface{}
}

func newDocumentCollectionStub(t *testing.T, rows []stubRow) *documentCollectionStub {
	t.Helper()
	stub := &documentCollectionStub{rows: rows}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.HasSuffix(r.URL.Path, "/get") {
			var query map[string]interface{}
			_ = json.Unmarshal(body, &query)
			stub.queries = append(stub.queries, query)
			stub.respond(w, query)
			return
		}
		// Collection resolution.
		_, _ = w.Write([]byte(`{"id":"collection-1"}`))
	})
	stub.server = httptest.NewServer(mux)
	t.Cleanup(stub.server.Close)
	return stub
}

// respond answers a get. The matching is deliberately simple: it covers the
// filters the reader builds and nothing more, so a reader that started emitting
// a shape Chroma would reject fails here rather than passing on a stub that was
// more forgiving than the database.
func (s *documentCollectionStub) respond(w http.ResponseWriter, query map[string]interface{}) {
	var ids []string
	var texts []string
	var metadatas []map[string]interface{}

	wanted := map[string]bool{}
	if raw, held := query["ids"]; held {
		for _, id := range raw.([]interface{}) {
			wanted[id.(string)] = true
		}
	}
	for _, row := range s.rows {
		if len(wanted) > 0 {
			if !wanted[row.id] {
				continue
			}
		} else if where, held := query["where"]; held {
			if !stubMatches(row, where.(map[string]interface{})) {
				continue
			}
		}
		ids = append(ids, row.id)
		texts = append(texts, row.text)
		metadatas = append(metadatas, row.metadata)
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ids": ids, "documents": texts, "metadatas": metadatas,
	})
}

// stubMatches evaluates the $and / $eq / $gte / $lte shapes the reader builds.
func stubMatches(row stubRow, where map[string]interface{}) bool {
	if clauses, held := where["$and"]; held {
		for _, clause := range clauses.([]interface{}) {
			if !stubMatches(row, clause.(map[string]interface{})) {
				return false
			}
		}
		return true
	}
	for field, condition := range where {
		value, _ := row.metadata[field].(string)
		for operator, want := range condition.(map[string]interface{}) {
			text, _ := want.(string)
			switch operator {
			case "$eq":
				if value != text {
					return false
				}
			case "$gte":
				if value < text {
					return false
				}
			case "$lte":
				if value > text {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}

func servingTestLevels() []string {
	return []string{"unclassified", "restricted", "confidential", "secret"}
}

// servingStubRows is one document at each of three levels, each with an
// envelope row and two chunk rows at different profiles.
func servingStubRows() []stubRow {
	rows := []stubRow{}
	for _, document := range []struct {
		id        string
		clearance string
		issued    string
		tier      string
	}{
		{"isw-2022-12-16", "unclassified", "2022-12-16", sourceTypeOSINTAssessment},
		{"HR-22-017", "confidential", "2022-12-06", sourceTypeHumint},
		{"HR-22-019", "secret", "2022-12-22", sourceTypeHumint},
	} {
		rows = append(rows, stubRow{
			id:   envelopeRecordID(document.id),
			text: "the whole document " + document.id,
			metadata: map[string]interface{}{
				"kind": documentKindEnvelope, "document_id": document.id,
				"source_type": document.tier, "clearance": document.clearance,
				"issued": document.issued, "title": "Document " + document.id,
			},
		})
		for _, profile := range []string{chunkProfileMedium, chunkProfileLarge} {
			chunkID := chunkIdentifier(document.id, profile, 1)
			parent, _ := json.Marshal(corpusChunkParent{
				DocumentID: document.id, Position: 1, SiblingCount: 1,
				CharSpan: []int{0, 10},
			})
			rows = append(rows, stubRow{
				id:   chunkID,
				text: "a passage of " + document.id,
				metadata: map[string]interface{}{
					"kind": documentKindChunk, "document_id": document.id,
					"source_type": document.tier, "clearance": document.clearance,
					"issued": document.issued, "title": "Document " + document.id,
					"chunk_profile": profile, "parent": string(parent),
				},
			})
		}
	}
	return rows
}

func servingReader(t *testing.T, stub *documentCollectionStub, clearance string) corpusDocumentReader {
	t.Helper()
	reader, err := newCorpusDocumentReader(corpusDocumentsCollection, clearance, servingTestLevels())
	if err != nil {
		t.Fatalf("build a reader at %s: %v", clearance, err)
	}
	reader.BaseURL = stub.server.URL + "/collections"
	return reader
}

// A cited chunk identifier resolves to its chunk, its parent document, and the
// document's other profile versions (srd017 R8.4, R8.5, rel10.5-uc001 S9).
func TestReadAPIResolvesACitedChunkToItsParentAndVersions(t *testing.T) {
	stub := newDocumentCollectionStub(t, servingStubRows())
	reader := servingReader(t, stub, "secret")

	cited := chunkIdentifier("HR-22-019", chunkProfileMedium, 1)
	chunk, found, err := reader.GetChunk(cited)
	if err != nil || !found {
		t.Fatalf("GetChunk(%s) found=%v err=%v", cited, found, err)
	}
	if chunk.Parent == nil || chunk.Parent.DocumentID != "HR-22-019" {
		t.Fatalf("the cited chunk carries no resolvable lineage: %+v", chunk.Parent)
	}
	if chunk.Text == "" {
		t.Error("the cited chunk came back with no text")
	}

	document, found, err := reader.GetDocument(chunk.Parent.DocumentID)
	if err != nil || !found {
		t.Fatalf("GetDocument(%s) found=%v err=%v", chunk.Parent.DocumentID, found, err)
	}
	if document.Kind != documentKindEnvelope {
		t.Errorf("GetDocument returned a %s row", document.Kind)
	}
	if !strings.Contains(document.Text, "the whole document") {
		t.Errorf("GetDocument returned %q rather than the document", document.Text)
	}

	versions, err := reader.GetVersions(chunk.Parent.DocumentID)
	if err != nil {
		t.Fatalf("GetVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("GetVersions returned %d chunks, want the 2 profiles", len(versions))
	}
	for _, version := range versions {
		if version.Kind != documentKindChunk {
			t.Errorf("GetVersions returned a %s row", version.Kind)
		}
		if version.DocumentID != chunk.Parent.DocumentID {
			t.Errorf("GetVersions returned a chunk of %s", version.DocumentID)
		}
	}
}

// The facet query answers with documents, filtered on clearance, source type,
// and issued date (srd017 R8.4).
func TestReadAPIListsDocumentsByFacet(t *testing.T) {
	stub := newDocumentCollectionStub(t, servingStubRows())
	reader := servingReader(t, stub, "secret")

	all, err := reader.ListDocuments(corpusDocumentFilter{})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("an unfiltered list returned %d documents, want 3", len(all))
	}
	for _, record := range all {
		if record.Kind != documentKindEnvelope {
			t.Errorf("the list returned a %s row; facets select sources rather than passages",
				record.Kind)
		}
	}

	humint, err := reader.ListDocuments(corpusDocumentFilter{SourceType: sourceTypeHumint})
	if err != nil {
		t.Fatalf("ListDocuments by tier: %v", err)
	}
	if len(humint) != 2 {
		t.Errorf("the humint tier returned %d documents, want 2", len(humint))
	}

	december, err := reader.ListDocuments(corpusDocumentFilter{
		IssuedFrom: "2022-12-10", IssuedTo: "2022-12-31"})
	if err != nil {
		t.Fatalf("ListDocuments by date: %v", err)
	}
	if len(december) != 2 {
		t.Errorf("the date range returned %d documents, want 2", len(december))
	}
	for _, record := range december {
		if record.Issued < "2022-12-10" || record.Issued > "2022-12-31" {
			t.Errorf("document %s issued %s is outside the range", record.DocumentID, record.Issued)
		}
	}
}

// An over-clearance document is unreachable by identifier, as a version of a
// permitted document, and through a facet query (srd017 R8.7,
// rel10.5-uc001 S9). The identifier is supplied directly rather than followed
// from a citation, because identifiers derive from source and position and a
// path safe only against identifiers the turn already returned is not an
// access control.
func TestReadAPIWithholdsAnOverClearanceDocumentEveryWay(t *testing.T) {
	stub := newDocumentCollectionStub(t, servingStubRows())
	reader := servingReader(t, stub, "confidential")

	if _, found, err := reader.GetDocument("HR-22-019"); err != nil || found {
		t.Errorf("GetDocument returned a secret document to a confidential requester"+
			" (found=%v err=%v)", found, err)
	}
	secretChunk := chunkIdentifier("HR-22-019", chunkProfileMedium, 1)
	if _, found, err := reader.GetChunk(secretChunk); err != nil || found {
		t.Errorf("GetChunk returned a secret chunk to a confidential requester"+
			" (found=%v err=%v)", found, err)
	}
	versions, err := reader.GetVersions("HR-22-019")
	if err != nil {
		t.Fatalf("GetVersions: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("GetVersions returned %d versions of a secret document", len(versions))
	}
	listed, err := reader.ListDocuments(corpusDocumentFilter{})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	for _, record := range listed {
		if record.Clearance == "secret" {
			t.Errorf("the facet query listed secret document %s to a confidential"+
				" requester", record.DocumentID)
		}
	}
	if len(listed) != 2 {
		t.Errorf("a confidential requester saw %d documents, want the 2 at or below", len(listed))
	}
}

// The clearance is applied on every path out of the collection rather than in
// each operation, so an operation added later inherits it. The assertion is
// that the raw read is already filtered, which is what makes that true.
func TestReadAPIAppliesClearanceOnEveryReadPath(t *testing.T) {
	stub := newDocumentCollectionStub(t, servingStubRows())
	reader := servingReader(t, stub, "unclassified")

	byIDs, err := reader.readByIDs([]string{
		envelopeRecordID("HR-22-019"),
		envelopeRecordID("HR-22-017"),
		envelopeRecordID("isw-2022-12-16"),
	})
	if err != nil {
		t.Fatalf("readByIDs: %v", err)
	}
	if len(byIDs) != 1 || byIDs[0].DocumentID != "isw-2022-12-16" {
		t.Errorf("an unclassified requester asking for three documents received %d;"+
			" the filter is not on the shared read path", len(byIDs))
	}
	byWhere, err := reader.readWhere(map[string]interface{}{
		"$and": []interface{}{
			map[string]interface{}{"kind": map[string]interface{}{"$eq": documentKindEnvelope}},
		},
	})
	if err != nil {
		t.Fatalf("readWhere: %v", err)
	}
	if len(byWhere) != 1 {
		t.Errorf("an unclassified requester matched %d documents by filter, want 1",
			len(byWhere))
	}
}

// A record whose clearance the ordered set does not declare is withheld, on the
// same fail-closed rule that writes an unrecognized marking at the highest
// level (srd010 R1.3).
func TestReadAPIWithholdsARecordAtAnUndeclaredLevel(t *testing.T) {
	rows := append(servingStubRows(), stubRow{
		id: envelopeRecordID("odd-1"),
		metadata: map[string]interface{}{
			"kind": documentKindEnvelope, "document_id": "odd-1",
			"clearance": "cosmic-top-secret", "issued": "2022-12-01",
		},
	})
	stub := newDocumentCollectionStub(t, rows)
	reader := servingReader(t, stub, "secret")

	if _, found, err := reader.GetDocument("odd-1"); err != nil || found {
		t.Errorf("a document at an undeclared level was served to the highest"+
			" cleared requester (found=%v err=%v)", found, err)
	}
}

// A reader cannot be built without an ordered set or at a level outside it, so
// there is no call shape that reads the collection without a decidable
// clearance (srd010 R1.1, R4).
func TestAReaderCannotBeBuiltWithoutADecidableClearance(t *testing.T) {
	if _, err := newCorpusDocumentReader(corpusDocumentsCollection, "secret", nil); err == nil {
		t.Error("a reader was built with no ordered clearance set")
	}
	if _, err := newCorpusDocumentReader(
		corpusDocumentsCollection, "cosmic-top-secret", servingTestLevels()); err == nil {
		t.Error("a reader was built at a level the ordered set does not declare")
	}
	if _, err := newCorpusDocumentReader(
		corpusDocumentsCollection, "unclassified", servingTestLevels()); err != nil {
		t.Errorf("a reader at a declared level was refused: %v", err)
	}
}

// Lineage survives the collection's flat metadata and is recoverable without
// reading the store (srd017 R8.6).
func TestLineageSurvivesTheFlatCollectionMetadata(t *testing.T) {
	document := corpusStoredDocument{
		DocumentID: "HR-22-019",
		Envelope: corpusEnvelope{
			DocumentID: "HR-22-019", Issued: "2022-12-22", Title: "Patriot decision",
			Caveats: []string{"NOFORN"},
		},
	}
	chunk := corpusChunkRecord{
		ChunkID:      chunkIdentifier("HR-22-019", chunkProfileMedium, 2),
		ChunkProfile: chunkProfileMedium,
		Metadata: corpusChunkMetadata{
			SourceType: sourceTypeHumint, Clearance: "secret",
		},
		Parent: &corpusChunkParent{
			DocumentID: "HR-22-019", Position: 2, SiblingCount: 5,
			CharSpan: []int{120, 900}, PrevChunkID: "prev", NextChunkID: "next",
			CoarserParent: "coarse",
		},
	}
	metadata, err := documentChromaMetadata(document, chunk)
	if err != nil {
		t.Fatalf("render metadata: %v", err)
	}
	for field, want := range map[string]string{
		"kind": documentKindChunk, "document_id": "HR-22-019",
		"source_type": sourceTypeHumint, "clearance": "secret",
		"issued": "2022-12-22", "chunk_profile": chunkProfileMedium,
		"caveats": "NOFORN",
	} {
		if got, _ := metadata[field].(string); got != want {
			t.Errorf("metadata %s is %q, want %q", field, got, want)
		}
	}
	var parent corpusChunkParent
	if err := json.Unmarshal([]byte(metadata["parent"].(string)), &parent); err != nil {
		t.Fatalf("the lineage did not survive as JSON: %v", err)
	}
	if parent.Position != 2 || parent.SiblingCount != 5 ||
		parent.PrevChunkID != "prev" || parent.NextChunkID != "next" ||
		parent.CoarserParent != "coarse" || len(parent.CharSpan) != 2 {
		t.Errorf("the lineage came back incomplete: %+v", parent)
	}
}

// The document row carries the whole document, unsearchable segments included.
// They were kept out of the vectors, not out of the document, and an analyst
// opening a citation reads them.
func TestTheDocumentRowCarriesTheUnsearchableSegments(t *testing.T) {
	envelope := corpusEnvelope{
		DocumentID: "HR-22-019",
		Segments: []corpusSegment{
			{Role: segmentRoleAuthority, Searchable: false, Text: "Classified by: DEFTECH/DDI."},
			{Role: segmentRoleSummary, Searchable: true, Text: "1. (S) SUMMARY: The decision holds."},
			{Role: segmentRoleEndnotes, Searchable: false, Text: "[1] https://example.invalid/a"},
		},
	}
	text := envelopeDocumentText(envelope)
	for _, want := range []string{"Classified by", "SUMMARY", "https://example.invalid/a"} {
		if !strings.Contains(text, want) {
			t.Errorf("the served document does not carry %q: %s", want, text)
		}
	}
	if searchable := envelope.searchableText(); strings.Contains(searchable, "Classified by") {
		t.Error("the searchable text carries the authority line, which belongs only" +
			" in the served document")
	}
}

// Every row is written with the same placeholder vector, so seeding the
// document collection calls no embedding provider (srd017 R8.2, R8.8).
func TestTheDocumentCollectionUsesAConstantPlaceholderVector(t *testing.T) {
	first, second := documentPlaceholderVector(), documentPlaceholderVector()
	if len(first) == 0 {
		t.Fatal("the placeholder vector is empty; Chroma requires an embedding")
	}
	if len(first) != len(second) {
		t.Fatalf("the placeholder differs in width between calls: %d and %d",
			len(first), len(second))
	}
	for index := range first {
		if first[index] != second[index] {
			t.Errorf("the placeholder differs at %d, so the collection would hold"+
				" values something could mistake for meaning", index)
		}
	}
}

// The citation entries a chunk resolved survive the collection's flat metadata
// and come back as they were written (srd017 R10.6; rel10.5 suite case (q)).
func TestChunkReferencesSurviveTheFlatMetadata(t *testing.T) {
	document := corpusStoredDocument{
		Envelope: corpusEnvelope{
			DocumentID: "isw-2022-12-01", SourceType: sourceTypeOSINTAssessment,
			Clearance: "unclassified", Issued: "2022-12-01",
		},
	}
	chunk := corpusChunkRecord{
		ChunkID:      chunkIdentifier("isw-2022-12-01", chunkProfileMedium, 32),
		ChunkProfile: chunkProfileMedium,
		Metadata: corpusChunkMetadata{
			SourceType: sourceTypeOSINTAssessment, Clearance: "unclassified",
		},
		Parent: &corpusChunkParent{DocumentID: "isw-2022-12-01", Position: 32, SiblingCount: 32},
		References: []corpusReference{
			{Number: 76, Text: "https://gur dot gov.ua/content/sohodni",
				URLs: []string{"https://gur dot gov.ua/content/sohodni"}},
			{Number: 77, Text: "Russia's Zapad-2021 Exercise | Institute for the Study of War"},
		},
	}

	metadata, err := documentChromaMetadata(document, chunk)
	if err != nil {
		t.Fatalf("render metadata: %v", err)
	}
	encoded, held := metadata["references"].(string)
	if !held || encoded == "" {
		t.Fatal("the chunk's references did not reach the collection metadata")
	}
	var references []corpusReference
	if err := json.Unmarshal([]byte(encoded), &references); err != nil {
		t.Fatalf("the references did not survive as JSON: %v", err)
	}
	if len(references) != 2 {
		t.Fatalf("%d references survived, want 2", len(references))
	}
	// The tier obscures the addresses it considers hostile to follow, and the
	// serving path is not where that is undone.
	if references[0].URLs[0] != "https://gur dot gov.ua/content/sohodni" {
		t.Errorf("an obscured address was rewritten in transit: %q", references[0].URLs[0])
	}
	if references[1].Text == "" || len(references[1].URLs) != 0 {
		t.Errorf("an entry naming a work rather than a link lost its text: %+v", references[1])
	}
}

// A chunk citing nothing carries no references key, so every tier but the
// assessments renders exactly as it did before.
func TestAChunkCitingNothingCarriesNoReferencesKey(t *testing.T) {
	document := corpusStoredDocument{
		Envelope: corpusEnvelope{
			DocumentID: "tg-rbc_news-64950", SourceType: sourceTypeOSINTNews,
			Clearance: "unclassified", Issued: "2022-12-01",
		},
	}
	chunk := corpusChunkRecord{
		ChunkID:      chunkIdentifier("tg-rbc_news-64950", chunkProfileMedium, 1),
		ChunkProfile: chunkProfileMedium,
		Metadata:     corpusChunkMetadata{SourceType: sourceTypeOSINTNews, Clearance: "unclassified"},
		Parent:       &corpusChunkParent{DocumentID: "tg-rbc_news-64950", Position: 1, SiblingCount: 1},
	}

	metadata, err := documentChromaMetadata(document, chunk)
	if err != nil {
		t.Fatalf("render metadata: %v", err)
	}
	if _, held := metadata["references"]; held {
		t.Error("a chunk citing nothing carries a references key")
	}
}
