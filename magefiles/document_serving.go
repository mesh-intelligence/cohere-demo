// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// The document-serving collection and the read API over it (srd017 R8).
//
// Retrieval returns chunk identifiers. An analyst reading a brief wants the
// passage, the report it was cut from, and the other sizes that passage was cut
// at, and none of those is a similarity question -- they are lookups by
// identifier. Chroma answers those without a vector query, so the presentation
// path is a second collection in the store that already runs rather than a
// second database (R8.1).
//
// The requirement that shapes the code is R8.7. rel11.0 filters the retrieval
// collection to the requester's level before the vector search, so an
// over-clearance document is never a candidate. Reading by identifier reaches
// exactly the documents that filter exists to withhold, so every operation here
// applies the same level. A presentation path that returned what retrieval
// withheld would undo that release rather than extend it.

// corpusDocumentsCollection is the served collection holding raw envelopes and
// every chunk-profile version, addressed by identifier and metadata filter.
const corpusDocumentsCollection = "corpus_documents"

// Record kinds in the document collection. The kind is metadata rather than an
// identifier convention, so a filter selects on it without parsing a string.
const (
	documentKindEnvelope = "envelope"
	documentKindChunk    = "chunk"
)

// documentPlaceholderVector is what the document collection stores where Chroma
// requires an embedding. The collection is never queried by similarity, so the
// value carries no meaning and the same constant is written for every record --
// which is also what keeps the seed free: no provider is called (R8.2).
//
// It is three dimensions because a collection pins its width at creation and a
// narrow one costs less to hold; nothing reads it.
func documentPlaceholderVector() []float64 {
	return []float64{0, 0, 0}
}

// documentRecord is one row of the served document collection, as the read API
// hands it back.
type documentRecord struct {
	ID           string
	Kind         string
	DocumentID   string
	SourceType   string
	Clearance    string
	Caveats      []string
	Issued       string
	Title        string
	ChunkProfile string
	Text         string
	// Lineage as the collection carries it. Chroma metadata values are flat
	// scalars, so the parent block travels as JSON and is parsed here rather
	// than being rejoined against the store (R8.6).
	Parent *corpusChunkParent
	// Cross-tier provenance on an envelope (srd017 R5.4, srd016 R6.6): what
	// this document was derived from, travelling the same flat-metadata way
	// the parent block does.
	DerivedFrom []corpusDerivedRef
	// The citation entries this chunk's own markers resolve to (srd017 R10.6),
	// travelling the same way. Empty on every tier but the assessments.
	References []corpusReference
}

// documentChromaMetadata renders one stored chunk as collection metadata. The
// facets the interface filters on are scalars; the lineage the provenance walk
// needs is a JSON string, because Chroma holds no nested values.
func documentChromaMetadata(
	document corpusStoredDocument, chunk corpusChunkRecord,
) (map[string]interface{}, error) {
	metadata := map[string]interface{}{
		"kind":          documentKindChunk,
		"document_id":   chunk.Parent.DocumentID,
		"source_type":   chunk.Metadata.SourceType,
		"clearance":     chunk.Metadata.Clearance,
		"issued":        document.Envelope.Issued,
		"title":         document.Envelope.Title,
		"chunk_profile": chunk.ChunkProfile,
		// The chunk-row render contract (GH-843): a revision re-grounds its
		// carried citations by reading them back from this collection, and
		// those rows reach the same document render a retrieved chunk does, so
		// they carry the rank and Admiralty grade the retrieval seed writes.
		"clearance_rank":                    clearanceRank(chunk.Metadata.Clearance),
		"admiralty_source_reliability":      chunk.Metadata.AdmiraltySourceReliability,
		"admiralty_information_credibility": chunk.Metadata.AdmiraltyInformationCredibility,
	}
	if len(document.Envelope.Caveats) > 0 {
		metadata["caveats"] = strings.Join(document.Envelope.Caveats, ",")
	}
	encoded, err := json.Marshal(chunk.Parent)
	if err != nil {
		return nil, fmt.Errorf("encode lineage for chunk %s: %w", chunk.ChunkID, err)
	}
	metadata["parent"] = string(encoded)
	// The entries this chunk's markers resolve to travel the same way the
	// lineage does, so the panel names the sources a passage cites without
	// rejoining against the store (srd017 R10.6). Omitted where the chunk cites
	// nothing, which keeps every other tier's row as it was.
	if len(chunk.References) > 0 {
		encoded, err := json.Marshal(chunk.References)
		if err != nil {
			return nil, fmt.Errorf("encode references for chunk %s: %w", chunk.ChunkID, err)
		}
		metadata["references"] = string(encoded)
	}
	return metadata, nil
}

