/**
 * The number at the counter, set large.
 *
 * Nothing is served as a dash rather than a zero. Zero is a number a
 * ticket could plausibly have been given, and a room reading "NOW SERVING
 * 0" spends a moment working out that it means nobody — which is a moment
 * every person in that room spends.
 *
 * The size comes from the stylesheet, in viewport units, so the same
 * component is a phone's header and a lobby television's whole screen.
 */
export function NowServing({
  number,
  label = "Now serving",
}: {
  number: number | null;
  label?: string;
}) {
  return (
    <div className="now-serving">
      <p className="label">{label}</p>
      <p className={number === null ? "number number--empty" : "number"}>
        {number === null ? "—" : number}
      </p>
      {/* The visible dash reads as "nobody" to somebody looking at it and
          as nothing at all to a screen reader, so the words are given
          separately rather than left implied by the glyph. */}
      <span className="visually-hidden">
        {number === null ? "Nobody is being served right now" : `Now serving number ${number}`}
      </span>
    </div>
  );
}
