import type { Appointment, Board, OrgSummary, SlotsResponse, Ticket } from "../api/types";

/**
 * Payloads shaped exactly as internal/api serves them.
 *
 * Fixtures rather than a running server: a component test is asking "given
 * this payload, what does the customer see", and the answer should not
 * depend on a database being up. The shapes are pinned by the Go view
 * tests on the other side, so a field renamed there fails a test there,
 * and these say what the front end does with the field once it arrives.
 */

export const MANILA = "Asia/Manila";

export function orgSummary(overrides: Partial<OrgSummary> = {}): OrgSummary {
  return {
    org: {
      name: "Demo Clinic",
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
      {
        slug: "check-up",
        name: "Annual check-up",
        description: "",
        duration_minutes: 45,
        price_cents: null,
      },
    ],
    queues: [
      {
        slug: "walk-ins",
        name: "Walk-ins",
        open: true,
        waiting: 3,
        estimated_wait_minutes: 30,
      },
    ],
    ...overrides,
  };
}

export function board(overrides: Partial<Board["queue"]> = {}, waiting = [42, 43, 44]): Board {
  return {
    queue: {
      organization: "Demo Clinic",
      queue: "Walk-ins",
      open: true,
      now_serving: 41,
      waiting: waiting.length,
      in_progress: 1,
      served_today: 12,
      estimated_wait_minutes: 30,
      service_day: "2026-04-02",
      generated_at: "2026-04-02T09:30:00+08:00",
      ...overrides,
    },
    waiting_numbers: waiting,
    in_progress_numbers: [41],
  };
}

export function ticket(overrides: Partial<Ticket> = {}): Ticket {
  return {
    id: "11111111-1111-1111-1111-111111111111",
    number: 44,
    customer_name: "Ada",
    status: "waiting",
    status_label: "Waiting",
    position: 3,
    estimated_wait_minutes: 30,
    cancellable: true,
    service_day: "2026-04-02",
    issued_at: "2026-04-02T09:15:00+08:00",
    ...overrides,
  };
}

export function slotsResponse(overrides: Partial<SlotsResponse> = {}): SlotsResponse {
  return {
    service: {
      slug: "consultation",
      name: "Consultation",
      description: "A twenty minute appointment with a clinician.",
      duration_minutes: 20,
      price_cents: 150000,
    },
    date: "2026-04-02",
    slots: [
      {
        start: "2026-04-02T10:00:00+08:00",
        end: "2026-04-02T10:20:00+08:00",
        staff_name: "Dana",
        token: "2026-04-02T10:00:00+08:00|staff-1",
      },
      {
        start: "2026-04-02T10:20:00+08:00",
        end: "2026-04-02T10:40:00+08:00",
        staff_name: "Dana",
        token: "2026-04-02T10:20:00+08:00|staff-1",
      },
    ],
    earliest_bookable: "2026-04-02T09:45:00+08:00",
    latest_bookable: "2026-06-01T09:30:00+08:00",
    ...overrides,
  };
}

export function appointment(overrides: Partial<Appointment> = {}): Appointment {
  return {
    id: "22222222-2222-2222-2222-222222222222",
    service: "Consultation",
    staff_name: "Dana",
    customer_name: "Ada Lovelace",
    starts_at: "2026-04-02T10:00:00+08:00",
    ends_at: "2026-04-02T10:20:00+08:00",
    status: "booked",
    cancellable: true,
    ...overrides,
  };
}
