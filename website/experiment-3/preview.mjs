// Makes each version open straight in a browser:
//   node website/experiment-3/preview.mjs   -> website/experiment-3/dist/v1/index.html, dist/v2/index.html, …
// index.html is written for the claude.ai viewer, which adds <!doctype html>, <head> and <body>,
// so it is wrapped in a full document here. A version's other pages (v3's schools.html) are
// full documents already and are copied beside it, so the links between them work.
import { readdirSync, readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const head = '<!doctype html>\n<html lang="en">\n<head>\n<meta charset="utf-8">\n<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">\n<style>body{margin:0}img{max-width:100%}[hidden]{display:none!important}</style>\n</head>\n<body>\n';

for (const v of readdirSync(here).filter(d => /^v\d+$/.test(d)).sort()) {
  const dir = join(here, v);
  if (!existsSync(join(dir, 'index.html'))) continue;
  const out = join(here, 'dist', v);
  mkdirSync(out, { recursive: true });
  writeFileSync(join(out, 'index.html'), head + readFileSync(join(dir, 'index.html'), 'utf8') + '\n</body>\n</html>\n');
  console.log(`dist/${v}/index.html`);
  for (const f of readdirSync(dir).filter(f => f.endsWith('.html') && f !== 'index.html' && !f.endsWith('-src.html') && f !== 'src.html')) {
    writeFileSync(join(out, f), readFileSync(join(dir, f)));
    console.log(`dist/${v}/${f}`);
  }
}
