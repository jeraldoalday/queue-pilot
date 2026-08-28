import { defineConfig, devices } from "@playwright/test";

/**
 * End-to-end against the built app, with the API answered at the network
 * boundary rather than by a running Go server.
 *
 * That is a deliberate line. The Go side already proves what only a real
 * Postgres can prove — the double-booking constraint, concurrent ticket
 * numbering, one winner per status change, tenant isolation — in its own
 * integration tests. Repeating any of that through a browser would be
 * slower, flakier, and would not prove anything the database tests do not
 * already. What a browser proves is what those tests cannot touch: that a
 * customer can get from a QR code to a number in their hand, that the
 * page updates without being reloaded, that the back button does not book
 * a second appointment.
 *
 * The payloads the mocks return are the fixtures the component tests use,
 * which are in turn shaped by the Go view tests. A field renamed in
 * internal/api fails a Go test first, which is where it should fail.
 */
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  // A committed .only would quietly narrow CI to one test and stay green
  // forever, which is the worst possible way for a suite to fail.
  forbidOnly: !!process.env["CI"],
  retries: process.env["CI"] ? 1 : 0,
  reporter: process.env["CI"] ? "github" : "list",

  use: {
    baseURL: "http://localhost:4173",
    // A trace for the first failure only. Tracing everything makes a green
    // run slow and leaves a hundred artefacts nobody opens.
    trace: "on-first-retry",
  },

  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
    {
      // Almost every page here is read on a phone held in a shop. A
      // layout that only works on a desktop is a layout that does not
      // work.
      name: "mobile",
      use: { ...devices["Pixel 7"] },
    },
  ],

  webServer: {
    command: "npm run build && npm run preview",
    url: "http://localhost:4173",
    reuseExistingServer: !process.env["CI"],
    timeout: 120_000,
  },
});
