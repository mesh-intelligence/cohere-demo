import { describe, expect, it } from "vitest";

import { deriveAttribution, type ChatResponse, type ChunkMetadata } from "./api";
import {
  citationIndex,
  fillIntsumForm,
  highestClearance,
  parseComposedSections,
  sourceEvaluationRows,
  type ComposedChunkMetadata,
  conformanceDisplay,
  admiraltyGradeIndex,
  citationGrade,
  classificationIndex,
  citationMarking,
} from "./intsumForm";
import { INTSUM_SECTIONS, INTSUM_TEMPLATE_VERSION } from "./intsumTemplate";

// A brief in the shape the deployed composition prompt asks for today --
// Assessment, Key Judgements, Supporting Reporting, Gaps. The template declares
// those headings as aliases, so the form fills from turns the mesh already
// produces rather than only from briefs written to a heading set nothing emits.
const COMPOSED_BRIEF = [
  "# Key Judgements",
  "Russian forces continued strikes against energy infrastructure.",
  "",
  "# Assessment",
  "The December strike series targeted transmission rather than generation.",
  "",
  "# Supporting Reporting",
  "Ukrainian air defence reported intercepts on 16 December.",
  "",
  "# Gaps",
  "Battle damage assessment for the 29 December series is unavailable.",
].join("\n");

function metadata(overrides: Partial<ChunkMetadata> = {}): ChunkMetadata {
  return {
    source_id: "isw-2022-12-16",
    source_type: "osint_assessment",
    clearance: "unclassified",
    admiralty_source_reliability: "B",
    admiralty_information_credibility: 2,
    ...overrides,
  };
}

function answerFrom(
  text: string,
  composedMetadata: ComposedChunkMetadata[][][] = [],
  citations: { start: number; end: number; text: string; record_id: string }[] = [],
) {
  return {
    text,
    sources: ["rag0"],
    citations,
    droppedCitations: [],
    floorDroppedCitations: [],
    grounded: true,
    attribution: { composed: [], attributed: true, reasons: [] },
    composedMetadata,
  };
}

describe("parseComposedSections", () => {
  it("assigns each block to the section declaring its heading", () => {
    const parsed = parseComposedSections(COMPOSED_BRIEF);

    expect(parsed.bodies.get("bluf")).toContain("energy infrastructure");
    expect(parsed.bodies.get("gaps")).toContain("Battle damage assessment");
    expect(parsed.unmatched).toEqual([]);
  });

  it("joins two headings that map to one section rather than keeping the last", () => {
    // Assessment and Supporting Reporting both declare threat-activity. Keeping
    // only the last would drop composed prose the provider wrote.
    const parsed = parseComposedSections(COMPOSED_BRIEF);
    const body = parsed.bodies.get("threat-activity") ?? "";

    expect(body).toContain("targeted transmission");
    expect(body).toContain("air defence reported intercepts");
  });

  it("reports a heading no section declares rather than folding it into one", () => {
    const parsed = parseComposedSections("# Annex B\nsomething the template does not cover");

    expect(parsed.unmatched).toEqual(["Annex B"]);
    expect(parsed.bodies.size).toBe(0);
  });

  it("drops text before the first heading rather than assigning it to a section", () => {
    // Assigning it would be the structure reconstruction srd012 R1.3 forbids.
    const parsed = parseComposedSections("a preamble with no heading\n\n# Gaps\nnone stated");

    expect(parsed.bodies.get("gaps")).toBe("none stated");
    expect([...parsed.bodies.keys()]).toEqual(["gaps"]);
  });

  it("parses no sections from a brief that carries no headings", () => {
    const parsed = parseComposedSections("one paragraph of prose and nothing else");

    expect(parsed.bodies.size).toBe(0);
    expect(parsed.unmatched).toEqual([]);
    expect(parsed.extras).toEqual([]);
  });

  it("keeps an undeclared heading's block as an extra rather than dropping it (GH-577)", () => {
    const parsed = parseComposedSections("# Annex B\nsomething the template does not cover");

    expect(parsed.unmatched).toEqual(["Annex B"]);
    expect(parsed.extras).toEqual([
      { heading: "Annex B", body: "something the template does not cover" },
    ]);
  });

  it("folds a run of undeclared headings into one extra titled by the first (GH-577)", () => {
    // The revision defect's live shape: a provider-added losses section with
    // per-date subheadings, none of them declared by the template.
    const parsed = parseComposedSections(
      [
        "# Gaps",
        "none stated",
        "# Russian Personnel and Equipment Losses",
        "## 2022-12-04",
        "figures for the fourth",
        "## 2022-12-10",
        "figures for the tenth",
      ].join("\n"),
    );

    expect(parsed.bodies.get("gaps")).toBe("none stated");
    expect(parsed.extras).toHaveLength(1);
    expect(parsed.extras[0].heading).toBe("Russian Personnel and Equipment Losses");
    expect(parsed.extras[0].body).toContain("## 2022-12-04");
    expect(parsed.extras[0].body).toContain("figures for the tenth");
    expect(parsed.unmatched).toEqual([
      "Russian Personnel and Equipment Losses",
      "2022-12-04",
      "2022-12-10",
    ]);
  });

  it("ends an extra block at the next declared heading (GH-577)", () => {
    const parsed = parseComposedSections(
      ["# Annex B", "annex prose", "# Outlook", "strikes continue"].join("\n"),
    );

    expect(parsed.extras).toEqual([{ heading: "Annex B", body: "annex prose" }]);
    expect(parsed.bodies.get("outlook")).toBe("strikes continue");
  });
});

