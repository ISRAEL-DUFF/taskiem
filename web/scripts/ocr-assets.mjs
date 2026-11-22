// Copies what the browser needs to read a photo's text on the person's own
// device (src/lib/photoText.ts) into public/ocr/<versions>
// (scripts/ocr-version.mjs): Tesseract.js's worker, its three LSTM cores
// (the browser loads the one it can run) and the English model. Served
// from Taskiem itself, so a photo is never sent anywhere to be read and
// nothing loads from a third party. Run before dev and build
// (package.json); public/ocr is not committed. Folders of other versions
// are removed, so only the one the build names is served.
import { cpSync, mkdirSync, existsSync, readdirSync, rmSync } from "node:fs";
import { join } from "node:path";
import { ocrDir, ocrPackages } from "./ocr-version.mjs";

const root = new URL("../public/ocr/", import.meta.url).pathname;
const dir = ocrDir();
const out = join(root, dir);
mkdirSync(out, { recursive: true });
for (const old of readdirSync(root)) {
  if (old !== dir) rmSync(join(root, old), { recursive: true, force: true });
}

const { tess, core, eng } = ocrPackages();

const files = [
  [join(tess, "dist/worker.min.js"), "worker.min.js"],
  [join(core, "tesseract-core-lstm.wasm.js"), "tesseract-core-lstm.wasm.js"],
  [join(core, "tesseract-core-simd-lstm.wasm.js"), "tesseract-core-simd-lstm.wasm.js"],
  [join(core, "tesseract-core-relaxedsimd-lstm.wasm.js"), "tesseract-core-relaxedsimd-lstm.wasm.js"],
  [join(eng, "4.0.0_best_int/eng.traineddata.gz"), "eng.traineddata.gz"],
];
for (const [from, to] of files) {
  if (!existsSync(from)) throw new Error(`ocr-assets: missing ${from}`);
  cpSync(from, join(out, to));
}
