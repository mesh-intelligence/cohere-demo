// Marking cited spans inside a rendered section (GH-382). The response
// carries every citation's span and record, but the form rendered prose
// plainly, so the BLUF read as uncited even when its exact sentence was in
// the citation index -- the reader could not see where an assertion's support
// was made, only that support existed somewhere.
//
// The marks are computed by locating each citation's own text inside the
// section body rather than by slicing the section with the answer's rune
// offsets: a section body is a substring the form cut from the answer, and
// carrying offsets through that cut is a second place for the rune-vs-byte
// mistake to live (the offsets are rune-based; render_citations' comment
// records why). Text location cannot drift out of range, and a span that the
// section does not contain simply produces no mark here -- it is marked in
// whichever section does hold it.

export interface SpanCitation {
  text: string;
  recordID: string;
}

export interface MarkedSegment {
  text: string;
  // The records citing this segment, empty when the segment is uncited prose.
  records: string[];
}

interface Interval {
  start: number;
  end: number;
  records: string[];
}

// markCitations splits a body into consecutive segments, each either plain or
// cited by one or more records. Overlapping citations merge into one segment
// carrying both records, so a sentence two sources support reads as one mark
// rather than as a nested pair.
export function markCitations(body: string, citations: SpanCitation[]): MarkedSegment[] {
  const intervals: Interval[] = [];
  for (const citation of citations) {
    const needle = citation.text.trim();
    if (needle.length < 12) continue; // too short to locate unambiguously
    let from = 0;
    for (;;) {
      const at = body.indexOf(needle, from);
      if (at < 0) break;
      intervals.push({ start: at, end: at + needle.length, records: [citation.recordID] });
      from = at + needle.length;
    }
  }
  if (intervals.length === 0) return [{ text: body, records: [] }];

  intervals.sort((left, right) => left.start - right.start || right.end - left.end);
  const merged: Interval[] = [];
  for (const interval of intervals) {
    const last = merged[merged.length - 1];
    if (last && interval.start <= last.end) {
      last.end = Math.max(last.end, interval.end);
      for (const record of interval.records) {
        if (!last.records.includes(record)) last.records.push(record);
      }
      continue;
    }
    merged.push({ ...interval, records: [...interval.records] });
  }

  const segments: MarkedSegment[] = [];
  let cursor = 0;
  for (const interval of merged) {
    if (interval.start > cursor) {
      segments.push({ text: body.slice(cursor, interval.start), records: [] });
    }
    segments.push({ text: body.slice(interval.start, interval.end), records: interval.records });
    cursor = interval.end;
  }
  if (cursor < body.length) segments.push({ text: body.slice(cursor), records: [] });
  return segments;
}

// --- What a revision changed (GH-605) ---

export interface RevisionSegment extends MarkedSegment {
  // True when this text does not appear in the brief the revision started
  // from, so the form can tint what the revise note actually changed.
  isNew: boolean;
}

function collapse(text: string): string {
  return text.replace(/\s+/g, " ").trim();
}

// A piece shorter than this collapses to connective tissue ("- ", "the",
// a heading marker) that membership cannot judge; it is never flagged new.
const MIN_JUDGED = 12;

function isNewText(text: string, collapsedPrior: string): boolean {
  const needle = collapse(text);
  return needle.length >= MIN_JUDGED && !collapsedPrior.includes(needle);
}

// splitSentences cuts text at sentence enders and newlines, keeping every
// character: the pieces concatenate back to the input exactly, so the marks
// layered on top never gain or lose a byte.
function splitSentences(text: string): string[] {
  const parts: string[] = [];
  let start = 0;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (ch === "." || ch === "!" || ch === "?" || ch === "\n") {
      let j = i + 1;
      while (j < text.length && text[j] === " ") j++;
      parts.push(text.slice(start, j));
      start = j;
      i = j - 1;
    }
  }
  if (start < text.length) parts.push(text.slice(start));
  return parts;
}

// flagRevisionSegments layers changed-text detection over the citation
// segments: a cited span is judged whole (spans are sentence-sized), plain
// runs are judged sentence by sentence. Membership is whitespace-collapsed,
// matching how locateChunk treats re-wrapped text. Without a prior brief
// nothing is new — the flags all read false and the form renders untinted.
export function flagRevisionSegments(
  segments: MarkedSegment[],
  priorText?: string,
): RevisionSegment[] {
  if (!priorText) {
    return segments.map((segment) => ({ ...segment, isNew: false }));
  }
  const prior = collapse(priorText);
  const out: RevisionSegment[] = [];
  for (const segment of segments) {
    if (segment.records.length > 0) {
      out.push({ ...segment, isNew: isNewText(segment.text, prior) });
      continue;
    }
    for (const piece of splitSentences(segment.text)) {
      out.push({ text: piece, records: [], isNew: isNewText(piece, prior) });
    }
  }
  return out;
}

// citedFraction reports how much of a body's non-whitespace text carries a
// citation, which is what lets a section state "partly cited" honestly
// instead of leaving the reader to eyeball the marks.
export function citedFraction(segments: MarkedSegment[]): number {
  let cited = 0;
  let total = 0;
  for (const segment of segments) {
    const weight = segment.text.replace(/\s+/g, "").length;
    total += weight;
    if (segment.records.length > 0) cited += weight;
  }
  return total === 0 ? 0 : cited / total;
}

// --- Cited-versus-composed (GH-381) ---

// A chunk id carries its profile and position; the source evaluation names
// the document. The suffix rule mirrors the harness's own
// (magefiles/evaluation_corpus.go chunkProfileSuffix), so the two agree on
// what "the same document" means.
const CHUNK_PROFILE_SUFFIX = /-(atomic|medium-900|large-1800)-\d+$/i;

export function documentIDOfRecord(recordID: string): string {
  return recordID.replace(CHUNK_PROFILE_SUFFIX, "");
}

// citedSourceIDs reduces a turn's citations to the documents they cite, which
// is what lets the evaluation table separate the sources that back an
// assertion from those retrieval merely placed in front of the provider. An
// external record (a URL from the search branch) has no chunk suffix and
// stands for itself.
export function citedSourceIDs(recordIDs: string[]): Set<string> {
  return new Set(recordIDs.map(documentIDOfRecord));
}
