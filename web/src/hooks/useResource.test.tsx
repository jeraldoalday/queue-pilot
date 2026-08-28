import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AbortError, ApiError } from "../api/client";
import { useResource } from "./useResource";

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
});

afterEach(() => {
  vi.useRealTimers();
});

describe("useResource", () => {
  it("loads once and reports the value", async () => {
    const load = vi.fn(async () => "board");
    const { result } = renderHook(() => useResource(load, []));

    expect(result.current.loading).toBe(true);
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.data).toBe("board");
    expect(result.current.error).toBeUndefined();
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("does not poll unless asked to", async () => {
    const load = vi.fn(async () => "board");
    renderHook(() => useResource(load, []));
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));

    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("re-fetches on the interval it was given", async () => {
    const load = vi.fn(async () => "board");
    renderHook(() => useResource(load, [], { pollMs: 15_000 }));
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));

    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    await waitFor(() => expect(load).toHaveBeenCalledTimes(2));

    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    await waitFor(() => expect(load).toHaveBeenCalledTimes(3));
  });

  // A customer watching their position must not see the page blank every
  // fifteen seconds. `loading` is the first load only; a refresh keeps
  // whatever was already on screen.
  it("keeps the previous value on screen while refreshing", async () => {
    let n = 0;
    const load = vi.fn(async () => `value ${++n}`);
    const { result } = renderHook(() => useResource(load, [], { pollMs: 1_000 }));

    await waitFor(() => expect(result.current.data).toBe("value 1"));

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });

    expect(result.current.loading).toBe(false);
    await waitFor(() => expect(result.current.data).toBe("value 2"));
  });

  // A dropped connection is not a reason to throw away a position in line
  // that was correct fifteen seconds ago.
  it("holds the last good value when a refresh fails", async () => {
    let call = 0;
    const load = vi.fn(async () => {
      call += 1;
      if (call === 2) throw new ApiError("internal", "server fell over", 500);
      return "value";
    });

    const { result } = renderHook(() => useResource(load, [], { pollMs: 1_000 }));
    await waitFor(() => expect(result.current.data).toBe("value"));

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });

    await waitFor(() => expect(result.current.error?.message).toBe("server fell over"));
    expect(result.current.data).toBe("value");
  });

  it("clears the error once a refresh succeeds again", async () => {
    let call = 0;
    const load = vi.fn(async () => {
      call += 1;
      if (call === 1) throw new ApiError("internal", "flaky", 500);
      return "value";
    });

    const { result } = renderHook(() => useResource(load, [], { pollMs: 1_000 }));
    await waitFor(() => expect(result.current.error).toBeDefined());

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    await waitFor(() => expect(result.current.error).toBeUndefined());
    expect(result.current.data).toBe("value");
  });

  // An abort is the hook's own doing — a newer request, or the page going
  // away. Surfacing it would show an error for a healthy app.
  it("ignores an abort rather than reporting it", async () => {
    const load = vi.fn(async () => {
      throw new AbortError();
    });

    const { result } = renderHook(() => useResource(load, []));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBeUndefined();
  });

  // Two requests in flight can land out of order, and the older landing
  // second overwrites the newer. Each new request aborts the one before.
  it("aborts the request in flight before starting another", async () => {
    const signals: AbortSignal[] = [];
    const load = vi.fn(async (signal: AbortSignal) => {
      signals.push(signal);
      return "value";
    });

    renderHook(() => useResource(load, [], { pollMs: 1_000 }));
    await waitFor(() => expect(signals).toHaveLength(1));

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    await waitFor(() => expect(signals).toHaveLength(2));

    expect(signals[0]?.aborted).toBe(true);
    expect(signals[1]?.aborted).toBe(false);
  });

  it("aborts the outstanding request when the page goes away", async () => {
    const signals: AbortSignal[] = [];
    const load = vi.fn(async (signal: AbortSignal) => {
      signals.push(signal);
      return "value";
    });

    const { unmount } = renderHook(() => useResource(load, [], { pollMs: 1_000 }));
    await waitFor(() => expect(signals).toHaveLength(1));

    unmount();
    expect(signals[0]?.aborted).toBe(true);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_000);
    });
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("starts over when its dependencies change", async () => {
    const load = vi.fn(async () => "value");
    const { rerender } = renderHook(({ id }: { id: string }) => useResource(load, [id]), {
      initialProps: { id: "a" },
    });
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));

    rerender({ id: "b" });
    await waitFor(() => expect(load).toHaveBeenCalledTimes(2));
  });

  // A caller writing `useResource((s) => api.board(org, queue, s))` makes a
  // new function every render. Depending on it would restart the poll on
  // every render, which is a request per frame rather than per interval.
  it("does not re-fetch when only the callback identity changes", async () => {
    const load = vi.fn(async (_signal: AbortSignal) => "value");
    const { rerender } = renderHook(() =>
      useResource(async (signal) => load(signal), []),
    );
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));

    rerender();
    rerender();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("fetches nothing while disabled", async () => {
    const load = vi.fn(async () => "value");
    const { result, rerender } = renderHook(
      ({ on }: { on: boolean }) => useResource(load, [], { enabled: on }),
      { initialProps: { on: false } },
    );

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(load).not.toHaveBeenCalled();

    rerender({ on: true });
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));
  });

  it("re-fetches on demand when something changed underneath it", async () => {
    const load = vi.fn(async () => "value");
    const { result } = renderHook(() => useResource(load, []));
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));

    act(() => result.current.refresh());
    await waitFor(() => expect(load).toHaveBeenCalledTimes(2));
  });
});
