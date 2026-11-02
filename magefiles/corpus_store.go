// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// The corpus artifact and the store the build writes beside it. Both are
// versioned repository paths rather than generated output directories, because
// a clone has to seed and run the demo without provider access and without
// first running a build (srd015 R6.1).
const (
	corpusDir             = "corpus"
	corpusManifestPath    = "corpus/corpus.yaml"
	corpusDocumentsDir    = "corpus/documents"
	corpusStoreDir        = "corpus/store"
	corpusStoreChunksDir  = "corpus/store/chunks"
	corpusStoreIndexPath  = "corpus/store/store.yaml"
	corpusRecordExtension = ".yaml"
)

// corpusManifest is corpus/corpus.yaml: the corpus version a store names, the
// totally ordered clearance set srd010 R1.1 declares, and the retrieval
// conditions the corpus is authored to hold. The build reads the ordered set to
// decide the fail-closed level and reads nothing else here to decide a vector.
type corpusManifest struct {
	ID              string            `yaml:"id"`
	Version         string            `yaml:"version"`
	Title           string            `yaml:"title"`
	Synthetic       bool              `yaml:"synthetic"`
	SyntheticNotice string            `yaml:"synthetic_notice"`
	ClearanceLevels []string          `yaml:"clearance_levels"`
	SourceTypes     map[string]string `yaml:"source_types"`
	// The declared marking-to-level mapping, its caveat vocabulary, and the
	// per-tier clearance policy the rel10.5 adapters read (srd017 R2.2, R2.4).
	// They live in the corpus artifact rather than in build code so a reviewer
	// reads the mapping where the corpus is defined. A corpus that declares
	// none leaves every adapter failing closed at the highest level.
	ClearanceMarkings map[string]string `yaml:"clearance_markings,omitempty"`
	ClearanceCaveats  []string          `yaml:"clearance_caveats,omitempty"`
	TierClearance     map[string]string `yaml:"tier_clearance,omitempty"`
	// The Admiralty rating each tier's material carries, as a property of the
	// tier (srd017 R2.6). Published reporting is not rated on that scale by the
	// organization that published it, so the rating is an evaluation of the
	// source declared in the corpus artifact -- which is what the scale is for
	// -- rather than a judgment a model made per document.
	TierAdmiralty map[string]string `yaml:"tier_admiralty,omitempty"`
	Admiralty     struct {
		SourceReliability      map[string]string `yaml:"source_reliability"`
		InformationCredibility map[int]string    `yaml:"information_credibility"`
	} `yaml:"admiralty"`
	RetrievalConditions struct {
		PrimaryTasking          string   `yaml:"primary_tasking"`
		RelevantToPrimaryTaskng []string `yaml:"relevant_to_primary_tasking"`
		Distractors             string   `yaml:"distractors"`
	} `yaml:"retrieval_conditions"`
}

// highestClearance is the level a chunk is written at when its document
// declares none or declares one outside the ordered set. Fail-closed is the
// whole point: a guess that lands low discloses, and an authoring omission
// should withhold a document rather than expose it (srd015 R2.5, srd010 R1.3).
func (m corpusManifest) highestClearance() string {
	if len(m.ClearanceLevels) == 0 {
		return ""
	}
	return m.ClearanceLevels[len(m.ClearanceLevels)-1]
}

func (m corpusManifest) declaresClearance(level string) bool {
	for _, declared := range m.ClearanceLevels {
		if declared == level {
			return true
		}
	}
	return false
}

// corpusFrontMatter is a document's declared metadata. Every value the build
// writes onto a chunk comes from here; nothing is read out of the prose to
// decide any of it, for the reason srd010 R1.2 gives -- something that reads a
// document to decide how restricted it is has already read it (srd015 R2.3).
type corpusFrontMatter struct {
	SourceID   string `yaml:"source_id"`
	SourceType string `yaml:"source_type"`
	Clearance  string `yaml:"clearance"`
	Admiralty  struct {
		SourceReliability      string `yaml:"source_reliability"`
		InformationCredibility int    `yaml:"information_credibility"`
	} `yaml:"admiralty"`
	Keywords  []string `yaml:"keywords"`
	Synthetic bool     `yaml:"synthetic"`
}

