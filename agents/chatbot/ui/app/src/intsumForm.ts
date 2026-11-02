// The contract that fills the ministry INTSUM form from one turn (srd016 R3).
//
// Every field this produces traces to data the response carried or to context
// the caller supplied explicitly. Nothing is inferred: a section the provider
// wrote no heading for is empty, a table the response carries no metadata for
// is empty, and neither is filled with a standard line to make the form look
// complete (R3.2). A reviewer reads a filled section as an assertion the turn
// made, so an empty one has to mean the turn made none.
//
// The form is derived rather than held, so a re-render of the same answer
// produces the same form and no state can drift from the turn it came from.

import type { Answer, ChunkMetadata, Citation, Conformance } from "./api";
import { isExternalRecordID } from "./api";
import {
  INTSUM_SECTIONS,
  INTSUM_TEMPLATE_VERSION,
  sectionForHeading,
  type TemplateSection,
} from "./intsumTemplate";

// The ordered clearance set, lowest first. The same set api.ts declares for the
// selector; the banner uses it to rank composed sources rather than to gate
// anything, so an unrecognized level sorts above every known one for the reason
// srd010 R1.3 gives -- an unknown marking is treated as the most restrictive,
// never as the least.
const BANNER_LEVELS = ["unclassified", "restricted", "confidential", "secret"];

// What the caller knows about the turn that the response does not carry. Passed
// in rather than read from the clock inside, so the form is a pure function of
// its inputs and a test does not have to freeze time. A field the caller omits
// renders empty, which is the same rule the response-sourced fields follow.
export interface TurnContext {
  dateTimeGroup?: string;
  originator?: string;
  tasking?: string;
}

// The response carries chunk metadata alongside the composed documents. A turn
// whose sources report none leaves the source-evaluation table empty rather
// than ungraded, because no metadata and an ungraded source are different
// facts about the turn.
export type ComposedChunkMetadata = ChunkMetadata;

export interface SourceEvaluationRow {
  source: string;
  // The highest clearance among the source's composed chunks, uppercase like
  // the banner; "unmarked" when no composed chunk declares a level (GH-646).
  classification: string;
  reliability: string;
  credibility: string;
  // True when the corpus declares no grade for this source. The scale's own
  // answer for a source that cannot be judged is F6, and the corpus declares
  // that as a tier fallback; a source carrying neither is shown as ungraded
  // rather than as a blank cell, which reads as a rating that failed to load.
  ungraded: boolean;
}

export interface CitationIndexEntry {
  recordID: string;
  // The cited spans resting on this record, in the order the provider reported
  // them. A brief citing one record seventy times indexes one entry carrying
  // seventy assertions, which is the shape the live December turn produced.
  assertions: string[];
  // Whether the record is external open-source material (srd011 R7.3). The
  // form marks these visibly and never offers the corpus provenance walk,
  // which resolves internal record ids and would open nothing here.
  external: boolean;
}

export interface FilledSection {
  id: string;
  label: string;
  order: number;
  source: TemplateSection["source"];
  required: boolean;
  // Prose the provider composed under one of the section's declared headings.
  body?: string;
  rows?: SourceEvaluationRow[];
  citations?: CitationIndexEntry[];
  fields?: { id: string; label: string; value: string }[];
  banner?: string;
  // False when the section has no content. The form renders the heading and
  // nothing else, and the panel can say why from `source`.
  filled: boolean;
}

export interface IntsumForm {
  templateVersion: string;
  sections: FilledSection[];
  // Headings the provider wrote that no section declares. Reported rather than
  // dropped: a brief whose structure the template does not cover is a
  // conformance finding, which rel12.2 acts on and this release surfaces.
  unmatchedHeadings: string[];
}

// fillIntsumForm maps one answered turn onto the template.
//
// An answer that composed nothing still produces a form: every section renders
// with its heading and no content (R3.3). The four degraded outcomes the mesh
// distinguishes all arrive here as an answer whose attribution is unattributed,
// and none of them fills a section, because each fills only from what the
// response actually carried.
export function fillIntsumForm(answer: Answer, context: TurnContext = {}): IntsumForm {
  const composed = parseComposedSections(answer.text);
  const metadata = composedChunkMetadata(answer);

  // A block the provider composed under an undeclared heading renders as a
  // prose section of its own (GH-577), slotted between Outlook (order 6) and
  // the Citation Index (order 7): outside the template's sections but inside
  // the brief, which is exactly what the unmatched note says of it.
  const extraSections: FilledSection[] = composed.extras.map((extra, index) => ({
    id: `extra-${index}`,
    label: extra.heading,
    order: 6.5 + index * 0.001,
    source: "composed_markdown",
    required: false,
    body: extra.body,
    filled: true,
  }));

  const sections = INTSUM_SECTIONS.map((section) =>
    fillSection(section, answer, context, composed, metadata),
  )
    .concat(extraSections)
    .sort((left, right) => left.order - right.order);

  return {
    templateVersion: INTSUM_TEMPLATE_VERSION,
    sections,
    unmatchedHeadings: composed.unmatched,
  };
}

