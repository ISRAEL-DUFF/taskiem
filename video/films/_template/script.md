# <Feature>: the explainer's script

The words and story for the explainer (plan: <the shared plan doc>; how it
is made: [README](../../README.md)). Who it follows, through what, with the
people, products and prices of the design boards and the demo world
(`web/e2e/demo/`). The captions carry the story with the sound off.

About <length>; 16:9 times below. Each scene is `scenes/<nn>-<name>.html`.
The assembler reads the caption from this table: keep its five columns.

| # | Time | Act | On screen | Caption |
| --- | --- | --- | --- | --- |
| 1 | 0:00–0:06 | Open | <the problem, as the owner lives it> | <one line> |
| 2 | 0:06–0:13 | End | The mark draws itself; the wordmark; **<the one thing to do>** | <the promise, in one line> |

## The short cuts

| Short | Scenes | Opening line |
| --- | --- | --- |
| <Name> | 1 | <A line, *the last words in pine.*> |

Each opens on its line (kit/title.html; the words in *stars* in pine) and
ends on the end card (film.json "end"). `node video/shorts.mjs <film>`
makes them all, in 9:16 and 1:1.