// corpusDocument is one parsed corpus file: its declared front matter and the
// prose body the build chunks, kept apart from the moment it is read.
type corpusDocument struct {
	Path        string
	FrontMatter corpusFrontMatter
	Body        string
}

// corpusChunkRecord is one on-disk chunk: the text, its metadata, and its
// vectors keyed by the embedding-model identity that produced each one
// (srd015 R2.2, R3.1). It is YAML a reviewer reads without running anything,
// down to the vector, which is a sequence of numbers rather than an encoded
// blob (R6.3).
type corpusChunkRecord struct {
	ChunkID string `yaml:"chunk_id"`
	// The profile that cut this chunk (srd017 R4.4). Omitted when absent so the
	// records the single-profile rel10.4 chunker wrote stay byte-identical; a
	// record carrying no profile was cut before profiles existed.
	ChunkProfile  string `yaml:"chunk_profile,omitempty"`
	Corpus        string `yaml:"corpus"`
	CorpusVersion string `yaml:"corpus_version"`
	Synthetic     bool   `yaml:"synthetic"`
	SourcePath    string `yaml:"source_path"`
	Position      int    `yaml:"position"`
	TextSHA256    string `yaml:"text_sha256"`
	Text          string `yaml:"text"`
	// Lineage back to the document this chunk was cut from (srd017 R5). Omitted
	// when absent so the single-profile rel10.4 records stay byte-identical.
	Parent     *corpusChunkParent `yaml:"parent,omitempty"`
	Provenance *corpusProvenance  `yaml:"provenance,omitempty"`
	// The citation entries resolving the markers this chunk's own text carries
	// (srd017 R10.4). They are metadata and never enter Text, so the digest
	// above still describes the prose that was embedded and a chunk that gains
	// references keeps the vectors it already holds (R5.5, srd015 R3.4).
	// Omitted where the chunk cites nothing, which is every tier but the
	// assessments.
	References []corpusReference   `yaml:"references,omitempty"`
	Metadata   corpusChunkMetadata `yaml:"metadata"`
	Vectors    []corpusChunkVector `yaml:"vectors"`
}

// corpusChunkParent is what makes a retrieved fragment reconstruct to a citable
// source (srd017 R5.1 through R5.3). Retrieval returns chunks, and a chunk
// identifier names a fragment; without this block the brief's citation points
// at something an analyst cannot open.
//
// The block is metadata and never enters the embedded text (R5.5), so the
// identity that decides whether a vector is recomputed turns on the passage
// alone. Adding a neighbouring document changes a chunk's lineage and not its
// hash, which is what keeps a rebuild from re-embedding the corpus every time
// it grows.
// The json tags mirror the yaml ones because this block also travels encoded
// inside the served document collection's flat metadata (srd017 R8.6), and the
// interface that parses it reads the same names the store files declare.
type corpusChunkParent struct {
	DocumentID   string       `yaml:"document_id" json:"document_id"`
	RawRef       corpusRawRef `yaml:"raw_ref" json:"raw_ref"`
	Position     int          `yaml:"position" json:"position"`
	SiblingCount int          `yaml:"sibling_count" json:"sibling_count"`
	// The half-open character span this chunk occupies in the joined searchable
	// body, so an interface can highlight the passage inside the full document
	// rather than searching for the chunk text in it.
	CharSpan      []int  `yaml:"char_span" json:"char_span"`
	PrevChunkID   string `yaml:"prev_chunk_id,omitempty" json:"prev_chunk_id,omitempty"`
	NextChunkID   string `yaml:"next_chunk_id,omitempty" json:"next_chunk_id,omitempty"`
	CoarserParent string `yaml:"coarser_parent,omitempty" json:"coarser_parent,omitempty"`
}

// corpusChunkMetadata travels with the chunk rather than being rejoined from
// the document at seed time, so the clearance tag rel11.0 filters on and the
// facets rel11.5 selects by arrive from the build (srd015 R2.2, R5.3).
type corpusChunkMetadata struct {
	SourceID                        string   `yaml:"source_id"`
	SourceType                      string   `yaml:"source_type"`
	Clearance                       string   `yaml:"clearance"`
	AdmiraltySourceReliability      string   `yaml:"admiralty_source_reliability"`
	AdmiraltyInformationCredibility int      `yaml:"admiralty_information_credibility"`
	Keywords                        []string `yaml:"keywords"`
}

