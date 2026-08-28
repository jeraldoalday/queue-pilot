import { Link, useParams } from "react-router-dom";

import { api } from "../api/client";
import { ErrorState, Loading } from "../components/PageState";
import { POLL_INTERVAL_MS, useResource } from "../hooks/useResource";
import { formatDuration, formatPrice } from "../lib/time";
import { formatJoinWait } from "../lib/wait";

/**
 * A business's front door: what they can be booked for, and which lines
 * are open right now.
 *
 * It polls, because half of what it shows is live. Somebody standing in a
 * doorway choosing between two queues is choosing on numbers that were
 * true a moment ago, and a page listing "3 waiting" for a line that
 * cleared ten minutes back sends them to the wrong counter.
 *
 * The whole page comes from one request. Firing three would render in
 * three stages, and the stage a customer sees longest is the empty one.
 */
export function OrgPage() {
  const { orgSlug = "" } = useParams();

  const summary = useResource((signal) => api.org(orgSlug, signal), [orgSlug], {
    pollMs: POLL_INTERVAL_MS,
  });

  if (summary.loading) {
    return (
      <main className="page">
        <Loading />
      </main>
    );
  }
  if (!summary.data && summary.error) {
    return (
      <main className="page">
        <ErrorState error={summary.error} onRetry={summary.refresh} />
      </main>
    );
  }
  if (!summary.data) return null;

  const { org, services, queues } = summary.data;

  return (
    <main className="page">
      <header className="page-header">
        <p className="eyebrow">Book or join a line</p>
        <h1>{org.name}</h1>
      </header>

      {queues.length > 0 ? (
        <section className="card card--flush">
          <h2 style={{ padding: "1rem 1.25rem 0", margin: 0 }}>Walk in</h2>
          <ul className="link-list">
            {queues.map((queue) => (
              <li key={queue.slug}>
                <Link to={`/${orgSlug}/q/${queue.slug}`}>
                  <span className="title">{queue.name}</span>
                  <span className="muted small">
                    {" — "}
                    {queue.waiting} waiting.{" "}
                    {formatJoinWait(queue.estimated_wait_minutes)}.
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      {services.length > 0 ? (
        <section className="card card--flush">
          <h2 style={{ padding: "1rem 1.25rem 0", margin: 0 }}>Book a time</h2>
          <ul className="link-list">
            {services.map((service) => (
              <li key={service.slug}>
                <Link to={`/${orgSlug}/book/${service.slug}`}>
                  <span className="title">{service.name}</span>
                  <span className="muted small">
                    {" — "}
                    {formatDuration(service.duration_minutes)}
                    {/* A price of zero means free; a null price means the
                        business does not publish one here. Collapsing the
                        two would print a currency symbol over every
                        service whose price is a conversation. */}
                    {service.price_cents !== null
                      ? ` · ${formatPrice(service.price_cents)}`
                      : ""}
                  </span>
                  {service.description ? (
                    <p className="muted small" style={{ margin: "0.25rem 0 0" }}>
                      {service.description}
                    </p>
                  ) : null}
                </Link>
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      {queues.length === 0 && services.length === 0 ? (
        <section className="card card--message">
          <h2>Nothing to book just yet</h2>
          <p className="muted">
            {org.name} hasn&rsquo;t opened any lines or published any services
            for online booking. Please get in touch with them directly.
          </p>
        </section>
      ) : null}
    </main>
  );
}
