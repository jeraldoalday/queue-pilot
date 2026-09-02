import { expect, test } from "@playwright/test";

import { mockApi } from "./api-mock";

/**
 * The booking journey: a customer opens a business's page, picks a
 * service, picks a time, books it, and later cancels from the link they
 * were given.
 */

test("a customer books a time and lands on their confirmation", async ({ page }) => {
  await mockApi(page);

  await page.goto("/demo");
  await expect(page.getByRole("heading", { name: "Demo Clinic" })).toBeVisible();

  await page.getByRole("link", { name: /Consultation/ }).click();
  await expect(page).toHaveURL("/demo/book/consultation");

  await page.getByRole("button", { name: "10:00 am" }).click();
  await page.getByLabel("Your name").fill("Ada Lovelace");
  await page.getByLabel("Email", { exact: true }).fill("ada@example.com");
  await page.getByRole("button", { name: /^Book 10:00 am/ }).click();

  await expect(page).toHaveURL("/demo/appointments/22222222-2222-2222-2222-222222222222");
  await expect(page.getByRole("heading", { name: "Consultation" })).toBeVisible();
  await expect(page.getByText("Thursday 2 April at 10:00 am")).toBeVisible();
  await expect(page.getByText("With Dana, until 10:20 am")).toBeVisible();
});

/**
 * The times are the business's, not the reader's. A browser set to a
 * different zone must still be shown the clinic's ten o'clock — this test
 * runs the whole flow in New York and asserts nothing moved.
 */
test.describe("read from another timezone", () => {
  test.use({ timezoneId: "America/New_York" });

  test("shows the business's clock, not the reader's", async ({ page }) => {
    await mockApi(page);

    await page.goto("/demo/book/consultation");
    await expect(page.getByRole("button", { name: "10:00 am" })).toBeVisible();
    // 10:00 in Manila is 22:00 the previous day in New York. Either would
    // be a plausible-looking bug.
    await expect(page.getByRole("button", { name: "10:00 pm" })).toHaveCount(0);

    await page.getByRole("button", { name: "10:00 am" }).click();
    await page.getByLabel("Your name").fill("Ada");
    await page.getByLabel("Email", { exact: true }).fill("ada@example.com");
    await page.getByRole("button", { name: /^Book / }).click();

    await expect(page.getByText("Thursday 2 April at 10:00 am")).toBeVisible();
  });
});

test("the date picker cannot be walked outside the booking window", async ({ page }) => {
  await mockApi(page);

  await page.goto("/demo/book/consultation");

  // The first bookable day is today, so there is nowhere back to go.
  await expect(page.getByRole("button", { name: /Previous day/ })).toBeDisabled();

  await page.getByRole("button", { name: /Next day/ }).click();
  await expect(page.getByText("Friday 3 April")).toBeVisible();
  await expect(page.getByRole("button", { name: /Previous day/ })).toBeEnabled();
});

/**
 * The ordinary failure on this page: the form was rendered before the
 * customer started typing, and somebody else took the slot in between.
 */
test("a slot taken while the customer typed is recoverable", async ({ page }) => {
  const state = await mockApi(page);

  await page.goto("/demo/book/consultation");
  await page.getByRole("button", { name: "10:00 am" }).click();
  await page.getByLabel("Your name").fill("Ada");
  await page.getByLabel("Email", { exact: true }).fill("ada@example.com");

  state.bookFailure = {
    status: 409,
    code: "slot_taken",
    message: "That time is no longer available. Please choose another.",
  };
  await page.getByRole("button", { name: /^Book / }).click();

  await expect(page.getByRole("alert")).toContainText("no longer available");

  state.slots = state.slots.filter((slot) => !slot.start.includes("T10:00"));
  await page.getByRole("button", { name: /times that are still free/i }).click();

  await expect(page.getByRole("button", { name: "10:00 am" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "10:20 am" })).toBeVisible();
});

test("a customer cancels from the link they were given", async ({ page }) => {
  await mockApi(page);

  await page.goto("/demo/appointments/22222222-2222-2222-2222-222222222222");
  await page.getByRole("button", { name: "Cancel this appointment" }).click();

  await expect(
    page.getByRole("heading", { name: "This appointment was cancelled" }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Cancel this appointment" })).toHaveCount(0);
});

/**
 * A booking that has already started is one the customer may still be
 * expected at, so the page must not offer to release it.
 */
test("an appointment that has started offers no cancel button", async ({ page }) => {
  await mockApi(page, {
    appointment: { id: "22222222-2222-2222-2222-222222222222", status: "booked", cancellable: false },
  });

  await page.goto("/demo/appointments/22222222-2222-2222-2222-222222222222");

  await expect(page.getByRole("heading", { name: "Consultation" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Cancel this appointment" })).toHaveCount(0);
});

test("an unknown business says nothing about which businesses exist", async ({ page }) => {
  await page.route("**/api/v1/**", (route) =>
    route.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "not_found", message: "Not found." } }),
    }),
  );

  await page.goto("/nobody-here");

  await expect(page.getByRole("heading", { name: /couldn.t find that page/i })).toBeVisible();
  await expect(page.locator("body")).not.toContainText(/organisation|organization|tenant/i);
});

/**
 * The confirmation page is open for as long as the tab is; the
 * appointment matters next Tuesday. This is the path that puts it
 * somewhere it will still be answered — and it is a real download in a
 * real browser, which is the part no unit test can stand in for.
 */
test("a customer can take their booking away as a calendar file", async ({ page }) => {
  await mockApi(page);

  await page.goto("/demo/appointments/22222222-2222-2222-2222-222222222222");

  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("link", { name: "Add to calendar" }).click(),
  ]);

  // The browser names the file from the header the API sent. Getting this
  // wrong is how every appointment lands in a downloads folder under one
  // invented name.
  expect(download.suggestedFilename()).toBe("demo-clinic-2026-04-02.ics");

  const stream = await download.createReadStream();
  const chunks: Buffer[] = [];
  for await (const chunk of stream) chunks.push(Buffer.from(chunk));
  const file = Buffer.concat(chunks).toString("utf8");

  expect(file).toContain("BEGIN:VEVENT");
  expect(file).toContain("22222222-2222-2222-2222-222222222222@queuepilot");
  // The page is still there behind the download: a customer who saves the
  // file has not been navigated away from their own booking.
  await expect(page.getByRole("heading", { name: "Consultation" })).toBeVisible();
});
