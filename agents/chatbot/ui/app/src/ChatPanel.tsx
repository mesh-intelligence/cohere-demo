import { useEffect, useRef, useState } from "react";
import {
  clearanceSelectLabel,
  levelLabel,
  readerSelectLabel,
  reliabilityFloorLabel,
  reliabilityFloorTitle,
} from "./uiConfig";
import {
  ADMIRALTY_RELIABILITY,
  CLEARANCE_LEVELS,
  DEFAULT_CLEARANCE,
  EMPTY_FACETS,
  citedSpan,
  facetsPayload,
  clearanceRank,
  gradesAtOrAbove,
  isExternalRecordID,
  sendChat,
  type Answer,
  type ClearanceLevel,
  type Attribution,
  type AttributionReason,
  type Citation,
  type ComposedSource,
  type DroppedCitation,
  type Facets,
  type Turn,
} from "./api";
import { fetchDocumentsByIds, fetchSourceTypes } from "./documentsApi";
import ExportPanel from "./ExportPanel";
import TurnFigure from "./TurnFigure";
import IntsumPanel from "./IntsumPanel";
import {
  composedChunkMetadata,
  higherBanner,
  highestClearance,
  type TurnContext,
} from "./intsumForm";
import ProvenancePanel from "./ProvenancePanel";
import { turnContextFor } from "./turnContext";
import { exportDocument } from "./exportDocument";
import { revisionFields, sendAudit, sendHarvest, sendSearch, sendSummarize, sendTranslate, exportBrief, type SearchResult } from "./api";
import evalCorpus from "./evalCorpus.json";
import { COMMANDS, isParseError, parseCommand } from "./commands";
import { auditTargets } from "./auditSentences";
import { buildSegments, rebuildTranslated, REPORT_LANGUAGES } from "./translate";
import AuditPanel, { type AuditFinding, type AuditReport } from "./AuditPanel";
import { newChatId, saveChat, subscribeOpen, type StoredChat } from "./chatHistory";
import { TraceStoryOverlay, useTurnActivity, type PanelProps } from "@declarative-agents/ui-kit";
import { CHAT_MILESTONES } from "./milestones";
import { storyChats } from "./storyChats";
import { traceOptions } from "./traceOptions";
import { useTurns } from "./turns";

interface Message {
  role: "user" | "assistant";
  content: string;
  // A /search turn's rows (GH-449): rendered as a result list, not prose.
  searchResults?: SearchResult[];
  // A /help turn (GH-497): the bubble renders the workflow figure instead of prose.
  helpFigure?: boolean;
  // The turn's cross-agent trace (GH-513): persisted with the chat so a
  // restored conversation still opens its timelines.
  traceId?: string;
  attribution?: Attribution;
  citations?: Citation[];
  droppedCitations?: DroppedCitation[];
  clearanceEnforcedAt?: string;
  error?: boolean;
}

// A cited span the turn could not resolve. Reported rather than hidden: an
// assertion that silently loses its citation reads as uncited, and the reader
// cannot tell that from a brief the provider never cited at all.
function DroppedCitations({ dropped }: { dropped: DroppedCitation[] }) {
  return (
    <div className="citations dropped-citations">
      {dropped.map((citation, index) => (
        <span
          className="citation-chip citation-chip-dropped"
          key={`${citation.reason}-${citation.start}-${index}`}
          title={`"${citation.text}" — cited, but the source could not be resolved (${citation.reason})`}
        >
          unresolved
        </span>
      ))}
    </div>
  );
}

// The level the turn was enforced at, read from the server rather than from the
// selector: the two differ whenever the requested level is not one this
// deployment declares, and what a reader needs is the level that applied.
//
// It says what was enforced and nothing about what that withheld. srd010 R5.3
// bars a suppressed count here, because "four documents exist that you may not
// see" is information about the restricted corpus, derivable per tasking, and
// an analyst who can vary the tasking maps what is there without reading any of
// it.
function EnforcedClearance({ level }: { level: string }) {
  return (
    <span className="clearance-enforced" title="The clearance level this turn was enforced at">
      enforced at {level}
    </span>
  );
}

// The analyst's clearance for the next turn. It reaches the mesh as a request
// header, which the chatbot treats as the authenticated request context; a
// deployment with real authentication sets that header itself and this control
// goes away with the two-panel INTSUM interface (srd016).
function ClearanceSelector({
  value,
  onChange,
  disabled,
}: {
  value: ClearanceLevel;
  onChange: (level: ClearanceLevel) => void;
  disabled: boolean;
}) {
  return (
    <label className="clearance-select" title="Retrieval is filtered to this level before the search runs">
      {clearanceSelectLabel()}
      <select
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value as ClearanceLevel)}
      >
        {CLEARANCE_LEVELS.map((level) => (
          <option key={level} value={level}>
            {levelLabel(level)}
          </option>
        ))}
      </select>
    </label>
  );
}

