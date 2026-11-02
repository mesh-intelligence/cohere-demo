// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"strconv"
	"strings"
)

// The multi-profile chunker (srd017 R4). rel10.4 cut one corpus at one size,
// because one size is all a retrieval demonstration needs. rel10.5 cuts the
// same passage at several, so the interface can move between chunk sizes over
// one document and a citation can widen from the sentence to the section it
// sits in.
//
// The constraint that shapes the implementation is R4.3: the medium-900 profile
// reproduces, character for character, what the rel10.4 chunker produces for the
// same input. Every vector in the committed store is keyed to the text that
// chunker wrote, so a profile that merely resembles it orphans all of them while
// the identity comparison still passes.

// Profile names, as srd017 R4.1 declares them.
//
// A fourth profile cutting at the source's own section headings was specified
// and withdrawn (GH-278). None of the five tiers writes headings -- the
// assessments are continuous prose, the messages are single posts, and the two
// aggregate tiers are generated digests -- so it returned one chunk per
// document and duplicated the atomic profile at the cost of a fourth set of
// records in every store and count.
const (
	chunkProfileAtomic = "atomic"
	chunkProfileMedium = "medium-900"
	chunkProfileLarge  = "large-1800"
)

// Splitters a profile may use. The splitter names what a chunk boundary is, and
// the sizes mean nothing without it: 900 characters of paragraphs and 900
// characters of sentences are different corpora.
const (
	chunkSplitterSegment   = "segment"
	chunkSplitterParagraph = "paragraph"
)

// chunkProfile is one named way of cutting a document. Profiles are build
// configuration recorded with the store rather than normative values, which is
// the treatment srd015 R4.5 gives chunking parameters and srd017 R4.5 extends
// to the profile set.
type chunkProfile struct {
	Name              string `yaml:"name"`
	Splitter          string `yaml:"splitter"`
	TargetCharacters  int    `yaml:"target_characters,omitempty"`
	OverlapCharacters int    `yaml:"overlap_characters,omitempty"`
}

// parameters renders a profile as the chunking parameters the paragraph
// splitter takes, so one function decides how a body is cut whichever profile
// asked for it.
func (p chunkProfile) parameters() corpusChunkParameters {
	return corpusChunkParameters{
		Splitter:          p.Splitter,
		TargetCharacters:  p.TargetCharacters,
		OverlapCharacters: p.OverlapCharacters,
	}
}

// defaultChunkProfiles returns the three declared profiles in the order srd017
// R4.1 states them.
//
// medium-900 takes its parameters from defaultCorpusChunkParameters rather than
// restating them, which is what makes R4.3 hold by construction: the two cannot
// drift because there is one set of values.
func defaultChunkProfiles() []chunkProfile {
	medium := defaultCorpusChunkParameters()
	return []chunkProfile{
		{Name: chunkProfileAtomic, Splitter: chunkSplitterSegment},
		{
			Name:              chunkProfileMedium,
			Splitter:          chunkSplitterParagraph,
			TargetCharacters:  medium.TargetCharacters,
			OverlapCharacters: medium.OverlapCharacters,
		},
		{
			Name:              chunkProfileLarge,
			Splitter:          chunkSplitterParagraph,
			TargetCharacters:  1800,
			OverlapCharacters: 300,
		},
	}
}

// chunkProfileNamed returns one declared profile.
func chunkProfileNamed(name string) (chunkProfile, bool) {
	for _, profile := range defaultChunkProfiles() {
		if profile.Name == name {
			return profile, true
		}
	}
	return chunkProfile{}, false
}

