import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { NowServing } from "./NowServing";

describe("NowServing", () => {
  it("shows the number", () => {
    render(<NowServing number={41} />);
    expect(screen.getByText("41")).toBeInTheDocument();
  });

  // Zero is a number a ticket could plausibly have been given, and a room
  // reading "NOW SERVING 0" spends a moment each working out that it means
  // nobody.
  it("shows a dash rather than a zero when nobody is at a counter", () => {
    render(<NowServing number={null} />);
    expect(screen.getByText("—")).toBeInTheDocument();
    expect(screen.queryByText("0")).not.toBeInTheDocument();
  });

  // A dash reads as "nobody" to somebody looking at it and as nothing at
  // all to a screen reader, so the words are given separately.
  it("says in words what the dash means", () => {
    render(<NowServing number={null} />);
    expect(screen.getByText("Nobody is being served right now")).toBeInTheDocument();
  });

  // The same component is the board's "Now serving" and the ticket page's
  // "Your number". Announcing the first on the second told a customer
  // their own number was somebody else's.
  it("announces the number under whatever label it was given", () => {
    render(<NowServing number={44} label="Your number" />);
    expect(screen.getByText("Your number 44")).toBeInTheDocument();
    expect(screen.queryByText(/now serving/i)).not.toBeInTheDocument();
  });

  it("lets the caller word the empty state for its own context", () => {
    render(<NowServing number={null} label="Your number" emptyText="No number yet" />);
    expect(screen.getByText("No number yet")).toBeInTheDocument();
  });
});
