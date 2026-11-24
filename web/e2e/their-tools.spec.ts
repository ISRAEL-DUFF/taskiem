import { createServer as createHttp, type Server as HttpServer } from "node:http";
import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Customers' tools on the web (made tools T5, docs/tools.md "Customers'
// tools"), on the real server: a business publishes two tools for its
// customers (Order status, whose answer is a card of public outputs with a
// button to Track an order) and one for its members; a person in their
// chat with it opens the tools button, sees Their tools (never the
// members' one), fills a form, says Yes and gets the answer, with nothing
// the business did not make public; its button opens the other tool
// filled in. axe in both themes, nothing wider than a phone. The person's
// code goes out on Taskiem's WhatsApp number to a fake Graph API on the
// port customer-chat.spec.ts uses, so this file runs one test at a time
// and apart from them.
test.use({ bypassCSP: true });
test.describe.configure({ mode: "serial" });

const H = { "X-Taskiem-Request": "1" };
const graphPort = Number(process.env.E2E_GRAPH_PORT ?? 12626);
const sent: { to: string; template?: { components?: { parameters?: { text?: string }[] }[] } }[] = [];
let graph: HttpServer;

test.beforeAll(async () => {
  graph = createHttp((req, res) => {
    let body = "";
    req.on("data", (c: Buffer) => (body += c.toString("utf8")));
    req.on("end", () => {
      res.writeHead(200, { "Content-Type": "application/json" });
      if (req.method === "POST" && req.url?.endsWith("/messages")) {
        const m = JSON.parse(body) as (typeof sent)[number];
        sent.push(m);
        res.end(JSON.stringify({ messaging_product: "whatsapp", contacts: [{ input: m.to, wa_id: m.to.replace(/^\+/, "") }], messages: [{ id: `wamid.tt${sent.length}` }] }));
        return;
      }
      res.end("{}");
    });
  });
  await new Promise<void>((resolve) => graph.listen(graphPort, "127.0.0.1", resolve));
});

test.afterAll(async () => {
  await new Promise<void>((resolve) => graph.close(() => resolve()));
});

