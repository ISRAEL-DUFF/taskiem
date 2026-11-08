// The app's own clips and the stills (video/README.md):
//   - clips: the phone alone, 6 to 10 s, silent and looping, for the app's
//     onboarding and its empty states;
//     a .webm on a clear background, and an .mp4 and a .gif on the paper;
//   - stills: each scene at its moment, 1920 × 1080 and 1080 × 1080, for
//     thumbnails, posters and the website.
//
// Which clips (scene, from, to) and which moments are in film.json.
//
//   node video/clips.mjs chat [--only clips|stills]

import { mkdir } from "node:fs/promises";
import { spawn } from "node:child_process";
import path from "node:path";
import { film, here } from "./film.mjs";

const [filmName, ...args] = process.argv.slice(2);
const f = await film(filmName);
const only = args.includes("--only") ? args[args.indexOf("--only") + 1] : null;
const paper = "0xefeae2"; // --paper in kit/kit.css

const clips = f.config.clips ?? [];
const stills = f.config.stills ?? {};

const run = (cmd, argv) =>
  new Promise((ok, fail) => {
    const p = spawn(cmd, argv, { stdio: ["ignore", "ignore", "inherit"] });
    p.on("close", (c) => (c === 0 ? ok() : fail(new Error(`${cmd} ${argv.join(" ")}: exit ${c}`))));
  });
const render = path.join(here, "render.mjs");
const probe = (file) =>
  new Promise((ok) => {
    let text = "";
    const p = spawn("ffprobe", ["-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=p=0", file]);
    p.stdout.on("data", (b) => (text += b));
    p.on("close", () => ok(text.trim()));
  });

if (only !== "stills") {
  const dir = path.join(f.out, "clips");
  await mkdir(dir, { recursive: true });
  for (const { name, scene, from, to } of clips) {
    const base = path.join(dir, `chat-${name}`);
    await run("node", [render, f.name, "scene", scene, "--phone", "--from", String(from), "--to", String(to), "--out", `${base}.webm`]);
    // On the paper, with a short fade at each end so the loop closes softly.
    const secs = (to - from) / 1000;
    const [w, h] = (await probe(`${base}.webm`)).split(",").map((n) => Math.ceil(Number(n) / 2) * 2);
    const onPaper = `[1:v][0:v]overlay=shortest=1:format=auto,fade=t=in:st=0:d=0.3:color=${paper},fade=t=out:st=${(secs - 0.3).toFixed(2)}:d=0.3:color=${paper},format=yuv420p`;
    await run("ffmpeg", ["-loglevel", "error", "-y", "-c:v", "libvpx-vp9", "-i", `${base}.webm`, "-f", "lavfi", "-i", `color=c=${paper}:s=${w}x${h}:r=30`, "-filter_complex", onPaper, "-c:v", "libx264", "-crf", "20", "-an", "-movflags", "+faststart", `${base}.mp4`]);
    await run("ffmpeg", ["-loglevel", "error", "-y", "-i", `${base}.mp4`, "-filter_complex", "fps=15,scale=320:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=128[p];[b][p]paletteuse=dither=sierra2_4a", "-loop", "0", `${base}.gif`]);
    console.log(`${base}: .webm .mp4 .gif (${secs} s)`);
  }
}

if (only !== "clips") {
  const dir = path.join(f.out, "stills");
  await mkdir(dir, { recursive: true });
  for (const [scene, ms] of Object.entries(stills)) {
    for (const size of ["16x9", "1x1"]) {
      await run("node", [render, f.name, "still", scene, String(ms), "--size", size, "--out", path.join(dir, `${scene}-${size}.png`)]);
    }
  }
  console.log(`${dir}: ${Object.keys(stills).length * 2} stills`);
}
