#!/usr/bin/env python3
"""Lays PNG files side by side with their names, to look at many at once.

    python3 sheet.py OUT.png a.png b.png ...        # one row per 6 pictures
    python3 sheet.py OUT.png DIR                    # every PNG in DIR
    python3 sheet.py --width 300 --cols 4 OUT.png DIR

Each picture is shrunk to --width (default 260) px wide, so a sheet of
twelve stays readable. Needs Pillow (pip install pillow).
"""
import argparse
import glob
import os

from PIL import Image, ImageDraw


def main():
    p = argparse.ArgumentParser()
    p.add_argument("out")
    p.add_argument("inputs", nargs="+")
    p.add_argument("--width", type=int, default=260)
    p.add_argument("--cols", type=int, default=6)
    a = p.parse_args()
    files = []
    for i in a.inputs:
        files += sorted(glob.glob(os.path.join(i, "*.png"))) if os.path.isdir(i) else [i]
    if not files:
        raise SystemExit("no PNG files")
    shots = []
    for f in files:
        im = Image.open(f).convert("RGB")
        im = im.resize((a.width, round(im.height * a.width / im.width)))
        shots.append((os.path.basename(f), im))
    rows = [shots[k:k + a.cols] for k in range(0, len(shots), a.cols)]
    heights = [max(im.height for _, im in r) + 24 for r in rows]
    sheet = Image.new("RGB", (a.cols * (a.width + 10), sum(heights)), (200, 30, 30))
    d = ImageDraw.Draw(sheet)
    y = 0
    for r, h in zip(rows, heights):
        for n, (name, im) in enumerate(r):
            x = n * (a.width + 10)
            d.text((x + 2, y + 5), name[:44], fill="white")
            sheet.paste(im, (x, y + 22))
        y += h
    sheet.save(a.out)
    print(f"{a.out}: {len(files)} pictures")


if __name__ == "__main__":
    main()
