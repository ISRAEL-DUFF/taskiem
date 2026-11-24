#!/usr/bin/env python3
"""Build a series package (video/series/PACKAGE.md) from its films and scripts.

    python3 video/series/build_package.py fashion --films <frames root> --out <dir> [--episodes 1,2] [--zip] [--force]

Reads video/series/<series>/series.json and episodes/NN-slug/episode.json, the
films' frames and cues.json (written by mobile/test/film.dart), and writes, per
episode: clean footage (9:16 and 16:9), a captioned preview with music,
captions (.srt and PNGs), narration (English, Pidgin draft), Flow prompts, the
recap/bridge lines and the combined cues; plus cards, music, the cast files and
the README and EDIT-GUIDE. With --zip, a series-<name>.zip beside --out.
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import zipfile
from pathlib import Path

from PIL import Image, ImageDraw

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent.parent
sys.path.insert(0, str(HERE))
import cards  # noqa: E402

COMPOSE = ROOT / "video" / "phone" / "compose.py"
MUSIC = ROOT / "video" / "music" / "compose.py"
SHAPES = {"9x16": dict(canvas="1080x1920", phone_height=1640), "16x9": dict(canvas="1920x1080", phone_height=980)}
STYLE = ("Stylised semi-realistic animated-feature character art, soft 3D-like shading, warm natural light, "
         "expressive eyes, no outlines.")
GUARD = ("Rich, luminous, warm skin tone with warm highlights, true to the description. Exactly one person, exactly as "
         "described, natural hands with five fingers. No text, no logos, no watermark.")


def run(cmd, **kw):
    r = subprocess.run([str(c) for c in cmd], capture_output=True, text=True, **kw)
    if r.returncode != 0:
        raise SystemExit(f"{' '.join(str(c) for c in cmd[:3])}...: {r.stderr[-1500:]}")
    return r.stdout


def ffprobe_duration(p):
    return float(run(["ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", p]).strip())


def mmss(t):
    return f"{int(t // 60)}:{t % 60:04.1f}"


def srt_time(t):
    ms = round(t * 1000)
    return f"{ms // 3600000:02d}:{ms // 60000 % 60:02d}:{ms // 1000 % 60:02d},{ms % 1000:03d}"


# ---- the cast: master prompts --------------------------------------------
def masters():
    """name -> master prompt, from the series cast files and the bible."""
    out = {}
    for f in [ROOT / "video" / "characters" / "bible.md"] + sorted((ROOT / "video" / "series").glob("*/characters.md")):
        text = f.read_text()
        for m in re.finditer(r"^## (?:\d+\. )?([^:\n]+?)(?::[^\n]*)?\n(.*?)(?=^## |\Z)", text, re.S | re.M):
            name, body = m.group(1).strip(), m.group(2)
            mp = re.search(r"\*\*Master prompt \(copy exactly\):\*\*\n\n((?:> .*\n?)+)", body)
            if mp:
                out[name.split(" (")[0].strip().lower()] = " ".join(l[2:].strip() for l in mp.group(1).strip().split("\n")).replace("**", "")
    return out


# ---- a film ---------------------------------------------------------------
class Film:
    def __init__(self, root, name):
        self.name = name
        self.dir = Path(root) / name
        self.meta = json.loads((self.dir / "cues.json").read_text())
        self.fps = self.meta["fps"]
        self.dur = self.meta["frames"] / self.fps
        self.says = [c for c in self.meta["cues"] if c["kind"] == "say"]


def compose(film, out, shape, captions, theme="light", force=False):
    if out.exists() and not force:
        return out
    spec = SHAPES[shape]
    cmd = ["python3", COMPOSE, "--frames", film.dir, "--out", out, "--canvas", spec["canvas"], "--phone-height", spec["phone_height"], "--theme", theme]
    if captions:
        cmd.append("--captions")
    out.parent.mkdir(parents=True, exist_ok=True)
    run(cmd)
    return out


def concat(files, out):
    lst = out.with_suffix(".txt")
    lst.write_text("".join(f"file '{Path(f).resolve()}'\n" for f in files))
    run(["ffmpeg", "-loglevel", "error", "-y", "-f", "concat", "-safe", "0", "-i", lst, "-c:v", "libx264", "-crf", "17", "-pix_fmt", "yuv420p", "-movflags", "+faststart", out])
    lst.unlink()
    return out


# ---- reel: Episode 0 from the other episodes' footage ----------------------
def make_reel(spec, films_root, work):
    """Frames and cues for a reel of beats: [{film, from, to, tag}]."""
    work.mkdir(parents=True, exist_ok=True)
    for old in work.glob("f*.png"):
        old.unlink()
    n, cues, fps = 0, [], 30
    for beat in spec:
        src = Film(films_root, beat["film"])
        a, b = int(beat["from"] * fps), min(src.meta["frames"], int(beat["to"] * fps))
        base = n
        cues.append({"kind": "say", "t": n / fps, "text": beat["tag"], "who": beat.get("who", "")})
        for i in range(a, b):
            link = work / f"f{n:05d}.png"
            os.symlink(src.dir / f"f{i:05d}.png", link)
            n += 1
        for c in src.meta["cues"]:
            if c["kind"] in ("say",):
                continue
            t = c["t"]
            if c["kind"] == "system":
                if a / fps <= t < b / fps:
                    cues.append({**c, "t": base / fps + (t - a / fps)})
                continue
            if a / fps - 0.7 <= t < b / fps:
                cues.append({**c, "t": base / fps + (t - a / fps)})
    meta = {"name": "reel", "fps": fps, "frames": n, "width": 390, "height": 844, "statusBar": 47, "gestureBar": 24, "cues": sorted(cues, key=lambda c: c["t"])}
    (work / "cues.json").write_text(json.dumps(meta, indent=1))
    return Film(work.parent, work.name)


# ---- one episode ------------------------------------------------------------
def build_episode(ep, series, args, out_root, cast):
    num, slug, title = ep["number"], ep["slug"], ep["title"]
    ed = out_root / "episodes" / f"{num:02d}-{slug}"
    ed.mkdir(parents=True, exist_ok=True)
    print(f"== Episode {num}: {title}", file=sys.stderr)

    films = []
    if ep.get("reel"):
        work = out_root / "_work" / f"reel-{num:02d}"
        films.append(make_reel(ep["reel"], args.films, work))
        films[0].name = f"reel-{num:02d}"
    else:
        films = [Film(args.films, f) for f in ep["films"]]

    # clean footage and captioned preview, per film, per shape
    shapes = [s for s in args.shapes.split(",")]
    clean, capt = {s: [] for s in shapes}, []
    for f in films:
        for s in shapes:
            clean[s].append(compose(f, ed / "parts" / f"{f.name}-{s}.mp4", s, False, force=args.force))
        capt.append(compose(f, ed / "parts" / f"{f.name}-preview-9x16.mp4", "9x16", True, force=args.force))
    for s in shapes:
        concat(clean[s], ed / f"footage-{s}.mp4")
    total = sum(f.dur for f in films)

    # music for this episode
    music_dir = out_root / "music"
    music_dir.mkdir(exist_ok=True)
    wav = music_dir / f"ep-{num:02d}.wav"
    if not wav.exists() or args.force:
        t0, scenes = 0.0, []
        for i, f in enumerate(films, 1):
            scenes.append({"scene": f"{i:02d}-{f.name}", "start": round(t0, 3), "duration": round(f.dur, 3)})
            t0 += f.dur
        work = out_root / "_work" / f"music-{num:02d}"
        work.mkdir(parents=True, exist_ok=True)
        (work / "film.json").write_text(json.dumps({"moods": {str(i): ep.get("mood", "sparse") for i in range(1, len(films) + 1)}}))
        (work / "timeline.json").write_text(json.dumps({"length": total, "scenes": scenes}))
        run(["python3", MUSIC, work / "film.json", work / "timeline.json", wav, "--no-sfx"])
    # the preview: captioned footage with the music under it
    prev_silent = concat(capt, ed / "_preview-silent.mp4")
    run(["ffmpeg", "-loglevel", "error", "-y", "-i", prev_silent, "-i", wav, "-filter_complex", "[1:a]volume=0.55,afade=t=in:d=0.5,afade=t=out:st=%.2f:d=1.2[a]" % max(0, total - 1.2),
         "-map", "0:v", "-map", "[a]", "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-shortest", "-movflags", "+faststart", ed / "preview-9x16.mp4"])
    prev_silent.unlink()

    # captions: srt, pngs, narration, cues
    offset, rows, all_cues = 0.0, [], []
    for f in films:
        for k, c in enumerate(f.says):
            end = f.says[k + 1]["t"] if k + 1 < len(f.says) else f.dur
            rows.append({"film": f.name, "idx": k, "start": offset + c["t"], "end": offset + end, "text": c["text"], "who": c.get("who", "")})
        for c in f.meta["cues"]:
            all_cues.append({**c, "film": f.name, "t": offset + c["t"]})
        offset += f.dur
    (ed / "captions.srt").write_text("".join(f"{i}\n{srt_time(r['start'])} --> {srt_time(r['end'] - 0.05)}\n{r['text']}\n\n" for i, r in enumerate(rows, 1)))
    (ed / "cues.json").write_text(json.dumps({"episode": num, "films": [{"name": f.name, "duration": f.dur} for f in films], "cues": all_cues}, indent=1))
    for variant, ink in (("dark-text", (14, 26, 20, 255)), ("white-text", (255, 255, 255, 255))):
        d = ed / "captions" / variant
        d.mkdir(parents=True, exist_ok=True)
        for i, r in enumerate(rows, 1):
            caption_png(d / f"{i:03d}.png", r["text"], ink)

    narr = ep.get("narration", {})
    for lang, label in (("en", "English"), ("pcm", "Pidgin (draft: a native speaker must check it)")):
        lines = [f"Episode {num}: {title}. Narration, {label}.", "One line per caption. Times are in the footage joined in order.", ""]
        said = []
        for r in rows:
            spoken = (narr.get(r["film"], {}).get(lang) or [])
            said.append(spoken[r["idx"]] if r["idx"] < len(spoken) else (r["text"] if lang == "en" else ""))
        for k, r in enumerate(rows):
            line = said[k]
            # a line may run on into the next captions that have no narration of their own
            end = r["end"]
            j = k + 1
            while j < len(rows) and not said[j] and rows[j]["film"] == r["film"]:
                end = rows[j]["end"]
                j += 1
            budget = end - r["start"]
            lines.append(f"[{mmss(r['start'])} to {mmss(end)}, {budget:.1f} s] caption: {r['text']}")
            lines.append(f"   SAY: {line or ('(covered by the line above)' if k else '(to write)')}")
            if line:
                wps = len(line.split()) / budget
                if wps > 2.6:
                    lines.append(f"   NOTE: {wps:.1f} words a second is fast; trim it or hold the picture longer")
            lines.append("")
        (ed / ("narration-en.txt" if lang == "en" else "narration-pidgin-draft.txt")).write_text("\n".join(lines))

    # flow prompts
    fp = [f"# Episode {num}: {title}. Character clips for Flow", "",
          "Generate each clip with Flow's Ingredients to Video, attaching the character's approved reference images; paste the whole prompt. Keep each clip to its length.", ""]
    for c in ep.get("flow", []):
        master = cast.get(c["who"].lower(), f"(missing cast entry for {c['who']})")
        fp += [f"## {c['id']}: {c['purpose']} ({c['seconds']} s)", f"Goes: {c['goes']}", "", "```",
               f"{master}. {c['action']}. Setting: {c['setting']}. Camera: {c['camera']}. Clip length: about {c['seconds']} seconds. {STYLE} Audio: natural ambient sound only, no speech, no music. {GUARD}",
               "```", ""]
    if not ep.get("flow"):
        fp.append("(No character clips planned for this episode.)")
    (ed / "flow-prompts.md").write_text("\n".join(fp))

    # recap, bridge, next
    b = [f"# Episode {num}: {title}. Recap and bridge", ""]
    for key, label in (("recap", "Opening recap (3 to 5 s)"), ("bridge", "Bridge to the next episode (5 to 8 s)")):
        v = ep.get(key)
        if v:
            b += [f"## {label}", f"English: {v['en']}", f"Pidgin (draft): {v.get('pcm', '(to write)')}", ""]
    nxt = ep.get("next")
    if nxt:
        b += ["## Next card", f"Episode {nxt['number']}: {nxt['title']}", ""]
    (ed / "bridge.md").write_text("\n".join(b))

    # cards
    cd = out_root / "cards"
    cd.mkdir(exist_ok=True)
    eyebrow = f"{series['name']} · Episode {num}" if num else f"{series['name']} · Start here"
    for s, spec in SHAPES.items():
        size = tuple(int(v) for v in spec["canvas"].split("x"))
        p = cards.card(cd / f"ep-{num:02d}-title-{s}.png", size, eyebrow, ep["headline"], ep.get("sub", ""))
        cards.to_video(str(p), str(cd / f"ep-{num:02d}-title-{s}.mp4"), 3.0)
        if nxt:
            p = cards.card(cd / f"ep-{num:02d}-next-{s}.png", size, f"Next · Episode {nxt['number']}", nxt.get("headline", nxt["title"]), (ep.get("bridge") or {}).get("en", ""), dark=True)
            cards.to_video(str(p), str(cd / f"ep-{num:02d}-next-{s}.mp4"), 3.5)
    return {"num": num, "title": title, "slug": slug, "films": [(f.name, f.dur) for f in films], "total": total, "ep": ep, "rows": rows}


def caption_png(path, text, ink):
    W, pad = 1080, 80
    f = cards.font("Inter-latin.ttf", 46)
    d0 = ImageDraw.Draw(Image.new("RGBA", (1, 1)))
    lines = cards.wrap(d0, text, f, W - 2 * pad)
    img = Image.new("RGBA", (W, 62 * len(lines) + 40), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    for i, ln in enumerate(lines):
        d.text((W / 2, 20 + i * 62), ln, font=f, fill=ink, anchor="mt")
    img.save(path)


# ---- the series -------------------------------------------------------------
def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("series")
    ap.add_argument("--films", required=True, help="the folder of film frame folders")
    ap.add_argument("--out", required=True)
    ap.add_argument("--episodes", help="e.g. 1,2 (default: all that are ready)")
    ap.add_argument("--shapes", default="9x16,16x9")
    ap.add_argument("--zip", action="store_true")
    ap.add_argument("--force", action="store_true")
    ap.add_argument("--partial", action="store_true", help="build an episode from the films that exist")
    args = ap.parse_args()

    sdir = HERE / args.series
    series = json.loads((sdir / "series.json").read_text())
    out = Path(args.out) / f"series-{args.series}"
    out.mkdir(parents=True, exist_ok=True)
    cast = masters()
    eps = []
    for p in sorted((sdir / "episodes").glob("*/episode.json")):
        ep = json.loads(p.read_text())
        eps.append(ep)
    want = {int(x) for x in args.episodes.split(",")} if args.episodes else None
    built = []
    for ep in eps:
        if want is not None and ep["number"] not in want:
            continue
        if args.partial and not ep.get("reel"):
            ep["films"] = [f for f in ep["films"] if (Path(args.films) / f / "cues.json").exists()]
        have = ep.get("films", []) if not ep.get("reel") else sorted({b["film"] for b in ep["reel"]})
        if any(not (Path(args.films) / f / "cues.json").exists() for f in have):
            print(f"-- Episode {ep['number']} skipped: films not ready ({have})", file=sys.stderr)
            continue
        built.append(build_episode(ep, series, args, out, cast))

    # series cards
    cd = out / "cards"
    cd.mkdir(exist_ok=True)
    for s, spec in SHAPES.items():
        size = tuple(int(v) for v in spec["canvas"].split("x"))
        for name, eyebrow, head, sub, dark in (
            ("series-title", series["name"], series["card_headline"], series["card_sub"], False),
            ("cta", "Taskiem", series["cta"], series["cta_sub"], True),
        ):
            p = cards.card(cd / f"{name}-{s}.png", size, eyebrow, head, sub, dark=dark)
            cards.to_video(str(p), str(cd / f"{name}-{s}.mp4"), 3.5)

    # the cast and the README / EDIT-GUIDE
    cdir = out / "characters"
    cdir.mkdir(exist_ok=True)
    for f in (sdir / "characters.md", ROOT / "video" / "characters" / "bible.md", ROOT / "video" / "characters" / "pronunciation.md", ROOT / "video" / "characters" / "flow-guide.md"):
        if f.exists():
            shutil.copy(f, cdir / (f.name if f.name != "characters.md" else f"{args.series}-characters.md"))
    write_readme(out, series, built)
    write_edit_guide(out, series, built)
    print(f"built {len(built)} episode(s) into {out}", file=sys.stderr)
    if args.zip:
        z = Path(args.out) / f"series-{args.series}.zip"
        with zipfile.ZipFile(z, "w", zipfile.ZIP_STORED) as zf:
            for p in sorted(out.rglob("*")):
                if p.is_file() and "_work" not in p.parts and p.name != "_preview-silent.mp4" and "parts" not in p.parts:
                    zf.write(p, p.relative_to(out.parent))
        print(z)


def write_readme(out, series, built):
    L = [f"# {series['title']}", "", series["blurb"], "",
         "Everything is provided as parts for your own video editor. See `EDIT-GUIDE.md` for the timeline of each episode.",
         "", "## Episodes", "", "| # | Episode | Length | Footage |", "| --- | --- | --- | --- |"]
    for b in built:
        L.append(f"| {b['num']} | {b['title']} | {mmss(b['total'])} | `episodes/{b['num']:02d}-{b['slug']}/` |")
    L += ["", "## In each episode folder", "",
          "| File | What it is |", "| --- | --- |",
          "| `footage-9x16.mp4`, `footage-16x9.mp4` | The app in a phone, clean: no captions, no sound |",
          "| `parts/` | The same footage one flow at a time (and a captioned 9:16 preview of each) |",
          "| `preview-9x16.mp4` | Captions and music on, to see the pacing |",
          "| `captions.srt`, `captions/` | Every caption, timed; and each as a transparent PNG (dark text, white text) |",
          "| `narration-en.txt` | The English narration, one line per caption, with the time it must fit |",
          "| `narration-pidgin-draft.txt` | A Pidgin draft: a native speaker must check it before recording |",
          "| `flow-prompts.md` | The character clips to make in Flow, and where each goes |",
          "| `bridge.md` | The recap line, the bridge line and the Next card text |",
          "| `cues.json` | The raw cues (taps, drags, captions) of the footage |", "",
          "`cards/` holds the title, Next, series and call-to-action cards (PNG and 3 s MP4, 9:16 and 16:9). "
          "`music/` holds a track per episode (WAV). `characters/` holds the cast (bible entries, Flow master prompts, pronunciation).",
          "", "## What you add", "",
          "- The character clips: generate them in Flow from `flow-prompts.md` (reference images first, see `characters/flow-guide.md`).",
          "- The narrator's voice: record or generate it from the narration files (English; Pidgin after review).",
          "- Your own cuts, transitions and final mix.", "",
          "## Things to know", "",
          "- The footage is the real app's own screens, drawn by its test runner with the story's data (Zuri Atelier). No real money moves and no real customer data is shown.",
          "- Taskiem works alongside WhatsApp and Telegram; it does not replace them. Say it that way.",
          f"- Call to action: **{series['cta']}**. Confirm the trial length before publishing; the app's own sign-up says 14 days today.",
          "- Phone-only parts (the real fingerprint dialog, push notifications) are drawn in the footage; film them on a device if you want the real thing."]
    (out / "README.md").write_text("\n".join(L) + "\n")


def write_edit_guide(out, series, built):
    L = [f"# Edit guide: {series['title']}", "",
         "Per episode, in order. Times are in the finished episode before you add your own cuts; "
         "the footage and its captions are timed to each other, so keep them together.", ""]
    for b in built:
        ep = b["ep"]
        L += [f"## Episode {b['num']}: {b['title']} ({mmss(b['total'])} of footage)", ""]
        L.append("| Step | What | File |")
        L.append("| --- | --- | --- |")
        step = 1
        if ep.get("recap"):
            L.append(f"| {step} | Recap line over a still or the last frame of the last episode (3 to 5 s): voice it | `bridge.md` |"); step += 1
        for c in ep.get("flow", []):
            if c["goes"].startswith("before"):
                L.append(f"| {step} | Character clip {c['id']}: {c['purpose']} ({c['seconds']} s) | `flow-prompts.md` |"); step += 1
        L.append(f"| {step} | Title card (3 s) | `cards/ep-{b['num']:02d}-title-*` |"); step += 1
        t = 0.0
        for name, dur in b["films"]:
            L.append(f"| {step} | Footage: {name} ({dur:.0f} s) with its captions; narration from `narration-en.txt` | `parts/{name}-*.mp4` |"); step += 1
        for c in ep.get("flow", []):
            if not c["goes"].startswith("before"):
                L.append(f"| {step} | Character clip {c['id']}: {c['purpose']} ({c['seconds']} s), {c['goes']} | `flow-prompts.md` |"); step += 1
        if ep.get("bridge"):
            L.append(f"| {step} | Bridge line (5 to 8 s): voice it over a character clip | `bridge.md` |"); step += 1
        if ep.get("next"):
            L.append(f"| {step} | Next card (3.5 s) | `cards/ep-{b['num']:02d}-next-*` |"); step += 1
        L.append(f"| {step} | Music under everything, dipped under the voice | `music/ep-{b['num']:02d}.wav` |")
        L.append("")
    (out / "EDIT-GUIDE.md").write_text("\n".join(L) + "\n")


if __name__ == "__main__":
    main()
