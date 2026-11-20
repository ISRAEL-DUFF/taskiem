---
name: mobile-screen
description: Build, redesign or change a screen, page, sheet or chat in the Taskiem phone app (mobile/, Flutter) so it looks as drawn in the app's own look ("Direction B"), in light and dark. Use for any work that changes what a mobile screen shows, and to review a PR's mobile screens against the design.
---

# Building a phone screen that looks right

The app's look is settled (`docs/spec/mobile-app-look.md`) and its rules are
in `mobile/CLAUDE.md`: read that first. This skill is the method. Working
code is half the job; the other half is **looking at the screen** and fixing
what is off, in both themes, before calling it done.

Flutter in a cloud session: `export PATH=/root/sdk/flutter/bin:$PATH`.
If it is not there, install the version CI pins (`FLUTTER_VERSION` in
`.github/workflows/mobile.yml`) the way CI does:

```sh
mkdir -p /root/sdk && cd /root/sdk
curl -fsSL -o flutter.tar.xz "https://storage.googleapis.com/flutter_infra_release/releases/stable/linux/flutter_linux_${FLUTTER_VERSION}-stable.tar.xz"
tar xf flutter.tar.xz && rm flutter.tar.xz
git config --global --add safe.directory /root/sdk/flutter
```
Run the commands below from `mobile/`. The scripts are in
`.claude/skills/mobile-screen/scripts/`. Keep pictures and throwaway files
in your scratchpad, never in the repository: below, `$SCRATCH` is that
folder, as an absolute path.

## 1. Know what it should look like

- Read the part of `docs/spec/mobile-app-look.md` for the area, and the
  screen's row in `docs/spec/app-screens-to-redesign.md`.
- If the founder gave a drawing, it wins. Note its layout: what sits in the
  top card, which rows, which buttons, the order of the sections.
- Find the closest screen already in the new look (the list in
  `mobile/CLAUDE.md`, or `test/looks/` for pictures of them) and read its
  code. Copy its structure; do not invent a new one.

## 2. Build it from the kit

Use the parts listed in `mobile/CLAUDE.md` ("The parts to build with"):
`TkPageScreen`, `TkSection` + `TkListRow`, `TkTopCard`, `TkFormGroup` +
`TkKitField`, `TkCta`, `TkButtonRow`, `TkSmallButton`, `TkLink(bold: true)`,
`TkCallout`, `TkChips`, `CardPill`, `TkFormSheet`, `pickOneOf`,
`pickAction`, `askSure`. Colours from the theme and `TkLook.of(context)`,
icons `'b-…'`. If a part the drawing needs is missing, add it to the kit
(`lib/kit/`) and to the table in `mobile/CLAUDE.md`, not inline in one
screen.

Keep what the screen does: the same server calls, the same words where the
tests look for them, the same semantics labels. Change the look, not the
behaviour, unless asked.

## 3. Draw it and look at it

1. Copy the preview template and fill in `screens` (and `answer` for what
   the screen reads beyond the recorded fixture):

   ```sh
   cp ../.claude/skills/mobile-screen/scripts/preview_test.dart test/zz_preview_test.dart
   flutter test test/zz_preview_test.dart --dart-define=OUT=$SCRATCH/after
   ```

   For a sheet or a state reached by tapping, add the taps after the first
   pumps (see how `test/everyone_test.dart` opens the switcher).
2. Lay the pictures out and **open them** (Read the PNG):

   ```sh
   python3 ../.claude/skills/mobile-screen/scripts/sheet.py $SCRATCH/sheet.png $SCRATCH/after
   ```
3. Check each picture against the drawing and the screens around it:
   - the head: Back as "‹ Page", the title large, no paragraph under it;
   - spacing, rounded panels, rows of one or two lines, nothing cut off;
   - **dark**: every word readable (your own pine bubble's words white in
     light; nothing dark on dark), panels visible against the ground, code
     boxes and fields visible;
   - scrolled down (`-1.png`): the end of the page looks finished;
   - no "Something went wrong" (if there is, it is the preview's stand-in
     server missing an answer or a required field: fix `answer`).
4. Fix, draw again, look again, until it matches. Then
   `rm test/zz_preview_test.dart`.

## 4. Check nothing else changed

A change to a kit part or a shared widget changes other screens. Draw the
same previews before your change and after it, and compare:

```sh
git stash; flutter test test/zz_preview_test.dart --dart-define=OUT=$SCRATCH/before; git stash pop
python3 ../.claude/skills/mobile-screen/scripts/compare.py $SCRATCH/before $SCRATCH/after $SCRATCH/changed.png
```

(For a PR, draw on a `git worktree` of `main` and on the branch.) Look at
every pair it lists: each difference must be one you meant.

The screenshot tests do this for the main screens on every run: `flutter
test` fails when one of the pictures in `test/looks/` changes. When it
fails, open `build/looks/NAME-kept.png`, `NAME-new.png` and
`NAME-differs.png` (the differences in red):
- not meant: fix your change;
- meant: write the pictures again, **look at the new ones**, and say so in
  the PR:

  ```sh
  flutter test --update-goldens test/<the test that takes it>_test.dart
  ```

For a new screen, add `await looks(t, 'name');` (from `test/looks.dart`)
in one of its tests, where the screen shows real data, then write its
pictures with `--update-goldens` and look at them. The test's root widget
must be the `TaskiemApp` it pumped. If the test checks something after that
line that time would change (unread counts, a toast), put the call at the
end of the test instead.

## 5. Finish

- `flutter analyze` clean; `flutter test` passes (about 12 minutes; the
  screen's own test file first).
- Large text: `test/sizes_test.dart`; what can be pressed:
  `test/targets_test.dart`.
- Mark the screen's row in `docs/spec/app-screens-to-redesign.md`, and add
  to `docs/spec/mobile-app-look.md` anything new about the look.
- If something here was wrong or missing, fix this skill or
  `mobile/CLAUDE.md` in the same change, so the next session knows.