// corpusChunkVector is one family's vector for one chunk. Identity is the
// embedding-model name the rag-server reports in its query response metadata,
// so the srd002 R3.3 comparison is decided against what the store wrote rather
// than against a separate naming (srd015 R3.2). Dimension is recorded because
// Chroma pins a collection's dimension at creation and a family repoint is
// therefore a full reseed (R3.5).
type corpusChunkVector struct {
	Identity  string    `yaml:"identity"`
	Dimension int       `yaml:"dimension"`
	Values    []float64 `yaml:"values"`
}

// vector returns the values stored under one identity.
func (r corpusChunkRecord) vector(identity string) ([]float64, bool) {
	for _, stored := range r.Vectors {
		if stored.Identity == identity {
			return stored.Values, true
		}
	}
	return nil, false
}

// identities names the families this record carries, in stored order.
func (r corpusChunkRecord) identities() []string {
	names := make([]string, 0, len(r.Vectors))
	for _, stored := range r.Vectors {
		names = append(names, stored.Identity)
	}
	return names
}

// withVector returns the record carrying one more family's vector, leaving the
// vectors already stored under other identities exactly as they were: a family
// change adds rather than replaces (srd015 R3.4). Re-storing an identity the
// record already holds overwrites that one entry alone.
func (r corpusChunkRecord) withVector(identity string, values []float64) corpusChunkRecord {
	updated := make([]corpusChunkVector, 0, len(r.Vectors)+1)
	replaced := false
	for _, stored := range r.Vectors {
		if stored.Identity == identity {
			updated = append(updated, corpusChunkVector{
				Identity: identity, Dimension: len(values), Values: values})
			replaced = true
			continue
		}
		updated = append(updated, stored)
	}
	if !replaced {
		updated = append(updated, corpusChunkVector{
			Identity: identity, Dimension: len(values), Values: values})
	}
	sort.SliceStable(updated, func(i, j int) bool {
		return updated[i].Identity < updated[j].Identity
	})
	r.Vectors = updated
	return r
}

// corpusStoreIndex is corpus/store/store.yaml: what corpus produced the store,
// with which chunking parameters, and which identities it holds at which
// dimension (srd015 R4.5, R6.2). Two stores are comparable only when both
// match, so the file states both rather than leaving either to be inferred.
type corpusStoreIndex struct {
	Corpus          string                `yaml:"corpus"`
	CorpusVersion   string                `yaml:"corpus_version"`
	Synthetic       bool                  `yaml:"synthetic"`
	SyntheticNotice string                `yaml:"synthetic_notice"`
	Chunking        corpusChunkParameters `yaml:"chunking"`
	Chunks          int                   `yaml:"chunks"`
	// The profiles the store holds, with the chunk and vector counts of each
	// (srd017 R7.2). Omitted for a single-profile store so the rel10.4 index
	// stays byte-identical.
	Documents int                    `yaml:"documents,omitempty"`
	Profiles  []corpusStoreProfile   `yaml:"profiles,omitempty"`
	Slice     *corpusSliceParameters `yaml:"slice,omitempty"`
	// The selection that decided which chunks were paid for, recorded so a store
	// says what it cost rather than leaving it to be counted (srd017 R6.1).
	EmbedSelection *corpusEmbedSelection `yaml:"embed_selection,omitempty"`
	Identities     []corpusStoreIdentity `yaml:"identities"`
}

// corpusStoreIdentity is one family's presence in the store.
type corpusStoreIdentity struct {
	Identity  string `yaml:"identity"`
	Provider  string `yaml:"provider"`
	InputType string `yaml:"input_type,omitempty"`
	Dimension int    `yaml:"dimension"`
	Chunks    int    `yaml:"chunks"`
}

// corpusChunkParameters is build configuration recorded with the store rather
// than a normative value: srd015 requires chunking and leaves the size, the
// overlap, and the splitter to the build that wrote the records.
type corpusChunkParameters struct {
	Splitter          string `yaml:"splitter"`
	TargetCharacters  int    `yaml:"target_characters"`
	OverlapCharacters int    `yaml:"overlap_characters"`
}

