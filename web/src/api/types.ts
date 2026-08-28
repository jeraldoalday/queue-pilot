/**
 * The wire shapes, mirroring internal/api/views.go field for field.
 *
 * They are written by hand rather than generated. A generator would be
 * worth it for a large or fast-moving surface; this one is a dozen types
 * that change when a product decision changes, and hand-writing them means
 * the *reason* a field is nullable travels with the field. `position` is
 * null for a customer at the counter, not zero, because somebody being
 * served has no place in a line — that is the sort of thing a `number |
 * null` from a generator states without explaining.
 */

/** Who the business is, and what time it is where they are. */
export interface Org {
  name: string;
  slug: string;
  /** An IANA zone name. Every time in every payload is rendered in it. */
  timezone: string;
  /**
   * The org's own clock at the moment of the response. The front end needs
   * it to decide what "today" means without asking the viewer's device,
   * which is routinely wrong by a day for anyone reading from abroad.
   */
  local_time: string;
}

export interface Service {
  slug: string;
  name: string;
  description: string;
  duration_minutes: number;
  /**
   * Null when the business does not publish a price here. A service priced
   * at zero is free, which is a different claim, so this must not be
   * collapsed to `0`.
   */
  price_cents: number | null;
}

export interface QueueSummary {
  slug: string;
  name: string;
  open: boolean;
  waiting: number;
  estimated_wait_minutes: number;
}

export interface OrgSummary {
  org: Org;
  services: Service[];
  queues: QueueSummary[];
}

/** The published board payload, unchanged — see internal/queue/api.go. */
export interface PublicStatus {
  organization: string;
  queue: string;
  open: boolean;
  /** Null when nobody is at a counter. Zero is a number a ticket could hold. */
  now_serving: number | null;
  waiting: number;
  in_progress: number;
  served_today: number;
  estimated_wait_minutes: number;
  service_day: string;
  generated_at: string;
}

export interface Board {
  queue: PublicStatus;
  /** The line as a screen draws it: numbers only, lowest first. */
  waiting_numbers: number[];
  in_progress_numbers: number[];
}

export type TicketStatus =
  | "waiting"
  | "called"
  | "serving"
  | "done"
  | "no_show"
  | "cancelled";

export interface Ticket {
  id: string;
  number: number;
  customer_name: string;
  status: TicketStatus;
  /** The status in words, worded once on the server so pages agree. */
  status_label: string;
  /** Null once the ticket stops waiting: no place in a line to report. */
  position: number | null;
  estimated_wait_minutes: number | null;
  /**
   * Whether "leave the line" applies. It is on the wire rather than
   * derived here so the rule exists in one language — the one with the
   * tests behind it.
   */
  cancellable: boolean;
  service_day: string;
  issued_at: string;
}

export interface Slot {
  start: string;
  end: string;
  staff_name: string;
  /**
   * The value that books this slot. It names the instant and the staff
   * member together because they have to travel together, and it is not
   * trusted on the way back — the server re-derives the day and matches.
   */
  token: string;
}

export interface SlotsResponse {
  service: Service;
  date: string;
  slots: Slot[];
  /** The ends of the booking window, so the picker cannot offer a day
   * the booking endpoint would refuse. */
  earliest_bookable: string;
  latest_bookable: string;
}

export type AppointmentStatus =
  | "booked"
  | "completed"
  | "cancelled"
  | "no_show";

export interface Appointment {
  id: string;
  service: string;
  staff_name: string;
  customer_name: string;
  starts_at: string;
  ends_at: string;
  status: AppointmentStatus;
  cancellable: boolean;
}

export interface JoinRequest {
  customer_name: string;
  customer_phone: string;
}

export interface BookRequest {
  slot: string;
  customer_name: string;
  customer_email: string;
  customer_phone: string;
}

/**
 * The error codes the API answers with. They are a closed set on the
 * server too — see internal/api/api.go — so a code arriving here that is
 * not in this union means the two have drifted, which is worth a type
 * error rather than a silent fallback.
 */
export type ErrorCode =
  | "not_found"
  | "validation"
  | "queue_closed"
  | "slot_taken"
  | "too_soon"
  | "too_far_ahead"
  | "not_cancellable"
  | "rate_limited"
  | "internal";

export interface ApiErrorBody {
  error: { code: ErrorCode; message: string };
}
