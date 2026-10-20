import { expect, test } from "@playwright/test";
import { axe, describe } from "./a11y";

// axe is injected into the page, which the Content Security Policy would refuse.
test.use({ bypassCSP: true });

test("an owner drafts a storefront", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("link", { name: "Storefront" }).click();
  await expect(page.getByRole("heading", { name: "Storefront", exact: true })).toBeVisible();
  await expect(page.getByText("You have no storefront yet.")).toBeVisible();

  // The address follows the name until one is typed.
  await page.getByLabel("Business name").fill("Mama Put Kitchen");
  await expect(page.getByLabel("Page address")).toHaveValue("mama-put-kitchen");
  await page.getByLabel("Tagline").fill("Hot jollof and swallow");
  await page.getByLabel("Phone").fill("0803 123 4567");
  await page.getByRole("radio", { name: /Bold/ }).check();
  await page.getByRole("radio", { name: "blue" }).check();

  // Hours: Monday open, Tuesday copies it.
  await page.getByRole("checkbox", { name: "Monday open" }).check();
  await page.getByLabel("Monday opens").fill("08:00");
  await page.getByLabel("Monday closes").fill("18:30");
  await page.getByRole("button", { name: "Tuesday: same as Monday" }).click();
  await expect(page.getByLabel("Tuesday closes")).toHaveValue("18:30");

  const vs = await axe(page);
  expect(vs, describe("/storefront", vs)).toEqual([]);

  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Draft saved." })).toBeVisible();
  await expect(page.getByText("Not published yet")).toBeVisible();

  // It comes back as saved.
  await page.reload();
  await expect(page.getByLabel("Business name")).toHaveValue("Mama Put Kitchen");
  await expect(page.getByLabel("Tuesday opens")).toHaveValue("08:00");
  await expect(page.getByRole("radio", { name: /Bold/ })).toBeChecked();
});

test("an owner sets up the question form and finds the orders", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("link", { name: "Storefront" }).click();
  // The draft from the test above has the forms.
  const forms = page.getByRole("region", { name: "Forms" });
  const questions = forms.getByRole("article", { name: "Questions" });
  await expect(questions.getByText("No workflow set up yet.")).toBeVisible();
  await expect(forms.getByRole("article", { name: "Orders" }).getByRole("button", { name: "Set up orders" })).toBeVisible();

  // Set up adds the default workflow and publishes it to prod.
  await questions.getByRole("button", { name: "Set up questions" }).click();
  await expect(questions.getByRole("link", { name: "Open its workflow for questions" })).toBeVisible();
  await questions.getByRole("switch", { name: "Show this form on your page" }).check();
  await questions.getByLabel("Button label").fill("Ask us anything");
  await questions.getByRole("button", { name: "Save questions" }).click();
  await expect(questions.getByRole("status")).toHaveText("Saved. It shows on your page once you publish it.");
  await expect(questions.getByText("On", { exact: true })).toBeVisible();

  // An order offers payment, the Paystack connection and how it is collected.
  const orders = forms.getByRole("article", { name: "Orders" });
  await orders.getByRole("button", { name: "Set up orders" }).click();
  await expect(orders.getByRole("radio", { name: /Customer pays after you accept/ })).toBeChecked();
  await orders.getByRole("checkbox", { name: "Delivery" }).check();
  await orders.getByLabel("Delivery note").fill("Yaba and Surulere only");
  await orders.getByRole("radio", { name: /No online payment/ }).check();
  await expect(orders.getByLabel("Paystack connection")).toHaveCount(0);

  const vs = await axe(page);
  expect(vs, describe("/storefront (forms)", vs)).toEqual([]);

  // It comes back as saved.
  await page.reload();
  await expect(questions.getByLabel("Button label")).toHaveValue("Ask us anything");
  await expect(questions.getByRole("switch", { name: "Show this form on your page" })).toBeChecked();

  await page.getByRole("navigation", { name: "Storefront" }).getByRole("link", { name: "Orders" }).click();
  await expect(page.getByRole("heading", { name: "Orders", exact: true })).toBeVisible();
  await expect(page.getByText("No orders yet.")).toBeVisible();
  await page.getByLabel("Show").selectOption("All");
  await expect(page).toHaveURL(/status=all/);
});

