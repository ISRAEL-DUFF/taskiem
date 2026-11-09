// A film (video/README.md): its folder in films/<name>/ with its script,
// its scenes and film.json, and where its renders go (out/<name>/). The
// scripts here take the film's name first: node video/assemble.mjs chat.

import { readFile, readdir, mkdir } from "node:fs/promises";
import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

export const here = path.dirname(fileURLToPath(import.meta.url));

export async function film(name) {
  const dir = path.join(here, "films", name ?? "");
  if (!name || name.startsWith("-") || !existsSync(path.join(dir, "film.json"))) {
    const films = (await readdir(path.join(here, "films"))).filter((f) => !f.startsWith("_"));
    console.error(`Name a film first: ${films.join(", ")} (video/films/; video/README.md)`);
    process.exit(2);
  }
  const out = path.join(here, "out", name);
  await mkdir(out, { recursive: true });
  const config = JSON.parse(await readFile(path.join(dir, "film.json"), "utf8"));
  const scenes = (await readdir(path.join(dir, "scenes")))
    .filter((f) => /^\d\d-.+\.html$/.test(f))
    .sort()
    .map((f) => f.replace(/\.html$/, ""));
  return {
    name,
    dir,
    out,
    config,
    scenes,
    script: () => readFile(path.join(dir, "script.md"), "utf8"),
    // A scene's page; 00-title is the kit's title card, shared by every film.
    page: (scene) => (scene === "00-title" ? path.join(here, "kit", "title.html") : path.join(dir, "scenes", `${scene}.html`)),
  };
}
