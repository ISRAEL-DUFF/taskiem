#!/usr/bin/env python3
"""Title, Next and end cards for a series, in the brand: paper ground, pine and lime.

    from cards import card
    card("out.png", (1080, 1920), eyebrow="Fashion · Episode 3",
         headline="Take *orders*", sub="Sell at the counter and from WhatsApp.")

Words in *stars* in the headline are drawn in pine (the rest in ink), as the
film kit's title card does. `to_video` turns a card into a short MP4 (a fade in
and out) for editors that want a clip, not a picture.
"""
import os
import subprocess

from PIL import Image, ImageDraw, ImageFont

PAPER = (239, 234, 226)
PINE = (13, 59, 46)
LIME = (200, 241, 105)
INK = (14, 26, 20)
MUTED = (85, 97, 90)

FONT_DIRS = ["/home/user/taskier/mobile/assets/fonts", "/usr/share/fonts/opentype/inter"]


def font(file, px, fallback="/usr/share/fonts/opentype/inter/Inter-Regular.otf"):
    for d in FONT_DIRS:
        p = os.path.join(d, file)
        if os.path.exists(p):
            return ImageFont.truetype(p, int(px))
    return ImageFont.truetype(fallback, int(px))


def wrap(draw, text, fnt, width):
    """Split text (with *starred* words kept as tokens) into lines that fit."""
    lines, line = [], []
    for tok in text.split():
        trial = " ".join(line + [tok])
        if draw.textlength(trial.replace("*", ""), font=fnt) > width and line:
            lines.append(" ".join(line))
            line = [tok]
        else:
            line.append(tok)
    if line:
        lines.append(" ".join(line))
    return lines


def draw_line(draw, xy, line, fnt, ink, accent, anchor="l"):
    """Draw one line whose *starred* words are in the accent colour."""
    parts, star, cur = [], False, ""
    for ch in line:
        if ch == "*":
            if cur:
                parts.append((cur, star))
            cur, star = "", not star
        else:
            cur += ch
    if cur:
        parts.append((cur, star))
    total = sum(draw.textlength(p, font=fnt) for p, _ in parts)
    x, y = xy
    if anchor == "m":
        x -= total / 2
    for p, s in parts:
        draw.text((x, y), p, font=fnt, fill=accent if s else ink)
        x += draw.textlength(p, font=fnt)


def mark(draw, cx, cy, size, dark=False):
    """The app's mark: a pine rounded square with a lime t (lime with a pine t on pine)."""
    r = size / 2
    draw.rounded_rectangle((cx - r, cy - r, cx + r, cy + r), radius=size * 0.28, fill=LIME if dark else PINE)
    draw.text((cx, cy - size * 0.02), "t", font=font("InterTight-700.ttf", size * 0.72), fill=PINE if dark else LIME, anchor="mm")


def card(out, canvas, eyebrow, headline, sub="", dark=False, footer="", pills=None):
    w, h = canvas
    bg, ink, accent, muted = (PINE, (245, 247, 242), LIME, (190, 205, 196)) if dark else (PAPER, INK, PINE, MUTED)
    img = Image.new("RGB", canvas, bg)
    d = ImageDraw.Draw(img)
    portrait = h >= w
    pad = int(w * (0.1 if portrait else 0.08))
    maxw = w - 2 * pad
    big = int((w if portrait else h) * (0.098 if portrait else 0.115))
    fh = font("InterTight-700.ttf", big)
    fe = font("InterTight-700.ttf", int(big * 0.30))
    fs = font("Inter-latin.ttf", int(big * 0.36))
    # content block, vertically centred
    heads = wrap(d, headline, fh, maxw)
    subs = wrap(d, sub, fs, maxw) if sub else []
    hh = len(heads) * int(big * 1.12)
    sh = len(subs) * int(big * 0.5) + (int(big * 0.4) if subs else 0)
    eh = int(big * 0.62)
    block = eh + int(big * 0.4) + hh + sh
    y = (h - block) // 2
    # eyebrow pill
    tw = d.textlength(eyebrow, font=fe)
    px_, py_ = int(big * 0.26), int(big * 0.12)
    pill = (pad, y, pad + tw + 2 * px_, y + eh)
    d.rounded_rectangle(pill, radius=eh / 2, fill=LIME if dark else PINE)
    d.text((pad + px_, y + eh / 2), eyebrow, font=fe, fill=PINE if dark else LIME, anchor="lm")
    y += eh + int(big * 0.4)
    for ln in heads:
        draw_line(d, (pad, y), ln, fh, ink, accent)
        y += int(big * 1.12)
    if subs:
        y += int(big * 0.4)
        for ln in subs:
            d.text((pad, y), ln, font=fs, fill=muted)
            y += int(big * 0.5)
    if pills:
        y += int(big * 0.5)
        fp = font("Inter-latin.ttf", int(big * 0.3))
        for p in pills:
            d.text((pad, y), p, font=fp, fill=ink)
            y += int(big * 0.46)
    mark(d, pad + big * 0.32, h - pad - big * 0.2, big * 0.64, dark)
    d.text((pad + big * 0.74, h - pad - big * 0.2), footer or "taskiem", font=font("InterTight-700.ttf", big * 0.34), fill=ink, anchor="lm")
    img.save(out)
    return out


def to_video(png, mp4, seconds=3.0):
    subprocess.run(["ffmpeg", "-loglevel", "error", "-y", "-loop", "1", "-framerate", "30", "-t", str(seconds), "-i", png,
                    "-vf", f"fade=t=in:st=0:d=0.35,fade=t=out:st={seconds - 0.35}:d=0.35,format=yuv420p",
                    "-c:v", "libx264", "-crf", "17", mp4], check=True)
    return mp4


if __name__ == "__main__":
    card("/tmp/card-test.png", (1080, 1920), "Fashion · Episode 3", "Take *orders*", "Sell at the counter and from WhatsApp.")
    card("/tmp/card-test-wide.png", (1920, 1080), "Fashion · Episode 3", "Take *orders*", "Sell at the counter and from WhatsApp.", dark=True)
    print("ok")
