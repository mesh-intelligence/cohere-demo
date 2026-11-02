// The provenance walk (srd016 R6): from a citation to the chunk it was
// computed against, the parent document opened whole, the other chunk-profile
// versions, and the cross-tier derivation where the document carries one.
//
// The walk shows only what the read boundary served. An identifier the
// boundary answered nothing for -- unknown, or above the viewer's clearance,
// the two deliberately indistinguishable (srd016 R5.7) -- renders no node at
// all (R6.5); the panel states that nothing was served and stops.

import { useEffect, useState } from "react";

import type { ClearanceLevel } from "./api";
import {
  fetchDocumentsByIds,
  fetchDocumentVersions,
  type ChunkReference,
  type DocumentView,
} from "./documentsApi";
import { isRawSourceRef, chunkCounterLabel, envelopeOf, locateChunk, versionsByProfile } from "./provenance";

interface Walk {
  chunk?: DocumentView;
  document?: DocumentView;
  versions: DocumentView[];
}

type WalkState =
  | { phase: "loading" }
  | { phase: "failed"; detail: string }
  | { phase: "done"; walk: Walk };

// The assertions the brief rests on this record, carried in from the citation
// index so repeated citations stay one node (R6.4).
export default function ProvenancePanel({
  recordID,
  assertions,
  clearance,
  onBack,
  onWalk,
}: {
  recordID: string;
  assertions: string[];
  clearance: ClearanceLevel;
  onBack: () => void;
  onWalk: (recordID: string) => void;
}) {
  const [state, setState] = useState<WalkState>({ phase: "loading" });

  useEffect(() => {
    const controller = new AbortController();
    setState({ phase: "loading" });
    (async () => {
      const cited = await fetchDocumentsByIds([recordID], clearance, controller.signal);
      const chunk = cited.find((view) => view.id === recordID);
      // A cited envelope is already the document; a cited chunk walks up.
      const documentID = chunk?.kind === "chunk" ? chunk.parent?.document_id : chunk?.documentID;
      if (!chunk || !documentID) {
        return { chunk, versions: [] } satisfies Walk;
      }
      const versions = await fetchDocumentVersions(documentID, clearance, controller.signal);
      return {
        chunk,
        document: envelopeOf(versions, documentID),
        versions,
      } satisfies Walk;
    })()
      .then((walk) => setState({ phase: "done", walk }))
      .catch((err) => {
        if (!controller.signal.aborted) {
          setState({ phase: "failed", detail: err instanceof Error ? err.message : String(err) });
        }
      });
    return () => controller.abort();
  }, [recordID, clearance]);

  return (
    <div className="provenance">
      <div className="provenance-bar">
        <button type="button" className="provenance-back" onClick={onBack}>
          ← form
        </button>
        <span className="provenance-record">{recordID}</span>
      </div>
      {state.phase === "loading" && (
        <div className="intsum-section-empty provenance-loading">reading the record…</div>
      )}
      {state.phase === "failed" && (
        <div className="provenance-failed">the read failed: {state.detail}</div>
      )}
      {state.phase === "done" && (
        <WalkView walk={state.walk} recordID={recordID} assertions={assertions} onWalk={onWalk} />
      )}
    </div>
  );
}

