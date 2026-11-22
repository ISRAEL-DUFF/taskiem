// Wraps each version's index.html in a full HTML document, so it opens straight in a browser.
//   node website/experiment-3/preview.mjs      -> website/experiment-3/dist/v1.html, v2.html, …
import { readdirSync, readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const out = join(here, 'dist');
mkdirSync(out, { recursive: true });

const head = '<!doctype html>\n<html lang="en">\n<head>\n<meta charset="utf-8">\n<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">\n<style>body{margin:0}img{max-width:100%}[hidden]{display:none!important}</style>\n</head>\n<body>\n';

for (const v of readdirSync(here).filter(d => /^v\d+$/.test(d)).sort()) {
  const page = join(here, v, 'index.html');
  if (!existsSync(page)) continue;
  writeFileSync(join(out, `${v}.html`), head + readFileSync(page, 'utf8') + '\n</body>\n</html>\n');
  console.log(`dist/${v}.html`);
}
