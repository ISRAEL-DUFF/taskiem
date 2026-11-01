// Website experiment 2: the Claude Design canvas in design/, built into a
// static site in dist/ (README.md).
//
//   node website/experiment-2/build.mjs [--whatsapp 2348000000000] [--app https://app.example.ng] [--email hello@example.ng]
//
// Each page in design/ is a Design Component (.dc.html): HTML with
// {{holes}}, <sc-for>, <sc-if> and <dc-import>, and a script whose
// renderVals() gives the values. This renders them in Chromium, as the
// canvas does, and writes plain HTML: no runtime, no framework.
//
// What the design wrote by hand about the product (the templates, the
// services, the plans) is replaced with the repository's own facts, read
// by the docs generator (go run ./tools/docsite -data), so the site never
// says something the product does not do.
import { readFile, writeFile, mkdir, rm, copyFile, mkdtemp } from "node:fs/promises";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
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
const email = arg("email");

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
// The design's placeholder links. Until a WhatsApp number or the app's
// address is given, both buttons lead to the contact page.
const chat = whatsapp ? `https://wa.me/${whatsapp}?text=hi` : app ? `${app}/signup` : "/contact";
const links = {
  "Home.dc.html": "/",
  "#whatsapp": chat,
  "#whatsapp-chat": chat,
  "#start-web": app ? `${app}/signup` : "/contact",
};

// ---------- The repository's facts ----------
const tmp = await mkdtemp(path.join(tmpdir(), "taskiem-site-"));
const dataFile = path.join(tmp, "website.json");
execFileSync("go", ["run", "./tools/docsite", "-data", dataFile], { cwd: repo, stdio: "inherit" });
const facts = JSON.parse(await readFile(dataFile, "utf8"));

const some = (list) => (list && list.length ? list : null);
const firstSentence = (s) => {
  const i = (s || "").indexOf(". ");
  return i >= 0 ? s.slice(0, i + 1) : s || "";
};
// "Paystack, Moniepoint (Monnify), iswallet or Flutterwave" is the rails a
// template runs on; a chip names the first and counts the others.
const chips = (uses) => (uses || []).map((u) => {
  const names = u.split(/, | or /);
  return names.length > 2 ? `${names[0]} or ${names.length - 1} others` : u;
});
const groupOf = {};
for (const g of facts.Templates) for (const t of g.Items) groupOf[t.ID] = g.ID;
const card = (t) => ({ title: t.Title, line: t.Summary, services: chips(t.Uses), href: `/templates#${groupOf[t.ID]}`, setUp: "#whatsapp" });

// One line under each group of templates.
const blurbs = {
  collections: "Stop chasing. Taskiem asks, politely, every time.",
  customers: "Small messages that make people feel looked after.",
  sales: "Know what came in without opening three apps.",
  reporting: "The numbers, ready when you want them.",
  alerts: "Hear about a problem first, with the reason.",
  payroll: "Pay people on time, with your yes and your PIN.",
  suppliers: "Pay what you owe, once, after you approve.",
  operations: "Stock, reminders and the jobs nobody remembers.",
  compliance: "Check who someone is before you pay or lend.",
  storefront: "Orders, bookings and questions from your page.",
  approvals: "Requests that wait for the right person's yes.",
  books: "Keep your accounting tool up to date by itself.",
  invoices: "Send, chase and close invoices without a spreadsheet.",
  payments: "Collect, pay out and refund, each exactly once.",
  schools: "School fees, from term bills to the gate list.",
};
const templateGroups = facts.Templates.map((g) => ({
  id: g.ID, anchor: `#${g.ID}`, title: g.Title, blurb: blurbs[g.ID] || "",
  items: g.Items.map((t) => ({ title: t.Title, line: t.Summary, services: chips(t.Uses) })),
}));
const serviceGroups = facts.Connectors.map((g) => ({
  title: g.Title,
  tiles: g.Items.map((c) => ({ mark: c.Name.slice(0, 1), name: c.Name, line: firstSentence(c.Description), href: `/integrations/${c.ID}` })),
}));
const services = facts.Services.map((c) => ({
  ID: c.ID, Name: c.Name, Kind: c.Kind, Description: c.Description,
  connect: c.Built ? "It is part of Taskiem: there is nothing to connect."
    : c.OneClick ? `One click: sign in to ${c.Name} and allow Taskiem. There is nothing to copy.`
      : `Paste the key from your ${c.Name} account once, when a workflow first needs it. Taskiem keeps it sealed and never shows it again.`,
  Does: some(c.Does), Starts: some(c.Starts), Templates: some((c.Templates || []).map(card)),
}));