function WalkView({
  walk,
  recordID,
  assertions,
  onWalk,
}: {
  walk: Walk;
  recordID: string;
  assertions: string[];
  onWalk: (recordID: string) => void;
}) {
  if (!walk.chunk) {
    // Absence, not refusal: the boundary answers the same nothing for an
    // unknown identifier and for one above the viewer's level, and the walk
    // does not narrow which (R6.5).
    return (
      <div className="intsum-section-empty">
        no record served for this identifier -- it is not in the served corpus, or it sits above
        your clearance
      </div>
    );
  }

  const span = walk.document
    ? locateChunk(walk.document.text, walk.chunk.text, walk.chunk.parent?.char_span)
    : undefined;

  return (
    <>
      {assertions.length > 0 && (
        <section className="intsum-section">
          <h3 className="intsum-heading">Assertions on this source</h3>
          <ul className="intsum-assertions">
            {assertions.map((assertion, index) => (
              <li key={index}>{assertion}</li>
            ))}
          </ul>
        </section>
      )}

      <section className="intsum-section">
        <h3 className="intsum-heading">Cited chunk</h3>
        <div className="provenance-meta">
          {walk.chunk.chunkProfile && <span>profile {walk.chunk.chunkProfile}</span>}
          {chunkCounterLabel(walk.chunk.parent) && <span>{chunkCounterLabel(walk.chunk.parent)}</span>}
          {walk.chunk.clearance && <span className="provenance-clearance">{walk.chunk.clearance}</span>}
        </div>
        <div className="intsum-body provenance-chunk">{walk.chunk.text}</div>
        <ChunkSources references={walk.chunk.references} />
      </section>

      <section className="intsum-section">
        <h3 className="intsum-heading">
          Parent document{walk.document?.title ? ` — ${walk.document.title}` : ""}
        </h3>
        {walk.document ? (
          <DocumentText text={walk.document.text} span={span} />
        ) : (
          <div className="intsum-section-empty">no document served for this chunk at your clearance</div>
        )}
      </section>

      {walk.document && walk.document.derivedFrom.length > 0 && (
        <section className="intsum-section">
          <h3 className="intsum-heading">Derived from</h3>
          <ul className="provenance-derived">
            {walk.document.derivedFrom.map((ref, index) => (
              <li key={`${ref.id}-${index}`}>
                <span className="provenance-relation">
                  {ref.relation ?? "derived"} · {ref.tier ?? "unstated tier"} ·{" "}
                </span>
                {ref.id ? (
                  isRawSourceRef(ref.id) ? (
                    // A raw source row is a provenance fact, not a served
                    // record: the documents API resolves document identifiers
                    // only, so offering a walk here could only dead-end
                    // (GH-351).
                    <span
                      className="provenance-raw-ref"
                      title="A raw source reference -- the dataset row this document was derived from. It is not a served corpus record, so there is no walk to open."
                    >
                      {ref.id}
                    </span>
                  ) : (
                    <button type="button" className="provenance-link" onClick={() => onWalk(ref.id!)}>
                      {ref.id}
                    </button>
                  )
                ) : (
                  <span className="intsum-unset">unnamed source</span>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}

      <section className="intsum-section">
        <h3 className="intsum-heading">Versions</h3>
        <Versions walk={walk} recordID={recordID} onWalk={onWalk} />
      </section>
    </>
  );
}

// The sources a cited chunk's own markers resolve to (srd017 R10.6). Addresses
// render as the tier wrote them and are not links: the tier obscures the ones
// it considers hostile to follow, and restoring them would undo that decision
// on the reader's behalf. A chunk citing nothing renders no block at all.
function ChunkSources({ references }: { references?: ChunkReference[] }) {
  if (!references || references.length === 0) {
    return null;
  }
  return (
    <div className="provenance-sources">
      <h4 className="provenance-sources-heading">Sources cited in this chunk</h4>
      <ol className="provenance-sources-list">
        {references.map((reference, index) => (
          <li key={reference.number ?? index} className="provenance-source">
            <span className="provenance-source-marker">[{reference.number}]</span>
            {reference.urls && reference.urls.length > 0 ? (
              <ul className="provenance-source-urls">
                {reference.urls.map((url, position) => (
                  <li key={position} className="provenance-source-url">{url}</li>
                ))}
              </ul>
            ) : (
              <span className="provenance-source-text">{reference.text}</span>
            )}
          </li>
        ))}
      </ol>
    </div>
  );
}

// The full document with the cited passage highlighted (R6.2, R6.3). An
// unlocatable passage renders the document plain rather than highlighting the
// wrong lines.
function DocumentText({ text, span }: { text: string; span?: { start: number; end: number } }) {
  if (!span) {
    return <div className="intsum-body provenance-document">{text}</div>;
  }
  return (
    <div className="intsum-body provenance-document">
      {text.slice(0, span.start)}
      <mark className="provenance-highlight">{text.slice(span.start, span.end)}</mark>
      {text.slice(span.end)}
    </div>
  );
}

function Versions({
  walk,
  recordID,
  onWalk,
}: {
  walk: Walk;
  recordID: string;
  onWalk: (recordID: string) => void;
}) {
  const profiles = versionsByProfile(walk.versions);
  if (profiles.length === 0) {
    return <div className="intsum-section-empty">no chunk versions served for this document</div>;
  }
  return (
    <div className="provenance-versions">
      {profiles.map(({ profile, chunks }) => (
        <div className="provenance-profile" key={profile}>
          <span className="facet-label">{profile}</span>
          {chunks.map((chunk) => (
            <button
              key={chunk.id}
              type="button"
              className={`facet-chip${chunk.id === recordID ? " facet-chip-selected" : ""}`}
              title={chunk.id}
              onClick={() => onWalk(chunk.id)}
            >
              {(chunk.parent?.position ?? 0) + 1}
            </button>
          ))}
        </div>
      ))}
    </div>
  );
}
