import { useCallback, useRef, useState } from "react";

import { ApiError, isAbort } from "../api/client";

/**
 * Running a write — joining a line, booking a time, cancelling either.
 *
 * The state it tracks is small but it is the state every form gets wrong
 * by hand. `pending` disables the button, which is what stops a customer
 * with a slow connection taking two numbers by tapping twice. `error`
 * holds the API's own message, because the server is where the rules live
 * and it words them for a person to read.
 *
 * A second call while one is in flight is dropped rather than queued. The
 * actions here are not idempotent — the whole point of the guard is that
 * running the same one twice books two appointments.
 */
export interface Action<Args extends unknown[], T> {
  run: (...args: Args) => Promise<T | undefined>;
  pending: boolean;
  error: ApiError | undefined;
  /** Clears the error, for a form that wants a clean slate on edit. */
  reset: () => void;
}

export function useAction<Args extends unknown[], T>(
  perform: (...args: Args) => Promise<T>,
): Action<Args, T> {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<ApiError | undefined>(undefined);
  const running = useRef(false);

  const performRef = useRef(perform);
  performRef.current = perform;

  const run = useCallback(async (...args: Args): Promise<T | undefined> => {
    if (running.current) return undefined;
    running.current = true;
    setPending(true);
    setError(undefined);

    try {
      return await performRef.current(...args);
    } catch (err) {
      if (isAbort(err)) return undefined;
      setError(
        err instanceof ApiError
          ? err
          : new ApiError("internal", "Something went wrong. Please try again.", 0),
      );
      return undefined;
    } finally {
      running.current = false;
      setPending(false);
    }
  }, []);

  const reset = useCallback(() => setError(undefined), []);

  return { run, pending, error, reset };
}
