// The right column of the chat route: the ministry INTSUM form the latest
// turn filled (srd016 R3). The panel renders what fillIntsumForm derived and
// adds nothing: every filled field traces to the response or to the stated
// turn context, an empty section renders its heading and says why it is
// empty, and a degraded turn shows a form with no content rather than a brief
// that says nothing (R3.2, R3.3).

import { useState, type ReactNode } from "react";

import { CLEARANCE_LEVELS, citedSpan, isExternalRecordID, type Answer } from "./api";
import {
  citedFraction,
  citedSourceIDs,
  flagRevisionSegments,
  markCitations,
  type SpanCitation,
} from "./citationMarks";
import { conformanceDisplay, fillIntsumForm, type FilledSection, type TurnContext, admiraltyGradeIndex, citationGrade, classificationIndex, citationMarking, higherBanner } from "./intsumForm";

// Why an empty section is empty, by where its content would have come from.
// The two fail differently and the form says which happened (srd016 R2.3
// commentary in the template): a composed section is absent because the
// provider wrote no such heading, a derived one because the turn carried no
// data to derive it from.
function emptyReason(section: FilledSection): string {
  return section.source === "composed_markdown"
    ? "the provider wrote nothing under this heading"
    : "the turn carried nothing to derive this from";
}

function BannerSection({ section }: { section: FilledSection }) {
  if (!section.filled || !section.banner) {
    // An empty banner states that there is nothing to mark, never a level: a
    // default marking would be the plausible fill R3.2 bars.
    return (
      <div className="intsum-banner intsum-banner-empty" title={emptyReason(section)}>
        no composed sources to mark
      </div>
    );
  }
  // The banner marks the document, not the reader: the level is the highest
  // among the composed sources and never the requester's own (R3.6).
  return <div className="intsum-banner">{section.banner.toUpperCase()}</div>;
}

function HeaderSection({ section }: { section: FilledSection }) {
  return (
    <dl className="intsum-header">
      {(section.fields ?? []).map((field) => (
        <div className="intsum-header-field" key={field.id}>
          <dt>{field.label}</dt>
          <dd>{field.value === "" ? <span className="intsum-unset">—</span> : field.value}</dd>
        </div>
      ))}
    </dl>
  );
}

function ProseSection({
  section,
  citations,
  grades,
  markings,
  onOpenProvenance,
  priorText,
}: {
  section: FilledSection;
  citations: SpanCitation[];
  grades: Map<string, string>;
  // Source classification levels, for the cited-span tints; an unclassified
  // source is absent and its spans keep the plain mark.
  markings: Map<string, string>;
  onOpenProvenance?: (recordID: string, assertions: string[]) => void;
  // The brief this revision started from, when the changed-text tint is on
  // (GH-605); undefined renders everything untinted.
  priorText?: string;
}) {
  if (!section.filled || !section.body) {
    return <div className="intsum-section-empty">{emptyReason(section)}</div>;
  }
  // The body is the provider's markdown, shown as the text it is. Rendering it
  // to HTML would restyle prose rel12.2 checks byte-for-byte against the
  // template's markdown representation, and the two must stay one artifact.
  // The marks add no characters: they wrap the spans the response already
  // named, so what a reader copies out is still the composed markdown
  // (GH-382).
  const segments = flagRevisionSegments(markCitations(section.body, citations), priorText);
  const fraction = citedFraction(segments);
  const marked = segments.some((segment) => segment.records.length > 0);
  return (
    <div className="intsum-body">
      {segments.map((segment, index) => {
        // A mark opens the provenance walk for its first internal record
        // (GH-598): the underline promised interaction, and the walk is what
        // it means. External (open-source) records resolve nothing in the
        // corpus, so a span cited only externally keeps the plain mark.
        const walkable = onOpenProvenance
          ? segment.records.find((record) => !isExternalRecordID(record))
          : undefined;
        // The span's tint is the highest classification among its cited
        // sources — the banner's most-restrictive rule, span by span.
        const marking = segment.records.reduce<string | undefined>(
          (level, record) => higherBanner(level, citationMarking(record, markings) || undefined),
          undefined,
        );
        const markClass = [
          "intsum-cited",
          walkable ? "intsum-cited-walkable" : "",
          marking ? `intsum-cited-${marking}` : "",
        ]
          .filter(Boolean)
          .join(" ");
        const citedTitle =
          `cited to ${segment.records.join(", ")}` +
          (marking ? ` — marked ${marking.toUpperCase()}` : "");
        return segment.records.length > 0 ? (
          <span key={index} className={segment.isNew ? "intsum-revision-new" : undefined}>
            <mark
              className={markClass}
              title={walkable ? `${citedTitle} — click to open the provenance walk` : citedTitle}
              {...(walkable
                ? {
                    role: "button",
                    tabIndex: 0,
                    onClick: () => onOpenProvenance?.(walkable, [segment.text]),
                    onKeyDown: (event: React.KeyboardEvent) => {
                      if (event.key === "Enter" || event.key === " ") {
                        event.preventDefault();
                        onOpenProvenance?.(walkable, [segment.text]);
                      }
                    },
                  }
                : {})}
            >
              {segment.text}
            </mark>
            {(() => {
              // The source's Admiralty grade, inline after the cited span
              // (GH-535): unique, joined when a span cites several sources.
              const marks = [
                ...new Set(segment.records.map((record) => citationGrade(record, grades)).filter(Boolean)),
              ];
              return marks.length > 0 ? (
                <sup className="citation-grade" title="Admiralty grade of the cited source(s)">
                  ({marks.join("·")})
                </sup>
              ) : null;
            })()}
          </span>
        ) : (
          <span key={index} className={segment.isNew ? "intsum-revision-new" : undefined}>
            {segment.text}
          </span>
        );
      })}
      {marked && (
        <div className="intsum-cited-note">
          {Math.round(fraction * 100)}% of this section carries a citation
        </div>
      )}
    </div>
  );
}

