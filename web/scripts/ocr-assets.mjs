// Copies what the browser needs to read a photo's text on the person's own
// device (src/lib/photoText.ts) into public/ocr/v7: Tesseract.js's worker,
// its three LSTM cores (the browser loads the one it can run) and the
// English model. Served from Taskiem itself, so a photo is never sent
// anywhere to be read and nothing loads from a third party. Run before
// dev and build (package.json); public/ocr is not committed.
import { cpSync, mkdirSync, existsSync, realpathSync } from "node:fs";
import { dirname, join } from "node:path";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const out = new URL("../public/ocr/v7/", import.meta.url).pathname;
mkdirSync(out, { recursive: true });

const tess = dirname(require.resolve("tesseract.js/package.json"));
const core = dirname(createRequire(join(realpathSync(tess), "package.json")).resolve("tesseract.js-core/package.json"));
const eng = dirname(require.resolve("@tesseract.js-data/eng/package.json"));

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
