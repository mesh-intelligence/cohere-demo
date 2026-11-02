// The chat empty-state figure (GH-356, GH-467, redrawn for GH-481): the
// /make-brief workflow mapped onto the INTSUM it produces -- each stage wired
// to the part of the document it writes -- with the rest of the command
// vocabulary as rows below. Soft-filled stages are Cohere model calls. One
// inline SVG, no external assets, sized by the chat column.

const LABEL = { fontSize: 9.5, fill: "#333", textAnchor: "middle" } as const;
const COMMAND = { fontSize: 10, fill: "var(--accent, #4078c0)", fontFamily: "ui-monospace, monospace" } as const;
const ARROW = { stroke: "#8a8f98", strokeWidth: 1, markerEnd: "url(#turn-arrow)" } as const;
const COHERE_FILL = "var(--accent-soft, #eef3fa)";

// The stage palette: one hue per /make-brief stage, reused by the connectors
// and the document ticks so "which stage wrote this part" is one color read.
const STAGE_COLORS = {
  embed: "#4078c0",
  select: "#3aa6b9",
  retrieve: "#2f9e77",
  rerank: "#d0a24a",
  compose: "#b07ce8",
  conformance: "#d06a6a",
} as const;

interface Stage {
  label: string;
  cohere?: boolean;
  w?: number;
}

interface Row {
  command: string;
  stages: Stage[];
  note?: string;
}

// The commands besides /make-brief, each row its machine's real stage chain:
// /search stops after retrieval, /harvest feeds the scratch corpus the next
// /make-brief reads, /revise re-runs /make-brief over the fused question,
// /summarize and /export operate on the brief on screen.
const ROWS: Row[] = [
  {
    command: "/search",
    stages: [{ label: "embed", cohere: true }, { label: "retrieve" }],
    note: "chunks with provenance, no brief",
  },
  {
    command: "/refine",
    stages: [{ label: "audit", cohere: true }, { label: "revise", w: 56, cohere: true }],
    note: "the audit findings steer the revision",
  },
  {
    command: "/harvest",
    stages: [{ label: "web search", w: 64 }, { label: "embed", cohere: true }, { label: "ingest" }],
    note: "tool-called queries, unclassified floor",
  },
  {
    command: "/revise",
    stages: [{ label: "fuse with brief", w: 78 }, { label: "re-run /make-brief", w: 112 }],
    note: "plain text with a brief showing",
  },
  {
    command: "/clearance",
    stages: [{ label: "set the analyst clearance", w: 148 }],
    note: "retrieval filters above the level before any model",
  },
  {
    command: "/translate",
    stages: [{ label: "render in a NATO language", cohere: true, w: 148 }],
    note: "citations and gradings stay; English is authoritative",
  },
  {
    command: "/summarize",
    stages: [{ label: "summarize the brief", cohere: true, w: 120 }],
  },
  {
    command: "/export",
    stages: [{ label: "render the brief as PDF", w: 152 }],
  },
  {
    command: "/eval",
    stages: [{ label: "run recall over the reference set", w: 172 }],
    note: "reports the retrieval-recall KPI here",
  },
];

const ROW_H = 34;
const BOX_H = 20;
const BOX_W = 56;
const GAP = 10;
const STAGE_X = 78;
const MAP_H = 179;

// The /make-brief pipeline column: label, y, color, whether it is a Cohere
// call, and the document part its connector lands on.
const MAP_STAGES: Array<{
  label: string;
  y: number;
  color: string;
  cohere?: boolean;
  note?: string;
}> = [
  { label: "choose sources", y: 22, color: STAGE_COLORS.select, cohere: true, note: "Command A · tool call" },
  { label: "search the corpus", y: 51, color: STAGE_COLORS.embed, cohere: true, note: "Embed · clearance-filtered" },
  { label: "rerank", y: 80, color: STAGE_COLORS.rerank, cohere: true, note: "orders and trims sources" },
  { label: "compose", y: 109, color: STAGE_COLORS.compose, cohere: true, note: "Command A writes the prose" },
  { label: "conformance", y: 138, color: STAGE_COLORS.conformance, note: "structural check, no model" },
];

// The miniature INTSUM: each part with the stage that writes it, drawn as a
// left tick in that stage's color. All five mappings are the deployed
// machine's: the banner from the composed chunks' clearance, the source list
// from what rerank let through, prose and citation marks from Command's
// structured output, the stamp from the deterministic section checks.
const DOC_X = 212;
const DOC_W = 124;
const DOC_PARTS: Array<{ label: string; y: number; h: number; color: string; sub?: string }> = [
  { label: "CLASSIFICATION BANNER", y: 22, h: 13, color: STAGE_COLORS.retrieve },
  { label: "Bottom Line Up Front", y: 41, h: 20, color: STAGE_COLORS.compose },
  { label: "Assessment", y: 67, h: 20, color: STAGE_COLORS.compose },
  { label: "Supporting Reporting", y: 93, h: 28, color: STAGE_COLORS.rerank, sub: "sources · citations [1][2]" },
  { label: "Gaps", y: 127, h: 16, color: STAGE_COLORS.compose },
  { label: "verdict: conformant", y: 149, h: 15, color: STAGE_COLORS.conformance },
];

// Connectors: one line per stage that writes a part, stage edge to part edge.
const MAP_LINKS: Array<{ from: number; to: number }> = [
  { from: 1, to: 0 }, // retrieve -> banner (the composed chunks' clearance)
  { from: 2, to: 3 }, // rerank -> Supporting Reporting (which sources survive)
  { from: 3, to: 1 }, // compose -> BLUF (and the Assessment/Gaps ticks share its color)
  { from: 4, to: 5 }, // conformance -> the stamp
];