// The plans, from deploy/plans.yaml.
const plan = Object.fromEntries(facts.Pricing.Plans.map((p) => [p.ID, p]));
const prices = (key) => ({ starter: plan.starter[key], growing: plan.growth[key], enterprise: plan.enterprise[key] });
const pricing = {
  monthly: { prices: prices("Monthly"), per: "a month" },
  yearly: { prices: prices("Annual"), per: "a year" },
};
const rows = facts.Pricing.Compare.map((r) => {
  const feature = r.Cells.every((c) => c === "Yes" || c === "—");
  const cell = (c) => (feature ? (c === "Yes" ? { yes: true } : { no: true }) : { text: c });
  return { name: r.Label, a: cell(r.Cells[0]), b: cell(r.Cells[1]), c: cell(r.Cells[2]) };
});
const overrides = {
  Templates: { groups: templateGroups },
  Integrations: { groups: serviceGroups },
  Pricing: {
    prices: pricing.monthly.prices, per: pricing.monthly.per, rows,
    features: { starter: plan.starter.Includes, growing: plan.growth.Includes, enterprise: plan.enterprise.Includes },
  },
};
// The home page's six cards, in the design's order.
const byID = {};
for (const g of facts.Templates) for (const t of g.Items) byID[t.ID] = t;
const homeCards = ["chase-overdue-invoices", "payment-thank-you-sms", "run-payroll", "failed-transfer-alert", "daily-sales-summary", "sales-ledger-sheet"]
  .map((id) => {
    if (!byID[id]) throw new Error(`template ${id} is not in templates/library`);
    return card(byID[id]);
  });
const patches = {
  connectorCount: facts.ConnectorCount,
  homeCards,
  homePrices: [pricing.monthly.prices.starter, pricing.monthly.prices.growing, pricing.monthly.prices.enterprise],
  email,
};

// ---------- Rendering ----------
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
sources.Service = await readFile(path.join(here, "pages", "Service.dc.html"), "utf8");

// Runs in the page: renders one Design Component to static HTML.
function renderInPage({ name, sources, props, overrides, patches }) {
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
        const p = {};
        for (const a of child.attributes) {
          if (!a.name.startsWith("hint-") && a.name !== "name") p[a.name] = fill(scope, a.value);
        }
        child.replaceWith(...build(child.getAttribute("name"), p).childNodes);
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
  const build = (cname, p, extra) => {
    const doc = parse(sources[cname]);
    const Component = componentOf(doc);
    const vals = Object.assign(new Component(p).renderVals(), extra || {});
    const root = doc.querySelector("x-dc").cloneNode(true);
    root.querySelector("helmet")?.remove();
    render(root, vals);
    return root;
  };
  const doc = parse(sources[name]);
  const root = build(name, props, overrides[name]);

  // The design's hand-written facts that are not in renderVals, replaced.
  const textIs = (el, start) => el.textContent.trim().startsWith(start);
  const all = (sel) => [...root.querySelectorAll(sel)];
  if (name === "Home") {
    const jobs = all("h2").find((h) => textIs(h, "Six jobs")).closest("section");
    jobs.querySelectorAll(".card, .card-pine").forEach((c, i) => {
      const t = patches.homeCards[i];
      if (!t) return;
      c.querySelector("h3").textContent = t.title;
      c.querySelector("p.small").textContent = t.line;
      const box = c.querySelector(".chip").parentElement;
      box.replaceChildren(...t.services.map((s) => Object.assign(document.createElement("span"), { className: "chip", textContent: s })));
      c.querySelector("a.link").setAttribute("href", t.href);
    });
    // The hero's phone, drawn with inline styles: marked for site.js's chat.
    const online = all("span").find((sp) => sp.textContent.trim() === "online");
    if (online) {
      const screen = online.closest("div[style*='overflow:hidden']");
      const body = screen.children[1];
      body.setAttribute("data-chat", "hero");
      for (const m of body.children) m.setAttribute("data-from", m.style.alignSelf === "flex-end" ? "me" : "tk");
      screen.parentElement.setAttribute("data-float", "");
    }
    const more = all("span.chip").find((s) => textIs(s, "and about"));
    if (more) more.textContent = `${patches.connectorCount} in all`;
    const pricingTeaser = all("h2").find((h) => textIs(h, "One flat price")).closest("section");
    pricingTeaser.querySelectorAll(".num").forEach((n, i) => { if (patches.homePrices[i]) n.textContent = patches.homePrices[i]; });
  }
  if (name === "Integrations") {
    root.querySelector(".page-top .lede").textContent =
      `${patches.connectorCount} services, Nigerian ones first. Link one, and every job you set up can use it.`;
  }
  if (name === "Pricing") {
    const staff = all("details.faq").find((d) => textIs(d.querySelector("summary"), "Which plan do I need to pay my staff"));
    if (staff) (staff.querySelector(".faq-body p") || staff.querySelector(".faq-body")).textContent =
      "Every plan. Paying staff after you approve works on Starter Trader, Growing Business and Enterprise & Regulated alike.";
  }
  if (name === "Contact") {
    const placeholder = all("p").find((p) => p.textContent.trim() === "[YOUR CONTACT EMAIL]");
    if (placeholder) {
      if (patches.email) {
        placeholder.replaceChildren(Object.assign(document.createElement("a"), { className: "link", href: `mailto:${patches.email}`, textContent: patches.email }));
      } else {
        placeholder.parentElement.remove();
      }
    }
    const form = root.querySelector("form");
    if (form && patches.email) form.setAttribute("data-email", patches.email);
  }
  return { title: doc.title, body: root.innerHTML };
}

