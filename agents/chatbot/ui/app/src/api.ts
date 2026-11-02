// Client for the chatbot chat endpoint. History is kept client-side (srd002 R4):
// the browser sends the accumulated turns with each request and the agent keeps
// no server-side session.

import { appClient } from "./client";

export const CHAT_ENDPOINT = "/api/v1/chat";

export type Role = "user" | "assistant";

export interface Turn {
  role: Role;
  content: string;
}

export interface ChatRequest {
  message: string;
  history: Turn[];
  // A revision turn's retained prior draft (GH-446): its presence is the
  // whole predicate, and the message is the revision instruction.
  prior_brief?: string;
  // The corpus records the prior draft cited (GH-604). The server reads them
  // back at the analyst's clearance and composes over them with the
  // instruction's retrieval, so a claim carried from the draft keeps its
  // source. Built by revisionFields, never by hand.
  prior_record_ids?: string[];
  // The wire form of the retrieval facets, present only on a faceted turn
  // (srd016 R4.4). Built by facetsPayload, never by hand: the payload's two
  // members are required together by the chat boundary's schema.
  facets?: FacetsPayload;
  // The final reader's clearance, as its rank in the declared order
  // (GH-378): optional, and when present the composed brief cites only
  // sources at or below that level. Distinct from the analyst's clearance,
  // which is the authenticated header governing retrieval -- an analyst may
  // read above what the brief's reader may hold, and the withheld spans are
  // reported rather than dropped.
  reader_clearance_rank?: number;
}

// The analyst's facet selections as the panel holds them. Distinct from the
// wire form: an empty selection here means the turn carries no facets field at
// all, so an unfaceted turn stays byte-for-byte what it was before facets
// existed (srd016 R4.4).
export interface Facets {
  keyword: string;
  sourceTypes: string[];
  reliabilities: string[];
}

export const EMPTY_FACETS: Facets = { keyword: "", sourceTypes: [], reliabilities: [] };

// The wire form the chat boundary requires: both members present on every
// faceted turn, so the retrieval tier composes one shape with no conditionals.
export interface FacetsPayload {
  facet_where: Record<string, unknown>;
  keyword: string;
}

// The Admiralty source-reliability grades, best first. A fixed doctrine scale
// like CLEARANCE_LEVELS above, not a corpus vocabulary: every corpus rates
// sources on the same six grades, so these are constant while the source types
// beside them are read from the served corpus (srd016 R4.5).
export const ADMIRALTY_RELIABILITY = ["A", "B", "C", "D", "E", "F"] as const;

// The stated absence of a metadata constraint. The chat boundary requires both
// facet members together and the store rejects a conjunction with an empty
// operand, so a keyword-only turn states its metadata constraint as a conjunct
// every record satisfies: no chunk carries an empty chunk_id.
const NOOP_CONJUNCT = { chunk_id: { $ne: "" } };

// facetsPayload renders the panel's selections as the wire form, or undefined
// when nothing is selected so the unfaceted turn carries no facets field at
// all (srd016 R4.4). Each selection is a narrowing conjunct; the clearance
// window is conjoined at the retrieval tier and never composed here, which is
// what keeps a facet unable to widen a turn (srd016 R4.3).
export function facetsPayload(facets: Facets): FacetsPayload | undefined {
  const keyword = facets.keyword.trim();
  const conjuncts: Record<string, unknown>[] = [];
  if (facets.sourceTypes.length > 0) {
    conjuncts.push({ source_type: { $in: [...facets.sourceTypes] } });
  }
  if (facets.reliabilities.length > 0) {
    conjuncts.push({ admiralty_source_reliability: { $in: [...facets.reliabilities] } });
  }
  if (keyword === "" && conjuncts.length === 0) {
    return undefined;
  }
  const facet_where =
    conjuncts.length === 0 ? NOOP_CONJUNCT : conjuncts.length === 1 ? conjuncts[0] : { $and: conjuncts };
  return { facet_where, keyword };
}

