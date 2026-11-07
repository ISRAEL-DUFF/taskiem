# The Taskiem chat explainer

An animated film of the Taskiem chat, about 2 minutes 20 seconds, with a
phone cut and six shorts made from its scenes. The plan is the shared doc
"Taskiem chat: animated explainer plan"; the words and story are in
[script.md](script.md).

Nothing here is filmed or drawn in a video editor: each scene is a web page
animated with CSS, drawn from the design boards and the app as built, and
rendered frame by frame. A word, a price or a timing is changed in the page
and the scene rendered again.

| Path | What it is |
| --- | --- |
| `script.md` | The final script: scenes, captions, the deal's numbers, the shorts |
| `kit/kit.css` | The stage (16:9 or 9:16), the phone, captions, a tap, the app's screen parts |
| `kit/kit.js` | The timeline: a scene plays in a browser; the renderer steps it frame by frame |
| `scenes/<nn>-<name>.html` | One scene each, numbered as in the script |
| `render.mjs` | Renders a still or a scene to video |
| `out/` | What it renders (not in git) |

## Rendering

It uses Playwright's Chromium from the web app (`pnpm install`, then
`pnpm --filter web exec playwright install chromium` once), the web app's own
Inter font (so no network), and ffmpeg.

```sh
node video/render.mjs still 03-briefing 7000              # one frame, 16:9, at 7 s
node video/render.mjs still 03-briefing 7000 --size 9x16  # the phone cut
node video/render.mjs scene 03-briefing                   # the scene as MP4, 30 fps
```

To watch a scene while working on it, open its page in a browser: it plays
and loops.

## Writing a scene

- Copy the screen from its design board or the app; keep the app's words,
  colours (lime only on pine) and sizes.
- Animate with CSS: the animation's delay is its moment in the scene. Use the
  app's timings (`web/src/motion.css`): the picker 280 ms, a sheet 360 ms, a
  diary line 400 ms with a 1.4 s glow, a card's arrival 600 ms, rows 40 ms apart.
- Count a figure up with `data-count`, show a tap with `<i class="tap">` (see
  `kit/kit.js`), and say the scene's length in `window.scene.duration`.
- Check it at both sizes and mid-motion before it goes in.
