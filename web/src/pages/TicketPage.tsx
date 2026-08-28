import { useParams } from "react-router-dom";

import { api } from "../api/client";
import { Notice } from "../components/Notice";
import { NowServing } from "../components/NowServing";
import { NumberList } from "../components/NumberList";
import { ErrorState, Loading } from "../components/PageState";
import { useAction } from "../hooks/useAction";
import { POLL_INTERVAL_MS, useResource } from "../hooks/useResource";
import { formatDay, wallClockTime } from "../lib/time";
import { formatPosition, formatWait } from "../lib/wait";

/**
 * A customer's own number, on their own phone.
 *
 * It polls so they watch their position fall rather than refreshing, and
 * it offers "leave the line" while that still means something. That button
 * is the whole reason this page can be trusted: a customer who simply
 * walks out is called to an empty room and written off as a no-show, which
 * inflates the wait shown to everybody behind them.
 */
export function TicketPage() {
  const { orgSlug = "", queueSlug = "", ticketId = "" } = useParams();

  const ticket = useResource(
    (signal) => api.ticket(orgSlug, queueSlug, ticketId, signal),
    [orgSlug, queueSlug, ticketId],
    { pollMs: POLL_INTERVAL_MS },
  );

  // The board is read alongside so the customer can see the line they are
  // in, not only their place in it. It is a second request rather than a
  // field on the ticket because the two poll independently: a ticket that
  // has been called changes far less often than the numbers around it.
  const board = useResource(
    (signal) => api.board(orgSlug, queueSlug, signal),
    [orgSlug, queueSlug],
    { pollMs: POLL_INTERVAL_MS },
  );

  const leave = useAction(async () => {
    const updated = await api.cancelTicket(orgSlug, queueSlug, ticketId);
    ticket.refresh();
    board.refresh();
    return updated;
  });

  if (ticket.loading) {
    return (
      <main className="page">
        <Loading label="Loading your ticket" />
      </main>
    );
  }
  if (!ticket.data && ticket.error) {
    return (
      <main className="page">
        <ErrorState error={ticket.error} onRetry={ticket.refresh} />
      </main>
    );
  }
  if (!ticket.data) return null;

  const t = ticket.data;

  return (
    <main className="page">
      <header className="page-header">
        <p className="eyebrow">{board.data?.queue.organization ?? "Your ticket"}</p>
        <h1>{board.data?.queue.queue ?? "Your number"}</h1>
      </header>

      <section className="card card--flush">
        <NowServing number={t.number} label="Your number" />
      </section>

      <section className="card">
        <div className="row row--between">
          <span className="badge">{t.status_label}</span>
          <span className="muted small">{formatDay(t.service_day)}</span>
        </div>

        {/* Position and wait are null once the ticket stops waiting. The
            page says what is happening instead of rendering a countdown
            for somebody already standing at a counter. */}
        {t.status === "waiting" && t.position !== null ? (
          <>
            <h2 style={{ marginTop: "1rem" }}>{formatPosition(t.position)}</h2>
            <p className="muted">
              {formatWait(t.estimated_wait_minutes ?? 0)}. Estimated from how
              long this line has been taking today.
            </p>
          </>
        ) : (
          <p className="muted" style={{ marginTop: "1rem" }}>
            {statusExplanation(t.status)}
          </p>
        )}
      </section>

      {board.data ? (
        <section className="card">
          <h2>The line right now</h2>
          <p className="muted small">
            Now serving{" "}
            {board.data.queue.now_serving === null
              ? "nobody"
              : board.data.queue.now_serving}
            .
          </p>
          <NumberList numbers={board.data.waiting_numbers} mine={t.number} />
        </section>
      ) : null}

      {t.cancellable ? (
        <section className="card">
          <h2>Not able to stay?</h2>
          <p className="muted small">
            Letting the counter know frees your place for the next person, and
            keeps the wait everyone else is shown honest.
          </p>
          {leave.error ? <Notice tone="error">{leave.error.message}</Notice> : null}
          <button
            type="button"
            className="danger"
            disabled={leave.pending}
            onClick={() => void leave.run()}
          >
            {leave.pending ? "Leaving…" : "Leave the line"}
          </button>
        </section>
      ) : null}

      <p className="updated-at">
        Issued at {wallClockTime(t.issued_at)}. This page updates on its own.
      </p>
    </main>
  );
}

/**
 * What a non-waiting status means to the person holding the ticket.
 *
 * The server already sends a label — "Called", "No-show" — and the page
 * shows it as a badge. This is the sentence underneath, which is a
 * different job: the label names the state, and this says what the
 * customer should do about it.
 */
function statusExplanation(status: string): string {
  switch (status) {
    case "called":
      return "Your number has been called. Please head to the counter.";
    case "serving":
      return "You're being served now.";
    case "done":
      return "This visit is finished. Thanks for coming in.";
    case "no_show":
      return "Your number was called and nobody came, so it was passed over. Please take a new number if you still need to be seen.";
    case "cancelled":
      return "This ticket was cancelled. Take a new number if you still need to be seen.";
    default:
      return "";
  }
}
