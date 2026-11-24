# What a series zip contains

Decision, 24 November 2026: **one delivery per series**, no second pass. The
person receiving it finishes the video in their own editor (adding the character
clips from Flow and the narrator's voice), so everything is delivered as parts
that can be cut, timed and mixed freely.

```
series-<name>.zip
  README.md                      the series, the episodes in order, how to use the zip
  EDIT-GUIDE.md                  per episode: the timeline, clip by clip
  characters/                    the cast: bible entries and Flow reference prompts
  episodes/
    00-<title>/ ... 10-<title>/
      footage-9x16.mp4           the app in the phone, clean (no captions), no sound
      footage-16x9.mp4           the same on a wide canvas
      preview-9x16.mp4           with captions and music, to see the pacing
      captions.srt               every caption, timed to the footage
      captions/                  each caption as a transparent PNG (for editors that want images)
      narration-en.txt           the English narration, one line per caption, with times
      narration-pidgin-draft.txt Pidgin draft, to be checked by a native speaker
      flow-prompts.md            the character clips to generate, and where each goes
      cues.json                  the film's raw cues (taps, drags, captions) for tooling
      bridge.md                  the recap line, the bridge line, the Next card text
  cards/                         title, Next, end and call-to-action cards (MP4 and PNG, 9:16 and 16:9)
  music/                         the series track (WAV) and a version per episode
```

## What the zip does not contain
- The character clips and the voice: the editor's owner makes them (Flow, a voice
  tool or a person), from `flow-prompts.md` and `narration-*.txt`.
- Things that need a real phone (push notifications, a real passkey, the
  camera and barcode scan): drawn in the footage where the app shows them, and
  listed in the README as "film this on a device" if the real device look is wanted.

## How footage is made
The real app's own screens, drawn by its test runner (`mobile/test/film*.dart`),
put into a phone by `video/phone/compose.py`. See `video/recording-app.md`.