// The declared clearance levels, lowest first. The same ordered set
// corpus/corpus.yaml declares and the rag-server enforces against
// (srd010 R1.1); the server refuses a level it does not carry, so a drift here
// is a refused turn rather than a widened one.
export const CLEARANCE_LEVELS = ["unclassified", "restricted", "confidential", "secret"] as const;

export type ClearanceLevel = (typeof CLEARANCE_LEVELS)[number];

// The level a turn runs at when the requester has chosen none. Lowest, because
// an absent clearance is an unknown authorization rather than an unrestricted
// one, and the two directions are not symmetric (srd010 R4.1).
export const DEFAULT_CLEARANCE: ClearanceLevel = CLEARANCE_LEVELS[0];

// The header the chatbot derives the enforced level from. Deliberately a header
// and not a body field: a body field is a value the client composes, and what
// matters is not where the value comes from in the happy path but who can
// supply it in the adversarial one (srd010 R3.1).
export const CLEARANCE_HEADER = "X-Analyst-Clearance";

// One RAG source's part in a turn. The mesh reports every source it consulted
// under metadata.sources, sorted into four lists by what became of it, and a
// composed entry carries the chunks that source contributed to the answer
// prompt.
//
// The mesh projects these entries before answering (GH-216). They are rendered
// from fan-out join entries that also carry the topology entry the query was
// dispatched to, base_url included, and those addresses used to arrive here.
export interface SourceOutcome {
  name?: string;
  signal?: string;
  documents?: string[][];
  // The declared metadata of the chunks this source contributed, shaped like
  // documents -- one list per query. The build wrote these onto every
  // collection record, and the INTSUM form's source-evaluation table and
  // classification banner are assembled from them (srd016 R3.4, R3.6).
  metadatas?: ChunkMetadata[][];
  // Reported on an excluded entry only: the identity that did not match the
  // query embedding model, which is why the source was excluded.
  embedding_model?: string;
}

// One composed chunk's declared metadata, as the corpus build recorded it. Every
// field is optional because the mesh reports what a record carries rather than a
// fixed shape, and a source reporting none leaves the table it feeds empty
// rather than filled with a guess.
export interface ChunkMetadata {
  source_id?: string;
  source_type?: string;
  clearance?: string;
  admiralty_source_reliability?: string;
  admiralty_information_credibility?: number;
}

// The chat machine_request maps its terminal signal to an answer and the source
// metadata behind it (srd002 R3, R4). The four source lists are the server's
// own account of the turn: composed sources reached the answer prompt, and the
// other three say why the rest did not.
export interface ChatResponse {
  answer?: string;
  error?: string;
  message?: string;
  trace?: { trace_id?: string };
  metadata?: {
    sources?: {
      composed?: SourceOutcome[];
      embedding_model_excluded?: SourceOutcome[];
      query_failed?: SourceOutcome[];
      not_selected?: string[];
    };
    // Spans the composition provider cited, each resolved to the record id of
    // the chunk it was computed against, and the ones the turn could not
    // resolve. Two lists because an empty citations list means opposite things
    // depending on this one: nothing was cited, or everything cited was dropped
    // (srd009 R4.4, R4.5).
    citations?: Citation[];
    dropped_citations?: DroppedCitation[];
    floor_dropped_citations?: DroppedCitation[];
    // The level the filter actually ran at. Reported so a reader confirms
    // enforcement occurred rather than assuming it, and read from the server
    // rather than echoed from the request: the two differ whenever the
    // requested level was not one the deployment declares.
    clearance_enforced_at?: string;
    conformance?: Conformance;
    // The OSINT search branch outcome (srd011 R5.4), beside the per-source
    // RAG outcomes and never inside them.
    osint?: { status?: string };
    // The document ids handed to composition, the set recall is measured at
    // (srd014 R2.2). The /eval command reads these; nothing else does.
    composition?: { documents?: string[] };
  };
}