const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function must<T>(r: Promise<{ status(): number; text(): Promise<string>; json(): Promise<unknown> }>, status: number): Promise<T> {
  const res = await r;
  expect(res.status(), await res.text()).toBe(status);
  return (await res.json()) as T;
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

const status = {
  schema: "wd/v1",
  id: "wf_status",
  version: 1,
  name: "Order status",
  trigger: { type: "manual" },
  inputs: { schema: { type: "object", required: ["ref"], properties: { ref: { type: "string", title: "Order number", maxLength: 20 } } } },
  outputs: [
    { name: "state", type: "string", title: "State", description: "Where it is", value: "=steps.look.output.state", public: true },
    { name: "ref", type: "string", title: "Ref", description: "The order", value: "=steps.look.output.ref", public: true },
    { name: "cost", type: "string", title: "Cost", description: "What it cost us", value: "=steps.look.output.cost" },
  ],
  tool: {
    name: "Order status",
    command: "order-status",
    about: "Where your order is",
    for: "customers",
    answer: {
      type: "card",
      title: "=outputs.state",
      ref: "=outputs.ref",
      lines: [{ label: "Order", value: "=outputs.ref" }],
      buttons: [{ label: "Track it", tool: "track", inputs: { ref: "=outputs.ref" } }],
    },
  },
  steps: [{ id: "look", type: "transform", config: { output: { ref: "=trigger.body.ref", state: "On its way", cost: "SECRET-COST-991" } } }],
};

const track = {
  schema: "wd/v1",
  id: "wf_track",
  version: 1,
  name: "Track an order",
  trigger: { type: "manual" },
  inputs: { schema: { type: "object", required: ["ref"], properties: { ref: { type: "string", title: "Order number", maxLength: 20 } } } },
  outputs: [{ name: "ref", type: "string", title: "Ref", description: "The order", value: "=steps.look.output.ref", public: true }],
  tool: { name: "Track an order", command: "track", about: "Where the rider is", for: "customers", answer: { type: "text", text: "='Tracking ' + outputs.ref" } },
  steps: [{ id: "look", type: "transform", config: { output: { ref: "=trigger.body.ref" } } }],
};

const staff = {
  schema: "wd/v1",
  id: "wf_staff",
  version: 1,
  name: "Give money back",
  trigger: { type: "manual" },
  inputs: { schema: { type: "object", properties: {} } },
  tool: { name: "Give money back", command: "give-back", about: "For staff only" },
  steps: [{ id: "look", type: "transform", config: { output: { ok: true } } }],
};

test("a customer uses a business's tools in their chat: a form, Yes, the answer from what it made public, a button to another tool", async ({ browser }) => {
  test.setTimeout(180_000);
  const biz = await (await browser.newContext()).newPage();
  const name = `Ada Stores ${letters()}`;
  await must(
    biz.request.post("/v1/signup", { headers: H, data: { tenant: name, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@tt.test`, name: "Ada Owner", password: "correct horse battery" } }),
    201,
  );
  for (const def of [status, track, staff]) {
    const wf = await must<{ id: string }>(biz.request.post("/v1/workflows", { headers: H, data: { name: def.name, definition: def } }), 201);
    await must(biz.request.post(`/v1/workflows/${wf.id}/versions/1/publish`, { headers: H }), 200);
  }
  const link = await must<{ code: string }>(biz.request.get("/v1/customers/chat-link", { headers: H }), 200);

  // The person, signed in by phone, chatting with the business.
  const page = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
  const rest = String(Date.now()).slice(-7) + String(Math.floor(Math.random() * 10));
  const e164 = `+23492${rest.slice(0, 8)}`;
  const before = sent.length;
  await must(page.request.post("/v1/auth/phone/start", { headers: H, data: { phone: e164 } }), 202);
  let code = "";
  await expect
    .poll(() => {
      const m = sent.slice(before).filter((x) => x.to === e164 || x.to === e164.slice(1)).pop();
      code = m?.template?.components?.flatMap((c) => c.parameters ?? []).find((p) => /^\d{6}$/.test(p.text ?? ""))?.text ?? "";
      return code;
    })
    .toMatch(/^\d{6}$/);
  const v = await must<{ ticket: string }>(page.request.post("/v1/auth/phone/verify", { headers: H, data: { phone: e164, code } }), 200);
  await must(page.request.post("/v1/auth/phone/create", { headers: H, data: { ticket: v.ticket, name: "Tolu Bello", adult: true, terms: true } }), 201);
  const chat = await must<{ id: string }>(page.request.post("/v1/me/chats", { headers: H, data: { code: link.code } }), 201);
  await page.goto(`/you/chats/${chat.id}`);
  await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();

  // The tools button: their shop and the rest, then their tools for customers.
  await page.getByTestId("person-tools-button").click();
  const theirs = page.getByTestId("their-tools");
  await expect(theirs.getByTestId("their-tool-order-status")).toContainText("Where your order is");
  await expect(theirs.getByTestId("their-tool-track")).toBeVisible();
  await expect(page.getByTestId("their-tool-give-back")).toHaveCount(0);
  await check(page, "their tools", 390);
  await page.screenshot({ path: "test-results/their-tools.png" });

  // A form; Next shows what will run; nothing starts before Yes.
  await theirs.getByTestId("their-tool-order-status").click();
  const sheet = page.getByTestId("their-tool-sheet");
  // The tools menu closing as the tool opens does not hand the focus back
  // to the message box behind the sheet: what is typed goes in the form.
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
  await expect(sheet).toBeFocused();
  await sheet.getByLabel("Order number").fill("A-100");
  await check(page, "their tool's form", 390);
  await sheet.getByRole("button", { name: "Next" }).click();
  await expect(sheet.getByRole("definition")).toHaveText("A-100");
  await expect(sheet.getByRole("button", { name: "Yes, go ahead" })).toBeVisible();
  await check(page, "their tool's yes", 390);
  await sheet.getByRole("button", { name: "Yes, go ahead" }).click();

  // The answer: the card, from what the business made public only.
  const card = sheet.getByTestId("tool-answer");
  await expect(card).toHaveAttribute("data-status", "completed", { timeout: 30_000 });
  await expect(card).toContainText("On its way");
  await expect(card).toContainText("A-100");
  await expect(sheet).not.toContainText("SECRET");
  await expect(sheet).toContainText("Your reference is");
  await check(page, "their tool's answer", 390);
  await page.screenshot({ path: "test-results/their-tool-answer.png" });

  // Its button opens Track an order, filled in.
  await card.getByRole("button", { name: "Track it" }).click();
  await expect(sheet.getByRole("heading", { name: "Track an order" })).toBeVisible();
  await expect(sheet.getByLabel("Order number")).toHaveValue("A-100");
  await sheet.getByRole("button", { name: "Next" }).click();
  await sheet.getByRole("button", { name: "Yes, go ahead" }).click();
  await expect(sheet.getByTestId("tool-answer")).toContainText("Tracking A-100", { timeout: 30_000 });
});
