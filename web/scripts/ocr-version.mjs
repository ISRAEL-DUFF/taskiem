// The folder the browser's photo reader is served from (public/ocr/<it>),
// named by the exact versions installed: Tesseract.js, its core and the
// English model. The server keeps /ocr/ files for a year without asking
// again (api/static.go), so a new version must have a new name, or a
// browser could go on with an old worker. Read by scripts/ocr-assets.mjs,
// which copies the files there, and by the Vite configs, which give it to
// src/lib/photoText.ts as __OCR_BASE__.
import { readFileSync, realpathSync } from "node:fs";
import { dirname, join } from "node:path";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);

const versionOf = (pkg) => JSON.parse(readFileSync(pkg, "utf8")).version;

/** Where the packages are: Tesseract.js, its core (Tesseract.js's own
 * dependency) and the English model. */
export function ocrPackages() {
  const tess = dirname(require.resolve("tesseract.js/package.json"));
  const core = dirname(createRequire(join(realpathSync(tess), "package.json")).resolve("tesseract.js-core/package.json"));
  const eng = dirname(require.resolve("@tesseract.js-data/eng/package.json"));
  return { tess, core, eng };
}

/** The folder's name, e.g. "7.0.0-core7.0.0-eng1.0.0". */
export function ocrDir() {
  const { tess, core, eng } = ocrPackages();
  const v = (d) => versionOf(join(d, "package.json"));
  return `${v(tess)}-core${v(core)}-eng${v(eng)}`;
}

/** The path the browser loads them from, e.g. "/ocr/7.0.0-core7.0.0-eng1.0.0". */
export function ocrBase() {
  return `/ocr/${ocrDir()}`;
}
