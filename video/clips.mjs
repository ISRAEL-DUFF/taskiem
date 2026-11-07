// The app's own clips and the stills (video/README.md):
//   - clips: the phone alone, 6 to 10 s, silent and looping, for onboarding
//     (when Chat tools is turned on) and for a tool sheet that is still empty;
//     a .webm on a clear background, and an .mp4 and a .gif on the paper;
//   - stills: each scene at its moment, 1920 × 1080 and 1080 × 1080, for
//     thumbnails, posters and the website.
//
//   node video/clips.mjs [--only clips|stills]

import { mkdir } from "node:fs/promises";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const args = process.argv.slice(2);
const only = args.includes("--only") ? args[args.indexOf("--only") + 1] : null;
const paper = "0xefeae2"; // --paper in kit/kit.css

// name, scene, from ms, to ms: where each clip is used is in its name.
const clips = [
  ["briefing", "03-briefing", 0, 9000],
  ["tag", "05-picker", 1000, 9500],
  ["tools", "06-tools", 0, 10000],
  ["diary", "07-diary", 0, 6000],
  ["orders", "08-trade", 1500, 11500],
  ["sell", "10-staff", 0, 8000],
  ["offer", "13-offer", 2000, 10000],
];
// Each scene's moment for a still (ms).
const stills = {
  "01-open": 3000, "02-intro": 3000, "03-briefing": 7000, "04-alerts": 4600, "05-picker": 3200, "06-tools": 7500,
  "07-diary": 4000, "08-trade": 11000, "09-both": 5000, "10-staff": 7000, "11-dark": 3000, "12-explore": 6000,
  "13-offer": 6500, "14-counter": 8600, "15-deal": 5000, "16-end": 5000,
};

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
  const dir = path.join(here, "out", "clips");
  await mkdir(dir, { recursive: true });
  for (const [name, scene, from, to] of clips) {
    const base = path.join(dir, `chat-${name}`);
    await run("node", [render, "scene", scene, "--phone", "--from", String(from), "--to", String(to), "--out", `${base}.webm`]);
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
  const dir = path.join(here, "out", "stills");
  await mkdir(dir, { recursive: true });
  for (const [scene, ms] of Object.entries(stills)) {
    for (const size of ["16x9", "1x1"]) {
      await run("node", [render, "still", scene, String(ms), "--size", size, "--out", path.join(dir, `${scene}-${size}.png`)]);
    }
  }
  console.log(`${dir}: ${Object.keys(stills).length * 2} stills`);
}
