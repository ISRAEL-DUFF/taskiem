import { createServer, type Server } from "node:http";
import { expect, test } from "@playwright/test";

// A partner's page on a second origin embeds the builder as an element and
// the run list as an iframe; its server mints end-user tokens through the
// partner API (docs/embedding.md). serve.sh makes the E2E tenant a partner.

const taskiem = "http://127.0.0.1:18080";
const partnerPort = 18181;
const partnerOrigin = `http://127.0.0.1:${partnerPort}`;
const primary = "#7b2ff7";

let server: Server;
let partnerKey = "";
let app = "";
let sub = "";

async function call<T>(method: string, path: string, token: string, body?: unknown): Promise<T> {
  const res = await fetch(taskiem + path, {
    method,
    headers: { ...(token && { Authorization: `Bearer ${token}` }), "Content-Type": "application/json", "X-Taskiem-Request": "1" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = (await res.json()) as T;
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${JSON.stringify(data)}`);
  return data;
}

const page = (appID: string) => `<!doctype html>
<html><head><meta charset="utf-8"><title>Payrolla</title>
<script src="${taskiem}/embed/v1/taskiem.js"></script>
</head><body>
<h1>Payrolla</h1>
<taskiem-builder id="builder" app="${appID}"></taskiem-builder>
<iframe id="frame" title="Runs" src="${taskiem}/embed/${appID}/frame?view=runs" style="width:100%;height:640px;border:0"></iframe>
<script>
  window.events = [];
  const token = async (ttl) => (await (await fetch("/token?ttl=" + (ttl || 900))).json()).token;
  const el = document.getElementById("builder");
  for (const n of ["taskiem-loaded", "taskiem-published", "taskiem-run-started", "taskiem-run-completed", "taskiem-token-expiring"])
    el.addEventListener(n, () => window.events.push(n));
  // A new token before the current one expires: minted by our server.
  el.addEventListener("taskiem-token-expiring", async () => el.setToken(await token()));
  token(65).then((t) => el.setToken(t));

  const frame = document.getElementById("frame");
  const send = async () => frame.contentWindow.postMessage({ type: "taskiem:token", token: await token() }, "${taskiem}");
  window.addEventListener("message", (e) => {
    if (e.origin !== "${taskiem}" || e.source !== frame.contentWindow) return;
    window.events.push(e.data.type);
    if (e.data.type === "taskiem:ready" || e.data.type === "taskiem:token-expiring") send();
  });
</script>
</body></html>`;

test.beforeAll(async () => {
  const login = await call<{ token: string }>("POST", "/v1/auth/login", "", { email: "owner@e2e.test", password: "correct horse battery" });
  partnerKey = (await call<{ key: string }>("POST", "/v1/api-keys", login.token, { name: `partner ${Date.now()}`, permissions: ["partner.read", "partner.manage"] })).key;
  sub = (await call<{ id: string }>("POST", "/v1/partner/sub-tenants", partnerKey, { name: `Customer ${Date.now()}` })).id;
  app = (
    await call<{ id: string }>("POST", "/v1/partner/embed-apps", partnerKey, {
      name: `web ${Date.now()}`,
      allowed_origins: [partnerOrigin],
      branding: { colours: { primary, on_primary: "#ffffff" }, radius: "12px", mode: "light" },
      allowed_connectors: [],
      end_user_permissions: ["workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start"],
      white_label: true,
    })
  ).id;
  server = createServer((req, res) => {
    const url = new URL(req.url ?? "/", partnerOrigin);
    if (url.pathname === "/token") {
      // The partner's server mints; the partner key never reaches the browser.
      call<{ token: string }>("POST", `/v1/partner/embed-apps/${app}/tokens`, partnerKey, {
        end_user_id: "alice",
        sub_tenant: sub,
        permissions: ["workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start"],
        ttl: Number(url.searchParams.get("ttl")) || 900,
      }).then(
        (t) => res.writeHead(200, { "Content-Type": "application/json" }).end(JSON.stringify({ token: t.token })),
        (e: unknown) => res.writeHead(500).end(String(e)),
      );
      return;
    }
    res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" }).end(page(app));
  });
  await new Promise<void>((resolve) => server.listen(partnerPort, "127.0.0.1", resolve));
});

test.afterAll(() => new Promise<void>((resolve) => server.close(() => resolve())));

test("a partner page embeds the builder and the run view, themed, and runs a workflow", async ({ page }) => {
  // Only this app's origin may frame its frame page.
  const framePage = await page.request.get(`${taskiem}/embed/${app}/frame?view=runs`);
  expect(framePage.headers()["content-security-policy"]).toContain(`frame-ancestors ${partnerOrigin}`);
  expect(framePage.headers()["x-frame-options"]).toBeUndefined();
  expect((await page.request.get(`${taskiem}/login`)).headers()["content-security-policy"]).toContain("frame-ancestors 'none'");

  await page.goto(partnerOrigin + "/");
  const b = page.locator("taskiem-builder");
  await expect(b.getByRole("heading", { name: "Automations" })).toBeVisible();

  // The app's branding, as custom properties inside the shadow root; no
  // platform branding for a white-label app.
  const root = b.getByTestId("taskiem-root");
  await expect.poll(() => root.evaluate((el) => getComputedStyle(el).getPropertyValue("--accent").trim())).toBe(primary);
  expect(await root.evaluate((el) => getComputedStyle(el).getPropertyValue("--tk-radius").trim())).toBe("12px");
  await expect(b.getByTestId("taskiem-powered-by")).toHaveCount(0);
  // The partner page's own styles are untouched.
  expect(await page.evaluate(() => getComputedStyle(document.body).getPropertyValue("--accent"))).toBe("");

  // Build, publish and run inside the element.
  await b.getByLabel("New workflow name").fill("Embedded flow");
  await b.getByRole("button", { name: "Create" }).click();
  await expect(b.getByTestId("node-start")).toBeVisible();
  await b.getByRole("button", { name: "Publish" }).click();
  await expect(b.getByText("Version 1 is published.")).toBeVisible();
  await b.getByRole("button", { name: "Run", exact: true }).click();
  await b.getByRole("button", { name: "Start run" }).click();
  const run = b.getByTestId("taskiem-run");
  await expect(run.locator(".badge.completed").first()).toBeVisible({ timeout: 30_000 });

  // The first token was short-lived: the element asked for the next one.
  await expect.poll(() => page.evaluate(() => (window as unknown as { events: string[] }).events), { timeout: 20_000 }).toEqual(
    expect.arrayContaining(["taskiem-loaded", "taskiem-published", "taskiem-run-started", "taskiem-run-completed", "taskiem-token-expiring", "taskiem:ready", "taskiem:loaded"]),
  );

  // The iframe got its token by postMessage and shows the same run.
  const f = page.frameLocator("#frame");
  await expect(f.getByRole("heading", { name: "Runs" })).toBeVisible();
  await f.getByRole("button", { name: "Refresh" }).click();
  await f.getByRole("cell", { name: "Embedded flow" }).first().click();
  await expect(f.getByTestId("taskiem-run").locator(".badge.completed").first()).toBeVisible({ timeout: 30_000 });
  await expect.poll(() => f.getByTestId("taskiem-root").evaluate((el) => getComputedStyle(el).getPropertyValue("--accent").trim())).toBe(primary);
  expect(page.url()).not.toContain("tsk_eut_");
  expect(await page.locator("#frame").getAttribute("src")).not.toContain("tsk_eut_");
});
