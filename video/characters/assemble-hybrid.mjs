// Build the chat film's hybrid cut: the app scenes, the Flow character
// cutaways (C1 to C8) and the narrator, with music ducked under the voice.
//
//   node video/characters/assemble-hybrid.mjs --clips <dir with C1.mp4..C8.mp4> --narration <audio file>
//
// Needs the app scenes rendered first (node video/assemble.mjs chat --name explainer),
// which leaves out/chat/<scene>-16x9.mp4. The narrator's line times come from
// narration/lines.json (tools/align-narration.py makes it from a transcript).
// Writes out/chat/hybrid-16x9.mp4. Straight cuts, so the plan's timeline holds.
import { spawnSync } from "node:child_process";
import { readFileSync, writeFileSync, mkdirSync, readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.join(here, "..");
const out = path.join(root, "out", "chat");
const args = process.argv.slice(2);
const flag = (n) => { const i = args.indexOf(`--${n}`); return i < 0 ? null : args[i + 1]; };
const clipsDir = flag("clips"), narration = flag("narration");
if (!clipsDir || !narration) { console.error("usage: --clips <dir> --narration <file>"); process.exit(2); }
const W = 1920, H = 1080;
const tmp = path.join(out, "hybrid-tmp"); mkdirSync(tmp, { recursive: true });

const sh = (cmd, argv, opts = {}) => {
  const r = spawnSync(cmd, argv, { encoding: "utf8", ...opts });
  if (r.status !== 0) throw new Error(`${cmd} failed:\n${(r.stderr || "").slice(-1500)}`);
  return r.stdout;
};
const dur = (f) => Number(sh("ffprobe", ["-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", f]).trim());
const find = (re) => { const f = readdirSync(clipsDir).find((n) => re.test(n)); if (!f) throw new Error(`missing clip ${re}`); return path.join(clipsDir, f); };

// Order of the cut (the plan's timeline): a cutaway goes before its scene.
const ORDER = ["C1", "01", "02", "03", "04", "C2", "05", "06", "07", "C3", "08", "C4", "09", "C5", "10", "11", "C6", "12", "C7", "13", "14", "15", "C8", "16"];
// Flow put a text label on these two; a patch over each hides it.
const PATCH = { C4: "delogo=x=40:y=28:w=222:h=72", C7: "delogo=x=6:y=6:w=170:h=72" };

const scenes = Object.fromEntries(readdirSync(out).filter((n) => /^\d\d-.*-16x9\.mp4$/.test(n)).map((n) => [n.slice(0, 2), path.join(out, n)]));
const segs = []; const timeline = []; const sceneStart = {};
let t = 0;
for (const id of ORDER) {
  const seg = path.join(tmp, `${id}.mp4`);
  const vf = (PATCH[id] ? PATCH[id] + "," : "") + `scale=${W}:${H}:flags=lanczos,fps=30,setsar=1,format=yuv420p`;
  let d, name;
  if (id.startsWith("C")) {
    const src = find(new RegExp(`(^|[^A-Za-z0-9])${id}\\.mp4$`)); d = dur(src); name = `00-${id}`;
    sh("ffmpeg", ["-loglevel", "error", "-y", "-i", src, "-vf", vf, "-af", `volume=0.35,afade=t=in:d=0.15,afade=t=out:st=${(d - 0.2).toFixed(2)}:d=0.2,aresample=48000`, "-ac", "2", "-c:v", "libx264", "-crf", "17", "-c:a", "aac", "-b:a", "192k", seg]);
  } else {
    const src = scenes[id]; if (!src) throw new Error(`scene ${id} not rendered`); d = dur(src);
    name = path.basename(src).replace(/-16x9\.mp4$/, "");
    sh("ffmpeg", ["-loglevel", "error", "-y", "-i", src, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo", "-vf", vf, "-t", d.toFixed(3), "-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-crf", "17", "-c:a", "aac", "-b:a", "192k", "-shortest", seg]);
    sceneStart[id] = t;
  }
  segs.push(seg); timeline.push({ scene: name, start: Number(t.toFixed(3)), duration: d }); t += d;
}
const list = path.join(tmp, "list.txt"); writeFileSync(list, segs.map((s) => `file '${s}'`).join("\n") + "\n");
const picture = path.join(tmp, "picture.mp4");
sh("ffmpeg", ["-loglevel", "error", "-y", "-f", "concat", "-safe", "0", "-i", list, "-c", "copy", picture]);

// Music for this cut's own timeline (cutaways are quiet scenes).
const tl = path.join(tmp, "timeline.json"); writeFileSync(tl, JSON.stringify({ length: t, scenes: timeline }, null, 1));
const music = path.join(tmp, "music.wav");
sh("python3", [path.join(root, "music", "compose.py"), path.join(root, "films", "chat", "film.json"), tl, music]);

// Narrator: each line, cut from the one recording, placed 0.3 s into its scene.
const lines = JSON.parse(readFileSync(path.join(here, "narration", "lines.json"), "utf8"));
const parts = [], labels = [];
lines.forEach((l, i) => {
  const id = String(l.line).padStart(2, "0");
  const pad = Math.min(0.1, l.start);
  const next = lines[i + 1] ? lines[i + 1].start - 0.08 : l.end + 0.5;
  const end = Math.min(l.end + 0.25, next);
  const at = Math.max(0, sceneStart[id] + 0.3 - pad);
  parts.push(`[n${i}]atrim=start=${(l.start - pad).toFixed(3)}:end=${end.toFixed(3)},asetpts=PTS-STARTPTS,adelay=${Math.round(at * 1000)}|${Math.round(at * 1000)}[v${i}]`);
  labels.push(`[v${i}]`);
});
const split = `[2:a]asplit=${lines.length}${lines.map((_, i) => `[n${i}]`).join("")}`;
const fc = [
  split, ...parts,
  `${labels.join("")}amix=inputs=${lines.length}:normalize=0:dropout_transition=0[nar]`,
  `[nar]asplit=2[nar1][nar2]`,
  `[1:a]volume=0.5[m]`,
  `[m][nar1]sidechaincompress=threshold=0.015:ratio=10:attack=15:release=500[md]`,
  `[0:a][md][nar2]amix=inputs=3:normalize=0:duration=first[mix]`,
  `[mix]loudnorm=I=-16:TP=-1.5:LRA=11,aresample=48000[a]`,
].join(";");
const final = path.join(out, "hybrid-16x9.mp4");
sh("ffmpeg", ["-loglevel", "error", "-y", "-i", picture, "-i", music, "-i", narration, "-filter_complex", fc, "-map", "0:v", "-map", "[a]", "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-t", t.toFixed(3), "-movflags", "+faststart", final]);
console.log(`${final} (${t.toFixed(1)} s)`);
