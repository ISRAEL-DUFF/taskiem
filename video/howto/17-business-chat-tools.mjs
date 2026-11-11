// How-to 17: the tools in a chat with another business. In Ada's chat with
// Bayo Rice Mills the tools are about Bayo: his shop at his prices for her,
// their orders and bills, the statement, an invoice to him. From the other
// side, Bayo looks around Ada's storefront (no price list shared: her
// storefront prices, how many are left) and books one of her services. Her
// own tools stay in her Taskiem chat.
//
//   node video/howto/17-business-chat-tools.mjs          # record, then build
//   node video/howto/17-business-chat-tools.mjs build    # build only
//
// Wants a fresh world (video/howto/world.sh). Before recording, it turns
// Ada's online bookings on (9:00 to 17:00 every day), as she would in
// Storefront, Bookings: the seed does not.
import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";

const SLUG = "17-business-chat-tools";
const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});
const ADA = "Ada · Ada's Kitchen, the owner";
const BAYO = "Bayo · Bayo Rice Mills, the owner";

// Ada's bookings, on: her services table, open 9 to 5, the booking form
// with its workflow. Through the API, signed in as Ada.
async function bookingsOn() {
  let cookie = "";
  const call = async (method, path, body) => {
    const r = await fetch(BASE + path, {
      method,
      headers: { "X-Taskiem-Request": "1", "Content-Type": "application/json", cookie },
      body: body ? JSON.stringify(body) : undefined,
    });
    const set = r.headers.getSetCookie?.() ?? [];
    if (set.length) cookie = set.map((c) => c.split(";")[0]).join("; ");
    const text = await r.text();
    if (r.status >= 300) throw new Error(`${method} ${path}: ${r.status} ${text.slice(0, 200)}`);
    return text ? JSON.parse(text) : {};
  };
  await call("POST", "/v1/auth/login", { email: "ada@demo.test", password: "correct horse battery" });
  const actions = (await call("GET", "/v1/storefront/actions")).actions;
  if (actions.find((a) => a.kind === "booking")?.enabled) return;
  const sf = await call("GET", "/v1/storefront");
  const days = Array.from({ length: 7 }, () => [{ open: "09:00", close: "17:00" }]);
  await call("PUT", "/v1/storefront/bookings", { services_table: sf.services_table, services_columns: sf.services_columns, availability: { days } });
  const wf = await call("POST", "/v1/templates/storefront-booking/instantiate", { params: {}, name: "Bookings" });
  await call("POST", `/v1/workflows/${wf.id}/versions/1/publish`);
  await call("PUT", "/v1/storefront/actions/booking", { enabled: true, payment: "none" });
}

async function chatWith(who, name) {
  const page = await person(who);
  await page.goto(BASE + "/chat");
  await later(page);
  await page.getByRole("link", { name: new RegExp(`^${name}`) }).first().click();
  const pane = page.getByTestId("chat-pane");
  await pane.waitFor();
  await page.waitForTimeout(1200);
  return { page, pane };
}

const tools = (page) => page.getByRole("region", { name: "Tools" });
const tile = (page, name) => tools(page).getByRole("button", { name: new RegExp(`^${name}`) });

// 1 · Ada: the tools in her chat with Bayo are about Bayo.
async function panel() {
  const { page, pane } = await chatWith("ada", "Bayo Rice Mills");
  await act(page, "howto17-panel", ADA, async () => {
    await say("Ada's chat with Bayo Rice Mills. Tap the tools button.", 1800);
    await pane.getByRole("button", { name: "Open tools" }).click();
    await tools(page).waitFor();
    await sleep(800);
    await say("The tools are about Bayo: With Bayo Rice Mills.", 3000);
    await say("His shop, your orders and bills with him, the statement between you, an invoice to him.", 4200);
    await tile(page, "Their shop").click();
    const shop = page.getByTestId("order-sheet");
    await shop.waitFor();
    await sleep(1200);
    await say("Their shop: Bayo's rice, at the prices he shares with Ada. Order from here.", 4200);
    await shop.getByRole("button", { name: "Close" }).click();
    await sleep(1000);
  });
  await page.context().close();
}

