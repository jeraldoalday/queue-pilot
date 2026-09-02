import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError, api, isAbort } from "./client";

/**
 * fetch is stubbed rather than mocked through a service worker. The client
 * is thin enough that what is worth testing is exactly what it does with a
 * Response — the status mapping, the abort handling, the URL it builds —
 * and a stub makes each of those a single readable line.
 */
function stubFetch(handler: (url: string, init: RequestInit) => Response | Promise<Response>) {
  const spy = vi.fn(async (input: string | URL | Request, init?: RequestInit) =>
    handler(String(input), init ?? {}),
  );
  vi.stubGlobal("fetch", spy);
  return spy;
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  vi.unstubAllGlobals();
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("request construction", () => {
  it("reads from the versioned API path", async () => {
    const spy = stubFetch(() => json({ org: { slug: "demo" } }));
    await api.org("demo");
    expect(spy.mock.calls[0]?.[0]).toBe("/api/v1/orgs/demo");
  });

  // A slug is business-supplied and an ID arrives from a URL the customer
  // may have edited. Neither should be able to change which endpoint is
  // called by containing a slash.
  it("encodes every path segment", async () => {
    const spy = stubFetch(() => json({}));
    await api.ticket("demo/../admin", "walk ins", "../../../etc");
    expect(spy.mock.calls[0]?.[0]).toBe(
      "/api/v1/orgs/demo%2F..%2Fadmin/queues/walk%20ins/tickets/..%2F..%2F..%2Fetc",
    );
  });

  // Sending Content-Type on a GET makes the request non-simple, which
  // costs every poll a preflight round trip before it can even start.
  it("sends no Content-Type on a read", async () => {
    const spy = stubFetch(() => json({}));
    await api.board("demo", "walk-ins");
    const init = spy.mock.calls[0]?.[1];
    expect(init?.method).toBe("GET");
    expect(init?.headers).toEqual({});
  });

  it("sends a JSON body and its Content-Type on a write", async () => {
    const spy = stubFetch(() => json({ id: "t1" }, 201));
    await api.joinQueue("demo", "walk-ins", {
      customer_name: "Ada",
      customer_phone: "",
    });

    const init = spy.mock.calls[0]?.[1];
    expect(init?.method).toBe("POST");
    expect(init?.headers).toEqual({ "Content-Type": "application/json" });
    expect(JSON.parse(String(init?.body))).toEqual({
      customer_name: "Ada",
      customer_phone: "",
    });
  });

  // A cancel takes no fields, and sending `{}` would be a body the server
  // has to decode for nothing.
  it("posts a cancel without a body", async () => {
    const spy = stubFetch(() => json({ id: "t1" }));
    await api.cancelTicket("demo", "walk-ins", "t1");

    const init = spy.mock.calls[0]?.[1];
    expect(init?.method).toBe("POST");
    expect(init?.body).toBeUndefined();
  });

  it("omits the date query when no date is given", async () => {
    const spy = stubFetch(() => json({ slots: [] }));
    await api.slots("demo", "consult", "");
    expect(spy.mock.calls[0]?.[0]).toBe("/api/v1/orgs/demo/services/consult/slots");
  });

  it("passes the date through as the slots endpoint expects it", async () => {
    const spy = stubFetch(() => json({ slots: [] }));
    await api.slots("demo", "consult", "2026-04-02");
    expect(spy.mock.calls[0]?.[0]).toBe(
      "/api/v1/orgs/demo/services/consult/slots?date=2026-04-02",
    );
  });
});

describe("error mapping", () => {
  // The code is what a component branches on; the message is what it
  // shows. Both come from the server, which is where the rules are.
  it("carries the API's own code and message through", async () => {
    stubFetch(() =>
      json({ error: { code: "slot_taken", message: "That time is no longer available." } }, 409),
    );

    await expect(
      api.book("demo", "consult", {
        slot: "x",
        customer_name: "Ada",
        customer_email: "ada@example.com",
        customer_phone: "",
      }),
    ).rejects.toMatchObject({
      code: "slot_taken",
      message: "That time is no longer available.",
      status: 409,
    });
  });

  // Not hypothetical: a proxy in front of the app answers 502 with its own
  // HTML, and a client that only understood the envelope would show a
  // customer "unexpected token <".
  it("maps a non-envelope failure by status rather than guessing", async () => {
    stubFetch(() => new Response("<html>502 Bad Gateway</html>", { status: 502 }));

    const err = await api.org("demo").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).code).toBe("internal");
    expect((err as ApiError).message).not.toContain("<html>");
  });

  it("maps a bare 404 to not_found", async () => {
    stubFetch(() => new Response("", { status: 404 }));
    await expect(api.org("nope")).rejects.toMatchObject({ code: "not_found" });
  });

  it("maps a bare 429 to rate_limited", async () => {
    stubFetch(() => new Response("", { status: 429 }));
    await expect(
      api.joinQueue("demo", "walk-ins", { customer_name: "Ada", customer_phone: "" }),
    ).rejects.toMatchObject({ code: "rate_limited" });
  });

  it("reports a dropped connection as something the customer can act on", async () => {
    stubFetch(() => {
      throw new TypeError("Failed to fetch");
    });

    const err = (await api.org("demo").catch((e: unknown) => e)) as ApiError;
    expect(err.code).toBe("internal");
    expect(err.status).toBe(0);
    expect(err.message).toMatch(/connection/i);
  });

  it("reports a body it cannot parse without leaking the parser's words", async () => {
    stubFetch(() => new Response("not json at all", { status: 200 }));

    const err = (await api.org("demo").catch((e: unknown) => e)) as ApiError;
    expect(err.code).toBe("internal");
    expect(err.message).not.toMatch(/JSON|token/i);
  });

  // Retrying a validation failure with the same input fails identically,
  // so offering a retry button for one is offering a button that cannot
  // work.
  it("knows which failures are worth retrying", () => {
    expect(new ApiError("internal", "", 500).retryable).toBe(true);
    expect(new ApiError("rate_limited", "", 429).retryable).toBe(true);
    expect(new ApiError("validation", "", 422).retryable).toBe(false);
    expect(new ApiError("not_found", "", 404).retryable).toBe(false);
    expect(new ApiError("slot_taken", "", 409).retryable).toBe(false);
  });
});