test("an owner sets booking hours and the booking form", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("link", { name: "Storefront", exact: true }).click();
  await page.getByRole("navigation", { name: "Storefront" }).getByRole("link", { name: "Bookings" }).click();
  await expect(page.getByRole("heading", { name: "Bookings", exact: true })).toBeVisible();
  await expect(page.getByText("No bookings in the next 30 days.")).toBeVisible();
  await page.getByRole("search", { name: "Filter bookings" }).getByLabel("Show").selectOption("This week");
  await expect(page).toHaveURL(/range=week/);
  await expect(page.getByText("No bookings this week.")).toBeVisible();

  // Bookings follow the opening hours from the draft until told otherwise.
  const hours = page.getByRole("region", { name: "Booking hours" });
  const same = hours.getByRole("switch", { name: /Same as opening hours/ });
  await expect(same).toBeChecked();
  await expect(hours.getByText("08:00 to 18:30").first()).toBeVisible();
  await same.uncheck();
  await hours.getByLabel("Monday bookings from").fill("09:00");
  await hours.getByLabel("Monday bookings until").fill("13:00");
  await hours.getByRole("button", { name: "Add a booking period on Monday" }).click();
  await hours.getByLabel("Monday bookings from (period 2)").fill("14:00");
  await hours.getByLabel("Monday bookings until (period 2)").fill("18:00");
  await hours.getByRole("checkbox", { name: "Saturday takes bookings" }).check();
  // A period must end after it starts.
  await hours.getByLabel("Saturday bookings until").fill("08:00");
  await expect(page.getByText("Booking hours on Saturday must end after they start, the same day.")).toBeVisible();
  await hours.getByLabel("Saturday bookings until").fill("15:00");
  await hours.getByLabel("Add a day off").fill("2026-12-25");
  await hours.getByRole("button", { name: "Add day off" }).click();
  await expect(hours.getByText("Fri 25 Dec")).toBeVisible();

  const rules = page.getByRole("region", { name: "Booking rules" });
  await rules.getByLabel("How far ahead (days)").fill("60");
  await rules.getByLabel("How soon, amount").fill("1");
  await rules.getByRole("switch", { name: /Confirm bookings automatically/ }).check();

  const vs = await axe(page);
  expect(vs, describe("/storefront/bookings", vs)).toEqual([]);

  await rules.getByRole("button", { name: "Save booking settings" }).click();
  await expect(rules.getByRole("status")).toContainText("Saved.");

  // It comes back as saved.
  await page.reload();
  await expect(hours.getByRole("switch", { name: /Same as opening hours/ })).not.toBeChecked();
  await expect(hours.getByLabel("Monday bookings from (period 2)")).toHaveValue("14:00");
  await expect(hours.getByLabel("Saturday bookings until")).toHaveValue("15:00");
  await expect(hours.getByText("Fri 25 Dec")).toBeVisible();
  await expect(rules.getByLabel("How far ahead (days)")).toHaveValue("60");
  await expect(rules.getByLabel("How soon, amount")).toHaveValue("1");
  await expect(rules.getByRole("switch", { name: /Confirm bookings automatically/ })).toBeChecked();

  // The booking form: payment said for appointments, a deposit, and the
  // server's refusal without a services table.
  await page.getByRole("navigation", { name: "Storefront" }).getByRole("link", { name: "Page" }).click();
  const booking = page.getByRole("region", { name: "Forms" }).getByRole("article", { name: "Bookings" });
  await booking.getByRole("button", { name: "Set up bookings" }).click();
  await expect(booking.getByRole("radio", { name: /Pay after I confirm/ })).toBeChecked();
  await booking.getByRole("radio", { name: /Pay when booking/ }).check();
  await booking.getByLabel("Deposit (per cent)").fill("150");
  await expect(booking.getByText("The deposit is 1 to 100 per cent, or leave it empty for the full price.")).toBeVisible();
  await booking.getByLabel("Deposit (per cent)").fill("30");
  await booking.getByRole("radio", { name: /Pay at the appointment/ }).check();
  await expect(booking.getByLabel("Deposit (per cent)")).toHaveCount(0);
  await booking.getByRole("switch", { name: "Show this form on your page" }).check();
  await booking.getByRole("button", { name: "Save bookings" }).click();
  await expect(booking.getByRole("alert")).toContainText("Choose the services table before taking bookings");
});