// ---------- Writing the site ----------
await rm(dist, { recursive: true, force: true });
await mkdir(path.join(dist, "img"), { recursive: true });
await mkdir(path.join(dist, "integrations"), { recursive: true });
for (const f of ["mark.svg", "mark-reversed.svg", "mark-small.svg", "wordmark.svg", "wordmark-reversed.svg", "avatar.svg", "og.png"]) {
  await copyFile(path.join(repo, "docs/brand/relay", f), path.join(dist, "img", f));
}
// The design's stylesheet, with its dark theme also applied when the reader's
// device is set to dark ("auto"), then this site's own additions.
let css = await readFile(path.join(design, "tk.css"), "utf8");
const darkRules = css.match(/[^}]*\[data-theme="dark"\][^{]*\{[^}]*\}/g) || [];
// The header's switch sets <html data-force="light|dark">, which wins
// over the device's setting.
const asAuto = (sel) => darkRules.map((r) => r.trim().replaceAll('.tk[data-theme="dark"]', `${sel} .tk[data-theme="auto"]`)).join("\n");
css += `\n@media (prefers-color-scheme:dark){\n${asAuto('html:not([data-force="light"])')}\n}\n${asAuto('html[data-force="dark"]')}\n`;
css += await readFile(path.join(here, "site.css"), "utf8");
await writeFile(path.join(dist, "tk.css"), css);
await copyFile(path.join(here, "site.js"), path.join(dist, "site.js"));

const page = (title, description, body, extra = "") => `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${title}</title>
<meta name="description" content="${description.replaceAll('"', "&quot;")}">
<meta property="og:image" content="/img/og.png">
<meta name="theme-color" content="#0d3b2e">
<script>
// The reader's theme, before the page paints: ?theme=light|dark|auto, else their last choice.
(function () {
  try {
    var q = new URLSearchParams(location.search).get("theme");
    if (q === "light" || q === "dark") localStorage.setItem("tk-theme", q);
    if (q === "auto") localStorage.removeItem("tk-theme");
    var t = q === "auto" ? null : q || localStorage.getItem("tk-theme");
    if (t === "light" || t === "dark") document.documentElement.setAttribute("data-force", t);
  } catch (e) { /* storage blocked: follow the device */ }
})();
</script>
<link rel="icon" href="/img/mark-small.svg" type="image/svg+xml">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@500&family=Plus+Jakarta+Sans:wght@400;500;800&display=swap">
<link rel="stylesheet" href="/tk.css">
</head>
<body>
${body}
${extra}<script src="/site.js"></script>
</body>
</html>
`;
const finish = (body) => {
  // Each component turns any theme but "dark" into "light"; on the site
  // every one follows the reader's setting instead.
  let out = body.replaceAll('data-theme="light"', 'data-theme="auto"');
  for (const [from, to] of Object.entries({ ...blobs, ...links })) out = out.replaceAll(`"${from}"`, `"${to}"`);
  return out;
};
const lede = "Tell Taskiem what to do in your own words. It reminds customers who owe you, thanks those who pay, and pays your staff after you approve.";

const browser = await chromium.launch();
const tab = await browser.newPage();
await tab.setContent("<!doctype html><html><body></body></html>");
for (const [name, out] of Object.entries(pages)) {
  const r = await tab.evaluate(renderInPage, { name, sources, props: { theme: "auto" }, overrides, patches });
  const title = out === "index" || !r.title ? "Taskiem · Run your business from WhatsApp" : `${r.title} · Taskiem`;
  const extra = name === "Pricing" ? `<script>window.TK_PRICES=${JSON.stringify(pricing)}</script>\n` : "";
  await writeFile(path.join(dist, `${out}.html`), page(title, lede, finish(r.body), extra));
}
for (const s of services) {
  const r = await tab.evaluate(renderInPage, { name: "Service", sources, props: { theme: "auto", service: s }, overrides: {}, patches });
  await writeFile(path.join(dist, "integrations", `${s.ID}.html`), page(`${s.Name} · Taskiem`, firstSentence(s.Description), finish(r.body)));
}
await browser.close();
await rm(tmp, { recursive: true, force: true });
console.log(`${Object.keys(pages).length} pages and ${services.length} service pages in ${path.relative(process.cwd(), dist) || dist}`);
