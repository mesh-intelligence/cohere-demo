// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The five source adapters (srd017 R1). Each reads one dataset's record shape
// and emits a corpusEnvelope; nothing downstream branches on which one ran.
//
// An adapter converts one record. Aggregating the row-oriented tiers into daily
// digests and applying the window belong to the slice loader, so an adapter
// stays a shape translation and the policy that decides which records survive
// stays in one place.

// Markers the segmenters split on. Each is a property of how its source writes
// documents, established by reading the datasets rather than assumed.
var (
	// A HUMINT report that carries a classification-authority line opens with
	// it, before the first portion-marked paragraph.
	humintAuthorityPrefix = "Classified by:"
	// Portion-marked paragraphs -- "1. (C) SUMMARY: ..." -- are the report's
	// structure, and the SUMMARY and FIELD COMMENT paragraphs are named.
	humintParagraphPattern = regexp.MustCompile(`(?m)^\d+\.\s*\([A-Z/]+\)\s*`)
	humintSummaryMarker    = "SUMMARY:"
	humintCommentMarker    = "FIELD COMMENT:"
	// An ISW assessment ends in a citation tail introduced by a bracketed
	// endnote number followed by a URL. The tail runs about a quarter of the
	// document and is pure citation apparatus. The separator is a non-breaking
	// space as often as an ordinary one.
	iswEndnotePattern = regexp.MustCompile(`\[\d{1,3}\][\s\x{00a0}]*https?://`)
	// The author list and publication time open most assessments, ending in the
	// timezone the tier states its publication time in. The tier used EST and
	// EDT early in the war and ET later, so all three anchor the byline. Roughly
	// a quarter of the assessments carry no byline at all and open on the
	// assessment itself, which is why the segment is optional rather than
	// required.
	iswBylinePattern = regexp.MustCompile(`(?s)^.{0,400}?(?:EST|EDT|ET)[\s\x{00a0}]+`)
	// A publication date stated in an assessment's own title.
	iswTitleDatePattern = regexp.MustCompile(
		`(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d{1,2}),?\s+(\d{4})`)
	// The opening of a citation entry in the tail: a bracketed number either at
	// the start of the text searched or after whitespace. The leading
	// alternation is what keeps a bracketed index inside a link from opening an
	// entry of its own -- Facebook permalinks carry "?__cft__[0]=", which is
	// glued to a word character and matches neither branch. The number is held
	// at or above one for the same reason and as a second filter on it.
	iswEntryStartPattern = regexp.MustCompile(`(^|[\s\x{00a0}])\[([1-9]\d{0,2})\]`)
	// A citation marker as the prose carries it. Unlike an entry start this has
	// no leading condition, because the tier writes markers against the
	// preceding punctuation ("supply issues.[26]") about five times as often as
	// after a space.
	iswMarkerPattern = regexp.MustCompile(`\[(\d{1,3})\]`)
	// The note the tier closes an assessment with, written verbatim in most of
	// them. It is the anchor the prose-to-tail cut prefers, and the segment
	// boundary the note itself is carried at, so the two read one constant
	// rather than two copies that agree until one is edited.
	iswClosingNote = "Note: ISW does not receive any classified material from any" +
		" source, uses only publicly available information, and draws extensively on" +
		" Russian, Ukrainian, and Western reporting and social media as well as" +
		" commercially available satellite imagery and other geospatial data as the" +
		" basis for these reports. References to all sources used are provided in the" +
		" endnotes of each update."
	// The opening of a reference inside an entry. Extraction splits on this
	// and on nothing else: an entry separates its references with semicolons
	// that are sometimes only whitespace, and a defanged URL carries internal
	// spaces (" dot "), so splitting on either separator would cut references
	// in half or glue them together (srd017 R10.1).
	iswReferenceStartPattern = regexp.MustCompile(`https?://`)
	// The Russian foreign-agent disclaimer Telegram channels append.
	telegramDisclaimerPattern = regexp.MustCompile(
		`(?s)(ДАННОЕ СООБЩЕНИЕ|ДАННЫЙ МАТЕРИАЛ|НАСТОЯЩЕЕ СООБЩЕНИЕ).*$`)
)