function fillSection(
  section: TemplateSection,
  answer: Answer,
  context: TurnContext,
  composed: ComposedSections,
  metadata: ComposedChunkMetadata[],
): FilledSection {
  const base: FilledSection = {
    id: section.id,
    label: section.label,
    order: section.order,
    source: section.source,
    required: section.required,
    filled: false,
  };

  if (section.source === "composed_markdown") {
    const body = composed.bodies.get(section.id);
    if (body) {
      return { ...base, body, filled: true };
    }
    return base;
  }

  switch (section.derivedFrom) {
    case "composed_source_clearance": {
      // A revision's banner never reads lower than the brief it started from
      // (GH-594): the fused prose can carry material the new retrieval did not
      // recompose, and demoting the marking would release it under-marked.
      const banner = higherBanner(highestClearance(metadata), answer.priorBanner);
      return banner ? { ...base, banner, filled: true } : base;
    }
    case "turn_context": {
      const values: Record<string, string | undefined> = {
        date_time_group: context.dateTimeGroup,
        originator: context.originator,
        tasking: context.tasking,
      };
      const fields = (section.fields ?? []).map((field) => ({
        id: field.id,
        label: field.label,
        value: values[field.id] ?? "",
      }));
      return { ...base, fields, filled: fields.some((field) => field.value !== "") };
    }
    case "composed_source_admiralty": {
      const rows = sourceEvaluationRows(metadata);
      return { ...base, rows, filled: rows.length > 0 };
    }
    case "response_citations": {
      const citations = citationIndex(answer.citations);
      return { ...base, citations, filled: citations.length > 0 };
    }
    default:
      return base;
  }
}

interface ComposedSections {
  bodies: Map<string, string>;
  unmatched: string[];
  // Blocks the provider wrote under headings the template does not declare,
  // in provider order. A run of consecutive undeclared headings folds into one
  // block titled by its first heading, the rest kept verbatim in the body, so
  // a provider-written section keeps its own sub-structure.
  extras: { heading: string; body: string }[];
}

// markdownHeadingRE matches an ATX heading at any level and captures its text.
// The provider is asked for markdown headings; a brief that carries none parses
// to no sections, which renders an empty form rather than one section holding
// the whole answer.
const markdownHeadingRE = /^\s{0,3}#{1,6}\s+(.+?)\s*#*\s*$/;

// parseComposedSections splits the composed markdown at its headings and
// assigns each block to the section declaring that heading.
//
// This reads the structure composition produced; it does not impose one
// (srd012 R1.3). Text before the first heading belongs to no section and is
// dropped rather than assigned to the first, because assigning it would be the
// reconstruction that requirement forbids. A heading no section declares is
// reported, and its block is kept as an extra rather than dropped (GH-577): a
// revision that adds a section under its own heading must still show — and
// mark — what the provider composed, with the unmatched note and the server's
// conformance verdict saying it sits outside the template.
export function parseComposedSections(markdown: string): ComposedSections {
  const bodies = new Map<string, string>();
  const unmatched: string[] = [];
  const extras: { heading: string; body: string }[] = [];

  let current: TemplateSection | undefined;
  let unknown = false;
  let extraHeading: string | undefined;
  let buffer: string[] = [];

  const flush = () => {
    const body = buffer.join("\n").trim();
    if (current && !unknown && body) {
      // A section declared under two headings the provider wrote twice keeps
      // both blocks rather than the last, so nothing composed is lost.
      const existing = bodies.get(current.id);
      bodies.set(current.id, existing ? `${existing}\n\n${body}` : body);
    }
    if (unknown && extraHeading !== undefined && body) {
      extras.push({ heading: extraHeading, body });
    }
    buffer = [];
  };

  for (const line of markdown.split("\n")) {
    const match = markdownHeadingRE.exec(line);
    if (!match) {
      buffer.push(line);
      continue;
    }
    const heading = match[1];
    const section = sectionForHeading(heading);
    if (section === undefined && unknown && extraHeading !== undefined) {
      // A further undeclared heading inside an open extra block stays in the
      // block as the line the provider wrote, keeping the section whole.
      unmatched.push(heading.trim());
      buffer.push(line);
      continue;
    }
    flush();
    current = section;
    unknown = section === undefined;
    extraHeading = unknown ? heading.trim() : undefined;
    if (unknown) {
      unmatched.push(heading.trim());
    }
  }
  flush();

  return { bodies, unmatched, extras };
}

