// Taskiem's website (taskiem.ng): the pages in pages/, built into a static
// site in dist/ for Vercel.
//
//   node website/site/build.mjs [--whatsapp 2348000000000] [--app https://app.example.ng] [--email hello@example.ng]
//
// Each page in pages/ starts with <!--page {"title","description","path"}-->,
// then its own <style>, {{HEADER}}, its <main>, {{FOOTER}} and its <script>.
// The build wraps it in a full document, fills in the shared parts and the
// repository's facts (prices from deploy/plans.yaml and the services that
// have a logo, read by the docs generator: go run ./tools/docsite -data),
// points the buttons at WhatsApp or the app, and writes dist/.
import { readFile, writeFile, mkdir, rm, copyFile, readdir, mkdtemp } from "node:fs/promises";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(here, "../..");
const dist = path.join(here, "dist");
const brand = path.join(repo, "docs/brand/relay");

const arg = (name) => {
  const i = process.argv.indexOf(`--${name}`);
  return i > 0 ? process.argv[i + 1] || "" : "";
};
const whatsapp = arg("whatsapp").replace(/[^0-9]/g, "");
const app = arg("app").replace(/\/$/, "");
const email = arg("email").trim();
const factsFile = arg("facts"); // for working offline: a file written earlier by docsite -data

// Where the buttons go. Without a WhatsApp number the chat buttons lead to
// the app's sign-up; without either, to the contact page.
const signup = app ? `${app}/signup?start=business` : "/contact";
const go = { chat: whatsapp ? `https://wa.me/${whatsapp}?text=hi` : signup, web: signup };

// ---------- the repository's facts ----------
let facts;
if (factsFile) {
  facts = JSON.parse(await readFile(factsFile, "utf8"));
} else {
  const tmp = await mkdtemp(path.join(tmpdir(), "taskiem-site-"));
  const f = path.join(tmp, "website.json");
  execFileSync("go", ["run", "./tools/docsite", "-data", f], { cwd: repo, stdio: "inherit" });
  facts = JSON.parse(await readFile(f, "utf8"));
}
const esc = (s) => String(s).replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;");

// ---------- the brand ----------
const svg = async (f) => (await readFile(path.join(brand, f), "utf8")).trim();
const MARK = await svg("mark.svg"), MARKREV = await svg("mark-reversed.svg"), WORD = await svg("wordmark.svg"), WORDREV = await svg("wordmark-reversed.svg");

// ---------- shared parts ----------
const NAV = [["What it does", "/#control"], ["Trade", "/#trade"], ["Your page", "/storefront"], ["Schools", "/schools"], ["Pricing", "/pricing"]];
const header = (current) => `<header class="top">
  <div class="wrap">
    <div class="bar">
      <a class="logo" href="/" aria-label="Taskiem home"><span class="mk">${MARK}</span><span class="wm wm-std">${WORD}</span><span class="wm wm-rev">${WORDREV}</span></a>
      <nav class="nav" id="nav" aria-label="Main">
        ${NAV.map(([t, h]) => `<a href="${h}"${h === current ? ' aria-current="page"' : ""}>${t}</a>`).join("\n        ")}
      </nav>
      <a class="btn btn-main mag" href="${go.web}">Start free</a>
      <button class="theme" id="theme" type="button" aria-label="Switch light or dark">
        <svg class="sun" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="4.5"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>
        <svg class="moon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5Z"/></svg>
      </button>
      <button class="menu" id="menu" type="button" aria-label="Menu" aria-expanded="false" aria-controls="nav"><i></i><i></i><i></i></button>
    </div>
  </div>
</header>`;
const legal = app ? `<a href="/privacy">Privacy</a><a href="/delete-account">Delete your account</a>` : "";
const footer = `<footer class="foot">
  <div class="wrap">
    <div class="foot-top">
      <div class="foot-brand">
        <a href="/" aria-label="Taskiem home"><span class="mk">${MARKREV}</span><span class="wm">${WORDREV}</span></a>
        <p>Business workflow automation Nigerian businesses can trust with their money, from the corner shop to the regulated lender.</p>
      </div>
      <nav class="foot-cols" aria-label="Footer">
        <div><p class="label">Product</p><a href="/#control">What it does</a><a href="/#trade">Trade</a><a href="/storefront">Your page</a><a href="/pricing">Pricing</a></div>
        <div><p class="label">Features</p><a href="/security">Security</a><a href="/tables">Your records</a><a href="/schools">Schools</a></div>
        <div><p class="label">Company</p><a href="/partners">For fintechs</a><a href="/about">About</a><a href="/contact">Contact</a>${legal}</div>
      </nav>
    </div>
    <div class="foot-bottom">
      <p>Built by the team behind Xenibase.</p>
      <p>© ${new Date().getFullYear()} Taskiem</p>
    </div>
  </div>
</footer>`;

