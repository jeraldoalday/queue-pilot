import { useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { api } from "../api/client";
import { Notice } from "../components/Notice";
import { NowServing } from "../components/NowServing";
import { NumberList } from "../components/NumberList";
import { ErrorState, Loading } from "../components/PageState";
import { StatGrid } from "../components/StatGrid";
import { useAction } from "../hooks/useAction";
import { POLL_INTERVAL_MS, useResource } from "../hooks/useResource";
import { formatJoinWait } from "../lib/wait";

/**
 * The page behind a shop's QR code: what is being served now, how long the
 * line is, and a form to join it.
 *
 * It polls, because a customer standing in the room watches this page
 * rather than reloading it — the same reason the server-rendered board
 * polls a partial, at the same interval.
 */
export function QueueBoardPage() {
  const { orgSlug = "", queueSlug = "" } = useParams();
  const navigate = useNavigate();

  const board = useResource(
    (signal) => api.board(orgSlug, queueSlug, signal),
    [orgSlug, queueSlug],
    { pollMs: POLL_INTERVAL_MS },
  );

  if (board.loading) return <main className="page"><Loading label="Loading the queue" /></main>;
  if (!board.data && board.error) {
    return (
      <main className="page">
        <ErrorState error={board.error} onRetry={board.refresh} />
      </main>
    );
  }
  if (!board.data) return null;

  const { queue, waiting_numbers } = board.data;

  return (
    <main className="page">
      <header className="page-header">
        <p className="eyebrow">{queue.organization}</p>
        <div className="row row--between">
          <h1>{queue.queue}</h1>
          <span className={queue.open ? "badge badge--open" : "badge badge--closed"}>
            {queue.open ? "Open" : "Closed"}
          </span>
        </div>
      </header>

      {/* A failed poll leaves the board on screen and says so quietly.
          Replacing a board that was right fifteen seconds ago with an
          error page is a worse answer than a slightly stale board. */}
      {board.error ? (
        <Notice tone="error">
          We couldn&rsquo;t refresh just now &mdash; these numbers may be a
          moment out of date.
        </Notice>
      ) : null}

      <section className="card card--flush">
        <NowServing number={queue.now_serving} />
      </section>

      <section className="card">
        <StatGrid
          stats={[
            { name: "Waiting", value: queue.waiting },
            { name: "At a counter", value: queue.in_progress },
            { name: "Served today", value: queue.served_today },
          ]}
        />
        <p className="muted small" style={{ marginTop: "0.75rem", marginBottom: 0 }}>
          {formatJoinWait(queue.estimated_wait_minutes)} if you join now.
        </p>
      </section>

      <section className="card">
        <h2>In line</h2>
        <NumberList numbers={waiting_numbers} />
      </section>

      {queue.open ? (
        <JoinForm
          orgSlug={orgSlug}
          queueSlug={queueSlug}
          onJoined={(ticketId) =>
            navigate(`/${orgSlug}/q/${queueSlug}/t/${ticketId}`, { replace: true })
          }
        />
      ) : (
        <section className="card">
          <h2>This line is closed</h2>
          <p className="muted">
            It isn&rsquo;t taking walk-ins right now. The numbers above are
            today&rsquo;s, so you can see how the day went.
          </p>
        </section>
      )}
    </main>
  );
}

/**
 * Taking a number.
 *
 * On success this replaces the current history entry rather than pushing
 * one. It is the same reason the server-rendered form redirects: a
 * customer who backs up onto the board and re-submits takes a second
 * number, and the first one is then called to an empty room.
 */
function JoinForm({
  orgSlug,
  queueSlug,
  onJoined,
}: {
  orgSlug: string;
  queueSlug: string;
  onJoined: (ticketId: string) => void;
}) {
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");

  const join = useAction(() =>
    api.joinQueue(orgSlug, queueSlug, {
      customer_name: name,
      customer_phone: phone,
    }),
  );

  return (
    <section className="card">
      <h2>Join the line</h2>
      <form
        onSubmit={async (event) => {
          event.preventDefault();
          const ticket = await join.run();
          if (ticket) onJoined(ticket.id);
        }}
      >
        {join.error ? <Notice tone="error">{join.error.message}</Notice> : null}

        <div className="field">
          <label htmlFor="join-name">Your name</label>
          <input
            id="join-name"
            name="customer_name"
            type="text"
            value={name}
            autoComplete="name"
            maxLength={60}
            onChange={(event) => setName(event.target.value)}
          />
          <p className="hint">
            Staff will call this out, so a first name is enough.
          </p>
        </div>

        <div className="field">
          <label htmlFor="join-phone">Phone number (optional)</label>
          <input
            id="join-phone"
            name="customer_phone"
            type="tel"
            value={phone}
            autoComplete="tel"
            maxLength={32}
            onChange={(event) => setPhone(event.target.value)}
          />
          <p className="hint">Only used if staff need to reach you.</p>
        </div>

        <div className="field">
          {/* Disabled while the request is in flight. The action drops a
              second call anyway, but a button that still looks pressable
              invites the tap that a slow connection turns into two
              numbers. */}
          <button type="submit" disabled={join.pending}>
            {join.pending ? "Taking your number…" : "Take a number"}
          </button>
        </div>
      </form>
    </section>
  );
}