describe("highestClearance", () => {
  it("marks the most restrictive composed level, not the requester's", () => {
    const level = highestClearance([
      metadata({ clearance: "unclassified" }),
      metadata({ clearance: "secret" }),
      metadata({ clearance: "confidential" }),
    ]);

    expect(level).toBe("secret");
  });

  it("ranks an unrecognized marking above every declared level", () => {
    // An unknown marking is treated as the most restrictive, never the least,
    // so the banner cannot read lower than the material warrants.
    expect(highestClearance([metadata({ clearance: "secret" }), metadata({ clearance: "cosmic" })])).toBe(
      "cosmic",
    );
  });

  it("returns nothing when no composed source declares a level", () => {
    expect(highestClearance([metadata({ clearance: undefined })])).toBeUndefined();
  });
});

describe("sourceEvaluationRows", () => {
  it("evaluates each source once however many chunks it contributed", () => {
    const rows = sourceEvaluationRows([metadata(), metadata(), metadata({ source_id: "HR-22-010" })]);

    expect(rows.map((row) => row.source)).toEqual(["isw-2022-12-16", "HR-22-010"]);
  });

  it("shows a source carrying no grade as ungraded rather than blank", () => {
    const rows = sourceEvaluationRows([
      metadata({
        source_id: "HR-22-010",
        admiralty_source_reliability: undefined,
        admiralty_information_credibility: undefined,
      }),
    ]);

    expect(rows[0].ungraded).toBe(true);
    expect(rows[0].reliability).toBe("ungraded");
    expect(rows[0].credibility).toBe("ungraded");
  });

  it("carries the declared reliability and credibility of a graded source", () => {
    const rows = sourceEvaluationRows([metadata()]);

    expect(rows[0]).toMatchObject({ reliability: "B", credibility: "2", ungraded: false });
  });

  it("marks a source at the highest clearance among its composed chunks", () => {
    // The banner's most-restrictive rule applied per source (GH-646): a later
    // secret chunk upgrades the row even though the grade came from the first.
    const rows = sourceEvaluationRows([
      metadata({ clearance: "unclassified" }),
      metadata({ clearance: "secret" }),
      metadata({ source_id: "HR-22-010", clearance: "confidential" }),
    ]);

    expect(rows.map((row) => row.classification)).toEqual(["SECRET", "CONFIDENTIAL"]);
  });

  it("shows a source with no declared level as unmarked", () => {
    const rows = sourceEvaluationRows([metadata({ clearance: undefined })]);

    expect(rows[0].classification).toBe("unmarked");
  });
});

