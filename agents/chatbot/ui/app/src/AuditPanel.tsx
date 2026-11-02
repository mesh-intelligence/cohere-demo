// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0
//
// The audit report table (GH-650): one row per audited sentence, weakest
// first, each source a provenance link so the presenter can read a low
// grade's reason and then show what the chunk actually says.

export interface AuditFinding {
  sentence: string;
  recordIDs: string[];
  // Absent when the sentence carries no citation (unverifiable) or the judge
  // returned no leading number.
  score?: number;
  // The judge's verdict with the leading score stripped for the Reason cell.
  reason: string;
  unverifiable: boolean;
  // The judge run's trace (GH-662), absent on unverifiable rows.
  traceId?: string;
}

export interface AuditReport {
  findings: AuditFinding[];
  scored: number;
  targets: number;
  average: string;
}

// Weakest first: scored rows ascending, then unverifiable rows, ties keeping
// document order (the sort is stable).
function ordered(findings: AuditFinding[]): AuditFinding[] {
  return [...findings].sort((a, b) => {
    if (a.unverifiable !== b.unverifiable) return a.unverifiable ? 1 : -1;
    return (a.score ?? 11) - (b.score ?? 11);
  });
}

export default function AuditPanel({
  report,
  onOpenProvenance,
  onOpenTrace,
}: {
  report: AuditReport;
  onOpenProvenance?: (recordID: string, assertions: string[]) => void;
  onOpenTrace?: (traceId: string) => void;
}) {
  return (
    <div className="audit-panel">
      <h3>Citation Audit</h3>
      <div className="audit-summary">
        {report.scored} of {report.targets} sentence(s) scored, average {report.average}/10 — judge: the
        Cohere classifier tier, each sentence against the chunks it cites, weakest first.
      </div>
      <table className="intsum-table audit-table">
        <thead>
          <tr>
            <th>Sentence</th>
            <th>Sources</th>
            <th>Score</th>
            <th>Reason</th>
          </tr>
        </thead>
        <tbody>
          {ordered(report.findings).map((finding, index) => (
            <tr
              key={`${finding.sentence.slice(0, 40)}:${index}`}
              className={
                finding.unverifiable
                  ? "audit-row-unverifiable"
                  : finding.score !== undefined && finding.score < 8
                    ? "audit-row-weak"
                    : undefined
              }
            >
              <td className="audit-sentence">{finding.sentence}</td>
              <td className="audit-sources">
                {finding.recordIDs.length === 0
                  ? "—"
                  : finding.recordIDs.map((recordID) =>
                      onOpenProvenance ? (
                        <button
                          key={recordID}
                          type="button"
                          className="provenance-link"
                          title="Open the provenance walk: the chunk's text beside this sentence"
                          onClick={() => onOpenProvenance(recordID, [finding.sentence])}
                        >
                          {recordID}
                        </button>
                      ) : (
                        <span key={recordID}>{recordID}</span>
                      ),
                    )}
              </td>
              <td className="audit-score">{finding.unverifiable ? "—" : (finding.score ?? "?")}</td>
              <td className="audit-reason">
                {finding.unverifiable ? "no citation — unverifiable" : finding.reason}
                {finding.traceId && onOpenTrace && (
                  <button
                    type="button"
                    className="bubble-trace audit-trace"
                    title="Open the judge run's trace — the audit prompt and the verdict"
                    onClick={() => onOpenTrace(finding.traceId as string)}
                  >
                    trace
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
