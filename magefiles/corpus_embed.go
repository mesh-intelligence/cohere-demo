// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"sort"
	"strings"
)

// The budgeted embed selection (srd017 R6). Conversion and chunking are free
// and cover the window whole; embedding is paid per chunk from a personal
// account, and the corpus cuts into roughly seventy thousand chunks across the
// four profiles. So which chunks carry vectors is a declared artifact rather
// than a consequence of how much data happened to be on disk.
//
// The rule that makes the budget worth having is R6.3: the count is checked
// before the first request goes out. A pass that embedded until it reached the
// ceiling would spend the whole budget every time it was misconfigured and
// leave a store whose contents depended on iteration order. The failure this
// release wants is a build that refuses having spent nothing.

// corpusEmbedCeiling is the most chunks that may carry vectors at any single
// profile, all tiers combined (srd017 R6.2).
const corpusEmbedCeiling = 3000

// corpusEmbedSelection is the declared selection: which profiles are embedded,
// over what sub-window, and how much of the budget each tier may take. It is
// recorded with the store, so a store says what was paid for rather than
// leaving it to be recovered by counting records (R6.1).
type corpusEmbedSelection struct {
	WindowStart string   `yaml:"window_start"`
	WindowEnd   string   `yaml:"window_end"`
	Profiles    []string `yaml:"profiles"`
	// Tiers embedded across the whole conversion window rather than the
	// sub-window above. A tier small enough that embedding all of it costs
	// little belongs here when narrowing it would cost the demonstration a
	// condition it needs.
	FullWindowTiers []string       `yaml:"full_window_tiers,omitempty"`
	PerTier         map[string]int `yaml:"per_tier"`
	Ceiling         int            `yaml:"ceiling"`
}

// spansFullWindow reports whether a tier is embedded across the conversion
// window rather than the embed sub-window.
func (s corpusEmbedSelection) spansFullWindow(tier string) bool {
	for _, declared := range s.FullWindowTiers {
		if declared == tier {
			return true
		}
	}
	return false
}

// The embed sub-window is the conversion window. It was narrower while the
// corpus converted twelve months and paid to search one; the store's weight
// then narrowed conversion to the same month (srd017 R3.1), so the two now
// coincide and the sub-window is kept as a seam rather than a difference.
const (
	embedWindowStart = sliceWindowStart
	embedWindowEnd   = sliceWindowEnd
)

// defaultCorpusEmbedSelection is the allocation the demonstration runs at.
//
// Only the retrieval profile is embedded. The other two are cut and stored so a
// citation resolves to its neighbours and its other chunk size, and those reads
// are by identifier rather than by similarity, so they need no vector.
//
// The sub-window is one month rather than the twelve the corpus converts,
// because the store is a versioned repository artifact a clone seeds from
// without a build (srd015 R6.1) and twelve months of vectors is around 114 MB
// of float text. One month is about 40 MB. Conversion still covers the year, so
// a citation resolves to any document in it and the interface facets span it;
// what narrows is the span retrieval searches.
//
// The synthetic tier is exempt. It holds 27 reports across the year, embedding
// all of them costs about a megabyte, and it is the only tier carrying more
// than one clearance level -- a single month of it would leave the rel11.0
// two-level assertion with one level and nothing to withhold.
//
// The per-tier allocation follows what each tier contributes to a tasking,
// bounded so no tier swamps retrieval. At the first allocation the assessments
// took 70 percent of the embedded chunks, and a turn on the strike tasking
// composed from assessments alone -- the tier that counts what was launched on
// those nights was 0.7 percent of the collection and never surfaced. srd015
// R1.2 requires a tasking's relevant documents to sit across sources, and a
// collection where one tier is seven in ten chunks cannot show that (GH-286).
//
// The assessments' allowance was then sized by measurement rather than by
// judgment. Ranking the verify's brief tasking against the collection put six
// assessment chunks ahead of every other tier, all cut from positions one
// through four of their documents, with the loss and strike digests at ranks
// seven through nine just below the five a turn composes from. An allowance of
// 60 is two chunks per assessment under the round-robin -- the opening and the
// key-takeaways sections, which carry the tier's best-ranked material -- and
// it is the widest cap at which the other tiers reach composition. Every
// assessment stays retrievable, its deeper sections stay served by identifier,
// and the three small tiers fit entirely with headroom.
func defaultCorpusEmbedSelection() corpusEmbedSelection {
	return corpusEmbedSelection{
		WindowStart:     embedWindowStart,
		WindowEnd:       embedWindowEnd,
		Profiles:        []string{chunkProfileMedium},
		FullWindowTiers: []string{sourceTypeHumint},
		PerTier: map[string]int{
			sourceTypeSIGINTSeries:    365,
			sourceTypeSIGINTEvent:     130,
			sourceTypeHumint:          60,
			sourceTypeOSINTNews:       400,
			sourceTypeOSINTAssessment: 60,
		},
		Ceiling: corpusEmbedCeiling,
	}
}

