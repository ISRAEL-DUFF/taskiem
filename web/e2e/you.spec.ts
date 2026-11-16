import { createServer as createHttp, type Server as HttpServer } from "node:http";
import { createServer as createNet, type Server as NetServer } from "node:net";
import { expect, test, type Page } from "@playwright/test";
import { confirmEmail } from "./confirm";

// Taskiem for everyone, stage 1 (docs/spec/taskiem-for-everyone.md §5–§7,
// §18): the phone way in, You, the switcher with its second proof, changing
// the number, and Start something. Codes go out on Taskiem's WhatsApp number
// to the fake Graph API serve.sh points it at (as whatsapp.spec.ts), and
// the second proof's email to the SMTP sink (as password.spec.ts); both
// listen on the same ports as those specs, so they run one at a time.

test.describe.configure({ mode: "serial" });

const H = { "X-Taskiem-Request": "1" };
const graphPort = 12626;
const smtpPort = 12525;
const sent: { to: string; template?: { components?: { parameters?: { text?: string }[] }[] } }[] = [];
const inbox: string[] = [];
let graph: HttpServer;
let smtp: NetServer;

test.beforeAll(async () => {
  graph = createHttp((req, res) => {
    let body = "";
    req.on("data", (c: Buffer) => (body += c.toString("utf8")));
    req.on("end", () => {
      res.writeHead(200, { "Content-Type": "application/json" });
      if (req.method === "POST" && req.url?.endsWith("/messages")) {
        const m = JSON.parse(body) as (typeof sent)[number];
        sent.push(m);
        res.end(JSON.stringify({ messaging_product: "whatsapp", contacts: [{ input: m.to, wa_id: m.to.replace(/^\+/, "") }], messages: [{ id: `wamid.you${sent.length}` }] }));
        return;
      }
      res.end("{}");
    });
  });
  smtp = createNet((sock) => {
    let data = false;
    let buf = "";
    let msg = "";
    sock.write("220 sink ESMTP\r\n");
    sock.on("data", (chunk) => {
      buf += chunk.toString("utf8");
      for (let i = buf.indexOf("\r\n"); i >= 0; i = buf.indexOf("\r\n")) {
        const line = buf.slice(0, i);
        buf = buf.slice(i + 2);
        if (data) {
          if (line === ".") {
            data = false;
            inbox.push(msg);
            msg = "";
            sock.write("250 queued\r\n");
          } else msg += line + "\n";
          continue;
        }
        const verb = line.slice(0, 4).toUpperCase();
        if (verb === "EHLO" || verb === "HELO") sock.write("250 sink\r\n");
        else if (verb === "DATA") {
          data = true;
          sock.write("354 go ahead\r\n");
        } else if (verb === "QUIT") sock.end("221 bye\r\n");
        else sock.write("250 ok\r\n");
      }
    });
  });
  await new Promise<void>((resolve) => graph.listen(graphPort, "127.0.0.1", resolve));
  await new Promise<void>((resolve) => smtp.listen(smtpPort, "127.0.0.1", resolve));
});

test.afterAll(async () => {
  await new Promise<void>((resolve) => graph.close(() => resolve()));
  await new Promise<void>((resolve) => smtp.close(() => resolve()));
});

/** A Nigerian number no other test uses, as a person types it and as +234. */
function aNumber(): { typed: string; e164: string } {
  const rest = String(Date.now()).slice(-7) + String(Math.floor(Math.random() * 10));
  const digits = `81${rest.slice(0, 8)}`;
  return { typed: `0${digits.slice(0, 3)} ${digits.slice(3, 6)} ${digits.slice(6)}`, e164: `+234${digits}` };
}

/** The newest code WhatsApp carried to the number. */
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

async function phoneIn(page: Page, n: { typed: string; e164: string }) {
  await page.goto("/login");
  await expect(page.getByRole("heading", { name: "Welcome to Taskiem" })).toBeVisible();
  await expect(page.getByText("Chat with the businesses you buy from, and run your own.").first()).toBeVisible();
  const before = sent.length;
  await page.getByLabel("Your phone number").fill(n.typed);
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByRole("heading", { name: "Enter the code" })).toBeVisible();
  await expect(page.getByText(/We sent a 6-digit code to WhatsApp/)).toBeVisible();
  // A wrong code first: said plainly, and nothing moves.
  await page.getByLabel("6-digit code").fill("000000");
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByRole("alert")).toContainText("not right");
  await page.getByLabel("6-digit code").fill(await codeFor(n.e164, before));
  await page.getByRole("button", { name: "Continue" }).click();
}

