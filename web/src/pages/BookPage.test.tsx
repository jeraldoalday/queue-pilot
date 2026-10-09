import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "../api/client";
import { BookPage } from "./BookPage";
import { appointment, orgSummary, slotsResponse } from "../test/fixtures";
import { renderAt } from "../test/render";

const { orgMock, slotsMock, bookMock } = vi.hoisted(() => ({
  orgMock: vi.fn(),
  slotsMock: vi.fn(),
  bookMock: vi.fn(),
}));

vi.mock("../api/client", async () => {
  const actual = await vi.importActual<typeof import("../api/client")>("../api/client");
  return {
    ...actual,
    api: { ...actual.api, org: orgMock, slots: slotsMock, book: bookMock },
  };
});

function renderBook() {
  return renderAt(<BookPage />, {
    path: "/:orgSlug/book/:serviceSlug",
    url: "/demo/book/consultation",
    extra: (
      <Route path="/:orgSlug/appointments/:appointmentId" element={<p>confirmation</p>} />
    ),
  });
}

beforeEach(() => {
  // Pinned so "today" is a fixed day and the date arithmetic under test is
  // the page's, not the calendar's.
  vi.setSystemTime(new Date("2026-04-02T01:30:00Z")); // 09:30 in Manila
  orgMock.mockReset().mockResolvedValue(orgSummary());
  slotsMock.mockReset().mockResolvedValue(slotsResponse());
  bookMock.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("BookPage", () => {
  it("opens on the business's own today, not the viewer's", async () => {
    renderBook();

    await screen.findByRole("heading", { name: "Consultation" });
    expect(slotsMock).toHaveBeenCalledWith("demo", "consultation", "2026-04-02", expect.anything());
    expect(screen.getByText("Thursday 2 April")).toBeInTheDocument();
  });

  it("shows each open time as its own choice", async () => {
    renderBook();

    expect(await screen.findByRole("button", { name: "10:00 am" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "10:20 am" })).toBeInTheDocument();
  });

  // The customer is never asked to choose a person: the server assigns
  // somebody who performs the service and is free then, and asking would
  // turn a two-click booking into a directory of employees.
  it("does not ask the customer to choose a staff member", async () => {
    renderBook();

    await screen.findByRole("button", { name: "10:00 am" });
    expect(screen.queryByLabelText(/staff|who/i)).not.toBeInTheDocument();
  });

  it("hides the details form until a time is chosen", async () => {
    const user = userEvent.setup();
    renderBook();

    await screen.findByRole("button", { name: "10:00 am" });
    expect(screen.queryByLabelText(/your name/i)).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "10:00 am" }));
    expect(screen.getByLabelText(/your name/i)).toBeInTheDocument();
  });

  it("names the booking on the submit button, so nobody books the wrong hour", async () => {
    const user = userEvent.setup();
    renderBook();

    await user.click(await screen.findByRole("button", { name: "10:20 am" }));
    expect(
      screen.getByRole("button", { name: "Book 10:20 am on Thursday 2 April" }),
    ).toBeInTheDocument();
  });

  it("books the slot the customer picked and sends them to the confirmation", async () => {
    const user = userEvent.setup();
    bookMock.mockResolvedValue(appointment());
    renderBook();

    await user.click(await screen.findByRole("button", { name: "10:00 am" }));
    await user.type(screen.getByLabelText(/your name/i), "Ada Lovelace");
    await user.type(screen.getByLabelText(/^email$/i), "ada@example.com");
    await user.click(screen.getByRole("button", { name: /^Book / }));

    expect(bookMock).toHaveBeenCalledWith("demo", "consultation", {
      slot: "2026-04-02T10:00:00+08:00|staff-1",
      customer_name: "Ada Lovelace",
      customer_email: "ada@example.com",
      customer_phone: "",
    });
    expect(await screen.findByText("confirmation")).toBeInTheDocument();
  });

  it("moves a day at a time and asks for that day's slots", async () => {
    const user = userEvent.setup();
    renderBook();

    await screen.findByRole("button", { name: "10:00 am" });
    await user.click(screen.getByRole("button", { name: /next day/i }));

    await waitFor(() =>
      expect(slotsMock).toHaveBeenCalledWith(
        "demo",
        "consultation",
        "2026-04-03",
        expect.anything(),
      ),
    );
  });

  // The picker is bounded by the window the server validates against. A
  // customer who tries three times and is refused three times stops
  // trying.
  it("will not step back before the first bookable day", async () => {
    renderBook();

    await screen.findByRole("button", { name: "10:00 am" });
    expect(screen.getByRole("button", { name: /previous day/i })).toBeDisabled();
  });

  // The window's own end is still a bookable day — it is where the current
  // instant plus 60 days falls, not the day before it — so stepping onto
  // it one day at a time must work the same as picking it in the date
  // input, whose max already allows it.
  it("allows stepping forward onto the very last bookable day, not just up to it", async () => {
    renderBook();

    await screen.findByRole("button", { name: "10:00 am" });
    const input = screen.getByLabelText(/pick a date/i) as HTMLInputElement;

    // One day short of the window's end (2026-06-01, per slotsResponse()).
    fireEvent.change(input, { target: { value: "2026-05-31" } });
    await waitFor(() =>
      expect(slotsMock).toHaveBeenLastCalledWith(
        "demo",
        "consultation",
        "2026-05-31",
        expect.anything(),
      ),
    );

    expect(screen.getByRole("button", { name: /next day/i })).not.toBeDisabled();
  });

  it("bounds the date input by the same window", async () => {
    renderBook();

    const input = (await screen.findByLabelText(/pick a date/i)) as HTMLInputElement;
    expect(input.min).toBe("2026-04-02");
    expect(input.max).toBe("2026-06-01");
  });

  it("says a fully-booked day is empty rather than showing nothing", async () => {
    slotsMock.mockResolvedValue(slotsResponse({ slots: [] }));
    renderBook();

    expect(await screen.findByText(/nothing free on this day/i)).toBeInTheDocument();
  });

  // A stale form is the ordinary case here — the page was rendered before
  // the customer started typing — so the server's refusal has to arrive as
  // something they can act on.
  it("offers to refresh the times when the slot was taken while they typed", async () => {
    const user = userEvent.setup();
    bookMock.mockRejectedValue(
      new ApiError("slot_taken", "That time is no longer available. Please choose another.", 409),
    );
    renderBook();

    await user.click(await screen.findByRole("button", { name: "10:00 am" }));
    await user.type(screen.getByLabelText(/your name/i), "Ada");
    await user.type(screen.getByLabelText(/^email$/i), "ada@example.com");
    await user.click(screen.getByRole("button", { name: /^Book / }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/no longer available/i);

    slotsMock.mockClear();
    await user.click(screen.getByRole("button", { name: /times that are still free/i }));
    await waitFor(() => expect(slotsMock).toHaveBeenCalled());
  });

  it("shows the server's own message for a field the customer must fix", async () => {
    const user = userEvent.setup();
    bookMock.mockRejectedValue(
      new ApiError("validation", "please enter an email we can send the confirmation to", 422),
    );
    renderBook();

    await user.click(await screen.findByRole("button", { name: "10:00 am" }));
    await user.type(screen.getByLabelText(/your name/i), "Ada");
    await user.click(screen.getByRole("button", { name: /^Book / }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/send the confirmation to/i);
    expect(screen.queryByText("confirmation")).not.toBeInTheDocument();
  });

  it("does not book twice when the button is pressed twice", async () => {
    const user = userEvent.setup();
    let release: ((a: unknown) => void) | undefined;
    bookMock.mockImplementation(() => new Promise((resolve) => (release = resolve)));
    renderBook();

    await user.click(await screen.findByRole("button", { name: "10:00 am" }));
    await user.type(screen.getByLabelText(/your name/i), "Ada");
    await user.type(screen.getByLabelText(/^email$/i), "ada@example.com");

    const submit = screen.getByRole("button", { name: /^Book / });
    await user.click(submit);
    await waitFor(() => expect(submit).toBeDisabled());
    await user.click(submit);

    expect(bookMock).toHaveBeenCalledTimes(1);
    await act(async () => {
      release?.(appointment());
    });
  });

  it("shows the shared not-found wording for a service that isn't bookable", async () => {
    slotsMock.mockRejectedValue(new ApiError("not_found", "Not found.", 404));
    renderBook();

    expect(
      await screen.findByRole("heading", { name: /couldn.t find that page/i }),
    ).toBeInTheDocument();
  });
});
