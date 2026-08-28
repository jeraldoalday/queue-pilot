import type {
  Appointment,
  ApiErrorBody,
  Board,
  BookRequest,
  ErrorCode,
  JoinRequest,
  OrgSummary,
  SlotsResponse,
  Ticket,
} from "./types";

/**
 * One place that talks to the API, so every call gets the same treatment:
 * the same base path, the same JSON headers, the same abort handling and
 * the same failure type. A component calling `fetch` itself is a component
 * that will eventually get one of those wrong, and the one it gets wrong
 * is usually the error branch nobody exercises by hand.
 */

/**
 * Requests go to a relative path, so the front end is served from the same
 * origin as the API and needs no configuration to find it. That is the
 * intended deployment; the dev server proxies /api to the Go binary to
 * match it. VITE_API_BASE_URL exists for the case that deployment is not
 * possible — a static host in front of an API elsewhere — and is a
 * deliberate opt-in rather than the default, because a hardcoded origin is
 * the thing that breaks when a business puts the app on its own domain.
 */
const BASE: string = import.meta.env["VITE_API_BASE_URL"] ?? "";

/**
 * ApiError is every failure this module raises, including the ones that
 * never reached the server.
 *
 * `code` is what a component branches on. A network failure or an
 * unparseable body is reported as "internal" rather than as a separate
 * kind, because the only honest thing to tell a customer about either is
 * that it did not work — and giving the two different shapes would push
 * that decision into every call site.
 */
export class ApiError extends Error {
  readonly code: ErrorCode;
  readonly status: number;

  constructor(code: ErrorCode, message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
  }

  /** Whether retrying the same request could plausibly succeed. */
  get retryable(): boolean {
    return this.code === "internal" || this.code === "rate_limited";
  }
}

/**
 * AbortError is raised when a request is cancelled — a poll interrupted by
 * a page change, or a slot lookup superseded because the customer picked
 * another date.
 *
 * It is a separate class so callers can ignore it. Rendering "something
 * went wrong" because the user navigated away is the single most common
 * way a polling UI ends up showing an error for a healthy app.
 */
export class AbortError extends Error {
  constructor() {
    super("request aborted");
    this.name = "AbortError";
  }
}

export function isAbort(err: unknown): err is AbortError {
  return err instanceof AbortError;
}

interface RequestOptions {
  signal?: AbortSignal;
  body?: unknown;
  method?: "GET" | "POST";
}

async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const { signal, body, method = body === undefined ? "GET" : "POST" } = opts;

  let response: Response;
  try {
    response = await fetch(`${BASE}${path}`, {
      method,
      // Content-Type is set only when there is a body. Sending it on a GET
      // makes the request non-simple for no reason, which costs every read
      // a preflight round trip before it can start.
      headers: body === undefined ? {} : { "Content-Type": "application/json" },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      ...(signal ? { signal } : {}),
    });
  } catch (err) {
    if (signal?.aborted || (err instanceof Error && err.name === "AbortError")) {
      throw new AbortError();
    }
    throw new ApiError(
      "internal",
      "We couldn't reach the server. Check your connection and try again.",
      0,
    );
  }

  if (!response.ok) {
    throw await errorFrom(response);
  }

  // 204 has no body. Nothing here returns one today, but a caller awaiting
  // JSON that never arrives fails with a parse error that describes the
  // wrong problem.
  if (response.status === 204) {
    return undefined as T;
  }

  try {
    return (await response.json()) as T;
  } catch {
    throw new ApiError("internal", "The server sent a reply we couldn't read.", response.status);
  }
}

/**
 * Turns a failed response into an ApiError.
 *
 * A body that is not the API's envelope is still a failure, and it is
 * mapped by status rather than guessed at. That case is not hypothetical:
 * a proxy or a load balancer in front of the app answers 502 with its own
 * HTML, and a client that only understood the envelope would report
 * "unexpected token <" to a customer.
 */
