// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The windowed slice loader (srd017 R3). It runs the five adapters over the
// source datasets, keeps what falls inside the declared window, aggregates the
// two row-oriented tiers into one envelope per day, and down-samples the
// Telegram tier to a declared cap.
//
// What this stage is not is a cost control. Conversion and chunking cover the
// window in full whatever the embedding budget allows (R3.3), because a
// document absent from the store cannot be cited, opened, or shown in a
// provenance walk even when its neighbours can. The budget bounds which chunks
// carry vectors and nothing else.

// The declared window (srd017 R3.1). Twelve months, both bounds inclusive,
// chosen because it is the longest span over which all five tiers carry
// material: the Telegram corpus ends in July 2023 and the attack dataset does
// not begin until late September 2022.
// The outer bounds the datasets overlap over, and the window the demonstration
// actually converts.
//
// The declared window is one month, not the twelve the tiers span. Every
// converted chunk costs a record in the store whether or not it carries a
// vector, and the store is a versioned repository artifact a clone seeds from
// without a build (srd015 R6.1). Twelve months at three profiles measured 235
// MB across 78,662 records against a 5.3 MB predecessor; December 2022, the
// densest month of the infrastructure-strike campaign, is 42 MB (srd017 R3.1,
// R3.3).
const (
	sliceOuterStart = "2022-07-01"
	sliceOuterEnd   = "2023-06-30"

	sliceWindowStart = "2022-12-01"
	sliceWindowEnd   = "2022-12-31"
)

// Dataset paths, relative to the source root. The datasets live outside the
// repository, so the root is supplied by the caller rather than derived from
// the checkout.
const (
	humintDatasetPath   = "humint/humint_reports.jsonl"
	iswDatasetPath      = "isw/isw_dailies.jsonl"
	telegramDatasetPath = "ukraine_news/news.csv"
	missileDatasetPath  = "missile_attacks/missile_attacks_daily.csv"
	lossesPersonnelPath = "war_losses/russia_losses_personnel.csv"
	lossesEquipmentPath = "war_losses/russia_losses_equipment.csv"
)

// corpusSliceParameters is the window and the sampling policy, recorded with
// the store as build configuration rather than fixed as normative values, which
// is the treatment srd015 R4.5 gives chunking parameters and R4.5 of srd017
// extends to the profiles.
type corpusSliceParameters struct {
	WindowStart string `yaml:"window_start"`
	WindowEnd   string `yaml:"window_end"`
	// Tiers converted across the outer bounds rather than the declared window.
	// A tier small enough that converting all of it costs the store little
	// belongs here when the window would take something the demonstration
	// needs.
	FullWindowTiers          []string `yaml:"full_window_tiers,omitempty"`
	TelegramPerChannelPerDay int      `yaml:"telegram_per_channel_per_day"`
	TelegramCap              int      `yaml:"telegram_cap"`
}

// convertsFullWindow reports whether a tier is converted across the outer
// bounds rather than the declared window.
func (p corpusSliceParameters) convertsFullWindow(tier string) bool {
	for _, declared := range p.FullWindowTiers {
		if declared == tier {
			return true
		}
	}
	return false
}

// windowFor returns the bounds a tier is converted over.
func (p corpusSliceParameters) windowFor(tier string) (string, string) {
	if p.convertsFullWindow(tier) {
		return sliceOuterStart, sliceOuterEnd
	}
	return p.WindowStart, p.WindowEnd
}

// inWindowForTier reports whether a date falls in the bounds a tier is
// converted over.
func (p corpusSliceParameters) inWindowForTier(tier, issued string) bool {
	start, end := p.windowFor(tier)
	return issued >= start && issued <= end
}

func defaultCorpusSliceParameters() corpusSliceParameters {
	return corpusSliceParameters{
		WindowStart: sliceWindowStart,
		WindowEnd:   sliceWindowEnd,
		// One message per channel per day. Twelve channels report every day of
		// the window, so the tier still carries opposing accounts of the same
		// night, which is what it is in the corpus for; a second message from
		// the same channel on the same day mostly repeats the first and the
		// tier is the largest contributor to the store after the assessments.
		// The cap is a second bound rather than a restatement of the first: it
		// holds whatever a change to the per-channel rule would otherwise let
		// through.
		// The synthetic tier is converted across the outer bounds. It holds 27
		// reports totalling well under a megabyte, and it is the only tier
		// carrying more than one clearance level: December alone gives four
		// reports at two of its four markings, which leaves the rel11.0 level
		// pairs thin for nothing saved.
		FullWindowTiers:          []string{sourceTypeHumint},
		TelegramPerChannelPerDay: 1,
		TelegramCap:              9000,
	}
}

