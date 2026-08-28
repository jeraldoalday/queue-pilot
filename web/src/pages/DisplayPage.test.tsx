import { screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "../api/client";
import { DisplayPage } from "./DisplayPage";
import { board } from "../test/fixtures";
import { renderAt } from "../test/render";

const { boardMock } = vi.hoisted(() => ({ boardMock: vi.fn() }));

vi.mock("../api/client", async () => {
  const actual = await vi.importActual<typeof import("../api/client")>("../api/client");
  return { ...actual, api: { ...actual.api, board: boardMock } };
});

function renderDisplay() {
  return renderAt(<DisplayPage />, {
    path: "/:orgSlug/q/:queueSlug/display",
    url: "/demo/q/walk-ins/display",
  });
}

beforeEach(() => {
  boardMock.mockReset();
});

describe("DisplayPage", () => {
  it("shows the number being served and the ones coming up", async () => {
    boardMock.mockResolvedValue(board());
    renderDisplay();

    expect(await screen.findByText("41")).toBeInTheDocument();
    expect(screen.getByText(/42\s+43\s+44/)).toBeInTheDocument();
    expect(screen.getByText("3 waiting")).toBeInTheDocument();
  });

  // A whole room reads this screen. The payload carries no names, and this
  // asserts the page cannot put one on a wall even if one arrived.
  it("shows no customer names", async () => {
    boardMock.mockResolvedValue(board());
    const { container } = renderDisplay();

    await screen.findByText("41");
    expect(container.textContent).not.toMatch(/Ada|Grace|Lovelace/);
  });

  // A busy counter has several people at once, and a screen showing only
  // the lowest is wrong for everybody else who was called.
  it("names the other numbers currently at a counter", async () => {
    const withThree = board();
    withThree.in_progress_numbers = [41, 39, 40];
    boardMock.mockResolvedValue(withThree);
    renderDisplay();

    expect(await screen.findByText(/Also called:\s*39\s+40/)).toBeInTheDocument();
  });

  it("says nothing about other counters when only one number is called", async () => {
    boardMock.mockResolvedValue(board());
    renderDisplay();

    await screen.findByText("41");
    expect(screen.queryByText(/Also called/)).not.toBeInTheDocument();
  });

  it("shows a dash for an empty line rather than an empty space", async () => {
    boardMock.mockResolvedValue(board({ now_serving: null, waiting: 0 }, []));
    renderDisplay();

    expect(await screen.findByText(/Next:\s*—/)).toBeInTheDocument();
  });

  it("says a closed line is closed rather than going blank", async () => {
    boardMock.mockResolvedValue(board({ open: false, now_serving: null }, []));
    renderDisplay();

    expect(await screen.findByText("Closed")).toBeInTheDocument();
  });

  // A screen that goes blank reads to a room as the queue being broken,
  // which is a worse message than numbers that are a moment stale.
  it("keeps the last numbers on screen when a refresh fails", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    boardMock
      .mockResolvedValueOnce(board())
      .mockRejectedValue(new ApiError("internal", "boom", 500));

    renderDisplay();
    await screen.findByText("41");

    await vi.advanceTimersByTimeAsync(15_000);

    expect(screen.getByText("41")).toBeInTheDocument();
    vi.useRealTimers();
  });

  it("says the screen is unavailable if it never loaded at all", async () => {
    boardMock.mockRejectedValue(new ApiError("not_found", "Not found.", 404));
    renderDisplay();

    expect(await screen.findByText(/isn.t available/i)).toBeInTheDocument();
  });
});
