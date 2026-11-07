// Renders the explainer's scenes (video/README.md): a still at one moment,
// or a scene as video, frame by frame, with the repository's own Inter so
// it needs no network.
//
//   node video/render.mjs still 03-briefing 7000 [--size 9x16] [--out file.png]
//   node video/render.mjs scene 03-briefing [--size 9x16] [--fps 30] [--out file.mp4]
//
// It uses Playwright's Chromium from the web app's dependencies and ffmpeg.

import { mkdir, readFile, rm } from "node:fs/promises";
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { fileURLToPath, pathToFileURL } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.dirname(here);
const require = createRequire(path.join(repo, "web", "package.json"));
const { chromium } = require("@playwright/test");

const [mode, name, ...rest] = process.argv.slice(2);
const flag = (k, d) => {
  const i = rest.indexOf(`--${k}`);
  return i >= 0 ? rest[i + 1] : d;
};
if (!["still", "scene"].includes(mode) || !name) {
  console.error("usage: node video/render.mjs still <scene> <ms> | scene <scene> [--size 9x16] [--fps 30] [--out file]");
  process.exit(2);
}
const size = flag("size", "16x9");
const [w, h] = size === "9x16" ? [1080, 1920] : [1920, 1080];
const fps = Number(flag("fps", "30"));
const outDir = path.join(here, "out");
await mkdir(outDir, { recursive: true });

// Inter from the web app's own dependency, inlined.
const inter = await readFile(require.resolve("@fontsource-variable/inter/files/inter-latin-wght-normal.woff2"));
const fontCSS = `@font-face{font-family:"Inter";src:url(data:font/woff2;base64,${inter.toString("base64")}) format("woff2");font-weight:100 900}`;

const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || undefined });
const page = await browser.newPage({ viewport: { width: w, height: h }, deviceScaleFactor: 1 });
const url = pathToFileURL(path.join(here, "scenes", `${name}.html`));
url.search = `render&size=${size}`;
await page.goto(url.href);
await page.addStyleTag({ content: fontCSS });
await page.evaluate(() => document.fonts.ready);
const stage = page.locator(".stage");

async function frameAt(ms) {
  await page.evaluate((t) => window.seek(t), ms);
  return stage.screenshot({ type: "png" });
}

if (mode === "still") {
  const ms = Number(rest[0] ?? 0);
  const out = flag("out", path.join(outDir, `${name}-${size}-${ms}.png`));
  await page.evaluate((t) => window.seek(t), ms);
  await stage.screenshot({ path: out, type: "png" });
  console.log(out);
} else {
  const duration = await page.evaluate(() => (window.scene && window.scene.duration) || 8000);
  const out = flag("out", path.join(outDir, `${name}-${size}.mp4`));
  await rm(out, { force: true });
  // PNG frames piped straight into ffmpeg: nothing is left on disk.
  const ff = spawn("ffmpeg", ["-loglevel", "error", "-y", "-f", "image2pipe", "-framerate", String(fps), "-i", "-", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-crf", "18", "-movflags", "+faststart", out], {
    stdio: ["pipe", "inherit", "inherit"],
  });
  const frames = Math.ceil((duration / 1000) * fps);
  for (let i = 0; i <= frames; i++) {
    const png = await frameAt((i * 1000) / fps);
    if (!ff.stdin.write(png)) await new Promise((r) => ff.stdin.once("drain", r));
  }
  ff.stdin.end();
  await new Promise((r, j) => ff.on("close", (c) => (c === 0 ? r() : j(new Error(`ffmpeg exited ${c}`)))));
  console.log(out);
}
await browser.close();
