# Taskiem's website (taskiem.ng)

The live site. It grew out of [website experiment 3](../experiment-3/README.md) (v6) and replaces [experiment 2](../experiment-2/README.md), which stays in the repository for reference.

```sh
node website/site/build.mjs                                   # dist/
node website/site/build.mjs --whatsapp 2348000000000 --app https://app.example.ng --email hello@example.ng
```

It needs Go (it runs `go run ./tools/docsite -data` for the facts) and Node 22. Nothing else: no packages, no browser.

| Path | What it is |
| --- | --- |
| `pages/*.html` | One file per page: a `<!--page {"title","description","path"}-->` line, the page's own `<style>`, `{{HEADER}}`, its `<main>`, `{{FOOTER}}` and its `<script>` |
| `assets/style.css` | The styles every page shares |
| `assets/site.js` | What every page shares: light and dark, the phone menu, the hero's light, buttons that lean, things that rise in |
| `logos/` | The services' logos, named by connector id; `order.json` is the order they scroll in |
| `vercel.json` | Clean addresses and headers; the build adds the redirects |
| `build.mjs` | Wraps each page in a full document and writes `dist/` |

## Pages

Home, Schools, Pricing, Inventory, Security, Your records (`/tables`), Your page (`/storefront`), For fintechs (`/partners`), About, Contact and 404. The previous site's How it works, Templates and Works with are gone; their addresses redirect to the home page's matching section.

## What the build fills in

| Where | From |
| --- | --- |
| Pricing: the plans, what each includes, the comparison, VAT, the free days | `deploy/plans.yaml`, through the docs generator |
| Home: the logo strip | The connectors in `connectors/` that have a logo in `logos/` |
| Every "Start free" and "Chat on WhatsApp" button | `--whatsapp` gives a `wa.me` link; otherwise `--app`'s sign-up; otherwise `/contact` |
| Contact: the ways in, and where the form's message goes | `--whatsapp`, `--email`, `--app`; each left off while empty |
| `/privacy` and `/delete-account` | Forwarded to the app when `--app` is given (Google Play keeps these addresses) |

The `website` workflow passes the repository's Actions variables `SITE_WHATSAPP`, `SITE_APP` and `SITE_EMAIL`, builds on pull requests, and publishes to Vercel from `main` (set-up steps in the [experiment 2 README](../experiment-2/README.md#publishing-taskiemng-on-vercel)).

## Before it is public

- **Logos:** each service's own site icon, or a one-colour mark for the big brands, fetched once. Swap them for each company's official files.
- **Copy rules:** only what the product does today. Not claimed yet: certifications, NDPA compliance, where production data is hosted, a live WhatsApp number.
- **Fonts** load from Google Fonts.
