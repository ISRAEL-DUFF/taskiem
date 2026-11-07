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
| `assemble.mjs` | Renders every scene and joins them into the film, with its captions (.srt) from the script |
| `out/` | What it renders (not in git) |

## Rendering

It uses Playwright's Chromium from the web app (`pnpm install`, then
`pnpm --filter web exec playwright install chromium` once), the web app's own
Inter font (so no network), and ffmpeg.

```sh
node video/render.mjs still 03-briefing 7000              # one frame, 16:9, at 7 s
node video/render.mjs still 03-briefing 7000 --size 9x16  # the phone cut
node video/render.mjs scene 03-briefing                   # the scene as MP4, 30 fps
node video/assemble.mjs                                   # every scene, joined: out/animatic-16x9.mp4 and .srt
node video/assemble.mjs --size 9x16 --keep                # the phone cut, reusing scenes already rendered
```

The whole film renders in about 7 minutes with four scenes at a time
(`--jobs 4`); scenes are joined with a 0.4 s dissolve (`--cut` for straight
cuts), so the film runs about 2:16.

To watch a scene while working on it, open its page in a browser: it plays
and loops.

## Writing a scene

- Copy the screen from its design board or the app; keep the app's words,
  colours (lime only on pine) and sizes.
- Animate with CSS: the animation's delay is its moment in the scene. Use the
  app's timings (`web/src/motion.css`): the picker 280 ms, a sheet 360 ms, a
  diary line 400 ms with a 1.4 s glow, a card's arrival 600 ms, rows 40 ms apart.
- Put a moving part on an element with its moment: `class="pop" style="--at:2.4s"`
  (also `in`, `up`, `show`, `leave`, `lands`, `fills`, `grows`, `toasts`), or
  one that comes and goes: `class="sheet-io" style="--at:1s;--out:3s"` (also
  `show-io`, `pop-io`). Two states in one place go in a `.swap`.
- Follow the Motion board (the design project's sixth board):
  - a sheet grows out of what was tapped: `class="morph-io"` with the tapped
    thing's box (`--t0 --l0 --r0 --b0 --rad0`, screen pixels) and the sheet's
    top (`--t1`), its content in `.after`;
  - the chat behind a sheet dims and blurs (`.veil`);
  - a tapped button or card presses in: `class="press" style="--press:2.8s"`;
  - Save draws its tick: `<span class="tick pop-io">`;
  - a number that changes rolls up: `<span class="roll" style="--at:7s"><span>old</span><span>new</span></span>`;
  - a line lands and makes its own room: `.lands-room`;
  - only good moments (the first sale of the day, an invoice paid) get a
    burst: `<i class="burst" data-at="ms">`.
  An element takes one moving part: a second class with an animation
  replaces the first, so put the second on a wrapper.
- Count a figure up with `data-count`; show a tap with `<i class="tap">` placed
  in the screen's own coordinates (390 × 844), inside `.app`; write a chat's
  head as `<header data-name data-sub data-av data-tone>` and its composer as
  `<footer data-composer>` (see `kit/kit.js`); say the scene's length in
  `window.scene.duration`.
- Check it at both sizes and mid-motion before it goes in.
