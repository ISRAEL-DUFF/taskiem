// Website experiment 2: the Claude Design canvas in design/, built into a
// static site in dist/ (README.md).
//
//   node website/experiment-2/build.mjs [--whatsapp 2348000000000] [--app https://app.example.ng]
//
// Each page in design/ is a Design Component (.dc.html): HTML with
// {{holes}}, <sc-for>, <sc-if> and <dc-import>, and a script whose
// renderVals() gives the values. This renders them in Chromium, exactly as
// the canvas does, and writes plain HTML: no runtime, no framework.
import { readFile, writeFile, mkdir, rm, copyFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { createRequire } from "node:module";

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(here, "../..");
const design = path.join(here, "design");
const dist = path.join(here, "dist");

const arg = (name) => {
  const i = process.argv.indexOf(`--${name}`);
  return i > 0 ? process.argv[i + 1] : "";
};
const whatsapp = arg("whatsapp").replace(/[^0-9]/g, "");
const app = arg("app").replace(/\/$/, "");

// The pages, by design file and published name (surge serves /name from name.html).
const pages = {
  Home: "index", HowItWorks: "how-it-works", Templates: "templates", Integrations: "integrations",
  Pricing: "pricing", Security: "security", Tables: "tables", Storefront: "storefront",
  Partners: "partners", About: "about", Contact: "contact", NotFound: "404",
};
const components = ["Header", "Cta", "Footer"];

// The design's uploads are the brand's own files (docs/brand/relay), served from img/.
const blobs = {
  "/_blob/50a98d2917f323d39577f6eb0dc5de03": "/tk.css",
  "/_blob/f27b4430c8fa6ccfceb23d47b15d0ea4": "/img/mark.svg",
  "/_blob/3c3ab2d1252ea744399a7067e5a21df5": "/img/wordmark.svg",
  "/_blob/883c87704e19d22b80e24a64c63ed9de": "/img/mark-reversed.svg",
  "/_blob/8c096f830081fc34bad0c5ab0c1c76ab": "/img/wordmark-reversed.svg",
  "/_blob/5315bafeb8fb29b4ae5c1a426166468c": "/img/avatar.svg",
};
// The design's placeholder links, and where they go once the outside facts are given.
const links = {
  "Home.dc.html": "/",
  "#whatsapp": whatsapp ? `https://wa.me/${whatsapp}?text=hi` : "#whatsapp",
  "#whatsapp-chat": whatsapp ? `https://wa.me/${whatsapp}?text=hi` : "#whatsapp",
  "#start-web": app ? `${app}/signup` : "#start-web",
};

const require = createRequire(path.join(repo, "web", "package.json"));
let chromium;
try {
  ({ chromium } = require("@playwright/test"));
} catch {
  ({ chromium } = require("playwright"));
}

const sources = {};
for (const name of [...Object.keys(pages), ...components]) {
  sources[name] = await readFile(path.join(design, `${name}.dc.html`), "utf8");
}

// Runs in the page: renders one Design Component to static HTML.
function renderInPage({ name, sources }) {
  // <sc-for> and <sc-if> become <template>s first: a parser moves unknown
  // tags out of a table, and a template may sit anywhere.
  const parse = (src) => new DOMParser().parseFromString(
    src.replace(/<sc-(for|if)\b/g, "<template data-sc=\"$1\"").replace(/<\/sc-(for|if)>/g, "</template>"),
    "text/html");
  const componentOf = (doc) => {
    const code = doc.querySelector("script[data-dc-script]").textContent;
    class DCLogic {
      constructor(props) { this.props = props; this.state = this.state || {}; }
      setState() {}
    }
    return new Function("DCLogic", `${code}\nreturn Component;`)(DCLogic);
  };
  const lookup = (scope, expr) => {
    const e = expr.trim();
    if (e === "true") return true;
    if (e === "false") return false;
    if (/^-?\d+(\.\d+)?$/.test(e)) return Number(e);
    return e.split(".").reduce((v, k) => (v == null ? undefined : v[k]), scope);
  };
  const HOLE = /\{\{\s*([^}]+?)\s*\}\}/g;
  const fill = (scope, s) => s.replace(HOLE, (_, e) => {
    const v = lookup(scope, e);
    return v == null || typeof v === "function" ? "" : String(v);
  });
  const render = (node, scope) => {
    for (const child of [...node.childNodes]) {
      if (child.nodeType === Node.TEXT_NODE) {
        child.textContent = fill(scope, child.textContent);
        continue;
      }
      if (child.nodeType !== Node.ELEMENT_NODE) continue;
      const tag = child.dataset?.sc ? `sc-${child.dataset.sc}` : child.tagName.toLowerCase();
      if (tag === "sc-for") {
        const list = lookup(scope, child.getAttribute("list").replace(HOLE, "$1")) || [];
        const as = child.getAttribute("as");
        const out = document.createDocumentFragment();
        list.forEach((item, i) => {
          const copy = child.content.cloneNode(true);
          render(copy, { ...scope, [as]: item, $index: i });
          out.append(copy);
        });
        child.replaceWith(out);
      } else if (tag === "sc-if") {
        if (lookup(scope, child.getAttribute("value").replace(HOLE, "$1"))) {
          const copy = child.content.cloneNode(true);
          render(copy, scope);
          child.replaceWith(copy);
        } else {
          child.remove();
        }
      } else if (tag === "dc-import") {
        const props = {};
        for (const a of child.attributes) {
          if (!a.name.startsWith("hint-") && a.name !== "name") props[a.name] = fill(scope, a.value);
        }
        child.replaceWith(...build(child.getAttribute("name"), props).childNodes);
      } else {
        for (const a of [...child.attributes]) {
          const only = a.value.match(/^\{\{\s*([^}]+?)\s*\}\}$/);
          const v = only ? lookup(scope, only[1]) : undefined;
          if (typeof v === "function") {
            // An event handler: kept as a name the page's own script handles.
            child.removeAttribute(a.name);
            child.setAttribute("data-action", only[1]);
          } else if (a.value.includes("{{")) {
            child.setAttribute(a.name, fill(scope, a.value));
          }
        }
        render(child, scope);
      }
    }
  };
  const build = (cname, props) => {
    const doc = parse(sources[cname]);
    const Component = componentOf(doc);
    const vals = new Component(props).renderVals();
    const root = doc.querySelector("x-dc").cloneNode(true);
    root.querySelector("helmet")?.remove();
    render(root, vals);
    return root;
  };
  const doc = parse(sources[name]);
  // "auto": the stylesheet follows the reader's light or dark setting.
  const body = build(name, { theme: "auto" });
  // The pricing page's yearly prices, for its toggle.
  let extra = null;
  if (name === "Pricing") {
    const Component = componentOf(doc);
    const c = new Component({ theme: "auto" });
    c.state = { yearly: true };
    const y = c.renderVals();
    c.state = { yearly: false };
    const m = c.renderVals();
    extra = { monthly: { prices: m.prices, per: m.per }, yearly: { prices: y.prices, per: y.per } };
  }
  return { title: doc.title, body: body.innerHTML, extra };
}

