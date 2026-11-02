// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0
//
// The report language selector (GH-670). Translation is a rendition of the
// approved English text, never a re-run of the flow: the brief's sentence
// units go out as an array, come back one-for-one, and are spliced into the
// original markdown at their own offsets — headings, bullets, and blank
// lines stay, and each unit's citations remap to the translated unit's span.
// Nothing metadata-derived (gradings, markings, the banner) passes through
// the model.

import type { Citation } from "./api";
import { auditTargets, type AuditTarget } from "./auditSentences";

export const REPORT_LANGUAGES: Array<{ code: string; label: string; prompt?: string }> = [
  { code: "en", label: "English" },
  { code: "fr", label: "French" },
  { code: "de", label: "German" },
  { code: "pl", label: "Polish" },
  { code: "ro", label: "Romanian" },
  // For the room (GH-680): the same rendition path, with the rules spelled
  // out because the flagship's instinct is to prefix rather than swap.
  {
    code: "pig",
    label: "Pig Latin",
    prompt:
      "Pig Latin (per word: move the leading consonant cluster to the end and add 'ay'; a vowel-initial word just gains 'way'; keep numbers, dates, and identifiers untouched)",
  },
];

// buildSegments returns the brief's translatable units in document order —
// the audit's own segmentation: sentences and bullets, headings untouched
// (they are the conformance template's labels and the form parses by them).
export function buildSegments(text: string, citations: Citation[]): AuditTarget[] {
  return auditTargets(text, citations);
}

// rebuildTranslated splices one translation per unit into the original text
// and emits unit-granular citations with rune offsets, the unit the offsets
// convention citedSpan slices by.
export function rebuildTranslated(
  text: string,
  units: AuditTarget[],
  translations: string[],
): { text: string; citations: Citation[] } {
  if (translations.length !== units.length) {
    throw new Error(
      `translation count mismatch: ${translations.length} translations for ${units.length} segments`,
    );
  }
  const runeLength = (value: string) => [...value].length;
  const parts: string[] = [];
  const citations: Citation[] = [];
  let cursor = 0;
  let outRunes = 0;
  units.forEach((unit, index) => {
    const before = text.slice(cursor, unit.start);
    parts.push(before);
    outRunes += runeLength(before);
    const translated = translations[index].trim() || unit.sentence;
    const start = outRunes;
    parts.push(translated);
    outRunes += runeLength(translated);
    for (const record_id of unit.recordIDs) {
      citations.push({ start, end: outRunes, text: translated, record_id });
    }
    cursor = unit.end;
  });
  parts.push(text.slice(cursor));
  return { text: parts.join(""), citations };
}
