import { describe, expect, it } from "vitest";

import { formatDateTimeGroup, turnContextFor } from "./turnContext";

describe("formatDateTimeGroup", () => {
  it("renders a moment as a Zulu date-time group", () => {
    expect(formatDateTimeGroup(new Date(Date.UTC(2026, 8, 1, 18, 30)))).toBe("011830Z SEP 26");
  });

  it("pads every component so the group reads at fixed width", () => {
    expect(formatDateTimeGroup(new Date(Date.UTC(2026, 0, 5, 3, 7)))).toBe("050307Z JAN 26");
  });

  it("reads the moment in UTC regardless of the analyst's zone", () => {
    // 23:30 in a UTC+2 zone is 21:30Z; the group must carry the Zulu reading,
    // so two analysts reading the same brief read the same moment.
    const local = new Date("2026-09-01T23:30:00+02:00");
    expect(formatDateTimeGroup(local)).toBe("012130Z SEP 26");
  });
});

describe("turnContextFor", () => {
  it("states the tasking and the completion moment and nothing else", () => {
    // The originator is deliberately absent: the panel holds no analyst
    // identity, and srd016 R3.2 bars filling the field with a plausible one.
    const context = turnContextFor("strikes overnight", new Date(Date.UTC(2026, 8, 1, 18, 30)));
    expect(context).toEqual({
      tasking: "strikes overnight",
      dateTimeGroup: "011830Z SEP 26",
    });
    expect("originator" in context).toBe(false);
  });
});
