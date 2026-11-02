import { describe, expect, it } from "vitest";

import { CLEARANCE_HEADER, DEFAULT_CLEARANCE } from "./api";
import {
  DOCUMENTS_ENDPOINT,
  DOCUMENTS_QUERY_ENDPOINT,
  DocumentsError,
  distinctSourceTypes,
  documentViews,
  envelopeRecordID,
  fetchDocumentsByIds,
  fetchSourceTypes,
} from "./documentsApi";

// A unit's read as the document boundary reports it: the mapped response under
// the fan-out join bookkeeping.
function unitRead(rows: { id: string; text?: string; metadata?: Record<string, unknown> }[]) {
  return {
    result: {
      structured_output: {
        mapped: {
          ids: rows.map((row) => row.id),
          documents: rows.map((row) => row.text ?? ""),
          metadatas: rows.map((row) => row.metadata ?? {}),
        },
      },
    },
  };
}

describe("documentViews", () => {
  it("types each served row and parses the encoded lineage once", () => {
    const parent = {
      document_id: "HR-22-008",
      position: 1,
      sibling_count: 4,
      char_span: [0, 221],
      coarser_parent: "HR-22-008-medium-900-0001",
    };
    const views = documentViews([
      unitRead([
        {
          id: "HR-22-008-atomic-0001",
          text: "the chunk text",
          metadata: {
            kind: "chunk",
            document_id: "HR-22-008",
            chunk_profile: "atomic",
            clearance: "secret",
            parent: JSON.stringify(parent),
          },
        },
      ]),
    ]);
    expect(views).toHaveLength(1);
    expect(views[0].parent).toEqual(parent);
    expect(views[0].chunkProfile).toBe("atomic");
    expect(views[0].text).toBe("the chunk text");
  });

  it("keeps a row whose lineage does not parse, without the lineage", () => {
    // The document is still readable; a walk that cannot continue says so
    // instead of failing the read that succeeded.
    const views = documentViews([
      unitRead([{ id: "r1", text: "body", metadata: { parent: "not json" } }]),
    ]);
    expect(views[0].text).toBe("body");
    expect(views[0].parent).toBeUndefined();
  });

  it("parses cross-tier derivation from the envelope row", () => {
    const derived = [{ tier: "osint_news", id: "TG-22-104", relation: "written_over" }];
    const views = documentViews([
      unitRead([
        {
          id: envelopeRecordID("HR-22-008"),
          metadata: { kind: "envelope", derived_from: JSON.stringify(derived) },
        },
      ]),
    ]);
    expect(views[0].derivedFrom).toEqual(derived);
  });

  it("keeps one view per identifier across overlapping units", () => {
    const views = documentViews([
      unitRead([{ id: "r1", text: "from rag0" }]),
      unitRead([{ id: "r1", text: "from rag1" }]),
    ]);
    expect(views).toHaveLength(1);
    expect(views[0].text).toBe("from rag0");
  });
});

describe("distinctSourceTypes", () => {
  it("flattens overlapping units into one sorted vocabulary", () => {
    const views = documentViews([
      unitRead([
        { id: "a#envelope", metadata: { source_type: "osint_news" } },
        { id: "b#envelope", metadata: { source_type: "humint" } },
      ]),
      unitRead([
        { id: "c#envelope", metadata: { source_type: "humint" } },
        { id: "d#envelope", metadata: { source_type: "sigint_event" } },
      ]),
    ]);
    expect(distinctSourceTypes(views)).toEqual(["humint", "osint_news", "sigint_event"]);
  });

  it("skips records that report no source type rather than inventing an option", () => {
    const views = documentViews([
      unitRead([
        { id: "a", metadata: { source_type: "humint" } },
        { id: "b", metadata: {} },
        { id: "c", metadata: { source_type: "  " } },
      ]),
    ]);
    expect(distinctSourceTypes(views)).toEqual(["humint"]);
  });

  it("answers an empty vocabulary for an empty read", () => {
    expect(distinctSourceTypes([])).toEqual([]);
  });
});

function stubFetch(status: number, body: unknown) {
  const calls: { url: string; headers: Record<string, string>; body: string }[] = [];
  const fake = async (url: string, init: RequestInit) => {
    calls.push({
      url,
      headers: init.headers as Record<string, string>,
      body: init.body as string,
    });
    return { ok: status < 400, status, json: async () => body } as Response;
  };
  globalThis.fetch = fake as unknown as typeof fetch;
  return calls;
}

describe("fetchSourceTypes", () => {
  it("reads envelopes under the analyst's clearance", async () => {
    // The vocabulary is clearance-scoped (srd016 R4.5 read against R5.7): an
    // option naming a source type reachable only above the analyst's level
    // would itself be information about the restricted corpus.
    const calls = stubFetch(200, {
      records: [unitRead([{ id: "a#envelope", metadata: { source_type: "humint" } }])],
    });
    const types = await fetchSourceTypes("confidential");
    expect(types).toEqual(["humint"]);
    expect(calls[0].url).toBe(DOCUMENTS_QUERY_ENDPOINT);
    expect(calls[0].headers[CLEARANCE_HEADER]).toBe("confidential");
    expect(JSON.parse(calls[0].body)).toEqual({ document_filter: { kind: { $eq: "envelope" } } });
  });

  it("defaults to the lowest declared level", async () => {
    const calls = stubFetch(200, { records: [] });
    await fetchSourceTypes();
    expect(calls[0].headers[CLEARANCE_HEADER]).toBe(DEFAULT_CLEARANCE);
  });

  it("throws on a failed read rather than answering an empty vocabulary", async () => {
    // No reachable source types is a corpus fact; an unreachable boundary is
    // not, and conflating them would hide the outage as a quiet loss of the
    // source-type facet.
    stubFetch(500, { error: "command_error", detail: "a boundary word failed" });
    await expect(fetchSourceTypes()).rejects.toThrow(DocumentsError);
  });
});