// ---------- pricing, from deploy/plans.yaml ----------
const P = facts.Pricing;
const CHECK = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>';
const SUB = { starter: "For one trader or one shop.", growth: "For a business with staff, customers and more to chase.", enterprise: "For lenders, payroll companies and banks." };
const plans = P.Plans.map((p, k) => {
  const feat = p.Name === P.TrialPlan;
  const btn = p.Contact || p.ID === "enterprise"
    ? `<a class="btn btn-alt btn-big mag" href="/contact">Talk to us</a>`
    : `<a class="btn ${feat ? "btn-lime" : "btn-main"} btn-big mag" href="${go.web}">Start ${P.TrialDays} days free${feat ? ' <span class="arr">→</span>' : ""}</a>`;
  const pre = p.ID === "enterprise" ? "from " : "";
  return `        <article class="plan${feat ? " feat" : ""}" style="--k:${k}">
          ${feat ? `<span class="badge">Your ${P.TrialDays} free days are on this plan</span>` : ""}
          <h3>${esc(p.Name)}</h3>
          <p class="psub">${esc(SUB[p.ID] || "")}</p>
          <p class="price"><span class="pre">${pre}</span><b class="amt num" data-m="${esc(p.Monthly)}" data-y="${esc(p.Annual)}">${esc(p.Monthly)}</b> <span class="per">a month</span></p>
          ${btn}
          <ul class="inc">${p.Includes.map((x) => `<li><i>${CHECK}</i>${esc(x)}</li>`).join("")}</ul>
        </article>`;
}).join("\n");
const compare = `<thead><tr><th scope="col">What is included</th>${P.Plans.map((p) => `<th scope="col"${p.Name === P.TrialPlan ? ' class="f"' : ""}>${esc(p.Name)}</th>`).join("")}</tr></thead><tbody>${P.Compare.map((r) =>
  `<tr><th scope="row">${esc(r.Label)}</th>${r.Cells.map((v) => (v === "Yes" ? `<td class="yes">${CHECK}</td>` : v === "—" ? '<td class="no">—</td>' : `<td class="num">${esc(v)}</td>`)).join("")}</tr>`).join("")}</tbody>`;

// ---------- the services with a logo ----------
const ids = new Set();
for (const g of facts.Connectors || []) for (const c of g.Items || []) ids.add(c.ID);
const order = JSON.parse(await readFile(path.join(here, "logos/order.json"), "utf8"));
const has = (id) => ids.has(id) || (id === "zoho" && [...ids].some((x) => x.startsWith("zoho")));
const logos = order.filter(([id]) => has(id))
  .map(([id, name, ext]) => `<span class="lg" title="${esc(name)}"><img src="/logos/${id}.${ext}" alt="${esc(name)}" width="40" height="40" loading="lazy"></span>`).join("");

// ---------- the contact page's ways in ----------
const ways = [
  whatsapp && `<a class="way" href="${go.chat}"><span class="way-ic">WA</span><b>WhatsApp</b><span>+${whatsapp}</span><em>Chat with Taskiem →</em></a>`,
  email && `<a class="way" href="mailto:${esc(email)}"><span class="way-ic">@</span><b>Email</b><span>${esc(email)}</span><em>Write to us →</em></a>`,
  app && `<a class="way" href="${signup}"><span class="way-ic">↗</span><b>Start on the web</b><span>${esc(app.replace(/^https?:\/\//, ""))}</span><em>Open the app →</em></a>`,
].filter(Boolean).join("\n") || `<p class="way-none">Taskiem's WhatsApp number and email go here once they are switched on. In the meantime, use the form.</p>`;