// Whether a cited record is an external open-source document rather than
// internal reporting. External documents carry their source URL as the
// document identity (srd011 R7.3), while internal record ids are corpus
// identifiers that never parse as web URLs -- so the URL shape is the
// provenance a reader distinguishes by, and the form marks it explicitly
// rather than dressing open-source material as cleared internal reporting
// (R7.4, suite case 8).
export function isExternalRecordID(recordID: string): boolean {
  // A URL, or the positional osint-N form the live engine's documents carry:
  // the chat provider caps document ids under 100 characters, which real
  // result links exceed, so the id is positional and the link rides the
  // document's data.url (GH-457).
  return /^https?:\/\//i.test(recordID.trim()) || /^osint-\d+$/.test(recordID.trim());
}

// The chat boundary accepts at most this many cited records per revision.
export const MAX_PRIOR_RECORD_IDS = 100;

// The request fields a revision of `prior` carries (GH-446, GH-604): the draft
// itself and the corpus records it cited, once each and in citation order.
// External search results are left out -- they are not corpus records and no
// retrieval unit holds them.
export function revisionFields(prior: Answer | undefined): Pick<ChatRequest, "prior_brief" | "prior_record_ids"> {
  if (!prior?.text) {
    return {};
  }
  const ids: string[] = [];
  for (const citation of prior.citations ?? []) {
    const id = citation.record_id?.trim();
    if (id && !isExternalRecordID(id) && !ids.includes(id)) {
      ids.push(id);
    }
  }
  return ids.length > 0
    ? { prior_brief: prior.text, prior_record_ids: ids.slice(0, MAX_PRIOR_RECORD_IDS) }
    : { prior_brief: prior.text };
}

// A cited span of the answer, resolved to a source the reader can open.
//
// start and end are rune offsets into the answer, not byte offsets -- verified
// against the live provider, where an answer containing a non-ASCII character
// ahead of the span matches by rune and is off by one by byte. JavaScript
// string indexing is by UTF-16 code unit, which agrees with rune indexing for
// the Basic Multilingual Plane and diverges above it, so spans are sliced with
// the Array.from form rather than with substring.
export interface Citation {
  start: number;
  end: number;
  text: string;
  record_id: string;
}

// A cited span the turn could not resolve to a composed document, reported so
// the reader learns why an assertion carries no source rather than being shown
// a citation that opens nothing.
export interface DroppedCitation {
  start: number;
  end: number;
  text: string;
  reason: string;
}

// The template conformance verdict (srd012 R3.3, R4.5), reported beside the
// source and citation outcomes and distinguishable from them. verdict is
// conformant, non_conformant, or unchecked -- the third is the check itself
// failing, which keeps the turn and the draft (R4.3). asserts is the check's
// own scope statement: structure only, never accuracy, support, or citation
// resolution, and never approval.
export interface Conformance {
  verdict?: string;
  template_version?: string;
  defects?: string[];
  asserts?: string;
}

export interface ComposedSource {
  name: string;
  documents: number;
}

// Why a turn carries no attribution. Each kind names the sources it concerns,
// so the panel can say which source failed rather than reporting that something
// did.
export type AttributionReason =
  | { kind: "none-composed" }
  | { kind: "no-documents"; sources: string[] }
  | { kind: "query-failed"; sources: string[] }
  | { kind: "embedding-model-excluded"; sources: string[] }
  | { kind: "not-selected"; sources: string[] };

export interface Attribution {
  composed: ComposedSource[];
  attributed: boolean;
  reasons: AttributionReason[];
}

