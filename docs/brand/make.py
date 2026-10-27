# Makes Taskiem's brand files (decision 0043), for each brand direction
# kept to experiment with: the wordmark outlined from Plus Jakarta Sans
# ExtraBold (SIL OFL), and the mark, as SVG, in docs/brand/<brand>/.
# Needs fonttools and brotli (pip install fonttools brotli):
#
#   python3 docs/brand/make.py tools/docsite/assets/fonts/plus-jakarta-sans.woff2 docs/brand
import sys, os
from fontTools.ttLib import TTFont
from fontTools.varLib.instancer import instantiateVariableFont
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools.pens.boundsPen import BoundsPen

font_path, out = sys.argv[1], sys.argv[2]
WHITE, BLACK = "#ffffff", "#141414"

f = instantiateVariableFont(TTFont(font_path), {"wght": 800})
gs, cmap, upm = f.getGlyphSet(), f.getBestCmap(), f["head"].unitsPerEm
cap = f["OS/2"].sCapHeight
xh = f["OS/2"].sxHeight


def word(text, track):
    """The text's outlines, its width, and where its dotless i sits."""
    paths, x, idot = [], 0, None
    for ch in text:
        name = cmap[ord(ch)]
        pen = SVGPathPen(gs)
        gs[name].draw(TransformPen(pen, (1, 0, 0, -1, x, 0)))
        paths.append(pen.getCommands())
        if ch == "ı":
            bp = BoundsPen(gs)
            gs[name].draw(bp)
            x0, _, x1, _ = bp.bounds
            side = round((x1 - x0) * 1.2)
            idot = (round(x + (x0 + x1) / 2 - side / 2), -(xh + round(0.035 * upm) + side), side)
        x += f["hmtx"][name][0] + track * upm
    return " ".join(paths), round(x - track * upm), idot


def wordmark(text, track, fill, dotfill=None, round_dot=False):
    letters, width, idot = word(text, track)
    top = -cap - 30 if idot is None else idot[1] - 10
    if idot is None:
        dot = ""
    elif round_dot:
        r = idot[2] / 2
        dot = f'<circle cx="{idot[0] + r:g}" cy="{idot[1] + r:g}" r="{r:g}" fill="{dotfill or fill}"/>'
    else:
        dot = f'<rect x="{idot[0]}" y="{idot[1]}" width="{idot[2]}" height="{idot[2]}" fill="{dotfill or fill}"/>'
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 {top} {width} {-top + 24}" role="img" aria-label="Taskiem">'
            f'<path fill="{fill}" d="{letters}"/>{dot}</svg>\n')


def svg(body):
    return f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" role="img" aria-label="Taskiem">{body}</svg>\n'


# --- B, relay (the default): a "t" whose foot hands on to a dot, in pine and
# lime; the wordmark's i carries the same round dot.
def relay():
    pine, lime = "#0d3b2e", "#c8f169"
    t = ('<path d="M27 40H61" stroke="{t}" stroke-width="11" stroke-linecap="round"/>'
         '<path d="M42 22V58Q42 74 58 74H64" stroke="{t}" stroke-width="11" stroke-linecap="round" fill="none"/>'
         '<circle cx="78" cy="74" r="7" fill="{d}"/>')
    # At 16 to 24 px the strokes are thicker and the foot shorter, so the dot
    # keeps a gap from it.
    small = ('<path d="M24 40H64M42 18V58Q42 76 60 76H62" stroke="{t}" stroke-width="16" stroke-linecap="round" fill="none"/>'
             '<circle cx="81" cy="76" r="9" fill="{d}"/>')
    round_ = ('<path d="M30 41H60" stroke="{t}" stroke-width="10" stroke-linecap="round"/>'
              '<path d="M43 25V57Q43 71 57 71H62" stroke="{t}" stroke-width="10" stroke-linecap="round" fill="none"/>'
              '<circle cx="74" cy="71" r="6" fill="{d}"/>')
    return {
        "wordmark.svg": wordmark("taskıem", -0.035, pine, round_dot=True),
        "wordmark-reversed.svg": wordmark("taskıem", -0.035, WHITE, lime, round_dot=True),
        "wordmark-mono.svg": wordmark("taskıem", -0.035, BLACK, round_dot=True),
        "mark.svg": svg(f'<rect width="100" height="100" rx="26" fill="{pine}"/>' + t.format(t=lime, d=WHITE)),
        "mark-reversed.svg": svg(f'<rect width="100" height="100" rx="26" fill="{lime}"/>' + t.format(t=pine, d=pine)),
        "mark-mono.svg": svg(f'<rect width="100" height="100" rx="26" fill="{BLACK}"/>' + t.format(t=WHITE, d=WHITE)),
        "mark-small.svg": svg(f'<rect width="100" height="100" rx="24" fill="{pine}"/>' + small.format(t=lime, d=WHITE)),
        "avatar.svg": svg(f'<rect width="100" height="100" fill="{pine}"/>' + round_.format(t=lime, d=WHITE)),
    }


