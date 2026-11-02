import { describe, expect, it } from "vitest";

import { gradesAtOrAbove,
  MAX_PRIOR_RECORD_IDS,
  revisionFields,
  CLEARANCE_HEADER,
  CLEARANCE_LEVELS,
  DEFAULT_CLEARANCE,
  EMPTY_FACETS,
  citedSpan,
  deriveAttribution,
  isExternalRecordID,
  facetsPayload,
  sendChat,
  clearanceRank,
  decodeBase64,
  exportBrief,
  EXPORT_ENDPOINT,
  ExportError,
  type ChatResponse,
} from "./api";

function outcome(name: string, documents: string[][] = []) {
  return { name, signal: "QueryResponded", documents };
}

function response(sources: NonNullable<NonNullable<ChatResponse["metadata"]>["sources"]>): ChatResponse {
  return { answer: "an answer", metadata: { sources } };
}

describe("deriveAttribution", () => {
  it("attributes a turn whose composed sources carry documents", () => {
    const result = deriveAttribution(
      response({
        composed: [outcome("rag0", [["chunk one", "chunk two"]]), outcome("rag1", [["chunk three"]])],
        not_selected: [],
        embedding_model_excluded: [],
        query_failed: [],
      }),
    );
    expect(result.attributed).toBe(true);
    expect(result.reasons).toEqual([]);
    expect(result.composed).toEqual([
      { name: "rag0", documents: 2 },
      { name: "rag1", documents: 1 },
    ]);
  });

  // The failure the whole change exists to catch. A rag-server bound to the
  // empty collection it opened at startup answers the query and takes part in
  // composition having retrieved nothing, and no outcome list reports a fault.
  it("refuses a turn that composed sources but retrieved no documents", () => {
    const result = deriveAttribution(response({ composed: [outcome("rag0", [[]])] }));
    expect(result.attributed).toBe(false);
    expect(result.composed).toEqual([{ name: "rag0", documents: 0 }]);
    expect(result.reasons).toEqual([{ kind: "no-documents", sources: ["rag0"] }]);
  });

  it("reports a query that failed", () => {
    const result = deriveAttribution(
      response({ composed: [], query_failed: [outcome("rag1")] }),
    );
    expect(result.attributed).toBe(false);
    expect(result.reasons).toEqual([
      { kind: "none-composed" },
      { kind: "query-failed", sources: ["rag1"] },
    ]);
  });

  it("reports an embedding-model mismatch", () => {
    const result = deriveAttribution(
      response({ composed: [], embedding_model_excluded: [outcome("rag0")] }),
    );
    expect(result.attributed).toBe(false);
    expect(result.reasons).toContainEqual({
      kind: "embedding-model-excluded",
      sources: ["rag0"],
    });
  });

  it("reports sources the router never selected", () => {
    const result = deriveAttribution(
      response({ composed: [], not_selected: ["rag0", "rag1"] }),
    );
    expect(result.attributed).toBe(false);
    expect(result.reasons).toContainEqual({ kind: "not-selected", sources: ["rag0", "rag1"] });
  });

  it("reports every populated outcome, not only the first", () => {
    const result = deriveAttribution(
      response({
        composed: [outcome("rag0", [[]])],
        query_failed: [outcome("rag1")],
        not_selected: ["rag2"],
      }),
    );
    expect(result.reasons.map((reason) => reason.kind)).toEqual([
      "no-documents",
      "query-failed",
      "not-selected",
    ]);
  });

  it("treats a response with no metadata as unattributed rather than throwing", () => {
    const result = deriveAttribution({ answer: "an answer" });
    expect(result.attributed).toBe(false);
    expect(result.composed).toEqual([]);
    expect(result.reasons).toEqual([{ kind: "none-composed" }]);
  });

  it("names an unnamed composed source by its position", () => {
    const result = deriveAttribution(
      response({ composed: [{ name: "  " }] }),
    );
    expect(result.composed).toEqual([{ name: "source 0", documents: 0 }]);
  });

  it("sums documents across every query a source answered", () => {
    const result = deriveAttribution(
      response({ composed: [outcome("rag0", [["one", "two"], ["three"]])] }),
    );
    expect(result.attributed).toBe(true);
    expect(result.composed).toEqual([{ name: "rag0", documents: 3 }]);
  });
});

