// The runtime vocabulary (GH-929): labels rebind at load, the template
// replaces whole or not at all, and absent config is the compiled DefTech
// default.
import { afterEach, describe, expect, it } from "vitest";

import { INTSUM_SECTIONS, INTSUM_TEMPLATE_VERSION } from "./intsumTemplate";
import {
  clearanceSelectLabel,
  levelLabel,
  setUIConfigForTest,
  templateSections,
  templateVersion,
  validateUIConfig,
} from "./uiConfig";

afterEach(() => setUIConfigForTest({}));

describe("validateUIConfig", () => {
  it("takes labels and wording and leaves unknown levels at their wire value", () => {
    setUIConfigForTest(
      validateUIConfig({
        levelLabels: { unclassified: "Public", secret: "Wall-crossed", bogus: 7 },
        clearanceSelectLabel: "requester access",
      }),
    );
    expect(levelLabel("unclassified")).toBe("Public");
    expect(levelLabel("secret")).toBe("Wall-crossed");
    expect(levelLabel("restricted")).toBe("restricted");
    expect(levelLabel("bogus")).toBe("bogus");
    expect(clearanceSelectLabel()).toBe("requester access");
  });

  it("rejects a malformed template whole rather than half-applying it", () => {
    const config = validateUIConfig({
      template: [
        { id: "ok", label: "OK", order: 1, source: "derived", required: true },
        { id: "", label: "broken", order: 2, source: "derived", required: true },
      ],
    });
    expect(config.template).toBeUndefined();
    setUIConfigForTest(config);
    expect(templateSections()).toBe(INTSUM_SECTIONS);
    expect(templateVersion()).toBe(INTSUM_TEMPLATE_VERSION);
  });

  it("takes a well-formed template and reports its version", () => {
    const config = validateUIConfig({
      templateVersion: "memo-1",
      template: [
        {
          id: "summary",
          label: "Summary",
          order: 1,
          source: "composed_markdown",
          required: true,
          headings: ["Summary", "Executive Summary"],
        },
        {
          id: "citation-index",
          label: "Citation Index",
          order: 2,
          source: "derived",
          derivedFrom: "citations",
          required: true,
          columns: [{ id: "record", label: "Record" }],
        },
      ],
    });
    setUIConfigForTest(config);
    expect(templateSections().map((section) => section.id)).toEqual(["summary", "citation-index"]);
    expect(templateVersion()).toBe("memo-1");
  });

  it("treats a non-object document as no configuration", () => {
    expect(validateUIConfig(null)).toEqual({});
    expect(validateUIConfig("banner")).toEqual({});
  });
});
