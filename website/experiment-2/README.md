# Website experiment 2

The Taskiem website as Claude Design drew it ([the canvas](https://claude.ai/artifact/7xWaWXohyXzSbQ49FBzjod)), from the relay brand ([brand](../../docs/brand/README.md)). It is separate from the website the docs generator builds (`tools/docsite`, [website](../../docs/strategy/website.md)), which stays as it is: this is an experiment to review, not the live site.

| | |
| --- | --- |
| `design/` | The canvas's pages and components as Claude Design saved them (`.dc.html`), its index (`canvas.json`) and its stylesheet (`tk.css`). Change the design in the canvas, then copy the files here again |
| `build.mjs` | Renders each page in Chromium, as the canvas does, into plain HTML in `dist/` (not committed) |
| `site.css`, `site.js` | What a real website needs beyond the drawing: the phone menu, the monthly and yearly prices, less motion when asked |

```sh
node website/experiment-2/build.mjs                                   # dist/
node website/experiment-2/build.mjs --whatsapp 2348000000000 --app https://app.example.ng
npx surge website/experiment-2/dist <name>.surge.sh                   # with SURGE_LOGIN and SURGE_TOKEN set
```

It needs Playwright with Chromium, as the card maker does (`pnpm install`, then `pnpm --filter web exec playwright install chromium` once). The logos are the brand's own files, copied from `docs/brand/relay` at build time, never the canvas's uploads. Every page follows the reader's light or dark setting.

## What the drawing leaves for later

- **The buttons' destinations.** *Chat with Taskiem on WhatsApp* and *Start on the web* stay as `#whatsapp` and `#start-web` until `--whatsapp` and `--app` are given.
- **Pages the drawing links to but does not draw:** a page per service under `/integrations/…` (the docs generator's website has them), and the legal pages.
- **The contact form** sends nothing yet.
- **Facts to check against the product** before this replaces the live site (the claims register in [website](../../docs/strategy/website.md#claims-register)): the pricing table's `[YOUR LIMIT]` cells; the services on *Works with*, written by hand (some, such as NIN checks and CSV import, are not connectors, and "about 30 more are on the way" undersells the ones that exist); the templates, written by hand rather than read from `templates/library`; the team roles on *How it works*, which the page marks as an example.