describe("isExternalRecordID", () => {
  // The URL shape is the provenance discriminator (srd011 R7.3): external
  // documents carry their source URL as identity, internal record ids never
  // parse as web URLs.
  it("marks http and https record ids as external", () => {
    expect(isExternalRecordID("https://osint-fixture.invalid/reports/december-2022-strikes")).toBe(true);
    expect(isExternalRecordID("http://example.org/a")).toBe(true);
    expect(isExternalRecordID("  HTTPS://mixed.example/case ")).toBe(true);
  });

  it("keeps internal corpus record ids internal", () => {
    expect(isExternalRecordID("isw-2022-12-05-medium-900-0002")).toBe(false);
    expect(isExternalRecordID("")).toBe(false);
    expect(isExternalRecordID("httpsish-record-id")).toBe(false);
  });
});

describe("citedSpan", () => {
  // The provider reports start and end as rune offsets. JavaScript indexes by
  // UTF-16 code unit, so an answer that is pure ASCII cannot tell the two
  // apart -- which is exactly why a byte- or code-unit-sliced implementation
  // passes every test written without one of these characters and highlights
  // the wrong text on the first brief that has one.
  it("slices a span by rune when the answer carries a non-BMP character", () => {
    const answer = "A 🛰 satellite passed over the site at dawn.";
    // Rune offsets: the satellite is one rune, two UTF-16 code units.
    const runes = Array.from(answer);
    const start = runes.indexOf("s");
    const end = start + "satellite".length;
    expect(citedSpan(answer, { start, end, text: "satellite", record_id: "rec-1" })).toBe(
      "satellite",
    );
    // The naive slice is off, which is the bug this guards.
    expect(answer.slice(start, end)).not.toBe("satellite");
  });

  it("slices a plain ASCII span", () => {
    const answer = "The corpus describes the rig subject.";
    expect(citedSpan(answer, { start: 4, end: 10, text: "corpus", record_id: "rec-1" })).toBe(
      "corpus",
    );
  });

  // A span the answer cannot contain means the provider and the text disagree.
  // The reader still sees what was cited rather than an exception or a truncation.
  it("falls back to the citation's own text when the span is out of range", () => {
    const answer = "Short.";
    expect(citedSpan(answer, { start: 2, end: 99, text: "carried text", record_id: "rec-1" })).toBe(
      "carried text",
    );
  });

  it("falls back when the span is inverted", () => {
    const answer = "The corpus describes the rig subject.";
    expect(citedSpan(answer, { start: 10, end: 4, text: "carried text", record_id: "rec-1" })).toBe(
      "carried text",
    );
  });
});

