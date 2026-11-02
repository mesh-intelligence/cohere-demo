// The command router (GH-449): explicit commands win, plain text takes the
// defaulting rules, and a typo is an error naming the vocabulary rather than
// a silent research turn.
import { describe, expect, it } from "vitest";

import { isParseError, parseCommand } from "./commands";

describe("parseCommand", () => {
  it("defaults plain text on an empty window to research", () => {
    const parsed = parseCommand("strike activity in the east", false);
    expect(parsed).toEqual({ command: "make-brief", argument: "strike activity in the east" });
  });

  it("defaults plain text to revise when a brief is on screen", () => {
    const parsed = parseCommand("more information on air defense", true);
    expect(parsed).toEqual({ command: "revise", argument: "more information on air defense" });
  });

  it("dispatches explicit commands with their arguments", () => {
    expect(parseCommand("/search Bakhmut logistics", false)).toEqual({
      command: "search",
      argument: "Bakhmut logistics",
    });
    expect(parseCommand("/make-brief  drone production  ", true)).toEqual({
      command: "make-brief",
      argument: "drone production",
    });
    expect(parseCommand("/summarize", true)).toEqual({ command: "summarize", argument: "" });
    expect(parseCommand("/export", true)).toEqual({ command: "export", argument: "" });
  });

  it("refuses an unknown command by naming the vocabulary", () => {
    const parsed = parseCommand("/serach typo", false);
    expect(isParseError(parsed)).toBe(true);
    if (isParseError(parsed)) expect(parsed.error).toContain("/search");
  });

  it("refuses commands missing what they need", () => {
    expect(isParseError(parseCommand("/search", false))).toBe(true);
    expect(isParseError(parseCommand("/make-brief", false))).toBe(true);
    expect(isParseError(parseCommand("/revise tighten the BLUF", false))).toBe(true);
    expect(isParseError(parseCommand("/summarize", false))).toBe(true);
    expect(isParseError(parseCommand("/export", false))).toBe(true);
  });

  it("treats command names case-insensitively", () => {
    expect(parseCommand("/Search convoys", false)).toEqual({ command: "search", argument: "convoys" });
  });
});

describe("/eval", () => {
  it("parses with and without a count", () => {
    expect(parseCommand("/eval", false)).toEqual({ command: "eval", argument: "" });
    expect(parseCommand("/eval 5", false)).toEqual({ command: "eval", argument: "5" });
  });
  it("rejects a non-numeric count", () => {
    const parsed = parseCommand("/eval five", false);
    expect(isParseError(parsed)).toBe(true);
  });
});

describe("/help", () => {
  it("parses without an argument and needs no document", () => {
    expect(parseCommand("/help", false)).toEqual({ command: "help", argument: "" });
  });
});

describe("/audit", () => {
  it("needs a brief and takes an optional numeric count", () => {
    expect(isParseError(parseCommand("/audit", false))).toBe(true);
    expect(parseCommand("/audit", true)).toEqual({ command: "audit", argument: "" });
    expect(parseCommand("/audit 3", true)).toEqual({ command: "audit", argument: "3" });
    expect(isParseError(parseCommand("/audit three", true))).toBe(true);
  });
});
