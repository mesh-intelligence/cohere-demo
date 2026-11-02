// The corpus panel (GH-361): what is in the database, from the documents
// down. The top level is the rolled-up envelope list -- one row per original
// document the stated clearance admits, filterable by tier -- a row unravels
// to the full text the corpus holds, and a further unravel lists the chunks
// each profile made of it, in position order. Everything is served through
// the clearance-enforced read boundary: the panel shows only what the stated
// clearance admits, and says at which level it is reading.

import { useEffect, useMemo, useState } from "react";

import { CLEARANCE_LEVELS, DEFAULT_CLEARANCE, type ClearanceLevel } from "./api";
import { corpusLevelLabel, levelLabel } from "./uiConfig";
import {
  fetchCorpusEnvelopes,
  fetchDocumentVersions,
  type DocumentView,
} from "./documentsApi";
import { locateChunk, versionsByProfile } from "./provenance";

function DocumentChunks({
  documentID,
  clearance,
  onOpenChunk,
}: {
  documentID: string;
  clearance: ClearanceLevel;
  // Reports the chunk whose text is open, so the parent can mark its passage
  // inside the document text it renders above this list (GH-602).
  onOpenChunk?: (chunk?: DocumentView) => void;
}) {
  const [versions, setVersions] = useState<DocumentView[]>();
  const [error, setError] = useState<string>();
  const [openChunk, setOpenChunk] = useState<string>();

  useEffect(() => {
    let cancelled = false;
    fetchDocumentVersions(documentID, clearance)
      .then((views) => {
        if (!cancelled) setVersions(views);
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [documentID, clearance]);

  if (error) return <div className="corpus-error">chunk read failed: {error}</div>;
  if (!versions) return <div className="corpus-note">reading the chunks…</div>;
  const profiles = versionsByProfile(versions);
  if (profiles.length === 0) {
    return <div className="corpus-note">the served collection holds no chunk rows for this document</div>;
  }
  return (
    <div className="corpus-chunks">
      {profiles.map(({ profile, chunks }) => (
        <div key={profile}>
          <div className="corpus-profile-label">
            {profile} · {chunks.length} chunk{chunks.length === 1 ? "" : "s"}
          </div>
          <ul>
            {chunks.map((chunk) => (
              <li key={chunk.id}>
                <button
                  type="button"
                  className="corpus-chunk-toggle"
                  onClick={() => {
                    const next = openChunk === chunk.id ? undefined : chunk.id;
                    setOpenChunk(next);
                    onOpenChunk?.(next ? chunk : undefined);
                  }}
                >
                  {chunk.id}
                  {chunk.parent?.position !== undefined && chunk.parent?.sibling_count !== undefined
                    ? ` · chunk ${chunk.parent.position} of ${chunk.parent.sibling_count}`
                    : ""}
                </button>
                {openChunk === chunk.id && <pre className="corpus-text corpus-chunk-text">{chunk.text}</pre>}
              </li>
            ))}
          </ul>
        </div>
      ))}
    </div>
  );
}

// The document text with the opened chunk's passage marked (GH-602), located
// the same way the provenance walk locates a cited chunk — char_span first,
// exact match, then whitespace-collapsed. An unlocatable chunk renders the
// document plain rather than highlighting the wrong lines.
function DocumentBody({ text, chunk }: { text: string; chunk?: DocumentView }) {
  const span = chunk ? locateChunk(text, chunk.text, chunk.parent?.char_span) : undefined;
  if (!span) {
    return <pre className="corpus-text">{text}</pre>;
  }
  return (
    <pre className="corpus-text">
      {text.slice(0, span.start)}
      <mark className="corpus-chunk-highlight">{text.slice(span.start, span.end)}</mark>
      {text.slice(span.end)}
    </pre>
  );
}

export default function CorpusPanel() {
  const [clearance, setClearance] = useState<ClearanceLevel>(DEFAULT_CLEARANCE);
  const [envelopes, setEnvelopes] = useState<DocumentView[]>();
  const [error, setError] = useState<string>();
  const [tier, setTier] = useState("");
  const [openDocument, setOpenDocument] = useState<string>();
  const [chunksFor, setChunksFor] = useState<string>();
  const [highlighted, setHighlighted] = useState<DocumentView>();

  useEffect(() => {
    let cancelled = false;
    setEnvelopes(undefined);
    setError(undefined);
    fetchCorpusEnvelopes(clearance)
      .then((views) => {
        if (!cancelled) setEnvelopes(views);
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [clearance]);

  const tiers = useMemo(
    () => [...new Set((envelopes ?? []).map((view) => view.sourceType).filter((t): t is string => Boolean(t)))].sort(),
    [envelopes],
  );
  const rows = useMemo(() => {
    const filtered = (envelopes ?? []).filter((view) => !tier || view.sourceType === tier);
    // Newest reporting first; documents without a date sort last, together.
    return filtered.sort((a, b) => (b.issued ?? "").localeCompare(a.issued ?? ""));
  }, [envelopes, tier]);

  return (
    <div className="corpus-panel">
      <div className="corpus-controls">
        <label>
          {corpusLevelLabel()}{" "}
          <select value={clearance} onChange={(e) => setClearance(e.target.value as ClearanceLevel)}>
            {CLEARANCE_LEVELS.map((level) => (
              <option key={level} value={level}>
                {levelLabel(level)}
              </option>
            ))}
          </select>
        </label>
        <label>
          tier{" "}
          <select value={tier} onChange={(e) => setTier(e.target.value)}>
            <option value="">all</option>
            {tiers.map((entry) => (
              <option key={entry} value={entry}>
                {entry}
              </option>
            ))}
          </select>
        </label>
        {envelopes && (
          <span className="corpus-count">
            {rows.length} of {envelopes.length} documents served at {clearance}
          </span>
        )}
      </div>
      {error && <div className="corpus-error">corpus read failed: {error}</div>}
      {!envelopes && !error && <div className="corpus-note">reading the corpus…</div>}
      <ul className="corpus-list">
        {rows.map((view) => {
          const documentID = view.documentID ?? view.id;
          const open = openDocument === documentID;
          return (
            <li key={view.id} className="corpus-row">
              <button
                type="button"
                className="corpus-doc-toggle"
                onClick={() => {
                  setOpenDocument(open ? undefined : documentID);
                  setChunksFor(undefined);
                  setHighlighted(undefined);
                }}
              >
                <span className="corpus-doc-id">{documentID}</span>
                <span className="corpus-doc-meta">
                  {view.sourceType ?? "untyped"} · {view.issued ?? "undated"} ·{" "}
                  {view.clearance ?? "unmarked"}
                  {view.reliability ? ` · grade ${view.reliability}` : " · ungraded"}
                </span>
                {view.title && <span className="corpus-doc-title">{view.title}</span>}
              </button>
              {open && (
                <div className="corpus-detail">
                  <DocumentBody text={view.text} chunk={highlighted} />
                  <button
                    type="button"
                    className="corpus-chunk-unravel"
                    onClick={() => {
                      setChunksFor((was) => (was === documentID ? undefined : documentID));
                      setHighlighted(undefined);
                    }}
                  >
                    {chunksFor === documentID ? "hide the chunks" : "unravel the chunks"}
                  </button>
                  {chunksFor === documentID && (
                    <DocumentChunks
                      documentID={documentID}
                      clearance={clearance}
                      onOpenChunk={setHighlighted}
                    />
                  )}
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
