import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "../api/client";
import { AppointmentPage } from "./AppointmentPage";
import { appointment } from "../test/fixtures";
import { renderAt } from "../test/render";

const { appointmentMock, cancelMock } = vi.hoisted(() => ({
  appointmentMock: vi.fn(),
  cancelMock: vi.fn(),
}));

vi.mock("../api/client", async () => {
  const actual = await vi.importActual<typeof import("../api/client")>("../api/client");
  return {
    ...actual,
    api: { ...actual.api, appointment: appointmentMock, cancelAppointment: cancelMock },
  };
});

function renderAppointment() {
  return renderAt(<AppointmentPage />, {
    path: "/:orgSlug/appointments/:appointmentId",
    url: "/demo/appointments/22222222-2222-2222-2222-222222222222",
  });
}

beforeEach(() => {
  appointmentMock.mockReset().mockResolvedValue(appointment());
  cancelMock.mockReset();
});

describe("AppointmentPage", () => {
  it("says what was booked, when, and with whom", async () => {
    renderAppointment();

    expect(await screen.findByRole("heading", { name: "Consultation" })).toBeInTheDocument();
    expect(screen.getByText("Thursday 2 April at 10:00 am")).toBeInTheDocument();
    expect(screen.getByText(/With Dana, until 10:20 am/)).toBeInTheDocument();
    expect(screen.getByText(/Booked for Ada Lovelace/)).toBeInTheDocument();
  });

  // The time comes off the wire already rendered in the business's zone.
  // Converting it again moves it by the offset, which is how a customer
  // turns up at two in the morning.
  it("shows the business's own clock, not the reader's", async () => {
    renderAppointment();

    expect(await screen.findByText(/at 10:00 am/)).toBeInTheDocument();
    expect(screen.queryByText(/2:00 am/)).not.toBeInTheDocument();
  });

  it("offers to cancel a booking that has not started", async () => {
    renderAppointment();
    expect(
      await screen.findByRole("button", { name: /cancel this appointment/i }),
    ).toBeInTheDocument();
  });

  // Cancellable is decided on the server, the same way the endpoint will
  // decide it. Offering the button for an appointment that began ten
  // minutes ago is offering a button that fails when pressed.
  it("hides the cancel button once the server says it no longer applies", async () => {
    appointmentMock.mockResolvedValue(appointment({ cancellable: false }));
    renderAppointment();

    await screen.findByRole("heading", { name: "Consultation" });
    expect(
      screen.queryByRole("button", { name: /cancel this appointment/i }),
    ).not.toBeInTheDocument();
  });

  it("re-reads the booking after cancelling so the page shows the new state", async () => {
    const user = userEvent.setup();
    cancelMock.mockResolvedValue(appointment({ status: "cancelled", cancellable: false }));
    renderAppointment();

    await screen.findByRole("button", { name: /cancel this appointment/i });
    appointmentMock.mockResolvedValue(
      appointment({ status: "cancelled", cancellable: false }),
    );
    await user.click(screen.getByRole("button", { name: /cancel this appointment/i }));

    expect(
      await screen.findByRole("heading", { name: /this appointment was cancelled/i }),
    ).toBeInTheDocument();
    expect(screen.getByText(/time has been released/i)).toBeInTheDocument();
  });

  // Unlike a walk-in ticket, a refused cancellation is reported. An
  // appointment that has already started is one the customer may still be
  // expected at, and telling them "cancelled" when it was not is how
  // somebody stays home from an appointment nobody released.
  it("reports a cancellation the server refused", async () => {
    const user = userEvent.setup();
    cancelMock.mockRejectedValue(
      new ApiError("not_cancellable", "This appointment has already started.", 409),
    );
    renderAppointment();

    await screen.findByRole("button", { name: /cancel this appointment/i });
    await user.click(screen.getByRole("button", { name: /cancel this appointment/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/already started/i);
  });

  it("explains a missed appointment rather than leaving it unexplained", async () => {
    appointmentMock.mockResolvedValue(appointment({ status: "no_show", cancellable: false }));
    renderAppointment();

    expect(
      await screen.findByRole("heading", { name: /this appointment was missed/i }),
    ).toBeInTheDocument();
  });

  it("does not cancel twice when the button is pressed twice", async () => {
    const user = userEvent.setup();
    let release: ((a: unknown) => void) | undefined;
    cancelMock.mockImplementation(() => new Promise((resolve) => (release = resolve)));
    renderAppointment();

    const button = await screen.findByRole("button", { name: /cancel this appointment/i });
    await user.click(button);
    await waitFor(() => expect(button).toBeDisabled());
    await user.click(button);

    expect(cancelMock).toHaveBeenCalledTimes(1);
    await act(async () => {
      release?.(appointment());
    });
  });

  // The link is the credential, so a wrong or expired one has to fail the
  // same way every other missing thing does.
  it("shows the shared not-found wording for a link that resolves to nothing", async () => {
    appointmentMock.mockRejectedValue(new ApiError("not_found", "Not found.", 404));
    renderAppointment();

    expect(
      await screen.findByRole("heading", { name: /couldn.t find that page/i }),
    ).toBeInTheDocument();
  });
});

describe("AppointmentPage calendar download", () => {
  // A confirmation page is only open for as long as the tab is. The
  // download is what puts the time somewhere it will still be answered
  // next Tuesday.
  it("offers the booking as a calendar file", async () => {
    renderAppointment();

    const link = await screen.findByRole("link", { name: "Add to calendar" });
    expect(link).toHaveAttribute(
      "href",
      "/api/v1/orgs/demo/appointments/22222222-2222-2222-2222-222222222222/calendar.ics",
    );
  });

  // A cancelled booking still has a file — that file is how a calendar
  // already holding the event finds out it is off — but "add to calendar"
  // is not what somebody reading a cancellation came for.
  it("does not offer it for a booking that is no longer on", async () => {
    appointmentMock.mockResolvedValue(appointment({ status: "cancelled", cancellable: false }));
    renderAppointment();

    expect(await screen.findByRole("heading", { name: /cancelled/i })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Add to calendar" })).not.toBeInTheDocument();
  });
});