// embedsProfile reports whether a profile is in the selection.
func (s corpusEmbedSelection) embedsProfile(profile string) bool {
	for _, declared := range s.Profiles {
		if declared == profile {
			return true
		}
	}
	return false
}

// inWindow reports whether a document's issued date falls in the embed
// sub-window. The sub-window may be narrower than the conversion window, which
// is how the demonstration covers twelve months of citable documents while
// paying to search a shorter span.
func (s corpusEmbedSelection) inWindow(issued string) bool {
	if s.WindowStart != "" && issued < s.WindowStart {
		return false
	}
	if s.WindowEnd != "" && issued > s.WindowEnd {
		return false
	}
	return true
}

// corpusEmbedCandidate is one document and the chunks cut from it, which is the
// unit the selection reasons over: the tier and the issued date are properties
// of the document, and the chunks inherit them.
type corpusEmbedCandidate struct {
	Envelope corpusEnvelope
	Records  []corpusChunkRecord
}

// corpusEmbedPlan is what the pass will do, decided before it does any of it.
type corpusEmbedPlan struct {
	Selected   map[string]bool
	PerProfile map[string]int
	PerTier    map[string]int
	Considered int
}

// count returns the number of chunks the plan will embed.
func (p corpusEmbedPlan) count() int {
	return len(p.Selected)
}

// planCorpusEmbedding decides which chunks carry vectors and checks the result
// against the ceiling before any request is issued (srd017 R6.1, R6.2, R6.3).
//
// The selection is deterministic: candidates are ordered by issued date and
// then by chunk identifier, so two runs over unchanged inputs select the same
// chunks and a re-run embeds nothing. Ordering by date rather than by
// identifier alone means a tier truncated by its allocation keeps a contiguous
// run of the window rather than an alphabetical scatter across it.
func planCorpusEmbedding(
	candidates []corpusEmbedCandidate, selection corpusEmbedSelection,
) (corpusEmbedPlan, error) {
	plan := corpusEmbedPlan{
		Selected:   make(map[string]bool),
		PerProfile: make(map[string]int),
		PerTier:    make(map[string]int),
	}

	ordered := append([]corpusEmbedCandidate(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Envelope.Issued != ordered[j].Envelope.Issued {
			return ordered[i].Envelope.Issued < ordered[j].Envelope.Issued
		}
		return ordered[i].Envelope.DocumentID < ordered[j].Envelope.DocumentID
	})

	// Eligible chunks are gathered per profile and tier, kept as one list per
	// document in the document's own position order.
	type allocationKey struct {
		profile string
		tier    string
	}
	eligible := make(map[allocationKey][][]corpusChunkRecord)
	keys := make([]allocationKey, 0)
	for _, candidate := range ordered {
		tier := candidate.Envelope.SourceType
		if !selection.spansFullWindow(tier) && !selection.inWindow(candidate.Envelope.Issued) {
			continue
		}
		records := append([]corpusChunkRecord(nil), candidate.Records...)
		sort.SliceStable(records, func(i, j int) bool {
			return records[i].ChunkID < records[j].ChunkID
		})
		perProfile := make(map[string][]corpusChunkRecord)
		for _, record := range records {
			if !selection.embedsProfile(record.ChunkProfile) {
				continue
			}
			plan.Considered++
			if _, declared := selection.PerTier[tier]; !declared {
				// A tier the selection does not budget for contributes nothing
				// rather than everything. An undeclared allowance is an
				// authoring omission, and the direction that costs money is the
				// wrong one to default to.
				continue
			}
			perProfile[record.ChunkProfile] = append(perProfile[record.ChunkProfile], record)
		}
		for profile, group := range perProfile {
			key := allocationKey{profile: profile, tier: tier}
			if _, seen := eligible[key]; !seen {
				keys = append(keys, key)
			}
			eligible[key] = append(eligible[key], group)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].profile != keys[j].profile {
			return keys[i].profile < keys[j].profile
		}
		return keys[i].tier < keys[j].tier
	})

	// A tier's allowance is spent round-robin across its documents: every
	// document contributes its first chunk before any contributes its second.
	// Spending it in date order instead lets the long documents at the front of
	// the window exhaust the allowance, which drops later documents from
	// retrieval entirely -- including ones the manifest names relevant to the
	// primary tasking. Under a cap, the tail of a long document is what gets
	// trimmed, not the documents behind it (GH-286).
	for _, key := range keys {
		allowance := selection.PerTier[key.tier]
		documents := eligible[key]
		taken := 0
		for round := 0; taken < allowance; round++ {
			progressed := false
			for _, records := range documents {
				if round >= len(records) || taken >= allowance {
					continue
				}
				record := records[round]
				plan.Selected[record.ChunkID] = true
				plan.PerProfile[key.profile]++
				plan.PerTier[key.tier]++
				taken++
				progressed = true
			}
			if !progressed {
				break
			}
		}
	}

	if err := assertEmbedCeiling(plan.PerProfile, selection); err != nil {
		return corpusEmbedPlan{}, err
	}
	return plan, nil
}

