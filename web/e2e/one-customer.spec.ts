import { createHmac } from "node:crypto";
import { createServer as createHttp, type Server as HttpServer } from "node:http";
import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Taskiem for everyone, stage 4, the web (docs/spec/taskiem-for-everyone.md
// §11): a business with its own WhatsApp number, a customer who writes to
// it and then chats in the app from the same phone, one thread with both
// lines on the business's side, the reply's line chosen, the person seeing
// the WhatsApp messages marked, and the chat link offering both. On the
// real server, with WhatsApp's Graph API stood in for on the port
// whatsapp.spec.ts, customer-chat.spec.ts and you.spec.ts use, so this file
// runs one test at a time and apart from them.
test.use({ bypassCSP: true });
test.describe.configure({ mode: "serial" });

const H = { "X-Taskiem-Request": "1" };
const graphPort = 12626;
// The business's own number, apart from whatsapp.spec.ts's.
const shop = { pnid: "222000888999", waba: "333000888999", token: "EAAonecustomer", display: "+234 800 000 0020", wame: "https://wa.me/2348000000020" };
const sent: { to: string; template?: { components?: { parameters?: { text?: string }[] }[] } }[] = [];
const shopSent: { to: string; type: string; text?: { body: string } }[] = [];
let graph: HttpServer;

test.beforeAll(async () => {
  graph = createHttp((req, res) => {
    let body = "";
    req.on("data", (c: Buffer) => (body += c.toString("utf8")));
    req.on("end", () => {
      const url = new URL(req.url ?? "/", "http://graph");
      const out = (v: unknown, status = 200) => {
        res.writeHead(status, { "Content-Type": "application/json" });
        res.end(JSON.stringify(v));
      };
      if (req.headers.authorization === `Bearer ${shop.token}`) {
        if (req.method === "POST" && url.pathname === `/v25.0/${shop.pnid}/messages`) {
          const m = JSON.parse(body) as (typeof shopSent)[number];
          shopSent.push(m);
          return out({ messaging_product: "whatsapp", contacts: [{ input: m.to, wa_id: m.to.replace(/^\+/, "") }], messages: [{ id: `wamid.one${shopSent.length}` }] });
        }
        if (req.method === "GET" && url.pathname === `/v25.0/${shop.pnid}`) return out({ display_phone_number: shop.display, verified_name: "Iya Basira Kitchen", id: shop.pnid });
        if (req.method === "GET" && url.pathname === `/v25.0/${shop.waba}/message_templates`) return out({ data: [], paging: { cursors: { before: "a", after: "b" } } });
        return out({}, 404);
      }
      if (req.method === "POST" && url.pathname.endsWith("/messages")) {
        const m = JSON.parse(body) as (typeof sent)[number];
        sent.push(m);
        return out({ messaging_product: "whatsapp", contacts: [{ input: m.to, wa_id: m.to.replace(/^\+/, "") }], messages: [{ id: `wamid.oc${sent.length}` }] });
      }
      out({});
    });
  });
  await new Promise<void>((resolve) => graph.listen(graphPort, "127.0.0.1", resolve));
});

test.afterAll(async () => {
  await new Promise<void>((resolve) => graph.close(() => resolve()));
});

const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

function aNumber(): { typed: string; e164: string; digits: string } {
  const rest = String(Date.now()).slice(-7) + String(Math.floor(Math.random() * 10));
  const d = `90${rest.slice(0, 8)}`;
  return { typed: `0${d.slice(0, 3)} ${d.slice(3, 6)} ${d.slice(6)}`, e164: `+234${d}`, digits: `234${d}` };
}

async function codeFor(e164: string, after = 0): Promise<string> {
  let code = "";
  await expect
    .poll(() => {
      const m = sent.slice(after).filter((x) => x.to === e164 || x.to === e164.slice(1)).pop();
      code = m?.template?.components?.flatMap((c) => c.parameters ?? []).find((p) => /^\d{6}$/.test(p.text ?? ""))?.text ?? "";
      return code;
    }, { message: `a code to ${e164}` })
    .toMatch(/^\d{6}$/);
  return code;
}

/** A message from the customer to the business's own number, as WhatsApp
 * delivers it. */
let n = 0;
async function deliver(page: Page, from: string, text: string) {
  const body = JSON.stringify({
    object: "whatsapp_business_account",
    entry: [
      {
        id: shop.waba,
        changes: [
          {
            field: "messages",
            value: {
              messaging_product: "whatsapp",
              metadata: { display_phone_number: shop.display.replace(/\D/g, ""), phone_number_id: shop.pnid },
              contacts: [{ profile: { name: "Kemi" }, wa_id: from }],
              messages: [{ from, id: `wamid.kemi${Date.now()}${++n}`, timestamp: String(Math.floor(Date.now() / 1000)), type: "text", text: { body: text } }],
            },
          },
        ],
      },
    ],
  });
  const sig = "sha256=" + createHmac("sha256", "e2e-app-secret").update(body).digest("hex");
  const r = await page.request.post("/channels/whatsapp", { headers: { "Content-Type": "application/json", "X-Hub-Signature-256": sig }, data: body });
  expect(r.status()).toBe(200);
}

/** axe in the light and the dark theme, and nothing wider than the page. */
async function check(page: Page, where: string, width: number) {
  const found: string[] = [];
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    await page.waitForTimeout(150);
    const vs = await axe(page);
    if (vs.length) found.push(describe(`${where} (${scheme})`, vs));
  }
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  expect(found, found.join("\n\n")).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(width);
}

