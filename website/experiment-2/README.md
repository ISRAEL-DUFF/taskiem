# Website experiment 2

The Taskiem website as Claude Design drew it ([the canvas](https://claude.ai/artifact/7xWaWXohyXzSbQ49FBzjod)), from the relay brand ([brand](../../docs/brand/README.md)). It is separate from the website the docs generator builds (`tools/docsite`, [website](../../docs/strategy/website.md)), which stays as it is: this is an experiment to review, not the live site.

| | |
| --- | --- |
| `design/` | The canvas's pages and components as Claude Design saved them (`.dc.html`), its index (`canvas.json`) and its stylesheet (`tk.css`). Change the design in the canvas, then copy the files here again |
| `build.mjs` | Renders each page in Chromium, as the canvas does, into plain HTML in `dist/` (not committed), with the repository's own facts in place of what the drawing wrote by hand |
| `pages/Service.dc.html` | A page for each service on *Works with*, in the design's parts and styles: the drawing links to them but does not draw them |
| `site.css`, `site.js` | What a real website needs beyond the drawing: the phone menu, the light/dark switch, the monthly and yearly prices, the contact form, and motion |

```sh
node website/experiment-2/build.mjs                                   # dist/
node website/experiment-2/build.mjs --whatsapp 2348000000000 --app https://app.example.ng --email hello@example.ng
```

It needs Go (it runs `go run ./tools/docsite -data` for the facts) and Playwright with Chromium, as the card maker does (`pnpm install`, then `pnpm --filter web exec playwright install chromium` once). The logos are the brand's own files, copied from `docs/brand/relay` at build time, never the canvas's uploads. Every page follows the reader's light or dark setting until they choose one with the switch in the header, which the site then remembers. A link can choose for them: `?theme=light`, `?theme=dark`, or `?theme=auto` to follow the device again.

## Publishing: taskiem.ng on Vercel

The `website` workflow (`.github/workflows/website.yml`) builds the site on every pull request that touches it, and on main publishes it to the Vercel project `taskiem-website`, which the first run creates. `vercel.json` gives it clean addresses (`taskiem.ng/pricing`, not `pricing.html`). Set up once:

1. **Vercel token.** In Vercel: Account Settings > Tokens > Create, scoped to your team. Add it to this repository as the Actions secret `VERCEL_TOKEN` (Settings > Secrets and variables > Actions). Never commit it or paste it anywhere else.
2. **The outside facts (optional).** Actions *variables* `SITE_WHATSAPP` (digits only, e.g. 2348012345678), `SITE_APP` (the app's address) and `SITE_EMAIL`. Each is left off the site while empty; change one, then run the workflow again (Actions > website > Run workflow).
3. **First publish.** Merge to main, or run the workflow by hand. The site is then at the project's `*.vercel.app` address.
4. **The domain.** In the Vercel project: Settings > Domains, add `taskiem.ng`, then `www.taskiem.ng` set to redirect to it. Vercel shows the DNS records to make.
5. **DNS at the registrar (Whogohost).** In the domain's DNS management, add the records exactly as Vercel shows them: usually an `A` record for `@` to `76.76.21.21` and a `CNAME` for `www` to `cname.vercel-dns.com`. Remove any other `A` record for `@` and any parking record. Vercel issues the HTTPS certificate itself once the records resolve, from minutes to a few hours.

## The repository's facts

The drawing wrote the product's details by hand, and some were wrong. The build replaces them with what `tools/docsite -data` reads from the repository, the same facts the docs generator's website shows:

| Where | From |
| --- | --- |
| Templates: every group and card | `templates/library` (offered templates only), with the services each runs on |
| Works with: every service, and a page for each | `connectors/*/` manifests: name, description, what workflows can do with it, what can start one, the templates that use it |
| Home: the six workflow cards, the count of services, the prices | The library, the manifests, `deploy/plans.yaml` |
| Pricing: prices, what each plan includes, the comparison table | `deploy/plans.yaml` |
| Pricing: "Which plan do I need to pay my staff?" | Every plan: no plan gates payroll |

Buttons lead to WhatsApp with `--whatsapp`, to the app's sign-up with `--app`, and to the contact page until either is given. The contact page shows `--email` and its form opens the reader's email app with the message; without it, the email line is left out and the form says it is not switched on yet.

## Motion

Calm, and true to how Taskiem behaves: the hero's chat plays, Taskiem typing before each reply; each step on *How it works* plays as it scrolls into view; the salary run takes a PIN, is approved, sent once and paid; an invoice marks itself paid; naira figures count up; sections ease in; the hero's phone floats. Chats hold their final height, so nothing on the page moves. With reduced motion asked for, or without JavaScript, every page reads as drawn.

## Still to decide

- Taskiem's WhatsApp number, the app's address and the contact email (the build's flags).
- The legal pages.
- The team roles on *How it works* (Owner, Manager, Cashier, Accountant), which the page marks as an example.
