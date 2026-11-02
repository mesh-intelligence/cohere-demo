import { describe, expect, it } from "vitest";

import type { DocumentView } from "./documentsApi";
import { isRawSourceRef, chunkCounterLabel, envelopeOf, locateChunk, versionsByProfile } from "./provenance";

function chunkView(
  id: string,
  profile: string,
  position: number,
  text = "",
): DocumentView {
  return {
    id,
    kind: "chunk",
    chunkProfile: profile,
    text,
    parent: { document_id: "DOC-1", position, sibling_count: 3 },
    derivedFrom: [], references: [],
  };
}

describe("locateChunk", () => {
  it("trusts the char span when it selects the chunk text", () => {
    const documentText = "alpha beta gamma";
    expect(locateChunk(documentText, "beta", [6, 10])).toEqual({ start: 6, end: 10 });
  });

  it("falls back to searching when the span indexes the searchable body, not the full document", () => {
    // The full document carries unsearchable segments ahead of the chunk
    // (srd016 R6.2), so the span computed over the searchable body lands
    // early. The chunk text itself decides where the highlight goes (R6.3).
    const documentText = "SECRET//NOFORN\n\nalpha beta gamma";
    const location = locateChunk(documentText, "beta", [6, 10]);
    expect(location).toEqual({ start: 22, end: 26 });
    expect(documentText.slice(location!.start, location!.end)).toBe("beta");
  });

  it("returns nothing for a passage it cannot locate, rather than highlighting the wrong lines", () => {
    expect(locateChunk("alpha beta", "gamma", [0, 5])).toBeUndefined();
    expect(locateChunk("", "gamma")).toBeUndefined();
    expect(locateChunk("alpha", "")).toBeUndefined();
  });

  it("locates a chunk whose pieces are joined by blank lines the document never wrote", () => {
    // The assessment tier writes its whole body as one line; the chunker cuts
    // it into pieces joined with "\n\n". Non-whitespace content is identical,
    // so the match collapses whitespace on both sides and maps back.
    const documentText = "Byline here. Russian forces continued attacks near Bakhmut. Ukrainian forces struck a bridge. Note at the end.";
    const chunk = "Russian forces continued attacks near Bakhmut.\n\nUkrainian forces struck a bridge.";
    const location = locateChunk(documentText, chunk, [0, 40]);
    expect(location).toBeDefined();
    expect(documentText.slice(location!.start, location!.end)).toBe(
      "Russian forces continued attacks near Bakhmut. Ukrainian forces struck a bridge.",
    );
  });

  it("collapses non-breaking spaces the same as ordinary ones", () => {
    const documentText = "Fighting continued\u00a0near the city. More prose after.";
    const chunk = "Fighting continued near\u00a0the city.";
    const location = locateChunk(documentText, chunk);
    expect(location).toBeDefined();
    expect(documentText.slice(location!.start, location!.end)).toBe(
      "Fighting continued\u00a0near the city.",
    );
  });
});

describe("versionsByProfile", () => {
  it("groups chunk rows by profile in position order and leaves the envelope out", () => {
    const envelope: DocumentView = {
      id: "DOC-1#envelope",
      kind: "envelope",
      text: "whole document",
      derivedFrom: [], references: [],
    };
    const grouped = versionsByProfile([
      envelope,
      chunkView("DOC-1-medium-0002", "medium", 2),
      chunkView("DOC-1-atomic-0001", "atomic", 1),
      chunkView("DOC-1-medium-0001", "medium", 1),
    ]);
    expect(grouped.map((entry) => entry.profile)).toEqual(["atomic", "medium"]);
    expect(grouped[1].chunks.map((chunk) => chunk.id)).toEqual([
      "DOC-1-medium-0001",
      "DOC-1-medium-0002",
    ]);
  });

  it("answers no versions for a read that served none", () => {
    expect(versionsByProfile([])).toEqual([]);
  });
});

describe("envelopeOf", () => {
  it("picks the document's envelope row by its identifier convention", () => {
    const envelope: DocumentView = {
      id: "DOC-1#envelope",
      kind: "envelope",
      text: "whole document",
      derivedFrom: [], references: [],
    };
    expect(envelopeOf([chunkView("DOC-1-atomic-0001", "atomic", 1), envelope], "DOC-1")).toBe(envelope);
    expect(envelopeOf([chunkView("DOC-1-atomic-0001", "atomic", 1)], "DOC-1")).toBeUndefined();
  });
});

describe("chunkCounterLabel", () => {
  it("renders the store's 1-based position without shifting it", () => {
    // "chunk 2 of 1" on a single-chunk document was the +1 doing the shifting
    // (GH-350): the first chunk is position 1 and renders as chunk 1.
    expect(chunkCounterLabel({ document_id: "DOC-1", position: 1, sibling_count: 1 })).toBe(
      "chunk 1 of 1",
    );
    expect(chunkCounterLabel({ document_id: "DOC-1", position: 2, sibling_count: 3 })).toBe(
      "chunk 2 of 3",
    );
  });

  it("says nothing when either field is absent", () => {
    expect(chunkCounterLabel(undefined)).toBeUndefined();
    expect(chunkCounterLabel({ document_id: "DOC-1", position: 1 })).toBeUndefined();
    expect(chunkCounterLabel({ document_id: "DOC-1", sibling_count: 2 })).toBeUndefined();
  });
});

describe("isRawSourceRef", () => {
  it("recognizes a dataset file-and-line reference", () => {
    expect(isRawSourceRef("missile_attacks_daily.csv:3989")).toBe(true);
    expect(isRawSourceRef("losses.json:12")).toBe(true);
  });

  it("leaves served document identifiers walkable", () => {
    expect(isRawSourceRef("missile-2022-12-30")).toBe(false);
    expect(isRawSourceRef("HR-22-019")).toBe(false);
    expect(isRawSourceRef("isw-2022-12-16")).toBe(false);
  });
});
