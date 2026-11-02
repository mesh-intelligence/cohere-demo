import { describe, expect, it } from "vitest";

import { exportDocument } from "./exportDocument";
import type { Answer, ChunkMetadata } from "./api";
import type { ComposedChunkMetadata } from "./intsumForm";

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
): Answer {
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

describe("exportDocument", () => {
  const brief = "# Gaps\nBattle damage assessment is unavailable.";

  it("wraps the composed prose with the banner and appends the derived sections", () => {
    const doc = exportDocument(
      answerFrom(
        brief,
        [[[metadata({ source_id: "isw-2022-12-16", clearance: "secret" })]]],
        [
          {
            start: 8,
            end: 20,
            text: "Battle damage",
            record_id: "isw-2022-12-16-medium-900-0001",
          },
        ],
      ),
    );

    expect(doc.startsWith("**SECRET**")).toBe(true);
    expect(doc.trim().endsWith("**SECRET**")).toBe(true);
    expect(doc).toContain(brief);
    expect(doc).toContain("# Source Evaluation");
    expect(doc).toContain("| isw-2022-12-16 | SECRET | B | 2 | cited |");
    expect(doc).toContain("# Citation Index");
    expect(doc).toContain("- **isw-2022-12-16-medium-900-0001**");
    expect(doc).toContain("    - Battle damage");
  });

  it("keeps a revision's inherited banner (GH-594) in the export", () => {
    const doc = exportDocument({
      ...answerFrom(brief, [[[metadata({ clearance: "unclassified" })]]]),
      priorBanner: "secret",
    });

    expect(doc.startsWith("**SECRET**")).toBe(true);
  });

  it("marks a composed-but-uncited source and an external citation as such", () => {
    const doc = exportDocument(
      answerFrom(
        brief,
        [[[metadata({ source_id: "isw-2022-12-05" })]]],
        [{ start: 0, end: 4, text: "Gaps span", record_id: "https://example.org/report" }],
      ),
    );

    expect(doc).toContain("| isw-2022-12-05 | UNCLASSIFIED | B | 2 | — |");
    expect(doc).toContain("- **https://example.org/report (open source)**");
  });

  it("renders the header fields the caller supplied", () => {
    const doc = exportDocument(answerFrom(brief), {
      dateTimeGroup: "161200Z DEC 22",
      originator: "DEFTECH",
      tasking: "December strikes",
    });

    expect(doc).toContain("DTG: 161200Z DEC 22");
    expect(doc).toContain("Originator: DEFTECH");
    expect(doc).toContain("Tasking: December strikes");
  });

  it("is just the prose when the turn derived nothing", () => {
    const doc = exportDocument(answerFrom(brief));

    expect(doc).toBe(brief);
  });
});