// inWindow reports whether a date falls inside the declared window. Both bounds
// are inclusive (R3.1), and the comparison is on the ISO date string, whose
// lexical and chronological orders agree.
func (p corpusSliceParameters) inWindow(issued string) bool {
	return issued >= p.WindowStart && issued <= p.WindowEnd
}

// corpusSliceReport counts what the loader converted per tier, so a build says
// what it read rather than leaving the counts to be recovered from the store.
type corpusSliceReport struct {
	Humint            int
	OSINTAssessment   int
	OSINTNews         int
	SIGINTEvent       int
	SIGINTSeries      int
	TelegramCandidate int
	TelegramSampled   int
}

func (r corpusSliceReport) total() int {
	return r.Humint + r.OSINTAssessment + r.OSINTNews + r.SIGINTEvent + r.SIGINTSeries
}

// loadCorpusSlice reads every tier and returns the converted window in a
// deterministic order. Two runs over unchanged inputs return byte-identical
// lists (R3.5), which is what lets a rebuild be idempotent rather than merely
// equivalent.
func loadCorpusSlice(
	root string, params corpusSliceParameters, mapping clearanceMapping, manifest corpusManifest,
) ([]corpusEnvelope, corpusSliceReport, error) {
	var report corpusSliceReport
	envelopes := make([]corpusEnvelope, 0, 1024)

	humint, err := loadHumintSlice(root, params, mapping, manifest)
	if err != nil {
		return nil, report, err
	}
	report.Humint = len(humint)
	envelopes = append(envelopes, humint...)

	assessments, err := loadISWSlice(root, params, manifest)
	if err != nil {
		return nil, report, err
	}
	report.OSINTAssessment = len(assessments)
	envelopes = append(envelopes, assessments...)

	news, candidates, err := loadTelegramSlice(root, params, manifest)
	if err != nil {
		return nil, report, err
	}
	report.OSINTNews = len(news)
	report.TelegramCandidate = candidates
	report.TelegramSampled = len(news)
	envelopes = append(envelopes, news...)

	strikes, err := loadMissileSlice(root, params, manifest)
	if err != nil {
		return nil, report, err
	}
	report.SIGINTEvent = len(strikes)
	envelopes = append(envelopes, strikes...)

	losses, err := loadLossesSlice(root, params, manifest)
	if err != nil {
		return nil, report, err
	}
	report.SIGINTSeries = len(losses)
	envelopes = append(envelopes, losses...)

	sortCorpusEnvelopes(envelopes)
	if err := assertUniqueDocumentIDs(envelopes); err != nil {
		return nil, report, err
	}
	return envelopes, report, nil
}

// assertUniqueDocumentIDs fails a build whose slice carries two documents under
// one identifier.
//
// The store keys a document's directory on the identifier, so a collision is
// not a duplicate but a loss: the second document overwrites the first, its
// chunks land beside the wrong envelope, and the store ends up holding fewer
// documents than the slice reported with no error anywhere. The Telegram tier
// brought exactly this -- each channel numbers its own messages, so the message
// number repeats across channels -- and the symptom was a document count that
// quietly disagreed with the count the loader logged.
func assertUniqueDocumentIDs(envelopes []corpusEnvelope) error {
	seen := make(map[string]corpusEnvelope, len(envelopes))
	for _, envelope := range envelopes {
		previous, collided := seen[envelope.DocumentID]
		if collided {
			return fmt.Errorf("two documents share the identifier %q: %s %s from %s"+
				" and %s %s from %s; the store keys a document's directory on the"+
				" identifier, so a collision loses one of them (srd017 R7.1)",
				envelope.DocumentID,
				previous.SourceType, previous.Issued, previous.RawRef.ID,
				envelope.SourceType, envelope.Issued, envelope.RawRef.ID)
		}
		seen[envelope.DocumentID] = envelope
	}
	return nil
}

// sortCorpusEnvelopes puts the slice in one total order. Sorting on the tier,
// the date, and the identifier together is what makes the order total: two
// documents of one tier on one day are separated by an identifier no two
// documents share.
func sortCorpusEnvelopes(envelopes []corpusEnvelope) {
	sort.SliceStable(envelopes, func(i, j int) bool {
		if envelopes[i].SourceType != envelopes[j].SourceType {
			return envelopes[i].SourceType < envelopes[j].SourceType
		}
		if envelopes[i].Issued != envelopes[j].Issued {
			return envelopes[i].Issued < envelopes[j].Issued
		}
		return envelopes[i].DocumentID < envelopes[j].DocumentID
	})
}

