import { useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { api } from "../api/client";
import { Notice } from "../components/Notice";
import { ErrorState, Loading } from "../components/PageState";
import { useAction } from "../hooks/useAction";
import { useResource } from "../hooks/useResource";
import {
  addDays,
  dayInZone,
  formatDay,
  formatDuration,
  isBefore,
  wallClockTime,
} from "../lib/time";

/**
 * Picking a time and booking it.
 *
 * The customer is never asked to choose a person. On the public side the
 * question is "when can I come in", and the server answers it by assigning
 * somebody who performs the service and is free then — asking would turn a
 * two-click booking into a directory of employees.
 *
 * This page does not poll. A slot list that reshuffles under a customer
 * halfway through typing their name is worse than one that is a few
 * minutes stale, and the stale case is already handled: the server
 * re-derives the day and refuses a slot that has since been taken, which
 * is the message this page then shows.
 */
export function BookPage() {
  const { orgSlug = "", serviceSlug = "" } = useParams();
  const navigate = useNavigate();

  // The org is loaded for its timezone, which is what "today" has to be
  // measured in. Asking the viewer's device instead is wrong by a whole
  // day for anyone reading from another continent — and wrong in the
  // evening for anyone reading from a different one to the business.
  const org = useResource((signal) => api.org(orgSlug, signal), [orgSlug]);
  const timeZone = org.data?.org.timezone;

  const [day, setDay] = useState<string>("");
  const chosenDay = day || (timeZone ? dayInZone(new Date(), timeZone) : "");

  const slots = useResource(
    (signal) => api.slots(orgSlug, serviceSlug, chosenDay, signal),
    [orgSlug, serviceSlug, chosenDay],
    { enabled: chosenDay !== "" },
  );

  const [token, setToken] = useState("");
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [phone, setPhone] = useState("");

  const book = useAction(() =>
    api.book(orgSlug, serviceSlug, {
      slot: token,
      customer_name: name,
      customer_email: email,
      customer_phone: phone,
    }),
  );

  if (org.loading || (slots.loading && !slots.data)) {
    return (
      <main className="page">
        <Loading label="Loading available times" />
      </main>
    );
  }
  if (org.error && !org.data) {
    return (
      <main className="page">
        <ErrorState error={org.error} onRetry={org.refresh} />
      </main>
    );
  }
  if (slots.error && !slots.data) {
    return (
      <main className="page">
        <ErrorState error={slots.error} onRetry={slots.refresh} />
      </main>
    );
  }
  if (!slots.data || !org.data) return null;

  const { service, slots: open, earliest_bookable, latest_bookable } = slots.data;
  const zone = org.data.org.timezone;
  const firstBookableDay = dayInZone(earliest_bookable, zone);
  const lastBookableDay = dayInZone(latest_bookable, zone);

  // The picker is bounded by the window the server validates against, so
  // it cannot offer a day the booking endpoint would refuse. A customer
  // who tries three times and is turned down three times stops trying.
  const canGoBack = isBefore(firstBookableDay, chosenDay);
  const canGoForward = isBefore(addDays(chosenDay, 1), lastBookableDay);

  const changeDay = (delta: number) => {
    setDay(addDays(chosenDay, delta));
    setToken("");
  };

  return (
    <main className="page">
      <header className="page-header">
        <p className="eyebrow">{org.data.org.name}</p>
        <h1>{service.name}</h1>
        <p className="muted small">
          {formatDuration(service.duration_minutes)}
          {service.description ? ` · ${service.description}` : ""}
        </p>
      </header>

      <section className="card">
        <div className="row row--between">
          <button
            type="button"
            className="secondary"
            disabled={!canGoBack}
            onClick={() => changeDay(-1)}
          >
            ← Previous day
          </button>
          <strong>{formatDay(chosenDay)}</strong>
          <button
            type="button"
            className="secondary"
            disabled={!canGoForward}
            onClick={() => changeDay(1)}
          >
            Next day →
          </button>
        </div>

        <div className="field">
          <label htmlFor="book-date">Or pick a date</label>
          <input
            id="book-date"
            type="date"
            value={chosenDay}
            min={firstBookableDay}
            max={lastBookableDay}
            onChange={(event) => {
              setDay(event.target.value);
              setToken("");
            }}
          />
        </div>
      </section>

      <section className="card">
        <h2>Available times</h2>
        {open.length === 0 ? (
          <p className="muted">
            Nothing free on this day. Try another one &mdash; online booking
            runs to {formatDay(lastBookableDay)}.
          </p>
        ) : (
          <div className="slot-grid">
            {open.map((slot) => (
              <button
                key={slot.token}
                type="button"
                aria-pressed={token === slot.token}
                onClick={() => setToken(slot.token)}
              >
                {wallClockTime(slot.start)}
              </button>
            ))}
          </div>
        )}
      </section>

      {token ? (
        <section className="card">
          <h2>Your details</h2>
          <form
            onSubmit={async (event) => {
              event.preventDefault();
              const booked = await book.run();
              if (booked) {
                // Replace rather than push, so backing up lands on the
                // service page rather than on a form that would book a
                // second appointment if submitted again.
                navigate(`/${orgSlug}/appointments/${booked.id}`, { replace: true });
              }
            }}
          >
            {book.error ? <Notice tone="error">{book.error.message}</Notice> : null}

            {/* A slot that was taken while the customer typed is the one
                failure worth acting on rather than only reporting: the
                list is refreshed so the times on screen are the ones that
                are actually left. */}
            {book.error?.code === "slot_taken" ? (
              <p className="small">
                <button type="button" className="secondary" onClick={slots.refresh}>
                  Show the times that are still free
                </button>
              </p>
            ) : null}

            <div className="field">
              <label htmlFor="book-name">Your name</label>
              <input
                id="book-name"
                type="text"
                value={name}
                autoComplete="name"
                onChange={(event) => setName(event.target.value)}
              />
            </div>

            <div className="field">
              <label htmlFor="book-email">Email</label>
              <input
                id="book-email"
                type="email"
                value={email}
                autoComplete="email"
                onChange={(event) => setEmail(event.target.value)}
              />
              <p className="hint">
                Your confirmation link goes here. Keep it &mdash; it is how you
                cancel if your plans change.
              </p>
            </div>

            <div className="field">
              <label htmlFor="book-phone">Phone number (optional)</label>
              <input
                id="book-phone"
                type="tel"
                value={phone}
                autoComplete="tel"
                onChange={(event) => setPhone(event.target.value)}
              />
            </div>

            <div className="field">
              <button type="submit" disabled={book.pending}>
                {book.pending
                  ? "Booking…"
                  : `Book ${wallClockTime(startOf(open, token))} on ${formatDay(chosenDay)}`}
              </button>
            </div>
          </form>
        </section>
      ) : (
        <p className="muted small">Choose a time above to continue.</p>
      )}
    </main>
  );
}

/**
 * The start time behind a chosen token, so the submit button can name the
 * booking it is about to make. A token with no matching slot yields an
 * empty string, which wallClockTime renders as a dash — the case only
 * arises if the list is refreshed out from under a selection, and a dash
 * on the button is a better answer than a crash.
 */
function startOf(slots: { token: string; start: string }[], token: string): string {
  return slots.find((slot) => slot.token === token)?.start ?? "";
}
