import { describe, expect, it } from "vitest";

import { formatJoinWait, formatPosition, formatWait, ordinal } from "./wait";

// This table is the Go table in internal/queue/wait_test.go, case for
// case. That is the point: the same customer can read the server-rendered
// board and this page within a minute of each other, and being told "about
// an hour" by one and "About 1 hour 30 minutes" by the other reads as the
// business not knowing.
describe("formatWait", () => {
  const cases: Array<[number, string]> = [
    [0, "You're next"],
    [-1, "You're next"],
    [2, "Less than 5 minutes"],
    [5, "About 5 minutes"],
    [45, "About 45 minutes"],
    [60, "About 1 hour"],
    [90, "About 1 hour 30 minutes"],
    [115, "About 1 hour 55 minutes"],
    [120, "About 2 hours"],
    [125, "About 2 hours 5 minutes"],
    [180, "About 3 hours"],
  ];

  it.each(cases)("renders %i minutes as %s", (minutes, want) => {
    expect(formatWait(minutes)).toBe(want);
  });

  // A single minute past the hour is the case that catches a naive
  // pluralisation: "1 hours 1 minutes" is what you get for free.
  it("pluralises the singular hour and minute", () => {
    expect(formatWait(61)).toBe("About 1 hour 1 minute");
  });

  // The value comes off the wire, so a payload with a missing or broken
  // field must not put "About NaN minutes" in front of a customer.
  it("treats an unusable value as no wait rather than rendering NaN", () => {
    expect(formatWait(Number.NaN)).toBe("You're next");
    expect(formatWait(Number.POSITIVE_INFINITY)).toBe("You're next");
  });
});

// The two wordings differ on purpose, and the difference is the whole
// reason there are two functions: a zero means "you are next" to somebody
// holding a ticket and "there is nobody ahead" to somebody deciding
// whether to walk in.
describe("formatJoinWait", () => {
  it("says there is no wait rather than that you are next", () => {
    expect(formatJoinWait(0)).toBe("No wait right now");
    expect(formatJoinWait(-5)).toBe("No wait right now");
  });

  it("falls back to the same wording once there is a wait", () => {
    expect(formatJoinWait(20)).toBe(formatWait(20));
    expect(formatJoinWait(95)).toBe("About 1 hour 35 minutes");
  });
});

describe("formatPosition", () => {
  it("treats the front of the line as being next, not as position one", () => {
    expect(formatPosition(1)).toBe("You're next");
  });

  it("counts the holder into their own position", () => {
    expect(formatPosition(4)).toBe("4 in line");
  });
});

describe("ordinal", () => {
  it.each([
    [1, "1st"],
    [2, "2nd"],
    [3, "3rd"],
    [4, "4th"],
    [11, "11th"],
    [12, "12th"],
    [13, "13th"],
    [21, "21st"],
    [22, "22nd"],
    [23, "23rd"],
    [101, "101st"],
    [111, "111th"],
    [112, "112th"],
  ])("renders %i as %s", (n, want) => {
    expect(ordinal(n)).toBe(want);
  });
});
