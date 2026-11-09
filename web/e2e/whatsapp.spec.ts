import { createHmac } from "node:crypto";
import { confirmEmail } from "./confirm";
import { createServer, type Server } from "node:http";
import { expect, test, type Page } from "@playwright/test";
import { axe, describe as axeWords } from "./a11y";

// Linking a WhatsApp number under Account: the code goes out through a
// fake Graph API on the port serve.sh points the platform number at. The
// same fake answers for an organisation's own number (Settings > WhatsApp
// number) and its WhatsApp Business Account's templates.

const graphPort = 12626;
const sent: {
  to: string;
  type: string;
  template?: {
    name: string;
    components: { type: string; parameters: { text?: string }[] }[];
  };
}[] = [];
let graph: Server;

// The own number whatsapp-templates connects, and its account's templates
// as the fake Business Management API lists them.
const own = {
  pnid: "222000111222",
  waba: "333000111333",
  token: "EAAown",
  secret: "e2e-own-app-secret-16",
  verify: "e2e-own-verify-token",
};
const templates = new Map<
  string,
  { id: string; status: string; reason?: string }
>();
// A business's own number, on the shared app, for customers' conversations,
// and what went out from it.
const shop = { pnid: "222000333444", waba: "333000555666", token: "EAAshop" };
const shopSent: {
  to: string;
  type: string;
  text?: { body: string };
  template?: { name: string };
}[] = [];

test.beforeAll(async () => {
  graph = createServer((req, res) => {
    let body = "";
    req.on("data", (c: Buffer) => (body += c.toString("utf8")));
    req.on("end", () => {
      const url = new URL(req.url ?? "/", "http://graph");
      const json = (status: number, v: unknown) => {
        res.writeHead(status, { "Content-Type": "application/json" });
        res.end(JSON.stringify(v));
      };
      if (
        req.method === "POST" &&
        url.pathname === "/v25.0/106540352242922/messages" &&
        req.headers.authorization === "Bearer EAAe2e"
      ) {
        const m = JSON.parse(body) as (typeof sent)[number];
        sent.push(m);
        json(200, {
          messaging_product: "whatsapp",
          contacts: [{ input: m.to, wa_id: m.to.slice(1) }],
          messages: [{ id: `wamid.e2e${sent.length}` }],
        });
        return;
      }
      // A second own number: customers' conversations (customer below).
      if (req.headers.authorization === `Bearer ${shop.token}`) {
        if (
          req.method === "POST" &&
          url.pathname === `/v25.0/${shop.pnid}/messages`
        ) {
          const m = JSON.parse(body) as (typeof shopSent)[number];
          shopSent.push(m);
          json(200, {
            messaging_product: "whatsapp",
            contacts: [{ input: m.to, wa_id: m.to.replace(/^\+/, "") }],
            messages: [{ id: `wamid.shop${shopSent.length}` }],
          });
          return;
        }
        if (req.method === "GET" && url.pathname === `/v25.0/${shop.pnid}`) {
          json(200, {
            display_phone_number: "+234 800 000 0010",
            verified_name: "Mama Put Kitchen",
            id: shop.pnid,
          });
          return;
        }
        if (
          req.method === "GET" &&
          url.pathname === `/v25.0/${shop.waba}/message_templates`
        ) {
          json(200, {
            data: [],
            paging: { cursors: { before: "a", after: "b" } },
          });
          return;
        }
        res.writeHead(404).end();
        return;
      }
      if (req.headers.authorization !== `Bearer ${own.token}`) {
        res.writeHead(404).end();
        return;
      }
      if (req.method === "GET" && url.pathname === `/v25.0/${own.pnid}`) {
        json(200, {
          display_phone_number: "+234 800 000 0009",
          verified_name: "E2E Stores",
          id: own.pnid,
        });
        return;
      }
      if (url.pathname === `/v25.0/${own.waba}/message_templates`) {
        if (req.method === "GET") {
          json(200, {
            data: [...templates].map(([name, t]) => ({
              id: t.id,
              name,
              language: "en",
              status: t.status,
              category: "UTILITY",
              rejected_reason: t.reason ?? "NONE",
            })),
            paging: { cursors: { before: "a", after: "b" } },
          });
          return;
        }
        const def = JSON.parse(body) as { name: string };
        if (templates.has(def.name)) {
          json(400, {
            error: { message: "A template with that name exists", code: 100 },
          });
          return;
        }
        const t = {
          id: `9${templates.size + 1}00000000000001`,
          status: "PENDING",
        };
        templates.set(def.name, t);
        json(200, { id: t.id, status: t.status, category: "UTILITY" });
        return;
      }
      res.writeHead(404).end();
    });
  });
  await new Promise<void>((resolve) =>
    graph.listen(graphPort, "127.0.0.1", resolve),
  );
});

