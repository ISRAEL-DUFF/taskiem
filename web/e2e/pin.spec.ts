import { execFileSync } from "node:child_process";
import { createServer as createHttp, type Server as HttpServer } from "node:http";
import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });
test.describe.configure({ mode: "serial" });

// Your PIN on the Account page (GET/PUT/DELETE /v1/me/pin;
// docs/spec/transaction-pin.md): four digits, for approving on WhatsApp
// and paying small amounts. With no phone and no passkey, it says to add a
// phone; with a phone, a code goes to it (by WhatsApp, to the fake Graph
// API below), and once typed the PIN is saved and pays at once. Removed
// with no proof. Against the real server.

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
        res.end(JSON.stringify({ messaging_product: "whatsapp", contacts: [{ input: m.to, wa_id: m.to.replace(/^\+/, "") }], messages: [{ id: `wamid.pin${sent.length}` }] }));
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

function sql(text: string): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, `/${process.env.E2E_DB ?? "taskiem_e2e"}$1`), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}

async function signUp(page: Page): Promise<string> {
  const email = `${letters()}-${Math.random().toString(36).slice(2, 8)}@pin.test`;
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant: `Pin Kitchen ${letters()}`, email, name: "Ada Owner", password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
  return email;
}

async function noAxe(page: Page, what: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const vs = await axe(page);
    expect(vs, describe(`${what} (${scheme})`, vs)).toEqual([]);
  }
  await page.emulateMedia({ colorScheme: "light" });
}

for (const width of [390, 360])
  test(`Your PIN: set with a code to the phone, paying at once, and removed, ${width} px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    const email = await signUp(page);
    await page.goto("/account");
    await page.evaluate(() => localStorage.setItem("taskiem-theme", "system"));
    const card = page.getByTestId("your-pin");

    // No phone and no passkey: it says what to add.
    await expect(card.getByTestId("your-pin-how")).toContainText("add your phone number");

    // A phone on the account: a code to it.
    const e164 = `+23480${String(Date.now()).slice(-7)}${width === 390 ? 1 : 2}`;
    sql(`INSERT INTO user_phones (user_id, number) SELECT id, '${e164}' FROM users WHERE email = '${email}'`);
    await page.reload();
    await expect(card.getByTestId("your-pin-how")).toContainText("We send a code to");
    await noAxe(page, "Your PIN");

    // Weak, and two different PINs, are refused here.
    const first = card.getByRole("textbox", { name: /^PIN / });
    const again = card.getByLabel("The same PIN again");
    await expect(first).toHaveAttribute("maxlength", "4");
    await first.fill("1234");
    await again.fill("1234");
    await card.getByRole("button", { name: "Send me a code" }).click();
    await expect(card.getByText("Choose a PIN that is not one digit repeated or a run like 1234.")).toBeVisible();
    await first.fill("4829");
    await again.fill("4828");
    await card.getByRole("button", { name: "Send me a code" }).click();
    await expect(card.getByText("The two PINs are not the same.")).toBeVisible();

    // The code, by WhatsApp; typed, the PIN is saved and pays at once.
    await again.fill("4829");
    const before = sent.length;
    await card.getByRole("button", { name: "Send me a code" }).click();
    let code = "";
    await expect
      .poll(() => {
        const m = sent.slice(before).filter((x) => x.to === e164 || x.to === e164.slice(1)).pop();
        code = m?.template?.components?.flatMap((c) => c.parameters ?? []).find((p) => /^\d{6}$/.test(p.text ?? ""))?.text ?? "";
        return code;
      })
      .toMatch(/^\d{6}$/);
    const box = card.getByTestId("your-pin-code");
    await expect(box).toBeVisible();
    await noAxe(page, "Your PIN, the code");
    await box.fill(code);
    await card.getByRole("button", { name: "Set PIN" }).click();
    await expect(card.getByTestId("your-pin-state")).toContainText("PIN set");
    expect(sql(`SELECT pays_from <= now() FROM whatsapp_pins p JOIN users u ON u.id = p.user_id WHERE u.email = '${email}'`)).toBe("t");
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);

    page.once("dialog", (d) => void d.accept());
    await card.getByRole("button", { name: "Remove" }).click();
    await expect(card.getByTestId("your-pin-state")).toBeHidden();
  });