describe("citationIndex", () => {
  it("indexes one entry per record however many times the brief cited it", () => {
    // The December demonstration turn reported seventy citations over five
    // records. One node per citation is what this prevents.
    const citations = Array.from({ length: 70 }, (_, index) => ({
      start: index,
      end: index + 1,
      text: `assertion ${index % 5}`,
      record_id: `isw-2022-12-0${index % 5}-medium-900-0002`,
    }));

    const index = citationIndex(citations);

    expect(index).toHaveLength(5);
    expect(index[0].assertions).toEqual(["assertion 0"]);
  });

  it("collects the distinct assertions resting on one record", () => {
    const index = citationIndex([
      { start: 0, end: 1, text: "first", record_id: "chunk-1" },
      { start: 2, end: 3, text: "second", record_id: "chunk-1" },
    ]);

    expect(index).toHaveLength(1);
    expect(index[0].assertions).toEqual(["first", "second"]);
  });

  it("skips a citation carrying no record", () => {
    expect(citationIndex([{ start: 0, end: 1, text: "unresolved", record_id: "" }])).toEqual([]);
  });

  it("marks an external open-source record and leaves internal records unmarked", () => {
    // srd011 R7.3: the external document's identity is its source URL, and the
    // index carries the distinction the form renders as the open-source badge.
    const index = citationIndex([
      { start: 0, end: 1, text: "external assertion", record_id: "https://osint-fixture.invalid/reports/december-2022-strikes" },
      { start: 2, end: 3, text: "internal assertion", record_id: "isw-2022-12-05-medium-900-0002" },
    ]);

    expect(index).toHaveLength(2);
    expect(index[0].external).toBe(true);
    expect(index[1].external).toBe(false);
  });
});

