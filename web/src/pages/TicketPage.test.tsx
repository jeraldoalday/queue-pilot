import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "../api/client";
import { TicketPage } from "./TicketPage";
import { board, ticket } from "../test/fixtures";
import { renderAt } from "../test/render";

const { boardMock, ticketMock, cancelMock } = vi.hoisted(() => ({
  boardMock: vi.fn(),
  ticketMock: vi.fn(),
  cancelMock: vi.fn(),
}));

vi.mock("../api/client", async () => {
  const actual = await vi.importActual<typeof import("../api/client")>("../api/client");
  return {
    ...actual,
    api: {
      ...actual.api,
      board: boardMock,
      ticket: ticketMock,
      cancelTicket: cancelMock,
    },
  };
});

function renderTicket() {
  return renderAt(<TicketPage />, {
    path: "/:orgSlug/q/:queueSlug/t/:ticketId",
    url: "/demo/q/walk-ins/t/11111111-1111-1111-1111-111111111111",
  });
}

beforeEach(() => {
  boardMock.mockReset().mockResolvedValue(board());
  ticketMock.mockReset().mockResolvedValue(ticket());
  cancelMock.mockReset();
});

describe("TicketPage", () => {
  it("shows the customer their number, their place and what it means in minutes", async () => {
    renderTicket();

    expect(await screen.findByText("Your number 44")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "3 in line" })).toBeInTheDocument();
    expect(screen.getByText(/About 30 minutes/)).toBeInTheDocument();
    expect(screen.getByText("Waiting")).toBeInTheDocument();
  });

  // The front of the line is a statement about position, not a duration,
  // and "About 0 minutes" is not a thing to tell anybody.
  it("tells the customer at the front that they are next", async () => {
    ticketMock.mockResolvedValue(ticket({ position: 1, estimated_wait_minutes: 0 }));
    renderTicket();

    expect(await screen.findByRole("heading", { name: "You're next" })).toBeInTheDocument();
  });

  it("picks the customer's own number out of the line", async () => {
    renderTicket();

    const mine = await screen.findByText((_content, element) =>
      element?.tagName === "LI" && element.textContent?.startsWith("44") === true,
    );
    expect(mine).toHaveClass("is-mine");
  });

  // A countdown for somebody already standing at a counter describes
  // nothing real, so position and wait give way to an instruction.
  it("replaces the countdown with an instruction once the number is called", async () => {
    ticketMock.mockResolvedValue(
      ticket({
        status: "called",
        status_label: "Called",
        position: null,
        estimated_wait_minutes: null,
        cancellable: true,
      }),
    );
    renderTicket();

    expect(await screen.findByText(/head to the counter/i)).toBeInTheDocument();
    expect(screen.queryByText(/in line/)).not.toBeInTheDocument();
  });

  it("explains a resolved ticket rather than leaving the badge to speak for itself", async () => {
    ticketMock.mockResolvedValue(
      ticket({
        status: "no_show",
        status_label: "No-show",
        position: null,
        estimated_wait_minutes: null,
        cancellable: false,
      }),
    );
    renderTicket();

    expect(await screen.findByText(/nobody came, so it was passed over/i)).toBeInTheDocument();
  });

  // The rule about who may leave lives on the server; the page renders
  // what it was told rather than deciding again.
  it("offers 'leave the line' only when the ticket says it is cancellable", async () => {
    renderTicket();
    expect(await screen.findByRole("button", { name: /leave the line/i })).toBeInTheDocument();
  });

  it("hides 'leave the line' for a ticket that can no longer be withdrawn", async () => {
    ticketMock.mockResolvedValue(
      ticket({ status: "serving", status_label: "Being served", position: null, cancellable: false }),
    );
    renderTicket();

    await screen.findByText(/being served now/i);
    expect(screen.queryByRole("button", { name: /leave the line/i })).not.toBeInTheDocument();
  });

  it("re-reads the ticket after leaving so the page shows the new state", async () => {
    const user = userEvent.setup();
    cancelMock.mockResolvedValue(ticket({ status: "cancelled", cancellable: false }));
    renderTicket();

    await screen.findByRole("button", { name: /leave the line/i });

    ticketMock.mockResolvedValue(
      ticket({
        status: "cancelled",
        status_label: "Cancelled",
        position: null,
        estimated_wait_minutes: null,
        cancellable: false,
      }),
    );
    await user.click(screen.getByRole("button", { name: /leave the line/i }));

    expect(cancelMock).toHaveBeenCalledWith(
      "demo",
      "walk-ins",
      "11111111-1111-1111-1111-111111111111",
    );
    expect(await screen.findByText(/this ticket was cancelled/i)).toBeInTheDocument();
  });

  it("reports a counter that got there first, without losing the page", async () => {
    const user = userEvent.setup();
    cancelMock.mockRejectedValue(
      new ApiError("not_cancellable", "This ticket can no longer be cancelled.", 409),
    );
    renderTicket();

    await screen.findByRole("button", { name: /leave the line/i });
    await user.click(screen.getByRole("button", { name: /leave the line/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/no longer be cancelled/i);
    expect(screen.getByText("Your number 44")).toBeInTheDocument();
  });

  it("does not cancel twice when the button is pressed twice", async () => {
    const user = userEvent.setup();
    let release: ((t: unknown) => void) | undefined;
    cancelMock.mockImplementation(() => new Promise((resolve) => (release = resolve)));
    renderTicket();

    const button = await screen.findByRole("button", { name: /leave the line/i });
    await user.click(button);
    await waitFor(() => expect(button).toBeDisabled());
    await user.click(button);

    expect(cancelMock).toHaveBeenCalledTimes(1);
    await act(async () => {
      release?.(ticket());
    });
  });

  // The issued time comes off the wire already rendered in the business's
  // zone. Converting it again would move it by the offset.
  it("shows the issue time as the business's own clock", async () => {
    renderTicket();
    expect(await screen.findByText(/Issued at 9:15 am/)).toBeInTheDocument();
  });

  it("shows the shared not-found wording for a ticket that isn't there", async () => {
    ticketMock.mockRejectedValue(new ApiError("not_found", "Not found.", 404));
    renderTicket();

    expect(
      await screen.findByRole("heading", { name: /couldn.t find that page/i }),
    ).toBeInTheDocument();
  });

  // The ticket is the page; the board beside it is context. Losing the
  // context must not lose the number the customer came here to read.
  it("still shows the ticket when the board alongside it fails", async () => {
    boardMock.mockRejectedValue(new ApiError("internal", "boom", 500));
    renderTicket();

    expect(await screen.findByText("Your number 44")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "3 in line" })).toBeInTheDocument();
  });
});
