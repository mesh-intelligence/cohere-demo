// Client for the chatbot's document read boundary (srd016 R5.2). The panel
// uses it to read the facet vocabularies from the corpus the deployment
// serves rather than shipping them in the application (srd016 R4.5): a corpus
// with different source types offers different facets without a rebuild.
//
// The read runs under the analyst's clearance like every other read, so the
// vocabulary offers only values from records the analyst could reach -- a
// facet option naming a source type that exists only above their level would
// itself be information about the restricted corpus.

import { CLEARANCE_HEADER, DEFAULT_CLEARANCE, type ChunkMetadata, type ClearanceLevel } from "./api";
import { appClient } from "./client";

export const DOCUMENTS_ENDPOINT = "/api/v1/documents";
export const DOCUMENTS_QUERY_ENDPOINT = "/api/v1/documents/query";

// A chunk's structural lineage as the collection serves it (srd017 R5): the
// parent block travels as a JSON string inside the flat metadata and is parsed
// here, at the client's read boundary, so the panels receive it typed and
// never touch the store's encoding (srd017 R8.6).
export interface ChunkLineage {
  document_id?: string;
  position?: number;
  sibling_count?: number;
  // The half-open character span the chunk occupies in the joined searchable
  // body. The full document also carries the segments the build marked
  // unsearchable, so a consumer verifies the span against the chunk text
  // before highlighting with it.
  char_span?: number[];
  prev_chunk_id?: string;
  next_chunk_id?: string;
  coarser_parent?: string;
  raw_ref?: { store?: string; id?: string; path?: string };
}

// One citation entry a chunk's markers resolve to (srd017 R10.1, R10.6): the
// number the prose cites it by, the entry as the source wrote it, and the
// addresses read out of it. Addresses are rendered as written -- the tier
// obscures the links it considers hostile to follow, and restoring them is a
// republication decision the interface has no standing to make.
export interface ChunkReference {
  // The field is `number` because that is what the store writes and what the
  // serving path encodes; the lineage block sets the same precedent of taking
  // the Go names verbatim rather than renaming them at this boundary.
  number?: number;
  text?: string;
  urls?: string[];
}

// One cross-tier provenance edge (srd017 R5.4): what a document was derived
// from and how.
export interface DerivedRef {
  tier?: string;
  id?: string;
  relation?: string;
}

// One served row of the document collection, typed for the panels.
export interface DocumentView {
  id: string;
  kind?: string;
  documentID?: string;
  sourceType?: string;
  clearance?: string;
  issued?: string;
  title?: string;
  chunkProfile?: string;
  reliability?: string;
  text: string;
  parent?: ChunkLineage;
  derivedFrom: DerivedRef[];
  references: ChunkReference[];
}

// The filter that lists document envelopes: one row per document, which is the
// cheapest read that spans the corpus. Chunk rows would answer the same
// question many times over.
export const ENVELOPE_FILTER = { kind: { $eq: "envelope" } };

export class DocumentsError extends Error {}

// envelopeRecordID names the envelope row of a document, mirroring the
// identifier convention the seed writes (magefiles/document_serving.go).
export function envelopeRecordID(documentID: string): string {
  return `${documentID}#envelope`;
}

// A served row's metadata as the collection carries it: the facet scalars the
// chunk records also carry, plus the document fields and the JSON-encoded
// lineage blocks.
interface DocumentRowMetadata extends ChunkMetadata {
  kind?: string;
  document_id?: string;
  issued?: string;
  title?: string;
  chunk_profile?: string;
  parent?: string;
  derived_from?: string;
  references?: string;
}

// One RAG unit's read as the fan-out join reports it: the unit's mapped
// response sits under result.structured_output.mapped, beside the join
// bookkeeping the panel has no use for.
interface DocumentReadRecord {
  result?: {
    structured_output?: {
      mapped?: {
        ids?: string[];
        documents?: string[];
        metadatas?: DocumentRowMetadata[];
      };
    };
  };
}

interface DocumentsQueryResponse {
  records?: DocumentReadRecord[];
  error?: string;
  detail?: string;
}