// chunkEnvelope cuts one envelope at one profile. Only searchable segments are
// chunked (R4.2), so the classification-authority line, the endnote tail, and
// the foreign-agent disclaimer stay in the served document and out of every
// vector.
func chunkEnvelope(
	envelope corpusEnvelope, profile chunkProfile, manifest corpusManifest,
) []corpusChunkRecord {
	spans := chunkEnvelopeSpans(envelope, profile)
	metadata := corpusChunkMetadataFromEnvelope(envelope, manifest)
	provenance := envelope.Provenance
	records := make([]corpusChunkRecord, 0, len(spans))
	for index, span := range spans {
		position := index + 1
		record := corpusChunkRecord{
			ChunkID:       chunkIdentifier(envelope.DocumentID, profile.Name, position),
			ChunkProfile:  profile.Name,
			Corpus:        manifest.ID,
			CorpusVersion: manifest.Version,
			Synthetic:     envelope.Synthetic,
			SourcePath:    envelope.RawRef.Path,
			Position:      position,
			TextSHA256:    corpusTextDigest(span.Text),
			Text:          span.Text,
			Parent: &corpusChunkParent{
				DocumentID:   envelope.DocumentID,
				RawRef:       envelope.RawRef,
				Position:     position,
				SiblingCount: len(spans),
				CharSpan:     []int{span.Start, span.End},
			},
			References: resolveChunkReferences(span.Text, envelope.References),
			Metadata:   metadata,
		}
		// The provenance travels onto every chunk of the document rather than
		// onto the document alone, because retrieval returns chunks and the
		// cross-tier walk starts from whichever one was cited (srd017 R5.4).
		if len(provenance.DerivedFrom) > 0 || strings.TrimSpace(provenance.Collector) != "" {
			carried := provenance
			carried.DerivedFrom = append([]corpusDerivedRef(nil), provenance.DerivedFrom...)
			record.Provenance = &carried
		}
		records = append(records, record)
	}
	// Neighbours are filled once the count is known, so the last chunk of a
	// document carries no next reference rather than one that dangles (R5.2).
	for index := range records {
		if index > 0 {
			records[index].Parent.PrevChunkID = records[index-1].ChunkID
		}
		if index+1 < len(records) {
			records[index].Parent.NextChunkID = records[index+1].ChunkID
		}
	}
	return records
}

// coarserProfileOf names the profile whose chunks cover the same passage at the
// next larger size. The chain runs atomic to medium to large, which is the
// order the sizes coarsen in, and ends at the largest.
func coarserProfileOf(name string) string {
	switch name {
	case chunkProfileAtomic:
		return chunkProfileMedium
	case chunkProfileMedium:
		return chunkProfileLarge
	default:
		return ""
	}
}

// chunkEnvelopeAtProfiles cuts one envelope at every profile and links each
// chunk to the coarser chunk covering the same passage (srd017 R5.3), which is
// what lets an interface widen from a sentence to the section it sits in.
//
// The link is decided by character span rather than by position, because the
// profiles cut at different boundaries and a chunk's position in one says
// nothing about where it falls in another.
func chunkEnvelopeAtProfiles(
	envelope corpusEnvelope, profiles []chunkProfile, manifest corpusManifest,
) map[string][]corpusChunkRecord {
	byProfile := make(map[string][]corpusChunkRecord, len(profiles))
	for _, profile := range profiles {
		byProfile[profile.Name] = chunkEnvelope(envelope, profile, manifest)
	}
	for name, records := range byProfile {
		coarser, linked := byProfile[coarserProfileOf(name)]
		if !linked {
			continue
		}
		for index := range records {
			records[index].Parent.CoarserParent = coveringChunk(records[index], coarser)
		}
	}
	return byProfile
}

// coveringChunk returns the coarser chunk overlapping this one most. A chunk
// whose span sits inside one coarser chunk resolves to it; a chunk straddling
// two resolves to the one holding more of it, so the link is defined for every
// chunk rather than only for the ones that nest cleanly.
func coveringChunk(record corpusChunkRecord, coarser []corpusChunkRecord) string {
	if record.Parent == nil || len(record.Parent.CharSpan) != 2 || len(coarser) == 0 {
		return ""
	}
	start, end := record.Parent.CharSpan[0], record.Parent.CharSpan[1]
	best, bestOverlap := "", 0
	for _, candidate := range coarser {
		if candidate.Parent == nil || len(candidate.Parent.CharSpan) != 2 {
			continue
		}
		overlap := min(end, candidate.Parent.CharSpan[1]) - max(start, candidate.Parent.CharSpan[0])
		if overlap > bestOverlap {
			best, bestOverlap = candidate.ChunkID, overlap
		}
	}
	if best == "" && len(coarser) > 0 {
		// A zero-length span overlaps nothing, so the chunk resolves to the
		// coarser chunk it starts inside rather than to nothing at all.
		for _, candidate := range coarser {
			if candidate.Parent != nil && len(candidate.Parent.CharSpan) == 2 &&
				start >= candidate.Parent.CharSpan[0] && start <= candidate.Parent.CharSpan[1] {
				return candidate.ChunkID
			}
		}
	}
	return best
}

