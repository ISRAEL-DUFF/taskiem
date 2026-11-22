# Website experiment 3: an animated landing page

One animated landing page for Taskiem. Its energy comes from [owo.app](https://owo.app): stacked headline blocks, colour bands beside a phone whose chat plays out, cycling words, and cards that deal out. It uses the relay brand ([brand](../../docs/brand/README.md)) and copy from [the website's structure and copy](../../docs/strategy/website.md). Like experiment 2, this is something to review, not the live site.

## Versions

| Folder | Status |
| --- | --- |
| `v1/` | **Frozen.** The first version, kept unchanged as the reference while we iterate. Preview: <https://claude.ai/artifact/8tcV54JwwbvdXLoZK6uaWw> |

Each later version goes in its own folder (`v2/`, …), and no earlier one is edited.

## Files in each version

| File | What it is |
| --- | --- |
| `index.html` | The page, with the logo SVGs from `docs/brand/relay` inlined |
| `src.html` | The same page with `{{MARK}}`, `{{WORD}}` and `{{WORDREV}}` where the logos go (`mark.svg`, `wordmark.svg`, `wordmark-reversed.svg`) |

The page is written for claude.ai's artifact viewer, which adds the `<!doctype html>`, `<head>` and `<body>` around it, so it has none of its own. Before serving it from an ordinary web host, add that wrapper with a charset and viewport meta. It loads Plus Jakarta Sans and JetBrains Mono from Google Fonts; the main site self-hosts them.

The chats' names and naira amounts are examples. The buttons point at `#begin` until there is a WhatsApp number and an app address to link to.
