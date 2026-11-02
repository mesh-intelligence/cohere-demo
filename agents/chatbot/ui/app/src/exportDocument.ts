// The export document (GH-599): what /export sends to the PDF renderer.
//
// The relay renders GFM through pandoc, and it used to receive only the
// composed markdown — so the PDF missed everything the form derives: the
// classification banner, the header, the source-evaluation table, and the
// citation index. The deliverable is the form, so the export document is
// built from the same derivations the panel renders, appended below the
// composed sections in the order the form shows them.

import type { Answer } from "./api";
import {
  citationIndex,
  composedChunkMetadata,
  highestClearance,
  higherBanner,
  sourceEvaluationRows,
  type TurnContext,
} from "./intsumForm";
import { citedSourceIDs } from "./citationMarks";

// A cell or list line must not break the GFM structure it sits in.
function flat(text: string): string {
  return text.replace(/\|/g, "\\|").replace(/\s+/g, " ").trim();
}

export function exportDocument(answer: Answer, context: TurnContext = {}): string {
  const parts: string[] = [];

  const metadata = composedChunkMetadata(answer);
  const banner = higherBanner(highestClearance(metadata), answer.priorBanner);
  if (banner) {
    parts.push(`**${banner.toUpperCase()}**`);
  }

  const header: string[] = [];
  if (context.dateTimeGroup) header.push(`DTG: ${context.dateTimeGroup}`);
  if (context.originator) header.push(`Originator: ${context.originator}`);
  if (context.tasking) header.push(`Tasking: ${context.tasking}`);
  if (header.length > 0) {
    parts.push(header.join("  \n"));
  }

  // The composed brief, byte for byte — the prose the analyst approved.
  parts.push(answer.text.trim());

  const cited = citedSourceIDs(answer.citations.map((citation) => citation.record_id));
  const rows = sourceEvaluationRows(metadata);
  if (rows.length > 0) {
    const table = [
      "| Source | Classification | Reliability | Credibility | Used |",
      "| --- | --- | --- | --- | --- |",
      ...rows.map(
        (row) =>
          `| ${flat(row.source)} | ${flat(row.classification)} | ${flat(row.reliability)} | ${flat(row.credibility)} | ${
            cited.has(row.source) ? "cited" : "—"
          } |`,
      ),
    ].join("\n");
    parts.push(`# Source Evaluation\n\n${table}`);
  }

  const entries = citationIndex(answer.citations);
  if (entries.length > 0) {
    const lines = entries.map((entry) => {
      const label = entry.external ? `${entry.recordID} (open source)` : entry.recordID;
      const assertions = entry.assertions.map((assertion) => `    - ${flat(assertion)}`);
      return [`- **${flat(label)}**`, ...assertions].join("\n");
    });
    parts.push(`# Citation Index\n\n${lines.join("\n")}`);
  }

  if (banner) {
    parts.push(`**${banner.toUpperCase()}**`);
  }

  return parts.join("\n\n");
}
