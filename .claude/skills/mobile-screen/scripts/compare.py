#!/usr/bin/env python3
"""Compares two folders of PNGs drawn by the same preview (before and after
a change, or main and a branch): says how much of each picture differs, and
writes a sheet of the ones that do, each pair side by side.

    python3 compare.py BEFORE_DIR AFTER_DIR OUT.png [--min 0.1]

--min is the share of pixels (in %) under which a picture counts as the
same (default 0.1). Needs Pillow.
"""
import argparse
import os

from PIL import Image, ImageChops, ImageDraw


def main():
    p = argparse.ArgumentParser()
    p.add_argument("before")
    p.add_argument("after")
    p.add_argument("out")
    p.add_argument("--min", type=float, default=0.1)
    a = p.parse_args()
    names = sorted(set(os.listdir(a.before)) | set(os.listdir(a.after)))
    pairs = []
    for n in names:
        if not n.endswith(".png"):
            continue
        b, f = os.path.join(a.before, n), os.path.join(a.after, n)
        if not os.path.exists(b) or not os.path.exists(f):
            print(f"{n}: only {'after' if os.path.exists(f) else 'before'}")
            continue
        x, y = Image.open(b).convert("RGB"), Image.open(f).convert("RGB")
        if x.size != y.size:
            print(f"{n}: size {x.size} -> {y.size}")
            pairs.append((n, x, y))
            continue
        diff = ImageChops.difference(x, y).convert("L").point(lambda v: 255 if v > 24 else 0)
        share = 100 * sum(1 for v in diff.getdata() if v) / (x.width * x.height)
        if share >= a.min:
            print(f"{n}: {share:.2f}% differs")
            pairs.append((n, x, y))
    if not pairs:
        print("no picture differs")
        return
    w = 300
    shrunk = []
    for n, x, y in pairs:
        x = x.resize((w, round(x.height * w / x.width)))
        y = y.resize((w, round(y.height * w / y.width)))
        shrunk.append((n, x, y))
    height = max(max(x.height, y.height) for _, x, y in shrunk) + 24
    sheet = Image.new("RGB", (len(shrunk) * (2 * w + 30), height), (200, 30, 30))
    d = ImageDraw.Draw(sheet)
    for k, (n, x, y) in enumerate(shrunk):
        left = k * (2 * w + 30)
        d.text((left + 2, 5), f"{n}: before | after", fill="white")
        sheet.paste(x, (left, 22))
        sheet.paste(y, (left + w + 6, 22))
    sheet.save(a.out)
    print(f"{a.out}: {len(pairs)} pairs")


if __name__ == "__main__":
    main()