// One facet vocabulary as a row of toggle chips. A chip narrows the next turn
// and never widens it: the selections become conjuncts under the clearance
// window at the retrieval tier (srd016 R4.3).
// The source-type facet renders as checkboxes rather than toggle chips
// (GH-379): a chip reads as a tag, and a multi-select constraint the analyst
// composes should look like one. The selection and its wire payload are
// unchanged.
// The feed stamps each highlight with the mesh's own event time, so a slow
// step is visible as a gap rather than inferred.
function shortActivityTime(timestamp?: string): string {
  if (!timestamp) return "";
  const value = new Date(timestamp);
  return Number.isNaN(value.valueOf())
    ? ""
    : value.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

function FacetCheckboxes({
  label,
  title,
  options,
  selected,
  onToggle,
  disabled,
}: {
  label: string;
  title: string;
  options: readonly string[];
  selected: string[];
  onToggle: (value: string) => void;
  disabled: boolean;
}) {
  if (options.length === 0) return null;
  return (
    <div className="facet-row" title={title}>
      <span className="facet-label">{label}</span>
      {options.map((option) => (
        <label key={option} className="facet-check">
          <input
            type="checkbox"
            checked={selected.includes(option)}
            onChange={() => onToggle(option)}
            disabled={disabled}
          />
          <span>{option}</span>
        </label>
      ))}
    </div>
  );
}

// The retrieval facets beside the clearance selector (srd016 R4.1). The source
// types are read from the corpus the deployment serves, under the analyst's
// clearance, so a different corpus offers different facets without a rebuild
// (R4.5); the reliability grades are the fixed Admiralty scale. A failed
// vocabulary read leaves the source-type row out and the keyword and rating
// facets standing -- the boundary being unreachable is no reason to lose the
// facets that need no vocabulary.
function FacetControls({
  facets,
  onChange,
  sourceTypes,
  disabled,
  analystFloor,
  onAnalystFloor,
  readerClearance,
  onReaderClearance,
  clearanceSlot,
}: {
  facets: Facets;
  onChange: (facets: Facets) => void;
  sourceTypes: string[];
  disabled: boolean;
  analystFloor: string;
  onAnalystFloor: (floor: string) => void;
  readerClearance: string;
  onReaderClearance: (level: string) => void;
  clearanceSlot?: React.ReactNode;
}) {
  function toggle(field: "sourceTypes", value: string) {
    const current = facets[field];
    const next = current.includes(value)
      ? current.filter((entry) => entry !== value)
      : [...current, value];
    onChange({ ...facets, [field]: next });
  }

  return (
    <div className="facets">
      <input
        className="facet-keyword"
        type="text"
        value={facets.keyword}
        placeholder="keyword"
        title="Retrieval admits only chunks containing this text, matched before the search runs"
        onChange={(e) => onChange({ ...facets, keyword: e.target.value })}
        disabled={disabled}
      />
      <FacetCheckboxes
        label="source"
        title="Retrieval admits only these source types, filtered before the search runs"
        options={sourceTypes}
        selected={facets.sourceTypes}
        onToggle={(value) => toggle("sourceTypes", value)}
        disabled={disabled}
      />
      {/* Reliability is one control, the analyst's: it narrows retrieval
          (the same facet the chips built). Who the brief is for is a
          clearance question, so the reader selector below carries that role
          (GH-378) -- the composed brief cites only sources at or below the
          reader's level, with withheld spans reported. */}
      <label className="facet-floor" title={reliabilityFloorTitle()}>
        <span>{reliabilityFloorLabel()}</span>
        <select
          value={analystFloor}
          onChange={(e) => onAnalystFloor(e.target.value)}
          disabled={disabled}
        >
          <option value="">any</option>
          {ADMIRALTY_RELIABILITY.map((grade) => (
            <option key={grade} value={grade}>
              {grade} or better
            </option>
          ))}
        </select>
      </label>
      {/* The two clearance dropdowns are one vocabulary with two roles --
          what the analyst may retrieve and what the reader may be shown --
          so they sit adjacent with parallel labels (GH-456). */}
      {clearanceSlot}
      <label className="facet-floor" title="Who this brief is being prepared for. The composed brief cites only sources at or below this level; spans above it are withheld and reported. The requester's own level still governs retrieval.">
        <span>{readerSelectLabel()}</span>
        <select
          value={readerClearance}
          onChange={(e) => onReaderClearance(e.target.value)}
          disabled={disabled}
        >
          <option value="">no stated reader</option>
          {CLEARANCE_LEVELS.map((level) => (
            <option key={level} value={level}>
              {levelLabel(level)}
            </option>
          ))}
        </select>
      </label>
    </div>
  );
}

// The audit loop /audit and /refine share (GH-543, GH-650): each sentence is
// judged whole against the chunks it cites by the classifier tier; a sentence
// carrying no citation is an unverifiable finding without a model call.
async function auditBrief(
  answer: Answer | undefined,
  clearance: ClearanceLevel,
  cap: number,
): Promise<AuditReport> {
  const text = answer?.text ?? "";
  // The cap spends itself on sentences that can be judged: cited sentences
  // first (document order kept within each class), uncited ones fill the
  // remainder as unverifiable rows (GH-654).
  const all = auditTargets(text, answer?.citations ?? []);
  const units = [
    ...all.filter((unit) => unit.recordIDs.length > 0),
    ...all.filter((unit) => unit.recordIDs.length === 0),
  ].slice(0, cap);
  const needed = [...new Set(units.flatMap((unit) => unit.recordIDs))];
  const chunkText = new Map<string, string>();
  if (needed.length > 0) {
    try {
      for (const doc of await fetchDocumentsByIds(needed, clearance)) {
        chunkText.set(doc.id, doc.text);
      }
    } catch {
      // Absent chunks leave their sentences unverifiable below.
    }
  }
  const findings: AuditFinding[] = [];
  let sum = 0;
  let scored = 0;
  for (const unit of units) {
    const documents = unit.recordIDs
      .map((recordID) => chunkText.get(recordID))
      .filter((chunk): chunk is string => Boolean(chunk));
    if (unit.recordIDs.length === 0 || documents.length === 0) {
      findings.push({
        sentence: unit.sentence,
        recordIDs: unit.recordIDs,
        reason:
          unit.recordIDs.length === 0 ? "no citation — unverifiable" : "cited text unavailable at this clearance",
        unverifiable: true,
      });
      continue;
    }
    try {
      const { verdict, traceId } = await sendAudit(unit.sentence, documents.join("\n\n---\n\n"));
      const match = verdict.match(/^\s*(10|[1-9])/);
      const score = match ? Number(match[1]) : undefined;
      if (score !== undefined) {
        sum += score;
        scored += 1;
      }
      findings.push({
        sentence: unit.sentence,
        recordIDs: unit.recordIDs,
        score,
        reason: verdict.replace(/^\s*(10|[1-9])[.:\s—-]*/, "").trim() || verdict,
        unverifiable: false,
        traceId,
      });
    } catch (err) {
      findings.push({
        sentence: unit.sentence,
        recordIDs: unit.recordIDs,
        reason: `audit failed: ${err instanceof Error ? err.message : String(err)}`,
        unverifiable: true,
      });
    }
  }
  return {
    findings,
    scored,
    targets: units.length,
    average: scored > 0 ? (sum / scored).toFixed(1) : "0.0",
  };
}


function historyFor(messages: Message[]): Turn[] {
  return messages
    .filter((m) => !m.error)
    .map((m) => ({ role: m.role, content: m.content }));
}

// Composed sources are what the server retrieved and fed to the answer prompt,
// reported per source with the number of chunks it contributed. A source that
// composed zero documents is shown too: it took part in the turn and
// contributed nothing, which is worth seeing rather than hiding.
function ComposedSources({ sources }: { sources: ComposedSource[] }) {
  return (
    <div className="sources">
      {sources.map((source) => (
        <span
          className="source-chip"
          key={source.name}
          title="RAG source that took part in composing this answer"
        >
          {source.name} · {source.documents} {source.documents === 1 ? "document" : "documents"}
        </span>
      ))}
    </div>
  );
}

// Citations the composition provider reported, each naming the record a span
// was computed against. Unlike the bracket tokens this replaced, a chip here is
// a source the reader can open: the provider computes citations against the
// documents it was given rather than writing them into its prose, so a chip
// exists only when a retrieved chunk backs the span.
function Citations({ answer, citations }: { answer: string; citations: Citation[] }) {
  return (
    <div className="citations">
      {citations.map((citation, index) => {
        // An external record is open-source material from the search branch,
        // marked so it never reads as cleared internal reporting (srd011
        // R7.3, R7.4).
        const external = isExternalRecordID(citation.record_id);
        return (
          <span
            className={external ? "citation-chip citation-chip-external" : "citation-chip"}
            key={`${citation.record_id}-${citation.start}-${index}`}
            title={
              external
                ? `"${citedSpan(answer, citation)}" — cited to open-source result ${citation.record_id} (unclearanced; not internal reporting)`
                : `"${citedSpan(answer, citation)}" — cited to ${citation.record_id}`
            }
          >
            {external ? "OPEN SOURCE · " : ""}
            {citation.record_id}
          </span>
        );
      })}
    </div>
  );
}

function describeReason(reason: AttributionReason): string {
  const named = "sources" in reason ? reason.sources.join(", ") : "";
  switch (reason.kind) {
    case "none-composed":
      return "no source took part in composing this answer";
    case "no-documents":
      return `${named} composed but retrieved no documents, which is what an unseeded corpus looks like`;
    case "query-failed":
      return `query failed: ${named}`;
    case "embedding-model-excluded":
      return `excluded for an embedding-model mismatch: ${named}`;
    case "not-selected":
      return `not selected by the source router: ${named}`;
  }
}

// An unattributed turn says which source outcome explains it. Collapsing every
// cause into one sentence is what made the panel disagree with the mesh.
function Unattributed({ reasons }: { reasons: AttributionReason[] }) {
  return (
    <div className="unattributed">
      <span className="degraded" title="No retrieved document reached this answer">
        unattributed answer
      </span>
      <ul className="reason-list">
        {reasons.map((reason) => (
          <li key={reason.kind}>{describeReason(reason)}</li>
        ))}
      </ul>
    </div>
  );
}

function SearchResults({ results }: { results: SearchResult[] }) {
  if (results.length === 0) {
    return <div className="search-results-empty">No chunks retrieved at this clearance.</div>;
  }
  return (
    <ol className="search-results" aria-label="Search results">
      {results.map((row) => (
        <li className="search-result" key={row.id}>
          <div className="search-result-head">
            <span className="search-result-id">{row.id}</span>
            <span className="search-result-source">{row.source}</span>
            {row.clearance && <span className="search-result-clearance">{row.clearance}</span>}
            {row.reliability && <span className="search-result-reliability">{row.reliability}</span>}
          </div>
          <div className="search-result-text">{row.text}</div>
        </li>
      ))}
    </ol>
  );
}

function MessageBubble({ msg, onTrace }: { msg: Message; onTrace?: (traceId: string) => void }) {
  const cls = msg.error
    ? "bubble bubble-error"
    : msg.role === "user"
      ? "bubble bubble-user"
      : "bubble bubble-assistant";
  return (
    <div className={`bubble-row bubble-row-${msg.role}`}>
      <div className={cls}>
        <div className="bubble-text">{msg.content}</div>
        {msg.traceId && onTrace && (
          <button
            type="button"
            className="bubble-trace"
            title="Open the agent timeline behind this answer"
            onClick={() => onTrace(msg.traceId as string)}
          >
            trace
          </button>
        )}
        {msg.searchResults && <SearchResults results={msg.searchResults} />}
        {msg.helpFigure && <TurnFigure />}
        {/* Attribution chrome belongs to server turns only (GH-656): a local
            status line carries neither attribution nor citations and shows
            no meta area, so it cannot read as an unattributed answer. */}
        {msg.role === "assistant" &&
          !msg.error &&
          !msg.searchResults &&
          !msg.helpFigure &&
          (msg.attribution !== undefined || (msg.citations?.length ?? 0) > 0) && (
          <div className="bubble-meta">
            {msg.attribution?.attributed ? (
              <ComposedSources sources={msg.attribution.composed} />
            ) : (
              <Unattributed reasons={msg.attribution?.reasons ?? [{ kind: "none-composed" }]} />
            )}
            {msg.clearanceEnforcedAt && <EnforcedClearance level={msg.clearanceEnforcedAt} />}
            {msg.citations && msg.citations.length > 0 && (
              <Citations answer={msg.content} citations={msg.citations} />
            )}
            {msg.droppedCitations && msg.droppedCitations.length > 0 && (
              <DroppedCitations dropped={msg.droppedCitations} />
            )}
          </div>
        )}
      </div>
    </div>
  );
}

export default function ChatPanel({ traceBackend }: PanelProps) {
  const [messages, setMessages] = useState<Message[]>([]);
  // The conversation's identity in the client-side history (GH-509).
  const [chatId, setChatId] = useState(() => newChatId());
  // The trace overlay (GH-513): a turn's timeline, opened from its bubble.
  const [traceOverlay, setTraceOverlay] = useState<string | undefined>(undefined);
  const [chatStartedAt, setChatStartedAt] = useState(() => Date.now());
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [clearance, setClearance] = useState<ClearanceLevel>(DEFAULT_CLEARANCE);
  const [facets, setFacets] = useState<Facets>(EMPTY_FACETS);
  // Reliability is the analyst's alone -- a retrieval facet (GH-357, GH-378).
  const [analystFloor, setAnalystFloor] = useState("");
  // Who the brief is being prepared for (GH-378): the composed brief cites
  // only sources at or below this level, and the analyst's own clearance
  // still governs what retrieval may reach.
  const [readerClearance, setReaderClearance] = useState("");
  // What the mesh is doing while a turn composes (GH-380), streamed from the
  // chatbot's own monitor rather than guessed from elapsed time.

  const [sourceTypes, setSourceTypes] = useState<string[]>([]);
  // The latest completed turn and what the panel knows about it, held for the
  // INTSUM column. A transport error completes no turn and leaves the previous
  // form standing -- the chat list shows the failure.
  const [latestAnswer, setLatestAnswer] = useState<Answer | undefined>(undefined);
  // The last /audit or /refine pass's sentence report (GH-650); cleared when
  // the next command runs so the table always describes the current brief.
  const [auditReport, setAuditReport] = useState<AuditReport | undefined>(undefined);
  // The aside's two tabs (GH-660): the report and the audit, flipped rather
  // than scrolled. /audit lands on Audit; every other command returns to
  // Report as it clears the stale audit.
  const [asideTab, setAsideTab] = useState<"report" | "audit">("report");
  // The report language (GH-670): a rendition of the approved English text.
  // Export and the audit stay on the English record.
  const [reportLanguage, setReportLanguage] = useState("en");
  const [translated, setTranslated] = useState<Answer | undefined>(undefined);
  const [translateTraceId, setTranslateTraceId] = useState<string | undefined>(undefined);
  const [translating, setTranslating] = useState(false);
  // Returns the translate run's trace id so the /translate turn can carry it
  // into the chat history the Traces overlay indexes (GH-720).
  async function switchLanguage(code: string): Promise<string | undefined> {
    setReportLanguage(code);
    if (code === "en" || !latestAnswer?.text) {
      setTranslated(undefined);
      return undefined;
    }
    const entry = REPORT_LANGUAGES.find((candidate) => candidate.code === code);
    const label = entry?.label ?? code;
    const promptLanguage = entry?.prompt ?? label;
    setTranslating(true);
    try {
      const units = buildSegments(latestAnswer.text, latestAnswer.citations ?? []);
      const outcome = await sendTranslate(units.map((unit) => unit.sentence), promptLanguage);
      const rendition = rebuildTranslated(latestAnswer.text, units, outcome.translations);
      setTranslated({ ...latestAnswer, text: rendition.text, citations: rendition.citations });
      setTranslateTraceId(outcome.traceId);
      return outcome.traceId;
    } catch (err) {
      setTranslated(undefined);
      setTranslateTraceId(undefined);
      setReportLanguage("en");
      setMessages((prev) => [
        ...prev,
        {
          role: "assistant",
          content: `Translation to ${label} failed: ${err instanceof Error ? err.message : String(err)}`,
          error: true,
        },
      ]);
    } finally {
      setTranslating(false);
    }
    return undefined;
  }
  // The tasking the document on screen answers, retained so a settings
  // change can regenerate it (GH-456).
  const [lastTasking, setLastTasking] = useState("");
  const [latestContext, setLatestContext] = useState<TurnContext>({});
  // The record the provenance walk is open on, or undefined for the form. The
  // walk lives in the same column as the form because it is what a citation in
  // the form resolves to, not a destination of its own (srd016 R6.1).
  const [provenance, setProvenance] = useState<
    { recordID: string; assertions: string[] } | undefined
  >(undefined);
  const listRef = useRef<HTMLDivElement>(null);
  const { startTurn, endTurn } = useTurns();

  // Restore a chat the sidebar opened, or reset for a fresh one (GH-509).
  useEffect(
    () =>
      subscribeOpen((stored: StoredChat | undefined) => {
        if (!stored) {
          setChatId(newChatId());
          setChatStartedAt(Date.now());
          setMessages([]);
          setLatestAnswer(undefined);
          setLastTasking("");
          setLatestContext({});
          setProvenance(undefined);
          return;
        }
        setChatId(stored.id);
        setChatStartedAt(stored.startedAt);
        setMessages((stored.messages as Message[]) ?? []);
        setLatestAnswer(stored.latestAnswer as Answer | undefined);
        setLastTasking(stored.lastTasking ?? "");
        setLatestContext((stored.latestContext as TurnContext) ?? {});
        setProvenance(undefined);
      }),
    [],
  );

  // Autosave: every message change persists the conversation, titled by its
  // first tasking, so a reload keeps the list and each chat's progress.
  useEffect(() => {
    if (messages.length === 0) return;
    const firstUser = messages.find((msg) => msg.role === "user");
    saveChat({
      id: chatId,
      title: (firstUser?.content ?? "untitled chat").slice(0, 80),
      startedAt: chatStartedAt,
      messages,
      latestAnswer,
      lastTasking,
      latestContext,
    });
  }, [messages, chatId, chatStartedAt, latestAnswer, lastTasking, latestContext]);

  // The source-type vocabulary is clearance-scoped: the read runs at the
  // analyst's level, so changing the selector refetches and the options are
  // only values from records the analyst could reach. Selections outside the
  // refreshed vocabulary are dropped rather than kept invisible and active.
  useEffect(() => {
    const controller = new AbortController();
    fetchSourceTypes(clearance, controller.signal)
      .then((types) => {
        setSourceTypes(types);
        setFacets((current) => ({
          ...current,
          sourceTypes: current.sourceTypes.filter((type) => types.includes(type)),
        }));
      })
      .catch(() => setSourceTypes([]));
    return () => controller.abort();
  }, [clearance]);

  // A changed retrieval control regenerates the document on screen (GH-456):
  // the retained tasking re-runs as a research turn under the new settings,
  // debounced so a burst of clicks costs one turn, and never while a turn is
  // in flight. The replayed /make-brief command appears in the window, so the
  // regeneration is visible rather than silent.
  const settingsLive = useRef(false);
  useEffect(() => {
    if (!settingsLive.current) {
      settingsLive.current = true;
      return;
    }
    if (!lastTasking || !latestAnswer || busy) return;
    const timer = setTimeout(() => {
      void submit(`/make-brief ${lastTasking}`);
    }, 700);
    return () => clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [facets, clearance, readerClearance]);

  function scrollToEnd() {
    requestAnimationFrame(() => {
      const el = listRef.current;
      if (el) el.scrollTop = el.scrollHeight;
    });
  }

  // The feed lives only for the pending turn: it opens when the turn starts
  // and closes when the answer lands, so a finished brief is not read beside
  // a stale progress log.
  const activity = useTurnActivity("chatbot", CHAT_MILESTONES, busy);

  async function submit(overrideRaw?: string) {
    const raw = (overrideRaw ?? input).trim();
    if (!raw || busy) return;
    // The command router (GH-449): explicit /commands win, plain text takes
    // the defaulting rules -- an empty window researches, a window holding a
    // brief revises it. Parsing here is the whole router; no model routes.
    const parsed = parseCommand(raw, Boolean(latestAnswer?.text));
    if (isParseError(parsed)) {
      setMessages((prev) => [
        ...prev,
        { role: "user", content: raw },
        { role: "assistant", content: parsed.error, error: true },
      ]);
      setInput("");
      scrollToEnd();
      return;
    }
    const message = parsed.argument;
    const priorHistory = historyFor(messages);
    const userMsg: Message = { role: "user", content: raw };
    setMessages((prev) => [...prev, userMsg]);
    setInput("");
    if (parsed.command !== "audit") {
      setAuditReport(undefined);
      setAsideTab("report");
      setTranslated(undefined);
      setTranslateTraceId(undefined);
      setReportLanguage("en");
    }

    if (parsed.command === "help") {
      // No server call: the figure is the help (GH-497).
      setMessages((prev) => [...prev, { role: "assistant", content: "", helpFigure: true }]);
      scrollToEnd();
      return;
    }
    if (parsed.command === "export") {
      try {
        // The PDF carries the whole form — banner, header, composed sections,
        // source evaluation, citation index — not only the prose (GH-599).
        const bytes = await exportBrief(
          latestAnswer ? exportDocument(latestAnswer, latestContext) : "",
        );
        const blob = new Blob([bytes.buffer as ArrayBuffer], { type: "application/pdf" });
        const url = URL.createObjectURL(blob);
        // Opened in a tab rather than downloaded (GH-461); the viewer's own
        // save control keeps the download path.
        window.open(url, "_blank", "noopener");
        setTimeout(() => URL.revokeObjectURL(url), 30_000);
        setMessages((prev) => [...prev, { role: "assistant", content: "Exported the brief as intsum.pdf — opened in a new tab." }]);
      } catch (err) {
        const detail = err instanceof Error ? err.message : String(err);
        setMessages((prev) => [...prev, { role: "assistant", content: `Export failed: ${detail}`, error: true }]);
      }
      scrollToEnd();
      return;
    }

    setBusy(true);
    scrollToEnd();

    // Record the turn window so the observability panel can correlate the mesh's
    // events to this turn.
    const turnId = startTurn(raw);
    let traceId: string | undefined;
    try {
      if (parsed.command === "search") {
        const found = await sendSearch(message, clearance);
        setMessages((prev) => [
          ...prev,
          {
            role: "assistant",
            content: `Retrieved ${found.results.length} chunk(s) for "${found.query}" at ${found.clearanceEnforcedAt ?? "the enforced level"}.`,
            searchResults: found.results,
            traceId: found.traceId,
          },
        ]);
        return;
      }
      if (parsed.command === "eval") {
        // The /eval command (srd014 R8): a demo-sized slice of the generated
        // reference corpus, driven through the same shipped chat boundary the
        // harness uses, recall measured at the composition set. First-N, never
        // re-sampled; the bubble names corpus, version, slice, and clearance,
        // and carries the synthetic-corpus label itself (R6.1).
        const slice = Math.max(1, Math.min(
          parsed.argument ? Number(parsed.argument) : 3, evalCorpus.taskings.length));
        const chunkSuffix = /-(atomic|medium-900|large-1800)-\d+$/i;
        const lines: string[] = [];
        let sum = 0;
        let failed = 0;
        for (const tasking of evalCorpus.taskings.slice(0, slice)) {
          const level = (CLEARANCE_LEVELS as readonly string[]).includes(tasking.clearance)
            ? (tasking.clearance as ClearanceLevel)
            : CLEARANCE_LEVELS[CLEARANCE_LEVELS.length - 1];
          try {
            const turn = await sendChat({ message: tasking.text, history: [] }, undefined, level);
            const composed = new Set(
              (turn.compositionDocuments ?? []).map((id) => id.replace(chunkSuffix, "")));
            const hits = tasking.relevant.filter((doc) => composed.has(doc));
            const recall = hits.length / tasking.relevant.length;
            sum += recall;
            lines.push(`${tasking.id} · recall ${recall.toFixed(2)} (${hits.length}/${tasking.relevant.length} references composed, ${level})`);
          } catch (err) {
            failed += 1;
            lines.push(`${tasking.id} · FAILED: ${err instanceof Error ? err.message : String(err)}`);
          }
        }
        const scored = slice - failed;
        const average = scored > 0 ? sum / scored : 0;
        setMessages((prev) => [
          ...prev,
          {
            role: "assistant",
            content:
              `Retrieval recall ${average.toFixed(3)} over the first ${slice} of ${evalCorpus.taskings.length} taskings` +
              ` (${evalCorpus.id} v${evalCorpus.version}, threshold 0.90)` +
              (failed ? `, ${failed} failed` : "") +
              `.\n${lines.join("\n")}\n` +
              `Measured over a synthetic demonstration corpus; the figures are method evidence, not operational performance.`,
          },
        ]);
        return;
      }
      if (parsed.command === "clearance") {
        // The /clearance command (GH-678): the selector's own state, driven
        // from the chat line; the next tasking runs under it.
        const level = parsed.argument.trim().toLowerCase() as ClearanceLevel;
        setClearance(level);
        setMessages((prev) => [
          ...prev,
          {
            role: "assistant",
            content: `Analyst clearance set to ${level} — rerun the tasking to regenerate the brief under it.`,
          },
        ]);
        return;
      }
            if (parsed.command === "translate") {
        // The /translate command (GH-676): the selector's own rendition path,
        // driven from the chat line.
        const wanted = parsed.argument.trim().toLowerCase();
        const entry = REPORT_LANGUAGES.find(
          (candidate) => candidate.code === wanted || candidate.label.toLowerCase() === wanted,
        );
        if (!entry || entry.code === "en") {
          setMessages((prev) => [
            ...prev,
            {
              role: "assistant",
              content:
                entry?.code === "en"
                  ? "The English record is already on screen."
                  : `Unknown language ${parsed.argument} — pick one of ${REPORT_LANGUAGES.slice(1)
                      .map((candidate) => candidate.label)
                      .join(", ")}.`,
              error: entry?.code !== "en",
            },
          ]);
          return;
        }
        traceId = await switchLanguage(entry.code);
        setMessages((prev) => [
          ...prev,
          {
            role: "assistant",
            content:
              `Rendered the brief in ${entry.label} on the Report tab — a machine translation of the ` +
              "English record, citations and gradings intact; the English record stays authoritative.",
            traceId,
          },
        ]);
        return;
      }
            if (parsed.command === "audit") {
        // The /audit command (GH-543, GH-650): every sentence of the brief is
        // judged whole against the chunks it cites; the report table renders
        // beside the brief, weakest first.
        const cap = Math.max(1, Math.min(parsed.argument ? Number(parsed.argument) : 12, 20));
        const audit = await auditBrief(latestAnswer, clearance, cap);
        setAuditReport(audit);
        setAsideTab("audit");
        const weak = audit.findings.filter(
          (finding) => !finding.unverifiable && finding.score !== undefined && finding.score < 8,
        ).length;
        const unverifiable = audit.findings.filter((finding) => finding.unverifiable).length;
        // The summary bubble carries the first judge run's trace so the audit
        // shows up in the Traces overlay; the table's per-row buttons open the
        // rest (GH-720).
        traceId = audit.findings.find((finding) => finding.traceId)?.traceId;
        setMessages((prev) => [
          ...prev,
          {
            role: "assistant",
            content:
              `Citation audit: ${audit.scored} of ${audit.targets} sentences scored, average ${audit.average}/10 ` +
              `(judge: the Cohere classifier tier, each sentence against its cited chunks). ` +
              `${weak} weak, ${unverifiable} unverifiable — the audit table beside the brief lists them weakest first.`,
            traceId,
          },
        ]);
        return;
      }
      if (parsed.command === "refine") {
        // The /refine command (GH-621, GH-629): the client-side pass of the
        // audit-driven loop (#613), now iterable. Each pass audits the
        // current brief with the shipped judge; findings below the bar become
        // a revision note the shipped revise path runs, so every pass
        // inherits the marking floor (GH-594) and the changed-text tint
        // (GH-605). A clean audit ends the loop early: the brief stands.
        const passes = Math.max(1, Math.min(parsed.argument ? Number(parsed.argument) : 1, 3));
        let current = latestAnswer;
        for (let pass = 1; pass <= passes; pass++) {
          const audit = await auditBrief(current, clearance, 12);
          setAuditReport(audit);
          const weak = audit.findings.filter(
            (finding) => !finding.unverifiable && finding.score !== undefined && finding.score < 8,
          );
          const unverified = audit.findings.filter(
            (finding) =>
              finding.unverifiable && !/not available in the supplied documents/i.test(finding.sentence),
          );
          const gaps = /not available in the supplied documents/i.test(current?.text ?? "");
          const clean = weak.length === 0 && !gaps;
          setMessages((prev) => [
            ...prev,
            {
              role: "assistant",
              content:
                `Refine pass ${pass}/${passes}: audited ${audit.scored} of ${audit.targets} sentences, average ${audit.average}/10.\n` +
                (clean
                  ? "Every audited sentence clears the bar and no gaps are declared — the brief stands."
                  : `Revising against ${weak.length} weak sentence(s)${gaps ? " and the declared gaps" : ""}…`),
            },
          ]);
          if (clean) {
            break;
          }
          const findingsText = weak
            .map(
              (finding, index) =>
                `${index + 1}. "${finding.sentence.slice(0, 160)}" (cited to ${finding.recordIDs.join(", ")}, ` +
                `scored ${finding.score}/10: ${finding.reason.slice(0, 120)})`,
            )
            .join("\n");
          const unverifiedText =
            unverified.length > 0
              ? "\nThese sentences carry no verifiable citation:\n" +
                unverified
                  .slice(0, 3)
                  .map((finding) => `- "${finding.sentence.slice(0, 120)}"`)
                  .join("\n")
              : "";
          const note =
            "An audit judged these sentences weakly supported by their cited documents:\n" +
            (findingsText || "(none)") +
            unverifiedText +
            (gaps ? "\nThe brief also declares gaps where material was not available.\n" : "\n") +
            "Rewrite each weak sentence so the cited reporting directly supports it, re-ground or drop " +
            "any claim that cannot be supported, fill declared gaps where the corpus allows, and keep " +
            "everything else unchanged.";
          const answer: Answer = await sendChat(
            {
              message: note,
              history: priorHistory,
              facets: facetsPayload(facets),
              ...revisionFields(current),
              ...(clearanceRank(readerClearance) !== undefined
                ? { reader_clearance_rank: clearanceRank(readerClearance) }
                : {}),
            },
            undefined,
            clearance,
          );
          traceId = answer.traceId;
          if (current) {
            answer.priorBanner = higherBanner(
              highestClearance(composedChunkMetadata(current)),
              current.priorBanner,
            );
            answer.priorText = current.text;
          }
          setLatestAnswer(answer);
          setLatestContext(turnContextFor(note, new Date()));
          setProvenance(undefined);
          setMessages((prev) => [
            ...prev,
            {
              role: "assistant",
              content: answer.text,
              attribution: answer.attribution,
              citations: answer.citations,
              droppedCitations: answer.droppedCitations,
              clearanceEnforcedAt: answer.clearanceEnforcedAt,
              traceId: answer.traceId,
            },
          ]);
          scrollToEnd();
          current = answer;
        }
        return;
      }
      if (parsed.command === "harvest") {
        const receipt = await sendHarvest(message);
        const lines = receipt.harvested
          .map((row) => `${row.id} · ${row.title}`)
          .join("\n");
        setMessages((prev) => [
          ...prev,
          {
            role: "assistant",
            content:
              receipt.harvested.length === 0
                ? `Nothing to harvest for ${receipt.queries.map((q) => `"${q}"`).join(" · ")}.`
                : `Harvested ${receipt.harvested.length} web result(s) into the scratch corpus — the model proposed ${receipt.queries.length} ${receipt.queries.length === 1 ? "query" : "queries"} by tool call: ${receipt.queries.map((q) => `"${q}"`).join(" · ")}` +
                  (receipt.failedEmbeds ? ` (${receipt.failedEmbeds} failed to embed)` : "") +
                  ` — retrievable at unclassified on the next research turn.\n${lines}`,
            traceId: receipt.traceId,
          },
        ]);
        return;
      }
      if (parsed.command === "summarize") {
        const summary = await sendSummarize(latestAnswer?.text ?? "");
        setMessages((prev) => [...prev, { role: "assistant", content: summary }]);
        return;
      }
      const answer: Answer = await sendChat(
        {
          message,
          history: priorHistory,
          facets: facetsPayload(facets),
          ...(parsed.command === "revise" ? revisionFields(latestAnswer) : {}),
          ...(clearanceRank(readerClearance) !== undefined
            ? { reader_clearance_rank: clearanceRank(readerClearance) }
            : {}),
        },
        undefined,
        clearance,
      );
      traceId = answer.traceId;
      // A revision inherits the marking of the brief it started from (GH-594):
      // the fused prose can carry material the new retrieval did not recompose,
      // and the banner must never read lower than that material warrants.
      // Chained revisions fold the whole ancestry through the prior answer's
      // own priorBanner.
      if (parsed.command === "revise" && latestAnswer) {
        answer.priorBanner = higherBanner(
          highestClearance(composedChunkMetadata(latestAnswer)),
          latestAnswer.priorBanner,
        );
        // The prior text is what the changed-text tint compares against
        // (GH-605): the immediate predecessor, not the whole ancestry — the
        // analyst asks what THIS note changed.
        answer.priorText = latestAnswer.text;
      }
      setLatestAnswer(answer);
      if (parsed.command === "make-brief") setLastTasking(message);
      setLatestContext(turnContextFor(message, new Date()));
      // A new turn produces a new form; a walk left open would show provenance
      // for a brief no longer on screen.
      setProvenance(undefined);
      setMessages((prev) => [
        ...prev,
        {
          role: "assistant",
          content: answer.text,
          attribution: answer.attribution,
          citations: answer.citations,
          droppedCitations: answer.droppedCitations,
          clearanceEnforcedAt: answer.clearanceEnforcedAt,
          traceId: answer.traceId,
        },
      ]);
    } catch (err) {
      const detail = err instanceof Error ? err.message : String(err);
      setMessages((prev) => [
        ...prev,
        { role: "assistant", content: `Request failed: ${detail}`, error: true },
      ]);
    } finally {
      endTurn(turnId, traceId);
      setBusy(false);
      scrollToEnd();
    }
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void submit();
    }
  }

  // The chat route rendered as two columns (srd016 R1.1): the tasking, its
  // facets, and the turn history on one side, and the ministry INTSUM form the
  // turn produces on the other. A column rather than a fourth routed panel,
  // because the form is what one turn produces and not a separate destination
  // -- which is also what keeps the panel set in ui.yaml, and the registry
  // that mounts it, untouched (R1.3).
  return (
    <div className="chat-columns">
      <div className="chat">
        {/* The retrieval controls read top-down with the conversation below
            them: what the next tasking will be filtered by, then the turns,
            then the box to type it in (GH-415). */}
        <div className="chat-controls">
          <FacetControls
            facets={facets}
            onChange={setFacets}
            sourceTypes={sourceTypes}
            disabled={busy}
            analystFloor={analystFloor}
            onAnalystFloor={(floor) => {
              setAnalystFloor(floor);
              setFacets({ ...facets, reliabilities: floor ? gradesAtOrAbove(floor) : [] });
            }}
            readerClearance={readerClearance}
            onReaderClearance={setReaderClearance}
            clearanceSlot={
              <ClearanceSelector value={clearance} onChange={setClearance} disabled={busy} />
            }
          />
        </div>
        <div className="chat-list" ref={listRef}>
          {messages.length === 0 ? (
            <div className="empty">
              <TurnFigure />
            </div>
          ) : (
            messages.map((m, i) => <MessageBubble msg={m} key={i} onTrace={setTraceOverlay} />)
          )}
          {busy && (
            <div className="bubble-row bubble-row-assistant">
              <div className="bubble bubble-assistant bubble-pending">
                <span className="pending-dots">…</span>
                {activity.length > 0 && (
                  <ul className="turn-activity" aria-label="Turn activity">
                    {activity.slice(-6).map((highlight, index) => (
                      <li key={`${highlight.label}-${index}`}>
                        <span className="activity-time">{shortActivityTime(highlight.at)}</span>{" "}
                        {highlight.label}
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            </div>
          )}
        </div>
        <div className="chat-input">
          {/* The command palette (GH-449): the workflow vocabulary and the
              Cohere models each features, visible before anyone types. A chip
              prefills its command; Enter still dispatches. */}
          <div className="command-palette" aria-label="Commands">
            {COMMANDS.map((entry) => (
              <button
                type="button"
                key={entry.command}
                className="command-chip"
                title={`${entry.usage} — ${entry.describes}`}
                disabled={busy}
                onClick={() => setInput(`/${entry.command} `)}
              >
                /{entry.command}
                <span className="command-models">{entry.models}</span>
              </button>
            ))}
          </div>
          <div className="chat-input-box">
            <textarea
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={onKeyDown}
              placeholder="Message the chatbot…  (Enter to send, Shift+Enter for a newline)"
              rows={3}
              disabled={busy}
            />
            <button onClick={() => void submit()} disabled={busy || input.trim().length === 0}>
              Send
            </button>
          </div>
        </div>
      </div>
      <aside className="intsum-column">
        {provenance ? (
          <ProvenancePanel
            recordID={provenance.recordID}
            assertions={provenance.assertions}
            clearance={clearance}
            onBack={() => setProvenance(undefined)}
            onWalk={(recordID) => setProvenance({ recordID, assertions: [] })}
          />
        ) : (
          <>
            <div className="aside-tabs" role="tablist">
              <button
                type="button"
                role="tab"
                aria-selected={asideTab === "report"}
                className={`aside-tab${asideTab === "report" ? " aside-tab-active" : ""}`}
                onClick={() => setAsideTab("report")}
              >
                Report
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={asideTab === "audit"}
                className={`aside-tab${asideTab === "audit" ? " aside-tab-active" : ""}`}
                onClick={() => setAsideTab("audit")}
              >
                Audit
              </button>
            </div>
            {asideTab === "report" && (
              <>
                <div className="report-language">
                  <label>
                    language{" "}
                    <select
                      value={reportLanguage}
                      disabled={translating || !latestAnswer?.text}
                      onChange={(event) => void switchLanguage(event.target.value)}
                    >
                      {REPORT_LANGUAGES.map((entry) => (
                        <option key={entry.code} value={entry.code}>
                          {entry.label}
                        </option>
                      ))}
                    </select>
                  </label>
                  {translating && <span className="translate-note">translating…</span>}
                  {translated && !translating && (
                    <span className="translate-note">
                      machine translation — the English record is authoritative
                      {translateTraceId && (
                        <button
                          type="button"
                          className="bubble-trace"
                          title="Open the translation run's trace — the segments sent and the tool call"
                          onClick={() => setTraceOverlay(translateTraceId)}
                        >
                          trace
                        </button>
                      )}
                    </span>
                  )}
                </div>
                <IntsumPanel
                  answer={translated ?? latestAnswer}
                  context={latestContext}
                  onOpenProvenance={(recordID, assertions) => setProvenance({ recordID, assertions })}
                />
                {/* Seeded with the full export document (GH-631): the button's
                    PDF and the /export command render the same deliverable, and
                    the analyst's edits apply to the whole form. */}
                <ExportPanel
                  markdown={latestAnswer ? exportDocument(latestAnswer, latestContext) : undefined}
                />
              </>
            )}
            {asideTab === "audit" &&
              (auditReport ? (
                <AuditPanel
                  report={auditReport}
                  onOpenProvenance={(recordID, assertions) => setProvenance({ recordID, assertions })}
                  onOpenTrace={(traceId) => setTraceOverlay(traceId)}
                />
              ) : (
                <div className="audit-empty">
                  <div className="intsum-section-empty">
                    No audit yet — each sentence of the brief is judged against the chunks it cites,
                    weakest first.
                  </div>
                  <button
                    type="button"
                    className="audit-run"
                    disabled={busy || !latestAnswer?.text}
                    title={latestAnswer?.text ? "Audit the brief on screen" : "Run /make-brief first"}
                    onClick={() => void submit("/audit")}
                  >
                    run audit
                  </button>
                </div>
              ))}
          </>
        )}
      </aside>
      {traceOverlay && (
        <TraceStoryOverlay
          {...traceOptions}
          chats={storyChats()}
          currentChatId={chatId}
          focusTraceId={traceOverlay}
          backend={traceBackend}
          onClose={() => setTraceOverlay(undefined)}
        />
      )}
    </div>
  );
}