// humintRecord is one line of data/humint/humint_reports.jsonl.
type humintRecord struct {
	ID              string `json:"id"`
	Date            string `json:"date"`
	Classification  string `json:"classification"`
	SourceID        string `json:"source_id"`
	Reliability     string `json:"reliability"`
	SourceAccess    string `json:"source_access"`
	Collector       string `json:"collector"`
	Title           string `json:"title"`
	RelatedMessages []struct {
		ID   string `json:"id"`
		Src  string `json:"src"`
		Date string `json:"date"`
	} `json:"related_messages"`
	Tags []string `json:"tags"`
	Text string   `json:"text"`
}

// adaptHumintRecord reads one synthetic HUMINT report. This is the only tier
// whose documents declare a classification, so it is the only one whose
// clearance comes from a marking rather than from tier policy.
func adaptHumintRecord(
	line []byte, mapping clearanceMapping, manifest corpusManifest,
) (corpusEnvelope, error) {
	var record humintRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return corpusEnvelope{}, fmt.Errorf("parse humint record: %w", err)
	}
	if strings.TrimSpace(record.ID) == "" {
		return corpusEnvelope{}, fmt.Errorf("humint record declares no id")
	}
	issued, err := normalizedIssuedDate(record.Date)
	if err != nil {
		return corpusEnvelope{}, fmt.Errorf("humint record %s: %w", record.ID, err)
	}
	clearance, caveats := clearanceFromMarking(record.Classification, mapping, manifest)
	derived := make([]corpusDerivedRef, 0, len(record.RelatedMessages))
	for _, related := range record.RelatedMessages {
		if strings.TrimSpace(related.ID) == "" {
			continue
		}
		derived = append(derived, corpusDerivedRef{
			Tier:     sourceTypeOSINTNews,
			ID:       related.ID,
			Relation: derivedRelationWrittenOver,
		})
	}
	envelope := corpusEnvelope{
		DocumentID:  record.ID,
		SourceType:  sourceTypeHumint,
		Clearance:   clearance,
		Caveats:     caveats,
		Issued:      issued,
		Origin:      record.SourceID,
		Reliability: record.Reliability,
		Provenance: corpusProvenance{
			Collector:   record.Collector,
			DerivedFrom: derived,
		},
		Title:     record.Title,
		Synthetic: true,
		RawRef:    corpusRawRef{Store: "humint", ID: record.ID, Path: "humint/humint_reports.jsonl"},
		Segments:  segmentHumintText(record.Text),
	}
	if strings.TrimSpace(envelope.Reliability) == "" {
		// A report whose source carries no rating is reported as unrated rather
		// than left blank, because the envelope contract requires the field and
		// an empty string would read as an adapter that failed to populate it.
		envelope.Reliability = "unrated"
	}
	return envelope, envelope.validate()
}

// segmentHumintText splits a report into its authority line, summary, body
// paragraphs, and field comment. The authority line is carried unsearchable:
// it states who classified the report and under what authority, which belongs
// in the served document and has no business in a retrieval vector
// (srd017 R1.4).
func segmentHumintText(text string) []corpusSegment {
	normalized := strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n")
	if normalized == "" {
		return nil
	}
	segments := make([]corpusSegment, 0, 6)
	body := normalized
	if strings.HasPrefix(normalized, humintAuthorityPrefix) {
		if cut := strings.Index(normalized, "\n"); cut >= 0 {
			segments = append(segments, corpusSegment{
				Role:       segmentRoleAuthority,
				Searchable: false,
				Text:       strings.TrimSpace(normalized[:cut]),
			})
			body = strings.TrimSpace(normalized[cut:])
		}
	}
	for _, paragraph := range splitHumintParagraphs(body) {
		role := segmentRoleBody
		switch {
		case strings.Contains(paragraph, humintSummaryMarker):
			role = segmentRoleSummary
		case strings.Contains(paragraph, humintCommentMarker):
			role = segmentRoleComment
		}
		segments = append(segments, corpusSegment{
			Role: role, Searchable: true, Text: paragraph,
		})
	}
	return segments
}