type Chat = { id: string; name: string; line: string; reply_line?: string; lines?: { line: string; stopped: boolean; notice?: string }[] };
const chatsOf = async (page: Page) => ((await (await page.request.get("/v1/customer-chats", { headers: H })).json()) as { chats: Chat[] }).chats;
const settingsOf = async (page: Page) =>
  ((await (await page.request.get("/v1/customers/chat-link", { headers: H })).json()) as { settings: { link_whatsapp: boolean; own_number?: string; takes_chats: boolean } }).settings;

test("one customer on WhatsApp and in the app: one thread with both lines, the reply's line, and the chat link's choice", async ({ browser }) => {
  test.setTimeout(240_000);
  const biz = await (await browser.newContext({ viewport: { width: 1280, height: 900 } })).newPage();
  const name = `Iya Basira ${letters()}`;
  const signup = await biz.request.post("/v1/signup", {
    headers: H,
    data: { tenant: name, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@one.test`, name: "Basira Owner", password: "correct horse battery" },
  });
  expect(signup.status(), await signup.text()).toBe(201);
  const connect = await biz.request.put("/v1/whatsapp/number", { headers: H, data: { phone_number_id: shop.pnid, waba_id: shop.waba, access_token: shop.token, own_app: false } });
  expect(connect.status(), await connect.text()).toBe(200);
  const link = (await (await biz.request.get("/v1/customers/chat-link", { headers: H })).json()) as { code: string };

  // Settings > Customer chats: the switch, on by default, with the number.
  await biz.goto("/settings/customers");
  await expect(biz.getByTestId("link-whatsapp")).toBeChecked();
  await expect(biz.getByTestId("taking-chats")).toContainText(`on your own number ${shop.display}`);

  // The customer writes to the business's own number first.
  const num = aNumber();
  await deliver(biz, num.digits, "Good morning, do you have jollof today?");
  await expect.poll(async () => (await chatsOf(biz)).length).toBe(1);

  // The chat link, signed out on a phone: Chat on Taskiem and Chat on WhatsApp.
  const person = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
  await person.goto(`/m/${link.code}`);
  await expect(person.getByRole("heading", { name })).toBeVisible();
  await expect(person.getByTestId("chat-here")).toHaveText("Chat on Taskiem");
  const wa = person.getByTestId("chat-on-whatsapp");
  await expect(wa).toHaveText("Chat on WhatsApp");
  await expect(wa).toHaveAttribute("href", shop.wame);
  await expect(wa).toHaveAttribute("target", "_blank");
  await expect(wa).toHaveAttribute("rel", /noopener/);
  await check(person, "/m/{code} with both", 390);
  await person.screenshot({ path: "test-results/one-customer-link.png" });

  // Chat on Taskiem, from the same phone: signed in by its code.
  await person.getByTestId("chat-here").click();
  const before = sent.length;
  await person.getByLabel("Your phone number").fill(num.typed);
  await person.getByRole("button", { name: "Continue" }).click();
  await person.getByLabel("6-digit code").fill(await codeFor(num.e164, before));
  await person.getByRole("button", { name: "Continue" }).click();
  await expect(person.getByRole("heading", { name: "Make your account" })).toBeVisible();
  await person.getByLabel("Your name").fill("Kemi Adeyemi");
  await person.getByLabel("I'm 18 or over").check();
  await person.getByTestId("way-in-terms").getByRole("checkbox").check();
  await person.getByRole("button", { name: "Create" }).click();
  await expect(person).toHaveURL(/\/you\/chats\/[0-9a-f-]{36}$/);
  // Their WhatsApp chat with the business is here too.
  await expect(person.getByTestId("person-whatsapp-here")).toHaveText("Your WhatsApp chat with them is here too");
  const offers = person.getByTestId("offers-question");
  await offers.getByRole("button", { name: "No", exact: true }).click();
  await person.getByLabel(`Message ${name}`).fill("Is it ready by noon?");
  await person.getByLabel(`Message ${name}`).press("Enter");
  await expect(person.getByTestId("person-message").filter({ hasText: "Is it ready by noon?" })).toBeVisible();

  // The business: one row for the customer, with both lines.
  await expect.poll(async () => (await chatsOf(biz)).map((c) => c.lines?.length)).toEqual([2]);
  const [thread] = await chatsOf(biz);
  expect(thread?.reply_line).toBe("app");
  await biz.goto("/chat");
  const row = biz.getByTestId("customer-chats").getByTestId("customer-chat");
  await expect(row).toHaveCount(1);
  await expect(row.getByTestId("both-lines")).toHaveText("· WhatsApp · App");
  await row.click();
  const pane = biz.getByTestId("customer-pane");
  // Typing a moment ago; then on both lines.
  await expect(pane.getByText("On WhatsApp and in the app")).toBeVisible({ timeout: 20_000 });
  // Both lines' messages, each marked where it came.
  const fromWa = pane.getByTestId("customer-message").filter({ hasText: "do you have jollof" });
  const fromApp = pane.getByTestId("customer-message").filter({ hasText: "Is it ready by noon?" });
  await expect(fromWa.getByTestId("message-line")).toHaveText("on WhatsApp");
  await expect(fromApp.getByTestId("message-line")).toHaveText("in the app");
  // The reply goes where they last wrote: in the app.
  const chooser = pane.getByTestId("line-chooser");
  await expect(chooser.getByRole("radio", { name: "in the app" })).toBeChecked();
  await expect(pane.getByRole("button", { name: /Send a card in the app/ })).toBeVisible();
  await pane.getByLabel(/^Reply to .* in the app$/).fill("Yes, by 11");
  await pane.getByLabel(/^Reply to .* in the app$/).press("Enter");
  await expect(pane.getByTestId("customer-message").filter({ hasText: "Yes, by 11" }).getByTestId("message-line")).toHaveText("in the app");

  // Switched to WhatsApp: it goes from the business's own number.
  await chooser.getByRole("radio", { name: "on WhatsApp" }).check();
  await expect(chooser).toContainText("Cards, offers and files go in the app.");
  const wasSent = shopSent.length;
  await pane.getByLabel(/^Reply to .* on WhatsApp$/).fill("I will send the menu on WhatsApp");
  await pane.getByLabel(/^Reply to .* on WhatsApp$/).press("Enter");
  const onWa = pane.getByTestId("customer-message").filter({ hasText: "I will send the menu" });
  await expect(onWa.getByTestId("message-line")).toHaveText("on WhatsApp");
  await expect.poll(() => shopSent.slice(wasSent).map((m) => m.text?.body)).toContain("I will send the menu on WhatsApp");
  expect(shopSent.slice(wasSent).every((m) => m.to.replace(/^\+/, "") === num.digits)).toBe(true);
  await check(biz, "a thread with both lines", 1280);

  // The person sees it, marked on WhatsApp; their app messages are not.
  const theirs = person.getByTestId("person-message").filter({ hasText: "I will send the menu" });
  await expect(theirs.getByTestId("person-on-whatsapp")).toHaveText("on WhatsApp", { timeout: 15_000 });
  await expect(person.getByTestId("person-message").filter({ hasText: "Yes, by 11" }).getByTestId("person-on-whatsapp")).toHaveCount(0);
  // What they write on WhatsApp now shows in the app too, as theirs.
  await deliver(biz, num.digits, "Thank you, I'll come at noon");
  await expect(person.getByTestId("person-message").filter({ hasText: "I'll come at noon" }).getByTestId("person-on-whatsapp")).toBeVisible({ timeout: 15_000 });
  await check(person, "a person's chat with WhatsApp messages", 390);
  await person.screenshot({ path: "test-results/one-customer-person.png" });
  await person.setViewportSize({ width: 360, height: 780 });
  await check(person, "a person's chat with WhatsApp messages", 360);
  // The reply now goes where they last wrote: WhatsApp.
  await expect.poll(async () => (await chatsOf(biz))[0]?.reply_line).toBe("whatsapp");

  // On a phone: the thread, the chooser and its marks fit, in both themes.
  for (const width of [390, 360]) {
    await biz.setViewportSize({ width, height: 844 });
    await biz.goto(`/chat?with=c-${thread?.id}`);
    await expect(pane.getByTestId("line-chooser").getByRole("radio", { name: "on WhatsApp" })).toBeChecked();
    await expect(pane.getByTestId("customer-message").filter({ hasText: "I'll come at noon" })).toBeVisible();
    await check(biz, `a thread with both lines at ${width}`, width);
    await biz.screenshot({ path: `test-results/one-customer-thread-${width}.png` });
  }

  // STOP on WhatsApp: that line is off, with why; the reply goes in the app.
  await deliver(biz, num.digits, "STOP");
  await expect.poll(async () => (await chatsOf(biz))[0]?.lines?.find((l) => l.line === "whatsapp")?.stopped).toBe(true);
  await biz.reload();
  await expect(chooser.getByRole("radio", { name: "on WhatsApp" })).toBeDisabled();
  await expect(chooser.getByRole("radio", { name: "in the app" })).toBeChecked();
  await expect(chooser.getByTestId("line-why")).toHaveText("Not on WhatsApp: They sent STOP on WhatsApp.");
  await check(biz, "a thread whose WhatsApp line sent STOP", 360);

  // Stood in: WhatsApp past its 24 hours and the app blocked; the reply goes
  // on WhatsApp as a message they answer, as before.
  await biz.route(`**/v1/customer-chats/${thread?.id}`, async (r: Route) => {
    if (r.request().method() !== "GET") return r.fallback();
    const real = (await (await r.fetch()).json()) as Chat & Record<string, unknown>;
    const lines = [
      { line: "app", id: thread?.id, phone: num.e164, window_open: false, window_until: null, stopped: false, blocked: true, notice: "You blocked this customer in the app", last_in_at: null },
      { line: "whatsapp", id: "00000000-0000-0000-0000-000000000001", phone: num.e164, window_open: false, window_until: null, stopped: false, blocked: false, last_in_at: null },
    ];
    await r.fulfill({ json: { ...real, lines, reply_line: "whatsapp", window_open: false, stopped: false, blocked: false, notice: undefined } });
  });
  await biz.reload();
  await expect(chooser.getByRole("radio", { name: "in the app" })).toBeDisabled();
  await expect(chooser.getByTestId("line-why")).toHaveText("Not in the app: You blocked them in the app.");
  await expect(pane.getByTestId("customer-note")).toHaveText("More than a day since they last wrote: your reply goes as a WhatsApp message they answer to carry on.");
  await expect(pane.getByTestId("customer-line-states")).toHaveCount(0);
  await expect(pane.getByRole("button", { name: /Send a card/ })).toHaveCount(0);
  await check(biz, "a thread past WhatsApp's 24 hours, blocked in the app", 360);
  await biz.screenshot({ path: "test-results/one-customer-stood-in.png" });
  await biz.unroute(`**/v1/customer-chats/${thread?.id}`);

  // Settings: WhatsApp off on the link, then chats in Taskiem off.
  await biz.setViewportSize({ width: 390, height: 844 });
  await biz.goto("/settings/customers");
  await check(biz, "/settings/customers with its own number", 390);
  await biz.getByTestId("link-whatsapp").uncheck();
  await expect.poll(async () => (await settingsOf(biz)).link_whatsapp).toBe(false);
  const anon = await (await browser.newContext({ viewport: { width: 360, height: 780 } })).newPage();
  await anon.goto(`/m/${link.code}`);
  await expect(anon.getByTestId("chat-here")).toHaveText("Chat here in your browser");
  await expect(anon.getByTestId("chat-on-whatsapp")).toHaveCount(0);
  await biz.getByTestId("link-whatsapp").check();
  await expect.poll(async () => (await settingsOf(biz)).link_whatsapp).toBe(true);
  await biz.getByTestId("takes-chats").uncheck();
  await expect.poll(async () => (await settingsOf(biz)).takes_chats).toBe(false);
  // A new browser: the card is kept a minute by the last one.
  const later = await (await browser.newContext({ viewport: { width: 360, height: 780 } })).newPage();
  await later.goto(`/m/${link.code}`);
  await expect(later.getByTestId("chat-on-whatsapp")).toHaveText("Chat on WhatsApp");
  await expect(later.getByTestId("chat-here")).toHaveCount(0);
  await expect(later.getByTestId("not-taking-chats")).toHaveCount(0);
  await check(later, "/m/{code} with WhatsApp alone", 360);
  await later.screenshot({ path: "test-results/one-customer-link-whatsapp.png" });
  await biz.getByTestId("takes-chats").check();

  const off = await biz.request.delete("/v1/whatsapp/number", { headers: H });
  expect([200, 204]).toContain(off.status());
});