describe("sendChat clearance", () => {
  function stubFetch(body: ChatResponse) {
    const calls: { headers: Record<string, string>; body: string }[] = [];
    const fake = async (_url: string, init: RequestInit) => {
      calls.push({
        headers: init.headers as Record<string, string>,
        body: init.body as string,
      });
      return { ok: true, status: 200, json: async () => body } as Response;
    };
    globalThis.fetch = fake as unknown as typeof fetch;
    return calls;
  }

  it("sends the analyst clearance as a header rather than a body field", async () => {
    // A body field is a value the client composes, and a front end that fills it
    // honestly does not stop a client that does not. The distinction is not
    // where the value comes from in the happy path but who can supply it in the
    // adversarial one (srd010 R3.1).
    const calls = stubFetch({ answer: "a brief" });
    await sendChat({ message: "a tasking", history: [] }, undefined, "confidential");
    expect(calls[0].headers[CLEARANCE_HEADER]).toBe("confidential");
    expect(calls[0].body).not.toContain("confidential");
  });

  it("defaults to the lowest declared level", async () => {
    const calls = stubFetch({ answer: "a brief" });
    await sendChat({ message: "a tasking", history: [] });
    expect(calls[0].headers[CLEARANCE_HEADER]).toBe(DEFAULT_CLEARANCE);
    expect(DEFAULT_CLEARANCE).toBe(CLEARANCE_LEVELS[0]);
  });

  it("reports the level the server enforced, not the one that was asked for", async () => {
    // They differ whenever the requested level is not one the deployment
    // declares, and what a reader needs is the level that actually applied.
    stubFetch({ answer: "a brief", metadata: { clearance_enforced_at: "unclassified" } });
    const answer = await sendChat({ message: "a tasking", history: [] }, undefined, "secret");
    expect(answer.clearanceEnforcedAt).toBe("unclassified");
  });

  it("derives nothing about what the filter withheld", async () => {
    // srd010 R5.3. The count is the disclosure: an analyst who can vary the
    // tasking and read a suppressed count maps the restricted corpus without
    // reading any of it. Nothing on the Answer may carry one.
    stubFetch({
      answer: "a brief",
      metadata: {
        clearance_enforced_at: "unclassified",
        sources: { composed: [{ name: "rag0", documents: [["one chunk"]] }] },
      },
    });
    const answer = await sendChat({ message: "a tasking", history: [] });
    const fields = JSON.stringify(answer).toLowerCase();
    for (const leak of ["excluded", "withheld", "suppressed", "hidden"]) {
      expect(fields).not.toContain(leak);
    }
  });
});

describe("facetsPayload", () => {
  it("renders no facets field at all for an empty selection", () => {
    // srd016 R4.4: the unfaceted turn is the request it has always been, not a
    // request carrying empty facet fields for the mesh to branch around.
    expect(facetsPayload(EMPTY_FACETS)).toBeUndefined();
    expect(facetsPayload({ keyword: "   ", sourceTypes: [], reliabilities: [] })).toBeUndefined();
  });

  it("states the absent metadata constraint as a no-op conjunct on a keyword-only turn", () => {
    // The chat boundary requires both members together, and the store rejects
    // a conjunction with an empty operand, so the absence is stated rather
    // than omitted: a conjunct every record satisfies.
    expect(facetsPayload({ keyword: "artillery", sourceTypes: [], reliabilities: [] })).toEqual({
      facet_where: { chunk_id: { $ne: "" } },
      keyword: "artillery",
    });
  });

  it("renders a single selection as its bare conjunct and two as a conjunction", () => {
    expect(facetsPayload({ keyword: "", sourceTypes: ["humint"], reliabilities: [] })).toEqual({
      facet_where: { source_type: { $in: ["humint"] } },
      keyword: "",
    });
    expect(
      facetsPayload({ keyword: "strike", sourceTypes: ["humint", "osint_news"], reliabilities: ["A", "B"] }),
    ).toEqual({
      facet_where: {
        $and: [
          { source_type: { $in: ["humint", "osint_news"] } },
          { admiralty_source_reliability: { $in: ["A", "B"] } },
        ],
      },
      keyword: "strike",
    });
  });

  it("never composes a clearance conjunct", () => {
    // srd016 R4.3: the clearance window is conjoined at the retrieval tier
    // from its own resolved declaration. A clearance term here would be a
    // client composing its own enforcement.
    const payload = facetsPayload({ keyword: "kw", sourceTypes: ["humint"], reliabilities: ["A"] });
    expect(JSON.stringify(payload)).not.toContain("clearance");
  });
});