function ResearchMap() {
  return (
    <g>
      <text x={4} y={14} {...COMMAND}>
        /make-brief
      </text>
      <text x={82} y={14} fontSize={8.5} fill="#999">
        one tasking becomes a conformant INTSUM
      </text>
      {MAP_STAGES.map((stage, index) => (
        <g key={stage.label}>
          {index > 0 && (
            <line x1={48} y1={MAP_STAGES[index - 1].y + BOX_H} x2={48} y2={stage.y - 2} {...ARROW} />
          )}
          <rect
            x={4}
            y={stage.y}
            width={88}
            height={BOX_H}
            rx={5}
            fill={stage.cohere ? COHERE_FILL : "#f6f7f8"}
            stroke={stage.color}
          />
          <text x={48} y={stage.y + 13.5} {...LABEL}>
            {stage.label}
          </text>
          {stage.note && (
            <text x={48} y={stage.y + BOX_H + 9} fontSize={7} fill="#999" textAnchor="middle">
              {stage.note}
            </text>
          )}
        </g>
      ))}
      <rect x={DOC_X} y={14} width={DOC_W} height={156} rx={4} fill="#ffffff" stroke="#c9ccd1" />
      {DOC_PARTS.map((part) => (
        <g key={part.label}>
          <rect x={DOC_X + 6} y={part.y} width={3} height={part.h} rx={1.5} fill={part.color} />
          <text x={DOC_X + 14} y={part.y + 9} fontSize={7.5} fill="#444">
            {part.label}
          </text>
          {part.sub && (
            <text x={DOC_X + 14} y={part.y + 19} fontSize={7} fill="#999">
              {part.sub}
            </text>
          )}
        </g>
      ))}
      {MAP_LINKS.map((link) => {
        const stage = MAP_STAGES[link.from];
        const part = DOC_PARTS[link.to];
        return (
          <path
            key={`${link.from}-${link.to}`}
            d={`M92,${stage.y + BOX_H / 2} C 160,${stage.y + BOX_H / 2} 170,${part.y + part.h / 2} ${DOC_X + 4},${part.y + part.h / 2}`}
            fill="none"
            stroke={stage.color}
            strokeWidth={1.1}
            opacity={0.85}
          />
        );
      })}
    </g>
  );
}

function StageBox({ x, y, stage }: { x: number; y: number; stage: Stage }) {
  const w = stage.w ?? BOX_W;
  return (
    <g>
      <rect
        x={x}
        y={y}
        width={w}
        height={BOX_H}
        rx={5}
        fill={stage.cohere ? COHERE_FILL : "#f6f7f8"}
        stroke={stage.cohere ? "var(--accent, #4078c0)" : "#c9ccd1"}
      />
      <text x={x + w / 2} y={y + 13.5} {...LABEL}>
        {stage.label}
      </text>
    </g>
  );
}

function CommandRow({ row, y }: { row: Row; y: number }) {
  let x = STAGE_X;
  const parts = row.stages.map((stage, index) => {
    const w = stage.w ?? BOX_W;
    const box = (
      <g key={stage.label}>
        {index > 0 && <line x1={x - GAP + 1} y1={y + BOX_H / 2} x2={x - 2} y2={y + BOX_H / 2} {...ARROW} />}
        <StageBox x={x} y={y} stage={stage} />
      </g>
    );
    x += w + GAP;
    return box;
  });
  return (
    <g>
      <text x={4} y={y + 14} {...COMMAND}>
        {row.command}
      </text>
      {parts}
      {row.note && (
        <text x={STAGE_X + 2} y={y + BOX_H + 10} fontSize={8.5} fill="#999">
          {row.note}
        </text>
      )}
    </g>
  );
}

export default function TurnFigure() {
  const rowsTop = MAP_H + 10;
  const height = rowsTop + ROWS.length * ROW_H + 34;
  return (
    <figure className="turn-figure">
      <svg
        viewBox={`0 0 340 ${height}`}
        role="img"
        aria-label="The research workflow mapped onto the INTSUM it produces: retrieval's clearance filter sets the classification banner, rerank decides the sources in Supporting Reporting, Command composes the Bottom Line Up Front, Assessment, Gaps, and citation marks, and the deterministic conformance check stamps the verdict. The other commands run their own workflows below. Soft-filled stages are Cohere model calls."
      >
        <defs>
          <marker id="turn-arrow" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="6" markerHeight="6" orient="auto">
            <path d="M0,0 L8,4 L0,8 z" fill="#8a8f98" />
          </marker>
        </defs>
        <ResearchMap />
        <line x1={4} y1={rowsTop - 6} x2={336} y2={rowsTop - 6} stroke="#e3e5e8" />
        {ROWS.map((row, index) => (
          <CommandRow key={row.command} row={row} y={rowsTop + index * ROW_H} />
        ))}
        <g>
          <rect
            x={4}
            y={height - 22}
            width={10}
            height={10}
            rx={2}
            fill={COHERE_FILL}
            stroke="var(--accent, #4078c0)"
          />
          <text x={20} y={height - 13.5} fontSize={8.5} fill="#777">
            Cohere model call (Embed 4, Rerank 3.5, Command A, Command R7B) · colored lines: the part of the brief a stage writes
          </text>
        </g>
      </svg>
      <figcaption>
        Type a command, or plain text: plain text runs /make-brief — or /revise when a brief is
        already on screen. Every brief reports the sources it composed from.
      </figcaption>
    </figure>
  );
}
