import { createServer, type Server } from "node:http";
import { expect, test } from "@playwright/test";

// Gate G4's path in the browser: sign up, pick a template, connect Termii
// (a fake on the port serve.sh points TASKIEM_TERMII_URL at), publish, run
// it once, and see the time from signup to the first successful run.

const termiiPort = 12727;
const texts: { to: string; sms: string }[] = [];
let termii: Server;

test.beforeAll(async () => {
  termii = createServer((req, res) => {
    let body = "";
    req.on("data", (c: Buffer) => (body += c.toString("utf8")));
    req.on("end", () => {
      if (req.url === "/api/sms/send") {
        const m = JSON.parse(body) as { to: string; sms: string };
        texts.push({ to: m.to, sms: m.sms });
      }
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ message_id: `m${texts.length}`, message: "Successfully Sent", balance: 99 }));
    });
  });
  await new Promise<void>((resolve) => termii.listen(termiiPort, "127.0.0.1", resolve));
});

test.afterAll(() => new Promise<void>((resolve) => termii.close(() => resolve())));

test("sign up, set up a template, and run it once", async ({ page }) => {
  const started = Date.now();
  const email = `ada-${started}@stores.test`;

  await page.goto("/login");
  await page.getByRole("link", { name: "Create an account" }).click();
  await page.getByLabel("Business or organisation name").fill("Ada Stores");
  await page.getByLabel("Your name").fill("Ada Obi");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Create account" }).click();

  // Signed in, on Get started, with nothing done yet.
  await expect(page.getByRole("heading", { name: "Get started" })).toBeVisible();
  await expect(page.getByTestId("onboarding-progress")).toHaveText("0 of 6 done");
  await expect(page.getByRole("link", { name: /Get started \(0\/6\)/ })).toBeVisible();

  // The guided first workflow.
  await page.getByRole("link", { name: /Text the owner about each new order/ }).click();
  await expect(page.getByRole("heading", { name: "Text the owner about each new order" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Create and publish" })).toBeDisabled();

  const connect = page.getByRole("form", { name: "Connect Termii" });
  await connect.getByLabel("API key").fill("TL-e2e-key");
  await connect.getByRole("button", { name: "Save connection" }).click();
  await expect(page.getByTestId("connected-termii")).toContainText("connected as main");
  await page.getByLabel("owner_phone").fill("2348012345678");
  await page.getByRole("button", { name: "Save owner_phone" }).click();
  await expect(page.getByTestId("variable-owner_phone")).toContainText("is set");

  await page.getByRole("button", { name: "Create and publish" }).click();
  await expect(page.getByText("Published.")).toBeVisible();
  await expect(page.getByLabel("Test input")).toHaveValue(/"order_id": "TEST-1"/);
  await page.getByRole("button", { name: "Run it now" }).click();
  await expect(page.getByText("It worked.")).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId("guide-run-status").locator(".badge")).toHaveText("completed");
  expect(texts).toHaveLength(1);
  expect(texts[0]).toMatchObject({ to: "2348012345678" });
  expect(texts[0]?.sms).toContain("New order TEST-1");

  // The checklist follows, and the time to the first run is shown.
  await page.getByRole("link", { name: "← Get started" }).click();
  await expect(page.getByTestId("first-run-time")).toContainText("after you signed up");
  for (const id of ["connection", "workflow", "publish", "first_run"]) {
    await expect(page.getByTestId(`check-${id}`)).toHaveClass("done");
  }
  await expect(page.getByTestId("check-invite")).not.toHaveClass("done");
  await expect(page.getByTestId("check-verify_email")).not.toHaveClass("done");

  // Gate G4: under 15 minutes, as the server measured it.
  const ob = await (await page.request.get("/v1/onboarding")).json();
  expect(ob.seconds_to_first_run).toBeGreaterThanOrEqual(0);
  expect(ob.seconds_to_first_run).toBeLessThan(15 * 60);
  expect(ob.seconds_to_first_run).toBeLessThanOrEqual(Math.ceil((Date.now() - started) / 1000));

  // Inviting waits for the confirmed email.
  const invite = await page.request.post("/v1/members", {
    headers: { "X-Taskiem-Request": "1" },
    data: { email: `op-${started}@stores.test`, password: "correct horse battery", roles: ["operator"] },
  });
  expect(invite.status()).toBe(403);
});