async function errorFrom(response: Response): Promise<ApiError> {
  let parsed: ApiErrorBody | undefined;
  try {
    parsed = (await response.json()) as ApiErrorBody;
  } catch {
    parsed = undefined;
  }

  if (parsed?.error?.code) {
    return new ApiError(parsed.error.code, parsed.error.message, response.status);
  }

  if (response.status === 404) {
    return new ApiError("not_found", "Not found.", 404);
  }
  if (response.status === 429) {
    return new ApiError(
      "rate_limited",
      "That was a lot of requests at once. Please wait a moment and try again.",
      429,
    );
  }
  return new ApiError("internal", "Something went wrong. Please try again.", response.status);
}

const encode = encodeURIComponent;

/**
 * The API surface, one function per endpoint. Every path segment is
 * encoded: a slug is business-supplied and an appointment ID arrives from
 * a URL the customer may have edited, and neither should be able to change
 * which endpoint is called by containing a slash.
 */
export const api = {
  org(orgSlug: string, signal?: AbortSignal): Promise<OrgSummary> {
    return request(`/api/v1/orgs/${encode(orgSlug)}`, { ...(signal ? { signal } : {}) });
  },

  board(orgSlug: string, queueSlug: string, signal?: AbortSignal): Promise<Board> {
    return request(`/api/v1/orgs/${encode(orgSlug)}/queues/${encode(queueSlug)}`, {
      ...(signal ? { signal } : {}),
    });
  },

  joinQueue(
    orgSlug: string,
    queueSlug: string,
    body: JoinRequest,
    signal?: AbortSignal,
  ): Promise<Ticket> {
    return request(`/api/v1/orgs/${encode(orgSlug)}/queues/${encode(queueSlug)}/tickets`, {
      body,
      ...(signal ? { signal } : {}),
    });
  },

  ticket(
    orgSlug: string,
    queueSlug: string,
    ticketId: string,
    signal?: AbortSignal,
  ): Promise<Ticket> {
    return request(
      `/api/v1/orgs/${encode(orgSlug)}/queues/${encode(queueSlug)}/tickets/${encode(ticketId)}`,
      { ...(signal ? { signal } : {}) },
    );
  },

  cancelTicket(
    orgSlug: string,
    queueSlug: string,
    ticketId: string,
    signal?: AbortSignal,
  ): Promise<Ticket> {
    return request(
      `/api/v1/orgs/${encode(orgSlug)}/queues/${encode(queueSlug)}/tickets/${encode(ticketId)}/cancel`,
      { method: "POST", ...(signal ? { signal } : {}) },
    );
  },

  slots(
    orgSlug: string,
    serviceSlug: string,
    date: string,
    signal?: AbortSignal,
  ): Promise<SlotsResponse> {
    const query = date ? `?date=${encode(date)}` : "";
    return request(
      `/api/v1/orgs/${encode(orgSlug)}/services/${encode(serviceSlug)}/slots${query}`,
      { ...(signal ? { signal } : {}) },
    );
  },

  book(
    orgSlug: string,
    serviceSlug: string,
    body: BookRequest,
    signal?: AbortSignal,
  ): Promise<Appointment> {
    return request(`/api/v1/orgs/${encode(orgSlug)}/services/${encode(serviceSlug)}/bookings`, {
      body,
      ...(signal ? { signal } : {}),
    });
  },

  appointment(
    orgSlug: string,
    appointmentId: string,
    signal?: AbortSignal,
  ): Promise<Appointment> {
    return request(`/api/v1/orgs/${encode(orgSlug)}/appointments/${encode(appointmentId)}`, {
      ...(signal ? { signal } : {}),
    });
  },

  cancelAppointment(
    orgSlug: string,
    appointmentId: string,
    signal?: AbortSignal,
  ): Promise<Appointment> {
    return request(
      `/api/v1/orgs/${encode(orgSlug)}/appointments/${encode(appointmentId)}/cancel`,
      { method: "POST", ...(signal ? { signal } : {}) },
    );
  },
};

export type Api = typeof api;