// assertEmbedCeiling holds the plan to the declared ceiling. It runs on the
// plan rather than on the pass, which is the whole point: a breach is reported
// with the provider uncalled.
func assertEmbedCeiling(perProfile map[string]int, selection corpusEmbedSelection) error {
	ceiling := selection.Ceiling
	if ceiling <= 0 {
		ceiling = corpusEmbedCeiling
	}
	profiles := make([]string, 0, len(perProfile))
	for profile := range perProfile {
		profiles = append(profiles, profile)
	}
	sort.Strings(profiles)
	for _, profile := range profiles {
		if perProfile[profile] > ceiling {
			return fmt.Errorf("the embed selection holds %d chunks at profile %s,"+
				" above the declared ceiling of %d; embedding is paid per chunk, so"+
				" the count is checked before the first request rather than after"+
				" the last (srd017 R6.2, R6.3). Narrow the selection's window or"+
				" its per-tier allocation and rebuild",
				perProfile[profile], profile, ceiling)
		}
	}
	return nil
}

// embedMemo embeds one passage once per build, however many chunks carry it.
//
// A vector is a pure function of the text and the model, so two chunks whose
// text is byte-identical have the same vector and the second request buys
// nothing. The corpus makes that worth doing: the assessments reuse standing
// methodology paragraphs, the news channels repost identical wire copy, and the
// two aggregate tiers render prose from templates, so days reporting the same
// figures produce the same sentence.
//
// The cache is keyed on the text digest the chunk record already carries, and
// it lives for one build. Nothing is written differently -- the chunks that
// share a passage each get the vector they would have been given -- so a corpus
// holding no duplicate text is embedded exactly as it was before.
type embedMemo struct {
	inner  func(string) ([]float64, error)
	cache  map[string][]float64
	reused int
}

// memoizeEmbedder returns the identity with a memoizing embed function and the
// memo that counts what it saved.
func memoizeEmbedder(identity corpusEmbeddingIdentity) (corpusEmbeddingIdentity, *embedMemo) {
	memo := &embedMemo{
		inner: identity.embed,
		cache: make(map[string][]float64),
	}
	identity.embed = memo.embed
	return identity, memo
}

// embed returns a copy rather than the cached slice, so a caller that mutates
// what it was handed cannot corrupt the vector every later chunk receives.
func (m *embedMemo) embed(text string) ([]float64, error) {
	digest := corpusTextDigest(text)
	if values, held := m.cache[digest]; held {
		m.reused++
		return append([]float64(nil), values...), nil
	}
	values, err := m.inner(text)
	if err != nil {
		return nil, err
	}
	m.cache[digest] = append([]float64(nil), values...)
	return append([]float64(nil), values...), nil
}

// requestsSaved reports how many provider calls the cache avoided.
func (m *embedMemo) requestsSaved() int {
	if m == nil {
		return 0
	}
	return m.reused
}

// describeEmbedPlan renders the plan for a build's own reporting, so an
// operator sees what a run will cost before it runs.
func describeEmbedPlan(plan corpusEmbedPlan, selection corpusEmbedSelection) string {
	profiles := make([]string, 0, len(plan.PerProfile))
	for profile := range plan.PerProfile {
		profiles = append(profiles, profile)
	}
	sort.Strings(profiles)
	parts := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		parts = append(parts, fmt.Sprintf("%s %d", profile, plan.PerProfile[profile]))
	}
	tiers := make([]string, 0, len(plan.PerTier))
	for tier := range plan.PerTier {
		tiers = append(tiers, tier)
	}
	sort.Strings(tiers)
	for _, tier := range tiers {
		parts = append(parts, fmt.Sprintf("%s %d", tier, plan.PerTier[tier]))
	}
	return fmt.Sprintf("embed selection: %d of %d considered chunks, ceiling %d (%s)",
		plan.count(), plan.Considered, selection.Ceiling, strings.Join(parts, ", "))
}
