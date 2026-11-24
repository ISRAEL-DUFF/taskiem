// How-to videos (video/howto/README.md): the real app on the demo world,
// recorded act by act with lib.mjs, with a caption under the phone for each
// step. An act calls say() as it goes; build() joins the title cards and
// the acts at 1080 × 1920, burns the captions in, and writes an .srt beside
// the MP4 for players that show their own.
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { execFileSync, spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { record, sleep, OUT } from "../recording/lib.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
export const HOWTO_OUT = path.join(here, "..", "out", "howto");
mkdirSync(HOWTO_OUT, { recursive: true });

// The phone's screen sits at the top of the frame; the caption band below.
const W = 1080, H = 1920, PHONE_H = 1500, PHONE_TOP = 36;

let current = null;

/** Show a caption from now until the next one, then wait so it can be read. */
export async function say(text, hold = 2600) {
  if (!current) throw new Error("say() outside an act");
  current.cues.push({ t: (Date.now() - current.t0) / 1000, text });
  if (hold) await sleep(hold);
}

/** Record one act: who it is (the line over the caption) and its steps. */
export async function act(page, name, who, fn) {
  const cues = { who, cues: [] };
  await record(page, name, async () => {
    current = { ...cues, t0: Date.now() };
    cues.cues = current.cues;
    try {
      await fn();
    } finally {
      current = null;
    }
  });
  writeFileSync(path.join(OUT, `${name}.cues.json`), JSON.stringify(cues, null, 2));
}

const dur = (f) => Number(execFileSync("ffprobe", ["-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", f], { encoding: "utf8" }).trim());
const assTime = (s) => {
  const cs = Math.max(0, Math.round(s * 100));
  const h = Math.floor(cs / 360000), m = Math.floor((cs % 360000) / 6000), sec = Math.floor((cs % 6000) / 100);
  return `${h}:${String(m).padStart(2, "0")}:${String(sec).padStart(2, "0")}.${String(cs % 100).padStart(2, "0")}`;
};
const srtTime = (s) => {
  const ms = Math.max(0, Math.round(s * 1000));
  const h = Math.floor(ms / 3600000), m = Math.floor((ms % 3600000) / 60000), sec = Math.floor((ms % 60000) / 1000);
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}:${String(sec).padStart(2, "0")},${String(ms % 1000).padStart(3, "0")}`;
};
const assText = (t) => t.replace(/\\/g, "\\\\").replace(/\{/g, "(").replace(/\}/g, ")").replace(/\n/g, "\\N");

// Ink #0e1a14, pine #0d3b2e and muted #5b6b63 in ASS's &HBBGGRR.
const ASS_HEAD = `[Script Info]
ScriptType: v4.00+
PlayResX: ${W}
PlayResY: ${H}
WrapStyle: 0

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Who,Inter,34,&H002E3B0D,&H002E3B0D,&H00000000,&H00000000,-1,0,0,0,100,100,0,0,1,0,0,8,70,70,${PHONE_TOP + PHONE_H + 26},1
Style: Cap,Inter,48,&H00141A0E,&H00141A0E,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,0,0,8,70,70,${PHONE_TOP + PHONE_H + 82},1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`;

/**
 * Join a how-to: parts are { title } (a title card, words in *stars* in
 * pine) or { act } (an act recorded with act()). Writes
 * out/howto/<slug>.mp4 and <slug>.srt.
 */
export function build(slug, parts) {
  const run = (cmd, argv) => {
    const r = spawnSync(cmd, argv, { stdio: ["ignore", "ignore", "inherit"] });
    if (r.status !== 0) throw new Error(`${cmd} ${argv.slice(0, 4).join(" ")}…: exit ${r.status}`);
  };
  const pieces = [];
  const srt = [];
  let at = 0;
  parts.forEach((p, i) => {
    if (p.title) {
      const card = path.join(HOWTO_OUT, `${slug}-title${i}.mp4`);
      run("node", [path.join(here, "..", "render.mjs"), "chat", "scene", "00-title", "--size", "9x16", "--title", p.title, "--out", card]);
      pieces.push(card);
      at += dur(card);
      return;
    }
    const clip = path.join(OUT, `${p.act}.mp4`);
    const cuesFile = path.join(OUT, `${p.act}.cues.json`);
    if (!existsSync(clip) || !existsSync(cuesFile)) throw new Error(`${p.act} is not recorded yet`);
    const { who, cues } = JSON.parse(readFileSync(cuesFile, "utf8"));
    const len = dur(clip);
    const lines = [`Dialogue: 0,${assTime(0)},${assTime(len)},Who,,0,0,0,,${assText(who)}`];
    cues.forEach((c, k) => {
      const end = k + 1 < cues.length ? cues[k + 1].t : len;
      lines.push(`Dialogue: 0,${assTime(c.t)},${assTime(end)},Cap,,0,0,0,,${assText(c.text)}`);
      srt.push({ from: at + c.t, to: at + end, text: c.text });
    });
    const ass = path.join(HOWTO_OUT, `${slug}-${p.act}.ass`);
    writeFileSync(ass, ASS_HEAD + lines.join("\n") + "\n");
    const framed = path.join(HOWTO_OUT, `${slug}-${p.act}.mp4`);
    run("ffmpeg", [
      "-loglevel", "error", "-y", "-i", clip,
      "-vf", `scale=-2:${PHONE_H},pad=${W}:${H}:(ow-iw)/2:${PHONE_TOP}:color=0xefeae2,fps=30,setsar=1,subtitles=${ass},format=yuv420p`,
      "-c:v", "libx264", "-crf", "20", framed,
    ]);
    pieces.push(framed);
    at += len;
  });
  const out = path.join(HOWTO_OUT, `${slug}.mp4`);
  const scale = pieces.map((_, i) => `[${i}:v]scale=${W}:${H},fps=30,setsar=1,format=yuv420p[v${i}]`);
  const join = `${pieces.map((_, i) => `[v${i}]`).join("")}concat=n=${pieces.length}:v=1:a=0[v]`;
  run("ffmpeg", ["-loglevel", "error", "-y", ...pieces.flatMap((c) => ["-i", c]), "-filter_complex", `${scale.join(";")};${join}`, "-map", "[v]", "-c:v", "libx264", "-crf", "20", "-movflags", "+faststart", out]);
  writeFileSync(path.join(HOWTO_OUT, `${slug}.srt`), srt.map((c, k) => `${k + 1}\n${srtTime(c.from)} --> ${srtTime(c.to)}\n${c.text}\n`).join("\n"));
  return out;
}
