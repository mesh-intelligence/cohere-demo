import { describe, expect, it } from "vitest";

import { citedFraction, citedSourceIDs, flagRevisionSegments, markCitations } from "./citationMarks";

// The marks (GH-382): a reader sees where an assertion's support was made,
// and bare prose means uncited.

describe("markCitations", () => {
  it("marks the cited sentence and leaves the rest plain", () => {
    const body = "Interceptors concentrated around Kyiv. Weather was unremarkable.";
    const segments = markCitations(body, [
      { text: "Interceptors concentrated around Kyiv.", recordID: "isw-2022-12-29" },
    ]);
    expect(segments.map((s) => s.records.length > 0)).toEqual([true, false]);
    expect(segments[0].records).toEqual(["isw-2022-12-29"]);
    expect(segments.map((s) => s.text).join("")).toBe(body);
  });

  it("merges overlapping citations into one mark carrying both records", () => {
    const body = "Russian forces struck energy infrastructure across five oblasts overnight.";
    const segments = markCitations(body, [
      { text: "Russian forces struck energy infrastructure", recordID: "isw-2022-12-29" },
      { text: "energy infrastructure across five oblasts", recordID: "missile-2022-12-29" },
    ]);
    const marked = segments.filter((s) => s.records.length > 0);
    expect(marked).toHaveLength(1);
    expect(marked[0].records.sort()).toEqual(["isw-2022-12-29", "missile-2022-12-29"]);
  });

  it("leaves a body whose citations belong to other sections untouched", () => {
    // A citation marks whichever section holds its text; a section that does
    // not contain it renders plain rather than guessing.
    const body = "There is no information on the period between 23 and 29 December.";
    const segments = markCitations(body, [
      { text: "Ukrainian air defences shot down 54 of the missiles", recordID: "isw-2022-12-29" },
    ]);
    expect(segments).toEqual([{ text: body, records: [] }]);
  });

  it("ignores spans too short to locate unambiguously", () => {
    const segments = markCitations("Kyiv and Lviv were struck.", [
      { text: "Kyiv", recordID: "isw-2022-12-29" },
    ]);
    expect(segments.every((s) => s.records.length === 0)).toBe(true);
  });
});

describe("citedFraction", () => {
  it("weighs cited text against the section's non-whitespace body", () => {
    const segments = markCitations("AAAAA BBBBB CCCCC. DDDDD EEEEE FFFFF GGGGG.", [
      { text: "AAAAA BBBBB CCCCC.", recordID: "r1" },
    ]);
    const fraction = citedFraction(segments);
    expect(fraction).toBeGreaterThan(0.3);
    expect(fraction).toBeLessThan(0.5);
  });
});

describe("citedSourceIDs", () => {
  it("reduces chunk records to the documents the evaluation names", () => {
    // The table lists documents (isw-2022-12-29); citations name chunks
    // (isw-2022-12-29-medium-900-0002). The suffix rule mirrors the
    // evaluation harness's so both agree on same-document (GH-381).
    const cited = citedSourceIDs([
      "isw-2022-12-29-medium-900-0002",
      "missile-2022-12-05-atomic-0001",
      "losses-2022-12-17-large-1800-0003",
    ]);
    expect([...cited].sort()).toEqual([
      "isw-2022-12-29",
      "losses-2022-12-17",
      "missile-2022-12-05",
    ]);
  });

  it("leaves an external record standing for itself", () => {
    const cited = citedSourceIDs(["https://osint-fixture.invalid/reports/december-2022-strikes"]);
    expect(cited.has("https://osint-fixture.invalid/reports/december-2022-strikes")).toBe(true);
  });

  it("collapses several chunks of one document to one source", () => {
    const cited = citedSourceIDs([
      "isw-2022-12-29-medium-900-0001",
      "isw-2022-12-29-medium-900-0002",
    ]);
    expect(cited.size).toBe(1);
  });
});

describe("flagRevisionSegments", () => {
  const prior = "# Assessment\nRussian forces struck energy infrastructure in December 2022.";

  it("flags text absent from the prior brief and keeps carried text plain", () => {
    const body =
      "Russian forces struck energy infrastructure in December 2022. Reported losses rose sharply over the same period.";
    const segments = flagRevisionSegments(markCitations(body, []), prior);

    const flagged = segments.filter((segment) => segment.isNew).map((segment) => segment.text);
    const carried = segments.filter((segment) => !segment.isNew).map((segment) => segment.text);
    expect(flagged.join("")).toContain("Reported losses rose sharply");
    expect(carried.join("")).toContain("struck energy infrastructure");
    expect(segments.map((segment) => segment.text).join("")).toBe(body);
  });

  it("judges re-wrapped carried text as carried, not new", () => {
    const body = "Russian forces struck energy\ninfrastructure in December 2022.";
    const segments = flagRevisionSegments(markCitations(body, []), prior);

    expect(segments.some((segment) => segment.isNew)).toBe(false);
  });

  it("flags a newly cited span whole", () => {
    const body = "Reported losses rose sharply over the same period.";
    const segments = flagRevisionSegments(
      markCitations(body, [
        { text: "Reported losses rose sharply", recordID: "losses-2022-12-17-medium-900-0001" },
      ]),
      prior,
    );
    const cited = segments.find((segment) => segment.records.length > 0);

    expect(cited?.isNew).toBe(true);
  });

  it("flags nothing without a prior brief", () => {
    const segments = flagRevisionSegments(markCitations("Anything at all, entirely new.", []));

    expect(segments.every((segment) => !segment.isNew)).toBe(true);
  });

  it("never flags connective fragments too short to judge", () => {
    const segments = flagRevisionSegments(markCitations("# Gaps\nnone.", []), prior);

    expect(segments.every((segment) => !segment.isNew)).toBe(true);
  });
});