func loadHumintSlice(
	root string, params corpusSliceParameters, mapping clearanceMapping, manifest corpusManifest,
) ([]corpusEnvelope, error) {
	lines, err := readDatasetLines(filepath.Join(root, filepath.FromSlash(humintDatasetPath)))
	if err != nil {
		return nil, err
	}
	envelopes := make([]corpusEnvelope, 0, len(lines))
	for _, line := range lines {
		envelope, err := adaptHumintRecord(line, mapping, manifest)
		if err != nil {
			return nil, err
		}
		if params.inWindowForTier(sourceTypeHumint, envelope.Issued) {
			envelopes = append(envelopes, envelope)
		}
	}
	return envelopes, nil
}

func loadISWSlice(
	root string, params corpusSliceParameters, manifest corpusManifest,
) ([]corpusEnvelope, error) {
	lines, err := readDatasetLines(filepath.Join(root, filepath.FromSlash(iswDatasetPath)))
	if err != nil {
		return nil, err
	}
	envelopes := make([]corpusEnvelope, 0, len(lines))
	for _, line := range lines {
		envelope, err := adaptISWRecord(line, manifest)
		if err != nil {
			return nil, err
		}
		if !params.inWindow(envelope.Issued) {
			continue
		}
		// Reconciliation runs on the converted document rather than inside the
		// adapter, which reads every record the dataset holds across four years
		// to find the ones the window admits. A defect in an assessment nothing
		// converts is not this build's to refuse.
		if err := reconcileISWMarkers(envelope); err != nil {
			return nil, err
		}
		envelopes = append(envelopes, envelope)
	}
	return envelopes, nil
}

// loadTelegramSlice down-samples the news tier. The window holds 224,800
// messages across twelve channels, which is more than the corpus needs and more
// than a reviewer can read, so the tier is sampled rather than truncated: taking
// the first N of the file would weight the corpus toward whichever channel
// happens to sort first.
//
// The rule keeps the longest messages of each channel on each day, which favours
// reporting over one-line headlines, and breaks ties on the message identifier
// so the selection is decided by the data rather than by map iteration order
// (R3.5).
func loadTelegramSlice(
	root string, params corpusSliceParameters, manifest corpusManifest,
) ([]corpusEnvelope, int, error) {
	rows, err := readDatasetRows(filepath.Join(root, filepath.FromSlash(telegramDatasetPath)))
	if err != nil {
		return nil, 0, err
	}
	type bucket struct {
		day     string
		channel string
	}
	grouped := make(map[bucket][]map[string]string)
	candidates := 0
	for _, row := range rows {
		day := strings.TrimSpace(row["date"])
		if len(day) > 10 {
			day = day[:10]
		}
		if !params.inWindow(day) {
			continue
		}
		candidates++
		key := bucket{day: day, channel: strings.TrimSpace(row["group_id"])}
		grouped[key] = append(grouped[key], row)
	}

	keys := make([]bucket, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].day != keys[j].day {
			return keys[i].day < keys[j].day
		}
		return keys[i].channel < keys[j].channel
	})

	envelopes := make([]corpusEnvelope, 0, params.TelegramCap)
	for _, key := range keys {
		selected := grouped[key]
		sort.SliceStable(selected, func(i, j int) bool {
			left, right := []rune(selected[i]["text"]), []rune(selected[j]["text"])
			if len(left) != len(right) {
				return len(left) > len(right)
			}
			return selected[i]["id"] < selected[j]["id"]
		})
		if params.TelegramPerChannelPerDay > 0 && len(selected) > params.TelegramPerChannelPerDay {
			selected = selected[:params.TelegramPerChannelPerDay]
		}
		for _, row := range selected {
			if params.TelegramCap > 0 && len(envelopes) >= params.TelegramCap {
				return envelopes, candidates, nil
			}
			envelope, err := adaptTelegramRow(row, manifest)
			if err != nil {
				return nil, candidates, err
			}
			envelopes = append(envelopes, envelope)
		}
	}
	return envelopes, candidates, nil
}

// loadMissileSlice folds the attack rows of one day into one digest. A day
// carries several rows because a night's activity is reported per munition
// type, and a reader asking what was launched on a date wants the night rather
// than one of its rows (R3.4).
func loadMissileSlice(
	root string, params corpusSliceParameters, manifest corpusManifest,
) ([]corpusEnvelope, error) {
	rows, err := readDatasetRows(filepath.Join(root, filepath.FromSlash(missileDatasetPath)))
	if err != nil {
		return nil, err
	}
	byDay := make(map[string][]corpusEnvelope)
	for index, row := range rows {
		issued, err := normalizedIssuedDate(row["time_start"])
		if err != nil {
			return nil, fmt.Errorf("missile row %d: %w", index, err)
		}
		if !params.inWindow(issued) {
			continue
		}
		envelope, err := adaptMissileRow(row, index, manifest)
		if err != nil {
			return nil, err
		}
		byDay[issued] = append(byDay[issued], envelope)
	}

	days := make([]string, 0, len(byDay))
	for day := range byDay {
		days = append(days, day)
	}
	sort.Strings(days)

	digests := make([]corpusEnvelope, 0, len(days))
	for _, day := range days {
		digest, err := missileDailyDigest(day, byDay[day], manifest)
		if err != nil {
			return nil, err
		}
		digests = append(digests, digest)
	}
	return digests, nil
}

