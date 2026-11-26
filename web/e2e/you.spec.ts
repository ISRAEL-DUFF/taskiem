import { createServer as createHttp, type Server as HttpServer } from "node:http";
import { createServer as createNet, type Server as NetServer } from "node:net";
import { expect, test, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { axe, describe as describeAxe } from "./a11y";
import { confirmEmail } from "./confirm";

// Taskiem for everyone, stage 1 (docs/spec/taskiem-for-everyone.md §5–§7,
// §18): the phone way in, You, the switcher with its second proof, changing
// the number, and Start something. Codes go out on Taskiem's WhatsApp number
// to the fake Graph API serve.sh points it at (as whatsapp.spec.ts), and
// the second proof's email to the SMTP sink (as password.spec.ts); both
// listen on the same ports as those specs, so they run one at a time.

test.describe.configure({ mode: "serial" });
test.use({ bypassCSP: true }); // axe is injected into the page

const H = { "X-Taskiem-Request": "1" };
const graphPort = 12626;
const smtpPort = 12525;
const sent: { to: string; template?: { components?: { parameters?: { text?: string }[] }[] } }[] = [];
const inbox: string[] = [];
let graph: HttpServer;
let smtp: NetServer;
/** The owner of the second test, for the invitation after it. */
let kemi: { email: string; phone: { typed: string; e164: string } } | undefined;

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

/** The phone way in; [from] is a page opened signed out first, which the
 * way in goes on to (?next=). */
async function phoneIn(page: Page, n: { typed: string; e164: string }, { from, wrongFirst = true }: { from?: string; wrongFirst?: boolean } = {}) {
  if (from) {
    await page.goto(from);
    await expect(page).toHaveURL(new RegExp(`/login\\?next=${encodeURIComponent(from).replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`));
  } else await page.goto("/login");
  await expect(page.getByRole("heading", { name: "Welcome to Taskiem" })).toBeVisible();
  await expect(page.getByText("Chat with the businesses you buy from, and run your own.").first()).toBeVisible();
  const before = sent.length;
  await page.getByLabel("Your phone number").fill(n.typed);
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByRole("heading", { name: "Enter the code" })).toBeVisible();
  await expect(page.getByText(/We sent a 6-digit code to WhatsApp/)).toBeVisible();
  if (wrongFirst) {
    // A wrong code first: said plainly, and nothing moves.
    await page.getByLabel("6-digit code").fill("000000");
    await page.getByRole("button", { name: "Continue" }).click();
    await expect(page.getByRole("alert")).toContainText("That code is not right.");
  }
  await page.getByLabel("6-digit code").fill(await codeFor(n.e164, before));
  await page.getByRole("button", { name: "Continue" }).click();
}

test("the way in says what is wrong with a number before sending, and takes a pasted code", async ({ page }) => {
  await page.goto("/login");
  const before = sent.length;
  await page.getByLabel("Your phone number").fill("0803 123");
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByRole("alert")).toContainText("That doesn't look like a phone number");
  expect(sent.length).toBe(before);
  const n = aNumber();
  await page.getByLabel("Your phone number").fill(n.typed);
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByRole("heading", { name: "Enter the code" })).toBeVisible();
  // "123 456" pasted is six digits, not "123 45".
  await page.getByLabel("6-digit code").fill("123 456");
  await expect(page.getByLabel("6-digit code")).toHaveValue("123456");
  // A new number: the account step has a way back to the number.
  await page.getByLabel("6-digit code").fill(await codeFor(n.e164, before));
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByRole("heading", { name: "Make your account" })).toBeVisible();
  await page.getByRole("button", { name: "Use another number" }).click();
  await expect(page.getByRole("heading", { name: "Welcome to Taskiem" })).toBeVisible();
});

