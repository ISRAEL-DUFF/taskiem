// Records what the web chat's page is answered while it loads, signed in to
// the demo world (web/e2e/demo), as the parity test's fixed answers
// (mobile/test/parity/fixtures/<name>/): both halves then show the same
// data, at a frozen moment, so a picture never changes with the clock.
//
//   node mobile/tool/capture.mjs <name> <path> [email]
//   EXPLORE=1 node mobile/tool/capture.mjs explore /chat
//   TOOLS=1 node mobile/tool/capture.mjs chat-tools "/chat?with=taskiem"
//   ORDER=1 node mobile/tool/capture.mjs chat-order "/chat?with=<Bayo's link>"
//   PRODUCT=1 node mobile/tool/capture.mjs chat-product "/chat?with=taskiem"
//   STAFF=1 node mobile/tool/capture.mjs chat-sell "/chat?with=taskiem"
//
// Needs the demo world running (serve-demo.sh, seed.ts) and Playwright.
import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
// Playwright from the web app, as video/recording/lib.mjs takes it.
const require = createRequire(path.join(here, "..", "..", "web", "package.json"));
const { chromium } = require("@playwright/test");

const [name, page, email = "ada@demo.test"] = process.argv.slice(2);
const base = process.env.DEMO_URL ?? "http://localhost:18090";
const out = path.join(here, "..", "test", "parity", "fixtures", name);
fs.rmSync(out, { recursive: true, force: true });
fs.mkdirSync(out, { recursive: true });
const b = await chromium.launch();
const ctx = await b.newContext({ viewport: { width: 390, height: 844 }, reducedMotion: "reduce", timezoneId: "UTC" });
const p = await ctx.newPage();
await p.goto(base + "/login");
await p.getByLabel("Email").fill(email);
await p.getByLabel("Password").fill("correct horse battery");
await p.getByRole("button", { name: "Sign in", exact: true }).click();
await p.waitForURL((u) => !u.pathname.startsWith("/login"));
const answers = {};
let n = 0;
p.on("response", async (r) => {
  const u = new URL(r.url());
  if (!u.pathname.startsWith("/v1/") || r.request().method() !== "GET" || u.pathname === "/v1/chat/stream") return;
  const key = u.pathname + u.search.replace(/after=[^&]*/, "after=*");
  try {
    const body = await r.body();
    const file = `${String(n++).padStart(3, "0")}${(r.headers()["content-type"] ?? "").startsWith("image/") ? ".img" : ".json"}`;
    fs.writeFileSync(path.join(out, file), body);
    answers[key] = { file, status: r.status(), type: r.headers()["content-type"] ?? "application/json" };
  } catch {
    // A response that went before its body was read: not kept.
  }
});
const at = new Date().toISOString();
await p.goto(base + page);
await p.waitForTimeout(4000);
// After it loads, what a person then does there (EXPLORE=1: Find a
// business opened, then "rice" searched), its answers kept too.
if (process.env.EXPLORE) {
  await p.getByTestId("new-chat").click();
  await p.waitForTimeout(4000);
  await p.getByTestId("explore-search").fill("rice");
  await p.waitForTimeout(3000);
}
// TOOLS=1: the tools panel opened (every tool's list read for its tile),
// then the Products sheet.
if (process.env.TOOLS) {
  await p.getByRole("button", { name: "Open tools" }).click();
  await p.waitForTimeout(4000);
  await p.getByTestId("tools-panel").getByRole("button", { name: /^Products/ }).click();
  await p.waitForTimeout(3000);
}
// ORDER=1: their shop opened from the tools panel, one item in the bag,
// and the review.
if (process.env.ORDER) {
  await p.getByRole("button", { name: "Open tools" }).click();
  await p.waitForTimeout(3000);
  await p.getByTestId("tools-panel").getByRole("button", { name: /^Their shop/ }).click();
  await p.waitForTimeout(4000);
  await p.getByRole("button", { name: /^One more / }).first().click();
  await p.getByRole("button", { name: "Review order" }).click();
  await p.waitForTimeout(3000);
}
// PRODUCT=1: Products from the tools panel, its first row's form, then
// Add product.
if (process.env.PRODUCT) {
  await p.getByRole("button", { name: "Open tools" }).click();
  await p.waitForTimeout(3000);
  await p.getByTestId("tools-panel").getByRole("button", { name: /^Products/ }).click();
  await p.waitForTimeout(3000);
  await p.getByTestId("tool-sheet").locator("li button").first().click();
  await p.waitForTimeout(4000);
}
// STAFF=1: Sell from the tools panel (Handover is staff's alone: the demo
// world's owner has none).
if (process.env.STAFF) {
  await p.getByRole("button", { name: "Open tools" }).click();
  await p.waitForTimeout(3000);
  await p.getByTestId("tools-panel").getByRole("button", { name: /^Sell/ }).click();
  await p.waitForTimeout(3000);
}
fs.writeFileSync(path.join(out, "answers.json"), JSON.stringify({ at, answers }, null, 1));
console.log(Object.keys(answers).length, "answers at", at);
await b.close();
