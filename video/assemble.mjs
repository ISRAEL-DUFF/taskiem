// Joins the explainer's scenes into one film (video/README.md): renders each
// scene (scenes/<nn>-*.html, in order) with render.mjs, a few at a time,
// joins them, and writes the captions from script.md.
//
//   node video/assemble.mjs [--size 9x16] [--name animatic] [--jobs 3] [--keep] [--cut]
//
// --keep reuses a scene's video already in out/ (render it again by deleting it).

import { readdir, readFile, writeFile, stat } from "node:fs/promises";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const args = process.argv.slice(2);
const flag = (k, d) => {
  const i = args.indexOf(`--${k}`);
  return i >= 0 ? args[i + 1] : d;
};
const size = flag("size", "16x9");
const name = flag("name", "animatic");
const jobs = Number(flag("jobs", "3"));
const keep = args.includes("--keep");
const out = path.join(here, "out");

const run = (cmd, argv) =>
  new Promise((ok, fail) => {
    const p = spawn(cmd, argv, { stdio: ["ignore", "ignore", "inherit"] });
    p.on("close", (c) => (c === 0 ? ok() : fail(new Error(`${cmd} ${argv.join(" ")}: exit ${c}`))));
  });
const exists = (f) => stat(f).then(() => true, () => false);

const scenes = (await readdir(path.join(here, "scenes"))).filter((f) => /^\d\d-.+\.html$/.test(f)).sort().map((f) => f.replace(/\.html$/, ""));
const video = (s) => path.join(out, `${s}-${size}.mp4`);

// Render, a few scenes at once.
const queue = [...scenes];
await Promise.all(
  Array.from({ length: jobs }, async () => {
    for (let s = queue.shift(); s; s = queue.shift()) {
      if (keep && (await exists(video(s)))) continue;
      const t = Date.now();
      await run("node", [path.join(here, "render.mjs"), "scene", s, "--size", size]);
      console.log(`${s}: ${((Date.now() - t) / 1000).toFixed(0)} s`);
    }
  }),
);

// Each scene's length, for the joins and the captions.
const durations = [];
for (const s of scenes) {
  const d = await new Promise((ok) => {
    let text = "";
    const p = spawn("ffprobe", ["-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", video(s)]);
    p.stdout.on("data", (b) => (text += b));
    p.on("close", () => ok(Number(text.trim())));
  });
  durations.push(d);
}

// Join with a short dissolve between scenes (--cut for straight cuts).
const fade = args.includes("--cut") ? 0 : 0.4;
const film = path.join(out, `${name}-${size}.mp4`);
if (fade === 0) {
  const list = path.join(out, `${name}-${size}.txt`);
  await writeFile(list, scenes.map((s) => `file '${video(s)}'`).join("\n") + "\n");
  await run("ffmpeg", ["-loglevel", "error", "-y", "-f", "concat", "-safe", "0", "-i", list, "-c", "copy", "-movflags", "+faststart", film]);
} else {
  const inputs = scenes.flatMap((s) => ["-i", video(s)]);
  const parts = [];
  let last = "0:v";
  let offset = 0;
  for (let i = 1; i < scenes.length; i++) {
    offset += durations[i - 1] - fade;
    const outLabel = i === scenes.length - 1 ? "v" : `x${i}`;
    parts.push(`[${last}][${i}:v]xfade=transition=fade:duration=${fade}:offset=${offset.toFixed(3)}[${outLabel}]`);
    last = outLabel;
  }
  await run("ffmpeg", ["-loglevel", "error", "-y", ...inputs, "-filter_complex", parts.join(";"), "-map", "[v]", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-crf", "18", "-movflags", "+faststart", film]);
}

// Captions: each scene's caption from script.md, for as long as its scene
// lasts in the film.
const script = await readFile(path.join(here, "script.md"), "utf8");
const caption = {};
for (const line of script.split("\n")) {
  const m = /^\| (\d+) \| [^|]+ \| [^|]+ \| [^|]+ \| (.+) \|$/.exec(line);
  if (m) caption[Number(m[1])] = m[2].trim();
}
const clock = (t) => {
  const ms = Math.round(t * 1000);
  const p = (n, w = 2) => String(n).padStart(w, "0");
  return `${p(Math.floor(ms / 3600000))}:${p(Math.floor(ms / 60000) % 60)}:${p(Math.floor(ms / 1000) % 60)},${p(ms % 1000, 3)}`;
};
let at = 0;
const srt = scenes
  .map((s, i) => {
    const from = at;
    at += durations[i] - (i < scenes.length - 1 ? fade : 0);
    const words = caption[Number(s.slice(0, 2))] ?? "";
    return `${i + 1}\n${clock(from + 0.3)} --> ${clock(at - 0.2)}\n${words}\n`;
  })
  .join("\n");
await writeFile(path.join(out, `${name}-${size}.srt`), srt);
console.log(`${film} (${at.toFixed(1)} s)`);