func defaultCorpusChunkParameters() corpusChunkParameters {
	return corpusChunkParameters{
		Splitter:          "paragraph",
		TargetCharacters:  900,
		OverlapCharacters: 320,
	}
}

// readCorpusManifest loads corpus/corpus.yaml and rejects a manifest that
// declares no clearance levels: without the ordered set the fail-closed level
// is undecidable and every later comparison is a guess.
func readCorpusManifest(root string) (corpusManifest, error) {
	var manifest corpusManifest
	path := filepath.Join(root, filepath.FromSlash(corpusManifestPath))
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest, fmt.Errorf("read corpus manifest: %w", err)
	}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return manifest, fmt.Errorf("parse corpus manifest %s: %w", path, err)
	}
	if len(manifest.ClearanceLevels) == 0 {
		return manifest, fmt.Errorf(
			"%s declares no clearance_levels; the ordered set is what makes"+
				" \"at or below\" decidable and what the fail-closed level is taken from"+
				" (srd010 R1.1)", path)
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return manifest, fmt.Errorf("%s declares no version; a store names the corpus"+
			" version it read (srd015 R4.5)", path)
	}
	return manifest, nil
}

// readCorpusDocuments parses every document under corpus/documents, splitting
// declared front matter from prose body. Documents are returned in filename
// order so a rebuild produces the same records in the same order.
func readCorpusDocuments(root string) ([]corpusDocument, error) {
	dir := filepath.Join(root, filepath.FromSlash(corpusDocumentsDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read corpus documents: %w", err)
	}
	documents := make([]corpusDocument, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		front, body, err := splitCorpusFrontMatter(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		documents = append(documents, corpusDocument{
			Path:        filepath.ToSlash(filepath.Join("documents", entry.Name())),
			FrontMatter: front,
			Body:        body,
		})
	}
	if len(documents) == 0 {
		return nil, fmt.Errorf("corpus %s holds no documents", dir)
	}
	sort.SliceStable(documents, func(i, j int) bool {
		return documents[i].Path < documents[j].Path
	})
	return documents, nil
}

// splitCorpusFrontMatter separates the leading YAML block from the prose. A
// document without one is an error rather than a document with empty metadata,
// because a missing block would otherwise flow through as an absent clearance
// and be written at the highest level, hiding an authoring mistake behind a
// fail-closed default that exists for a different case.
func splitCorpusFrontMatter(text string) (corpusFrontMatter, string, error) {
	var front corpusFrontMatter
	const fence = "---\n"
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(normalized, fence) {
		return front, "", fmt.Errorf("no YAML front matter: the file does not open with ---")
	}
	rest := normalized[len(fence):]
	end := strings.Index(rest, "\n"+strings.TrimSuffix(fence, "\n")+"\n")
	if end < 0 {
		return front, "", fmt.Errorf("front matter is not closed by a --- line")
	}
	block := rest[:end+1]
	body := rest[end+len("\n"+fence):]
	if err := yaml.Unmarshal([]byte(block), &front); err != nil {
		return front, "", fmt.Errorf("parse front matter: %w", err)
	}
	if strings.TrimSpace(front.SourceID) == "" {
		return front, "", fmt.Errorf("front matter declares no source_id")
	}
	return front, strings.TrimSpace(body), nil
}

// chunkCorpusDocument splits a document body into retrieval units and gives
// each one an identifier derived from the source document and its position, so
// a rebuild over an unchanged document yields the same identifier and overwrites
// the record rather than adding a second copy of it (srd015 R2.4).
func chunkCorpusDocument(
	document corpusDocument, manifest corpusManifest, params corpusChunkParameters,
) []corpusChunkRecord {
	texts := chunkCorpusText(document.Body, params)
	metadata := corpusChunkMetadataFrom(document.FrontMatter, manifest)
	records := make([]corpusChunkRecord, 0, len(texts))
	for index, text := range texts {
		position := index + 1
		records = append(records, corpusChunkRecord{
			ChunkID:       fmt.Sprintf("%s-%04d", document.FrontMatter.SourceID, position),
			Corpus:        manifest.ID,
			CorpusVersion: manifest.Version,
			Synthetic:     manifest.Synthetic,
			SourcePath:    document.Path,
			Position:      position,
			TextSHA256:    corpusTextDigest(text),
			Text:          text,
			Metadata:      metadata,
		})
	}
	return records
}

// corpusChunkMetadataFrom copies the document's declared front matter onto the
// chunk. The clearance is the only value it decides rather than copies, and it
// decides it in one direction: absent or outside the declared ordered set
// becomes the highest level (srd015 R2.5).
func corpusChunkMetadataFrom(
	front corpusFrontMatter, manifest corpusManifest,
) corpusChunkMetadata {
	clearance := strings.TrimSpace(front.Clearance)
	if !manifest.declaresClearance(clearance) {
		clearance = manifest.highestClearance()
	}
	keywords := append([]string(nil), front.Keywords...)
	return corpusChunkMetadata{
		SourceID:                        front.SourceID,
		SourceType:                      front.SourceType,
		Clearance:                       clearance,
		AdmiraltySourceReliability:      front.Admiralty.SourceReliability,
		AdmiraltyInformationCredibility: front.Admiralty.InformationCredibility,
		Keywords:                        keywords,
	}
}

// chunkCorpusText accumulates paragraphs to the target size and carries the
// previous chunk's closing paragraph forward as overlap when it fits the
// budget. Overlapping by whole paragraphs rather than by a character count
// keeps the stored text readable, which the store has to be (srd015 R6.3).
func chunkCorpusText(body string, params corpusChunkParameters) []string {
	spans := chunkCorpusTextWithSpans(body, params)
	texts := make([]string, 0, len(spans))
	for _, span := range spans {
		texts = append(texts, span.Text)
	}
	return texts
}

// corpusTextSpan is one chunk and the half-open region of the body it covers.
// The span is computed as the chunk is cut rather than recovered afterwards by
// searching for the text: overlap means a chunk's text is not always a
// contiguous substring of the body, so a search would find the wrong offset or
// none at all.
type corpusTextSpan struct {
	Text  string
	Start int
	End   int
}

// chunkCorpusTextWithSpans cuts a body and reports where each chunk came from.
func chunkCorpusTextWithSpans(body string, params corpusChunkParameters) []corpusTextSpan {
	paragraphs := splitCorpusParagraphs(body)
	if len(paragraphs) == 0 {
		return nil
	}
	paragraphs = splitOversizeParagraphs(paragraphs, params.TargetCharacters)
	offsets := locateParagraphs(body, paragraphs)
	// Chunks are accumulated as paragraph indices rather than as text, so each
	// one can be rendered and located from the same decision.
	var chunks [][]int
	var current []int
	length := 0
	for index, paragraph := range paragraphs {
		if length > 0 && length+len(paragraph)+2 > params.TargetCharacters {
			chunks = append(chunks, current)
			current, length = nil, 0
		}
		current = append(current, index)
		length += len(paragraph) + 2
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}

	spans := make([]corpusTextSpan, 0, len(chunks))
	for index, chunk := range chunks {
		included := chunk
		if index > 0 {
			previous := chunks[index-1]
			tail := previous[len(previous)-1]
			if len(paragraphs[tail]) <= params.OverlapCharacters {
				included = append([]int{tail}, chunk...)
			}
		}
		parts := make([]string, 0, len(included))
		for _, paragraph := range included {
			parts = append(parts, paragraphs[paragraph])
		}
		first, last := included[0], included[len(included)-1]
		spans = append(spans, corpusTextSpan{
			Text:  strings.Join(parts, "\n\n"),
			Start: offsets[first],
			End:   offsets[last] + len(paragraphs[last]),
		})
	}
	return spans
}

// locateParagraphs finds where each paragraph sits in the body it came from.
//
// The pieces appear in the body in the order the splitter produced them, so the
// scan moves forward and never rescans: a paragraph whose text repeats later in
// the document is located at its own occurrence rather than at the first one. A
// piece the splitter rewrote past recognition -- which the heading merge can
// produce -- falls back to the end of the previous piece, so a span stays
// ordered even when it cannot be exact.
func locateParagraphs(body string, paragraphs []string) []int {
	offsets := make([]int, len(paragraphs))
	cursor := 0
	for index, paragraph := range paragraphs {
		found := strings.Index(body[cursor:], paragraph)
		if found < 0 {
			offsets[index] = cursor
			continue
		}
		offsets[index] = cursor + found
		cursor = offsets[index] + len(paragraph)
	}
	return offsets
}

// splitOversizeParagraphs cuts any paragraph longer than the target into
// sentence-bounded pieces that fit it.
//
// The accumulator above can only decide where to end a chunk, never how to
// split one, so a paragraph longer than the target became a chunk of its own at
// whatever length it happened to be. That was harmless for a corpus authored as
// Markdown, where a paragraph is a few hundred characters, and is not harmless
// for real reporting: the ISW assessments arrive as one continuous run with no
// line breaks at all, tens of thousands of characters long. Left whole, each
// assessment is one chunk at every profile, the size profiles stop meaning
// anything, and the embedding provider truncates the passage it is given so the
// stored vector describes the opening paragraph of a document the chunk claims
// to be.
//
// A paragraph that already fits is returned untouched, so a corpus whose
// paragraphs are all shorter than the target chunks exactly as it did before
// (srd017 R4.3).
func splitOversizeParagraphs(paragraphs []string, target int) []string {
	if target <= 0 {
		return paragraphs
	}
	split := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if len(paragraph) <= target {
			split = append(split, paragraph)
			continue
		}
		split = append(split, splitParagraphOnSentences(paragraph, target)...)
	}
	return split
}

// splitParagraphOnSentences accumulates sentences up to the target. A sentence
// longer than the target on its own is cut on a rune boundary rather than
// dropped or left whole, because a corpus of real reporting carries run-on
// passages and neither losing one nor exceeding the target is acceptable.
func splitParagraphOnSentences(paragraph string, target int) []string {
	sentences := splitIntoSentences(paragraph)
	pieces := make([]string, 0, len(sentences))
	var current strings.Builder
	for _, sentence := range sentences {
		for len(sentence) > target {
			cut := target
			for cut > 0 && !utf8.RuneStart(sentence[cut]) {
				cut--
			}
			if current.Len() > 0 {
				pieces = append(pieces, strings.TrimSpace(current.String()))
				current.Reset()
			}
			pieces = append(pieces, strings.TrimSpace(sentence[:cut]))
			sentence = sentence[cut:]
		}
		if current.Len() > 0 && current.Len()+len(sentence)+1 > target {
			pieces = append(pieces, strings.TrimSpace(current.String()))
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString(" ")
		}
		current.WriteString(sentence)
	}
	if current.Len() > 0 {
		pieces = append(pieces, strings.TrimSpace(current.String()))
	}
	return pieces
}

// splitIntoSentences breaks a passage after terminal punctuation followed by a
// space. It is deliberately simple: the boundary only has to be a place a
// reader would accept a chunk ending, and an abbreviation that ends a piece one
// sentence early costs nothing a reader would notice.
func splitIntoSentences(paragraph string) []string {
	sentences := make([]string, 0, 16)
	start := 0
	for index := 0; index < len(paragraph)-1; index++ {
		switch paragraph[index] {
		case '.', '!', '?':
			if paragraph[index+1] == ' ' {
				if sentence := strings.TrimSpace(paragraph[start : index+1]); sentence != "" {
					sentences = append(sentences, sentence)
				}
				start = index + 1
			}
		}
	}
	if sentence := strings.TrimSpace(paragraph[start:]); sentence != "" {
		sentences = append(sentences, sentence)
	}
	return sentences
}

// splitCorpusParagraphs breaks a body on blank lines, dropping empties. A
// Markdown heading is kept with the text under it rather than split off, so a
// chunk carries the subject its prose refers to.
func splitCorpusParagraphs(body string) []string {
	blocks := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n")
	paragraphs := make([]string, 0, len(blocks))
	for _, block := range blocks {
		trimmed := strings.TrimSpace(block)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") && len(paragraphs) == 0 {
			paragraphs = append(paragraphs, trimmed)
			continue
		}
		paragraphs = append(paragraphs, trimmed)
	}
	if len(paragraphs) > 1 && strings.HasPrefix(paragraphs[0], "#") {
		paragraphs = append([]string{paragraphs[0] + "\n\n" + paragraphs[1]}, paragraphs[2:]...)
	}
	return paragraphs
}

func corpusTextDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// corpusRecordFilename is the chunk id lowercased, so the filesystem layout is
// as stable as the identifier and a rebuild rewrites a record in place.
func corpusRecordFilename(chunkID string) string {
	return strings.ToLower(chunkID) + corpusRecordExtension
}

// loadCorpusStore reads every committed chunk record, keyed by chunk id. A
// store that does not exist yet is an empty store rather than an error, so the
// first build on a clean checkout is an ordinary build.
func loadCorpusStore(root string) (map[string]corpusChunkRecord, error) {
	dir := filepath.Join(root, filepath.FromSlash(corpusStoreChunksDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]corpusChunkRecord{}, nil
		}
		return nil, fmt.Errorf("read chunk store: %w", err)
	}
	records := make(map[string]corpusChunkRecord, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), corpusRecordExtension) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		var record corpusChunkRecord
		if err := yaml.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("parse chunk record %s: %w", entry.Name(), err)
		}
		if record.ChunkID == "" {
			return nil, fmt.Errorf("chunk record %s carries no chunk_id", entry.Name())
		}
		records[record.ChunkID] = record
	}
	return records, nil
}

// writeCorpusChunkRecord writes one record whole, through a temporary file in
// the same directory renamed into place. A record appears complete or not at
// all: a half-written vector is the condition a later build would read as
// already held and never retry (srd015 R4.4).
func writeCorpusChunkRecord(root string, record corpusChunkRecord) error {
	dir := filepath.Join(root, filepath.FromSlash(corpusStoreChunksDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create chunk store directory: %w", err)
	}
	encoded, err := encodeCorpusYAML(record)
	if err != nil {
		return fmt.Errorf("encode chunk %s: %w", record.ChunkID, err)
	}
	path := filepath.Join(dir, corpusRecordFilename(record.ChunkID))
	return writeFileAtomically(path, encoded)
}

// writeCorpusStoreIndex writes corpus/store/store.yaml the same way.
func writeCorpusStoreIndex(root string, index corpusStoreIndex) error {
	dir := filepath.Join(root, filepath.FromSlash(corpusStoreDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create store directory: %w", err)
	}
	encoded, err := encodeCorpusYAML(index)
	if err != nil {
		return fmt.Errorf("encode store index: %w", err)
	}
	return writeFileAtomically(filepath.Join(root, filepath.FromSlash(corpusStoreIndexPath)), encoded)
}

// readCorpusStoreIndex loads the store index. A store with no index is
// reported as absent so a seed can say the store was never built rather than
// that it holds no identities.
func readCorpusStoreIndex(root string) (corpusStoreIndex, error) {
	var index corpusStoreIndex
	path := filepath.Join(root, filepath.FromSlash(corpusStoreIndexPath))
	data, err := os.ReadFile(path)
	if err != nil {
		return index, fmt.Errorf("read store index: %w", err)
	}
	if err := yaml.Unmarshal(data, &index); err != nil {
		return index, fmt.Errorf("parse store index %s: %w", path, err)
	}
	return index, nil
}

// encodeCorpusYAML renders a record at a fixed indent so a rebuild that changes
// nothing produces a byte-identical file and store equality is a comparison a
// test can make (srd015 R4.3).
func encodeCorpusYAML(value interface{}) ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteString("# Copyright (c) 2026 Mesh Intelligence Inc.\n")
	buffer.WriteString("# SPDX-License-Identifier: MIT-0\n")
	buffer.WriteString("# Generated by mage corpus:build over the Ukraine 2022 corpus.\n")
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(value); err != nil {
		_ = encoder.Close()
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// writeFileAtomically renames a temporary file in the destination directory
// over the target, so a reader sees the old file or the new one.
func writeFileAtomically(path string, data []byte) error {
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return fmt.Errorf("chmod %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("rename into %s: %w", path, err)
	}
	return nil
}

// sortedCorpusChunkIDs orders chunk ids for stable reporting and iteration.
func sortedCorpusChunkIDs(records map[string]corpusChunkRecord) []string {
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
