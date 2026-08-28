import type { Page, Route } from "@playwright/test";

/**
 * The API, answered in the browser.
 *
 * Each handler is a function of the request rather than a fixed payload,
 * so a test can move the queue on between polls and assert that the page
 * followed. That is the thing these tests exist to prove and the thing a
 * static fixture cannot express.
 */

export const MANILA = "Asia/Manila";

export interface MockState {
  orgName: string;
  queueName: string;
  queueOpen: boolean;
  nowServing: number | null;
  waiting: number[];
  inProgress: number[];
  servedToday: number;
  waitMinutes: number;
  /** Set to fail the next board read, to exercise the stale-board path. */
  boardFails: boolean;
  ticket: {
    id: string;
    number: number;
    status: string;
    statusLabel: string;
    position: number | null;
    waitMinutes: number | null;
    cancellable: boolean;
  };
  slots: Array<{ start: string; end: string; staff: string; token: string }>;
  /** The error the next booking attempt should fail with, if any. */
  bookFailure: { status: number; code: string; message: string } | null;
  appointment: {
    id: string;
    status: string;
    cancellable: boolean;
  };
}

export function defaultState(): MockState {
  return {
    orgName: "Demo Clinic",
    queueName: "Walk-ins",
    queueOpen: true,
    nowServing: 41,
    waiting: [42, 43, 44],
    inProgress: [41],
    servedToday: 12,
    waitMinutes: 30,
    boardFails: false,
    ticket: {
      id: "11111111-1111-1111-1111-111111111111",
      number: 45,
      status: "waiting",
      statusLabel: "Waiting",
      position: 4,
      waitMinutes: 40,
      cancellable: true,
    },
    slots: [
      {
        start: "2026-04-02T10:00:00+08:00",
        end: "2026-04-02T10:20:00+08:00",
        staff: "Dana",
        token: "2026-04-02T10:00:00+08:00|staff-1",
      },
      {
        start: "2026-04-02T10:20:00+08:00",
        end: "2026-04-02T10:40:00+08:00",
        staff: "Dana",
        token: "2026-04-02T10:20:00+08:00|staff-1",
      },
    ],
    bookFailure: null,
    appointment: {
      id: "22222222-2222-2222-2222-222222222222",
      status: "booked",
      cancellable: true,
    },
  };
}

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({
    status,
    contentType: "application/json; charset=utf-8",
    body: JSON.stringify(body),
  });
}

function apiError(route: Route, status: number, code: string, message: string) {
  return json(route, { error: { code, message } }, status);
}

/**
 * Installs the mock and returns the state object. Mutating it changes what
 * the next request sees, which is how a test moves a queue along.
 */
