# Taskiem explainers

Animated films of Taskiem's features, made from code. Each film comes as a
16:9 cut and a 9:16 cut, with shorts in 9:16 and 1:1, looping clips for the
app, and stills, all with music written for them. The first is the chat
([films/chat](films/chat/script.md)).

Nothing here is filmed or drawn in a video editor. Each scene is a web page
animated with CSS, drawn from the design boards and the app as built, and
rendered frame by frame. So a word, a price or a timing is changed in the
page and the scene rendered again.

| Path | What it is | Shared or per film |
| --- | --- | --- |
| `kit/kit.css`, `kit/kit.js` | The stage at 16:9, 9:16 or 1:1, the phone, captions, taps, the app's screen parts, the Motion board's moving parts, and the timeline | Shared |
| `kit/title.html` | A short's opening line (scene `00-title`) | Shared |
| `music/compose.py` | The music: an original Afro-pop instrumental, made note by note and written again for every cut | Shared |
| `film.mjs`, `render.mjs`, `assemble.mjs`, `shorts.mjs`, `clips.mjs` | Find a film, render, join, make the shorts, make the clips and stills | Shared |
| `films/<film>/script.md` | The script: scenes and their captions, the shorts | Per film |
| `films/<film>/scenes/<nn>-<name>.html` | One scene each, numbered as in the script | Per film |
| `films/<film>/film.json` | The end card's number, each scene's mood for the music, the clips, the stills' moments | Per film |
| `films/_template/` | A film to start from | |
| `out/<film>/` | What it renders (not in git) | |

## Rendering

It uses Playwright's Chromium from the web app (`pnpm install`, then
`pnpm --filter web exec playwright install chromium` once), the web app's own
Inter font (so no network), ffmpeg, and Python 3 with numpy for the music.
Every command takes the film's name first.

```sh
node video/render.mjs chat still 03-briefing 7000              # one frame, 16:9, at 7 s
node video/render.mjs chat still 03-briefing 7000 --size 9x16  # the phone cut (or 1x1)
node video/render.mjs chat scene 03-briefing                   # the scene as MP4, 30 fps
node video/assemble.mjs chat --name explainer                  # out/chat/explainer-16x9.mp4, .srt and .wav
node video/assemble.mjs chat --name explainer --size 9x16 --keep  # the phone cut, reusing scenes already rendered
node video/shorts.mjs chat                                     # out/chat/short-<name>-9x16.mp4 and -1x1.mp4
node video/clips.mjs chat                                      # out/chat/clips/ and out/chat/stills/
```

A film of 16 scenes renders in about 7 minutes with four scenes at a time
(`--jobs 4`). Scenes are joined with a 0.4 s dissolve (`--cut` for straight
cuts). To watch a scene while working on it, open its page in a browser: it
plays and loops.

## The music and sound

`music/compose.py` writes the music for every cut from its timeline
(`out/<film>/<name>-<size>.timeline.json`, which `assemble.mjs` writes), so
a short gets a track that fits it too. It is an original Afro-pop
instrumental at 112 BPM in F major: a log-drum bass, kick, clap, rim,
shaker, keys on the off-beats, a pad and a kalimba tune over F, Am, Dm and
B♭.

A film says how the music feels in each scene with a mood, in `film.json`
(`"moods": {"3": "build"}`; a scene without one is sparse):

| Mood | What plays | For |
| --- | --- | --- |
| `sparse` | The pad and the tune, a light shaker | An opening, a title card |
| `build` | The groove comes in, not yet full | The first act |
| `full` | Everything, the tune in the back | The working middle |
| `peak` | Everything, the tune in front | The acts the film builds to |
| `lift` | A little less | A breath between acts |
| `low` | A dark pad, the bass and a soft kick | Night, a pause |

At the end card (`"end"`) the groove stops on a held chord and the mark
gets its chime. Every tap in a scene gets a soft tock, and every burst (a
good moment) a short chime, read from the scene's page, so moving a tap
moves its sound.

Each cut is mixed to −16 LUFS (two passes of ffmpeg's `loudnorm`). Every
piece works muted too: the shorts carry their words on screen. To lay other
music under a cut, `--music track.wav` (it is cut to the length and faded
out); `--silent` for none.

## Making a new explainer

1. **Start the film:** `cp -r video/films/_template video/films/<film>`. It
   renders as it is, so `node video/render.mjs <film> still 01-open 3000`
   shows the starting point.
2. **Script** (`script.md`): who it follows, through which moments, and a
   caption for each scene that carries the story with the sound off. Keep
   the table's five columns: the assembler reads the captions from it. Add
   the shorts' table: each short's scenes and its opening line, with the
   words for pine in `*stars*`.
3. **Style frames:** draw two or three key scenes in full (the opening, the
   one moment that sells the feature, the end card) and check them as stills
   at 16:9, 9:16 and 1:1 before going on.
4. **Animatic:** every scene at its full length, simply moving, joined with
   `assemble.mjs`, to check the story and the timing.
5. **Polish:** each scene against its design board and the Motion board
   (below), checked at every size and mid-motion.
6. **Cuts and sound:** give each scene its mood and the end card's number
   in `film.json`, list the clips and the stills' moments there, then
   `assemble.mjs` at 16:9 and 9:16, `shorts.mjs` and `clips.mjs`.

Reuse the kit's parts, and add a new part to the kit (not to one film) when
a second film would want it too.

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
- A part placed beside the phone (`.float`) or a second phone needs a place
  of its own at 9:16 (`.portrait …`) and 1:1 (`.square …`).
- Check it at every size and mid-motion before it goes in.
