import { createServer, type Server } from "node:http";
import { expect, test } from "@playwright/test";

// Linking a WhatsApp number under Account: the code goes out through a
// fake Graph API on the port serve.sh points the platform number at.

const graphPort = 12626;
const sent: { to: string; type: string; template?: { name: string; components: { type: string; parameters: { text?: string }[] }[] } }[] = [];
let graph: Server;

test.beforeAll(async () => {
  graph = createServer((req, res) => {
    let body = "";
    req.on("data", (c: Buffer) => (body += c.toString("utf8")));
    req.on("end", () => {
      if (req.method !== "POST" || req.url !== "/v25.0/106540352242922/messages" || req.headers.authorization !== "Bearer EAAe2e") {
        res.writeHead(404).end();
        return;
      }
      const m = JSON.parse(body) as (typeof sent)[number];
      sent.push(m);
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ messaging_product: "whatsapp", contacts: [{ input: m.to, wa_id: m.to.slice(1) }], messages: [{ id: `wamid.e2e${sent.length}` }] }));
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
  await expect(page.getByRole("heading", { name: "Workflows" })).toBeVisible();
  const added = await page.request.post("/v1/members", { headers: { "X-Taskiem-Request": "1" }, data: { email, password: "correct horse battery", roles: ["viewer"] } });
  expect(added.status()).toBe(201);
  await page.request.post("/v1/auth/logout", { headers: { "X-Taskiem-Request": "1" } });
  await page.context().clearCookies();

  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Workflows" })).toBeVisible();

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
