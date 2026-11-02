import { describe, expect, it } from "vitest";

import { appendHighlight, highlightFor, type Highlight } from "@declarative-agents/ui-kit";
import { CHAT_MILESTONES } from "./milestones";

// The activity feed (GH-380): milestone words become lines, everything else is
// silence, and a fan-out that repeats one word does not stutter. The feed
// mechanics are the kit's; what this application claims is its own map, so the
// cases below read it through the kit's helper.

describe("highlightFor", () => {
  it("labels the Cohere milestones by the branch they rerank", () => {
    expect(highlightFor({ command_name: "cohere_rerank_rag" }, CHAT_MILESTONES)?.label).toBe(
      "reranking the corpus results (Cohere)",
    );
    expect(highlightFor({ command_name: "cohere_rerank_search" }, CHAT_MILESTONES)?.label).toBe(
      "reranking the search results (Cohere)",
    );
    expect(highlightFor({ command_name: "cohere_rerank_fused" }, CHAT_MILESTONES)?.label).toBe(
      "fusing and reranking both branches (Cohere)",
    );
    expect(highlightFor({ command_name: "invoke_cohere_command" }, CHAT_MILESTONES)?.label).toBe(
      "composing the brief (Cohere Command)",
    );
  });

  it("says nothing for the machine's bookkeeping words", () => {
    // A hundred transitions per turn is the reason the feed maps milestones
    // rather than everything.
    expect(highlightFor({ command_name: "check_gaps_section" }, CHAT_MILESTONES)).toBeUndefined();
    expect(highlightFor({ command_name: "record_threat_activity_ok" }, CHAT_MILESTONES)).toBeUndefined();
    expect(highlightFor({}, CHAT_MILESTONES)).toBeUndefined();
  });

  it("carries the mesh's own event time", () => {
    const at = "2026-09-04T12:22:19.141Z";
    expect(highlightFor({ command_name: "embed_query_cohere", timestamp: at }, CHAT_MILESTONES)?.at).toBe(at);
  });
});

describe("appendHighlight", () => {
  it("collapses a fan-out that repeats one word", () => {
    // rag_query runs once per unit; the feed says "querying the RAG units"
    // once rather than per unit.
    const one: Highlight = { label: "querying the RAG units" };
    const feed = appendHighlight(appendHighlight([], one), { ...one });
    expect(feed).toHaveLength(1);
  });

  it("keeps distinct steps in order", () => {
    const feed = appendHighlight(
      appendHighlight([], { label: "embedding the tasking" }),
      { label: "querying the RAG units" },
    );
    expect(feed.map((entry) => entry.label)).toEqual([
      "embedding the tasking",
      "querying the RAG units",
    ]);
  });
});