function SourceEvaluationSection({
  section,
  cited,
}: {
  section: FilledSection;
  // The documents the brief actually cites. The table lists what retrieval
  // composed, which is a larger set, and saying which rows back an assertion
  // is the difference between "19 sources" and "8 of them were used"
  // (GH-381).
  cited: Set<string>;
}) {
  const rows = section.rows ?? [];
  if (!section.filled || rows.length === 0) {
    return <div className="intsum-section-empty">{emptyReason(section)}</div>;
  }
  const citedRows = rows.filter((row) => cited.has(row.source)).length;
  return (
    <>
      <table className="intsum-table">
        <thead>
          <tr>
            <th>Source</th>
            <th>Classification</th>
            <th>Reliability</th>
            <th>Credibility</th>
            <th>Used</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            const used = cited.has(row.source);
            return (
              <tr
                key={row.source}
                className={
                  [row.ungraded ? "intsum-row-ungraded" : "", used ? "" : "intsum-row-uncited"]
                    .filter(Boolean)
                    .join(" ") || undefined
                }
              >
                <td>{row.source}</td>
                <td>{row.classification}</td>
                <td>{row.reliability}</td>
                <td>{row.credibility}</td>
                <td
                  className="intsum-used"
                  title={
                    used
                      ? "This source backs at least one cited assertion."
                      : "Retrieval composed this source; the brief cites nothing from it."
                  }
                >
                  {used ? "cited" : "—"}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <div className="intsum-cited-note">
        cited {citedRows} of {rows.length} composed sources
      </div>
    </>
  );
}

function CitationIndexSection({
  section,
  onOpenProvenance,
}: {
  section: FilledSection;
  onOpenProvenance?: (recordID: string, assertions: string[]) => void;
}) {
  const entries = section.citations ?? [];
  if (!section.filled || entries.length === 0) {
    return <div className="intsum-section-empty">{emptyReason(section)}</div>;
  }
  return (
    <table className="intsum-table">
      <thead>
        <tr>
          <th>Record</th>
          <th>Assertions</th>
        </tr>
      </thead>
      <tbody>
        {entries.map((entry) => (
          <tr key={entry.recordID} className={entry.external ? "intsum-row-external" : undefined}>
            <td className="intsum-record-id">
              {/* An external record is open-source material: it is marked as
                  such, carries no clearance, and never offers the corpus
                  provenance walk, which resolves internal record ids (srd011
                  R7.3, R7.4). Dressing it as internal reporting is exactly
                  what the marking exists to prevent. */}
              {entry.external ? (
                <>
                  <span
                    className="intsum-external-badge"
                    title="Open-source result from the external search branch. Unclearanced; not internal reporting."
                  >
                    OPEN SOURCE
                  </span>{" "}
                  {entry.recordID}
                </>
              ) : onOpenProvenance ? (
                <button
                  type="button"
                  className="provenance-link"
                  title="Open the provenance walk for this record"
                  onClick={() => onOpenProvenance(entry.recordID, entry.assertions)}
                >
                  {entry.recordID}
                </button>
              ) : (
                entry.recordID
              )}
            </td>
            <td>
              {entry.assertions.length === 0 ? (
                <span className="intsum-unset">cited without a quoted span</span>
              ) : (
                <ul className="intsum-assertions">
                  {entry.assertions.map((assertion, index) => (
                    <li key={index}>{assertion}</li>
                  ))}
                </ul>
              )}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function sectionBody(
  section: FilledSection,
  citations: SpanCitation[],
  cited: Set<string>,
  grades: Map<string, string>,
  markings: Map<string, string>,
  onOpenProvenance?: (recordID: string, assertions: string[]) => void,
  priorText?: string,
): ReactNode {
  switch (section.id) {
    case "header":
      return <HeaderSection section={section} />;
    case "source-evaluation":
      return <SourceEvaluationSection section={section} cited={cited} />;
    case "citation-index":
      return <CitationIndexSection section={section} onOpenProvenance={onOpenProvenance} />;
    default:
      return (
        <ProseSection
          section={section}
          citations={citations}
          grades={grades}
          markings={markings}
          onOpenProvenance={onOpenProvenance}
          priorText={priorText}
        />
      );
  }
}

export default function IntsumPanel({
  answer,
  context,
  onOpenProvenance,
}: {
  answer?: Answer;
  context: TurnContext;
  // Present when a provenance walk is available: the citation index's record
  // identifiers become the entry into it (srd016 R6.1).
  onOpenProvenance?: (recordID: string, assertions: string[]) => void;
}) {
  // Whether a revision's changed text is tinted (GH-605). On by default; the
  // toggle shows only when the answer carries the brief it revised.
  const [showChanges, setShowChanges] = useState(true);
  if (!answer) {
    return (
      <div className="intsum-empty">
        No INTSUM yet. Send a tasking and the form fills from what the turn composed.
      </div>
    );
  }

  const form = fillIntsumForm(answer, context);
  // Each citation's own text, resolved through the same rune-safe slice the
  // chat bubbles use, is what the marks locate in a section body.
  const citedSources = citedSourceIDs(answer.citations.map((citation) => citation.record_id));
  const grades = admiraltyGradeIndex(answer.composedMetadata);
  const markings = classificationIndex(answer.composedMetadata);
  // The tint legend lists each classified level among the composed sources,
  // lowest first; an all-unclassified brief renders no legend and no tints.
  const legendLevels = CLEARANCE_LEVELS.filter((level) =>
    [...markings.values()].includes(level),
  );
  const spanCitations: SpanCitation[] = answer.citations.map((citation) => ({
    text: citedSpan(answer.text, citation),
    recordID: citation.record_id,
  }));
  const banner = form.sections.find((section) => section.id === "classification-banner");
  const sections = form.sections.filter((section) => section.id !== "classification-banner");
  const conformance = conformanceDisplay(answer.conformance);

  return (
    <div className="intsum-form">
      {banner && <BannerSection section={banner} />}
      {legendLevels.length > 0 && (
        <div
          className="intsum-marking-legend"
          title="Cited text is tinted by the highest classification among its cited sources; text from unclassified sources carries no tint."
        >
          {legendLevels.map((level) => (
            <span key={level} className={`intsum-marking-chip intsum-marking-chip-${level}`}>
              {level.toUpperCase()}
            </span>
          ))}
        </div>
      )}
      {conformance && (
        <div
          className={`intsum-conformance intsum-conformance-${conformance.tone}`}
          title={conformance.asserts}
        >
          <span className="intsum-conformance-line">{conformance.line}</span>
          {conformance.defects.length > 0 && (
            <ul className="intsum-conformance-defects">
              {conformance.defects.map((defect, index) => (
                <li key={index}>{defect}</li>
              ))}
            </ul>
          )}
        </div>
      )}
      {answer.priorText && (
        <button
          type="button"
          className={`intsum-changes-toggle${showChanges ? " intsum-changes-toggle-on" : ""}`}
          title="Tint the text this revision changed against the brief it started from"
          onClick={() => setShowChanges((was) => !was)}
        >
          {showChanges ? "revision changes: shown" : "revision changes: hidden"}
        </button>
      )}
      {sections.map((section) => (
        <section className="intsum-section" key={section.id}>
          <h3 className="intsum-heading">{section.label}</h3>
          {sectionBody(
            section,
            spanCitations,
            citedSources,
            grades,
            markings,
            onOpenProvenance,
            showChanges ? answer.priorText : undefined,
          )}
        </section>
      ))}
      {answer.floorDroppedCitations.length > 0 && (
        <div
          className="intsum-floor-dropped"
          title="Cited spans withheld because their source sits above the reader's clearance. The assertions remain in the brief; their citations are withheld from release."
        >
          <span className="intsum-floor-dropped-count">
            {answer.floorDroppedCitations.length} citation
            {answer.floorDroppedCitations.length === 1 ? "" : "s"} withheld above the reader's clearance
          </span>
          <ul>
            {answer.floorDroppedCitations.map((entry, index) => (
              <li key={index}>{entry.text}</li>
            ))}
          </ul>
        </div>
      )}
      {form.unmatchedHeadings.length > 0 && (
        <div className="intsum-unmatched" title="Headings the provider wrote that the template does not declare">
          Outside the template: {form.unmatchedHeadings.join(", ")}
        </div>
      )}
    </div>
  );
}