// envelopeChromaMetadata renders a document's envelope as collection metadata.
// The envelope is what an analyst opens when they follow a citation, so it
// carries the whole document including the segments that were never embedded,
// and the cross-tier derivation the provenance walk resolves (srd016 R6.6).
func envelopeChromaMetadata(envelope corpusEnvelope) (map[string]interface{}, error) {
	metadata := map[string]interface{}{
		"kind":        documentKindEnvelope,
		"document_id": envelope.DocumentID,
		"source_type": envelope.SourceType,
		"clearance":   envelope.Clearance,
		"issued":      envelope.Issued,
		"title":       envelope.Title,
	}
	if len(envelope.Caveats) > 0 {
		metadata["caveats"] = strings.Join(envelope.Caveats, ",")
	}
	if len(envelope.Provenance.DerivedFrom) > 0 {
		encoded, err := json.Marshal(envelope.Provenance.DerivedFrom)
		if err != nil {
			return nil, fmt.Errorf("encode derived_from for %s: %w", envelope.DocumentID, err)
		}
		metadata["derived_from"] = string(encoded)
	}
	return metadata, nil
}

// envelopeDocumentText renders the whole document, unsearchable segments
// included, in document order. This is what "open the source" returns, so the
// classification-authority line, the endnote tail, and the disclaimer are all
// present -- they were kept out of the vectors, not out of the document.
func envelopeDocumentText(envelope corpusEnvelope) string {
	parts := make([]string, 0, len(envelope.Segments))
	for _, segment := range envelope.Segments {
		if text := strings.TrimSpace(segment.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// envelopeRecordID names the envelope row of a document.
func envelopeRecordID(documentID string) string {
	return documentID + "#envelope"
}

// corpusDocumentSeedReport counts what the document seed wrote.
type corpusDocumentSeedReport struct {
	Collection string
	Documents  int
	Chunks     int
}

// seedDocumentsCollection writes every document's envelope and every chunk cut
// from it into the served document collection.
//
// No embedding provider is called. The collection is read by identifier and by
// metadata filter, never by similarity, so a constant placeholder stands where
// Chroma requires a vector and the seed costs nothing (R8.2, R8.8).
func seedDocumentsCollection(root, collection string) (corpusDocumentSeedReport, error) {
	report := corpusDocumentSeedReport{Collection: collection}
	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		return report, err
	}
	if len(documents) == 0 {
		return report, fmt.Errorf(
			"the document store at %s holds no documents; run the corpus build before seeding",
			corpusStoreDocumentsDir)
	}

	documentIDs := make([]string, 0, len(documents))
	for id := range documents {
		documentIDs = append(documentIDs, id)
	}
	sort.Strings(documentIDs)

	ids := make([]string, 0, len(documents))
	texts := make([]string, 0, len(documents))
	metadatas := make([]map[string]interface{}, 0, len(documents))
	embeddings := make([][]float64, 0, len(documents))
	for _, documentID := range documentIDs {
		document := documents[documentID]
		envelopeMetadata, err := envelopeChromaMetadata(document.Envelope)
		if err != nil {
			return report, err
		}
		ids = append(ids, envelopeRecordID(documentID))
		texts = append(texts, envelopeDocumentText(document.Envelope))
		metadatas = append(metadatas, envelopeMetadata)
		embeddings = append(embeddings, documentPlaceholderVector())
		report.Documents++

		for _, chunk := range document.Chunks {
			if chunk.Parent == nil {
				return report, fmt.Errorf("chunk %s carries no parent block, so the"+
					" served collection has nothing to resolve it to (srd017 R5.1)",
					chunk.ChunkID)
			}
			metadata, err := documentChromaMetadata(document, chunk)
			if err != nil {
				return report, err
			}
			ids = append(ids, chunk.ChunkID)
			texts = append(texts, chunk.Text)
			metadatas = append(metadatas, metadata)
			embeddings = append(embeddings, documentPlaceholderVector())
			report.Chunks++
		}
	}

	if err := chromaDropCollection(collection); err != nil {
		return report, err
	}
	if err := chromaCollectionAdd(collection, ids, texts, embeddings, metadatas); err != nil {
		return report, err
	}
	return report, nil
}

// corpusDocumentReader is the read API the interface consumes (R8.4). It holds
// the collection it reads and the clearance every read is answered at.
//
// The clearance is a field rather than a parameter so that no caller can omit
// it. A reader is constructed for a requester, and every operation on it is
// bounded by that requester's level -- there is no call shape that reads the
// collection without one (R8.7).
type corpusDocumentReader struct {
	Collection string
	Clearance  string
	Levels     []string
	// The collections endpoint to read through. Empty means the deployed
	// Chroma the seed writes to; a test points it at a server of its own so the
	// clearance rule can be proven without standing a database up.
	BaseURL string
}

// collectionsURL is the endpoint this reader addresses.
func (r corpusDocumentReader) collectionsURL() string {
	if strings.TrimSpace(r.BaseURL) != "" {
		return r.BaseURL
	}
	return chromaCollectionsURL
}

// newCorpusDocumentReader builds a reader for one requester.
func newCorpusDocumentReader(
	collection, clearance string, levels []string,
) (corpusDocumentReader, error) {
	reader := corpusDocumentReader{Collection: collection, Clearance: clearance, Levels: levels}
	if len(levels) == 0 {
		return reader, fmt.Errorf("the document reader was given no ordered clearance" +
			" set, so \"at or below\" is undecidable and every read would be a guess" +
			" (srd010 R1.1)")
	}
	if reader.levelIndex(clearance) < 0 {
		// A clearance the ordered set does not declare serves nothing rather
		// than everything, which is the direction srd010 R4 requires of every
		// clearance error.
		return reader, fmt.Errorf("clearance %q is not in the declared ordered set %v;"+
			" a read at an undeclared level serves nothing (srd010 R1.1, R4)",
			clearance, levels)
	}
	return reader, nil
}

// levelIndex is the position of a level in the ordered set, or -1.
func (r corpusDocumentReader) levelIndex(level string) int {
	for index, declared := range r.Levels {
		if declared == level {
			return index
		}
	}
	return -1
}

// permits reports whether a record's clearance is at or below the requester's.
// A record whose level the ordered set does not declare is withheld, on the
// same fail-closed rule that writes an unrecognized marking at the highest
// level (srd010 R1.3).
func (r corpusDocumentReader) permits(clearance string) bool {
	level := r.levelIndex(clearance)
	if level < 0 {
		return false
	}
	return level <= r.levelIndex(r.Clearance)
}

// GetChunk returns one chunk with its lineage, which is what renders a cited
// hit (R8.4). A chunk above the requester's level is not returned.
func (r corpusDocumentReader) GetChunk(chunkID string) (documentRecord, bool, error) {
	records, err := r.readByIDs([]string{chunkID})
	if err != nil {
		return documentRecord{}, false, err
	}
	if len(records) == 0 {
		return documentRecord{}, false, nil
	}
	return records[0], true, nil
}

// GetDocument returns a document's envelope, which is the "open the source"
// action (R8.4). The whole document comes back, including the segments that
// were never embedded.
func (r corpusDocumentReader) GetDocument(documentID string) (documentRecord, bool, error) {
	records, err := r.readByIDs([]string{envelopeRecordID(documentID)})
	if err != nil {
		return documentRecord{}, false, err
	}
	if len(records) == 0 {
		return documentRecord{}, false, nil
	}
	return records[0], true, nil
}

// GetVersions returns a document's chunks at every profile, for drilling
// between chunk sizes (R8.4). The envelope row is left out; it is what
// GetDocument returns.
func (r corpusDocumentReader) GetVersions(documentID string) ([]documentRecord, error) {
	records, err := r.readWhere(map[string]interface{}{
		"$and": []interface{}{
			map[string]interface{}{"document_id": map[string]interface{}{"$eq": documentID}},
			map[string]interface{}{"kind": map[string]interface{}{"$eq": documentKindChunk}},
		},
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].ChunkProfile != records[j].ChunkProfile {
			return records[i].ChunkProfile < records[j].ChunkProfile
		}
		return records[i].ID < records[j].ID
	})
	return records, nil
}

// corpusDocumentFilter is the metadata query the interface facets on (R8.4).
// An empty field is not a constraint.
type corpusDocumentFilter struct {
	SourceType string
	Clearance  string
	IssuedFrom string
	IssuedTo   string
}

// ListDocuments returns the envelopes matching a facet query. It answers with
// documents rather than chunks, because the facets select sources rather than
// passages.
func (r corpusDocumentReader) ListDocuments(filter corpusDocumentFilter) ([]documentRecord, error) {
	clauses := []interface{}{
		map[string]interface{}{"kind": map[string]interface{}{"$eq": documentKindEnvelope}},
	}
	if filter.SourceType != "" {
		clauses = append(clauses, map[string]interface{}{
			"source_type": map[string]interface{}{"$eq": filter.SourceType}})
	}
	if filter.Clearance != "" {
		clauses = append(clauses, map[string]interface{}{
			"clearance": map[string]interface{}{"$eq": filter.Clearance}})
	}
	if filter.IssuedFrom != "" {
		clauses = append(clauses, map[string]interface{}{
			"issued": map[string]interface{}{"$gte": filter.IssuedFrom}})
	}
	if filter.IssuedTo != "" {
		clauses = append(clauses, map[string]interface{}{
			"issued": map[string]interface{}{"$lte": filter.IssuedTo}})
	}
	where := map[string]interface{}{"$and": clauses}
	records, err := r.readWhere(where)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Issued != records[j].Issued {
			return records[i].Issued < records[j].Issued
		}
		return records[i].DocumentID < records[j].DocumentID
	})
	return records, nil
}

