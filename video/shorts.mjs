// Makes the six shorts (video/script.md, "The short cuts"): for each, its
// title card, its scenes and the end card, with the music written to it, in
// 9:16 and 1:1, by assemble.mjs. A scene already rendered at a size is reused.
//
//   node video/shorts.mjs [--sizes 9x16,1x1] [--only deals] [--jobs 3]

import { readFile } from "node:fs/promises";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const args = process.argv.slice(2);
const flag = (k, d) => {
  const i = args.indexOf(`--${k}`);
  return i >= 0 ? args[i + 1] : d;
};
const sizes = flag("sizes", "9x16,1x1").split(",");
const only = flag("only");

const script = await readFile(path.join(here, "script.md"), "utf8");
const shorts = [];
for (const line of script.split("\n")) {
  // | Morning | 3–4 | Your business, *before you open the shop.* |
  const m = /^\| ([A-Z][a-z]+) \| (\d+)(?:–(\d+))? \| (.+) \|$/.exec(line);
  if (!m) continue;
  const from = Number(m[2]);
  const to = Number(m[3] ?? m[2]);
  const scenes = Array.from({ length: to - from + 1 }, (_, i) => from + i);
  shorts.push({ name: `short-${m[1].toLowerCase()}`, scenes: [...scenes, 16], title: m[4] });
}

for (const size of sizes) {
  for (const s of shorts) {
    if (only && s.name !== `short-${only}`) continue;
    const argv = [path.join(here, "assemble.mjs"), "--keep", "--size", size, "--name", s.name, "--scenes", s.scenes.join(","), "--title", s.title, "--jobs", flag("jobs", "3")];
    await new Promise((ok, fail) => {
      const p = spawn("node", argv, { stdio: "inherit" });
      p.on("close", (c) => (c === 0 ? ok() : fail(new Error(`${s.name} ${size}: exit ${c}`))));
    });
  }
}
