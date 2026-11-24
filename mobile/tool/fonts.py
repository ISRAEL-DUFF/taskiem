"""Writes the app's Inter from the web's own font files (docs/spec/mobile-app.md §4.1).

The web bundles Inter from @fontsource-variable/inter as one WOFF2 per
alphabet (latin, latin-ext, vietnamese, greek, cyrillic...), weights 100 to
900 on one axis. Flutter takes TTF, so each of those files is unpacked here,
unchanged, into mobile/assets/fonts/; the app lists them as one family with
the rest as fallbacks, so every letter comes from the same file as on the web.

Run from the repository root after `pnpm install`, with fontTools and brotli
(a tool only; nothing of it ships):

    python3 mobile/tool/fonts.py
"""

import glob
import os
import sys

from fontTools.ttLib import TTFont

SRC = glob.glob("node_modules/.pnpm/@fontsource-variable+inter@*/node_modules/@fontsource-variable/inter")
SUBSETS = ["latin", "latin-ext", "vietnamese", "greek", "greek-ext", "cyrillic", "cyrillic-ext"]
OUT = "mobile/assets/fonts"


def main() -> int:
    if len(SRC) != 1:
        print("fonts.py: expected one @fontsource-variable/inter in node_modules, found", SRC, file=sys.stderr)
        return 1
    os.makedirs(OUT, exist_ok=True)
    for s in SUBSETS:
        f = TTFont(os.path.join(SRC[0], "files", f"inter-{s}-wght-normal.woff2"))
        f.flavor = None
        f.save(os.path.join(OUT, f"Inter-{s}.ttf"))
    lic = os.path.join(SRC[0], "LICENSE")
    with open(lic, encoding="utf-8") as r, open(os.path.join(OUT, "LICENSE-OFL.txt"), "w", encoding="utf-8") as w:
        w.write(r.read())
    print("wrote", len(SUBSETS), "fonts from", SRC[0])
    return 0


if __name__ == "__main__":
    sys.exit(main())
