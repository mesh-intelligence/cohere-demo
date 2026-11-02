// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The normalized ingest envelope every source tier is read through (srd017 R1).
// rel10.4 read one corpus whose documents declared their own front matter, so
// the reader and the schema were authored together. rel10.5 reads five real
// datasets in four different shapes, none of which declares anything the build
// wants, and the envelope is what keeps the window filter, the chunker, the
// lineage pass, and the store written once rather than five times.
//
// The rule that makes it work is R1.2: every adapter populates the same
// top-level fields, so a consumer cannot tell which one ran without reading
// SourceType.

// The five declared source-type names (srd017 R1.1). They travel onto the chunk
// metadata and the collection record, so the interface facets and the corpus
// manifest name the same values these constants do.
const (
	sourceTypeHumint          = "humint"
	sourceTypeOSINTNews       = "osint_news"
	sourceTypeOSINTAssessment = "osint_assessment"
	sourceTypeSIGINTEvent     = "sigint_event"
	sourceTypeSIGINTSeries    = "sigint_series"
	corpusEnvelopeDateLayout  = "2006-01-02"
)

// corpusSourceTypes is the declared set, in the order srd017 R1.1 states them.
func corpusSourceTypes() []string {
	return []string{
		sourceTypeHumint,
		sourceTypeOSINTNews,
		sourceTypeOSINTAssessment,
		sourceTypeSIGINTEvent,
		sourceTypeSIGINTSeries,
	}
}

// Segment roles. The role names what a segment is; Searchable decides whether
// it is chunked. The two are separate because R1.4 requires material carried
// for display and citation to stay in the served document while staying out of
// every vector.
const (
	segmentRoleAuthority  = "authority"
	segmentRoleByline     = "byline"
	segmentRoleSummary    = "summary"
	segmentRoleBody       = "body"
	segmentRoleComment    = "comment"
	segmentRoleEndnotes   = "endnotes"
	segmentRoleDisclaimer = "disclaimer"
)

// corpusSegment is one typed unit of a document body (srd017 R1.3). Holding the
// body as segments rather than as one string is what lets the searchable flag
// exist at all: a document is not uniformly embeddable, and the parts that are
// not have to be addressable to be excluded.
type corpusSegment struct {
	Role       string `yaml:"role"`
	Searchable bool   `yaml:"searchable"`
	Text       string `yaml:"text"`
}

// corpusRawRef points back at the untransformed source record (srd017 R1.6), so
// a served document is attributable to a line in a dataset rather than only to
// the build that read it.
type corpusRawRef struct {
	Store string `yaml:"store" json:"store"`
	ID    string `yaml:"id" json:"id"`
	Path  string `yaml:"path" json:"path"`
}

// corpusDerivedRef is one cross-tier provenance edge (srd017 R5.4): the tier and
// identifier of reporting this document was built from, and the relation that
// says how. Relation distinguishes a report written over another document from
// a digest aggregating rows, which a consumer cannot infer from the identifier.
type corpusDerivedRef struct {
	Tier     string `yaml:"tier" json:"tier"`
	ID       string `yaml:"id" json:"id"`
	Relation string `yaml:"relation" json:"relation"`
}

// Relations a derived reference may declare.
const (
	derivedRelationWrittenOver = "written_over"
	derivedRelationAggregates  = "aggregates"
)

// corpusProvenance carries who produced the document and what it was derived
// from. Collector is the reporting chain for the synthetic tier and the
// publisher for the real ones.
type corpusProvenance struct {
	Collector   string             `yaml:"collector"`
	DerivedFrom []corpusDerivedRef `yaml:"derived_from,omitempty"`
}

