import { expect, test } from "@playwright/test";

import { mockApi } from "./api-mock";

/**
 * The walk-in journey, end to end: a customer scans a code in a shop,
 * reads the line, takes a number, watches their position fall, and leaves
 * when they cannot stay.
 *
 * This is the thing no unit test covers — not because any single step is
 * hard, but because the steps are joined by navigation, polling and the
 * back button, and those are where a working set of components stops
 * being a working product.
 */

test("a customer joins the line and lands on their own number", async ({ page }) => {
  await mockApi(page);

  await page.goto("/demo/q/walk-ins");

  await expect(page.getByRole("heading", { name: "Walk-ins" })).toBeVisible();
  await expect(page.getByText("Now serving 41")).toBeVisible();
  await expect(page.getByText("About 30 minutes if you join now.")).toBeVisible();

  await page.getByLabel("Your name").fill("Ada");
  await page.getByRole("button", { name: "Take a number" }).click();

  await expect(page).toHaveURL("/demo/q/walk-ins/t/11111111-1111-1111-1111-111111111111");
  await expect(page.getByText("Your number 45")).toBeVisible();
  await expect(page.getByRole("heading", { name: "4 in line" })).toBeVisible();
});

/**
 * The reason joining replaces the history entry rather than pushing one.
 * A customer who backs onto the board and submits again takes a second
 * number, and the first is then called to an empty room.
 */
test("going back from a ticket does not land on a form that takes a second number", async ({
  page,
}) => {
  await mockApi(page);

  await page.goto("/demo");
  await page.getByRole("link", { name: /Walk-ins/ }).click();
  await page.getByLabel("Your name").fill("Ada");
  await page.getByRole("button", { name: "Take a number" }).click();
  await expect(page.getByText("Your number 45")).toBeVisible();

  await page.goBack();

  await expect(page).toHaveURL("/demo");
  await expect(page.getByRole("heading", { name: "Demo Clinic" })).toBeVisible();
});

/**
 * The page a customer stands and watches. Nobody reloads it, so if the
 * poll does not work the page is wrong for as long as they are looking
 * at it.
 */
test("the board follows the queue without being reloaded", async ({ page }) => {
  const state = await mockApi(page);

  await page.goto("/demo/q/walk-ins");
  await expect(page.getByText("Now serving 41")).toBeVisible();

  state.nowServing = 42;
  state.waiting = [43, 44];
  state.inProgress = [42];

  await expect(page.getByText("Now serving 42")).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText("Now serving 41")).toHaveCount(0);
});

test("a customer watches their own position fall", async ({ page }) => {
  const state = await mockApi(page);

  await page.goto("/demo/q/walk-ins/t/11111111-1111-1111-1111-111111111111");
  await expect(page.getByRole("heading", { name: "4 in line" })).toBeVisible();

  state.ticket = { ...state.ticket, position: 1, waitMinutes: 0 };

  await expect(page.getByRole("heading", { name: "You're next" })).toBeVisible({
    timeout: 20_000,
  });
});

/**
 * Leaving the line is what makes the estimates everyone else sees honest.
 * A customer who walks out silently is called to an empty room and
 * written off as a no-show.
 */
test("a customer leaves the line and the page says so", async ({ page }) => {
  await mockApi(page);

  await page.goto("/demo/q/walk-ins/t/11111111-1111-1111-1111-111111111111");
  await page.getByRole("button", { name: "Leave the line" }).click();

  await expect(page.getByText("This ticket was cancelled.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Leave the line" })).toHaveCount(0);
});

test("a closed line shows its day but takes no more walk-ins", async ({ page }) => {
  await mockApi(page, { queueOpen: false, nowServing: null, waiting: [], servedToday: 40 });

  await page.goto("/demo/q/walk-ins");

  await expect(page.getByText("Closed", { exact: true })).toBeVisible();
  await expect(page.getByText("40", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Take a number" })).toHaveCount(0);
});

test("the server's own words are shown when a join is refused", async ({ page }) => {
  await mockApi(page);

  await page.goto("/demo/q/walk-ins");
  await page.getByRole("button", { name: "Take a number" }).click();

  await expect(page.getByRole("alert")).toHaveText("please enter a name");
  await expect(page).toHaveURL("/demo/q/walk-ins");
});

/**
 * A board that was right fifteen seconds ago is a better answer than an
 * error page. This asserts the numbers survive a failed refresh.
 */
test("a failed refresh leaves the numbers up with a note", async ({ page }) => {
  const state = await mockApi(page);

  await page.goto("/demo/q/walk-ins");
  await expect(page.getByText("Now serving 41")).toBeVisible();

  state.boardFails = true;

  await expect(page.getByRole("alert")).toContainText("may be a moment out of date", {
    timeout: 20_000,
  });
  await expect(page.getByText("Now serving 41")).toBeVisible();
});

/**
 * A whole room reads the waiting-room screen, so what it does not show is
 * as much a requirement as what it does.
 */
test("the waiting-room display shows numbers and no names", async ({ page }) => {
  await mockApi(page);

  await page.goto("/demo/q/walk-ins/display");

  await expect(page.getByText("Now serving 41")).toBeVisible();
  await expect(page.getByText("3 waiting")).toBeVisible();
  await expect(page.locator("body")).not.toContainText("Ada");
  // Nothing on this screen is meant to be pressed — it is left running on
  // a monitor, and a control is something to catch while cleaning it.
  await expect(page.getByRole("button")).toHaveCount(0);
});

test("an unknown queue says the same thing an unknown business does", async ({ page }) => {
  await mockApi(page);

  await page.goto("/demo/q/does-not-exist");

  await expect(page.getByRole("heading", { name: /couldn.t find that page/i })).toBeVisible();
});
