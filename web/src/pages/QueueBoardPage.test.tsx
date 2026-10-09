import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "../api/client";
import { QueueBoardPage } from "./QueueBoardPage";
import { board, ticket } from "../test/fixtures";
import { renderAt } from "../test/render";

const { boardMock, joinMock } = vi.hoisted(() => ({
  boardMock: vi.fn(),
  joinMock: vi.fn(),
}));

// The client is mocked rather than fetch, because what these tests are
// about is what a customer sees for a given payload. The client's own
// behaviour — status mapping, aborts, the URLs it builds — is covered
// directly in client.test.ts, and asserting it again through six layers of
// component would only make these tests fail for reasons that are not
// about the page.
vi.mock("../api/client", async () => {
  const actual = await vi.importActual<typeof import("../api/client")>("../api/client");
  return {
    ...actual,
    api: { ...actual.api, board: boardMock, joinQueue: joinMock },
  };
});

function renderBoard() {
  return renderAt(<QueueBoardPage />, {
    path: "/:orgSlug/q/:queueSlug",
    url: "/demo/q/walk-ins",
    extra: <Route path="/:orgSlug/q/:queueSlug/t/:ticketId" element={<p>ticket page</p>} />,
  });
}

beforeEach(() => {
  boardMock.mockReset();
  joinMock.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("QueueBoardPage", () => {
  it("shows what is being served, how long the line is, and who is in it", async () => {
    boardMock.mockResolvedValue(board());
    renderBoard();

    expect(await screen.findByRole("heading", { name: "Walk-ins" })).toBeInTheDocument();
    expect(screen.getByText("Demo Clinic")).toBeInTheDocument();
    expect(screen.getByText("41")).toBeInTheDocument();
    expect(screen.getByText("Open")).toBeInTheDocument();

    for (const number of ["42", "43", "44"]) {
      expect(screen.getByText(number)).toBeInTheDocument();
    }
  });

  // Zero is a number a ticket could plausibly hold, and a room reading
  // "NOW SERVING 0" spends a moment each working out that it means nobody.
  it("shows a dash rather than a zero when nobody is at a counter", async () => {
    boardMock.mockResolvedValue(board({ now_serving: null, in_progress: 0 }, []));
    renderBoard();

    expect(await screen.findByText("—")).toBeInTheDocument();
    expect(screen.getByText(/nobody is being served/i)).toBeInTheDocument();
  });

  it("words the wait for somebody deciding whether to join", async () => {
    boardMock.mockResolvedValue(board({ estimated_wait_minutes: 90 }));
    renderBoard();

    expect(await screen.findByText(/About 1 hour 30 minutes if you join now/)).toBeInTheDocument();
  });

  it("says there is no wait rather than that you are next", async () => {
    boardMock.mockResolvedValue(board({ estimated_wait_minutes: 0 }, []));
    renderBoard();

    expect(await screen.findByText(/No wait right now if you join now/)).toBeInTheDocument();
  });

  // A closed line still shows its numbers — a customer wants to know
  // whether they missed it, not to be told the page does not exist.
  it("hides the join form on a closed line but keeps the day's numbers", async () => {
    boardMock.mockResolvedValue(board({ open: false, served_today: 40 }));
    renderBoard();

    expect(await screen.findByText("Closed")).toBeInTheDocument();
    expect(screen.getByText("40")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /take a number/i })).not.toBeInTheDocument();
  });

  it("sends the customer to their own ticket after joining", async () => {
    const user = userEvent.setup();
    boardMock.mockResolvedValue(board());
    joinMock.mockResolvedValue(ticket());
    renderBoard();

    await screen.findByRole("heading", { name: "Walk-ins" });
    await user.type(screen.getByLabelText(/your name/i), "Ada");
    await user.click(screen.getByRole("button", { name: /take a number/i }));

    expect(joinMock).toHaveBeenCalledWith("demo", "walk-ins", {
      customer_name: "Ada",
      customer_phone: "",
    });
    expect(await screen.findByText("ticket page")).toBeInTheDocument();
  });

  it("sends the phone number when one is given", async () => {
    const user = userEvent.setup();
    boardMock.mockResolvedValue(board());
    joinMock.mockResolvedValue(ticket());
    renderBoard();

    await screen.findByRole("heading", { name: "Walk-ins" });
    await user.type(screen.getByLabelText(/your name/i), "Ada");
    await user.type(screen.getByLabelText(/phone number/i), "+63 917 000 0000");
    await user.click(screen.getByRole("button", { name: /take a number/i }));

    expect(joinMock).toHaveBeenCalledWith("demo", "walk-ins", {
      customer_name: "Ada",
      customer_phone: "+63 917 000 0000",
    });
  });

  // The server words its validation rules for a person to read, so the
  // page shows the server's sentence rather than inventing its own.
  it("shows the server's own message when a join is refused", async () => {
    const user = userEvent.setup();
    boardMock.mockResolvedValue(board());
    joinMock.mockRejectedValue(new ApiError("validation", "please enter a name", 422));
    renderBoard();

    await screen.findByRole("heading", { name: "Walk-ins" });
    await user.click(screen.getByRole("button", { name: /take a number/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent("please enter a name");
    expect(screen.queryByText("ticket page")).not.toBeInTheDocument();
  });

  it("says the line closed underneath them rather than failing silently", async () => {
    const user = userEvent.setup();
    boardMock.mockResolvedValue(board());
    joinMock.mockRejectedValue(
      new ApiError("queue_closed", "This line is not accepting walk-ins right now.", 409),
    );
    renderBoard();

    await screen.findByRole("heading", { name: "Walk-ins" });
    await user.type(screen.getByLabelText(/your name/i), "Ada");
    await user.click(screen.getByRole("button", { name: /take a number/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/not accepting walk-ins/i);
  });

  // A slow connection plus an impatient tap is exactly how a customer ends
  // up holding two numbers, one of which is called to an empty room.
  it("does not take two numbers when the button is pressed twice", async () => {
    const user = userEvent.setup();
    boardMock.mockResolvedValue(board());

    let release: ((t: unknown) => void) | undefined;
    joinMock.mockImplementation(() => new Promise((resolve) => (release = resolve)));
    renderBoard();

    await screen.findByRole("heading", { name: "Walk-ins" });
    await user.type(screen.getByLabelText(/your name/i), "Ada");

    const button = screen.getByRole("button", { name: /take a number/i });
    await user.click(button);
    await waitFor(() => expect(button).toBeDisabled());
    await user.click(button);

    expect(joinMock).toHaveBeenCalledTimes(1);
    await act(async () => {
      release?.(ticket());
    });
  });

  it("shows the shared not-found wording for a queue that isn't there", async () => {
    boardMock.mockRejectedValue(new ApiError("not_found", "Not found.", 404));
    renderBoard();

    expect(await screen.findByRole("heading", { name: /couldn.t find that page/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /try again/i })).not.toBeInTheDocument();
  });

  it("offers a retry only for a failure that retrying could fix", async () => {
    boardMock.mockRejectedValue(new ApiError("internal", "Something went wrong.", 500));
    renderBoard();

    expect(await screen.findByRole("button", { name: /try again/i })).toBeInTheDocument();
  });

  // Replacing a board that was right fifteen seconds ago with a full error
  // page is a worse answer than a board with a note on it.
  it("keeps the board on screen when a refresh fails", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

    boardMock
      .mockResolvedValueOnce(board())
      .mockRejectedValue(new ApiError("internal", "gateway timeout", 504));

    renderBoard();
    await screen.findByRole("heading", { name: "Walk-ins" });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });

    expect(await screen.findByRole("alert")).toHaveTextContent(/may be a moment out of date/i);
    expect(screen.getByRole("heading", { name: "Walk-ins" })).toBeInTheDocument();
    expect(screen.getByText("41")).toBeInTheDocument();
    void user;
  });
});
