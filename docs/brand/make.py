# Makes Taskiem's brand files (decision 0043): the wordmark outlined from
# Plus Jakarta Sans ExtraBold (SIL OFL), and the mark, as SVG. Needs
# fonttools and brotli (pip install fonttools brotli):
#
#   python3 docs/brand/make.py tools/docsite/assets/fonts/plus-jakarta-sans.woff2 docs/brand
import sys, os
from fontTools.ttLib import TTFont
from fontTools.varLib.instancer import instantiateVariableFont
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen

font_path, out = sys.argv[1], sys.argv[2]
os.makedirs(out, exist_ok=True)
PINE, LIME, WHITE, BLACK = "#0d3b2e", "#c8f169", "#ffffff", "#141414"

f = instantiateVariableFont(TTFont(font_path), {"wght": 800})
gs, cmap, upm = f.getGlyphSet(), f.getBestCmap(), f["head"].unitsPerEm
track = -0.035 * upm
paths, x = [], 0
for ch in "taskiem":
    name = cmap[ord(ch)]
    pen = SVGPathPen(gs)
    gs[name].draw(TransformPen(pen, (1, 0, 0, -1, x, 0)))
    paths.append(pen.getCommands())
    x += f["hmtx"][name][0] + track
width = round(x - track)
top = -f["OS/2"].sCapHeight - 30
letters = " ".join(paths)

def wordmark(fill):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 {top} {width} {-top + 24}" role="img" aria-label="taskiem">'
            f'<path fill="{fill}" d="{letters}"/></svg>\n')

# The mark: a "t" whose foot hands on to a dot. Small sizes drop the dot
# and thicken the strokes so the t still reads at 16 px.
MARK = ('<path d="M27 40H61" stroke="{t}" stroke-width="11" stroke-linecap="round"/>'
        '<path d="M42 22V58Q42 74 58 74H64" stroke="{t}" stroke-width="11" stroke-linecap="round" fill="none"/>'
        '<circle cx="78" cy="74" r="7" fill="{d}"/>')
SMALL = '<path d="M24 40H64M42 18V60Q42 78 60 78H70" stroke="{t}" stroke-width="16" stroke-linecap="round" fill="none"/>'
ROUND = ('<path d="M30 41H60" stroke="{t}" stroke-width="10" stroke-linecap="round"/>'
         '<path d="M43 25V57Q43 71 57 71H62" stroke="{t}" stroke-width="10" stroke-linecap="round" fill="none"/>'
         '<circle cx="74" cy="71" r="6" fill="{d}"/>')

def svg(body):
    return f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" role="img" aria-label="Taskiem">{body}</svg>\n'

files = {
    "taskiem-wordmark.svg": wordmark(PINE),
    "taskiem-wordmark-reversed.svg": wordmark(WHITE),
    "taskiem-wordmark-mono.svg": wordmark(BLACK),
    "taskiem-mark.svg": svg(f'<rect width="100" height="100" rx="26" fill="{PINE}"/>' + MARK.format(t=LIME, d=WHITE)),
    "taskiem-mark-reversed.svg": svg(f'<rect width="100" height="100" rx="26" fill="{LIME}"/>' + MARK.format(t=PINE, d=PINE)),
    "taskiem-mark-mono.svg": svg(f'<rect width="100" height="100" rx="26" fill="{BLACK}"/>' + MARK.format(t=WHITE, d=WHITE)),
    "taskiem-mark-small.svg": svg(f'<rect width="100" height="100" rx="24" fill="{PINE}"/>' + SMALL.format(t=LIME)),
    "taskiem-avatar.svg": svg(f'<rect width="100" height="100" fill="{PINE}"/>' + ROUND.format(t=LIME, d=WHITE)),
}
for n, body in files.items():
    open(os.path.join(out, n), "w").write(body)
print("ok", width)