// corpusEnvelope is one document in the shape every tier is read into. The
// top-level fields are R1.2's required set; Segments is R1.3's typed body.
type corpusEnvelope struct {
	DocumentID  string           `yaml:"document_id"`
	SourceType  string           `yaml:"source_type"`
	Clearance   string           `yaml:"clearance"`
	Caveats     []string         `yaml:"caveats,omitempty"`
	Issued      string           `yaml:"issued"`
	Origin      string           `yaml:"origin"`
	Reliability string           `yaml:"reliability"`
	Provenance  corpusProvenance `yaml:"provenance"`
	Title       string           `yaml:"title"`
	Synthetic   bool             `yaml:"synthetic"`
	RawRef      corpusRawRef     `yaml:"raw_ref"`
	Segments    []corpusSegment  `yaml:"segments"`
	// The citation entries an assessment's endnote tail carries, keyed by
	// endnote number (srd017 R10.1). Only the assessment tier fills this: the
	// field is omitted from a document that carries no tail, so every other
	// tier's envelope encodes exactly as it did before assessments gained
	// references.
	References []corpusReference `yaml:"references,omitempty"`
}

// corpusReference is one numbered entry of an assessment's citation tail. The
// text is the entry as the source wrote it, and the URLs are what was
// extracted from that text without altering either -- a tier that obscures its
// own links keeps them obscured, because the obscuring is the publisher's
// editorial decision and reversing it is not this build's to make
// (srd017 R10.1). An entry that resolves to no URL keeps its text rather than
// being dropped (R10.3): the tier writes plain-text bibliographic citations,
// and discarding one leaves a marker resolving to nothing.
type corpusReference struct {
	Number int      `yaml:"number" json:"number"`
	Text   string   `yaml:"text" json:"text"`
	URLs   []string `yaml:"urls,omitempty" json:"urls,omitempty"`
}