// missileDailyDigest combines one day's rows into a single envelope, carrying
// every row it absorbed in the provenance so the digest resolves back to the
// rows a reader would otherwise have to find themselves (R5.4).
func missileDailyDigest(
	day string, rows []corpusEnvelope, manifest corpusManifest,
) (corpusEnvelope, error) {
	if len(rows) == 0 {
		return corpusEnvelope{}, fmt.Errorf("missile digest for %s aggregates no rows", day)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].DocumentID < rows[j].DocumentID
	})
	statements := make([]string, 0, len(rows))
	derived := make([]corpusDerivedRef, 0, len(rows))
	for _, row := range rows {
		if text := strings.TrimSpace(row.searchableText()); text != "" {
			statements = append(statements, text)
		}
		derived = append(derived, row.Provenance.DerivedFrom...)
	}
	digest := corpusEnvelope{
		DocumentID:  "missile-" + day,
		SourceType:  sourceTypeSIGINTEvent,
		Clearance:   tierClearance(sourceTypeSIGINTEvent, manifest),
		Issued:      day,
		Origin:      rows[0].Origin,
		Reliability: rows[0].Reliability,
		Provenance: corpusProvenance{
			Collector:   rows[0].Provenance.Collector,
			DerivedFrom: derived,
		},
		Title:  fmt.Sprintf("Missile and drone strike activity reported on %s", day),
		RawRef: corpusRawRef{Store: "missile_attacks", ID: "missile-" + day, Path: missileDatasetPath},
		Segments: []corpusSegment{{
			Role: segmentRoleBody, Searchable: true, Text: strings.Join(statements, " "),
		}},
	}
	return digest, digest.validate()
}

// loadLossesSlice reads both loss files and emits one daily delta per day in the
// window. The files are cumulative running totals in descending date order, so
// they are sorted ascending before any difference is taken -- a delta computed
// in the file's own order is the negation of the day's reporting.
func loadLossesSlice(
	root string, params corpusSliceParameters, manifest corpusManifest,
) ([]corpusEnvelope, error) {
	personnel, err := readDatasetRows(filepath.Join(root, filepath.FromSlash(lossesPersonnelPath)))
	if err != nil {
		return nil, err
	}
	equipment, err := readDatasetRows(filepath.Join(root, filepath.FromSlash(lossesEquipmentPath)))
	if err != nil {
		return nil, err
	}
	equipmentByDate := make(map[string]map[string]string, len(equipment))
	for _, row := range equipment {
		equipmentByDate[strings.TrimSpace(row["date"])] = row
	}

	days := make([]lossesDay, 0, len(personnel))
	for _, row := range personnel {
		date := strings.TrimSpace(row["date"])
		if date == "" {
			continue
		}
		days = append(days, lossesDay{
			Date:      date,
			Personnel: row,
			Equipment: equipmentByDate[date],
		})
	}
	sort.SliceStable(days, func(i, j int) bool { return days[i].Date < days[j].Date })

	envelopes := make([]corpusEnvelope, 0, len(days))
	for index, day := range days {
		if !params.inWindow(day.Date) {
			continue
		}
		// The day before the window opens supplies the first day's difference
		// without being converted itself, so the window bounds what the corpus
		// holds rather than what a delta may be computed from.
		var previous lossesDay
		if index > 0 {
			previous = days[index-1]
		}
		envelope, err := adaptLossesDelta(day, previous, manifest)
		if err != nil {
			return nil, err
		}
		envelopes = append(envelopes, envelope)
	}
	return envelopes, nil
}

// readDatasetLines returns the non-empty lines of a JSON Lines dataset. The ISW
// file carries assessments tens of kilobytes long, so the scanner is given a
// buffer that holds one.
func readDatasetLines(path string) ([][]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset: %w", err)
	}
	defer file.Close()
	lines := make([][]byte, 0, 512)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		lines = append(lines, append([]byte(nil), scanner.Bytes()...))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return lines, nil
}

// readDatasetRows returns a CSV dataset as column maps. Rows whose field count
// disagrees with the header are read as far as they go rather than failing the
// build, because the attack dataset carries embedded newlines in its free-text
// columns and a strict reader rejects the file.
func readDatasetRows(path string) ([]map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset: %w", err)
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read %s header: %w", path, err)
	}
	rows := make([]map[string]string, 0, 1024)
	for {
		record, err := reader.Read()
		if err != nil {
			break
		}
		row := make(map[string]string, len(header))
		for index, name := range header {
			if index < len(record) {
				row[name] = record[index]
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