// ---------- pages ----------
const page = (meta, own, body) => `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>${esc(meta.title)}</title>
<meta name="description" content="${esc(meta.description)}">
<link rel="canonical" href="https://taskiem.ng${meta.path}">
<meta property="og:type" content="website">
<meta property="og:site_name" content="Taskiem">
<meta property="og:title" content="${esc(meta.title)}">
<meta property="og:description" content="${esc(meta.description)}">
<meta property="og:url" content="https://taskiem.ng${meta.path}">
<meta property="og:image" content="https://taskiem.ng/img/og.png">
<meta name="twitter:card" content="summary_large_image">
<meta name="theme-color" content="#0d3b2e">
<link rel="icon" href="/img/mark-small.svg" type="image/svg+xml">
<link rel="apple-touch-icon" href="/img/avatar.svg">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@500;700&family=Plus+Jakarta+Sans:wght@500;600;700;800&display=swap">
<link rel="stylesheet" href="/style.css">
${own}
</head>
<body>
${body}
<script src="/site.js"></script>
</body>
</html>
`;

await rm(dist, { recursive: true, force: true });
await mkdir(path.join(dist, "img"), { recursive: true });
await mkdir(path.join(dist, "logos"), { recursive: true });
for (const f of ["mark.svg", "mark-small.svg", "mark-reversed.svg", "wordmark.svg", "wordmark-reversed.svg", "avatar.svg", "og.png"]) {
  await copyFile(path.join(brand, f), path.join(dist, "img", f));
}
for (const f of await readdir(path.join(here, "logos"))) if (!f.endsWith(".json")) await copyFile(path.join(here, "logos", f), path.join(dist, "logos", f));
await copyFile(path.join(here, "assets/style.css"), path.join(dist, "style.css"));
await copyFile(path.join(here, "assets/site.js"), path.join(dist, "site.js"));

for (const f of (await readdir(path.join(here, "pages"))).filter((f) => f.endsWith(".html"))) {
  const src = await readFile(path.join(here, "pages", f), "utf8");
  const m = src.match(/^<!--page (\{.*?\})-->\n/);
  if (!m) throw new Error(`${f}: no <!--page {...}--> line`);
  const meta = JSON.parse(m[1]);
  let rest = src.slice(m[0].length);
  let own = "";
  const st = rest.match(/^<style>\n[\s\S]*?\n<\/style>\n/);
  if (st) { own = st[0].trim(); rest = rest.slice(st[0].length); }
  let body = rest
    .replace("{{HEADER}}", header(meta.path))
    .replace("{{FOOTER}}", footer)
    .replaceAll("{{MARK}}", MARK).replaceAll("{{WORD}}", WORD).replaceAll("{{WORDREV}}", WORDREV)
    .replace("{{LOGOS}}", logos)
    .replace("{{PLANS}}", plans)
    .replace("{{COMPARE}}", compare)
    .replaceAll("{{VAT}}", String(P.VATPercent))
    .replaceAll("{{TRIAL}}", String(P.TrialDays))
    .replaceAll("{{CONNECTORS}}", String(facts.ConnectorCount))
    .replace("{{WAYS}}", ways)
    .replaceAll("{{EMAIL}}", esc(email));
  body = body.replace(/href="\/contact" data-go="(chat|web)"/g, (_, k) => `href="${go[k]}"`);
  const left = body.match(/\{\{[A-Z]+\}\}/);
  if (left) throw new Error(`${f}: ${left[0]} was not filled`);
  await writeFile(path.join(dist, f), page(meta, own, body));
  console.log(`dist/${f}`);
}

// Clean addresses (/pricing, not pricing.html). The privacy notice and account
// deletion live in the app; Google Play keeps these two addresses.
const vercel = JSON.parse(await readFile(path.join(here, "vercel.json"), "utf8"));
vercel.redirects = [
  ...(app ? ["/privacy", "/delete-account"].map((p) => ({ source: p, destination: `${app}${p}`, permanent: false })) : []),
  // Pages the previous site had and this one does not.
  { source: "/how-it-works", destination: "/#control", permanent: true },
  { source: "/templates", destination: "/#build", permanent: true },
  { source: "/integrations", destination: "/#trade", permanent: true },
  { source: "/integrations/:id", destination: "/#trade", permanent: true },
];
await writeFile(path.join(dist, "vercel.json"), JSON.stringify(vercel, null, 2) + "\n");
console.log("dist/vercel.json");