// resolveChunkReferences returns the document's citation entries that this
// chunk's own text cites, in the order the text cites them, each once however
// often it is cited (srd017 R10.4).
//
// The chunk's text is read and not written. That is the whole of the design:
// the stored vector was computed over this text, the build carries a vector
// forward only where its digest is unchanged, and the interface locates the
// passage inside its parent document by matching this text against it. Editing
// the prose to remove the markers -- the obvious way to stop a chunk citing
// numbers it cannot resolve -- would discard every vector the tier holds and
// break that match, while leaving a panel that still renders.
//
// A marker naming an entry the document does not carry is passed over rather
// than reported. Reconciliation already refused such a document at conversion,
// so a second check here would either never fire or contradict the first.
//
// A document carrying no entries resolves nothing, which is every tier but the
// assessments.
func resolveChunkReferences(text string, carried []corpusReference) []corpusReference {
	if len(carried) == 0 {
		return nil
	}
	byNumber := make(map[int]corpusReference, len(carried))
	for _, reference := range carried {
		byNumber[reference.Number] = reference
	}
	var resolved []corpusReference
	cited := make(map[int]bool)
	for _, match := range iswMarkerPattern.FindAllStringSubmatch(text, -1) {
		number, err := strconv.Atoi(match[1])
		if err != nil || cited[number] {
			continue
		}
		reference, held := byNumber[number]
		if !held {
			continue
		}
		cited[number] = true
		resolved = append(resolved, reference)
	}
	return resolved
}

// chunkIdentifier names a chunk by its document, the profile that cut it, and
// its position. The profile is in the identifier because the profiles of one
// document coexist in the store and would otherwise collide at every position
// (R4.4).
func chunkIdentifier(documentID, profile string, position int) string {
	return fmt.Sprintf("%s-%s-%04d", documentID, profile, position)
}

// chunkEnvelopeSpans cuts the searchable body according to the profile's
// splitter and reports where each chunk sits in it. The atomic profile is the
// one that does not use the joined body to decide boundaries: it cuts per
// segment, so a chunk is a unit the source itself wrote.
func chunkEnvelopeSpans(envelope corpusEnvelope, profile chunkProfile) []corpusTextSpan {
	body := envelope.searchableText()
	switch profile.Splitter {
	case chunkSplitterSegment:
		texts := make([]string, 0, len(envelope.Segments))
		for _, segment := range envelope.Segments {
			if !segment.Searchable {
				continue
			}
			if text := strings.TrimSpace(segment.Text); text != "" {
				texts = append(texts, text)
			}
		}
		return spansOfTexts(body, texts)
	default:
		return chunkCorpusTextWithSpans(body, profile.parameters())
	}
}

// spansOfTexts locates pieces that are contiguous substrings of the body, which
// the segment splitter produces because it neither reorders nor overlaps what
// it cuts.
func spansOfTexts(body string, texts []string) []corpusTextSpan {
	offsets := locateParagraphs(body, texts)
	spans := make([]corpusTextSpan, 0, len(texts))
	for index, text := range texts {
		spans = append(spans, corpusTextSpan{
			Text:  text,
			Start: offsets[index],
			End:   offsets[index] + len(text),
		})
	}
	return spans
}

// corpusChunkMetadataFromEnvelope copies the envelope's declared values onto the
// chunk. Nothing here reads chunk text to decide a value, which is the
// provenance rule srd010 R1.2 states and srd015 R2.3 carries onto the record.
//
// The clearance is taken as the adapter decided it, from the tier policy or the
// declared marking, and is checked against the ordered set here as a second
// gate: a level outside the set is written at the highest rather than passed
// through (srd017 R2.5).
func corpusChunkMetadataFromEnvelope(
	envelope corpusEnvelope, manifest corpusManifest,
) corpusChunkMetadata {
	clearance := strings.TrimSpace(envelope.Clearance)
	if !manifest.declaresClearance(clearance) {
		clearance = manifest.highestClearance()
	}
	reliability, credibility := admiraltyForEnvelope(envelope, manifest)
	return corpusChunkMetadata{
		SourceID:                        envelope.DocumentID,
		SourceType:                      envelope.SourceType,
		Clearance:                       clearance,
		AdmiraltySourceReliability:      reliability,
		AdmiraltyInformationCredibility: credibility,
		Keywords:                        nil,
	}
}