describe("fillIntsumForm", () => {
  it("renders every declared section in template order", () => {
    const form = fillIntsumForm(answerFrom(COMPOSED_BRIEF));

    expect(form.templateVersion).toBe(INTSUM_TEMPLATE_VERSION);
    expect(form.sections.map((section) => section.id)).toEqual(
      [...INTSUM_SECTIONS].sort((a, b) => a.order - b.order).map((section) => section.id),
    );
  });

  it("renders an undeclared heading's block as a filled prose section before the citation index (GH-577)", () => {
    const form = fillIntsumForm(
      answerFrom(`${COMPOSED_BRIEF}\n\n# Russian Personnel and Equipment Losses\ncited loss figures`),
    );
    const ids = form.sections.map((section) => section.id);
    const extra = form.sections.find((section) => section.id === "extra-0");

    expect(extra?.filled).toBe(true);
    expect(extra?.label).toBe("Russian Personnel and Equipment Losses");
    expect(extra?.body).toBe("cited loss figures");
    expect(extra?.source).toBe("composed_markdown");
    expect(ids.indexOf("extra-0")).toBeGreaterThan(ids.indexOf("outlook"));
    expect(ids.indexOf("extra-0")).toBeLessThan(ids.indexOf("citation-index"));
    expect(form.unmatchedHeadings).toContain("Russian Personnel and Equipment Losses");
  });

  it("fills a section only from prose the provider composed under its heading", () => {
    const form = fillIntsumForm(answerFrom(COMPOSED_BRIEF));
    const bluf = form.sections.find((section) => section.id === "bluf");

    expect(bluf?.filled).toBe(true);
    expect(bluf?.body).toContain("energy infrastructure");
  });

  it("leaves a section the provider wrote no heading for empty rather than plausible", () => {
    // Outlook is declared and the deployed prompt does not ask for it, so this
    // is the live shape rather than a contrived one.
    const form = fillIntsumForm(answerFrom(COMPOSED_BRIEF));
    const outlook = form.sections.find((section) => section.id === "outlook");

    expect(outlook?.filled).toBe(false);
    expect(outlook?.body).toBeUndefined();
  });

  it("renders the headings and no content for a turn that composed nothing", () => {
    const form = fillIntsumForm(answerFrom(""));

    expect(form.sections).toHaveLength(INTSUM_SECTIONS.length);
    expect(form.sections.every((section) => section.filled === false)).toBe(true);
  });

  it("renders an empty form for each degraded outcome the mesh distinguishes", () => {
    const degraded: ChatResponse[] = [
      { answer: "", metadata: { sources: { composed: [{ name: "rag0", documents: [[]] }] } } },
      { answer: "", metadata: { sources: { query_failed: [{ name: "rag0" }] } } },
      { answer: "", metadata: { sources: { embedding_model_excluded: [{ name: "rag0" }] } } },
      { answer: "", metadata: { sources: { not_selected: ["rag0"] } } },
    ];

    for (const body of degraded) {
      const attribution = deriveAttribution(body);
      expect(attribution.attributed).toBe(false);

      const form = fillIntsumForm(answerFrom(body.answer ?? ""));
      expect(form.sections.every((section) => section.filled === false)).toBe(true);
    }
  });

  it("marks the banner from the composed sources and never from the requester", () => {
    const form = fillIntsumForm(
      answerFrom(COMPOSED_BRIEF, [[[metadata({ clearance: "unclassified" }), metadata({ clearance: "secret" })]]]),
    );
    const banner = form.sections.find((section) => section.id === "classification-banner");

    expect(banner?.banner).toBe("secret");
  });

  it("never marks a revision lower than the brief it started from (GH-594)", () => {
    // The live shape: a SECRET Patriot brief revised with a losses note whose
    // retrieval composed only unclassified records. The fused prose still
    // carries the SECRET-derived material, so the banner holds.
    const answer = answerFrom(COMPOSED_BRIEF, [[[metadata({ clearance: "unclassified" })]]]);
    const form = fillIntsumForm({ ...answer, priorBanner: "secret" });
    const banner = form.sections.find((section) => section.id === "classification-banner");

    expect(banner?.banner).toBe("secret");
  });

  it("lets a revision's own higher sources outrank the inherited banner", () => {
    const answer = answerFrom(COMPOSED_BRIEF, [[[metadata({ clearance: "secret" })]]]);
    const form = fillIntsumForm({ ...answer, priorBanner: "confidential" });
    const banner = form.sections.find((section) => section.id === "classification-banner");

    expect(banner?.banner).toBe("secret");
  });

  it("fills the header only from context the caller supplied", () => {
    const form = fillIntsumForm(answerFrom(COMPOSED_BRIEF), {
      dateTimeGroup: "161200Z DEC 22",
      tasking: "Strikes on energy infrastructure",
    });
    const header = form.sections.find((section) => section.id === "header");

    expect(header?.fields).toEqual([
      { id: "date_time_group", label: "DTG", value: "161200Z DEC 22" },
      { id: "originator", label: "Originator", value: "" },
      { id: "tasking", label: "Tasking", value: "Strikes on energy infrastructure" },
    ]);
  });

  it("leaves the header empty when the caller supplies no context", () => {
    const form = fillIntsumForm(answerFrom(COMPOSED_BRIEF));
    const header = form.sections.find((section) => section.id === "header");

    expect(header?.filled).toBe(false);
    expect(header?.fields?.every((field) => field.value === "")).toBe(true);
  });

  it("reports a heading the template does not declare", () => {
    const form = fillIntsumForm(answerFrom("# Key Judgements\nstated\n\n# Annex B\nextra"));

    expect(form.unmatchedHeadings).toEqual(["Annex B"]);
  });

  it("produces the same form twice from one answer", () => {
    const answer = answerFrom(COMPOSED_BRIEF, [[[metadata()]]], [
      { start: 0, end: 4, text: "an assertion", record_id: "isw-2022-12-16-medium-900-0002" },
    ]);

    expect(fillIntsumForm(answer)).toEqual(fillIntsumForm(answer));
  });
});