describe("fetchDocumentsByIds", () => {
  it("reads the named records and answers absence as an empty result", async () => {
    // An identifier above the level or naming nothing is simply absent (srd016
    // R5.7): the walk shows no node, and nothing distinguishes the two cases.
    const calls = stubFetch(200, { records: [unitRead([])] });
    const views = await fetchDocumentsByIds(["above-clearance-0001"], "unclassified");
    expect(views).toEqual([]);
    expect(calls[0].url).toBe(DOCUMENTS_ENDPOINT);
    expect(JSON.parse(calls[0].body)).toEqual({ record_ids: ["above-clearance-0001"] });
  });
});

describe("corpus envelope rows", () => {
  it("carries the reliability grade the seed writes onto every row", () => {
    // The corpus panel's rolled-up list states each document's Admiralty
    // grade beside its clearance (GH-361); the mapping surfaces the metadata
    // field the seed writes unconditionally.
    const views = documentViews([
      {
        result: {
          structured_output: {
            mapped: {
              ids: ["isw-2022-12-16#envelope"],
              documents: ["the held text"],
              metadatas: [
                {
                  kind: "envelope",
                  document_id: "isw-2022-12-16",
                  source_type: "osint_assessment",
                  clearance: "unclassified",
                  admiralty_source_reliability: "B",
                },
              ],
            },
          },
        },
      },
    ] as never);
    expect(views[0].reliability).toBe("B");
    expect(views[0].kind).toBe("envelope");
  });
});

// The citation entries a chunk's markers resolve to travel as JSON inside the
// flat collection metadata, the way the lineage block does (srd017 R10.6).
describe("chunk references", () => {
  it("parses the encoded entries onto the view", () => {
    const [view] = documentViews([
      unitRead([
        {
          id: "isw-2022-12-01-medium-900-0032",
          text: "occupation officials are demanding money.[76]",
          metadata: {
            kind: "chunk",
            document_id: "isw-2022-12-01",
            references: JSON.stringify([
              { number: 76, text: "https://t.me/rybar/41602", urls: ["https://t.me/rybar/41602"] },
              { number: 77, text: "Russia's Zapad-2021 Exercise", urls: [] },
            ]),
          },
        },
      ]),
    ]);

    expect(view.references).toHaveLength(2);
    expect(view.references[0].number).toBe(76);
    expect(view.references[0].urls).toEqual(["https://t.me/rybar/41602"]);
    // An entry naming a work rather than a link keeps its text.
    expect(view.references[1].urls).toEqual([]);
    expect(view.references[1].text).toContain("Zapad-2021");
  });

  it("keeps an obscured address exactly as the tier wrote it", () => {
    const [view] = documentViews([
      unitRead([
        {
          id: "isw-2022-12-03-medium-900-0001",
          metadata: {
            kind: "chunk",
            references: JSON.stringify([
              { number: 4, urls: ["https://gur dot gov.ua/content/sohodni", "https://interfax(dot)com/x"] },
            ]),
          },
        },
      ]),
    ]);

    expect(view.references[0].urls).toEqual([
      "https://gur dot gov.ua/content/sohodni",
      "https://interfax(dot)com/x",
    ]);
  });

  it("reads a row carrying no entries, or malformed ones, as none", () => {
    const [absent, empty, malformed] = documentViews([
      unitRead([
        { id: "tg-rbc_news-64950-medium-900-0001", metadata: { kind: "chunk" } },
        { id: "isw-a", metadata: { kind: "chunk", references: "" } },
        { id: "isw-b", metadata: { kind: "chunk", references: "{not json" } },
      ]),
    ]);

    expect(absent.references).toEqual([]);
    expect(empty.references).toEqual([]);
    expect(malformed.references).toEqual([]);
  });

  // The payload below is copied verbatim from the deployed documents API for
  // isw-2022-12-01-medium-900-0032, not hand-written. The field is `number`
  // because that is what the Go encoder emits; a fixture written to match the
  // interface instead agreed with the client's own mistake and shipped a panel
  // that rendered "[undefined]" for every entry (GH-399).
  it("reads the field name the serving path actually emits", () => {
    const served =
      '[{"number":76,"text":"https://sprotyv dot mod.gov.ua/2022/12/01/v-krymu",' +
      '"urls":["https://sprotyv dot mod.gov.ua/2022/12/01/v-krymu"]},' +
      '{"number":77,"text":"https://www.facebook.com/GeneralStaff.ua/posts/pfbid028",' +
      '"urls":["https://www.facebook.com/GeneralStaff.ua/posts/pfbid028"]}]';
    const [view] = documentViews([
      unitRead([
        { id: "isw-2022-12-01-medium-900-0032", metadata: { kind: "chunk", references: served } },
      ]),
    ]);

    expect(view.references.map((r) => r.number)).toEqual([76, 77]);
    expect(view.references[0].urls?.[0]).toContain("sprotyv dot mod.gov.ua");
  });
});