// 2 · Bayo: Ada shares no price list with him, so he sees her storefront.
async function shop() {
  const { page, pane } = await chatWith("bayo", "Ada's Kitchen");
  await act(page, "howto17-shop", BAYO, async () => {
    await say("From the other side: Bayo in his chat with Ada's Kitchen.", 1800);
    await pane.getByRole("button", { name: "Open tools" }).click();
    await tools(page).waitFor();
    await sleep(800);
    await say("His tools here are about Ada: her shop, and Book, for her services.", 3600);
    await tile(page, "Their shop").click();
    const sheet = page.getByTestId("order-sheet");
    await sheet.waitFor();
    await sleep(1000);
    await say("Ada shares no price list with Bayo, so he sees her storefront: her prices, and how many are left.", 4600);
    await sheet.getByLabel("Search their shop").pressSequentially("rice", { delay: 110 });
    await sleep(2200);
    await sheet.getByRole("button", { name: "Close" }).click();
    await sleep(800);
  });
  await page.context().close();
}

// 3 · Bayo books one of Ada's services.
async function book() {
  const { page, pane } = await chatWith("bayo", "Ada's Kitchen");
  await act(page, "howto17-book", BAYO, async () => {
    await say("Book: Ada's services, as her storefront shows them.", 300);
    await pane.getByRole("button", { name: "Open tools" }).click();
    await tile(page, "Book").click();
    const sheet = page.getByRole("dialog");
    await sheet.getByRole("button", { name: /Office lunch delivery/ }).waitFor();
    await sleep(2400);
    await sheet.getByRole("button", { name: /Office lunch delivery/ }).click();
    await sheet.getByRole("group", { name: "Day" }).waitFor();
    await sleep(800);
    await say("A day, and a time she is free.", 1400);
    await sheet.getByRole("group", { name: "Day" }).getByRole("button").nth(1).click();
    await sleep(1200);
    await sheet.getByRole("group", { name: "Time" }).getByRole("button", { name: "12:00" }).click();
    await sleep(1200);
    await page.locator(".tk-cta").filter({ hasText: "12:00" }).click();
    await sheet.getByLabel("Phone number they can reach you on").waitFor();
    await say("Check it, a phone number for Ada, a note. Then Book.", 1200);
    await sheet.getByLabel("Phone number they can reach you on").pressSequentially("0803 555 0101", { delay: 70 });
    await sheet.getByLabel("Note for them").pressSequentially("Lunch for 20 at our office.", { delay: 55 });
    await sleep(800);
    await page.getByRole("button", { name: "Book", exact: true }).click();
    await pane.getByText(/^Booked: Office lunch delivery/).last().waitFor({ timeout: 15000 });
    await sleep(800);
    await say("Booked. The chat says so, to Bayo and to Ada.", 4200);
  });
  await page.context().close();
}

// 4 · Ada sees it; her own tools are in her Taskiem chat.
async function home() {
  const { page, pane } = await chatWith("ada", "Bayo Rice Mills");
  await act(page, "howto17-home", ADA, async () => {
    await pane.getByText(/^Booked: Office lunch delivery/).last().evaluate((el) => el.scrollIntoView({ block: "center" }));
    await say("Ada sees Bayo's booking in their chat.", 3200);
    await page.goto(BASE + "/chat?with=taskiem");
    await say("Her own tools are in her Taskiem chat.", 0);
    await later(page);
    const box = page.getByTestId("taskiem-pane");
    await box.waitFor();
    await sleep(1400);
    await box.getByRole("button", { name: "Open tools" }).click();
    await tools(page).waitFor();
    await sleep(800);
    await say("Products, Stock, Sell and the rest: here, not in a chat with another business.", 4600);
  });
  await page.context().close();
}

const PARTS = [
  { title: "How to: *the tools in a chat with a business.*" },
  { title: "Tools *about them.*" },
  { act: "howto17-panel" },
  { title: "Their *shop.*" },
  { act: "howto17-shop" },
  { title: "*Book* their services." },
  { act: "howto17-book" },
  { title: "Your own tools: *Taskiem.*" },
  { act: "howto17-home" },
];

// No argument: everything. "build": only join. Act names: record those, then join.
const ACTS = { panel, shop, book, home };
const which = process.argv.slice(2);
if (which.some((n) => n !== "build") || which.length === 0) await bookingsOn();
if (which.length === 0) for (const f of Object.values(ACTS)) await f();
else for (const n of which.filter((n) => n !== "build")) await ACTS[n]();
console.log(build(SLUG, PARTS));
process.exit(0);