// readByIDs and readWhere are the two shapes Chroma answers without a vector
// query. Both filter the result to the requester's level before returning it.
func (r corpusDocumentReader) readByIDs(ids []string) ([]documentRecord, error) {
	return r.get(map[string]interface{}{"ids": ids})
}

func (r corpusDocumentReader) readWhere(where map[string]interface{}) ([]documentRecord, error) {
	return r.get(map[string]interface{}{"where": where})
}

// get performs the read and applies the clearance.
//
// The filter is applied here, on every path out of the collection, rather than
// in each operation. An operation added later inherits it instead of having to
// remember it, which is the difference between an access control and a
// convention (R8.7).
func (r corpusDocumentReader) get(body map[string]interface{}) ([]documentRecord, error) {
	body["include"] = []string{"documents", "metadatas"}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	collectionID, err := chromaResolveCollectionAt(r.collectionsURL(), r.Collection)
	if err != nil {
		return nil, err
	}
	data, status, err := requestHTTP(http.MethodPost,
		r.collectionsURL()+"/"+collectionID+"/get", string(payload))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", r.Collection, err)
	}
	if status/100 != 2 {
		return nil, fmt.Errorf("read %s: status %d: %s", r.Collection, status, data)
	}
	var response struct {
		IDs       []string                 `json:"ids"`
		Documents []string                 `json:"documents"`
		Metadatas []map[string]interface{} `json:"metadatas"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", r.Collection, err)
	}

	records := make([]documentRecord, 0, len(response.IDs))
	for index, id := range response.IDs {
		metadata := map[string]interface{}{}
		if index < len(response.Metadatas) && response.Metadatas[index] != nil {
			metadata = response.Metadatas[index]
		}
		record := documentRecord{
			ID:           id,
			Kind:         metadataString(metadata, "kind"),
			DocumentID:   metadataString(metadata, "document_id"),
			SourceType:   metadataString(metadata, "source_type"),
			Clearance:    metadataString(metadata, "clearance"),
			Issued:       metadataString(metadata, "issued"),
			Title:        metadataString(metadata, "title"),
			ChunkProfile: metadataString(metadata, "chunk_profile"),
		}
		if caveats := metadataString(metadata, "caveats"); caveats != "" {
			record.Caveats = strings.Split(caveats, ",")
		}
		if index < len(response.Documents) {
			record.Text = response.Documents[index]
		}
		if encoded := metadataString(metadata, "parent"); encoded != "" {
			var parent corpusChunkParent
			if err := json.Unmarshal([]byte(encoded), &parent); err != nil {
				return nil, fmt.Errorf("decode lineage on %s: %w", id, err)
			}
			record.Parent = &parent
		}
		if encoded := metadataString(metadata, "derived_from"); encoded != "" {
			if err := json.Unmarshal([]byte(encoded), &record.DerivedFrom); err != nil {
				return nil, fmt.Errorf("decode derived_from on %s: %w", id, err)
			}
		}
		if encoded := metadataString(metadata, "references"); encoded != "" {
			if err := json.Unmarshal([]byte(encoded), &record.References); err != nil {
				return nil, fmt.Errorf("decode references on %s: %w", id, err)
			}
		}
		if !r.permits(record.Clearance) {
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

// metadataString reads one flat metadata value as text.
func metadataString(metadata map[string]interface{}, field string) string {
	value, held := metadata[field]
	if !held {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

// chromaResolveCollection returns a collection's identifier, which the get
// endpoint is addressed by.
func chromaResolveCollection(collection string) (string, error) {
	return chromaResolveCollectionAt(chromaCollectionsURL, collection)
}

// chromaResolveCollectionAt resolves against a given endpoint.
func chromaResolveCollectionAt(base, collection string) (string, error) {
	data, status, err := requestHTTP(http.MethodPost, base,
		fmt.Sprintf(`{"name":%q,"get_or_create":true}`, collection))
	if err != nil {
		return "", err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return "", fmt.Errorf("resolve %s collection: status %d: %s", collection, status, data)
	}
	var resolved struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &resolved); err != nil {
		return "", fmt.Errorf("decode collection id: %w", err)
	}
	return resolved.ID, nil
}