// documentViews flattens every unit's rows into typed views, parsing the
// JSON-encoded lineage once here so no panel touches the encoding. A row whose
// lineage does not parse keeps its content and drops the lineage: the document
// is still readable, and a walk that cannot continue says so instead of
// failing the read that succeeded.
export function documentViews(records: DocumentReadRecord[]): DocumentView[] {
  const views = new Map<string, DocumentView>();
  for (const record of records) {
    const mapped = record.result?.structured_output?.mapped ?? {};
    const ids = mapped.ids ?? [];
    for (let index = 0; index < ids.length; index++) {
      const id = ids[index];
      // Units may hold the same corpus and answer the same row; one view per
      // identifier, first unit wins.
      if (!id || views.has(id)) {
        continue;
      }
      const metadata = mapped.metadatas?.[index] ?? {};
      views.set(id, {
        id,
        kind: metadata.kind,
        documentID: metadata.document_id,
        sourceType: metadata.source_type,
        clearance: metadata.clearance,
        issued: metadata.issued,
        title: metadata.title,
        chunkProfile: metadata.chunk_profile,
        reliability: metadata.admiralty_source_reliability,
        text: mapped.documents?.[index] ?? "",
        parent: parseEncoded<ChunkLineage>(metadata.parent),
        derivedFrom: parseEncoded<DerivedRef[]>(metadata.derived_from) ?? [],
        references: parseEncoded<ChunkReference[]>(metadata.references) ?? [],
      });
    }
  }
  return [...views.values()];
}

function parseEncoded<T>(encoded: string | undefined): T | undefined {
  if (!encoded) {
    return undefined;
  }
  try {
    return JSON.parse(encoded) as T;
  } catch {
    return undefined;
  }
}

async function readDocuments(
  endpoint: string,
  body: Record<string, unknown>,
  clearance: ClearanceLevel,
  signal?: AbortSignal,
): Promise<DocumentView[]> {
  let res: Response;
  try {
    res = await appClient().request(endpoint, {
      method: "POST",
      headers: { "content-type": "application/json", [CLEARANCE_HEADER]: clearance },
      body: JSON.stringify(body),
      signal,
    });
  } catch (err) {
    throw new DocumentsError(err instanceof Error ? err.message : String(err));
  }
  let parsed: DocumentsQueryResponse = {};
  try {
    parsed = (await res.json()) as DocumentsQueryResponse;
  } catch {
    throw new DocumentsError(`document boundary returned HTTP ${res.status} with a non-JSON body`);
  }
  if (!res.ok) {
    throw new DocumentsError(parsed.detail ?? parsed.error ?? `document boundary returned HTTP ${res.status}`);
  }
  return documentViews(parsed.records ?? []);
}

// fetchDocumentsByIds reads the named records under the analyst's clearance.
// An identifier above the level, or one that names nothing, is simply absent
// from the result -- the boundary answers absence rather than refusal (srd016
// R5.7), and the walk renders no node for it (R6.5).
export function fetchDocumentsByIds(
  recordIDs: string[],
  clearance: ClearanceLevel = DEFAULT_CLEARANCE,
  signal?: AbortSignal,
): Promise<DocumentView[]> {
  return readDocuments(DOCUMENTS_ENDPOINT, { record_ids: recordIDs }, clearance, signal);
}

// fetchDocumentVersions reads every served row of one document -- its envelope
// and each chunk-profile version -- which is what the walk shows beside the
// cited chunk (srd016 R6.1).
export function fetchDocumentVersions(
  documentID: string,
  clearance: ClearanceLevel = DEFAULT_CLEARANCE,
  signal?: AbortSignal,
): Promise<DocumentView[]> {
  return readDocuments(
    DOCUMENTS_QUERY_ENDPOINT,
    { document_filter: { document_id: { $eq: documentID } } },
    clearance,
    signal,
  );
}

// fetchSourceTypes reads the source types the served corpus spans, distinct
// and sorted. A failed read throws rather than answering an empty vocabulary:
// the two mean different things to the panel -- no reachable source types is a
// corpus fact, an unreachable boundary is not.
export async function fetchSourceTypes(
  clearance: ClearanceLevel = DEFAULT_CLEARANCE,
  signal?: AbortSignal,
): Promise<string[]> {
  const views = await readDocuments(
    DOCUMENTS_QUERY_ENDPOINT,
    { document_filter: ENVELOPE_FILTER },
    clearance,
    signal,
  );
  return distinctSourceTypes(views);
}

// distinctSourceTypes reduces the served envelopes to one sorted vocabulary.
// Units answer independently and may overlap, so the set is what matters and
// the order is imposed here rather than inherited from topology order.
export function distinctSourceTypes(views: DocumentView[]): string[] {
  const types = new Set<string>();
  for (const view of views) {
    const sourceType = view.sourceType?.trim();
    if (sourceType) {
      types.add(sourceType);
    }
  }
  return [...types].sort();
}

// fetchCorpusEnvelopes reads the rolled-up document list: one envelope row
// per document the analyst's clearance admits, which is the corpus panel's
// top level (GH-361). The rows carry the full held text, so unraveling a
// document's content is a render of what this read already returned.
export function fetchCorpusEnvelopes(
  clearance: ClearanceLevel = DEFAULT_CLEARANCE,
  signal?: AbortSignal,
): Promise<DocumentView[]> {
  return readDocuments(DOCUMENTS_QUERY_ENDPOINT, { document_filter: ENVELOPE_FILTER }, clearance, signal);
}
