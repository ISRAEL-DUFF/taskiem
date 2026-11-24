#!/usr/bin/env python3
"""Put the app's own frames into a phone, with a tap ring and captions.

    python3 video/phone/compose.py --frames /tmp/films/today --out today.mp4
        [--canvas 1080x1920] [--theme light|dark] [--time 13:00]
        [--bg "#efeae2"] [--phone-height 1500] [--top 90] [--captions]

Reads the frames (f00000.png, three pixels to a point) and cues.json that
mobile/test/film.dart wrote, and draws, in this order: a soft shadow, a
generic Android phone (graphite metal frame with a bright edge and antenna
breaks, side buttons, black glass border), the app's picture in the rounded
screen, a lime ring where each finger touches, the status bar, the gesture
bar, the punch-hole camera, the glass sheen, and, with --captions, the
captions under the phone. Then ffmpeg joins them into an MP4.

The phone is generic (no maker's name or shape). One unit is one point of
the app's own screen (390 x 844).
"""
import argparse
import json
import math
import os
import subprocess
import sys
from pathlib import Path

import numpy as np
from PIL import Image, ImageChops, ImageDraw, ImageFilter, ImageFont

LIME = (200, 241, 105)
PINE = (13, 59, 46)
SS = 2  # supersampling for the static parts

# ---- geometry, in screen points -------------------------------------------
SCREEN_W, SCREEN_H = 390, 844
GLASS = 11          # the black border round the screen
FRAME = 6           # the metal frame
EDGE = GLASS + FRAME
BODY_W, BODY_H = SCREEN_W + 2 * EDGE, SCREEN_H + 2 * EDGE
R_BODY = 62
R_SCREEN = 46
BTN = 3.4           # how far a side button stands out
GESTURE = 24        # the gesture bar's height


def font(name_hint, px):
    for p in (
        "/usr/share/fonts/opentype/inter/Inter-Medium.otf",
        "/usr/share/fonts/opentype/inter/Inter-Regular.otf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
    ):
        if name_hint in p and os.path.exists(p):
            return ImageFont.truetype(p, int(px))
    for p in (
        "/usr/share/fonts/opentype/inter/Inter-Regular.otf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
    ):
        if os.path.exists(p):
            return ImageFont.truetype(p, int(px))
    return ImageFont.load_default()


def rrect_mask(size, box, radius, ss=SS):
    """An anti-aliased rounded rectangle, as a mode L image of [size]."""
    w, h = size
    m = Image.new("L", (w * ss, h * ss), 0)
    d = ImageDraw.Draw(m)
    x0, y0, x1, y1 = [v * ss for v in box]
    d.rounded_rectangle((x0, y0, x1, y1), radius=radius * ss, fill=255)
    return m.resize((w, h), Image.LANCZOS)


def gradient(size, stops, angle_deg):
    """A linear gradient as RGB; stops are (position 0..1, (r, g, b))."""
    w, h = size
    a = math.radians(angle_deg)
    ys, xs = np.mgrid[0:h, 0:w].astype(np.float32)
    dx, dy = math.cos(a), math.sin(a)
    proj = xs * dx + ys * dy
    lo, hi = proj.min(), proj.max()
    t = (proj - lo) / (hi - lo + 1e-6)
    pos = [s[0] for s in stops]
    out = np.zeros((h, w, 3), np.float32)
    for c in range(3):
        out[..., c] = np.interp(t, pos, [s[1][c] for s in stops])
    return Image.fromarray(out.clip(0, 255).astype(np.uint8), "RGB")