test.afterAll(
  () => new Promise<void>((resolve) => graph.close(() => resolve())),
);

test("link a WhatsApp number with a code, then unlink it", async ({ page }) => {
  const email = `wa-${Date.now()}@e2e.test`;
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", {
      level: 1,
      name: /^Good (morning|afternoon|evening)/,
    }),
  ).toBeVisible();
  const added = await page.request.post("/v1/members", {
    headers: { "X-Taskiem-Request": "1" },
    data: { email, password: "correct horse battery", roles: ["viewer"] },
  });
  expect(added.status()).toBe(201);
  confirmEmail(email);
  await page.request.post("/v1/auth/logout", {
    headers: { "X-Taskiem-Request": "1" },
  });
  await page.context().clearCookies();

  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", {
      level: 1,
      name: /^Good (morning|afternoon|evening)/,
    }),
  ).toBeVisible();

  await page.goto("/account");
  const wa = page.getByTestId("whatsapp");
  await expect(wa.getByText("+1 555 000 1111")).toBeVisible();
  const number = `+23480${String(Date.now()).slice(-8)}`;
  await wa.getByLabel("Your WhatsApp number").fill(number);
  await wa
    .getByLabel("Your password, to link a number")
    .fill("correct horse battery");
  await wa.getByRole("button", { name: "Send a code" }).click();

  await expect.poll(() => sent.filter((m) => m.to === number).length).toBe(1);
  const otp = sent.find((m) => m.to === number);
  expect(otp?.type).toBe("template");
  expect(otp?.template?.name).toBe("taskiem_otp");
  const code = otp?.template?.components[0]?.parameters[0]?.text ?? "";
  expect(code).toMatch(/^\d{6}$/);

  // A wrong code first.
  await wa
    .getByLabel(`Code sent to ${number}`)
    .fill(code === "000000" ? "111111" : "000000");
  await wa.getByRole("button", { name: "Link", exact: true }).click();
  await expect(wa.getByText("that code is not right")).toBeVisible();
  await wa.getByLabel(`Code sent to ${number}`).fill(code);
  await wa.getByRole("button", { name: "Link", exact: true }).click();
  await expect(wa.getByTestId("whatsapp-number")).toHaveText(number);

  page.once("dialog", (d) => void d.accept());
  await wa.getByRole("button", { name: "Unlink" }).click();
  await expect(wa.getByTestId("whatsapp-number")).toHaveCount(0);
  await expect(wa.getByLabel("Your WhatsApp number")).toBeVisible();
});

