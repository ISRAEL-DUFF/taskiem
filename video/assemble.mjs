// Joins the explainer's scenes into one film (video/README.md): renders each
// scene (scenes/<nn>-*.html, in order) with render.mjs, a few at a time,
// joins them, writes the captions from script.md, and lays the music
// (music/compose.py, written to the film's own timeline) under it.
//
//   node video/assemble.mjs [--size 9x16|1x1] [--name film] [--jobs 3] [--keep] [--cut] [--silent]
//   node video/assemble.mjs --scenes 13,14,15,16 --title "Haggle *in the chat.*" --name short-deals
//
// --keep reuses a scene's video already in out/ (render it again by deleting it).
// --scenes takes some of the scenes, by number; --title puts a short's
// title card (scenes/00-title.html) in front of them. --music other.wav lays
// another track instead of the one written for the film; --silent, none.

import { readdir, readFile, writeFile, stat, rename, rm } from "node:fs/promises";
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
const name = flag("name", "film");
const title = flag("title");
const jobs = Number(flag("jobs", "3"));
const keep = args.includes("--keep");
const out = path.join(here, "out");

const run = (cmd, argv) =>
  new Promise((ok, fail) => {
    const p = spawn(cmd, argv, { stdio: ["ignore", "ignore", "inherit"] });
    p.on("close", (c) => (c === 0 ? ok() : fail(new Error(`${cmd} ${argv.join(" ")}: exit ${c}`))));
  });
const exists = (f) => stat(f).then(() => true, () => false);

const wanted = flag("scenes")?.split(",").map(Number);
const scenes = (await readdir(path.join(here, "scenes")))
  .filter((f) => /^\d\d-.+\.html$/.test(f) && !f.startsWith("00-"))
  .filter((f) => !wanted || wanted.includes(Number(f.slice(0, 2))))
  .sort()
  .map((f) => f.replace(/\.html$/, ""));
const video = (s) => path.join(out, s === "00-title" ? `${name}-title-${size}.mp4` : `${s}-${size}.mp4`);
if (title) {
  scenes.unshift("00-title");
  await run("node", [path.join(here, "render.mjs"), "scene", "00-title", "--size", size, "--title", title, "--out", video("00-title")]);
}

// Render, a few scenes at once.
const queue = [...scenes];
await Promise.all(
  Array.from({ length: jobs }, async () => {
    for (let s = queue.shift(); s; s = queue.shift()) {
      if (s === "00-title" || (keep && (await exists(video(s))))) continue;
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
const picture = path.join(out, `${name}-${size}.picture.mp4`);
if (fade === 0) {
  const list = path.join(out, `${name}-${size}.txt`);
  await writeFile(list, scenes.map((s) => `file '${video(s)}'`).join("\n") + "\n");
  await run("ffmpeg", ["-loglevel", "error", "-y", "-f", "concat", "-safe", "0", "-i", list, "-c", "copy", "-movflags", "+faststart", picture]);
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
  await run("ffmpeg", ["-loglevel", "error", "-y", ...inputs, "-filter_complex", parts.join(";"), "-map", "[v]", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-crf", "18", "-movflags", "+faststart", picture]);
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
    const words = s === "00-title" ? title.replaceAll("*", "") : (caption[Number(s.slice(0, 2))] ?? "");
    return `${i + 1}\n${clock(from + 0.3)} --> ${clock(at - 0.2)}\n${words}\n`;
  })
  .join("\n");
await writeFile(path.join(out, `${name}-${size}.srt`), srt);

// The music, written to this film's timeline, mixed to -16 LUFS (what
// phones and the web expect) and laid under the picture.
if (args.includes("--silent")) {
  await rename(picture, film);
} else {
  const timeline = path.join(out, `${name}-${size}.timeline.json`);
  const wav = path.join(out, `${name}-${size}.wav`);
  let start = 0;
  const parts = scenes.map((s, i) => {
    const part = { scene: s, start: Number(start.toFixed(3)), duration: durations[i] };
    start += durations[i] - fade;
    return part;
  });
  await writeFile(timeline, JSON.stringify({ length: at, scenes: parts }, null, 1));
  if (flag("music")) await run("ffmpeg", ["-loglevel", "error", "-y", "-i", flag("music"), "-af", `afade=t=out:st=${Math.max(0, at - 2.5).toFixed(2)}:d=2.5`, "-t", at.toFixed(3), wav]);
  else await run("python3", [path.join(here, "music", "compose.py"), timeline, wav]);
  // Two passes: measure, then one gain for the whole track (no pumping).
  const target = "I=-16:TP=-1.5:LRA=11";
  const measured = await new Promise((ok, fail) => {
    let text = "";
    const p = spawn("ffmpeg", ["-hide_banner", "-i", wav, "-af", `loudnorm=${target}:print_format=json`, "-f", "null", "-"]);
    p.stderr.on("data", (b) => (text += b));
    p.on("close", (c) => (c === 0 ? ok(JSON.parse(text.slice(text.lastIndexOf("{")))) : fail(new Error("loudnorm measure failed"))));
  });
  const m = `measured_I=${measured.input_i}:measured_TP=${measured.input_tp}:measured_LRA=${measured.input_lra}:measured_thresh=${measured.input_thresh}:offset=${measured.target_offset}:linear=true`;
  await run("ffmpeg", ["-loglevel", "error", "-y", "-i", picture, "-i", wav, "-filter_complex", `[1:a]loudnorm=${target}:${m},aresample=48000[a]`, "-map", "0:v", "-map", "[a]", "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-shortest", "-movflags", "+faststart", film]);
  await rm(picture);
}
console.log(`${film} (${at.toFixed(1)} s)`);