export async function mockApi(page: Page, overrides: Partial<MockState> = {}) {
  const state: MockState = { ...defaultState(), ...overrides };

  // The payloads describe a fixed day, so the browser has to agree about
  // what day it is — the booking page opens on "today" and bounds its
  // picker by the window the API sent, and against a real clock those two
  // are years apart. setFixedTime pins Date and Date.now only; setInterval
  // keeps running, which the polling tests depend on.
  await page.clock.setFixedTime(new Date("2026-04-02T01:30:00Z")); // 09:30 in Manila

  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const method = route.request().method();

    // The org summary: the front door.
    if (/^\/api\/v1\/orgs\/[^/]+$/.test(path)) {
      return json(route, {
        org: {
          name: state.orgName,
          slug: "demo",
          timezone: MANILA,
          local_time: "2026-04-02T09:30:00+08:00",
        },
        services: [
          {
            slug: "consultation",
            name: "Consultation",
            description: "A twenty minute appointment with a clinician.",
            duration_minutes: 20,
            price_cents: 150000,
          },
        ],
        queues: [
          {
            slug: "walk-ins",
            name: state.queueName,
            open: state.queueOpen,
            waiting: state.waiting.length,
            estimated_wait_minutes: state.waitMinutes,
          },
        ],
      });
    }

    // Joining a line.
    if (/\/queues\/[^/]+\/tickets$/.test(path) && method === "POST") {
      if (!state.queueOpen) {
        return apiError(
          route,
          409,
          "queue_closed",
          "This line is not accepting walk-ins right now.",
        );
      }
      const body = route.request().postDataJSON() as { customer_name?: string };
      if (!body.customer_name?.trim()) {
        return apiError(route, 422, "validation", "please enter a name");
      }
      return json(route, ticketPayload(state), 201);
    }

    if (/\/tickets\/[^/]+\/cancel$/.test(path) && method === "POST") {
      state.ticket = {
        ...state.ticket,
        status: "cancelled",
        statusLabel: "Cancelled",
        position: null,
        waitMinutes: null,
        cancellable: false,
      };
      return json(route, ticketPayload(state));
    }

    if (/\/queues\/[^/]+\/tickets\/[^/]+$/.test(path)) {
      return json(route, ticketPayload(state));
    }

    // An unknown slug 404s, so a test can prove the front end says the
    // same thing for a queue that does not exist as for a business that
    // does not — which is the whole point of the API answering both the
    // same way.
    const queueSlug = /\/queues\/([^/]+)/.exec(path)?.[1];
    if (queueSlug !== undefined && queueSlug !== "walk-ins") {
      return apiError(route, 404, "not_found", "Not found.");
    }

    // The board, which is the thing that moves between polls.
    if (/\/queues\/[^/]+$/.test(path)) {
      if (state.boardFails) {
        return apiError(route, 500, "internal", "Something went wrong.");
      }
      return json(route, boardPayload(state));
    }

    if (/\/slots$/.test(path)) {
      return json(route, {
        service: {
          slug: "consultation",
          name: "Consultation",
          description: "A twenty minute appointment with a clinician.",
          duration_minutes: 20,
          price_cents: 150000,
        },
        date: url.searchParams.get("date") ?? "2026-04-02",
        slots: state.slots.map((slot) => ({
          start: slot.start,
          end: slot.end,
          staff_name: slot.staff,
          token: slot.token,
        })),
        earliest_bookable: "2026-04-02T09:45:00+08:00",
        latest_bookable: "2026-06-01T09:30:00+08:00",
      });
    }

    if (/\/bookings$/.test(path) && method === "POST") {
      if (state.bookFailure) {
        const failure = state.bookFailure;
        state.bookFailure = null;
        return apiError(route, failure.status, failure.code, failure.message);
      }
      return json(route, appointmentPayload(state), 201);
    }

    if (/\/appointments\/[^/]+\/cancel$/.test(path) && method === "POST") {
      state.appointment = { ...state.appointment, status: "cancelled", cancellable: false };
      return json(route, appointmentPayload(state));
    }

    if (/\/appointments\/[^/]+$/.test(path)) {
      return json(route, appointmentPayload(state));
    }

    return apiError(route, 404, "not_found", "Not found.");
  });

  return state;
}

function boardPayload(state: MockState) {
  return {
    queue: {
      organization: state.orgName,
      queue: state.queueName,
      open: state.queueOpen,
      now_serving: state.nowServing,
      waiting: state.waiting.length,
      in_progress: state.inProgress.length,
      served_today: state.servedToday,
      estimated_wait_minutes: state.waitMinutes,
      service_day: "2026-04-02",
      generated_at: "2026-04-02T09:30:00+08:00",
    },
    waiting_numbers: state.waiting,
    in_progress_numbers: state.inProgress,
  };
}

function ticketPayload(state: MockState) {
  return {
    id: state.ticket.id,
    number: state.ticket.number,
    customer_name: "Ada",
    status: state.ticket.status,
    status_label: state.ticket.statusLabel,
    position: state.ticket.position,
    estimated_wait_minutes: state.ticket.waitMinutes,
    cancellable: state.ticket.cancellable,
    service_day: "2026-04-02",
    issued_at: "2026-04-02T09:15:00+08:00",
  };
}

function appointmentPayload(state: MockState) {
  return {
    id: state.appointment.id,
    service: "Consultation",
    staff_name: "Dana",
    customer_name: "Ada Lovelace",
    starts_at: "2026-04-02T10:00:00+08:00",
    ends_at: "2026-04-02T10:20:00+08:00",
    status: state.appointment.status,
    cancellable: state.appointment.cancellable,
  };
}