// An organisation's own number and its templates (docs/whatsapp.md#the-template-registry):
// each library template's status in the number's WhatsApp Business
// Account, Submit, Meta's answer on the status webhook, and Check now.
test("the own number's templates: submit, hear from Meta, check now", async ({
  page,
}) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", {
      level: 1,
      name: /^Good (morning|afternoon|evening)/,
    }),
  ).toBeVisible();

  await page.goto("/settings");
  const section = page.getByTestId("whatsapp-own-number");
  await section.getByLabel("Phone number id").fill(own.pnid);
  await section.getByLabel("WhatsApp Business Account id").fill(own.waba);
  await section.getByLabel("System user access token").fill(own.token);
  await section.getByLabel("App secret").fill(own.secret);
  await section.getByLabel("Verify token").fill(own.verify);
  await section.getByRole("button", { name: "Connect" }).click();
  await expect(section.getByText("Connected:")).toBeVisible();

  // Nothing submitted yet: every template waits, and the page says what
  // cannot go out until then.
  const tpls = section.getByTestId("whatsapp-templates");
  await expect(tpls.getByTestId("whatsapp-templates-summary")).toContainText(
    "0 of 23 templates approved",
  );
  await expect(tpls.getByTestId("whatsapp-templates-summary")).toContainText(
    "taskiem_invoice",
  );
  const invoice = tpls.getByTestId("whatsapp-template-taskiem_invoice");
  await expect(invoice.getByTestId("status")).toHaveText("Not submitted");

  // Submit: the fake holds it pending.
  await invoice.getByRole("button", { name: "Submit" }).click();
  await expect(invoice.getByTestId("status")).toHaveText("Waiting for Meta");
  expect(templates.get("taskiem_invoice")?.status).toBe("PENDING");
  await expect(invoice.getByRole("button", { name: "Submit" })).toHaveCount(0);

  // Meta's answer arrives on the status webhook, signed with the app's secret.
  const approved = JSON.stringify({
    object: "whatsapp_business_account",
    entry: [
      {
        id: own.waba,
        time: 1759800000,
        changes: [
          {
            field: "message_template_status_update",
            value: {
              event: "APPROVED",
              message_template_id: templates.get("taskiem_invoice")?.id,
              message_template_name: "taskiem_invoice",
              message_template_language: "en",
              reason: "NONE",
            },
          },
        ],
      },
    ],
  });
  const sig =
    "sha256=" + createHmac("sha256", own.secret).update(approved).digest("hex");
  const hook = await page.request.post(`/channels/whatsapp/n/${own.pnid}`, {
    headers: { "Content-Type": "application/json", "X-Hub-Signature-256": sig },
    data: approved,
  });
  expect(hook.status()).toBe(200);
  await page.reload();
  await expect(
    section
      .getByTestId("whatsapp-template-taskiem_invoice")
      .getByTestId("status"),
  ).toHaveText("Approved");
  await expect(section.getByTestId("whatsapp-templates-summary")).toContainText(
    "1 of 23 templates approved",
  );

  // Check now asks Meta: a rejection comes with its reason.
  templates.set("taskiem_receipt", {
    id: "930000000000001",
    status: "REJECTED",
    reason: "INVALID_FORMAT",
  });
  await section.getByRole("button", { name: "Check now" }).click();
  const receipt = section.getByTestId("whatsapp-template-taskiem_receipt");
  await expect(receipt.getByTestId("status")).toHaveText("Rejected");
  await expect(receipt).toContainText("INVALID_FORMAT");

  page.once("dialog", (d) => void d.accept());
  await section.getByRole("button", { name: "Disconnect" }).click();
  await expect(section.getByTestId("whatsapp-templates")).toHaveCount(0);
});