describe("sendChat facets", () => {
  function stubFetch(body: ChatResponse) {
    const calls: { body: string }[] = [];
    const fake = async (_url: string, init: RequestInit) => {
      calls.push({ body: init.body as string });
      return { ok: true, status: 200, json: async () => body } as Response;
    };
    globalThis.fetch = fake as unknown as typeof fetch;
    return calls;
  }

  it("keeps the unfaceted request byte-for-byte free of facets", async () => {
    const calls = stubFetch({ answer: "a brief" });
    await sendChat({ message: "a tasking", history: [], facets: facetsPayload(EMPTY_FACETS) });
    expect(calls[0].body).toBe(JSON.stringify({ message: "a tasking", history: [] }));
  });

  it("carries the facet payload in the body on a faceted turn", async () => {
    // Facets are body fields on purpose, unlike the clearance: they are
    // constraints the client legitimately composes, and the retrieval tier
    // conjoins them under a window nothing in the body can influence.
    const calls = stubFetch({ answer: "a brief" });
    const facets = facetsPayload({ keyword: "strike", sourceTypes: ["humint"], reliabilities: [] });
    await sendChat({ message: "a tasking", history: [], facets });
    const sent = JSON.parse(calls[0].body) as { facets?: unknown };
    expect(sent.facets).toEqual({
      facet_where: { source_type: { $in: ["humint"] } },
      keyword: "strike",
    });
  });
});

describe("sendChat conformance", () => {
  function stubFetch(body: ChatResponse) {
    const fake = async () => ({ ok: true, status: 200, json: async () => body }) as Response;
    globalThis.fetch = fake as unknown as typeof fetch;
  }

  it("carries the structural verdict to the Answer", async () => {
    stubFetch({
      answer: "a brief",
      metadata: {
        conformance: { verdict: "non_conformant", template_version: "1", defects: ["a defect"] },
      },
    });
    const answer = await sendChat({ message: "a tasking", history: [] });
    expect(answer.conformance?.verdict).toBe("non_conformant");
    expect(answer.conformance?.defects).toEqual(["a defect"]);
  });

  it("leaves the verdict absent when the mesh reported none", async () => {
    stubFetch({ answer: "a brief" });
    const answer = await sendChat({ message: "a tasking", history: [] });
    expect(answer.conformance).toBeUndefined();
  });
});

describe("exportBrief", () => {
  function stubExportFetch(status: number, body: unknown) {
    const calls: { url: string; body: string }[] = [];
    const fake = async (url: string, init: RequestInit) => {
      calls.push({ url, body: init.body as string });
      return { ok: status < 400, status, json: async () => body } as Response;
    };
    globalThis.fetch = fake as unknown as typeof fetch;
    return calls;
  }

  it("sends the document alone and decodes the rendered PDF", async () => {
    // The request carries schema_version and content and nothing else: no
    // clearance, identity, or history reaches the exporter (srd013 R3.5).
    const calls = stubExportFetch(200, {
      status: "rendered",
      pdf_base64: btoa("%PDF-1.5 body"),
    });
    const bytes = await exportBrief("# Brief\n\nBody.\n");
    expect(new TextDecoder().decode(bytes)).toBe("%PDF-1.5 body");
    expect(calls[0].url).toBe(EXPORT_ENDPOINT);
    expect(JSON.parse(calls[0].body)).toEqual({
      schema_version: "1",
      content: "# Brief\n\nBody.\n",
    });
  });

  it("throws the endpoint's stated error on a failed render", async () => {
    // A failed render returns no partial document (srd013 R5.2), and the
    // panel surfaces the stated cause rather than a decoded fragment.
    stubExportFetch(500, { status: "failed", error: "export_failed" });
    await expect(exportBrief("# Brief\n")).rejects.toThrow("export_failed");
  });

  it("refuses a 200 that carries no document", async () => {
    stubExportFetch(200, { status: "rendered" });
    await expect(exportBrief("# Brief\n")).rejects.toThrow(ExportError);
  });
});

describe("decodeBase64", () => {
  it("round-trips bytes through the encoder's alphabet", () => {
    const bytes = decodeBase64(btoa("\x00\x01binary\xff"));
    expect(Array.from(bytes.slice(0, 2))).toEqual([0, 1]);
    expect(bytes.length).toBe(9);
  });
});