// splitHumintParagraphs cuts on the portion-marked paragraph numbering, keeping
// each marker with the paragraph it introduces. A report that does not use the
// numbering is returned whole rather than dropped.
func splitHumintParagraphs(body string) []string {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	starts := humintParagraphPattern.FindAllStringIndex(body, -1)
	if len(starts) == 0 {
		return []string{body}
	}
	paragraphs := make([]string, 0, len(starts))
	for index, start := range starts {
		end := len(body)
		if index+1 < len(starts) {
			end = starts[index+1][0]
		}
		if paragraph := strings.TrimSpace(body[start[0]:end]); paragraph != "" {
			paragraphs = append(paragraphs, paragraph)
		}
	}
	return paragraphs
}

// iswRecord is one line of data/isw/isw_dailies.jsonl.
type iswRecord struct {
	Date  string `json:"date"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Text  string `json:"text"`
}

// adaptISWRecord reads one ISW daily assessment. The tier is published
// reporting, so its clearance is tier policy and no assessment's text is read
// to decide it (srd017 R2.4).
func adaptISWRecord(
	line []byte, manifest corpusManifest,
) (corpusEnvelope, error) {
	var record iswRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return corpusEnvelope{}, fmt.Errorf("parse isw record: %w", err)
	}
	if strings.TrimSpace(record.URL) == "" {
		return corpusEnvelope{}, fmt.Errorf("isw record of %s declares no url,"+
			" which is the identifier the tier is keyed on", record.Date)
	}
	issued, err := normalizedIssuedDate(record.Date)
	if err != nil {
		return corpusEnvelope{}, fmt.Errorf("isw record %s: %w", record.URL, err)
	}
	if err := checkISWTitleDate(record.Title, issued); err != nil {
		return corpusEnvelope{}, fmt.Errorf("isw record %s: %w", record.URL, err)
	}
	envelope := corpusEnvelope{
		DocumentID:  "isw-" + issued,
		SourceType:  sourceTypeOSINTAssessment,
		Clearance:   tierClearance(sourceTypeOSINTAssessment, manifest),
		Issued:      issued,
		Origin:      "Institute for the Study of War",
		Reliability: "published_assessment",
		Provenance:  corpusProvenance{Collector: "Institute for the Study of War"},
		Title:       record.Title,
		RawRef:      corpusRawRef{Store: "isw", ID: record.URL, Path: "isw/isw_dailies.jsonl"},
		Segments:    segmentISWText(record.Text),
	}
	for _, segment := range envelope.Segments {
		if segment.Role == segmentRoleEndnotes {
			envelope.References = parseISWReferences(segment.Text)
		}
	}
	return envelope, envelope.validate()
}

// checkISWTitleDate holds a record's date field to the publication date its own
// title states. The dataset carries one assessment whose title names a year
// three years from its date field, and the window filter selects on that field
// -- so a disagreement is a record that would be admitted or dropped by
// accident. R1.5 makes that a build failure rather than a default.
func checkISWTitleDate(title, issued string) error {
	match := iswTitleDatePattern.FindStringSubmatch(title)
	if match == nil {
		return nil
	}
	stated, err := time.Parse("January 2 2006",
		fmt.Sprintf("%s %s %s", match[1], match[2], match[3]))
	if err != nil {
		return nil
	}
	carried, err := time.Parse(corpusEnvelopeDateLayout, issued)
	if err != nil {
		return err
	}
	if stated.Year() != carried.Year() {
		return fmt.Errorf("title states a publication date of %s while the record"+
			" carries %s; the issued date is the one the source itself carries and"+
			" is not defaulted (srd017 R1.5)",
			stated.Format(corpusEnvelopeDateLayout), issued)
	}
	return nil
}

// segmentISWText splits an assessment into its byline boilerplate, its body,
// its closing note, and its endnote tail. Three segments are carried
// unsearchable for the same reason and to different effect (srd017 R1.4).
//
// The closing note is the sourcing statement the tier ends nearly every full
// assessment with, written verbatim each time. Chunked, it lands in the last
// chunk of every assessment that carries it, so those chunks resemble each
// other more than they resemble the assessments they were cut from -- a
// retrieval defect rather than noise, and the reason R1.8 carries fixed
// editorial boilerplate out of the searchable body. The endnote tail is roughly a
// quarter of the tier's text and is entirely URLs, so embedding it would spend
// a quarter of the tier's budget on citation apparatus. The byline is the
// author list and interactive-map boilerplate, identical across every
// assessment, so embedding it would put the same paragraph into thousands of
// chunks. Both stay readable in the served document, which is where a citation
// needs them.
func segmentISWText(text string) []corpusSegment {
	normalized := strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n")
	if normalized == "" {
		return nil
	}
	segments := make([]corpusSegment, 0, 3)
	body := normalized
	if byline := iswBylinePattern.FindString(body); byline != "" && len(byline) < len(body) {
		segments = append(segments, corpusSegment{
			Role: segmentRoleByline, Searchable: false, Text: strings.TrimSpace(byline),
		})
		body = strings.TrimSpace(body[len(byline):])
	}
	endnotes := ""
	if cut := iswTailStart(body); cut >= 0 {
		endnotes = strings.TrimSpace(body[cut:])
		body = strings.TrimSpace(body[:cut])
	}
	// The closing note is split off after the tail, not before it, because the
	// tail cut anchors on the note and would lose its anchor if the note were
	// already gone.
	closing := ""
	if strings.HasSuffix(body, iswClosingNote) {
		closing = iswClosingNote
		body = strings.TrimSpace(strings.TrimSuffix(body, iswClosingNote))
	}
	if body != "" {
		segments = append(segments, corpusSegment{
			Role: segmentRoleBody, Searchable: true, Text: body,
		})
	}
	if closing != "" {
		segments = append(segments, corpusSegment{
			Role: segmentRoleDisclaimer, Searchable: false, Text: closing,
		})
	}
	if endnotes != "" {
		segments = append(segments, corpusSegment{
			Role: segmentRoleEndnotes, Searchable: false, Text: endnotes,
		})
	}
	return segments
}

// iswTailStart reports where an assessment's citation tail begins, or -1 where
// it carries none.
//
// The tail is preferentially found by the note the tier closes on: the tail is
// what follows it, and the first entry after it opens the tail whatever that
// entry's link looks like. Anchoring on a well-formed link instead is what
// fails, and it fails silently -- the tier writes its links by hand and
// mistypes them, and an assessment whose first entry reads "ttps://" has that
// entry admitted into the body and embedded as though it were assessment
// (srd017 R1.7). The tail is short by exactly one entry, which no length or
// ratio check separates from an assessment that simply cites less.
//
// An assessment carrying no closing note falls back to the link-anchored
// pattern, which is what the abbreviated reports need. Where the note is
// present and nothing follows it, the assessment ends there and has no tail:
// the fallback is not tried, because any link-anchored match would then lie
// before the note and cut the assessment itself in half.
func iswTailStart(body string) int {
	if index := strings.Index(body, iswClosingNote); index >= 0 {
		return iswEntryStart(body, index+len(iswClosingNote))
	}
	if match := iswEndnotePattern.FindStringIndex(body); match != nil {
		return match[0]
	}
	return -1
}

// iswEntryStart returns the offset of the first citation entry at or after
// from, or -1. Searching a slice rather than the whole string is what lets the
// pattern's leading `^` stand for "opens the region searched", so an entry
// written hard against the closing note's full stop is found alongside one
// separated from it by a space.
func iswEntryStart(body string, from int) int {
	if from < 0 || from > len(body) {
		return -1
	}
	match := iswEntryStartPattern.FindStringSubmatchIndex(body[from:])
	if match == nil {
		return -1
	}
	// Group one is the leading whitespace, empty where the entry opens the
	// region, so the bracket sits where that group ends.
	return from + match[3]
}

// reconcileISWMarkers holds an assessment's prose markers to the entries its
// tail carries. Every marker naming an entry the tail does not hold is
// reported together, because they share one cause and listing the first would
// hide it (srd017 R10.5).
//
// This is the check that catches a cut in the wrong place. The defect it was
// written for leaves the tail short by one entry while the prose still cites
// it, which is invisible to a length comparison and visible here as a marker
// resolving to nothing.
func reconcileISWMarkers(envelope corpusEnvelope) error {
	entries := make(map[string]bool)
	for _, segment := range envelope.Segments {
		if segment.Role != segmentRoleEndnotes {
			continue
		}
		for _, match := range iswEntryStartPattern.FindAllStringSubmatch(segment.Text, -1) {
			entries[match[2]] = true
		}
	}
	unresolved := make([]string, 0)
	seen := make(map[string]bool)
	for _, segment := range envelope.Segments {
		if !segment.Searchable {
			continue
		}
		for _, match := range iswMarkerPattern.FindAllStringSubmatch(segment.Text, -1) {
			if entries[match[1]] || seen[match[1]] {
				continue
			}
			seen[match[1]] = true
			unresolved = append(unresolved, "["+match[1]+"]")
		}
	}
	if len(unresolved) == 0 {
		return nil
	}
	return fmt.Errorf("isw assessment %s cites %s, which its endnote tail does not"+
		" carry; the tail is cut at the first entry following the closing note, so a"+
		" marker resolving to nothing means the cut fell in the wrong place and"+
		" citation apparatus is being embedded as assessment (srd017 R10.5)",
		envelope.DocumentID, strings.Join(unresolved, ", "))
}

// parseISWReferences reads an assessment's citation tail into numbered
// entries (srd017 R10.1). An entry begins where iswEntryStartPattern matches
// and runs to the next entry's bracket, so its text is what the source wrote
// between the two -- including a plain-text citation carrying no link at all,
// which is kept with an empty reference list rather than dropped (R10.3).
//
// References are split on the scheme alone. The tier separates them with
// semicolons that are sometimes only whitespace, and defanging writes spaces
// inside a single URL (" dot "), so neither punctuation nor whitespace
// separates references reliably; the scheme does. What each split yields is
// stored exactly as written, defanging and all.
func parseISWReferences(tail string) []corpusReference {
	starts := iswEntryStartPattern.FindAllStringSubmatchIndex(tail, -1)
	if len(starts) == 0 {
		return nil
	}
	references := make([]corpusReference, 0, len(starts))
	for i, start := range starts {
		number, err := strconv.Atoi(tail[start[4]:start[5]])
		if err != nil {
			continue
		}
		from := start[5] + 1
		to := len(tail)
		if i+1 < len(starts) {
			// The next entry's bracket, not its leading whitespace, so the
			// whitespace between two entries belongs to neither.
			to = starts[i+1][3]
		}
		text := strings.TrimSpace(tail[from:to])
		references = append(references, corpusReference{
			Number: number,
			Text:   text,
			URLs:   extractISWReferenceURLs(text),
		})
	}
	return references
}

// extractISWReferenceURLs cuts an entry's text at each scheme and returns the
// runs between them, trimmed of the punctuation the tier writes between
// references. A run keeps its internal spaces: those are the defanging, and
// removing them would refang a link the publisher chose to obscure.
func extractISWReferenceURLs(text string) []string {
	starts := iswReferenceStartPattern.FindAllStringIndex(text, -1)
	if len(starts) == 0 {
		return nil
	}
	urls := make([]string, 0, len(starts))
	for i, start := range starts {
		end := len(text)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		url := strings.TrimSpace(text[start[0]:end])
		url = strings.TrimRight(url, ";,\u00a0 \t")
		if url != "" {
			urls = append(urls, url)
		}
	}
	return urls
}

// adaptTelegramRow reads one row of data/ukraine_news/news.csv. The row arrives
// as the column map the CSV reader produced, because the loader reads the file
// once and the adapter converts what it yields.
func adaptTelegramRow(
	row map[string]string, manifest corpusManifest,
) (corpusEnvelope, error) {
	id := strings.TrimSpace(row["id"])
	if id == "" {
		return corpusEnvelope{}, fmt.Errorf("telegram row declares no id")
	}
	issued, err := normalizedIssuedDate(row["date"])
	if err != nil {
		return corpusEnvelope{}, fmt.Errorf("telegram row %s: %w", id, err)
	}
	channel := strings.TrimSpace(row["group_name"])
	if channel == "" {
		channel = strings.TrimSpace(row["group_id"])
	}
	// The identifier carries the channel as well as the message number. Each
	// channel numbers its own messages, so the number alone repeats across
	// channels -- a single month of the window holds over a thousand such
	// collisions, and keying on it silently overwrote one channel's message
	// with another's.
	envelope := corpusEnvelope{
		DocumentID:  telegramDocumentID(row["group_id"], id),
		SourceType:  sourceTypeOSINTNews,
		Clearance:   tierClearance(sourceTypeOSINTNews, manifest),
		Issued:      issued,
		Origin:      channel,
		Reliability: "unrated_open_source",
		Provenance:  corpusProvenance{Collector: channel},
		Title:       telegramTitle(row["text"], channel, issued),
		RawRef:      corpusRawRef{Store: "ukraine_news", ID: id, Path: "ukraine_news/news.csv"},
		Segments:    segmentTelegramText(row["text"]),
	}
	return envelope, envelope.validate()
}

// telegramDocumentID names a message by its channel and number. A message
// carrying no channel falls back to the number alone rather than being dropped,
// and the uniqueness check the slice loader runs is what catches it if that
// ever collides.
func telegramDocumentID(channel, id string) string {
	channel = strings.TrimSpace(channel)
	if channel == "" {
		return "tg-" + id
	}
	return "tg-" + channel + "-" + id
}

// telegramTitle gives a message a title, since the source carries none and the
// envelope contract requires one. The first line, trimmed, is what a reader
// would use to recognize the message.
func telegramTitle(text, channel, issued string) string {
	first := strings.TrimSpace(text)
	if cut := strings.IndexAny(first, ".\n"); cut > 0 {
		first = first[:cut]
	}
	first = strings.TrimSpace(first)
	if len([]rune(first)) > 90 {
		first = string([]rune(first)[:90])
	}
	if first == "" {
		return fmt.Sprintf("%s message of %s", channel, issued)
	}
	return first
}

// segmentTelegramText separates the foreign-agent disclaimer Russian channels
// append from the message itself. The disclaimer is boilerplate a state
// requirement attaches to the message, repeated verbatim across every message
// from that channel, so it is carried for display and kept out of the vector
// (srd017 R1.4).
func segmentTelegramText(text string) []corpusSegment {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return nil
	}
	segments := make([]corpusSegment, 0, 2)
	disclaimer := ""
	if match := telegramDisclaimerPattern.FindStringIndex(normalized); match != nil {
		disclaimer = strings.TrimSpace(normalized[match[0]:])
		normalized = strings.TrimSpace(normalized[:match[0]])
	}
	if normalized != "" {
		segments = append(segments, corpusSegment{
			Role: segmentRoleBody, Searchable: true, Text: normalized,
		})
	}
	if disclaimer != "" {
		segments = append(segments, corpusSegment{
			Role: segmentRoleDisclaimer, Searchable: false, Text: disclaimer,
		})
	}
	return segments
}

// adaptMissileRow reads one row of the missile and drone attack dataset into a
// single-row envelope. The loader aggregates the rows of one day into a digest;
// this adapter's job is the shape, and the row it converted is named in the
// provenance so the digest that absorbs it inherits a resolvable reference
// (srd017 R5.4).
func adaptMissileRow(
	row map[string]string, index int, manifest corpusManifest,
) (corpusEnvelope, error) {
	issued, err := normalizedIssuedDate(row["time_start"])
	if err != nil {
		return corpusEnvelope{}, fmt.Errorf("missile row %d: %w", index, err)
	}
	id := fmt.Sprintf("missile-%s-%04d", issued, index)
	model := strings.TrimSpace(row["model"])
	if model == "" {
		model = "unspecified munition"
	}
	envelope := corpusEnvelope{
		DocumentID:  id,
		SourceType:  sourceTypeSIGINTEvent,
		Clearance:   tierClearance(sourceTypeSIGINTEvent, manifest),
		Issued:      issued,
		Origin:      "Ukrainian Air Force operational reporting",
		Reliability: "official_release",
		Provenance: corpusProvenance{
			Collector: "Ukrainian Air Force",
			DerivedFrom: []corpusDerivedRef{{
				Tier:     sourceTypeSIGINTEvent,
				ID:       fmt.Sprintf("missile_attacks_daily.csv:%d", index),
				Relation: derivedRelationAggregates,
			}},
		},
		Title:    fmt.Sprintf("%s strike activity, %s", model, issued),
		RawRef:   corpusRawRef{Store: "missile_attacks", ID: id, Path: "missile_attacks/missile_attacks_daily.csv"},
		Segments: []corpusSegment{{Role: segmentRoleBody, Searchable: true, Text: missileRowText(row, model, issued)}},
	}
	return envelope, envelope.validate()
}

// missileRowText renders one attack row as prose. The row is columns, and a
// retrieval corpus holds documents, so the adapter states what the row records
// rather than serializing it.
func missileRowText(row map[string]string, model, issued string) string {
	var text strings.Builder
	fmt.Fprintf(&text, "On %s Ukrainian air defence reported %s activity", issued, model)
	if place := strings.TrimSpace(row["launch_place"]); place != "" {
		fmt.Fprintf(&text, " launched from %s", place)
	}
	if target := strings.TrimSpace(row["target"]); target != "" {
		fmt.Fprintf(&text, " against %s", target)
	}
	text.WriteString(".")
	launched := strings.TrimSpace(row["launched"])
	destroyed := strings.TrimSpace(row["destroyed"])
	if launched != "" || destroyed != "" {
		fmt.Fprintf(&text, " Reported launched %s, reported destroyed %s.",
			blankAsUnreported(launched), blankAsUnreported(destroyed))
	}
	if region := strings.TrimSpace(row["affected region"]); region != "" {
		fmt.Fprintf(&text, " Affected region %s.", region)
	}
	return text.String()
}

// lossesDay is one day of the loss series, holding the row each of the two
// files carries for that date. The two are read together because the tier's
// unit is the day rather than the file: srd017 R3.4 states one daily delta, and
// two envelopes per day would double the tier's document count against what the
// use case measures.
type lossesDay struct {
	Date      string
	Personnel map[string]string
	Equipment map[string]string
}

// adaptLossesDelta renders one day's change against the day before it. The
// source files are cumulative running totals, so the day's own reporting is the
// difference between consecutive rows -- a document stating the cumulative
// total answers "how many since February 2022", which is a question about the
// war rather than about the day the analyst asked for.
//
// The previous day is the row before this one in date order, which for the
// first day of the window is a row outside it. That row is read for its figures
// alone and is not itself converted, so the window bounds what the corpus holds
// without making the first day's delta unavailable.
func adaptLossesDelta(
	today, previous lossesDay, manifest corpusManifest,
) (corpusEnvelope, error) {
	issued, err := normalizedIssuedDate(today.Date)
	if err != nil {
		return corpusEnvelope{}, fmt.Errorf("losses day: %w", err)
	}
	id := "losses-" + issued
	derived := make([]corpusDerivedRef, 0, 2)
	for _, series := range []string{"personnel", "equipment"} {
		derived = append(derived, corpusDerivedRef{
			Tier:     sourceTypeSIGINTSeries,
			ID:       fmt.Sprintf("russia_losses_%s.csv:%s", series, issued),
			Relation: derivedRelationAggregates,
		})
	}
	envelope := corpusEnvelope{
		DocumentID:  id,
		SourceType:  sourceTypeSIGINTSeries,
		Clearance:   tierClearance(sourceTypeSIGINTSeries, manifest),
		Issued:      issued,
		Origin:      "Ukrainian General Staff daily figures",
		Reliability: "official_release",
		Provenance: corpusProvenance{
			Collector:   "Ukrainian General Staff",
			DerivedFrom: derived,
		},
		Title:    fmt.Sprintf("Reported Russian losses reported on %s", issued),
		RawRef:   corpusRawRef{Store: "war_losses", ID: id, Path: "war_losses/russia_losses_personnel.csv"},
		Segments: []corpusSegment{{Role: segmentRoleBody, Searchable: true, Text: lossesDeltaText(today, previous, issued)}},
	}
	return envelope, envelope.validate()
}

// lossesDeltaText states the day's reported change and the running total beside
// it, so a retrieval hit answers both what was reported that day and where the
// total stood. A column the source left blank or marked NaN is passed over
// rather than reported as zero, because an unreported figure and a figure of
// zero are different claims.
func lossesDeltaText(today, previous lossesDay, issued string) string {
	var text strings.Builder
	fmt.Fprintf(&text,
		"Ukrainian General Staff figures reported on %s record the following"+
			" Russian losses for the preceding day, with the running total in"+
			" parentheses.", issued)
	counted := 0
	for _, series := range []struct {
		name    string
		today   map[string]string
		earlier map[string]string
	}{
		{"personnel", today.Personnel, previous.Personnel},
		{"equipment", today.Equipment, previous.Equipment},
	} {
		for _, column := range lossesReportedColumns(series.name) {
			total, ok := lossesFigure(series.today, column)
			if !ok {
				continue
			}
			if earlier, had := lossesFigure(series.earlier, column); had {
				fmt.Fprintf(&text, " %s %+d (%d).", capitalizeColumn(column), total-earlier, total)
			} else {
				fmt.Fprintf(&text, " %s running total %d.", capitalizeColumn(column), total)
			}
			counted++
		}
	}
	if counted == 0 {
		text.WriteString(" The source reports no figures for this day.")
	}
	return text.String()
}

// lossesFigure reads one column as a whole number. The files carry blanks and
// NaN for a figure the source did not report that day, and both mean absent
// rather than zero.
func lossesFigure(row map[string]string, column string) (int, bool) {
	value := strings.TrimSpace(row[column])
	if value == "" || strings.EqualFold(value, "nan") {
		return 0, false
	}
	if cut := strings.Index(value, "."); cut >= 0 {
		value = value[:cut]
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return number, true
}

// lossesReportedColumns names the columns each loss series reports, in the
// order the digest states them. The equipment series carries twenty columns and
// the digest names the ones a tasking asks about rather than all of them.
func lossesReportedColumns(series string) []string {
	if series == "personnel" {
		return []string{"personnel", "POW"}
	}
	return []string{
		"tank", "APC", "field artillery", "MRL", "drone",
		"aircraft", "helicopter", "naval ship", "cruise missiles",
	}
}

// capitalizeColumn renders a dataset column name at the head of a sentence.
// Column names are lower-case except the acronyms the source capitalizes, and
// those are left as the source wrote them.
func capitalizeColumn(column string) string {
	if column == "" || strings.ToUpper(column) == column {
		return column
	}
	return strings.ToUpper(column[:1]) + column[1:]
}

func blankAsUnreported(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unreported"
	}
	return strings.TrimSuffix(strings.TrimSpace(value), ".0")
}

// normalizedIssuedDate reads a source's own date into the envelope's layout.
// The datasets state dates three ways -- a bare date, a date and time, and a
// timestamp -- and none of them may be defaulted: a record whose date cannot be
// established fails rather than being placed inside or outside the window by
// accident (srd017 R1.5).
func normalizedIssuedDate(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("record carries no date; the issued date is the one" +
			" the source itself carries and is not defaulted (srd017 R1.5)")
	}
	layouts := []string{
		corpusEnvelopeDateLayout,
		"2006-01-02 15:04",
		"2006-01-02 15:04:05",
		time.RFC3339,
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Format(corpusEnvelopeDateLayout), nil
		}
	}
	return "", fmt.Errorf("date %q matches none of the layouts the sources use"+
		" %v; the issued date is not defaulted (srd017 R1.5)", value, layouts)
}