test("a second proof's link opened where nobody is signed in says where to open it, and keeps the token", async ({ page }) => {
  await page.goto("/second-proof#token=abc.def");
  await expect(page.getByTestId("second-proof-elsewhere")).toContainText("Open this link in the browser where you asked for it");
  await expect(page).toHaveURL(/\/second-proof$/);
  expect(page.url()).not.toContain("abc.def");
});

test("a new person makes an account with their phone, then starts workflows and a business", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const n = aNumber();
  // A page of their own opened signed out: the way in goes on to it.
  await phoneIn(page, n, { from: "/you/settings" });
  await expect(page.getByRole("heading", { name: "Make your account" })).toBeVisible();
  await page.getByLabel("Your name").fill("Ngozi Eze");
  await page.getByLabel("I'm 18 or over").check();
  await page.getByTestId("way-in-terms").getByRole("checkbox").check();
  await page.getByRole("button", { name: "Create" }).click();

  await expect(page).toHaveURL(/\/you\/settings$/);
  await expect(page.getByTestId("my-phone")).toContainText(n.e164.replace(/^\+234(\d{3})(\d{3})(\d{4})$/, "+234 $1 $2 $3"));

  // You: no business yet, so no switcher.
  await page.getByRole("link", { name: "Chats" }).click();
  await expect(page.getByRole("heading", { name: "Hello, Ngozi" })).toBeVisible();
  await expect(page).toHaveURL(/\/you$/);
  await expect(page.getByText("Your chats with businesses and people will show here.")).toBeVisible();
  await expect(page.getByTestId("switcher")).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);

  // Start something: workflows, with no name.
  await page.getByTestId("start-something").click();
  await expect(page.getByText("What do you want to start?")).toBeVisible();
  await page.getByTestId("start-workflows").click();
  await page.getByRole("button", { name: "Start workflows" }).click();
  // A new account with no way to prove a new phone (the PIN is none):
  // one first, before the workspace opens; it cannot be skipped.
  const step = page.getByTestId("sign-in-way-step");
  await expect(step.getByRole("heading", { name: "Add a way to sign in on a new phone" })).toBeVisible();
  await expect(step).toContainText("A code to your number isn't enough on its own if someone gets your SIM.");
  await expect(step.getByRole("button", { name: /skip|not now|later/i })).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const vs = await axe(page);
    expect(vs, describeAxe(`the sign-in way step (${scheme})`, vs)).toEqual([]);
  }
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto("/workflows");
  await expect(step).toBeVisible();
  await step.getByLabel("New password", { exact: true }).fill("ngozi long password");
  await step.getByLabel("New password again").fill("ngozi long password");
  await step.getByRole("button", { name: "Set password" }).click();
  await expect(page).toHaveURL(/\/workflows$/);
  await expect(page.getByTestId("sign-in-way-step")).toHaveCount(0);
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
  // Two clicks make one business: the button stays off once it has started.
  await page.getByRole("button", { name: "Start the business" }).dblclick();
  // The new business's own start page, not /you/start where this began.
  await expect(page).toHaveURL((u) => u.pathname === "/start");
  const mine = (await (await page.request.get("/v1/me/businesses")).json()) as { businesses: { name: string }[] };
  expect(mine.businesses.filter((b) => b.name === "Ngozi Fabrics")).toHaveLength(1);
  await expect(page.getByTestId("switcher-top")).toContainText("Ngozi Fabrics");
  const shop = (await (await page.request.get("/v1/me")).json()) as { tenant_kind: string; areas: Record<string, boolean> };
  expect(shop.tenant_kind).toBe("business");
  expect(shop.areas.storefront).toBe(true);
});

/** Runs SQL on the e2e database, as confirmEmail does; :'v' is [v]. */
function sql(text: string, v: string) {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-q", "-v", `v=${v}`], { input: text + "\n", encoding: "utf8" });
}