await rm(dist, { recursive: true, force: true });
await mkdir(path.join(dist, "img"), { recursive: true });
for (const f of ["mark.svg", "mark-reversed.svg", "mark-small.svg", "wordmark.svg", "wordmark-reversed.svg", "avatar.svg", "og.png"]) {
  await copyFile(path.join(repo, "docs/brand/relay", f), path.join(dist, "img", f));
}
// The design's stylesheet, with its dark theme also applied when the reader's
// device is set to dark ("auto").
let css = await readFile(path.join(design, "tk.css"), "utf8");
const darkRules = css.match(/[^}]*\[data-theme="dark"\][^{]*\{[^}]*\}/g) || [];
css += `\n@media (prefers-color-scheme:dark){\n${darkRules.map((r) => r.trim().replaceAll('[data-theme="dark"]', '[data-theme="auto"]')).join("\n")}\n}\n`;
css += await readFile(path.join(here, "site.css"), "utf8");
await writeFile(path.join(dist, "tk.css"), css);

const browser = await chromium.launch();
const tab = await browser.newPage();
await tab.setContent("<!doctype html><html><body></body></html>");
for (const [name, out] of Object.entries(pages)) {
  const r = await tab.evaluate(renderInPage, { name, sources });
  // Each component turns any theme but "dark" into "light"; on the site
  // every one follows the reader's setting instead.
  let body = r.body.replaceAll('data-theme="light"', 'data-theme="auto"');
  for (const [from, to] of Object.entries({ ...blobs, ...links })) body = body.replaceAll(`"${from}"`, `"${to}"`);
  const pricing = r.extra ? `<script>window.TK_PRICES=${JSON.stringify(r.extra)}</script>` : "";
  const html = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${out === "index" || !r.title ? "Taskiem · Run your business from WhatsApp" : `${r.title} · Taskiem`}</title>
<meta name="description" content="Tell Taskiem what to do in your own words. It reminds customers who owe you, thanks those who pay, and pays your staff after you approve.">
<meta property="og:image" content="/img/og.png">
<meta name="theme-color" content="#0d3b2e">
<link rel="icon" href="/img/mark-small.svg" type="image/svg+xml">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@500&family=Plus+Jakarta+Sans:wght@400;500;800&display=swap">
<link rel="stylesheet" href="/tk.css">
</head>
<body>
${body}
${pricing}
<script src="/site.js"></script>
</body>
</html>
`;
  await writeFile(path.join(dist, `${out}.html`), html);
  console.log(`${out}.html`);
}
await browser.close();
await copyFile(path.join(here, "site.js"), path.join(dist, "site.js"));