describe("gradesAtOrAbove", () => {
  it("admits the floor letter and everything better", () => {
    // "At or above B" is A and B: the Admiralty letters order best-first and
    // the floor is inclusive (GH-357).
    expect(gradesAtOrAbove("B")).toEqual(["A", "B"]);
    expect(gradesAtOrAbove("A")).toEqual(["A"]);
    expect(gradesAtOrAbove("F")).toEqual(["A", "B", "C", "D", "E", "F"]);
  });

  it("returns nothing for an unstated floor", () => {
    expect(gradesAtOrAbove("")).toEqual([]);
  });
});

describe("floor-dropped citations", () => {
  it("decodes the withheld list and defaults it empty", async () => {
    const stub = (body: ChatResponse) => {
      globalThis.fetch = (async () =>
        ({ ok: true, status: 200, json: async () => body }) as Response) as unknown as typeof fetch;
    };
    stub({
      answer: "a brief",
      metadata: {
        floor_dropped_citations: [{ start: 0, end: 4, text: "span", reason: "below-release-grade" }],
      },
    });
    const withheld = await sendChat({ message: "t", history: [] });
    expect(withheld.floorDroppedCitations).toHaveLength(1);
    expect(withheld.floorDroppedCitations[0].reason).toBe("below-release-grade");
    stub({ answer: "a brief" });
    const absent = await sendChat({ message: "t", history: [] });
    expect(absent.floorDroppedCitations).toEqual([]);
  });
});

describe("clearanceRank", () => {
  it("ranks the declared levels lowest first, matching the seeded rank", () => {
    // The rank is what the request carries because the level names do not
    // sort: "secret" precedes "unclassified" lexicographically (GH-378).
    expect(clearanceRank("unclassified")).toBe(0);
    expect(clearanceRank("secret")).toBe(3);
    expect(clearanceRank("confidential")).toBe(2);
  });

  it("has no rank for an unstated reader", () => {
    expect(clearanceRank("")).toBeUndefined();
    expect(clearanceRank("cosmic")).toBeUndefined();
  });
});

describe("revisionFields (GH-604)", () => {
  const prior = {
    text: "Bottom Line Up Front ...",
    citations: [
      { start: 0, end: 3, text: "a", record_id: "isw-2022-12-16-c3" },
      { start: 4, end: 6, text: "b", record_id: "https://example.org/report" },
      { start: 7, end: 9, text: "c", record_id: "isw-2022-12-16-c3" },
      { start: 10, end: 12, text: "d", record_id: "osint-2" },
      { start: 13, end: 15, text: "e", record_id: "humint-0042" },
    ],
  } as unknown as Parameters<typeof revisionFields>[0];

  it("carries the draft and its cited corpus records once each, in citation order", () => {
    expect(revisionFields(prior)).toEqual({
      prior_brief: "Bottom Line Up Front ...",
      prior_record_ids: ["isw-2022-12-16-c3", "humint-0042"],
    });
  });

  it("carries the draft alone when it cited no corpus record", () => {
    const uncited = { ...prior, citations: [] } as unknown as Parameters<typeof revisionFields>[0];
    expect(revisionFields(uncited)).toEqual({ prior_brief: "Bottom Line Up Front ..." });
  });

  it("carries nothing without a draft", () => {
    expect(revisionFields(undefined)).toEqual({});
  });

  it("caps the ids at the boundary's limit", () => {
    const many = {
      text: "draft",
      citations: Array.from({ length: MAX_PRIOR_RECORD_IDS + 5 }, (_, i) => ({
        start: i, end: i + 1, text: "x", record_id: `rec-${i}`,
      })),
    } as unknown as Parameters<typeof revisionFields>[0];
    expect(revisionFields(many).prior_record_ids).toHaveLength(MAX_PRIOR_RECORD_IDS);
  });
});