// searchableText joins the segments that are chunked, in document order. This
// is the body the chunker splits and the body a chunk's character span indexes
// into, so lineage offsets and chunk text are decided by one function rather
// than by two that agree today.
func (e corpusEnvelope) searchableText() string {
	parts := make([]string, 0, len(e.Segments))
	for _, segment := range e.Segments {
		if !segment.Searchable {
			continue
		}
		if text := strings.TrimSpace(segment.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// issuedDate parses the issued date. R1.5 makes the date the source's own, so a
// value that will not parse is an error rather than something to default.
func (e corpusEnvelope) issuedDate() (time.Time, error) {
	return time.Parse(corpusEnvelopeDateLayout, e.Issued)
}

// hasCaveat reports whether a dissemination caveat rides on this document.
func (e corpusEnvelope) hasCaveat(caveat string) bool {
	for _, carried := range e.Caveats {
		if strings.EqualFold(carried, caveat) {
			return true
		}
	}
	return false
}

// corpusEnvelopeRequiredFields names the top-level fields R1.2 requires every
// adapter to populate. The test that holds the five adapters to one shape reads
// this list rather than carrying its own copy, so a field added to the envelope
// is a field the parity assertion covers.
func corpusEnvelopeRequiredFields() []string {
	return []string{
		"document_id", "source_type", "clearance", "issued",
		"origin", "reliability", "provenance", "title", "raw_ref", "segments",
	}
}

// populatedFields reports which of the required fields this envelope actually
// carries a value for. Caveats and derived_from are omitted deliberately: they
// are present when the document has them and absent when it does not, so
// requiring them would make an uncaveated document a defect.
func (e corpusEnvelope) populatedFields() map[string]bool {
	populated := map[string]bool{
		"document_id": strings.TrimSpace(e.DocumentID) != "",
		"source_type": strings.TrimSpace(e.SourceType) != "",
		"clearance":   strings.TrimSpace(e.Clearance) != "",
		"issued":      strings.TrimSpace(e.Issued) != "",
		"origin":      strings.TrimSpace(e.Origin) != "",
		"reliability": strings.TrimSpace(e.Reliability) != "",
		"provenance":  strings.TrimSpace(e.Provenance.Collector) != "",
		"title":       strings.TrimSpace(e.Title) != "",
		"raw_ref":     strings.TrimSpace(e.RawRef.ID) != "" && strings.TrimSpace(e.RawRef.Store) != "",
		"segments":    len(e.Segments) > 0,
	}
	return populated
}

// validate holds an envelope to the envelope contract before anything
// downstream consumes it. It fails rather than repairs: a document that reaches
// the chunker missing a field is a document whose chunks carry a metadata gap
// the store would then record as though it were the source's.
func (e corpusEnvelope) validate() error {
	populated := e.populatedFields()
	missing := make([]string, 0, len(populated))
	for _, field := range corpusEnvelopeRequiredFields() {
		if !populated[field] {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("envelope %q (%s) populates no %s; every adapter"+
			" populates the same fields (srd017 R1.2)",
			e.DocumentID, e.SourceType, strings.Join(missing, ", "))
	}
	if _, err := e.issuedDate(); err != nil {
		return fmt.Errorf("envelope %q carries issued %q, which is not a %s date:"+
			" the issued date is the one the source itself carries and is not"+
			" defaulted (srd017 R1.5)", e.DocumentID, e.Issued, corpusEnvelopeDateLayout)
	}
	if !corpusRecognizedSourceType(e.SourceType) {
		return fmt.Errorf("envelope %q declares source type %q, which is not one of"+
			" the five declared tiers %v (srd017 R1.1) or a tier a case declares",
			e.DocumentID, e.SourceType, corpusSourceTypes())
	}
	if strings.TrimSpace(e.searchableText()) == "" {
		return fmt.Errorf("envelope %q marks no segment searchable; a document"+
			" that reaches the chunker with nothing to chunk is an adapter"+
			" defect rather than an empty document", e.DocumentID)
	}
	return nil
}

// clearanceMapping is the declared marking-to-level mapping and the caveat
// vocabulary (srd017 R2.2, R2.3). It is configuration the corpus artifact
// declares rather than a table resident in build code, so a reviewer reads the
// mapping where the corpus is defined and a change to it is a change to the
// corpus rather than to the build.
type clearanceMapping struct {
	Markings map[string]string `yaml:"clearance_markings"`
	Caveats  []string          `yaml:"clearance_caveats"`
}

// clearanceFromMarking maps one source marking onto the declared ordered set and
// separates any dissemination caveat riding on it.
//
// Three rules, and the third is the one that matters. An unrecognized or absent
// marking returns the highest declared level, because an authoring or mapping
// omission has to withhold a document rather than expose one (srd017 R2.5,
// srd010 R1.3, srd015 R2.5). NOFORN is a caveat beside secret rather than a
// level above it (R2.3): it restricts who may receive a secret document and
// does not make it more classified than secret, and modelling it as a fifth
// level would reorder a set rel11.0 iterates as pairs.
func clearanceFromMarking(
	marking string, mapping clearanceMapping, manifest corpusManifest,
) (string, []string) {
	normalized := strings.ToUpper(strings.TrimSpace(marking))
	caveats := caveatsInMarking(normalized, mapping.Caveats)
	level, declared := mapping.Markings[normalized]
	if !declared || !manifest.declaresClearance(level) {
		return manifest.highestClearance(), caveats
	}
	return level, caveats
}

// caveatsInMarking pulls the declared caveat tokens out of a marking. The
// marking is read for its tokens alone; no part of a document's text reaches
// this function, which is the provenance rule srd010 R1.2 states.
func caveatsInMarking(normalized string, declared []string) []string {
	if normalized == "" || len(declared) == 0 {
		return nil
	}
	tokens := strings.Split(normalized, "//")
	found := make([]string, 0, len(declared))
	for _, caveat := range declared {
		want := strings.ToUpper(strings.TrimSpace(caveat))
		for _, token := range tokens[1:] {
			if strings.TrimSpace(token) == want {
				found = append(found, want)
				break
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	sort.Strings(found)
	return found
}

// tierClearance is the clearance a tier's material carries as a property of the
// tier (srd017 R2.4). The four real tiers are published reporting and are
// unclassified because of where they came from, not because of what they say --
// so the level is declared per tier and no document's text is read to decide
// it. A tier the manifest declares no policy for fails closed at the highest
// level, for the same reason an unrecognized marking does.
func tierClearance(sourceType string, manifest corpusManifest) string {
	declared, ok := manifest.TierClearance[sourceType]
	if !ok || !manifest.declaresClearance(declared) {
		return manifest.highestClearance()
	}
	return declared
}
