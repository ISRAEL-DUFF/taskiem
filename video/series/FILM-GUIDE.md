# Filming an episode in the phone app

How to make one film (one flow, 15 to 60 seconds) of the real app's own
screens. Read `video/recording-app.md` for the method; this is the working guide.
Examples to copy: `mobile/test/film_pay_test.dart` (a payment: Today menu, sheet,
number keyboard, fingerprint), `film_signup_test.dart` (a conversation with
letter keyboard), `film_today_test.dart` (the simplest).

## Environment

```sh
export PATH=/tmp/claude-0/-home-user-taskier/4a7f812e-7eae-5391-b4de-ccc94df594e3/scratchpad/flutter-sdk/flutter/bin:$PATH
export CI=true FLUTTER_SUPPRESS_ANALYTICS=true BOT=true
cd <repo>/mobile && flutter pub get --enforce-lockfile      # once per checkout
```
Flutter 3.47.9. Do not change app code (`mobile/lib`). A film is a new test file
`mobile/test/film_<name>_test.dart`, skipped unless `VIDEO_DIR` is set.

## The loop

1. **Find the app's own test for the screen** (`mobile/test/*_test.dart`:
   `sell_sheet_test.dart`, `invoices_page_test.dart`, ...). Copy its set-up
   (the stand-in server's recorded answers, the widget it pumps). Keep its
   fixtures; rename the people and things to the film's story.
2. **Write the film** with `Film` (`mobile/test/film.dart`): `Film.start(t, name)`,
   then `say(caption)`, `play/hold(seconds)`, `tap(finder, then: seconds)`,
   `drag(finder, offset)`, `keyboard(true, kind: 'number'|'text')`,
   `type(field, text)`, `system('biometric', seconds)`, `finish()`.
3. **Dry run** (no pictures; 5 to 10 seconds) until it passes:
   `VIDEO_DRY=1 VIDEO_DIR=<out> flutter test test/film_<name>_test.dart`
4. **Real run** (writes 30 pictures a second; about 0.4 s per picture; run it in the
   background and wait): `VIDEO_DIR=<out> flutter test test/film_<name>_test.dart`
5. **Compose** (puts it in a phone, parallel, about 0.05 s per picture):
   `python3 video/phone/compose.py --frames <out>/<name> --out <name>.mp4 --captions`
   and look at stills: `... --still 4.5 --out frame.png`. Check light **and** the
   moments that matter (a tap ring, the keyboard, the result).
6. **Review**: is it right, slow enough to read, free of dead air, true to the app?

`<out>` is the shared folder
`/tmp/claude-0/-home-user-taskier/4a7f812e-7eae-5391-b4de-ccc94df594e3/scratchpad/films`.
Each film writes to `<out>/<name>/` (`f00000.png ...`, `cues.json`).

## Rules for a good film

- **One goal, one flow.** What the viewer can do after it. 15 to 60 seconds.
- **Reproducible by the viewer (founder's rule).** These are how-to videos: people
  follow them on their own phone. Every step shown must be one a person can do in the
  real app: a real button, a real route to each sheet (a tap, never a sheet opened by test
  code), only the app's own options (no stand-in-only choices), and a caption that matches
  the frame. Cutting to a second person's phone is fine (two people can play both parts);
  inventing a screen, button, field or option is not. If the real route to a screen cannot
  be filmed, cut the step or say so; never fake the route. Stand-in data (names, prices) is
  fine. Check each film against this before handing back.
- **Only what the app does.** Use the screen's own flow, the real words. Never
  invent a button. If the screen cannot be filmed from its tests' set-up, say so.
- **A caption per step**, 8 to 12 words, present tense, plain ("Tap Next."). The
  first caption sets the story and has `who:` ("Zainab · Zuri Atelier"). Captions
  are the on-screen words for the viewer; the narrator's lines are separate.
- **Pace.** Hold 0.6 to 1 s after a screen settles before the next tap; hold 2 to 3 s
  on the moment that matters (a result, a warning). No `settle()` where something
  visible happens: it skips time without pictures. Use it only to let data load.
- **Taps** need the target visible: `await t.ensureVisible(find...)` then `await t.pump()`.
- **Typing** needs `keyboard(true, ...)` first, `keyboard(false)` after, and a tap on the
  field first. `kind: 'text'` for words, `'number'` for numbers. Type the way a person does.
- **Light theme** (`TkMode.light`) unless the story is evening. Phone clock `--time 13:00`.
- **The story's names**, not the fixture's: Zuri Atelier (owner Zainab Yusuf, apprentice
  Dami, supplier Alhaja Sade of Balogun fabrics, customer Nneka Eze, rider Emeka).
  Amounts in naira with the app's own formatting. No real brands.
- **Positioning:** Taskiem runs the business's work alongside its conversations; never
  say it replaces WhatsApp. Do not claim end-to-end encryption.

## Reporting back

For each film: the test file path, the shared output folder, the running time, the
caption list with times (from `cues.json`), what the film shows, and anything that
differs from the real app (stand-in data, a screen that could not be reached, a
keyboard type not drawn). Do not commit; the files are collected afterwards.
