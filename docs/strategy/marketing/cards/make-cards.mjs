// Print-ready business cards and staff ID cards for everyone in a list,
// from the same design as the card maker page (card-maker.html).
//
//   node docs/strategy/marketing/cards/make-cards.mjs people.json out/
//
// Writes taskiem-business-card-<name>.pdf and taskiem-staff-id-<name>.pdf
// per person: page 1 the front, page 2 the back, with bleed, text as
// vector type. Needs Playwright with Chromium (README.md).
import { readFile, mkdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { createRequire } from "node:module";

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(here, "../../../..");
const [listFile, outDir] = process.argv.slice(2);
if (!listFile || !outDir) {
  console.error("usage: node make-cards.mjs people.json out-dir");
  process.exit(2);
}

// Playwright from the web app's dependencies, or a global install.
const require = createRequire(path.join(repo, "web", "package.json"));
let chromium;
try {
  ({ chromium } = require("@playwright/test"));
} catch {
  ({ chromium } = require("playwright"));
}

const people = JSON.parse(await readFile(listFile, "utf8"));
const page = await readFile(path.join(here, "card-maker.html"), "utf8");
// The repository's own font files, so the cards need no network.
const font = async (family, file) =>
  `@font-face{font-family:"${family}";src:url(data:font/woff2;base64,${(await readFile(path.join(repo, "tools/docsite/assets/fonts", file))).toString("base64")}) format("woff2");font-weight:100 900}`;
const fonts = (await font("Plus Jakarta Sans", "plus-jakarta-sans.woff2")) + (await font("JetBrains Mono", "jetbrains-mono.woff2"));
// Offline: drop the page's web font and script links; the cards need neither.
const html = `<!doctype html><html><head><meta charset="utf-8"><style>${fonts}</style><script>window.__cardMakerHeadless=true</script></head><body>${page
  .replace(/<link [^>]*fonts\.g[^>]*>/g, "")
  .replace(/<script src=[^>]*><\/script>/g, "")}</body></html>`;

const slug = (s) => String(s || "card").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "card";

await mkdir(outDir, { recursive: true });
const browser = await chromium.launch();
try {
  for (const person of people) {
    const data = { ...person };
    if (person.photo) {
      const file = path.resolve(path.dirname(listFile), person.photo);
      const ext = path.extname(file).slice(1).toLowerCase().replace("jpg", "jpeg");
      data.photo = `data:image/${ext};base64,${(await readFile(file)).toString("base64")}`;
    }
    for (const [kind, name] of [["bc", "business-card"], ["id", "staff-id"]]) {
      const tab = await browser.newPage();
      await tab.setContent(html, { waitUntil: "load" });
      const [w, h] = await tab.evaluate(([k, d]) => window.cardMaker.printMode(k, d), [kind, data]);
      await tab.evaluate(() => document.fonts.ready);
      const file = path.join(outDir, `taskiem-${name}-${slug(person.slug || person.name)}.pdf`);
      await tab.pdf({ path: file, width: `${w}mm`, height: `${h}mm`, printBackground: true, margin: { top: 0, right: 0, bottom: 0, left: 0 } });
      await tab.close();
      console.log(file);
    }
  }
} finally {
  await browser.close();
}
