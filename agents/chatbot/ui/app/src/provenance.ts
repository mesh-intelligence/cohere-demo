// The provenance walk's pure logic (srd016 R6): given the served records, what
// the panel shows and where the highlight lands. Kept apart from the component
// so the span arithmetic and the version grouping are testable without a
// renderer.

import { envelopeRecordID, type ChunkLineage, type DocumentView } from "./documentsApi";

// Where the cited chunk sits inside the full document, as a half-open span
// into the document text the panel renders.
export interface LocatedSpan {
  start: number;
  end: number;
}

// locateChunk finds the chunk's passage inside the full document (R6.3).
//
// The lineage's char_span indexes the joined searchable body, and the served
// document is the whole envelope -- unsearchable segments included (R6.2) --
// so the two disagree whenever an unsearchable segment precedes the chunk. The
// span is trusted only when the text it selects is the chunk's text; otherwise
// the chunk text is searched for directly. A passage that cannot be located
// returns undefined and the panel shows the document unhighlighted rather than
// highlighting the wrong lines.
export function locateChunk(
  documentText: string,
  chunkText: string,
  charSpan?: number[],
): LocatedSpan | undefined {
  const chunk = chunkText.trim();
  if (documentText === "" || chunk === "") {
    return undefined;
  }
  if (charSpan && charSpan.length === 2) {
    const [start, end] = charSpan;
    if (start >= 0 && end > start && end <= documentText.length) {
      const selected = documentText.slice(start, end).trim();
      if (selected === chunk) {
        return { start, end };
      }
    }
  }
  const found = documentText.indexOf(chunk);
  if (found !== -1) {
    return { start: found, end: found + chunk.length };
  }
  // A chunk cut from one long paragraph joins its pieces with blank lines the
  // document never wrote -- the assessment tier's whole body is a single line
  // -- so an exact search cannot find it. The passage is still there with the
  // same non-whitespace characters in the same order, so both sides are
  // matched with runs of whitespace collapsed, and the match is mapped back to
  // offsets in the document as served.
  const doc = collapseWhitespace(documentText);
  const target = collapseWhitespace(chunk).text;
  if (target === "") {
    return undefined;
  }
  const at = doc.text.indexOf(target);
  if (at === -1) {
    return undefined;
  }
  return { start: doc.offsets[at], end: doc.offsets[at + target.length - 1] + 1 };
}

// collapseWhitespace rewrites text with every whitespace run as one space,
// recording for each kept character its offset in the original, so a match in
// the collapsed text names a span in the text the panel renders.
function collapseWhitespace(text: string): { text: string; offsets: number[] } {
  let collapsed = "";
  const offsets: number[] = [];
  let pendingSpaceAt = -1;
  for (let index = 0; index < text.length; index++) {
    const character = text[index];
    if (/\s/.test(character)) {
      pendingSpaceAt = pendingSpaceAt === -1 ? index : pendingSpaceAt;
      continue;
    }
    if (pendingSpaceAt !== -1 && collapsed !== "") {
      collapsed += " ";
      offsets.push(pendingSpaceAt);
    }
    pendingSpaceAt = -1;
    collapsed += character;
    offsets.push(index);
  }
  return { text: collapsed, offsets };
}

// One chunk-profile's rows of a document, ordered by position.
export interface ProfileVersions {
  profile: string;
  chunks: DocumentView[];
}

// versionsByProfile groups a document's served chunk rows by chunk profile,
// each profile's chunks in position order (R6.1). The envelope row is not a
// version and is left out; a row without a profile has no shelf to sit on and
// is left out with it.
export function versionsByProfile(views: DocumentView[]): ProfileVersions[] {
  const profiles = new Map<string, DocumentView[]>();
  for (const view of views) {
    if (view.kind !== "chunk" || !view.chunkProfile) {
      continue;
    }
    const rows = profiles.get(view.chunkProfile) ?? [];
    rows.push(view);
    profiles.set(view.chunkProfile, rows);
  }
  return [...profiles.entries()]
    .map(([profile, chunks]) => ({
      profile,
      chunks: [...chunks].sort(
        (left, right) => (left.parent?.position ?? 0) - (right.parent?.position ?? 0),
      ),
    }))
    .sort((left, right) => left.profile.localeCompare(right.profile));
}

// envelopeOf picks the document's envelope row out of a versions read.
export function envelopeOf(views: DocumentView[], documentID: string): DocumentView | undefined {
  return views.find((view) => view.id === envelopeRecordID(documentID));
}

// chunkCounterLabel renders "chunk N of M" from a cited chunk's lineage. The
// store's position is 1-based -- the corpus probe asserts
// 1 <= position <= sibling_count -- so the position renders as it is: adding
// one displayed the first chunk of a single-chunk document as "chunk 2 of 1"
// (GH-350). Either field absent means there is nothing truthful to say, and
// the caller renders no counter at all rather than a guessed one.
export function chunkCounterLabel(parent?: ChunkLineage): string | undefined {
  if (!parent || parent.position === undefined || parent.sibling_count === undefined) {
    return undefined;
  }
  return `chunk ${parent.position} of ${parent.sibling_count}`;
}

// isRawSourceRef says whether a derivation reference names a raw source row
// (a dataset file and line, "missile_attacks_daily.csv:3989") rather than a
// served corpus document. The distinction decides whether the provenance
// panel offers a walk: the documents API serves document identifiers only,
// so walking a raw ref can only dead-end, and the reference is a provenance
// fact to display rather than a link to follow (GH-351).
export function isRawSourceRef(id: string): boolean {
  return /\.\w+:\d+$/.test(id);
}
