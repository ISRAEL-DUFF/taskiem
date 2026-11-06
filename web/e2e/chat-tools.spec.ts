import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Chat tools, phase 1 (docs/spec/chat-tools.md): an owner turns the switch
// on in Settings; then "@Taskiem" in a business conversation goes to
// Taskiem only, and the sender is told where its answer is.
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () =>
  String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@tools.test`,
      name: `${tenant.split(" ")[0]} Owner`,
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

async function linked(browser: Browser) {
  const stamp = letters();
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, `Dangote Depot ${stamp}`);
  const buyer = await (
    await browser.newContext({ viewport: { width: 390, height: 844 } })
  ).newPage();
  await signUp(buyer, `Ada Kitchen ${stamp}`);
  await seller.request.put("/v1/link-settings", { headers: H, data: { findable: true } });
  const found = await (
    await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`dangote depot ${stamp}`)}`, { headers: H })
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

test("the switch, and @Taskiem in a business conversation", async ({ browser }) => {
  const { seller, buyer, link } = await linked(browser);
  await buyer.goto("/settings");
  const box = buyer.getByLabel("Name a business to message it");
  await expect(box).not.toBeChecked();
  await box.check();
  await expect(box).toBeChecked();
  await axeBoth(buyer, "Settings with chat tools");
  expect((await (await buyer.request.get("/v1/settings/chat-tools", { headers: H })).json()).chat_tools).toBe(true);

  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane.getByLabel("Message").fill("@Taskiem status");
  await pane.getByLabel("Message").press("Enter");
  const note = buyer.locator(".toast", { hasText: "Only Taskiem saw that" });
  await expect(note).toBeVisible();
  await expect(note.getByRole("link", { name: "Open" })).toHaveAttribute("href", "/chat?with=taskiem");
  await expect(pane.locator('[data-testid="chat-message"][data-mine="true"]')).toHaveCount(0);
  const theirs = (await (await seller.request.get(`/v1/link-chats/${link}/messages`, { headers: H })).json()).messages as { kind: string }[];
  expect(theirs.filter((m) => m.kind !== "card")).toEqual([]);
});