describe("aborting", () => {
  // A poll interrupted by a page change is not a failure, and a UI that
  // reports it shows "something went wrong" for a perfectly healthy app
  // every time somebody navigates.
  it("reports a cancelled request as an abort, not an error", async () => {
    stubFetch(
      (_url, init) =>
        new Promise((_resolve, reject) => {
          init.signal?.addEventListener("abort", () => {
            const err = new Error("aborted");
            err.name = "AbortError";
            reject(err);
          });
        }),
    );

    const controller = new AbortController();
    const pending = api.board("demo", "walk-ins", controller.signal);
    controller.abort();

    const err = await pending.catch((e: unknown) => e);
    expect(isAbort(err)).toBe(true);
    expect(err).not.toBeInstanceOf(ApiError);
  });
});

describe("appointmentCalendarUrl", () => {
  it("addresses the booking's calendar file", () => {
    expect(api.appointmentCalendarUrl("demo", "22222222-2222-2222-2222-222222222222")).toBe(
      "/api/v1/orgs/demo/appointments/22222222-2222-2222-2222-222222222222/calendar.ics",
    );
  });

  // The same rule every other path here follows: a slug is
  // business-supplied and an ID arrives from a URL the customer may have
  // edited, so neither may change which endpoint is addressed.
  it("encodes both segments", () => {
    expect(api.appointmentCalendarUrl("a/b", "../secret")).toBe(
      "/api/v1/orgs/a%2Fb/appointments/..%2Fsecret/calendar.ics",
    );
  });
});