describe("conformanceDisplay", () => {
  it("marks a non-conformant brief as a draft carrying its defects", () => {
    // srd012 R4.1: surfaced with the verdict and defect list, never presented
    // as final -- and never rewritten to pass.
    const display = conformanceDisplay({
      verdict: "non_conformant",
      template_version: "1",
      defects: ["required section Gaps is absent or has no content under its heading"],
      asserts: "structure only",
    });
    expect(display?.tone).toBe("draft");
    expect(display?.line).toContain("draft");
    expect(display?.line).toContain("does not conform");
    expect(display?.line).toContain("v1");
    expect(display?.defects).toHaveLength(1);
  });

  it("states conformance to the template version without approval language", () => {
    // AC5: conformance is a structural fact, not a review. None of the words
    // a reader could mistake for one may appear.
    const display = conformanceDisplay({ verdict: "conformant", template_version: "1" });
    expect(display?.tone).toBe("conformant");
    expect(display?.line).toContain("v1");
    for (const approval of ["approved", "verified", "reviewed", "final"]) {
      expect(display?.line.toLowerCase()).not.toContain(approval);
    }
  });

  it("says the check did not run when the verdict is unchecked", () => {
    // The chain's third honest value (srd012 R4.3): the check broke, the turn
    // and the draft survived, and the reader is told rather than reassured.
    const display = conformanceDisplay({ verdict: "unchecked", template_version: "1" });
    expect(display?.tone).toBe("unchecked");
    expect(display?.line).toContain("did not run");
  });

  it("renders nothing for an absent or unknown verdict", () => {
    // The form's standing rule: nothing invented. A mesh that did not check,
    // or a verdict this contract does not know, shows no conformance claim.
    expect(conformanceDisplay(undefined)).toBeUndefined();
    expect(conformanceDisplay({ verdict: "something-new" })).toBeUndefined();
  });
});

describe("admiraltyGradeIndex (GH-535)", () => {
  it("maps documents to compact grades and resolves chunk ids", () => {
    const metadata = [
      [[{ source_id: "isw-2022-12-16", admiralty_source_reliability: "B", admiralty_information_credibility: 2 }]],
      [[{ source_id: "hr-22-018", admiralty_source_reliability: "F", admiralty_information_credibility: 6 }]],
      [[{ source_id: "ungraded-doc" }]],
    ];
    const grades = admiraltyGradeIndex(metadata);
    expect(citationGrade("isw-2022-12-16-medium-900-0002", grades)).toBe("B2");
    expect(citationGrade("hr-22-018", grades)).toBe("F6");
    expect(citationGrade("ungraded-doc-medium-900-0001", grades)).toBe("");
  });
});

describe("classificationIndex (GH-722)", () => {
  it("maps documents to their highest chunk marking and leaves unclassified absent", () => {
    const metadata = [
      [
        [
          { source_id: "hr-22-018", clearance: "confidential" },
          { source_id: "hr-22-018", clearance: "secret" },
        ],
      ],
      [[{ source_id: "hr-22-014", clearance: "Restricted" }]],
      [[{ source_id: "isw-2022-12-16", clearance: "unclassified" }]],
      [[{ source_id: "unmarked-doc" }]],
    ];
    const markings = classificationIndex(metadata);
    expect(citationMarking("hr-22-018-medium-900-0001", markings)).toBe("secret");
    expect(citationMarking("HR-22-014", markings)).toBe("restricted");
    expect(citationMarking("isw-2022-12-16-medium-900-0002", markings)).toBe("");
    expect(citationMarking("unmarked-doc", markings)).toBe("");
  });
});
