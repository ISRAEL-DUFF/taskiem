import { createHmac } from "node:crypto";
import { createServer, type Server } from "node:http";
import { expect, test } from "@playwright/test";

// Linking a WhatsApp number under Account: the code goes out through a
// fake Graph API on the port serve.sh points the platform number at. The
// same fake answers for an organisation's own number (Settings > WhatsApp
// number) and its WhatsApp Business Account's templates.

const graphPort = 12626;
const sent: { to: string; type: string; template?: { name: string; components: { type: string; parameters: { text?: string }[] }[] } }[] = [];
let graph: Server;

// The own number whatsapp-templates connects, and its account's templates
// as the fake Business Management API lists them.
const own = { pnid: "222000111222", waba: "333000111333", token: "EAAown", secret: "e2e-own-app-secret-16", verify: "e2e-own-verify-token" };
const templates = new Map<string, { id: string; status: string; reason?: string }>();

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
      if (req.method === "POST" && url.pathname === "/v25.0/106540352242922/messages" && req.headers.authorization === "Bearer EAAe2e") {
        const m = JSON.parse(body) as (typeof sent)[number];
        sent.push(m);
        json(200, { messaging_product: "whatsapp", contacts: [{ input: m.to, wa_id: m.to.slice(1) }], messages: [{ id: `wamid.e2e${sent.length}` }] });
        return;
      }
      if (req.headers.authorization !== `Bearer ${own.token}`) {
        res.writeHead(404).end();
        return;
      }
      if (req.method === "GET" && url.pathname === `/v25.0/${own.pnid}`) {
        json(200, { display_phone_number: "+234 800 000 0009", verified_name: "E2E Stores", id: own.pnid });
        return;
      }
      if (url.pathname === `/v25.0/${own.waba}/message_templates`) {
        if (req.method === "GET") {
          json(200, {
            data: [...templates].map(([name, t]) => ({ id: t.id, name, language: "en", status: t.status, category: "UTILITY", rejected_reason: t.reason ?? "NONE" })),
            paging: { cursors: { before: "a", after: "b" } },
          });
          return;
        }
        const def = JSON.parse(body) as { name: string };
        if (templates.has(def.name)) {
          json(400, { error: { message: "A template with that name exists", code: 100 } });
          return;
        }
        const t = { id: `9${templates.size + 1}00000000000001`, status: "PENDING" };
        templates.set(def.name, t);
        json(200, { id: t.id, status: t.status, category: "UTILITY" });
        return;
      }
      res.writeHead(404).end();
    });
  });
  await new Promise<void>((resolve) => graph.listen(graphPort, "127.0.0.1", resolve));
});

test.afterAll(() => new Promise<void>((resolve) => graph.close(() => resolve())));

test("link a WhatsApp number with a code, then unlink it", async ({ page }) => {
  const email = `wa-${Date.now()}@e2e.test`;
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
  const added = await page.request.post("/v1/members", { headers: { "X-Taskiem-Request": "1" }, data: { email, password: "correct horse battery", roles: ["viewer"] } });
  expect(added.status()).toBe(201);
  await page.request.post("/v1/auth/logout", { headers: { "X-Taskiem-Request": "1" } });
  await page.context().clearCookies();

  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();

  await page.goto("/account");
  const wa = page.getByTestId("whatsapp");
  await expect(wa.getByText("+1 555 000 1111")).toBeVisible();
  const number = `+23480${String(Date.now()).slice(-8)}`;
  await wa.getByLabel("Your WhatsApp number").fill(number);
  await wa.getByLabel("Your password, to link a number").fill("correct horse battery");
  await wa.getByRole("button", { name: "Send a code" }).click();

  await expect.poll(() => sent.filter((m) => m.to === number).length).toBe(1);
  const otp = sent.find((m) => m.to === number);
  expect(otp?.type).toBe("template");
  expect(otp?.template?.name).toBe("taskiem_otp");
  const code = otp?.template?.components[0]?.parameters[0]?.text ?? "";
  expect(code).toMatch(/^\d{6}$/);

  // A wrong code first.
  await wa.getByLabel(`Code sent to ${number}`).fill(code === "000000" ? "111111" : "000000");
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
test("the own number's templates: submit, hear from Meta, check now", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();

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
  await expect(tpls.getByTestId("whatsapp-templates-summary")).toContainText("0 of 20 templates approved");
  await expect(tpls.getByTestId("whatsapp-templates-summary")).toContainText("taskiem_invoice");
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
    entry: [{ id: own.waba, time: 1759800000, changes: [{ field: "message_template_status_update", value: { event: "APPROVED", message_template_id: templates.get("taskiem_invoice")?.id, message_template_name: "taskiem_invoice", message_template_language: "en", reason: "NONE" } }] }],
  });
  const sig = "sha256=" + createHmac("sha256", own.secret).update(approved).digest("hex");
  const hook = await page.request.post(`/channels/whatsapp/n/${own.pnid}`, { headers: { "Content-Type": "application/json", "X-Hub-Signature-256": sig }, data: approved });
  expect(hook.status()).toBe(200);
  await page.reload();
  await expect(section.getByTestId("whatsapp-template-taskiem_invoice").getByTestId("status")).toHaveText("Approved");
  await expect(section.getByTestId("whatsapp-templates-summary")).toContainText("1 of 20 templates approved");

  // Check now asks Meta: a rejection comes with its reason.
  templates.set("taskiem_receipt", { id: "930000000000001", status: "REJECTED", reason: "INVALID_FORMAT" });
  await section.getByRole("button", { name: "Check now" }).click();
  const receipt = section.getByTestId("whatsapp-template-taskiem_receipt");
  await expect(receipt.getByTestId("status")).toHaveText("Rejected");
  await expect(receipt).toContainText("INVALID_FORMAT");

  page.once("dialog", (d) => void d.accept());
  await section.getByRole("button", { name: "Disconnect" }).click();
  await expect(section.getByTestId("whatsapp-templates")).toHaveCount(0);
});