export interface Answer {
  text: string;
  // Sources the server composed the answer from.
  sources: string[];
  // Spans the provider cited, each naming a source the reader can open. These
  // come from response metadata rather than from the answer text: a model may
  // write bracket tokens that name nothing, or write none at all for a turn
  // that was fully cited.
  citations: Citation[];
  // Cited spans the turn could not resolve, reported rather than hidden.
  droppedCitations: DroppedCitation[];
  // Cited spans withheld below the final reader's reliability floor
  // (GH-357): reported apart from droppedCitations because the reasons
  // answer different questions.
  floorDroppedCitations: DroppedCitation[];
  grounded: boolean;
  attribution: Attribution;
  traceId?: string;
  // What the turn was enforced at, straight from the server. The panel shows
  // this rather than the selected level so a level the deployment does not
  // declare reads as the level that actually applied.
  clearanceEnforcedAt?: string;
  // The structural verdict on the composed brief, straight from the server.
  // Absent on a turn the mesh did not check, and the form then renders
  // nothing about conformance rather than inventing a verdict.
  conformance?: Conformance;
  // The declared metadata of every chunk the turn composed from, nested source
  // by source and query by query as the response reports it. The INTSUM form
  // reads this rather than fetching the records again, so the table and the
  // brief describe the same turn.
  composedMetadata: ChunkMetadata[][][];
  // The search branch outcome, straight from the server: composed, empty,
  // errored, dropped, or disabled (srd011 R5.4). Absent on a response that
  // predates the branch.
  osintStatus?: string;
  // The composition set's document ids, for the /eval recall computation.
  // Optional so fixture answers elsewhere need not carry it.
  compositionDocuments?: string[];
  // The banner of the brief a revision started from (GH-594). A revision keeps
  // prose derived from the prior brief even when the new turn's retrieval
  // composed nothing at that level, so the form's banner must never read lower
  // than this. Set client-side by the revise path; absent on a fresh brief.
  priorBanner?: string;
  // The text of the brief a revision started from (GH-605), so the form can
  // tint what the revise note changed. Set beside priorBanner; absent on a
  // fresh brief.
  priorText?: string;
}

// sendAudit scores one cited claim against the chunk it cites (GH-543). The
// judge run's trace id rides along (GH-662) so the audit table can open it.
export async function sendAudit(
  summary: string,
  document: string,
): Promise<{ verdict: string; traceId?: string }> {
  const res = await appClient().request("/api/v1/audit", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ summary, document }),
  });
  const body = (await res.json()) as {
    verdict?: string;
    message?: string;
    error?: string;
    trace?: { trace_id?: string };
  };
  if (!res.ok) throw new Error(body.message ?? body.error ?? `audit returned HTTP ${res.status}`);
  return { verdict: (body.verdict ?? "").trim(), traceId: body.trace?.trace_id };
}

// sendTranslate renders the brief's segments in the target language (GH-670):
// one translation per segment, same order, via the flagship's forced tool call.
export async function sendTranslate(
  segments: string[],
  language: string,
): Promise<{ translations: string[]; traceId?: string }> {
  const res = await appClient().request("/api/v1/translate", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ segments, language }),
  });
  const body = (await res.json()) as {
    translations?: string[];
    message?: string;
    error?: string;
    trace?: { trace_id?: string };
  };
  if (!res.ok) throw new Error(body.message ?? body.error ?? `translate returned HTTP ${res.status}`);
  return { translations: body.translations ?? [], traceId: body.trace?.trace_id };
}

export class ChatError extends Error {}