class Phone:
    """The static layers: [back] under the app's picture, [front] over it."""

    def __init__(self, canvas, phone_h, top, bg, theme, clock):
        self.cw, self.ch = canvas
        self.s = phone_h / BODY_H            # pixels per point
        self.theme = theme
        s = self.s
        self.bw, self.bh = round(BODY_W * s), round(BODY_H * s)
        self.x0 = (self.cw - self.bw) // 2    # the body's top left on the canvas
        self.y0 = top
        self.sx = self.x0 + round(EDGE * s)   # the screen's top left
        self.sy = self.y0 + round(EDGE * s)
        self.sw, self.sh = round(SCREEN_W * s), round(SCREEN_H * s)
        self.bg = bg
        self.clock = clock
        self._back()
        self._front()

    # -- helpers ---------------------------------------------------------
    def px(self, v):
        return v * self.s

    def _back(self):
        s = self.s
        cw, ch = self.cw, self.ch
        back = Image.new("RGBA", (cw, ch), self.bg + (255,))
        # Soft background light: a little brighter behind the phone.
        glow = Image.new("L", (cw, ch), 0)
        gd = ImageDraw.Draw(glow)
        gd.ellipse((cw * 0.1, ch * 0.12, cw * 0.9, ch * 0.88), fill=42)
        glow = glow.filter(ImageFilter.GaussianBlur(cw * 0.12))
        back = Image.composite(Image.new("RGBA", (cw, ch), (255, 255, 255, 255)), back, glow)

        # Shadows: a wide soft one and a tight contact one.
        for dy, blur, alpha in ((30, 34, 70), (7, 8, 90)):
            sh = Image.new("L", (cw, ch), 0)
            ImageDraw.Draw(sh).rounded_rectangle(
                (self.x0, self.y0 + self.px(dy), self.x0 + self.bw, self.y0 + self.bh + self.px(dy)),
                radius=self.px(R_BODY), fill=alpha)
            sh = sh.filter(ImageFilter.GaussianBlur(self.px(blur)))
            back = Image.composite(Image.new("RGBA", (cw, ch), (20, 18, 16, 255)), back, sh)

        # Side buttons (right edge): a volume rocker and a power key.
        for y_top, h in ((178, 74), (270, 46)):
            bx0 = self.x0 + self.bw - self.px(0.6)
            by0 = self.y0 + self.px(y_top)
            w, hh = round(self.px(BTN + 0.6)), round(self.px(h))
            b = gradient((max(w, 2), max(hh, 2)), [(0, (96, 99, 105)), (0.5, (52, 54, 58)), (1, (30, 31, 34))], 0)
            m = rrect_mask(b.size, (0, 0, b.size[0], b.size[1]), self.px(1.4))
            back.paste(b, (round(bx0), round(by0)), m)

        # The metal frame: graphite, bright at the top left, dark at the
        # bottom right; a fine bright edge round the outside.
        body_box = (0, 0, self.bw, self.bh)
        body_mask = rrect_mask((self.bw, self.bh), body_box, self.px(R_BODY))
        metal = gradient((self.bw, self.bh),
                         [(0, (118, 122, 130)), (0.22, (62, 65, 71)), (0.55, (36, 38, 42)), (1, (24, 25, 28))], 62)
        edge_inner = rrect_mask((self.bw, self.bh),
                                (self.px(1.1), self.px(1.1), self.bw - self.px(1.1), self.bh - self.px(1.1)),
                                self.px(R_BODY - 1.1))
        rim = ImageChops.subtract(body_mask, edge_inner)
        rim_light = gradient((self.bw, self.bh), [(0, (235, 238, 244)), (0.35, (140, 144, 152)), (1, (50, 52, 58))], 62)
        metal.paste(rim_light, (0, 0), rim)
        # Antenna breaks: thin dark cuts across the frame near each corner.
        cuts = Image.new("L", (self.bw, self.bh), 0)
        cd = ImageDraw.Draw(cuts)
        w = max(1, round(self.px(0.9)))
        for fx in (78, BODY_W - 78):
            cd.rectangle((self.px(fx), 0, self.px(fx) + w, self.px(FRAME + 1.2)), fill=255)
            cd.rectangle((self.px(fx), self.bh - self.px(FRAME + 1.2), self.px(fx) + w, self.bh), fill=255)
        for fy in (84, BODY_H - 84):
            cd.rectangle((0, self.px(fy), self.px(FRAME + 1.2), self.px(fy) + w), fill=255)
            cd.rectangle((self.bw - self.px(FRAME + 1.2), self.px(fy), self.bw, self.px(fy) + w), fill=255)
        metal.paste(Image.new("RGB", metal.size, (14, 14, 17)), (0, 0), ImageChops.multiply(cuts, body_mask))
        back.paste(metal, (self.x0, self.y0), body_mask)

        # The black glass border, with a faint lighter edge where it meets the frame.
        gx0, gy0 = self.px(FRAME), self.px(FRAME)
        gl = rrect_mask((self.bw, self.bh), (gx0, gy0, self.bw - gx0, self.bh - gy0), self.px(R_BODY - FRAME))
        glass_col = gradient((self.bw, self.bh), [(0, (14, 15, 18)), (1, (4, 4, 6))], 60)
        back.paste(glass_col, (self.x0, self.y0), gl)
        inner_hi = ImageChops.subtract(
            gl, rrect_mask((self.bw, self.bh),
                           (gx0 + self.px(0.7), gy0 + self.px(0.7), self.bw - gx0 - self.px(0.7), self.bh - gy0 - self.px(0.7)),
                           self.px(R_BODY - FRAME - 0.7)))
        back.paste(Image.new("RGB", (self.bw, self.bh), (52, 55, 62)), (self.x0, self.y0), inner_hi.point(lambda v: v // 2))

        # The screen's rounded window: what the app's picture is cut to.
        self.screen_mask = rrect_mask((self.sw, self.sh), (0, 0, self.sw, self.sh), self.px(R_SCREEN))
        self.back = back

    def _front(self):
        s = self.s
        cw, ch = self.cw, self.ch
        S = 2
        fr = Image.new("RGBA", (cw * S, ch * S), (0, 0, 0, 0))
        d = ImageDraw.Draw(fr)
        dark = self.theme == "light"             # dark icons on a light screen
        ink = (18, 22, 20, 235) if dark else (250, 250, 250, 240)

        def P(x, y):                             # screen points -> canvas pixels (x S)
            return ((self.sx + self.px(x)) * S, (self.sy + self.px(y)) * S)

        # Status bar: the time, then signal, wifi, battery.
        f = font("Medium", self.px(15.5) * S)
        tx, ty = P(27, 23.5)
        d.text((tx, ty), self.clock, font=f, fill=ink, anchor="lm")
        # signal: four rising bars
        x = 297
        for i in range(4):
            h = 5 + i * 2.6
            bx0, by1 = P(x + i * 4.6, 23.5 + 6.5)
            bx1, by0 = P(x + i * 4.6 + 3.2, 23.5 + 6.5 - h)
            col = ink if i < 4 else (ink[0], ink[1], ink[2], 90)
            d.rounded_rectangle((bx0, by0, bx1, by1), radius=self.px(0.8) * S, fill=col)
        # wifi: three arcs and a dot
        cx, cy = P(331, 29.5)
        for r in (11.5, 7.7, 3.9):
            rr = self.px(r) * S
            d.arc((cx - rr, cy - rr, cx + rr, cy + rr), 225, 315, fill=ink, width=max(2, round(self.px(1.7) * S)))
        dr = self.px(1.5) * S
        d.ellipse((cx - dr, cy - dr * 0.2 - dr, cx + dr, cy - dr * 0.2 + dr), fill=ink)
        # battery: an outline, a fill to 82 per cent, and a nub
        bx0, by0 = P(345, 17.5)
        bx1, by1 = P(366.5, 29.5)
        d.rounded_rectangle((bx0, by0, bx1, by1), radius=self.px(3.2) * S, outline=ink, width=max(2, round(self.px(1.3) * S)))
        d.rounded_rectangle((bx0 + self.px(2.1) * S, by0 + self.px(2.1) * S, bx0 + (bx1 - bx0) * 0.82, by1 - self.px(2.1) * S),
                            radius=self.px(1.7) * S, fill=ink)
        nx0, ny0 = P(367.6, 21.2)
        nx1, ny1 = P(369.2, 25.8)
        d.rounded_rectangle((nx0, ny0, nx1, ny1), radius=self.px(0.8) * S, fill=ink)

        # The gesture bar at the bottom.
        gx0, gy0 = P(195 - 62, 844 - 12)
        gx1, gy1 = P(195 + 62, 844 - 7.4)
        d.rounded_rectangle((gx0, gy0, gx1, gy1), radius=self.px(2.3) * S, fill=ink[:3] + (200,))

        # The punch-hole camera: black, a ring, a hint of lens glass.
        cx, cy = P(195, 22)
        for r, col in ((10.6, (26, 28, 34, 255)), (8.6, (6, 6, 8, 255)), (5.2, (14, 16, 26, 255))):
            rr = self.px(r) * S
            d.ellipse((cx - rr, cy - rr, cx + rr, cy + rr), fill=col)
        rr = self.px(2.1) * S
        d.ellipse((cx - self.px(1.7) * S - rr / 2, cy - self.px(1.9) * S - rr / 2,
                   cx - self.px(1.7) * S + rr / 2, cy - self.px(1.9) * S + rr / 2), fill=(70, 86, 140, 150))
        fr = fr.resize((cw, ch), Image.LANCZOS)

        # The glass sheen: a diagonal soft light from the top left, over the screen only.
        sheen = gradient((self.sw, self.sh), [(0, (255, 255, 255)), (0.34, (255, 255, 255)), (1, (255, 255, 255))], 70)
        a = np.zeros((self.sh, self.sw), np.float32)
        ys, xs = np.mgrid[0:self.sh, 0:self.sw].astype(np.float32)
        t = (xs / self.sw * 0.55 + ys / self.sh * 0.45)
        a = np.clip((0.40 - t) / 0.40, 0, 1) ** 1.6 * (0.10 if dark else 0.07) * 255
        sheen_a = Image.fromarray(a.astype(np.uint8), "L")
        sheen_a = ImageChops.multiply(sheen_a, self.screen_mask)
        fr.paste(Image.new("RGBA", (self.sw, self.sh), (255, 255, 255, 255)), (self.sx, self.sy), sheen_a)
        # A hair of darkening at the screen's rim, as glass over a display has.
        rim = ImageChops.subtract(self.screen_mask, rrect_mask((self.sw, self.sh),
                                  (self.px(1.2), self.px(1.2), self.sw - self.px(1.2), self.sh - self.px(1.2)), self.px(R_SCREEN - 1.2)))
        fr.paste(Image.new("RGBA", (self.sw, self.sh), (0, 0, 0, 255)), (self.sx, self.sy), rim.point(lambda v: v * 45 // 255))
        self.front = fr

    # -- per frame -------------------------------------------------------
    def frame(self, app_png, rings, under=(), over=()):
        img = self.back.copy()
        app = Image.open(app_png).convert("RGB").resize((self.sw, self.sh), Image.LANCZOS)
        screen = app.convert("RGBA")
        for layer in under:
            screen = Image.alpha_composite(screen, layer)
        for ring in rings:
            screen = Image.alpha_composite(screen, ring(self))
        for layer in over:
            screen = Image.alpha_composite(screen, layer)
        img.paste(screen.convert("RGB"), (self.sx, self.sy), self.screen_mask)
        return Image.alpha_composite(img, self.front).convert("RGB")

    def to_px(self, x, y):
        return x * self.s, y * self.s


def ease_out(t):
    return 1 - (1 - t) ** 3


def tap_ring(cue, t):
    """A lime ring grows from where a finger touched, then fades."""
    age = t - cue["t"]
    life = 0.62
    if age < -0.02 or age > life:
        return None
    u = max(age, 0) / life

    def draw(ph):
        S = 2
        ov = Image.new("RGBA", (ph.sw * S, ph.sh * S), (0, 0, 0, 0))
        d = ImageDraw.Draw(ov)
        cx, cy = cue["x"] * ph.s * S, cue["y"] * ph.s * S
        r = ph.px(16 + 34 * ease_out(u)) * S
        alpha = (1 - u) ** 1.3
        # a dark halo so the ring reads on a white page, then the lime ring and fill
        d.ellipse((cx - r, cy - r, cx + r, cy + r), outline=PINE + (int(150 * alpha),), width=round(ph.px(7.4) * S))
        d.ellipse((cx - r, cy - r, cx + r, cy + r), fill=LIME + (int(105 * alpha),), outline=LIME + (int(255 * alpha),), width=round(ph.px(4.4) * S))
        # the fingertip: a soft dark dot while it is down
        if u < 0.45:
            k = 1 - u / 0.45
            dr = ph.px(9) * S
            d.ellipse((cx - dr, cy - dr, cx + dr, cy + dr), fill=(10, 30, 24, int(95 * k)), outline=(255, 255, 255, int(180 * k)), width=round(ph.px(1.6) * S))
        return ov.resize((ph.sw, ph.sh), Image.LANCZOS)

    return draw


def drag_ring(cue, t):
    age = t - cue["t"]
    dur = cue["seconds"]
    if age < -0.02 or age > dur + 0.25:
        return None
    u = min(max(age, 0) / dur, 1)
    fade = 1 if age <= dur else max(0, 1 - (age - dur) / 0.25)

    def draw(ph):
        S = 2
        ov = Image.new("RGBA", (ph.sw * S, ph.sh * S), (0, 0, 0, 0))
        d = ImageDraw.Draw(ov)
        x = (cue["x"] + (cue["x2"] - cue["x"]) * u) * ph.s * S
        y = (cue["y"] + (cue["y2"] - cue["y"]) * u) * ph.s * S
        r = ph.px(22) * S
        d.ellipse((x - r, y - r, x + r, y + r), outline=PINE + (int(110 * fade),), width=round(ph.px(6.2) * S))
        d.ellipse((x - r, y - r, x + r, y + r), fill=LIME + (int(70 * fade),), outline=LIME + (int(255 * fade),), width=round(ph.px(3.6) * S))
        dr = ph.px(9) * S
        d.ellipse((x - dr, y - dr, x + dr, y + dr), fill=(10, 30, 24, int(95 * fade)), outline=(255, 255, 255, int(180 * fade)), width=round(ph.px(1.6) * S))
        return ov.resize((ph.sw, ph.sh), Image.LANCZOS)

    return draw


# ---- the phone's own parts: keyboard and fingerprint prompt ---------------
def sym_font(px):
    for p in ("/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",):
        if os.path.exists(p):
            return ImageFont.truetype(p, int(px))
    return font("Regular", px)


def keyboard_state(cues, t):
    """(progress 0..1, height, pressed key or None) of the keyboard at [t]."""
    state = None
    for c in cues:
        if c["kind"] == "keyboard" and c["t"] <= t:
            state = c
    if not state:
        return 0.0, 0, None
    u = min((t - state["t"]) / (8 / 30), 1.0)
    p = ease_out(u) if state["show"] else 1 - ease_out(u)
    pressed = None
    for c in cues:
        if c["kind"] == "key" and 0 <= t - c["t"] < 0.16:
            pressed = c["key"]
    return p, state["height"], pressed


def keyboard_layer(ph, cues, t, theme):
    p, kh, pressed = keyboard_state(cues, t)
    if p <= 0.001:
        return None
    S = 2
    ov = Image.new("RGBA", (ph.sw * S, ph.sh * S), (0, 0, 0, 0))
    d = ImageDraw.Draw(ov)
    dark = theme == "dark"
    bg = (32, 34, 38, 255) if dark else (228, 231, 236, 255)
    key = (60, 63, 68, 255) if dark else (255, 255, 255, 255)
    key_on = (96, 100, 108, 255) if dark else (196, 202, 212, 255)
    accent = (154, 182, 255, 255) if dark else (168, 199, 250, 255)
    ink = (235, 236, 240, 255) if dark else (28, 30, 34, 255)
    u = lambda v: ph.px(v) * S
    top = SCREEN_H - kh * p
    d.rectangle((0, u(top), ph.sw * S, ph.sh * S), fill=bg)
    rows = [["1", "2", "3", "\u232b"], ["4", "5", "6", "-"], ["7", "8", "9", "_"], [",", "0", ".", "\u2713"]]
    pad, gap = 8, 6
    usable = kh - GESTURE - pad - 4
    kw = (SCREEN_W - 2 * pad - 3 * gap) / 4
    kht = (usable - 3 * gap) / 4
    f = font("Medium", u(24))
    sf = sym_font(u(24))
    for ri, row in enumerate(rows):
        for ci, label in enumerate(row):
            x0 = pad + ci * (kw + gap)
            y0 = top + pad + ri * (kht + gap)
            on = (label == pressed)
            fill = accent if label == "\u2713" else key
            if on:
                fill = key_on
            d.rounded_rectangle((u(x0), u(y0), u(x0 + kw), u(y0 + kht)), radius=u(16), fill=fill)
            cx, cy = u(x0 + kw / 2), u(y0 + kht / 2)
            if label == "_":
                d.line([(cx - u(14), cy - u(3)), (cx - u(14), cy + u(4)), (cx + u(14), cy + u(4)), (cx + u(14), cy - u(3))], fill=ink[:3] + (190,), width=round(u(2)), joint="curve")
            elif label in ("\u232b", "\u2713"):
                d.text((cx, cy), label, font=sf, fill=ink, anchor="mm")
            else:
                d.text((cx, cy), label, font=f, fill=ink, anchor="mm")
    return ov.resize((ph.sw, ph.sh), Image.LANCZOS)


def system_layer(ph, cues, t):
    """A phone dialog over the app: the fingerprint prompt."""
    cur = None
    for c in cues:
        if c["kind"] == "system" and c["t"] <= t < c["t"] + c["seconds"] + 0.4:
            cur = c
    if not cur:
        return None
    age, dur = t - cur["t"], cur["seconds"]
    slide_in = ease_out(min(age / 0.3, 1))
    slide_out = ease_out(min(max(age - dur, 0) / 0.3, 1))
    vis = slide_in * (1 - slide_out)
    ok = age > dur - 0.5
    S = 2
    ov = Image.new("RGBA", (ph.sw * S, ph.sh * S), (0, 0, 0, 0))
    d = ImageDraw.Draw(ov)
    u = lambda v: ph.px(v) * S
    d.rectangle((0, 0, ph.sw * S, ph.sh * S), fill=(0, 0, 0, int(125 * vis)))
    h = 300
    y0 = SCREEN_H - 20 - h + (1 - vis) * (h + 40)
    x0, x1 = 14, SCREEN_W - 14
    # a soft shadow, then the card
    sh = Image.new("L", ov.size, 0)
    ImageDraw.Draw(sh).rounded_rectangle((u(x0), u(y0 + 6), u(x1), u(y0 + h + 6)), radius=u(30), fill=90)
    sh = sh.filter(ImageFilter.GaussianBlur(u(10)))
    ov = Image.composite(Image.new("RGBA", ov.size, (0, 0, 0, 255)), ov, sh.point(lambda v: v // 3))
    d = ImageDraw.Draw(ov)
    d.rounded_rectangle((u(x0), u(y0), u(x1), u(y0 + h)), radius=u(30), fill=(251, 251, 253, 255))
    cx = u((x0 + x1) / 2)
    # the app's little mark and name
    d.rounded_rectangle((cx - u(14), u(y0 + 24), cx + u(14), u(y0 + 52)), radius=u(8), fill=PINE + (255,))
    d.text((cx, u(y0 + 38.5)), "t", font=font("Medium", u(20)), fill=LIME + (255,), anchor="mm")
    d.text((cx, u(y0 + 76)), cur.get("title", "Confirm it's you"), font=font("Medium", u(21)), fill=(28, 30, 34, 255), anchor="mm")
    d.text((cx, u(y0 + 102)), cur.get("line", "Taskiem"), font=font("Regular", u(15)), fill=(86, 90, 98, 255), anchor="mm")
    # the fingerprint: arcs that turn into a green tick when it is read
    fy = y0 + 178
    if ok:
        d.ellipse((cx - u(34), u(fy - 34), cx + u(34), u(fy + 34)), fill=(30, 142, 62, 255))
        d.line([(cx - u(15), u(fy + 1)), (cx - u(4), u(fy + 12)), (cx + u(17), u(fy - 12))], fill=(255, 255, 255, 255), width=round(u(5)), joint="curve")
    else:
        col = (26, 115, 232, 255)
        for r, a0, a1 in ((30, 200, 340), (23, 190, 350), (16, 180, 360), (9, 170, 10 + 360), (30, 20, 70), (23, 20, 60), (16, 12, 50), (9, 14, 38)):
            d.arc((cx - u(r), u(fy) - u(r), cx + u(r), u(fy) + u(r)), a0, a1, fill=col, width=round(u(2.6)))
        d.arc((cx - u(36), u(fy) - u(36), cx + u(36), u(fy) + u(36)), 0, 360, fill=(26, 115, 232, 70), width=round(u(1.4)))
    d.text((cx, u(y0 + 246)), "Touch the fingerprint sensor" if not ok else "Confirmed", font=font("Regular", u(14)), fill=(86, 90, 98, 255), anchor="mm")
    d.text((cx, u(y0 + h - 24)), "Cancel", font=font("Medium", u(16)), fill=(26, 115, 232, 255), anchor="mm")
    return ov.resize((ph.sw, ph.sh), Image.LANCZOS)


def captions_layer(ph, cues, t, canvas):
    """The caption in force at [t], under the phone."""
    cur = None
    for c in cues:
        if c["kind"] == "say" and c["t"] <= t:
            cur = c
    if not cur:
        return None
    cw, ch = canvas
    layer = Image.new("RGBA", (cw, ch), (0, 0, 0, 0))
    d = ImageDraw.Draw(layer)
    y = ph.y0 + ph.bh + 64
    if cur.get("who"):
        f = font("Medium", 34)
        d.text((cw / 2, y), cur["who"], font=f, fill=PINE + (255,), anchor="mt")
        y += 56
    f = font("Regular", 46)
    words, lines, line = cur["text"].split(), [], ""
    for w in words:
        trial = (line + " " + w).strip()
        if d.textlength(trial, font=f) > cw - 170 and line:
            lines.append(line)
            line = w
        else:
            line = trial
    lines.append(line)
    for ln in lines:
        d.text((cw / 2, y), ln, font=f, fill=(14, 26, 20, 255), anchor="mt")
        y += 62
    return layer


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--frames", required=True, help="a film's folder (f00000.png ..., cues.json)")
    ap.add_argument("--out", required=True)
    ap.add_argument("--canvas", default="1080x1920")
    ap.add_argument("--theme", default="light", choices=["light", "dark"])
    ap.add_argument("--time", default="13:00")
    ap.add_argument("--bg", default="#efeae2")
    ap.add_argument("--phone-height", type=int, default=0)
    ap.add_argument("--top", type=int, default=-1)
    ap.add_argument("--captions", action="store_true")
    ap.add_argument("--keep", help="a folder to keep the composed pictures in")
    ap.add_argument("--still", type=float, help="write one picture at this second (to --out, a PNG) and stop")
    a = ap.parse_args()

    folder = Path(a.frames)
    meta = json.loads((folder / "cues.json").read_text())
    cues = meta["cues"]
    fps, n = meta["fps"], meta["frames"]
    cw, ch = [int(v) for v in a.canvas.split("x")]
    bg = tuple(int(a.bg.lstrip("#")[i:i + 2], 16) for i in (0, 2, 4))
    ph_h = a.phone_height or int(ch * (0.74 if a.captions else 0.86))
    top = a.top if a.top >= 0 else int((ch - ph_h) * (0.30 if a.captions else 0.5))
    phone = Phone((cw, ch), ph_h, top, bg, a.theme, a.time)

    def compose(i):
        t = i / fps
        rings = []
        for c in cues:
            r = tap_ring(c, t) if c["kind"] == "tap" else drag_ring(c, t) if c["kind"] == "drag" else None
            if r:
                rings.append(r)
        img = phone.frame(folder / f"f{i:05d}.png", rings, under=[l for l in (keyboard_layer(phone, cues, t, a.theme),) if l], over=[l for l in (system_layer(phone, cues, t),) if l])
        if a.captions:
            cap = captions_layer(phone, cues, t, (cw, ch))
            if cap:
                img = Image.alpha_composite(img.convert("RGBA"), cap).convert("RGB")
        return img

    if a.still is not None:
        compose(min(n - 1, round(a.still * fps))).save(a.out)
        print(a.out)
        return

    out_dir = Path(a.keep) if a.keep else Path(a.out).with_suffix(".frames")
    out_dir.mkdir(parents=True, exist_ok=True)
    for old in out_dir.glob("c*.png"):
        old.unlink()
    for i in range(n):
        compose(i).save(out_dir / f"c{i:05d}.png")
        if i % 30 == 0:
            print(f"{i}/{n}", file=sys.stderr)
    subprocess.run(["ffmpeg", "-loglevel", "error", "-y", "-framerate", str(fps), "-i", str(out_dir / "c%05d.png"),
                    "-vf", "format=yuv420p", "-c:v", "libx264", "-crf", "17", "-preset", "medium", "-movflags", "+faststart", a.out],
                   check=True)
    if not a.keep:
        for p in out_dir.glob("c*.png"):
            p.unlink()
        out_dir.rmdir()
    print(a.out)


if __name__ == "__main__":
    main()
