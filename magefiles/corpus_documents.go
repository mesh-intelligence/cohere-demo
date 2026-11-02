// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The multi-version store (srd017 R7). rel10.4 wrote one flat directory of
// chunk records, which is the right shape for a corpus cut one way. rel10.5
// cuts each document four ways and keeps the document itself, so the store
// gains a directory per document holding its raw envelope beside every profile
// built from it.
//
// The reason is what reads it. The serving path answers "give me this document
// and its other chunk sizes" from a chunk identifier, and a flat directory
// makes that a scan of the whole store. A directory per document makes it a
// directory read (R7.1).

const corpusStoreDocumentsDir = "corpus/store/documents"

// corpusEnvelopeRecordName is the envelope's filename inside a document's
// directory. It sorts before every chunk record, so a reviewer opening the
// directory meets the document before its fragments.
const corpusEnvelopeRecordName = "00-envelope.yaml"

// corpusStoredDocument is one document as the store holds it -- the envelope the
// adapters produced and every chunk cut from it, across all profiles.
type corpusStoredDocument struct {
	DocumentID string
	Envelope   corpusEnvelope
	Chunks     []corpusChunkRecord
}

// chunksAtProfile returns this document's chunks at one profile, in position
// order.
func (d corpusStoredDocument) chunksAtProfile(profile string) []corpusChunkRecord {
	matched := make([]corpusChunkRecord, 0, len(d.Chunks))
	for _, chunk := range d.Chunks {
		if chunk.ChunkProfile == profile {
			matched = append(matched, chunk)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].Position < matched[j].Position
	})
	return matched
}

