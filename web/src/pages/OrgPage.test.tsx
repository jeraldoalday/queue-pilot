import { screen } from "@testing-library/react";
import { Route } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "../api/client";
import { OrgPage } from "./OrgPage";
import { orgSummary } from "../test/fixtures";
import { renderAt } from "../test/render";

const { orgMock } = vi.hoisted(() => ({ orgMock: vi.fn() }));

vi.mock("../api/client", async () => {
  const actual = await vi.importActual<typeof import("../api/client")>("../api/client");
  return { ...actual, api: { ...actual.api, org: orgMock } };
});

function renderOrg() {
  return renderAt(<OrgPage />, {
    path: "/:orgSlug",
    url: "/demo",
    extra: <Route path="/:orgSlug/q/:queueSlug" element={<p>board</p>} />,
  });
}

beforeEach(() => {
  orgMock.mockReset().mockResolvedValue(orgSummary());
});

describe("OrgPage", () => {
  it("lists the lines to walk into and the services to book", async () => {
    renderOrg();

    expect(await screen.findByRole("heading", { name: "Demo Clinic" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Walk-ins/ })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Consultation/ })).toBeInTheDocument();
  });

  // Somebody in a doorway choosing between two lines is choosing on these
  // numbers, which is why the page polls rather than rendering once.
  it("shows how long each line is and what that means in minutes", async () => {
    renderOrg();

    expect(await screen.findByText(/3 waiting\./)).toBeInTheDocument();
    expect(screen.getByText(/About 30 minutes\./)).toBeInTheDocument();
  });

  it("links a line to its own board", async () => {
    renderOrg();

    const link = await screen.findByRole("link", { name: /Walk-ins/ });
    expect(link).toHaveAttribute("href", "/demo/q/walk-ins");
  });

  it("links a service to its booking page", async () => {
    renderOrg();

    const link = await screen.findByRole("link", { name: /Consultation/ });
    expect(link).toHaveAttribute("href", "/demo/book/consultation");
  });

  // A price of zero means free; a null price means the business does not
  // publish one here. Collapsing the two prints a currency symbol over
  // every service whose price is a conversation.
  it("prints a price only where the business published one", async () => {
    renderOrg();

    await screen.findByRole("link", { name: /Consultation/ });
    expect(screen.getByRole("link", { name: /Consultation/ })).toHaveTextContent("1,500");
    expect(screen.getByRole("link", { name: /Annual check-up/ })).not.toHaveTextContent("0.00");
  });

  it("shows the service duration so a customer can plan around it", async () => {
    renderOrg();

    expect(await screen.findByText(/20 min/)).toBeInTheDocument();
    expect(screen.getByText(/45 min/)).toBeInTheDocument();
  });

  // A business partway through setting itself up should say so rather than
  // render an empty page that reads as broken.
  it("says so plainly when there is nothing on offer yet", async () => {
    orgMock.mockResolvedValue(orgSummary({ services: [], queues: [] }));
    renderOrg();

    expect(await screen.findByText(/hasn.t opened any lines/i)).toBeInTheDocument();
  });

  it("shows only the section it has something for", async () => {
    orgMock.mockResolvedValue(orgSummary({ services: [] }));
    renderOrg();

    expect(await screen.findByText("Walk in")).toBeInTheDocument();
    expect(screen.queryByText("Book a time")).not.toBeInTheDocument();
  });

  it("shows the shared not-found wording for a business that isn't there", async () => {
    orgMock.mockRejectedValue(new ApiError("not_found", "Not found.", 404));
    renderOrg();

    expect(
      await screen.findByRole("heading", { name: /couldn.t find that page/i }),
    ).toBeInTheDocument();
  });
});
