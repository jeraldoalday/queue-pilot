import { useCallback, useEffect, useRef, useState } from "react";

import { ApiError, isAbort } from "../api/client";

/**
 * Loading a payload, and then keeping it fresh.
 *
 * Almost every page here is a live view of something that changes without
 * the customer doing anything: a line moves, a number is called, a slot is
 * taken. The server-rendered pages solve that by polling a partial with
 * htmx; this is the same idea with the same interval, and most of the code
 * is about the two failure modes polling introduces.
 *
 * The first is flicker. A refresh must not blank the page it is
 * refreshing, so the previous value is held while the next one is in
 * flight, and `loading` is only true for the first load. A customer
 * watching their position must not see it vanish every fifteen seconds.
 *
 * The second is the stale answer. Two requests in flight can land out of
 * order, and the older one landing second overwrites the newer. Each new
 * request aborts the one before it, so there is only ever one.
 */

/** The poll interval, matching the htmx pages' own. */
export const POLL_INTERVAL_MS = 15_000;

export interface Resource<T> {
  data: T | undefined;
  /** True only until the first answer — success or failure — arrives. */
  loading: boolean;
  /**
   * The last failure, cleared by a successful refresh. A poll that fails
   * once against a page already showing data leaves the data in place: a
   * dropped connection is not a reason to throw away a position in line
   * that was correct fifteen seconds ago.
   */
  error: ApiError | undefined;
  /** Fetches again immediately, e.g. after an action changed something. */
  refresh: () => void;
}

interface Options {
  /** Milliseconds between refreshes. Omit or pass 0 to load once. */
  pollMs?: number;
  /**
   * Skips fetching entirely. Used where a route parameter has not resolved
   * yet, so the hook can still be called unconditionally.
   */
  enabled?: boolean;
}

/**
 * Fetches `load` and re-fetches it on an interval.
 *
 * `load` is called with an AbortSignal and is expected to honour it. It is
 * held in a ref rather than listed as a dependency: a caller writing
 * `useResource((signal) => api.board(org, queue, signal))` creates a new
 * function on every render, and depending on it would restart the poll on
 * every render — which is a request per frame, not per interval.
 * `deps` is what actually decides when to start over.
 */
export function useResource<T>(
  load: (signal: AbortSignal) => Promise<T>,
  deps: readonly unknown[],
  options: Options = {},
): Resource<T> {
  const { pollMs = 0, enabled = true } = options;

  const [data, setData] = useState<T | undefined>(undefined);
  const [loading, setLoading] = useState(enabled);
  const [error, setError] = useState<ApiError | undefined>(undefined);

  const loadRef = useRef(load);
  loadRef.current = load;

  const inFlight = useRef<AbortController | undefined>(undefined);
  // A nonce that `refresh` bumps to re-run the effect on demand.
  const [nonce, setNonce] = useState(0);
  const refresh = useCallback(() => setNonce((n) => n + 1), []);

  useEffect(() => {
    if (!enabled) {
      setLoading(false);
      return;
    }

    let cancelled = false;

    const run = async () => {
      inFlight.current?.abort();
      const controller = new AbortController();
      inFlight.current = controller;

      try {
        const next = await loadRef.current(controller.signal);
        if (cancelled || controller.signal.aborted) return;
        setData(next);
        setError(undefined);
      } catch (err) {
        // An abort is this hook's own doing — a newer request, or the page
        // going away. Reporting it would show "something went wrong" for a
        // perfectly healthy app the moment somebody navigates.
        if (cancelled || isAbort(err)) return;
        setError(
          err instanceof ApiError
            ? err
            : new ApiError("internal", "Something went wrong. Please try again.", 0),
        );
      } finally {
        if (!cancelled) setLoading(false);
      }
    };

    setLoading((wasLoading) => wasLoading && data === undefined);
    void run();

    if (pollMs <= 0) {
      return () => {
        cancelled = true;
        inFlight.current?.abort();
      };
    }

    const timer = setInterval(() => void run(), pollMs);
    return () => {
      cancelled = true;
      clearInterval(timer);
      inFlight.current?.abort();
    };
    // `data` is deliberately absent: including it would restart the poll
    // every time the poll produced a value.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, pollMs, enabled, nonce]);

  return { data, loading, error, refresh };
}