test("someone already using Taskiem with no way to sign in on a new phone is reminded on Today and Account until they add an email", async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 800 });
  const n = aNumber();
  await phoneIn(page, n, { wrongFirst: false });
  await page.getByLabel("Your name").fill("Tolu Ade");
  await page.getByLabel("I'm 18 or over").check();
  await page.getByTestId("way-in-terms").getByRole("checkbox").check();
  await page.getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("heading", { name: "Hello, Tolu" })).toBeVisible();
  // An account in use before the change: reminded, not stopped.
  sql("UPDATE users SET sign_in_way_due = false WHERE id = (SELECT user_id FROM user_phones WHERE number = :'v');", n.e164);
  await page.getByTestId("start-something").click();
  await page.getByTestId("start-business").click();
  await page.getByLabel("Business name").fill("Tolu Prints");
  await page.getByText("I sell things").click();
  await page.getByRole("button", { name: "Start the business" }).click();
  await expect(page).toHaveURL((u) => u.pathname === "/start");
  await page.goto("/home");
  const note = page.getByTestId("sign-in-way");
  await expect(note).toContainText("Add a way to sign in on a new phone.");
  await expect(note).toContainText("A code to your number isn't enough on its own if someone gets your SIM.");
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(360);
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const vs = await axe(page);
    expect(vs, describeAxe(`Today with the reminder (${scheme})`, vs)).toEqual([]);
  }
  await page.emulateMedia({ colorScheme: "light" });
  await expect(note.getByRole("link", { name: "Add a way" })).toHaveAttribute("href", "/account#sign-in-way");
  // On You too, where nothing can be added: its button opens the business at Account.
  await page.getByTestId("switcher-top").click();
  await page.getByRole("dialog", { name: "Switch to" }).getByTestId("switch-you").click();
  await expect(page.getByRole("heading", { name: "Hello, Tolu" })).toBeVisible();
  await page.getByTestId("sign-in-way").getByRole("button", { name: "Add a way" }).click();
  await expect(page).toHaveURL(/\/account#sign-in-way$/);
  await expect(page.getByTestId("sign-in-way")).toBeVisible();
  const email = `tolu-${Date.now()}@you.test`;
  const mailed = inbox.length;
  await page.getByTestId("add-email").getByLabel("Email").fill(email);
  await page.getByTestId("add-email").getByRole("button", { name: "Send the link" }).click();
  let link = "";
  await expect
    .poll(() => {
      link = inbox.slice(mailed).filter((x) => x.includes(`To: ${email}`)).map((x) => x.replace(/=\n/g, "").match(/(http\S+\/confirm-email#\S+)/)?.[1]).find(Boolean) ?? "";
      return link;
    })
    .not.toBe("");
  // Its link, opened: the email is a way, and the reminder goes.
  await page.goto(link.replace("http://localhost:18080", "").replace(/=3D/g, "="));
  await expect(page.getByTestId("email-confirmed")).toBeVisible();
  await page.goto("/home");
  await expect(page.getByRole("heading", { name: /Tolu/ })).toBeVisible();
  await expect(page.getByTestId("sign-in-way")).toHaveCount(0);
  const me = (await (await page.request.get("/v1/me")).json()) as { user: { needs_sign_in_way: boolean } };
  expect(me.user.needs_sign_in_way).toBe(false);
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
  await owner.getByTestId("change-number").getByRole("button", { name: "Send a code" }).click();
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
      link = inbox.filter((x) => x.includes(`To: ${email}`)).map((x) => x.match(/(http\S+\/second-proof#token=[\w.-]+)/)?.[1]).find(Boolean) ?? "";
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
  await page.getByTestId("change-number").getByRole("button", { name: "Send a code" }).click();
  await page.getByLabel("Code sent to your new number").fill("111111");
  await page.getByRole("button", { name: "Confirm the new number" }).click();
  await expect(page.getByRole("alert")).toContainText("That code is not right.");
  // A wrong code is not a signing out: the sheet stays, the session too.
  await expect(page.getByTestId("change-number")).toBeVisible();
  await expect(page.getByText("You were signed out")).toHaveCount(0);
  expect((await page.request.get("/v1/me")).status()).toBe(200);
  await page.getByLabel("Code sent to your new number").fill(await codeFor(m.e164, before));
  await page.getByRole("button", { name: "Confirm the new number" }).click();
  await expect(page.getByTestId("change-number")).toHaveCount(0);
  await expect(page.getByTestId("my-phone")).toContainText(m.e164.replace(/^\+234(\d{3})(\d{3})(\d{4})$/, "+234 $1 $2 $3"));
  // Still signed in: a wrong code is not a signing out.
  await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();

  // A business's page opened signed out: a phone code alone never carries
  // an earlier proof (a device id can be copied), so the second proof is
  // asked again, and then the one business opens on that page.
  await page.request.post("/v1/auth/logout", { headers: H });
  await phoneIn(page, m, { from: "/members", wrongFirst: false });
  const proof = page.getByRole("dialog", { name: "Prove it's you" });
  await expect(proof).toBeVisible();
  await proof.getByLabel("Your password").fill("correct horse battery");
  await proof.getByRole("button", { name: "Confirm with my password" }).click();
  await expect(page).toHaveURL(/\/members$/);
  await expect(page.getByTestId("switcher")).toContainText(tenant);
  await page.request.post("/v1/auth/logout", { headers: H });
  await ctx.close();
  kemi = { email, phone: m };
});

test("an invitation accepted on a new device asks for a second proof first, then opens the business", async ({ browser }) => {
  test.skip(!kemi, "needs the owner of the test before");
  const who = kemi as NonNullable<typeof kemi>;
  // Another business invites Kemi by her email.
  const inviter = await browser.newPage();
  const tenant = `Bola Foods ${String(Date.now()).slice(-6)}`;
  const bola = `bola-${Date.now()}@you.test`;
  const up = await inviter.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: bola, name: "Bola", password: "correct horse battery", kinds: ["shop"] },
  });
  expect(up.status(), await up.text()).toBe(201);
  confirmEmail(bola);
  const invited = await inviter.request.post("/v1/members", { headers: H, data: { email: who.email, roles: ["builder"] } });
  expect(invited.status(), await invited.text()).toBe(201);
  await inviter.close();

  // A new device: signed in by phone, the businesses locked.
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await phoneIn(page, who.phone, { wrongFirst: false });
  await expect(page.getByTestId("businesses-locked")).toBeVisible();
  await page.getByTestId("my-invitations").getByTestId("my-invitation").filter({ hasText: tenant }).getByRole("button", { name: "Accept" }).click();
  // Not moved: the second proof first (the email link, in another tab).
  await expect(page.getByTestId("second-proof")).toBeVisible();
  const me = (await (await page.request.get("/v1/me")).json()) as { personal: boolean };
  expect(me.personal).toBe(true);
  const mailed = inbox.length;
  await page.getByRole("button", { name: "Email me a link" }).click();
  let link = "";
  await expect
    .poll(() => {
      link = inbox.slice(mailed).filter((x) => x.includes(`To: ${who.email}`)).map((x) => x.match(/(http\S+\/second-proof#token=[\w.-]+)/)?.[1]).find(Boolean) ?? "";
      return link;
    })
    .not.toBe("");
  const other = await ctx.newPage();
  await other.goto(link.replace("http://localhost:18080", ""));
  await expect(other.getByText("Done: this device is yours now.")).toBeVisible();
  await other.close();
  // Back in the tab that asked: it sees the link was opened.
  // (Coming back to it may already have; else the button asks.)
  await page.bringToFront();
  await page
    .getByRole("button", { name: "I've opened the link" })
    .click({ timeout: 3000 })
    .catch(() => undefined);
  await expect(page).toHaveURL(/\/home$/);
  await expect(page.getByTestId("switcher")).toContainText(tenant);
  await ctx.close();
});