// composedChunkMetadata gathers the declared metadata of every chunk the turn
// composed from, flattened across sources and queries.
export function composedChunkMetadata(answer: Answer): ComposedChunkMetadata[] {
  const records: ComposedChunkMetadata[] = [];
  for (const outcome of answer.composedMetadata ?? []) {
    for (const query of outcome ?? []) {
      for (const record of query ?? []) {
        if (record) {
          records.push(record);
        }
      }
    }
  }
  return records;
}

// highestClearance returns the most restrictive level among the composed
// sources, which is what the banner marks. It marks the document, not the
// reader, so the requester's own level is never consulted here (R3.6).
export function highestClearance(metadata: ComposedChunkMetadata[]): string | undefined {
  let best: string | undefined;
  let bestRank = -1;
  for (const record of metadata) {
    const level = record.clearance?.trim();
    if (!level) {
      continue;
    }
    const index = BANNER_LEVELS.indexOf(level.toLowerCase());
    // An unrecognized marking outranks every known level rather than sorting
    // below them, so a banner never reads lower than the material warrants.
    const rank = index === -1 ? BANNER_LEVELS.length : index;
    if (rank > bestRank) {
      bestRank = rank;
      best = level;
    }
  }
  return best;
}

// higherBanner returns the more restrictive of two banner levels, ranked the
// way highestClearance ranks a single set — an unrecognized marking outranks
// every known level, so a merge never lowers a marking it cannot place.
export function higherBanner(a: string | undefined, b: string | undefined): string | undefined {
  if (!a) return b;
  if (!b) return a;
  const rank = (level: string) => {
    const index = BANNER_LEVELS.indexOf(level.trim().toLowerCase());
    return index === -1 ? BANNER_LEVELS.length : index;
  };
  return rank(b) > rank(a) ? b : a;
}

// sourceEvaluationRows builds the Admiralty table, one row per distinct source.
//
// Chunks are grouped by their source identifier because a brief composing six
// chunks from one report evaluates that report once. A source whose chunks
// carry no grade is shown as ungraded, which is the scale's answer for a source
// that cannot be judged rather than an omission the reader has to interpret.
export function sourceEvaluationRows(metadata: ComposedChunkMetadata[]): SourceEvaluationRow[] {
  const rows = new Map<string, SourceEvaluationRow>();
  // A source's marking is the highest clearance among its composed chunks --
  // the banner's own most-restrictive rule (GH-646) -- so every chunk is
  // read even though the Admiralty grade comes from the first.
  const marking = new Map<string, string | undefined>();
  for (const record of metadata) {
    const source = record.source_id?.trim() || record.source_type?.trim();
    if (!source) {
      continue;
    }
    marking.set(source, higherBanner(marking.get(source), record.clearance?.trim() || undefined));
    if (rows.has(source)) {
      continue;
    }
    const reliability = record.admiralty_source_reliability?.trim() ?? "";
    const credibility =
      typeof record.admiralty_information_credibility === "number"
        ? String(record.admiralty_information_credibility)
        : "";
    const ungraded = reliability === "" || credibility === "";
    rows.set(source, {
      source,
      classification: "",
      reliability: ungraded ? "ungraded" : reliability,
      credibility: ungraded ? "ungraded" : credibility,
      ungraded,
    });
  }
  for (const [source, row] of rows) {
    const level = marking.get(source);
    row.classification = level ? level.toUpperCase() : "unmarked";
  }
  return [...rows.values()];
}