export async function sendChat(
  req: ChatRequest,
  signal?: AbortSignal,
  clearance: ClearanceLevel = DEFAULT_CLEARANCE,
): Promise<Answer> {
  let res: Response;
  try {
    res = await appClient().request(CHAT_ENDPOINT, {
      method: "POST",
      headers: { "content-type": "application/json", [CLEARANCE_HEADER]: clearance },
      body: JSON.stringify(req),
      signal,
    });
  } catch (err) {
    throw new ChatError(err instanceof Error ? err.message : String(err));
  }

  let body: ChatResponse = {};
  try {
    body = (await res.json()) as ChatResponse;
  } catch {
    throw new ChatError(`chat endpoint returned HTTP ${res.status} with a non-JSON body`);
  }

  if (!res.ok) {
    throw new ChatError(body.message ?? body.error ?? `chat endpoint returned HTTP ${res.status}`);
  }

  const text = (body.answer ?? "").trim();
  const attribution = deriveAttribution(body);
  return {
    text,
    sources: attribution.composed.map((source) => source.name),
    citations: body.metadata?.citations ?? [],
    droppedCitations: body.metadata?.dropped_citations ?? [],
    floorDroppedCitations: body.metadata?.floor_dropped_citations ?? [],
    grounded: attribution.attributed,
    attribution,
    traceId: body.trace?.trace_id,
    clearanceEnforcedAt: body.metadata?.clearance_enforced_at,
    conformance: body.metadata?.conformance,
    composedMetadata: composedChunkMetadata(body),
    osintStatus: body.metadata?.osint?.status,
    compositionDocuments: body.metadata?.composition?.documents ?? [],
  };
}

// deriveAttribution decides whether a turn answered from the corpus, and when it
// did not, which source outcome explains that.
//
// A composed entry means the source took part in composition, not that it had
// anything to contribute: a rag-server bound to an empty collection answers the
// query and composes zero chunks. So attribution requires composed sources that
// carry documents, not merely composed sources.
//
// This is the same rule demoVerifyComposedSources applies in
// magefiles/demo_verify.go, deliberately: the panel and mage app:verify must
// never disagree about a turn. Change one and the other has to change with it.
export function deriveAttribution(body: ChatResponse): Attribution {
  const sources = body.metadata?.sources ?? {};
  const composed = (sources.composed ?? []).map((outcome, index) => ({
    name: outcomeName(outcome, index),
    documents: documentCount(outcome),
  }));
  const retrieved = composed.reduce((total, source) => total + source.documents, 0);
  if (retrieved > 0) {
    return { composed, attributed: true, reasons: [] };
  }

  const reasons: AttributionReason[] = [];
  if (composed.length === 0) {
    reasons.push({ kind: "none-composed" });
  } else {
    reasons.push({ kind: "no-documents", sources: composed.map((source) => source.name) });
  }
  const failed = outcomeNames(sources.query_failed);
  if (failed.length > 0) {
    reasons.push({ kind: "query-failed", sources: failed });
  }
  const excluded = outcomeNames(sources.embedding_model_excluded);
  if (excluded.length > 0) {
    reasons.push({ kind: "embedding-model-excluded", sources: excluded });
  }
  const unselected = (sources.not_selected ?? []).filter((name) => name.trim() !== "");
  if (unselected.length > 0) {
    reasons.push({ kind: "not-selected", sources: unselected });
  }
  return { composed, attributed: false, reasons };
}

// composedChunkMetadata gathers what each composed source reported about the
// chunks it contributed. A source that reports none contributes an empty list
// rather than being skipped, so the nesting still lines up with the composed
// order a reader of the response would follow.
function composedChunkMetadata(body: ChatResponse): ChunkMetadata[][][] {
  return (body.metadata?.sources?.composed ?? []).map((outcome) => outcome.metadatas ?? []);
}

function outcomeName(outcome: SourceOutcome, index: number): string {
  const name = outcome.name?.trim();
  return name ? name : `source ${index}`;
}

function outcomeNames(outcomes: SourceOutcome[] | undefined): string[] {
  return (outcomes ?? [])
    .map((outcome) => outcome.name?.trim() ?? "")
    .filter((name) => name !== "");
}

function documentCount(outcome: SourceOutcome): number {
  const documents = outcome.documents ?? [];
  return documents.reduce((total, query) => total + query.length, 0);
}

