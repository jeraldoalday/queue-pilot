import type { ApiError } from "../api/client";
import { Notice } from "./Notice";

/**
 * The two states every page shares before it has anything to show.
 *
 * They are a component rather than copied JSX because the *wording* is the
 * decision, not the markup. A missing page has to say the same thing
 * whether it was a mistyped org slug or a deleted queue — the API answers
 * both identically so the URL space cannot be probed, and a front end that
 * distinguished them in prose would give away exactly what the API is
 * careful to withhold.
 */

export function Loading({ label = "Loading" }: { label?: string }) {
  return (
    <div className="card card--message" role="status">
      <p className="muted">{label}…</p>
    </div>
  );
}

export function ErrorState({
  error,
  onRetry,
}: {
  error: ApiError;
  onRetry?: () => void;
}) {
  if (error.code === "not_found") {
    return (
      <div className="card card--message">
        <h1>We couldn&rsquo;t find that page</h1>
        <p className="muted">
          The link may be mistyped, or the business may have taken it down. If
          you scanned a code, try scanning it again.
        </p>
      </div>
    );
  }

  return (
    <div className="card card--message">
      <Notice tone="error">{error.message}</Notice>
      {/* A retry button is only offered where retrying could work. The same
          validation failure retried with the same input fails identically,
          and a button that cannot succeed is worse than no button. */}
      {error.retryable && onRetry ? (
        <button type="button" onClick={onRetry}>
          Try again
        </button>
      ) : null}
    </div>
  );
}