// profiles names the profiles this document was cut at, sorted.
func (d corpusStoredDocument) profiles() []string {
	seen := make(map[string]bool, len(d.Chunks))
	for _, chunk := range d.Chunks {
		if chunk.ChunkProfile != "" {
			seen[chunk.ChunkProfile] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// corpusDocumentDir is where one document's versions live. The identifier is
// lowercased for the same reason chunk record filenames are: two identifiers
// differing only in case would collide on a case-insensitive filesystem and the
// collision would surface as a document silently overwriting another.
//
// The directory depends on the document's tier verdict (srd017 R9.7): a
// keep-private document lives in the private data checkout, a publishable one
// in this repository's tracked store. Writing a keep-private document with no
// private checkout present is an error rather than a fallback, because the
// fallback would stage copyrighted text for `git add -A` to commit.
func corpusDocumentDir(root, documentID string) (string, error) {
	if corpusDocumentKeepPrivate(documentID) {
		private := corpusPrivateStoreDocumentsDir(root)
		if private == "" {
			return "", fmt.Errorf("document %s belongs to a keep-private tier: %w",
				documentID, corpusMissingPrivateStore(root))
		}
		return filepath.Join(private, strings.ToLower(documentID)), nil
	}
	return filepath.Join(root, filepath.FromSlash(corpusStoreDocumentsDir),
		strings.ToLower(documentID)), nil
}

// writeCorpusEnvelopeIfChanged writes a document's envelope only when the
// encoded form differs from what is on disk, so a rebuild over unchanged inputs
// writes nothing (R7.4).
func writeCorpusEnvelopeIfChanged(root string, envelope corpusEnvelope) (bool, error) {
	encoded, err := encodeCorpusYAML(envelope)
	if err != nil {
		return false, fmt.Errorf("encode envelope %s: %w", envelope.DocumentID, err)
	}
	dir, err := corpusDocumentDir(root, envelope.DocumentID)
	if err != nil {
		return false, err
	}
	path := filepath.Join(dir, corpusEnvelopeRecordName)
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, encoded) {
		return false, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("create document directory %s: %w", envelope.DocumentID, err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return false, fmt.Errorf("write envelope %s: %w", envelope.DocumentID, err)
	}
	return true, nil
}

// writeCorpusDocumentChunkIfChanged writes one chunk into its document's
// directory, on the same unchanged-means-untouched rule.
func writeCorpusDocumentChunkIfChanged(
	root string, record corpusChunkRecord,
) (bool, error) {
	if record.Parent == nil || record.Parent.DocumentID == "" {
		return false, fmt.Errorf("chunk %s names no parent document, so the store"+
			" has nowhere to co-locate it (srd017 R5.1, R7.1)", record.ChunkID)
	}
	encoded, err := encodeCorpusYAML(record)
	if err != nil {
		return false, fmt.Errorf("encode chunk %s: %w", record.ChunkID, err)
	}
	dir, err := corpusDocumentDir(root, record.Parent.DocumentID)
	if err != nil {
		return false, err
	}
	path := filepath.Join(dir, corpusRecordFilename(record.ChunkID))
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, encoded) {
		return false, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("create document directory %s: %w",
			record.Parent.DocumentID, err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return false, fmt.Errorf("write chunk %s: %w", record.ChunkID, err)
	}
	return true, nil
}

// loadCorpusDocumentStore reads every document the store holds, each with its
// envelope and all its chunks. A document directory carrying no envelope is an
// error rather than a document with none: the envelope is what the serving path
// returns when an analyst opens the source, and a chunk whose parent cannot be
// opened is the citation-leads-nowhere case R5.6 exists to prevent.
//
// The store is read as the union of the tracked location and the private data
// checkout when one is present (srd015 R6.1): one corpus, two locations, split
// by tier verdict. A document identifier appearing in both is an error rather
// than a precedence rule, because whichever copy lost would be a store a
// reviewer read and a seed ignored.
func loadCorpusDocumentStore(root string) (map[string]corpusStoredDocument, error) {
	locations := []string{filepath.Join(root, filepath.FromSlash(corpusStoreDocumentsDir))}
	if private := corpusPrivateStoreDocumentsDir(root); private != "" {
		locations = append(locations, private)
	}
	documents := map[string]corpusStoredDocument{}
	sources := map[string]string{}
	for _, dir := range locations {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read document store: %w", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			document, err := loadCorpusStoredDocument(filepath.Join(dir, entry.Name()))
			if err != nil {
				return nil, err
			}
			if previous, held := sources[document.DocumentID]; held {
				return nil, fmt.Errorf("document %s is stored in both %s and %s;"+
					" the split store holds each document in exactly one location"+
					" (srd015 R6.1)", document.DocumentID, previous, dir)
			}
			sources[document.DocumentID] = dir
			documents[document.DocumentID] = document
		}
	}
	return documents, nil
}

// loadCorpusStoredDocument reads one document directory.
func loadCorpusStoredDocument(dir string) (corpusStoredDocument, error) {
	var document corpusStoredDocument
	entries, err := os.ReadDir(dir)
	if err != nil {
		return document, fmt.Errorf("read %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), corpusRecordExtension) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return document, fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		if entry.Name() == corpusEnvelopeRecordName {
			if err := yaml.Unmarshal(data, &document.Envelope); err != nil {
				return document, fmt.Errorf("parse envelope %s: %w", entry.Name(), err)
			}
			document.DocumentID = document.Envelope.DocumentID
			continue
		}
		var record corpusChunkRecord
		if err := yaml.Unmarshal(data, &record); err != nil {
			return document, fmt.Errorf("parse chunk record %s: %w", entry.Name(), err)
		}
		document.Chunks = append(document.Chunks, record)
	}
	if document.DocumentID == "" {
		return document, fmt.Errorf("document directory %s holds no envelope; a chunk"+
			" whose parent cannot be opened is a citation that leads nowhere"+
			" (srd017 R7.1)", filepath.Base(dir))
	}
	sort.SliceStable(document.Chunks, func(i, j int) bool {
		return document.Chunks[i].ChunkID < document.Chunks[j].ChunkID
	})
	return document, nil
}

// pruneCorpusDocumentStore removes what the corpus no longer produces. It works
// at two levels because a document can shrink as well as disappear: a document
// absent from the build loses its whole directory with every profile variant in
// it, and a document that kept fewer chunks at one profile loses the ones past
// its new end (R7.3).
//
// Pruning on the chunk identifier alone would be the defect worth naming: the
// identifier carries the profile, so removing a document by chunk identifier
// leaves its other profiles behind, and a seed reading the store would load
// chunks of a document the corpus no longer holds.
func pruneCorpusDocumentStore(
	root string, wantedDocuments map[string]bool, wantedChunks map[string]bool,
) ([]string, error) {
	stored, err := loadCorpusDocumentStore(root)
	if err != nil {
		return nil, err
	}
	documentIDs := make([]string, 0, len(stored))
	for id := range stored {
		documentIDs = append(documentIDs, id)
	}
	sort.Strings(documentIDs)

	var removed []string
	for _, id := range documentIDs {
		dir, err := corpusDocumentDir(root, id)
		if err != nil {
			return removed, err
		}
		if !wantedDocuments[id] {
			if err := os.RemoveAll(dir); err != nil {
				return removed, fmt.Errorf("remove document %s: %w", id, err)
			}
			removed = append(removed, id)
			continue
		}
		for _, chunk := range stored[id].Chunks {
			if wantedChunks[chunk.ChunkID] {
				continue
			}
			path := filepath.Join(dir, corpusRecordFilename(chunk.ChunkID))
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return removed, fmt.Errorf("remove orphaned chunk %s: %w", chunk.ChunkID, err)
			}
			removed = append(removed, chunk.ChunkID)
		}
	}
	return removed, nil
}

// corpusStoreProfile is one profile's presence in the store: how many chunks it
// cut and how many of them carry a vector. The two differ because the embedding
// budget selects a subset, and an index reporting only the total would leave
// what was actually paid for to be recovered by counting records (srd017 R6.6,
// R7.2).
type corpusStoreProfile struct {
	Profile  string `yaml:"profile"`
	Chunks   int    `yaml:"chunks"`
	Embedded int    `yaml:"embedded"`
}

// corpusStoreProfiles summarizes the profiles a document store holds.
func corpusStoreProfiles(
	documents map[string]corpusStoredDocument, identity string,
) []corpusStoreProfile {
	chunks := make(map[string]int)
	embedded := make(map[string]int)
	for _, document := range documents {
		for _, chunk := range document.Chunks {
			if chunk.ChunkProfile == "" {
				continue
			}
			chunks[chunk.ChunkProfile]++
			if _, held := chunk.vector(identity); held {
				embedded[chunk.ChunkProfile]++
			}
		}
	}
	names := make([]string, 0, len(chunks))
	for name := range chunks {
		names = append(names, name)
	}
	sort.Strings(names)
	profiles := make([]corpusStoreProfile, 0, len(names))
	for _, name := range names {
		profiles = append(profiles, corpusStoreProfile{
			Profile:  name,
			Chunks:   chunks[name],
			Embedded: embedded[name],
		})
	}
	return profiles
}