# --- A, seal: a round stamp with a tilted T, vermilion and ink.
def seal():
    ink, red = "#1f2a6b", "#d9461f"
    t = '<g transform="rotate(-8 50 50)"><path d="M29 31H71V42.5H56.5V71H43.5V42.5H29Z" fill="{t}"/></g>'
    ring = '<circle cx="50" cy="50" r="40.5" fill="none" stroke="{t}" stroke-width="2.6"/>'
    small = '<g transform="rotate(-8 50 50)"><path d="M26 28H74V44H58V76H42V44H26Z" fill="{t}"/></g>'
    return {
        "wordmark.svg": wordmark("Taskiem", -0.03, ink),
        "wordmark-reversed.svg": wordmark("Taskiem", -0.03, WHITE),
        "wordmark-mono.svg": wordmark("Taskiem", -0.03, BLACK),
        "mark.svg": svg(f'<circle cx="50" cy="50" r="48" fill="{red}"/>' + ring.format(t=WHITE) + t.format(t=WHITE)),
        "mark-reversed.svg": svg(f'<circle cx="50" cy="50" r="48" fill="{WHITE}"/>' + ring.format(t=red) + t.format(t=red)),
        "mark-mono.svg": svg(f'<circle cx="50" cy="50" r="48" fill="{BLACK}"/>' + ring.format(t=WHITE) + t.format(t=WHITE)),
        "mark-small.svg": svg(f'<circle cx="50" cy="50" r="50" fill="{red}"/>' + small.format(t=WHITE)),
        "avatar.svg": svg(f'<rect width="100" height="100" fill="{red}"/>' + ring.format(t=WHITE) + t.format(t=WHITE)),
    }


# --- C, signal: "taskiem" with a saffron square for the i's dot, in ink.
def signal():
    ink, saffron = "#121212", "#ffb21e"
    t = ('<path d="M24 42H60" stroke="{t}" stroke-width="12"/>'
         '<path d="M40 20V62Q40 78 56 78H62" stroke="{t}" stroke-width="12" fill="none"/>'
         '<rect x="66" y="20" width="15" height="15" fill="{q}"/>')
    small = ('<path d="M18 44H62M38 16V64Q38 82 58 82H64" stroke="{t}" stroke-width="17" fill="none"/>'
             '<rect x="66" y="14" width="20" height="20" fill="{q}"/>')
    return {
        "wordmark.svg": wordmark("taskıem", -0.04, ink, saffron),
        "wordmark-reversed.svg": wordmark("taskıem", -0.04, WHITE, saffron),
        "wordmark-mono.svg": wordmark("taskıem", -0.04, BLACK),
        "mark.svg": svg(f'<rect width="100" height="100" rx="24" fill="{ink}"/>' + t.format(t=WHITE, q=saffron)),
        "mark-reversed.svg": svg(f'<rect width="100" height="100" rx="24" fill="{saffron}"/>' + t.format(t=ink, q=ink)),
        "mark-mono.svg": svg(f'<rect width="100" height="100" rx="24" fill="{BLACK}"/>' + t.format(t=WHITE, q=WHITE)),
        "mark-small.svg": svg(f'<rect width="100" height="100" rx="22" fill="{ink}"/>' + small.format(t=WHITE, q=saffron)),
        "avatar.svg": svg(f'<rect width="100" height="100" fill="{ink}"/><g transform="translate(6 4) scale(.88)">' + t.format(t=WHITE, q=saffron) + "</g>"),
    }


for brand, files in {"relay": relay(), "seal": seal(), "signal": signal()}.items():
    d = os.path.join(out, brand)
    os.makedirs(d, exist_ok=True)
    for name, body in files.items():
        with open(os.path.join(d, name), "w") as fh:
            fh.write(body)
    print(brand, len(files))
