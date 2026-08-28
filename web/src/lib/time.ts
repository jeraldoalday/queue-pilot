/**
 * Times as the business keeps them, not as the viewer's device does.
 *
 * Every instant the API sends is RFC 3339 with the organisation's own
 * offset — a clinic's 10:00 is the clinic's 10:00 whoever is reading. A
 * `Date` throws that away the moment it is constructed: it holds an
 * instant, and every method on it renders in the *browser's* zone. Left
 * alone, a customer in London reading a Manila clinic's slot list is shown
 * times eight hours off and a date that is sometimes the wrong day.
 *
 * So formatting goes through Intl with an explicit timeZone, taken from
 * the org payload. That is the whole reason `org.timezone` is on the wire.
 */

/**
 * Formats an instant as a time of day in the given zone: "10:00 am".
 *
 * An unparseable instant renders as an em dash rather than "Invalid Date".
 * The string came from the API, so a bad one is a bug — but a bug that
 * shows a customer a dash is one they can work around by asking, and
 * "Invalid Date" on a confirmation page is not.
 */
export function formatTime(iso: string, timeZone: string): string {
  const date = parseInstant(iso);
  if (!date) return "—";
  return new Intl.DateTimeFormat("en-GB", {
    hour: "numeric",
    minute: "2-digit",
    hour12: true,
    timeZone,
  }).format(date);
}

/** Formats an instant as a weekday and date: "Thu 2 April". */
export function formatDate(iso: string, timeZone: string): string {
  const date = parseInstant(iso);
  if (!date) return "—";
  return new Intl.DateTimeFormat("en-GB", {
    weekday: "short",
    day: "numeric",
    month: "long",
    timeZone,
  }).format(date);
}

/** Formats an instant as a date and time together, for a confirmation. */
export function formatDateTime(iso: string, timeZone: string): string {
  const date = parseInstant(iso);
  if (!date) return "—";
  return `${formatDate(iso, timeZone)} at ${formatTime(iso, timeZone)}`;
}

/**
 * The calendar day an instant falls on *in the given zone*, as
 * YYYY-MM-DD — the form the slots endpoint takes as ?date=.
 *
 * This is the function that has to exist. `toISOString().slice(0, 10)` is
 * the obvious one-liner and it is wrong: it answers in UTC, so for a
 * Manila business every evening after 16:00 local is reported as the next
 * day, and a customer opening the booking page after work is shown
 * tomorrow's slots as though they were today's.
 */
export function dayInZone(instant: Date | string, timeZone: string): string {
  const date = typeof instant === "string" ? parseInstant(instant) : instant;
  if (!date || Number.isNaN(date.getTime())) return "";

  // en-CA gives YYYY-MM-DD directly. formatToParts would be the pedantic
  // route; this is the same answer with far less to go wrong, and the
  // tests pin the format so a locale-data change cannot quietly alter it.
  return new Intl.DateTimeFormat("en-CA", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    timeZone,
  }).format(date);
}

/**
 * Adds whole days to a YYYY-MM-DD string, which is what the booking page's
 * "next day" and "previous day" controls do.
 *
 * The arithmetic is done in UTC deliberately. The input is a bare calendar
 * date with no zone attached, so the only requirement is that adding one
 * lands on the next date on a calendar — and UTC is the only zone with no
 * daylight-saving jump to make "+1 day" land on the same date twice or
 * skip one entirely.
 */
export function addDays(day: string, delta: number): string {
  const parsed = parseDay(day);
  if (!parsed) return day;
  parsed.setUTCDate(parsed.getUTCDate() + delta);
  return parsed.toISOString().slice(0, 10);
}

/** Compares two YYYY-MM-DD strings. Lexicographic order is date order. */
export function isBefore(day: string, other: string): boolean {
  return day < other;
}

/**
 * Formats a YYYY-MM-DD calendar date for display: "Thursday 2 April".
 *
 * It takes no timezone, and that is the point. A bare calendar date is
 * zoneless — "2 April" is 2 April to everyone — so it is parsed at UTC
 * midnight and rendered in UTC, and the two cancel out exactly.
 *
 * Rendering it in the *org's* zone is the tempting version and it is
 * wrong. Anchoring at UTC midnight and formatting in Manila shows 2 April
 * as 2 April, but anchoring at UTC noon to compensate then shows it as
 * 3 April on Kiritimati (UTC+14) — there is no anchor hour that survives
 * the whole range, because the drift is the zone conversion itself. Not
 * converting is the only version with no edge to get wrong.
 */
export function formatDay(day: string): string {
  const parsed = parseDay(day);
  if (!parsed) return day;
  return new Intl.DateTimeFormat("en-GB", {
    weekday: "long",
    day: "numeric",
    month: "long",
    timeZone: "UTC",
  }).format(parsed);
}

/** Minutes as a service duration reads on a list: "45 min", "1 h 30". */
export function formatDuration(minutes: number): string {
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  return rest === 0 ? `${hours} h` : `${hours} h ${rest}`;
}

/**
 * Money, from the integer cents the API sends. A service with no published
 * price sends null, which is a different fact from a price of zero, so
 * this takes only a number and the caller decides what null renders as.
 */
export function formatPrice(cents: number, currency = "PHP"): string {
  return new Intl.NumberFormat("en-PH", {
    style: "currency",
    currency,
  }).format(cents / 100);
}

function parseInstant(iso: string): Date | null {
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? null : date;
}

/** Parses YYYY-MM-DD as a UTC midnight, rejecting anything else. */
function parseDay(day: string): Date | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) return null;
  const date = new Date(`${day}T00:00:00Z`);
  return Number.isNaN(date.getTime()) ? null : date;
}
