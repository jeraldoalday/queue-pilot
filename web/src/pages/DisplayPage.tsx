import { useParams } from "react-router-dom";

import { api } from "../api/client";
import { NowServing } from "../components/NowServing";
import { POLL_INTERVAL_MS, useResource } from "../hooks/useResource";

/**
 * The screen on the waiting-room wall.
 *
 * Numbers only — never a name, since a whole room reads it. That is not a
 * styling choice here: the board payload carries no names, so this page
 * could not show one if it were written to.
 *
 * It is meant to be opened once and left running for a day, which shapes
 * the rest of it. Nothing is interactive, so there is nothing to
 * accidentally press while cleaning the screen. A failed refresh shows the
 * last numbers rather than an error, because a screen that goes blank
 * reads to a room as the queue being broken. And there is no loading text
 * beyond the first paint: a display that flashes "Loading" every fifteen
 * seconds is a display people stop trusting.
 */
export function DisplayPage() {
  const { orgSlug = "", queueSlug = "" } = useParams();

  const board = useResource(
    (signal) => api.board(orgSlug, queueSlug, signal),
    [orgSlug, queueSlug],
    { pollMs: POLL_INTERVAL_MS },
  );

  if (!board.data) {
    return (
      <main className="display">
        <div />
        <div className="display-main">
          <p className="muted">
            {board.error ? "This screen isn't available." : "Starting up…"}
          </p>
        </div>
        <div />
      </main>
    );
  }

  const { queue, waiting_numbers, in_progress_numbers } = board.data;
  // The lowest number in progress is the one set large; any others are
  // shown beside it. A busy counter has several people at once, and a
  // screen showing only the first is wrong for everybody else called.
  const alsoCalled = in_progress_numbers.slice(1);

  return (
    <main className="display">
      <header className="display-head">
        <span>{queue.queue}</span>
        <span className="muted">{queue.organization}</span>
      </header>

      <div className="display-main">
        <NowServing number={queue.now_serving} />
      </div>

      <footer className="display-foot">
        {alsoCalled.length > 0 ? (
          <span>Also called: {alsoCalled.join("  ")}</span>
        ) : null}
        <span>
          Next: {waiting_numbers.slice(0, 5).join("  ") || "—"}
        </span>
        <span>{queue.waiting} waiting</span>
        {!queue.open ? <span>Closed</span> : null}
      </footer>
    </main>
  );
}