// admiraltyForEnvelope decides a chunk's Admiralty rating, which is what
// rel12.2 checks a brief's source-evaluation table against.
//
// A document carrying its own NATO letter-number grade keeps it. That is the
// synthetic tier, whose reports are written with a per-source grade, and the
// grade is the collector's evaluation of that source on that day.
//
// A document carrying none takes its tier's declared rating. That is the four
// real tiers: published reporting is not graded on this scale by whoever
// published it, and an Admiralty rating evaluates a source rather than a
// sentence, so the tier is the right unit to declare it at.
//
// Neither path reads a document to decide the rating. The per-document grade
// is copied from what the source declared, and the per-tier grade is declared
// in the corpus artifact -- which is the provenance rule srd010 R1.2 states and
// srd015 R2.3 carries onto the record. A tier the manifest rates not at all
// returns no rating rather than an invented one.
func admiraltyForEnvelope(envelope corpusEnvelope, manifest corpusManifest) (string, int) {
	if letter, credibility := admiraltyFromReliability(envelope.Reliability); letter != "" {
		return letter, credibility
	}
	return admiraltyFromReliability(manifest.TierAdmiralty[envelope.SourceType])
}

// admiraltyFromReliability reads a NATO letter-number rating -- B3, A1, C4 --
// into the two Admiralty axes. A value that is not one, which is what the real
// tiers' descriptive reliabilities are, returns no rating.
func admiraltyFromReliability(reliability string) (string, int) {
	value := strings.TrimSpace(strings.ToUpper(reliability))
	if len(value) != 2 {
		return "", 0
	}
	letter := value[:1]
	if letter < "A" || letter > "F" {
		return "", 0
	}
	credibility, err := strconv.Atoi(value[1:])
	if err != nil || credibility < 1 || credibility > 6 {
		return "", 0
	}
	return letter, credibility
}

// chunkEnvelopesAtProfiles cuts every envelope at every declared profile and
// returns the records in a deterministic order -- by profile, then by the order
// the slice loader produced. Ordering by profile keeps a store diff readable
// when one profile's parameters change.
func chunkEnvelopesAtProfiles(
	envelopes []corpusEnvelope, profiles []chunkProfile, manifest corpusManifest,
) []corpusChunkRecord {
	cut := make([]map[string][]corpusChunkRecord, 0, len(envelopes))
	for _, envelope := range envelopes {
		cut = append(cut, chunkEnvelopeAtProfiles(envelope, profiles, manifest))
	}
	records := make([]corpusChunkRecord, 0, len(envelopes)*len(profiles))
	for _, profile := range profiles {
		for _, byProfile := range cut {
			records = append(records, byProfile[profile.Name]...)
		}
	}
	return records
}

