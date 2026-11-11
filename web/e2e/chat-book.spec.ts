import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Book in a business's conversation (docs/spec/business-chat-tools.md §3):
// a Book tile after Their shop when the other business offers services,
// and a sheet that picks a service, a day and a time, and books. Their
// services and times are answered here; the panel and the sheet are real.
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () =>
  String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@book.test`,
      name: `${tenant.split(" ")[0]} Owner`,
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

async function linked(browser: Browser) {
  const stamp = letters();
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, `Bisi Salon ${stamp}`);
  const buyer = await (
    await browser.newContext({ viewport: { width: 390, height: 844 } })
  ).newPage();
  await signUp(buyer, `Ada Kitchen ${stamp}`);
  await seller.request.put("/v1/link-settings", { headers: H, data: { findable: true } });
  const found = await (
    await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`bisi salon ${stamp}`)}`, { headers: H })
  ).json();
  await buyer.request.post("/v1/links", {
    headers: H,
    data: { business: found.results[0].business, role: "supplier" },
  });
  const link = (await (await seller.request.get("/v1/links", { headers: H })).json()).links[0].id as string;
  await seller.request.post(`/v1/links/${link}/accept`, { headers: H });
  return { seller, buyer, link };
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.evaluate((t) => {
      if (t === "dark") document.documentElement.dataset.theme = "dark";
      else delete document.documentElement.dataset.theme;
    }, scheme);
    await page.waitForTimeout(300);
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
}

const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });

const services = {
  open: true,
  phone: "",
  services: [
    { id: "s1", name: "Haircut", minutes: 30, price_kobo: 300_000, description: "Wash and cut" },
    { id: "s2", name: "Braids", minutes: 90 },
  ],
};

const noSideways = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth);

test("Book: their services, a day and a time, booked", async ({ browser }) => {
  const { seller, buyer, link } = await linked(browser);
  await buyer.request.put("/v1/settings/chat-tools", { headers: H, data: { chat_tools: true } });

  // They offer no services: no Book tile.
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane.getByRole("button", { name: "Open tools" }).click();
  const tools = buyer.getByRole("region", { name: "Tools" });
  await expect(tools.getByRole("button", { name: /^Their shop/ })).toBeVisible();
  await expect(tools.getByRole("button", { name: /^Book/ })).toHaveCount(0);

  // They do.
  const days: [string, string] = ["2026-11-20", "2026-11-21"];
  const at = (d: string, hhmm: string) => new Date(`${d}T${hhmm}:00+01:00`).toISOString();
  let booked: Record<string, unknown> | undefined;
  let bookKey: string | undefined;
  await buyer.route(`**/v1/link-chats/${link}/services`, (r) => r.fulfill(json(services)));
  await buyer.route(`**/v1/link-chats/${link}/services/*/times*`, (r) => {
    const day = new URL(r.request().url()).searchParams.get("day") ?? days[0];
    return r.fulfill(
      json({ service: services.services[0], days, day, times: [at(day, "09:00"), at(day, "09:30"), at(day, "10:00"), at(day, "14:30")] }),
    );
  });
  await buyer.route(`**/v1/link-chats/${link}/bookings`, (r) => {
    booked = r.request().postDataJSON() as Record<string, unknown>;
    bookKey = r.request().headers()["idempotency-key"];
    return r.fulfill(
      json(
        {
          booking: {
            reference: "BS-7K2Q9M",
            service_id: "s1",
            service_name: "Haircut",
            starts_at: booked.start,
            ends_at: booked.start,
            price_kobo: 300_000,
          },
        },
        201,
      ),
    );
  });
  await buyer.reload();
  await pane.getByRole("button", { name: "Open tools" }).click();
  const tile = tools.getByRole("button", { name: /^Book/ });
  await expect(tile).toContainText("2 services");
  // After Their shop.
  const names = await tools.locator(".tk-tile-name").allTextContents();
  expect(names.indexOf("Book")).toBe(names.indexOf("Their shop") + 1);
  await axeBoth(buyer, "the tools with Book");
  await tile.click();

  const sheet = buyer.getByTestId("book-sheet");
  await expect(sheet.getByTestId("book-service")).toHaveCount(2);
  await expect(sheet).toContainText("30 min · ₦3,000");
  await expect(sheet).toContainText("Price on the day");
  expect(await noSideways(buyer)).toBe(true);
  await axeBoth(buyer, "Book: the services");

  await sheet.getByRole("button", { name: /Haircut/ }).click();
  const daysGroup = sheet.getByRole("group", { name: "Day" });
  await expect(daysGroup.getByRole("button", { name: "Fri 20 Nov" })).toHaveAttribute("aria-pressed", "true");
  await daysGroup.getByRole("button", { name: "Sat 21 Nov" }).click();
  await expect(daysGroup.getByRole("button", { name: "Sat 21 Nov" })).toHaveAttribute("aria-pressed", "true");
  const timesGroup = sheet.getByRole("group", { name: "Time" });
  await timesGroup.getByRole("button", { name: "14:30" }).click();
  expect(await noSideways(buyer)).toBe(true);
  await axeBoth(buyer, "Book: a day and a time");
  await sheet.getByRole("button", { name: /Sat 21 Nov at 14:30/ }).click();

  await expect(sheet).toContainText("You pay Bisi Salon");
  const phone = sheet.getByLabel("Phone number they can reach you on");
  const book = sheet.getByRole("button", { name: "Book", exact: true });
  await expect(book).toBeDisabled();
  await phone.fill("0803 123 4567");
  await sheet.getByLabel("Note for them").fill("Two of us");
  await axeBoth(buyer, "Book: check your booking");
  await book.click();
  await expect(buyer.getByText(/Booked with Bisi Salon \w+: Haircut, Sat 21 Nov, 14:30/)).toBeVisible();
  expect(booked).toMatchObject({ service_id: "s1", start: at(days[1], "14:30"), note: "Two of us", phone: "0803 123 4567" });
  expect(bookKey).toBeTruthy();
  void seller;
});

test("Book: they take no bookings online", async ({ browser }) => {
  const { buyer, link } = await linked(browser);
  await buyer.setViewportSize({ width: 360, height: 780 });
  await buyer.request.put("/v1/settings/chat-tools", { headers: H, data: { chat_tools: true } });
  await buyer.route(`**/v1/link-chats/${link}/services`, (r) =>
    r.fulfill(json({ ...services, open: false, closed: "Bisi Salon does not take bookings online right now. Ask them here in the chat." })),
  );
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane.getByRole("button", { name: "Open tools" }).click();
  const tile = buyer.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Book/ });
  await expect(tile).toContainText("Not taking bookings online");
  await tile.click();
  const sheet = buyer.getByTestId("book-sheet");
  await expect(sheet.getByTestId("book-closed")).toContainText("does not take bookings online");
  await expect(sheet.getByTestId("book-service")).toHaveCount(2);
  await expect(sheet.getByRole("button", { name: /Haircut/ })).toHaveCount(0);
  expect(await noSideways(buyer)).toBe(true);
  await axeBoth(buyer, "Book: not taking bookings");
});
