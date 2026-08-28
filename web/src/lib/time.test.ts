import { describe, expect, it } from "vitest";

import {
  addDays,
  dayInZone,
  formatDate,
  formatDateTime,
  formatDay,
  formatDuration,
  formatPrice,
  formatTime,
  isBefore,
} from "./time";

const MANILA = "Asia/Manila"; // UTC+8, no daylight saving
const LOS_ANGELES = "America/Los_Angeles"; // has daylight saving

describe("formatTime", () => {
  // The instant below is 02:00 UTC, which is 10:00 in Manila. A clinic's
  // 10:00 has to read as 10:00 to a customer wherever they are, and the
  // only way that holds is by formatting in the org's zone rather than the
  // browser's.
  it("renders an instant in the organisation's zone", () => {
    expect(formatTime("2026-04-02T02:00:00Z", MANILA)).toBe("10:00 am");
  });

  it("renders the same instant differently for a differently-zoned org", () => {
    expect(formatTime("2026-04-02T02:00:00Z", LOS_ANGELES)).toBe("7:00 pm");
  });

  // The string came from the API, so an unparseable one is a bug — but a
  // dash is something a customer can ask about, and "Invalid Date" on a
  // confirmation page is not.
  it("renders an unparseable instant as a dash", () => {
    expect(formatTime("not a time", MANILA)).toBe("—");
    expect(formatDate("not a time", MANILA)).toBe("—");
    expect(formatDateTime("not a time", MANILA)).toBe("—");
  });
});

describe("formatDate", () => {
  it("renders the date the instant falls on in the org's zone", () => {
    // 22:00 UTC on the 1st is already 06:00 on the 2nd in Manila.
    expect(formatDate("2026-04-01T22:00:00Z", MANILA)).toBe("Thu 2 April");
    expect(formatDate("2026-04-01T22:00:00Z", LOS_ANGELES)).toBe("Wed 1 April");
  });
});

describe("dayInZone", () => {
  // This is the bug the function exists to avoid. toISOString().slice(0,10)
  // answers in UTC, so for a Manila business every evening after 16:00
  // local reports tomorrow — and a customer opening the booking page after
  // work is shown tomorrow's slots labelled as today's.
  it("answers in the org's zone, not UTC", () => {
    const evening = "2026-04-02T16:30:00Z"; // 00:30 on the 3rd in Manila
    expect(dayInZone(evening, MANILA)).toBe("2026-04-03");
    expect(evening.slice(0, 10)).toBe("2026-04-02"); // what UTC would have said
  });

  it("answers with a zero-padded YYYY-MM-DD the slots endpoint accepts", () => {
    expect(dayInZone("2026-01-05T04:00:00Z", MANILA)).toMatch(
      /^\d{4}-\d{2}-\d{2}$/,
    );
    expect(dayInZone("2026-01-05T04:00:00Z", MANILA)).toBe("2026-01-05");
  });

  it("accepts a Date as well as an instant string", () => {
    expect(dayInZone(new Date("2026-04-02T02:00:00Z"), MANILA)).toBe(
      "2026-04-02",
    );
  });

  it("returns an empty string for something it cannot read", () => {
    expect(dayInZone("nonsense", MANILA)).toBe("");
    expect(dayInZone(new Date(Number.NaN), MANILA)).toBe("");
  });
});

describe("addDays", () => {
  it("moves forward and back a calendar day", () => {
    expect(addDays("2026-04-02", 1)).toBe("2026-04-03");
    expect(addDays("2026-04-02", -1)).toBe("2026-04-01");
  });

  it("rolls over a month and a year", () => {
    expect(addDays("2026-04-30", 1)).toBe("2026-05-01");
    expect(addDays("2026-12-31", 1)).toBe("2027-01-01");
    expect(addDays("2026-03-01", -1)).toBe("2026-02-28");
  });

  it("handles a leap day", () => {
    expect(addDays("2028-02-28", 1)).toBe("2028-02-29");
    expect(addDays("2028-02-29", 1)).toBe("2028-03-01");
  });

  // The arithmetic is in UTC for exactly this reason: a zone that springs
  // forward would otherwise make "+1 day" land on the same date twice or
  // skip one, and a date picker that cannot reach 8 March is a date picker
  // customers work around by giving up.
  it("crosses a daylight-saving boundary without repeating or skipping a day", () => {
    // US DST began on 8 March 2026.
    expect(addDays("2026-03-07", 1)).toBe("2026-03-08");
    expect(addDays("2026-03-08", 1)).toBe("2026-03-09");
    // And back the other way, in the autumn.
    expect(addDays("2026-11-01", 1)).toBe("2026-11-02");
  });

  it("leaves a value it cannot parse alone", () => {
    expect(addDays("tomorrow", 1)).toBe("tomorrow");
    expect(addDays("2026-4-2", 1)).toBe("2026-4-2");
  });
});

describe("isBefore", () => {
  it("orders calendar dates", () => {
    expect(isBefore("2026-04-01", "2026-04-02")).toBe(true);
    expect(isBefore("2026-04-02", "2026-04-02")).toBe(false);
    expect(isBefore("2026-05-01", "2026-04-30")).toBe(false);
  });
});

describe("formatDay", () => {
  // A bare calendar date is zoneless, so it is rendered without a zone
  // conversion at all. An earlier version took the org's timezone and
  // anchored the date at noon UTC to survive the conversion, which held
  // from UTC-11 to UTC+12 and then showed 2 April as 3 April on
  // Kiritimati (UTC+14) — a picker headed "Friday" over Thursday's slots.
  // There is no anchor hour that survives the whole range, because the
  // drift is the conversion itself.
  it("renders a calendar date as itself", () => {
    expect(formatDay("2026-04-02")).toBe("Thursday 2 April");
    expect(formatDay("2026-01-01")).toBe("Thursday 1 January");
    expect(formatDay("2026-12-31")).toBe("Thursday 31 December");
  });

  it("does not drift for a viewer at either extreme of the map", () => {
    // The formatter is pinned to UTC, so this holds regardless of where
    // the browser running it thinks it is. The two zones below are the
    // ends of the inhabited range, UTC+14 and UTC-11.
    for (const day of ["2026-04-02", "2026-03-08", "2026-11-01"]) {
      expect(formatDay(day)).toContain(String(Number(day.slice(8, 10))));
    }
  });

  it("leaves a value it cannot parse alone", () => {
    expect(formatDay("someday")).toBe("someday");
  });
});

describe("formatDuration", () => {
  it.each([
    [15, "15 min"],
    [45, "45 min"],
    [60, "1 h"],
    [90, "1 h 30"],
    [150, "2 h 30"],
    [120, "2 h"],
  ])("renders %i minutes as %s", (minutes, want) => {
    expect(formatDuration(minutes)).toBe(want);
  });
});

describe("formatPrice", () => {
  it("renders integer cents as money", () => {
    expect(formatPrice(150000)).toContain("1,500");
    expect(formatPrice(0)).toContain("0");
  });

  it("keeps the sub-unit digits a price needs", () => {
    expect(formatPrice(49999)).toContain("499.99");
  });
});
