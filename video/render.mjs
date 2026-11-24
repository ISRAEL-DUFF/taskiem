// Renders a film's scenes (video/README.md): a still at one moment, or a
// scene as video, frame by frame, with the repository's own Inter so it
// needs no network.
//
//   node video/render.mjs chat still 03-briefing 7000 [--size 9x16] [--out file.png]
//   node video/render.mjs chat scene 03-briefing [--size 9x16|1x1] [--fps 30] [--out file.mp4]
//   node video/render.mjs chat scene 00-title --title "Haggle *in the chat.*" --out file.mp4
//   node video/render.mjs chat scene 06-tools --phone --from 0 --to 9000 --out clip.webm
//
// --phone renders the phone alone, on a clear background (kept in a .webm),
// for the app's own clips; --from and --to take part of a scene.
//
// It uses Playwright's Chromium from the web app's dependencies and ffmpeg.

import { readFile, rm } from "node:fs/promises";
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
import path from "node:path";
import { film, here } from "./film.mjs";

const repo = path.dirname(here);
const require = createRequire(path.join(repo, "web", "package.json"));
const { chromium } = require("@playwright/test");

const [filmName, mode, name, ...rest] = process.argv.slice(2);
const flag = (k, d) => {
  const i = rest.indexOf(`--${k}`);
  return i >= 0 ? rest[i + 1] : d;
};
if (!["still", "scene"].includes(mode) || !name) {
  console.error("usage: node video/render.mjs <film> still <scene> <ms> | <film> scene <scene> [--size 9x16|1x1] [--fps 30] [--out file]");
  process.exit(2);
}
const size = flag("size", "16x9");
const [w, h] = { "9x16": [1080, 1920], "1x1": [1080, 1080] }[size] ?? [1920, 1080];
const fps = Number(flag("fps", "30"));
const f = await film(filmName);
const outDir = f.out;

// Inter from the web app's own dependency, inlined.
const inter = await readFile(require.resolve("@fontsource-variable/inter/files/inter-latin-wght-normal.woff2"));
const fontCSS = `@font-face{font-family:"Inter";src:url(data:font/woff2;base64,${inter.toString("base64")}) format("woff2");font-weight:100 900}`;

const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || undefined });
const page = await browser.newPage({ viewport: { width: w, height: h }, deviceScaleFactor: 1 });
const url = pathToFileURL(f.page(name));
url.search = `render&size=${size}`;
// --title "line" for the shorts' title card (kit/title.html, scene 00-title).
if (flag("title")) url.searchParams.set("title", flag("title"));
await page.goto(url.href);
await page.addStyleTag({ content: fontCSS });
await page.evaluate(() => document.fonts.ready);
const phone = rest.includes("--phone");
if (phone) await page.addStyleTag({ content: "html, body, .stage { background: transparent !important; }" });
const stage = page.locator(phone ? ".phone" : ".stage").first();
const shot = (o) => stage.screenshot({ type: "png", omitBackground: phone, ...o });

async function frameAt(ms) {
  await page.evaluate((t) => window.seek(t), ms);
  return shot();
}

if (mode === "still") {
  const ms = Number(rest[0] ?? 0);
  const out = flag("out", path.join(outDir, `${name}-${size}-${ms}.png`));
  await page.evaluate((t) => window.seek(t), ms);
  await shot({ path: out });
  console.log(out);
} else {
  const from = Number(flag("from", "0"));
  const duration = Number(flag("to", 0)) || (await page.evaluate(() => (window.scene && window.scene.duration) || 8000));
  const out = flag("out", path.join(outDir, `${name}-${size}.mp4`));
  await rm(out, { force: true });
  // PNG frames piped straight into ffmpeg: nothing is left on disk. A .webm
  // keeps the clear background.
  const codec = out.endsWith(".webm")
    ? ["-c:v", "libvpx-vp9", "-pix_fmt", "yuva420p", "-b:v", "0", "-crf", "30", "-auto-alt-ref", "0"]
    : ["-c:v", "libx264", "-pix_fmt", "yuv420p", "-crf", "18", "-movflags", "+faststart"];
  const ff = spawn("ffmpeg", ["-loglevel", "error", "-y", "-f", "image2pipe", "-framerate", String(fps), "-i", "-", ...codec, out], {
    stdio: ["pipe", "inherit", "inherit"],
  });
  const frames = Math.ceil(((duration - from) / 1000) * fps);
  for (let i = 0; i <= frames; i++) {
    const png = await frameAt(from + (i * 1000) / fps);
    if (!ff.stdin.write(png)) await new Promise((r) => ff.stdin.once("drain", r));
  }
  ff.stdin.end();
  await new Promise((r, j) => ff.on("close", (c) => (c === 0 ? r() : j(new Error(`ffmpeg exited ${c}`)))));
  console.log(out);
}
await browser.close();
