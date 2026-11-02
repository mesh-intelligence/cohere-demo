// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0
//
// Sentence segmentation for the audit (GH-650). The audit's unit is the
// sentence, not the citation fragment: offsets are preserved so each unit can
// collect the citations whose spans overlap it, and the judge then sees whole
// assertions — the numbers included — rather than the "December 16" the raw
// citation offsets clip out.

import type { Citation } from "./api";
import { isExternalRecordID } from "./api";

export interface AuditTarget {
  // The sentence text, trimmed for display and judging.
  sentence: string;
  // Offsets into the original brief, so citations map by overlap.
  start: number;
  end: number;
  // Distinct corpus record ids cited inside this sentence, in citation order.
  recordIDs: string[];
}

// A sentence ends at . ! or ? followed by whitespace and an upper-case,
// digit, quote, or dash opener. Abbreviation-safe enough for brief prose;
// a missed split only widens the unit the judge reads, never drops text.
const SENTENCE_BREAK = /(?<=[.!?])\s+(?=["“(—-]?[A-Z0-9])/;

// auditTargets segments the brief into sentence units with their citations.
//
// Markdown headings and blank lines are skipped (structure, not assertions);
// each bullet line is one unit with its marker stripped; prose lines split at
// sentence boundaries. External (web) citations are excluded — the audit
// judges against corpus chunks the analyst can fetch at clearance.
export function auditTargets(text: string, citations: Citation[]): AuditTarget[] {
  const targets: AuditTarget[] = [];
  let offset = 0;
  for (const line of text.split("\n")) {
    const lineStart = offset;
    offset += line.length + 1;
    const trimmed = line.trim();
    if (trimmed === "" || trimmed.startsWith("#")) {
      continue;
    }
    const bullet = /^[-*•]\s+/.exec(trimmed);
    const bodyStart = lineStart + line.indexOf(trimmed) + (bullet ? bullet[0].length : 0);
    const body = bullet ? trimmed.slice(bullet[0].length) : trimmed;
    if (body === "") {
      continue;
    }
    let cursor = bodyStart;
    const pieces = bullet ? [body] : body.split(SENTENCE_BREAK);
    for (const piece of pieces) {
      const start = text.indexOf(piece, cursor);
      if (start === -1) {
        continue;
      }
      const end = start + piece.length;
      cursor = end;
      const sentence = piece.trim();
      if (sentence === "") {
        continue;
      }
      targets.push({ sentence, start, end, recordIDs: [] });
    }
  }
  // Citation offsets are runes (code points), string indices are UTF-16
  // units; they diverge only past an astral character, so the map is built
  // only when one exists.
  let toUnits: number[] | undefined;
  if (text.length !== [...text].length) {
    toUnits = [0];
    let units = 0;
    for (const ch of text) {
      units += ch.length;
      toUnits.push(units);
    }
  }
  const unitIndex = (rune: number) =>
    toUnits ? toUnits[Math.max(0, Math.min(rune, toUnits.length - 1))] : rune;
  for (const target of targets) {
    const seen = new Set<string>();
    for (const citation of citations) {
      const recordID = citation.record_id?.trim() ?? "";
      if (!recordID || seen.has(recordID) || isExternalRecordID(recordID)) {
        continue;
      }
      if (unitIndex(citation.start) < target.end && unitIndex(citation.end) > target.start) {
        seen.add(recordID);
        target.recordIDs.push(recordID);
      }
    }
  }
  return targets;
}
