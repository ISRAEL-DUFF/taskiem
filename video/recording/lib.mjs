// Recording helpers (video/recording/README.md): sign in off camera, record
// a page's screen through Chrome's screencast (in real time), show where
// taps land, and time each action from the tap to what it brings.
import { createRequire } from "node:module";
import { mkdirSync, writeFileSync, rmSync, appendFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { Buffer } from "node:buffer";
import { performance } from "node:perf_hooks";
import { setTimeout } from "node:timers";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const require = createRequire(path.join(here, "..", "..", "web", "package.json"));
const { chromium } = require("@playwright/test");

// The demo world (web/e2e/demo/serve-demo.sh, then seed.ts).
export const BASE = process.env.DEMO_URL ?? "http://localhost:18090";
export const OUT = path.join(here, "..", "out", "recording");
mkdirSync(OUT, { recursive: true });
export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

let browser;
export async function launch() {
  browser ??= await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || undefined });
  return browser;
}

// A tap shows as a soft lime ring where it lands (the recording only).
const taps = `addEventListener("pointerdown", (e) => {
  const d = document.createElement("div");
  d.style.cssText = "position:fixed;z-index:2147483647;pointer-events:none;left:" + (e.clientX - 22) + "px;top:" + (e.clientY - 22) + "px;width:44px;height:44px;border-radius:50%;background:rgba(200,241,105,.45);box-shadow:0 0 0 2px rgba(13,59,46,.35);transition:transform .45s ease-out,opacity .45s ease-out";
  document.documentElement.appendChild(d);
  requestAnimationFrame(() => { d.style.transform = "scale(1.6)"; d.style.opacity = "0"; });
  setTimeout(() => d.remove(), 600);
}, true);`;

export async function person(who, { dark = false } = {}) {
  const b = await launch();
  const ctx = await b.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, hasTouch: true, isMobile: true, colorScheme: dark ? "dark" : "light", timezoneId: "Africa/Lagos", locale: "en-NG" });
  await ctx.addInitScript(taps);
  const page = await ctx.newPage();
  await page.goto(BASE + "/login");
  await page.getByLabel("Email").fill(`${who}@demo.test`);
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.waitForURL((u) => !u.pathname.startsWith("/login"), { timeout: 20000 });
  return page;
}

// Timings: from the action to what it should bring, in ms.
export const timings = [];
export async function timed(label, act, done) {
  const t = performance.now();
  await act();
  await done();
  const ms = Math.round(performance.now() - t);
  timings.push({ label, ms });
  appendFileSync(OUT + "/timings.jsonl", JSON.stringify({ label, ms }) + "\n");
  return ms;
}

// Record a page while fn runs: JPEG frames with their times, made into a
// 30 fps MP4 that holds each frame for as long as it was on screen.
export async function record(page, name, fn) {
  const dir = `${OUT}/${name}-frames`;
  rmSync(dir, { recursive: true, force: true });
  mkdirSync(dir, { recursive: true });
  const cdp = await page.context().newCDPSession(page);
  const frames = [];
  cdp.on("Page.screencastFrame", async ({ data, metadata, sessionId }) => {
    const f = `${dir}/${String(frames.length).padStart(5, "0")}.jpg`;
    writeFileSync(f, Buffer.from(data, "base64"));
    frames.push({ f, t: metadata.timestamp });
    await cdp.send("Page.screencastFrameAck", { sessionId }).catch(() => {});
  });
  await cdp.send("Page.startScreencast", { format: "jpeg", quality: 92, maxWidth: 780, maxHeight: 1688, everyNthFrame: 1 });
  let error;
  try {
    await fn();
  } catch (e) {
    error = e;
    await page.screenshot({ path: `${OUT}/${name}-FAILED.png` }).catch(() => {});
  }
  await sleep(400);
  const end = Date.now() / 1000;
  await cdp.send("Page.stopScreencast").catch(() => {});
  if (frames.length) {
    const list = frames.map((fr, i) => `file '${fr.f}'\nduration ${Math.max(0.001, (i + 1 < frames.length ? frames[i + 1].t : end) - fr.t).toFixed(4)}`);
    list.push(`file '${frames.at(-1).f}'`);
    writeFileSync(`${dir}/list.txt`, list.join("\n") + "\n");
    execFileSync("ffmpeg", ["-loglevel", "error", "-y", "-f", "concat", "-safe", "0", "-i", `${dir}/list.txt`, "-vf", "fps=30,scale=780:1688,format=yuv420p", "-c:v", "libx264", "-crf", "18", `${OUT}/${name}.mp4`]);
  }
  if (error) throw error;
  return `${OUT}/${name}.mp4`;
}