test("a new person makes an account with their phone, then starts workflows and a business", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const n = aNumber();
  await phoneIn(page, n);
  await expect(page.getByRole("heading", { name: "Make your account" })).toBeVisible();
  await page.getByLabel("Your name").fill("Ngozi Eze");
  await page.getByLabel("I'm 18 or over").check();
  await page.getByTestId("way-in-terms").getByRole("checkbox").check();
  await page.getByRole("button", { name: "Create" }).click();

  // You: no business yet, so no switcher.
  await expect(page.getByRole("heading", { name: "Hello, Ngozi" })).toBeVisible();
  await expect(page).toHaveURL(/\/you$/);
  await expect(page.getByText("Chats with businesses will show here.")).toBeVisible();
  await expect(page.getByTestId("switcher")).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await page.getByRole("link", { name: "Settings" }).click();
  await expect(page.getByTestId("my-phone")).toContainText(n.e164.replace(/^\+234(\d{3})(\d{3})(\d{4})$/, "+234 $1 $2 $3"));

  // Start something: workflows, with no name.
  await page.getByRole("link", { name: "Chats" }).click();
  await page.getByTestId("start-something").click();
  await expect(page.getByText("What do you want to start?")).toBeVisible();
  await page.getByTestId("start-workflows").click();
  await page.getByRole("button", { name: "Start workflows" }).click();
  await expect(page).toHaveURL(/\/workflows$/);
  await expect(page.getByTestId("switcher-top")).toContainText("Ngozi Eze's workflows");
  const me = (await (await page.request.get("/v1/me")).json()) as { tenant_kind: string; areas: Record<string, boolean> };
  expect(me.tenant_kind).toBe("workflows");
  expect(Object.values(me.areas).some(Boolean)).toBe(false);

  // Back to You, then a business.
  await page.getByTestId("switcher-top").click();
  const sheet = page.getByRole("dialog", { name: "Switch to" });
  await expect(sheet.getByTestId("switch-business")).toContainText(["Workflows"]);
  await sheet.getByTestId("switch-you").click();
  await expect(page.getByRole("heading", { name: "Hello, Ngozi" })).toBeVisible();
  await page.getByTestId("start-something").click();
  await page.getByTestId("start-business").click();
  await page.getByLabel("Business name").fill("Ngozi Fabrics");
  await page.getByText("I sell things").click();
  await page.getByRole("button", { name: "Start the business" }).click();
  await expect(page).toHaveURL(/\/start$/);
  await expect(page.getByTestId("switcher-top")).toContainText("Ngozi Fabrics");
  const shop = (await (await page.request.get("/v1/me")).json()) as { tenant_kind: string; areas: Record<string, boolean> };
  expect(shop.tenant_kind).toBe("business");
  expect(shop.areas.storefront).toBe(true);
});

test("an owner signs in with their phone on a new device, proves it is them, switches, and changes number", async ({ browser }) => {
  // The owner, made with an email and a password, adds a phone under Account.
  const owner = await browser.newPage();
  const email = `you-${Date.now()}@you.test`;
  const tenant = `Kemi Stores ${String(Date.now()).slice(-6)}`;
  const up = await owner.request.post("/v1/signup", { headers: H, data: { tenant, email, name: "Kemi Ade", password: "correct horse battery", kinds: ["shop"] } });
  expect(up.status(), await up.text()).toBe(201);
  confirmEmail(email);
  const n = aNumber();
  await owner.goto("/account");
  await owner.getByRole("button", { name: "Add a phone" }).click();
  let before = sent.length;
  await owner.getByLabel("New phone number").fill(n.typed);
  await owner.getByRole("button", { name: "Send a code" }).click();
  await owner.getByLabel("Code sent to your new number").fill(await codeFor(n.e164, before));
  await owner.getByRole("button", { name: "Confirm the new number" }).click();
  await expect(owner.getByTestId("my-phone")).toContainText("+234");
  await owner.close();

  // A new device: the phone way in opens You, the business locked.
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await phoneIn(page, n);
  await expect(page.getByRole("heading", { name: "Hello, Kemi" })).toBeVisible();
  await expect(page.getByTestId("businesses-locked")).toBeVisible();
  await page.getByTestId("switcher").click();
  const sheet = page.getByRole("dialog", { name: "Switch to" });
  await expect(sheet.getByTestId("switch-you")).toHaveAttribute("aria-current", "true");
  const row = sheet.getByTestId("switch-business").filter({ hasText: tenant });
  await expect(row).toContainText("prove it's you");
  await row.click();

  // The second proof: a link to the confirmed email, opened on this device.
  await expect(page.getByTestId("second-proof")).toBeVisible();
  await page.getByRole("button", { name: "Email me a link" }).click();
  await expect(page.getByTestId("proof-link-sent")).toContainText("We sent a link to");
  let link = "";
  await expect
    .poll(() => {
      link = inbox.find((x) => x.includes(`To: ${email}`))?.match(/(http\S+\/second-proof#token=[\w.-]+)/)?.[1] ?? "";
      return link;
    })
    .not.toBe("");
  await page.goto(link.replace("http://localhost:18080", ""));
  await expect(page.getByText("Done: this device is yours now.")).toBeVisible();
  await page.getByTestId("second-proof-page").getByTestId("switch-business").filter({ hasText: tenant }).click();
  await expect(page).toHaveURL(/\/home$/);
  await expect(page.getByTestId("switcher")).toContainText(tenant);

  // Back to You from the business.
  await page.getByTestId("switcher").click();
  await page.getByRole("dialog", { name: "Switch to" }).getByTestId("switch-you").click();
  await expect(page.getByRole("heading", { name: "Hello, Kemi" })).toBeVisible();
  await expect(page.getByTestId("businesses-locked")).toHaveCount(0);

  // Change number: this device is proved, so only the new number's code.
  const m = aNumber();
  await page.getByRole("link", { name: "Settings" }).click();
  await page.getByRole("button", { name: "Change number" }).click();
  before = sent.length;
  await page.getByLabel("New phone number").fill(m.typed);
  await page.getByRole("button", { name: "Send a code" }).click();
  await page.getByLabel("Code sent to your new number").fill("111111");
  await page.getByRole("button", { name: "Confirm the new number" }).click();
  await expect(page.getByRole("alert")).toContainText("not right");
  await page.getByLabel("Code sent to your new number").fill(await codeFor(m.e164, before));
  await page.getByRole("button", { name: "Confirm the new number" }).click();
  await expect(page.getByTestId("change-number")).toHaveCount(0);
  await expect(page.getByTestId("my-phone")).toContainText(m.e164.replace(/^\+234(\d{3})(\d{3})(\d{4})$/, "+234 $1 $2 $3"));
  // Still signed in: a wrong code is not a signing out.
  await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
  await ctx.close();
});