// citedSpan returns the text a citation covers, sliced by rune.
//
// The provider reports start and end as rune offsets. JavaScript indexes
// strings by UTF-16 code unit, so substring agrees with the provider for every
// character in the Basic Multilingual Plane and disagrees above it -- an emoji
// or a rare script ahead of the span shifts every later offset by one. Slicing
// the code-point array is what makes the highlight land on the cited text
// rather than near it.
//
// The citation carries its own text, so a caller could display that instead.
// This exists for the panel to locate the span inside the answer, which is what
// lets a reader see which sentence a source backs.
export function citedSpan(answer: string, citation: Citation): string {
  const runes = Array.from(answer);
  if (citation.start < 0 || citation.end > runes.length || citation.end <= citation.start) {
    // An out-of-range span is the provider and the answer disagreeing. Fall back
    // to the text the citation carries rather than throwing or truncating: the
    // reader still sees what was cited.
    return citation.text;
  }
  return runes.slice(citation.start, citation.end).join("");
}

// --- Export (srd013 R4.1, R4.2, GH-352) ---

// The export relay on this origin. The exporter itself admits only the
// chatbot as a caller, so the SPA never addresses it directly.
export const EXPORT_ENDPOINT = "/api/v1/export";

export class ExportError extends Error {}

interface ExportResponse {
  status?: string;
  error?: string;
  pdf_base64?: string;
}

// exportBrief sends the analyst's approved markdown and returns the rendered
// PDF's bytes. The request carries the document and nothing else -- no
// clearance, no identity, no history (srd013 R3.5) -- and a failed render
// throws with the endpoint's stated error rather than returning partial
// output (srd013 R5.2).
export async function exportBrief(content: string, signal?: AbortSignal): Promise<Uint8Array> {
  let res: Response;
  try {
    res = await appClient().request(EXPORT_ENDPOINT, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ schema_version: "1", content }),
      signal,
    });
  } catch (err) {
    throw new ExportError(err instanceof Error ? err.message : String(err));
  }

  let body: ExportResponse = {};
  try {
    body = (await res.json()) as ExportResponse;
  } catch {
    throw new ExportError(`export endpoint returned HTTP ${res.status} with a non-JSON body`);
  }
  if (!res.ok || body.status !== "rendered" || !body.pdf_base64) {
    throw new ExportError(body.error ?? `export endpoint returned HTTP ${res.status}`);
  }
  return decodeBase64(body.pdf_base64);
}

// decodeBase64 turns the endpoint's encoded document into bytes. Exported for
// the test that proves a known encoding round-trips; atob handles the standard
// alphabet the exporter's encoder emits.
export function decodeBase64(encoded: string): Uint8Array {
  const raw = atob(encoded);
  const bytes = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
  return bytes;
}


// gradesAtOrAbove lists the Admiralty grades a floor admits, best first --
// the analyst reliability dropdown's facet payload (GH-357). The floor letter
// itself is included: "at or above B" is A and B.
export function gradesAtOrAbove(floor: string): string[] {
  const index = ADMIRALTY_RELIABILITY.indexOf(floor as (typeof ADMIRALTY_RELIABILITY)[number]);
  if (index < 0) return [];
  return ADMIRALTY_RELIABILITY.slice(0, index + 1);
}

// clearanceRank places a level in the declared order, lowest first, matching
// the rank the corpus seeds onto every chunk (GH-378). An unstated reader has
// no rank and the request omits the field.
export function clearanceRank(level: string): number | undefined {
  const index = CLEARANCE_LEVELS.indexOf(level as ClearanceLevel);
  return index < 0 ? undefined : index;
}

// The /search command's wire (GH-447): retrieval alone, served by the
// chatbot's search machine. One result row per retrieved chunk with its
// provenance, plus what explains an empty list.
export interface SearchResult {
  id: string;
  source: string;
  text: string;
  clearance?: string;
  reliability?: string;
}

export interface SearchAnswer {
  results: SearchResult[];
  query: string;
  clearanceEnforcedAt?: string;
  failedSources?: number;
  // The turn's trace, so the results row offers the same walk a research
  // bubble does (GH-615).
  traceId?: string;
}

