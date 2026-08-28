import { useParams } from "react-router-dom";

import { api } from "../api/client";
import { Notice } from "../components/Notice";
import { ErrorState, Loading } from "../components/PageState";
import { useAction } from "../hooks/useAction";
import { useResource } from "../hooks/useResource";
import { formatDay, wallClockTime } from "../lib/time";

/**
 * A customer's own booking, reached from the link in their confirmation.
 *
 * The link is the credential: the ID is an unguessable UUID, and anyone
 * holding it can read this page and cancel from it. That is deliberate and
 * it is why the page carries no other identifying detail — it says what
 * was booked and when, not who else the business sees that day.
 *
 * It does not poll. An appointment does not change minute to minute, and
 * a page that re-fetched itself while somebody read their confirmation
 * would be spending a request to tell them what they already know.
 */
export function AppointmentPage() {
  const { orgSlug = "", appointmentId = "" } = useParams();

  const appointment = useResource(
    (signal) => api.appointment(orgSlug, appointmentId, signal),
    [orgSlug, appointmentId],
  );

  const cancel = useAction(async () => {
    const updated = await api.cancelAppointment(orgSlug, appointmentId);
    appointment.refresh();
    return updated;
  });

  if (appointment.loading) {
    return (
      <main className="page">
        <Loading label="Loading your booking" />
      </main>
    );
  }
  if (!appointment.data && appointment.error) {
    return (
      <main className="page">
        <ErrorState error={appointment.error} onRetry={appointment.refresh} />
      </main>
    );
  }
  if (!appointment.data) return null;

  const a = appointment.data;
  const day = a.starts_at.slice(0, 10);

  return (
    <main className="page">
      <header className="page-header">
        <p className="eyebrow">{a.status === "booked" ? "You're booked" : "Your booking"}</p>
        <h1>{a.service}</h1>
      </header>

      <section className="card">
        <p style={{ fontSize: "1.25rem", marginBottom: "0.25rem" }}>
          <strong>
            {formatDay(day)} at {wallClockTime(a.starts_at)}
          </strong>
        </p>
        <p className="muted">
          With {a.staff_name}, until {wallClockTime(a.ends_at)}.
        </p>
        <p className="muted small" style={{ marginBottom: 0 }}>
          Booked for {a.customer_name}.
        </p>
      </section>

      {a.status !== "booked" ? (
        <section className="card">
          <h2>{statusHeading(a.status)}</h2>
          <p className="muted" style={{ marginBottom: 0 }}>
            {statusExplanation(a.status)}
          </p>
        </section>
      ) : null}

      {/* Cancellable is decided on the server, the same way the endpoint
          will decide it: still booked, and not yet started. Offering the
          button for an appointment that began ten minutes ago is offering
          a button that fails when pressed. */}
      {a.cancellable ? (
        <section className="card">
          <h2>Need to cancel?</h2>
          <p className="muted small">
            Cancelling frees the time for somebody else. If you simply
            don&rsquo;t come, the slot stays blocked and it is recorded as a
            missed appointment.
          </p>
          {cancel.error ? <Notice tone="error">{cancel.error.message}</Notice> : null}
          <button
            type="button"
            className="danger"
            disabled={cancel.pending}
            onClick={() => void cancel.run()}
          >
            {cancel.pending ? "Cancelling…" : "Cancel this appointment"}
          </button>
        </section>
      ) : null}

      <p className="updated-at">
        Keep this link. It is how you check or cancel this booking.
      </p>
    </main>
  );
}

function statusHeading(status: string): string {
  switch (status) {
    case "cancelled":
      return "This appointment was cancelled";
    case "completed":
      return "This appointment is done";
    case "no_show":
      return "This appointment was missed";
    default:
      return "";
  }
}

function statusExplanation(status: string): string {
  switch (status) {
    case "cancelled":
      return "The time has been released. Book again whenever suits you.";
    case "completed":
      return "Thanks for coming in.";
    case "no_show":
      return "The time was held and nobody came. You can book again whenever suits you.";
    default:
      return "";
  }
}
