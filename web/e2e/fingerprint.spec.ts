import { execFileSync } from "node:child_process";
import { generateKeyPairSync } from "node:crypto";
import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// Fingerprint on your phones, on the Account page (GET and DELETE
// /v1/me/fingerprint): the web cannot pay by fingerprint, but lists the
// phones it is on for and removes one. The phone's key is put in the
// database as the app would register it. Against the real server.

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

function sql(text: string): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}

async function signUp(page: Page): Promise<string> {
  const email = `${letters()}-${Math.random().toString(36).slice(2, 8)}@fingerprint.test`;
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant: `Fingerprint Kitchen ${letters()}`, email, name: "Ada Owner", password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
  return email;
}

test("Fingerprint on your phones: listed on the web, which cannot pay with it, and removed", async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 800 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  const email = await signUp(page);
  await page.goto("/account");
  const card = page.getByTestId("fingerprint-phones");
  await expect(card).toContainText("Not on for any phone.");
  await expect(card).toContainText("₦500,000 a payment");

  const der = generateKeyPairSync("ec", { namedCurve: "prime256v1" }).publicKey.export({ type: "spki", format: "der" }).toString("hex");
  sql(`INSERT INTO pay_keys (user_id, device_id, device_name, platform, public_key)
    SELECT id, gen_random_uuid(), 'Tecno Spark 10', 'android', decode('${der}', 'hex') FROM users WHERE lower(email) = lower('${email}')`);
  await page.evaluate(() => localStorage.setItem("taskiem-theme", "system"));
  await page.reload();
  await expect(card.getByTestId("fingerprint-phone")).toContainText("Tecno Spark 10");
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const vs = await axe(page);
    expect(vs, describe(`Fingerprint on your phones (${scheme})`, vs)).toEqual([]);
  }
  await page.emulateMedia({ colorScheme: "light" });
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(360);

  page.once("dialog", (d) => void d.accept());
  await card.getByRole("button", { name: "Remove Tecno Spark 10" }).click();
  await expect(card).toContainText("Not on for any phone.");
  expect(sql(`SELECT count(*) FROM pay_keys k JOIN users u ON u.id = k.user_id WHERE lower(u.email) = lower('${email}')`)).toBe("0");
});
