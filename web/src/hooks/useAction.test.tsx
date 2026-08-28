import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { AbortError, ApiError } from "../api/client";
import { useAction } from "./useAction";

describe("useAction", () => {
  it("returns what the action produced", async () => {
    const { result } = renderHook(() => useAction(async (n: number) => n * 2));

    let value: number | undefined;
    await act(async () => {
      value = await result.current.run(21);
    });

    expect(value).toBe(42);
    expect(result.current.pending).toBe(false);
    expect(result.current.error).toBeUndefined();
  });

  it("reports pending while the action is in flight", async () => {
    let release: (() => void) | undefined;
    const perform = vi.fn(
      () =>
        new Promise<string>((resolve) => {
          release = () => resolve("done");
        }),
    );

    const { result } = renderHook(() => useAction(perform));

    act(() => {
      void result.current.run();
    });
    await waitFor(() => expect(result.current.pending).toBe(true));

    await act(async () => {
      release?.();
    });
    await waitFor(() => expect(result.current.pending).toBe(false));
  });

  // The whole reason for the guard: these actions are not idempotent, and
  // a customer on a slow connection tapping "join" twice would otherwise
  // take two numbers.
  it("drops a second call while the first is still running", async () => {
    let release: (() => void) | undefined;
    const perform = vi.fn(
      () =>
        new Promise<string>((resolve) => {
          release = () => resolve("ticket");
        }),
    );

    const { result } = renderHook(() => useAction(perform));

    act(() => {
      void result.current.run();
    });
    await waitFor(() => expect(result.current.pending).toBe(true));

    let second: string | undefined = "not undefined";
    await act(async () => {
      second = await result.current.run();
    });

    expect(second).toBeUndefined();
    expect(perform).toHaveBeenCalledTimes(1);

    await act(async () => {
      release?.();
    });
  });

  it("allows another call once the first has finished", async () => {
    const perform = vi.fn(async () => "ok");
    const { result } = renderHook(() => useAction(perform));

    await act(async () => {
      await result.current.run();
    });
    await act(async () => {
      await result.current.run();
    });

    expect(perform).toHaveBeenCalledTimes(2);
  });

  // The server words its rules for a person to read, so the message is
  // shown as it arrives rather than translated into something vaguer.
  it("keeps the API's own message for the form to show", async () => {
    const perform = vi.fn(async () => {
      throw new ApiError("validation", "please enter a name", 422);
    });

    const { result } = renderHook(() => useAction(perform));
    await act(async () => {
      await result.current.run();
    });

    expect(result.current.error?.code).toBe("validation");
    expect(result.current.error?.message).toBe("please enter a name");
    expect(result.current.pending).toBe(false);
  });

  it("wraps a failure that is not an ApiError rather than leaking it", async () => {
    const perform = vi.fn(async () => {
      throw new TypeError("undefined is not a function");
    });

    const { result } = renderHook(() => useAction(perform));
    await act(async () => {
      await result.current.run();
    });

    expect(result.current.error).toBeInstanceOf(ApiError);
    expect(result.current.error?.message).not.toContain("undefined is not a function");
  });

  it("treats an abort as neither success nor failure", async () => {
    const perform = vi.fn(async () => {
      throw new AbortError();
    });

    const { result } = renderHook(() => useAction(perform));

    let value: unknown = "not undefined";
    await act(async () => {
      value = await result.current.run();
    });

    expect(value).toBeUndefined();
    expect(result.current.error).toBeUndefined();
  });

  it("clears the last error on request", async () => {
    const perform = vi.fn(async () => {
      throw new ApiError("validation", "nope", 422);
    });

    const { result } = renderHook(() => useAction(perform));
    await act(async () => {
      await result.current.run();
    });
    expect(result.current.error).toBeDefined();

    act(() => result.current.reset());
    expect(result.current.error).toBeUndefined();
  });

  it("clears the previous error when the action is run again", async () => {
    let fail = true;
    const perform = vi.fn(async () => {
      if (fail) throw new ApiError("validation", "nope", 422);
      return "ok";
    });

    const { result } = renderHook(() => useAction(perform));
    await act(async () => {
      await result.current.run();
    });
    expect(result.current.error).toBeDefined();

    fail = false;
    await act(async () => {
      await result.current.run();
    });
    expect(result.current.error).toBeUndefined();
  });
});
