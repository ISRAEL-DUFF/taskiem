# Website experiment 3: an animated landing page

One animated landing page for Taskiem. Its energy comes from [owo.app](https://owo.app): stacked headline blocks, colour bands beside a phone whose chat plays out, cycling words, and cards that deal out. It uses the relay brand ([brand](../../docs/brand/README.md)) and copy from [the website's structure and copy](../../docs/strategy/website.md). Like experiment 2, this is something to review, not the live site.

## Versions

| Folder | Status |
| --- | --- |
| `v1/` | **Frozen.** The first version, kept unchanged as the reference while we iterate. Preview: <https://claude.ai/artifact/8tcV54JwwbvdXLoZK6uaWw> |
| `v2/` | Brand colours only (pine, lime, mist, white, with a little mint and amber). The hero phone switches between Taskiem chat, WhatsApp and Telegram sales; an endless strip of the connectors' logos; a workflow canvas that builds and runs itself; one sale traced step by step; animated job cards; a PIN approval. Preview: <https://claude.ai/artifact/72teAue6hJYHpD9Bs6ksyH> |

Each later version goes in its own folder (`v3/`, …), and no earlier one is edited.

## Opening a version

```sh
node website/experiment-3/preview.mjs   # writes dist/v1.html, dist/v2.html, … (not committed)
```

Then open `website/experiment-3/dist/v2.html` in a browser. It needs the internet for the fonts; everything else is in the file.

The logo strip in v2 uses each service's own site icon, or a one-colour mark for the big brands, fetched once and built into the page. Before the site goes public, swap them for the official files from each company's brand page. Dojah and Lenco are missing because no usable icon could be fetched; iswallet, Payrolla and Xenibase have no public logo.

## Files in each version

| File | What it is |
| --- | --- |
| `index.html` | The page, with the logo SVGs from `docs/brand/relay` inlined |
| `src.html` | The same page with `{{MARK}}`, `{{WORD}}` and `{{WORDREV}}` where the logos go (`mark.svg`, `wordmark.svg`, `wordmark-reversed.svg`) |

The page is written for claude.ai's artifact viewer, which adds the `<!doctype html>`, `<head>` and `<body>` around it, so it has none of its own. `preview.mjs` adds that wrapper; do the same before serving it from an ordinary web host. It loads Plus Jakarta Sans and JetBrains Mono from Google Fonts; the main site self-hosts them.

The chats' names and naira amounts are examples. The buttons point at `#begin` until there is a WhatsApp number and an app address to link to.
