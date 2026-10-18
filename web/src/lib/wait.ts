/**
 * Rendering a wait, and nothing else.
 *
 * The estimate itself is never computed here. `queue.Estimate` on the
 * server decides how long a line takes — how many counters are working it,
 * what the queue's own measured service time is, how much to round up by —
 * and the API sends the answer in whole minutes. Recomputing it in
 * TypeScript would mean the number a customer reads on their phone and the
 * number on the sign above the door came from two implementations, and the
 * first divergence would be discovered by somebody who left and came back
 * to find their place gone.
 *
 * What is here is the wording, because prose is a rendering decision and
 * the API is machine-facing. The phrasings match `queue.FormatWait` and
 * `queue.FormatJoinWait` exactly, and the tests mirror the Go table case
 * for case, so the HTML page and this page say the same sentence.
 */

/**
 * The granularity the server rounds every estimate up to. It is not used
 * to round anything here — the value has already been rounded — but a wait
 * below it is the one case that gets its own wording, and the threshold
 * has to be the same number.
 */
export const WAIT_ROUNDING_MINUTES = 5;

/** English pluralisation for the two words this module needs. */
function plural(n: number, one: string, many: string): string {
  return n === 1 ? one : many;
}

/**
 * Renders the wait for someone who already holds a ticket.
 *
 * A zero is a statement about position rather than about time: being next
 * is not "no wait", it is "you're next", and the caller shows the position
 * alongside. Past an hour the wait is spelled out in hours *and* minutes
 * rather than collapsed into a phrase — telling somebody facing 1h55m that
 * it is "about an hour" understates it by nearly an hour, in the one
 * direction the estimate is deliberately careful never to be wrong in.
 */
export function formatWait(minutes: number): string {
  if (!Number.isFinite(minutes) || minutes <= 0) {
    return "You're next";
  }
  if (minutes < WAIT_ROUNDING_MINUTES) {
    return `Less than ${WAIT_ROUNDING_MINUTES} minutes`;
  }
  if (minutes < 60) {
    return `About ${Math.floor(minutes)} minutes`;
  }

  const hours = Math.floor(minutes / 60);
  const rest = Math.floor(minutes % 60);
  if (rest === 0) {
    return `About ${hours} ${plural(hours, "hour", "hours")}`;
  }
  return `About ${hours} ${plural(hours, "hour", "hours")} ${rest} ${plural(
    rest,
    "minute",
    "minutes",
  )}`;
}

/**
 * Renders the wait for someone deciding whether to join at all.
 *
 * Worded differently from formatWait on purpose: a zero there means "you
 * are next", while a zero here means there is nobody to wait behind.
 */
export function formatJoinWait(minutes: number): string {
  if (!Number.isFinite(minutes) || minutes <= 0) {
    return "No wait right now";
  }
  return formatWait(minutes);
}

/**
 * Renders a place in line. The server counts the holder into their own
 * position, so 1 is the front and never 0; this only puts it into words.
 */
export function formatPosition(position: number): string {
  if (position <= 1) {
    return "You're next";
  }
  return `${position} in line`;
}

