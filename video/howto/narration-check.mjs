// Check a how-to's narrator lines against its recorded captions.
//   node video/howto/narration-check.mjs 01-link-and-price-list
// Reads narration.md (this folder) and out/howto/<slug>.srt, prints each
// numbered line with its caption's length and speed, and flags problems.
import { readFileSync, existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const slug = process.argv[2];
if (!slug) { console.error("usage: narration-check.mjs <slug>"); process.exit(2); }
const MAX = 2.4; // words a second

const md = readFileSync(path.join(here, "narration.md"), "utf8");
const sec = md.split(/\n## /).find((s) => s.startsWith(slug + ":"));
if (!sec) { console.error(`no narration for ${slug}`); process.exit(2); }
const lines = [...sec.matchAll(/^(\d+)\. (.+)$/gm)].map((m) => m[2]);

const srtFile = path.join(here, "..", "out", "howto", `${slug}.srt`);
if (!existsSync(srtFile)) { console.error(`${srtFile} not found: record the video first`); process.exit(2); }
const t = (s) => { const [h, m, r] = s.split(":"); const [sec, ms] = r.split(","); return +h * 3600 + +m * 60 + +sec + +ms / 1000; };
const cues = readFileSync(srtFile, "utf8").trim().split(/\n\n/).map((b) => {
  const [, time] = b.split("\n"); const [a, z] = time.split(" --> ");
  return t(z) - t(a);
});

let bad = 0;
if (cues.length !== lines.length) { console.log(`MISMATCH: ${lines.length} narrator lines, ${cues.length} captions`); bad++; }
lines.forEach((text, i) => {
  const d = cues[i]; if (d === undefined) return;
  const words = text.split(/\s+/).length, wps = words / d;
  const flag = wps > MAX ? "  TOO FAST" : "";
  if (flag) bad++;
  console.log(`${String(i + 1).padStart(2)}  ${d.toFixed(1).padStart(5)}s  ${words.toString().padStart(2)}w  ${wps.toFixed(1)}/s${flag}  ${text.slice(0, 60)}`);
});
console.log(bad ? `\n${bad} problem(s)` : "\nall lines fit");
process.exit(bad ? 1 : 0);
