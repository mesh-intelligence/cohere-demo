// The analyst's edit-and-export control under the INTSUM form (srd013 R4.1,
// R4.2, GH-352). Export is the analyst's own act after the human edit the
// VISION requires, never a stage of the turn: nothing here runs until the
// button is pressed, so a slow render delays no brief reaching an analyst.
// The exported document is the brief markdown as edited here -- the exporter
// renders what it is given, conformant or not (srd012 AC4).

import { useEffect, useState } from "react";

import { ExportError, exportBrief } from "./api";

export default function ExportPanel({ markdown }: { markdown?: string }) {
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState(markdown ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [rendered, setRendered] = useState<number>();

  // A new turn's brief replaces the working copy: edits belong to the brief
  // they were made on, and carrying them across turns would export text the
  // analyst never saw against the new tasking.
  useEffect(() => {
    setText(markdown ?? "");
    setEditing(false);
    setError(undefined);
    setRendered(undefined);
  }, [markdown]);

  if (markdown === undefined) return null;

  async function submit() {
    setBusy(true);
    setError(undefined);
    setRendered(undefined);
    try {
      const bytes = await exportBrief(text);
      setRendered(bytes.length);
      const blob = new Blob([bytes.buffer as ArrayBuffer], { type: "application/pdf" });
      const url = URL.createObjectURL(blob);
      // Opened in a tab rather than downloaded (GH-461): the browser's PDF
      // viewer shows the form immediately, and its own save control keeps
      // the download path. Revoked on a delay so the viewer has read the
      // blob.
      window.open(url, "_blank", "noopener");
      setTimeout(() => URL.revokeObjectURL(url), 30_000);
    } catch (err) {
      setError(err instanceof ExportError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="export-panel">
      <div className="export-controls">
        <button
          type="button"
          className="export-edit-toggle"
          onClick={() => setEditing((was) => !was)}
          disabled={busy}
        >
          {editing ? "done editing" : "edit brief"}
        </button>
        <button
          type="button"
          className="export-submit"
          onClick={() => void submit()}
          disabled={busy || text.trim().length === 0}
          title="Render the brief markdown above -- as edited here -- to a PDF"
        >
          {busy ? "rendering…" : "export PDF"}
        </button>
        {rendered !== undefined && (
          <span className="export-result">rendered {Math.round(rendered / 1024)} KB</span>
        )}
      </div>
      {editing && (
        <textarea
          className="export-editor"
          value={text}
          onChange={(event) => setText(event.target.value)}
          rows={14}
          disabled={busy}
        />
      )}
      {text !== markdown && !editing && (
        <div className="export-edited-note">the export renders your edited copy, not the generated brief</div>
      )}
      {error && <div className="export-error">export failed: {error}</div>}
    </div>
  );
}