// citationIndex lists each cited record once with the assertions resting on it.
//
// The provider cites a span at a time, and a brief may cite one record many
// times -- the December demonstration turn reported seventy citations over five
// records. Indexing per citation would make the section a list of repeats, so
// the index is per record and the assertions collect under it in the order the
// provider reported them.
export function citationIndex(citations: Citation[]): CitationIndexEntry[] {
  const entries = new Map<string, CitationIndexEntry>();
  for (const citation of citations) {
    const recordID = citation.record_id?.trim();
    if (!recordID) {
      continue;
    }
    const entry = entries.get(recordID);
    const assertion = citation.text.trim();
    if (entry) {
      if (assertion && !entry.assertions.includes(assertion)) {
        entry.assertions.push(assertion);
      }
      continue;
    }
    entries.set(recordID, {
      recordID,
      assertions: assertion ? [assertion] : [],
      external: isExternalRecordID(recordID),
    });
  }
  return [...entries.values()];
}

// How the form presents the structural verdict (srd012 R4.1). Derived, like
// everything else on the form: the words come from the verdict the server
// reported, and none of them is approval language -- conformance is a
// structural fact about sections, not a review of the brief (AC5). A missing
// verdict, or one this contract does not know, renders nothing rather than a
// guess, the form's standing empty-state rule.
export interface ConformanceDisplay {
  tone: "conformant" | "draft" | "unchecked";
  line: string;
  defects: string[];
  asserts?: string;
}

export function conformanceDisplay(conformance?: Conformance): ConformanceDisplay | undefined {
  if (!conformance) {
    return undefined;
  }
  const version = conformance.template_version?.trim();
  const versionSuffix = version ? ` to template v${version}` : "";
  switch (conformance.verdict) {
    case "conformant":
      return {
        tone: "conformant",
        line: `structurally conformant${versionSuffix}`,
        defects: [],
        asserts: conformance.asserts,
      };
    case "non_conformant":
      return {
        tone: "draft",
        // Draft, not final: a failing brief is surfaced with its defects and
        // retained for the analyst to edit (srd012 R4.1, R4.3).
        line: `draft — does not conform${versionSuffix}`,
        defects: conformance.defects ?? [],
        asserts: conformance.asserts,
      };
    case "unchecked":
      return {
        tone: "unchecked",
        line: "the structural check did not run on this brief",
        defects: [],
        asserts: conformance.asserts,
      };
    default:
      return undefined;
  }
}

// admiraltyGradeIndex maps each composed document to its compact Admiralty
// grade ("B2"), for the inline marks after citations (GH-535). Ungraded
// sources are absent rather than labeled, so prose carries only real grades.
export function admiraltyGradeIndex(metadata: ChunkMetadata[][][]): Map<string, string> {
  const index = new Map<string, string>();
  for (const source of metadata) {
    for (const query of source) {
      for (const record of query) {
        const doc = record.source_id?.trim();
        const reliability = record.admiralty_source_reliability?.trim() ?? "";
        const credibility =
          typeof record.admiralty_information_credibility === "number"
            ? String(record.admiralty_information_credibility)
            : "";
        const key = doc?.toLowerCase();
        if (key && reliability !== "" && credibility !== "" && !index.has(key)) {
          index.set(key, reliability + credibility);
        }
      }
    }
  }
  return index;
}

// citationGrade resolves one cited record to its source grade: record ids are
// chunk ids, graded at the parent document the metadata rows name.
const gradeChunkSuffix = /-(atomic|medium-900|large-1800)-\d+$/i;
export function citationGrade(recordID: string, grades: Map<string, string>): string {
  const id = recordID.toLowerCase();
  return grades.get(id) ?? grades.get(id.replace(gradeChunkSuffix, "")) ?? "";
}

// classificationIndex maps each composed document to the highest clearance
// among its chunks, for the cited-span tints. Unclassified and unmarked
// sources are absent, so unclassified prose keeps the plain mark.
export function classificationIndex(metadata: ChunkMetadata[][][]): Map<string, string> {
  const index = new Map<string, string>();
  for (const source of metadata) {
    for (const query of source) {
      for (const record of query) {
        const doc = record.source_id?.trim().toLowerCase();
        const level = record.clearance?.trim().toLowerCase();
        if (!doc || !level || level === "unclassified") {
          continue;
        }
        index.set(doc, higherBanner(index.get(doc), level) ?? level);
      }
    }
  }
  return index;
}

// citationMarking resolves one cited record to its source's classification,
// through the same chunk-to-document step citationGrade takes. Empty for an
// unclassified or unknown source.
export function citationMarking(recordID: string, markings: Map<string, string>): string {
  const id = recordID.toLowerCase();
  return markings.get(id) ?? markings.get(id.replace(gradeChunkSuffix, "")) ?? "";
}