// Customers' conversations (docs/spec/chat-five-more-flows.md, phase 1): a
// customer writing to a business's own number is a thread in the Chat page,
// and a reply from there goes out from that number. Checked at 390 and 360
// px, in both themes with axe.
test.describe("customers' conversations", () => {
  test.use({ bypassCSP: true });
  const H = { "X-Taskiem-Request": "1" };
  const customer = "2348031112222";
  const deliver = async (
    page: Page,
    text: string,
    n: number,
    from = customer,
    name = "Chidi",
  ) => {
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
                metadata: {
                  display_phone_number: "2348000000010",
                  phone_number_id: shop.pnid,
                },
                contacts: [{ profile: { name }, wa_id: from }],
                messages: [
                  {
                    from,
                    id: `wamid.cust${Date.now()}${n}`,
                    timestamp: String(Math.floor(Date.now() / 1000)),
                    type: "text",
                    text: { body: text },
                  },
                ],
              },
            },
          ],
        },
      ],
    });
    const sig =
      "sha256=" +
      createHmac("sha256", "e2e-app-secret").update(body).digest("hex");
    const r = await page.request.post("/channels/whatsapp", {
      headers: {
        "Content-Type": "application/json",
        "X-Hub-Signature-256": sig,
      },
      data: body,
    });
    expect(r.status()).toBe(200);
  };
  // WhatsApp's word on a reply: delivered, read, failed.
  const status = async (page: Page, id: string, st: string) => {
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
                metadata: {
                  display_phone_number: "2348000000010",
                  phone_number_id: shop.pnid,
                },
                statuses: [
                  {
                    id,
                    status: st,
                    timestamp: String(Math.floor(Date.now() / 1000)),
                    recipient_id: customer,
                  },
                ],
              },
            },
          ],
        },
      ],
    });
    const sig =
      "sha256=" +
      createHmac("sha256", "e2e-app-secret").update(body).digest("hex");
    const r = await page.request.post("/channels/whatsapp", {
      headers: {
        "Content-Type": "application/json",
        "X-Hub-Signature-256": sig,
      },
      data: body,
    });
    expect(r.status()).toBe(200);
  };
  const bothThemes = async (page: Page, where: string) => {
    for (const scheme of ["light", "dark"] as const) {
      await page.evaluate((t) => {
        if (t === "dark") document.documentElement.dataset.theme = "dark";
        else delete document.documentElement.dataset.theme;
      }, scheme);
      await page.waitForTimeout(250);
      const vs = await axe(page);
      expect(vs, axeWords(`${where} (${scheme})`, vs)).toEqual([]);
    }
    await page.evaluate(() => delete document.documentElement.dataset.theme);
  };
  const fits = async (page: Page) =>
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);

  test("a customer's message is a thread, and a reply goes from the business's number", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const tag = String(Date.now()).replace(/\d/g, (d) =>
      "abcdefghij".charAt(Number(d)),
    );
    const signup = await page.request.post("/v1/signup", {
      headers: H,
      data: {
        tenant: `Mama Put Kitchen ${tag}`,
        email: `${tag}@mamaput.test`,
        name: "Ada Okafor",
        password: "correct horse battery",
      },
    });
    expect(signup.status(), await signup.text()).toBe(201);
    await page.request.put("/v1/settings/areas", {
      headers: H,
      data: { areas: { storefront: true, orders: true } },
    });
    const connect = await page.request.put("/v1/whatsapp/number", {
      headers: H,
      data: {
        phone_number_id: shop.pnid,
        waba_id: shop.waba,
        access_token: shop.token,
        own_app: false,
      },
    });
    expect(connect.status(), await connect.text()).toBe(200);

    await deliver(
      page,
      "Good afternoon. Do you deliver to Yaba? I want 2 plates of jollof",
      1,
    );
    await page.goto("/chat");
    const rows = page.getByTestId("customer-chats");
    const row = rows.getByTestId("customer-chat").filter({ hasText: "Chidi" });
    await expect(row).toContainText("Do you deliver to Yaba?");
    await fits(page);
    await bothThemes(page, "the chat list with a customer");

    await row.click();
    const pane = page.getByTestId("customer-pane");
    await expect(pane.getByTestId("customer-message")).toContainText([
      "Do you deliver to Yaba?",
    ]);
    await expect(pane.getByTestId("customer-note")).toHaveCount(0);
    await pane
      .getByLabel("Reply to Chidi")
      .fill("Yes, Yaba is ₦1,500. Two plates come to ₦9,000.");
    await pane.getByRole("button", { name: "Send" }).click();
    await expect(pane.getByTestId("customer-message").last()).toContainText(
      "Yes, Yaba is ₦1,500.",
    );
    await expect(pane.getByRole("img", { name: "Sending" })).toHaveCount(0);
    await expect
      .poll(
        () =>
          shopSent.filter((m) => m.to.replace(/^\+/, "") === customer).length,
      )
      .toBe(1);
    const out = shopSent[shopSent.length - 1];
    expect(out?.type).toBe("text");
    expect(out?.text?.body).toBe(
      "Yes, Yaba is ₦1,500. Two plates come to ₦9,000.",
    );
    // One tick once WhatsApp has it, two when it reaches their phone.
    await expect(pane.getByRole("img", { name: "Sent" })).toBeVisible();
    await status(page, `wamid.shop${shopSent.length}`, "delivered");
    await expect(pane.getByRole("img", { name: "Delivered" })).toBeVisible({
      timeout: 10_000,
    });
    await fits(page);
    await bothThemes(page, "a customer's thread");

    // Their answer arrives in the open thread.
    await deliver(page, "Perfect, send me the account number", 2);
    await expect(pane.getByTestId("customer-message").last()).toContainText(
      "send me the account number",
      { timeout: 10_000 },
    );

    // Archived, the thread leaves the list and waits under Archived;
    // unarchived, it comes back.
    await pane
      .getByRole("button", { name: "More about this conversation" })
      .click();
    await page.getByRole("menuitem", { name: "Archive" }).click();
    await page.goto("/chat");
    await expect(row).toHaveCount(0);
    await page.getByRole("button", { name: "Archived (1)" }).click();
    await expect(row).toBeVisible();
    await row.click();
    await pane
      .getByRole("button", { name: "More about this conversation" })
      .click();
    await page.getByRole("menuitem", { name: "Unarchive" }).click();
    await page.goto("/chat");
    await expect(row).toBeVisible();
    await row.click();

    // At 360 px too.
    await page.setViewportSize({ width: 360, height: 640 });
    await fits(page);
    await bothThemes(page, "a customer's thread at 360");

    // STOP: the thread is emptied, and there is no box to reply from.
    await deliver(page, "STOP", 3);
    await expect(pane.getByTestId("customer-note")).toContainText(
      "they sent STOP",
      { timeout: 10_000 },
    );
    await expect(pane.getByTestId("customer-message")).toHaveCount(0);
    await expect(pane.getByLabel("Reply to Chidi")).toHaveCount(0);
    await bothThemes(page, "a customer who sent STOP");

    // The number goes back for the other tests.
    const off = await page.request.delete("/v1/whatsapp/number", {
      headers: H,
    });
    expect([200, 204]).toContain(off.status());
  });
  // Orders from customers' messages (phase 2): the card in a customer's
  // thread, at 390 and 360 px in both themes. These browser tests run with
  // no catalogue table and no model, so the order endpoints answer from a
  // stand-in here; reading an order, confirming it into an order and sending
  // it to the customer are checked in Go (api/order_drafts_test.go).
  test("an order on a customer's card is put together and confirmed", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const tag = String(Date.now()).replace(/\d/g, (d) =>
      "abcdefghij".charAt(Number(d)),
    );
    const signup = await page.request.post("/v1/signup", {
      headers: H,
      data: {
        tenant: `Bola Foods ${tag}`,
        email: `${tag}@bolafoods.test`,
        name: "Bola Ade",
        password: "correct horse battery",
      },
    });
    expect(signup.status(), await signup.text()).toBe(201);
    await page.request.put("/v1/settings/areas", {
      headers: H,
      data: { areas: { storefront: true, orders: true } },
    });
    const connect = await page.request.put("/v1/whatsapp/number", {
      headers: H,
      data: {
        phone_number_id: shop.pnid,
        waba_id: shop.waba,
        access_token: shop.token,
        own_app: false,
      },
    });
    expect(connect.status(), await connect.text()).toBe(200);

    const buyer = "2348031113333";
    await deliver(page, "Good evening, I want to order", 1, buyer, "Bola");
    await page.goto("/chat");
    await page
      .getByTestId("customer-chats")
      .getByTestId("customer-chat")
      .filter({ hasText: "Bola" })
      .click();
    const pane = page.getByTestId("customer-pane");
    const card = pane.getByTestId("customer-order");
    // First on the real server, as far as it goes without a catalogue:
    // start an order, say where it goes and the fee, see why it waits,
    // and drop it.
    await pane.getByRole("button", { name: "Start an order" }).click();
    await expect(card).toContainText("The order has no items.");
    await card.getByLabel("Delivered").check();
    await card.getByLabel("Deliver to").fill("4 Bode Thomas, Surulere");
    await card.getByLabel("Delivery fee, ₦").fill("2000");
    await card.getByRole("button", { name: "Save" }).click();
    await expect(card.getByLabel("Deliver to")).toHaveValue(
      "4 Bode Thomas, Surulere",
    );
    await expect(card).toContainText("₦2,000.00");
    await expect(card.getByRole("button", { name: "Confirm" })).toBeDisabled();
    await card.getByRole("button", { name: "Drop" }).click();
    await expect(
      pane.getByRole("button", { name: "Start an order" }),
    ).toBeVisible();

    // Then with a catalogue, from a stand-in.
    // The stand-in: one order, changed as the API changes it.
    const items = [
      { id: "jp", name: "Jollof rice plate", price_kobo: 450000 },
      { id: "ch", name: "Chapman", price_kobo: 150000 },
    ];
    type Line = {
      item_id?: string;
      name?: string;
      quantity?: number;
      price_kobo?: number;
      said?: string;
      ask?: unknown;
    };
    let order:
      | (Record<string, unknown> & { lines: Line[]; version: number })
      | undefined;
    const sums = () => {
      if (!order) return;
      const itemsKobo = order.lines.reduce(
        (t, l) => t + (l.price_kobo ?? 0) * (l.quantity ?? 0),
        0,
      );
      const fee =
        order.fulfilment === "delivery" ? Number(order.delivery_kobo ?? 0) : 0;
      const open = order.status === "open" || order.status === "asked";
      const ready =
        open &&
        order.lines.length > 0 &&
        order.lines.every((l) => l.item_id && l.quantity && !l.ask) &&
        (order.fulfilment === "pickup" ||
          (order.fulfilment === "delivery" && Boolean(order.address)));
      Object.assign(order, {
        items_kobo: itemsKobo,
        total_kobo: itemsKobo + fee,
        ready,
        waits: ready
          ? undefined
          : order.lines.length
            ? "Say where to deliver it."
            : "The order has no items.",
        questions: order.lines.filter((l) => l.ask).length,
      });
    };
    await page.route("**/v1/customer-chats/*/orders", async (r) => {
      const chat = r
        .request()
        .url()
        .split("/customer-chats/")[1]
        ?.split("/")[0];
      if (r.request().method() === "POST") {
        order = {
          id: "d1",
          chat_id: chat,
          status: "open",
          lines: [],
          delivery_kobo: 0,
          items_kobo: 0,
          total_kobo: 0,
          questions: 0,
          ready: false,
          waits: "The order has no items.",
          version: 1,
          by_reader: false,
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
        };
        return r.fulfill({ status: 201, json: order });
      }
      return r.fulfill({
        json: {
          orders: order ? [order] : [],
          zones: [],
          pay_page: true,
          items,
        },
      });
    });
    await page.route("**/v1/order-drafts/d1**", async (r) => {
      const body = (r.request().postDataJSON() ?? {}) as Record<
        string,
        unknown
      >;
      if (!order) return r.fulfill({ status: 404, json: { error: "none" } });
      const url = r.request().url();
      if (url.endsWith("/confirm")) {
        order = {
          ...order,
          status: "confirmed",
          order_status: "awaiting_payment",
          reference: "BO-7QX4KD",
          version: order.version + 1,
        };
        return r.fulfill({ json: { order, told: true } });
      }
      const line = Number(body.line ?? 0);
      switch (body.op) {
        case "add": {
          const it = items.find((x) => x.id === body.item_id);
          order.lines.push({
            item_id: it?.id,
            name: it?.name,
            quantity: 1,
            price_kobo: it?.price_kobo,
          });
          break;
        }
        case "quantity":
          order.lines[line] = {
            ...order.lines[line],
            quantity: Number(body.quantity),
          };
          break;
        case "details":
          for (const k of ["fulfilment", "address", "delivery_kobo", "pay"])
            if (k in body) order[k] = body[k];
          break;
      }
      order.version += 1;
      sums();
      return r.fulfill({ json: order });
    });

    // The thread opens again from the page's address.
    await page.reload();
    await pane.getByRole("button", { name: "Start an order" }).click();
    await expect(card).toContainText("Needs you");
    await expect(card).toContainText("The order has no items.");
    await card.getByLabel("Add an item").selectOption({
      label: "Jollof rice plate · ₦4,500.00",
    });
    await expect(card.getByTestId("customer-order-line")).toContainText(
      "1 × Jollof rice plate",
    );
    await card
      .getByRole("button", { name: "One more Jollof rice plate" })
      .click();
    await expect(card.getByTestId("customer-order-line")).toContainText(
      "2 × Jollof rice plate",
    );
    await card.getByLabel("Delivered").check();
    await card.getByLabel("Deliver to").fill("12 Herbert Macaulay, Yaba");
    await card.getByLabel("Delivery fee, ₦").fill("1500");
    await card.getByRole("button", { name: "Save" }).click();
    await expect(card).toContainText("Ready to confirm");
    await expect(card).toContainText("₦10,500.00");
    await expect(card.getByRole("button", { name: "Confirm" })).toBeEnabled();
    await fits(page);
    await bothThemes(page, "an order on a customer's card");
    await page.setViewportSize({ width: 360, height: 640 });
    await fits(page);
    await bothThemes(page, "an order on a customer's card at 360");

    await card.getByRole("button", { name: "Confirm" }).click();
    await expect(card).toContainText("Waiting for payment");
    await expect(card).toContainText("Order BO-7QX4KD");
    await bothThemes(page, "a confirmed order");

    const off = await page.request.delete("/v1/whatsapp/number", {
      headers: H,
    });
    expect([200, 204]).toContain(off.status());
  });
});
