// Joins the recording (video/recording/README.md): a title card before each
// part (the explainer kit's, kit/title.html, at 9:16) and the acts recorded
// by tour.mjs, framed at 1080 × 1920 on the paper colour, into
// out/chat-app-recording.mp4.
//
//   node video/recording/build.mjs [--out file.mp4]

import { existsSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { FILM } from "./order.mjs";
import { OUT } from "./lib.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const args = process.argv.slice(2);
const out = args.includes("--out") ? args[args.indexOf("--out") + 1] : path.join(here, "..", "out", "chat-app-recording.mp4");
const run = (cmd, argv) => {
  const r = spawnSync(cmd, argv, { stdio: ["ignore", "ignore", "inherit"] });
  if (r.status !== 0) throw new Error(`${cmd} ${argv.slice(0, 4).join(" ")}…: exit ${r.status}`);
};

const clips = [];
FILM.forEach((part, i) => {
  const card = path.join(OUT, `title${i}.mp4`);
  run("node", [path.join(here, "..", "render.mjs"), "chat", "scene", "00-title", "--size", "9x16", "--title", part.title, "--out", card]);
  clips.push(card);
  for (const a of part.acts ?? []) {
    const f = path.join(OUT, `${a}.mp4`);
    if (!existsSync(f)) throw new Error(`${a} is not recorded yet: node video/recording/tour.mjs ${a}`);
    clips.push(f);
  }
});

// Each piece framed alike (the phone's screen at full height, centred on
// the paper), then joined end to end.
const scale = clips.map((_, i) => `[${i}:v]scale=-2:1920,pad=1080:1920:(ow-iw)/2:0:color=0xefeae2,fps=30,setsar=1,format=yuv420p[v${i}]`);
const join = `${clips.map((_, i) => `[v${i}]`).join("")}concat=n=${clips.length}:v=1:a=0[v]`;
run("ffmpeg", ["-loglevel", "error", "-y", ...clips.flatMap((c) => ["-i", c]), "-filter_complex", `${scale.join(";")};${join}`, "-map", "[v]", "-c:v", "libx264", "-crf", "20", "-movflags", "+faststart", out]);
console.log(out);