export async function sendSearch(
  message: string,
  clearance: ClearanceLevel = DEFAULT_CLEARANCE,
  signal?: AbortSignal,
): Promise<SearchAnswer> {
  let res: Response;
  try {
    res = await appClient().request("/api/v1/search", {
      method: "POST",
      headers: { "content-type": "application/json", [CLEARANCE_HEADER]: clearance },
      body: JSON.stringify({ message }),
      signal,
    });
  } catch (err) {
    throw new ChatError(err instanceof Error ? err.message : String(err));
  }
  let body: {
    results?: SearchResult[];
    query?: string;
    clearance_enforced_at?: string;
    failed_sources?: number | string;
    trace?: { trace_id?: string };
    message?: string;
    error?: string;
  } = {};
  try {
    body = (await res.json()) as typeof body;
  } catch {
    throw new ChatError(`search endpoint returned HTTP ${res.status} with a non-JSON body`);
  }
  if (!res.ok) {
    throw new ChatError(body.message ?? body.error ?? `search endpoint returned HTTP ${res.status}`);
  }
  return {
    results: body.results ?? [],
    query: body.query ?? message,
    clearanceEnforcedAt: body.clearance_enforced_at,
    failedSources: typeof body.failed_sources === "string" ? Number(body.failed_sources) || 0 : body.failed_sources,
    traceId: body.trace?.trace_id,
  };
}

// The /summarize command's wire (GH-448): the Command model alone over the
// document the window holds.
export async function sendSummarize(document: string, signal?: AbortSignal): Promise<string> {
  let res: Response;
  try {
    res = await appClient().request("/api/v1/summarize", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ document }),
      signal,
    });
  } catch (err) {
    throw new ChatError(err instanceof Error ? err.message : String(err));
  }
  let body: { summary?: string; message?: string; error?: string } = {};
  try {
    body = (await res.json()) as typeof body;
  } catch {
    throw new ChatError(`summarize endpoint returned HTTP ${res.status} with a non-JSON body`);
  }
  if (!res.ok) {
    throw new ChatError(body.message ?? body.error ?? `summarize endpoint returned HTTP ${res.status}`);
  }
  return (body.summary ?? "").trim();
}

// The /harvest command's wire (GH-459): search, embed, ingest; the receipt
// names what became corpus.
export interface HarvestAnswer {
  harvested: Array<{ id: string; url: string; title: string }>;
  failedEmbeds: number;
  // The web queries the model proposed via the report_search_terms tool call,
  // or the raw tasking on the fallback leg (GH-612).
  queries: string[];
  traceId?: string;
}

export async function sendHarvest(message: string, signal?: AbortSignal): Promise<HarvestAnswer> {
  let res: Response;
  try {
    res = await appClient().request("/api/v1/harvest", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ message }),
      signal,
    });
  } catch (err) {
    throw new ChatError(err instanceof Error ? err.message : String(err));
  }
  let body: {
    harvested?: Array<{ id: string; url: string; title: string }> | string;
    failed_embeds?: number | string;
    queries?: string[];
    trace?: { trace_id?: string };
    message?: string;
    error?: string;
  } = {};
  try {
    body = (await res.json()) as typeof body;
  } catch {
    throw new ChatError(`harvest endpoint returned HTTP ${res.status} with a non-JSON body`);
  }
  if (!res.ok) {
    throw new ChatError(body.message ?? body.error ?? `harvest endpoint returned HTTP ${res.status}`);
  }
  return {
    harvested: Array.isArray(body.harvested) ? body.harvested : [],
    failedEmbeds:
      typeof body.failed_embeds === "string"
        ? Number(body.failed_embeds) || 0
        : (body.failed_embeds ?? 0),
    queries: Array.isArray(body.queries) && body.queries.length > 0 ? body.queries : [message],
    traceId: body.trace?.trace_id,
  };
}