// buildUkraineCorpusStore is the rel10.5 build: it loads the windowed slice from
// the source datasets, cuts every envelope at every declared profile, and writes
// each document's envelope beside its chunks in the multi-version store.
//
// This is the build the demo runs. The synthetic corpus it replaced, and the
// flat chunk directory that corpus was stored in, are retired (srd017 R9.1).
func buildUkraineCorpusStore(
	root string,
	dataRoot string,
	identity corpusEmbeddingIdentity,
	slice corpusSliceParameters,
	profiles []chunkProfile,
	selection corpusEmbedSelection,
) (corpusBuildReport, corpusSliceReport, error) {
	var sliceReport corpusSliceReport
	report := corpusBuildReport{Identity: identity.identity, Provider: identity.provider}
	manifest, err := readCorpusManifest(root)
	if err != nil {
		return report, sliceReport, err
	}
	mapping := clearanceMapping{
		Markings: manifest.ClearanceMarkings,
		Caveats:  manifest.ClearanceCaveats,
	}
	envelopes, sliceReport, err := loadCorpusSliceFor(root, dataRoot, slice, mapping, manifest)
	if err != nil {
		return report, sliceReport, err
	}

	// Everything is cut before anything is embedded, because the ceiling is a
	// property of the whole selection and cannot be checked one document at a
	// time (srd017 R6.3).
	candidates := make([]corpusEmbedCandidate, 0, len(envelopes))
	for _, envelope := range envelopes {
		byProfile := chunkEnvelopeAtProfiles(envelope, profiles, manifest)
		records := make([]corpusChunkRecord, 0, len(profiles))
		for _, profile := range profiles {
			records = append(records, byProfile[profile.Name]...)
		}
		candidates = append(candidates, corpusEmbedCandidate{Envelope: envelope, Records: records})
		report.Chunks += len(records)
	}

	plan, err := planCorpusEmbedding(candidates, selection)
	if err != nil {
		return report, sliceReport, err
	}

	// A passage is embedded once however many chunks carry it, which the corpus
	// makes worth doing: the tiers repeat standing paragraphs, wire copy, and
	// templated sentences.
	identity, memo := memoizeEmbedder(identity)

	stored, err := loadCorpusDocumentStore(root)
	if err != nil {
		return report, sliceReport, err
	}
	held := make(map[string]corpusChunkRecord)
	for _, document := range stored {
		for _, chunk := range document.Chunks {
			held[chunk.ChunkID] = chunk
		}
	}

	wantedDocuments := make(map[string]bool, len(candidates))
	wantedChunks := make(map[string]bool, report.Chunks)
	for _, candidate := range candidates {
		wantedDocuments[candidate.Envelope.DocumentID] = true
		written, err := writeCorpusEnvelopeIfChanged(root, candidate.Envelope)
		if err != nil {
			return report, sliceReport, err
		}
		if written {
			report.Written++
		}
		for _, record := range candidate.Records {
			wantedChunks[record.ChunkID] = true
			completed := record
			if plan.Selected[record.ChunkID] {
				embedded := false
				completed, embedded, err = resolveCorpusChunkVector(record, held, identity)
				if err != nil {
					return report, sliceReport, err
				}
				if embedded {
					report.Embedded++
				} else {
					report.Reused++
				}
				// Chroma pins a collection's dimension at creation, so two
				// widths in one store is a seed that cannot succeed. The
				// disagreement is caught here rather than at the seed, where it
				// would surface as a rejected add halfway through
				// (srd015 R3.5).
				values, _ := completed.vector(identity.identity)
				if report.Dimension == 0 {
					report.Dimension = len(values)
				} else if len(values) != report.Dimension {
					return report, sliceReport, fmt.Errorf(
						"chunk %s embedded to %d dimensions under identity %q but earlier"+
							" chunks embedded to %d; a collection pins its dimension at"+
							" creation and cannot hold both (srd015 R3.5)",
						completed.ChunkID, len(values), identity.identity, report.Dimension)
				}
			}
			// A chunk outside the selection is stored without a vector and
			// without an error, and one a wider selection already paid for has
			// its vectors removed rather than carried (R6.4). The seed writes
			// the selection alone into the retrieval collection, so a kept
			// vector is weight the repository carries and nothing serves --
			// and weight is what the window is sized by (R3.3). What removal
			// costs is the free re-widening a kept vector bought: cents of
			// embedding spend at this corpus's scale, against roughly half the
			// store.
			written, err := writeCorpusDocumentChunkIfChanged(root, completed)
			if err != nil {
				return report, sliceReport, err
			}
			if written {
				report.Written++
			}
		}
	}

	report.Deduplicated = memo.requestsSaved()

	removed, err := pruneCorpusDocumentStore(root, wantedDocuments, wantedChunks)
	if err != nil {
		return report, sliceReport, err
	}
	report.Removed = removed

	if err := writeUkraineStoreIndex(root, manifest, slice, selection, identity); err != nil {
		return report, sliceReport, err
	}
	return report, sliceReport, nil
}

// writeUkraineStoreIndex recomputes the index from the documents on disk, so it
// names the profiles the store holds and how many chunks of each carry a vector
// rather than reporting what this build happened to write (srd017 R7.2).
func writeUkraineStoreIndex(
	root string,
	manifest corpusManifest,
	slice corpusSliceParameters,
	selection corpusEmbedSelection,
	built corpusEmbeddingIdentity,
) error {
	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		return err
	}
	records := make(map[string]corpusChunkRecord)
	for _, document := range documents {
		for _, chunk := range document.Chunks {
			records[chunk.ChunkID] = chunk
		}
	}
	previous, err := readCorpusStoreIndex(root)
	if err != nil {
		previous = corpusStoreIndex{}
	}
	sliceParams := slice
	embedSelection := selection
	index := corpusStoreIndex{
		Corpus:          manifest.ID,
		CorpusVersion:   manifest.Version,
		Synthetic:       manifest.Synthetic,
		SyntheticNotice: strings.TrimSpace(manifest.SyntheticNotice),
		Chunking:        defaultCorpusChunkParameters(),
		Chunks:          len(records),
		Documents:       len(documents),
		Profiles:        corpusStoreProfiles(documents, built.identity),
		Slice:           &sliceParams,
		EmbedSelection:  &embedSelection,
		Identities:      corpusStoreIdentities(records, built, previous.Identities),
	}
	return writeCorpusStoreIndex(root, index)
}
