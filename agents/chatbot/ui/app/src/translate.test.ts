// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

import { describe, expect, it } from "vitest";
import type { Citation } from "./api";
import { buildSegments, rebuildTranslated } from "./translate";

function citation(start: number, end: number, record_id: string): Citation {
  return { start, end, text: "", record_id };
}

describe("rebuildTranslated", () => {
  const text = "# Bottom Line Up Front\nStrikes hit Kyiv on December 16. Defences held.\n- Losses rose.";

  it("splices translations at unit offsets and keeps the markdown structure", () => {
    const units = buildSegments(text, []);
    const rendition = rebuildTranslated(
      text,
      units,
      ["Des frappes ont touché Kyiv le 16 décembre.", "Les défenses ont tenu.", "Les pertes ont augmenté."],
    );

    expect(rendition.text).toContain("# Bottom Line Up Front");
    expect(rendition.text).toContain("- Les pertes ont augmenté.");
    expect(rendition.text).not.toContain("Strikes hit Kyiv");
  });

  it("remaps each unit's citations to the translated unit's rune span", () => {
    const units = buildSegments(text, [citation(23, 40, "isw-2022-12-16")]);
    const rendition = rebuildTranslated(
      text,
      units,
      ["Des frappes — le 16 décembre.", "Les défenses ont tenu.", "Les pertes ont augmenté."],
    );

    const cited = rendition.citations.find((entry) => entry.record_id === "isw-2022-12-16");
    expect(cited).toBeDefined();
    const runes = [...rendition.text];
    expect(runes.slice(cited!.start, cited!.end).join("")).toBe("Des frappes — le 16 décembre.");
  });

  it("refuses a count mismatch rather than misaligning references", () => {
    const units = buildSegments(text, []);
    expect(() => rebuildTranslated(text, units, ["une seule"])).toThrow(/mismatch/);
  });
});
