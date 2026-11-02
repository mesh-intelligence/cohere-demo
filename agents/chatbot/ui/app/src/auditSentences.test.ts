// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

import { describe, expect, it } from "vitest";
import { auditTargets } from "./auditSentences";
import type { Citation } from "./api";

function citation(start: number, end: number, record_id: string): Citation {
  return { start, end, text: "", record_id } as Citation;
}

describe("auditTargets", () => {
  it("splits prose into sentences and maps overlapping citations", () => {
    const text = "Strikes hit Kyiv on December 16. Air defences downed 60 of 76 missiles.";
    const targets = auditTargets(text, [
      citation(12, 16, "isw-2022-12-16"),
      citation(33, 71, "missile-2022-12-16"),
    ]);

    expect(targets.map((t) => t.sentence)).toEqual([
      "Strikes hit Kyiv on December 16.",
      "Air defences downed 60 of 76 missiles.",
    ]);
    expect(targets[0].recordIDs).toEqual(["isw-2022-12-16"]);
    expect(targets[1].recordIDs).toEqual(["missile-2022-12-16"]);
  });

  it("skips headings, keeps bullets whole, and leaves uncited sentences bare", () => {
    const text = "# Gaps\n- Battle damage assessment is not available. It may follow.\nOutlook is stable.";
    const targets = auditTargets(text, []);

    expect(targets.map((t) => t.sentence)).toEqual([
      "Battle damage assessment is not available. It may follow.",
      "Outlook is stable.",
    ]);
    expect(targets.every((t) => t.recordIDs.length === 0)).toBe(true);
  });

  it("collects distinct corpus records and drops external citations", () => {
    const text = "One sentence cited twice to the same record and once externally.";
    const targets = auditTargets(text, [
      citation(0, 12, "HR-22-019"),
      citation(13, 20, "HR-22-019"),
      citation(21, 30, "osint-1"),
    ]);

    expect(targets).toHaveLength(1);
    expect(targets[0].recordIDs).toEqual(["HR-22-019"]);
  });
});
