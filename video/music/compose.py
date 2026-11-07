"""The explainer's music, written for it (video/README.md): an original
Afro-pop instrumental, 112 BPM in F major, made here note by note so it is
Taskiem's own, with no licence to buy.

It follows the film: the timeline that assemble.mjs writes says which scene
plays when, and the music rises and falls with the acts (sparse for the
opening, the groove from the morning, full for tools, trade and deals,
quiet for the dark scene, a held chord on the end card). Taps and the good
moments (the bursts) get a soft sound of their own, read from each scene's
page, where they are drawn.

    python3 video/music/compose.py out/film-16x9.timeline.json out/film-16x9.wav [--no-sfx]

Only numpy and the standard library.
"""

import json
import re
import sys
import wave
from pathlib import Path

import numpy as np

SR = 44100
BPM = 112
BEAT = 60 / BPM
STEP = BEAT / 4  # a sixteenth
BAR = BEAT * 4
SWING = 0.10  # the off sixteenths come a little late
rng = np.random.default_rng(7)  # the same noise every time, so the same track

HERE = Path(__file__).resolve().parent
SCENES = HERE.parent / "scenes"


def hz(m):
    return 440.0 * 2 ** ((m - 69) / 12)


def tt(n):
    return np.arange(n) / SR


# The parts. Each returns a mono buffer, a note's whole life.

def pad(m, length, bright=8):
    n = int((length + 0.6) * SR)
    t = tt(n)
    s = np.zeros(n)
    for cents in (-5, 0, 6):
        f = hz(m) * 2 ** (cents / 1200)
        ph = rng.uniform(0, 2 * np.pi)
        for k in range(1, bright + 1):
            s += np.sin(2 * np.pi * f * k * t + ph * k) / k ** 1.4
    env = np.minimum(1, t / 0.35) * np.where(t < length, 1, np.exp(-(t - length) / 0.18))
    return s * env * 0.05


def keys(m):
    t = tt(int(0.5 * SR))
    f = hz(m)
    s = np.sin(2 * np.pi * f * t) + 0.45 * np.sin(4 * np.pi * f * t) * np.exp(-t / 0.08) + 0.12 * np.sin(6 * np.pi * f * t)
    return s * np.minimum(1, t / 0.004) * np.exp(-t / 0.14) * 0.12


def kalimba(m):
    t = tt(int(1.4 * SR))
    f = hz(m)
    s = np.sin(2 * np.pi * f * t) * np.exp(-t / 0.45) + 0.35 * np.sin(2 * np.pi * f * 5.4 * t) * np.exp(-t / 0.03) + 0.12 * np.sin(2 * np.pi * f * 3.0 * t) * np.exp(-t / 0.12)
    return s * np.minimum(1, t / 0.002) * 0.22


def bell(m, length=2.0):
    t = tt(int(length * SR))
    f = hz(m)
    s = np.sin(2 * np.pi * f * t) * np.exp(-t / 0.9) + 0.4 * np.sin(2 * np.pi * f * 2.76 * t) * np.exp(-t / 0.35) + 0.15 * np.sin(2 * np.pi * f * 5.4 * t) * np.exp(-t / 0.12)
    return s * np.minimum(1, t / 0.002)


def kick():
    t = tt(int(0.45 * SR))
    f = 48 + 95 * np.exp(-t / 0.035)
    s = np.sin(2 * np.pi * np.cumsum(f) / SR) * np.exp(-t / 0.28)
    s += 0.3 * rng.standard_normal(len(t)) * np.exp(-t / 0.002)
    return np.tanh(1.6 * s) * 0.55


def logdrum(m):
    t = tt(int(0.6 * SR))
    f = hz(m) * (1 + 0.6 * np.exp(-t / 0.025))
    s = np.sin(2 * np.pi * np.cumsum(f) / SR)
    s = np.tanh(2.2 * s) * np.exp(-t / 0.3)
    return s * np.minimum(1, t / 0.003) * 0.32


def rim():
    t = tt(int(0.12 * SR))
    n = rng.standard_normal(len(t))
    n = n - np.convolve(n, np.ones(6) / 6, "same")
    s = 0.6 * n * np.exp(-t / 0.012) + np.sin(2 * np.pi * 1750 * t) * np.exp(-t / 0.018)
    return s * 0.10


def clap():
    t = tt(int(0.3 * SR))
    n = rng.standard_normal(len(t))
    n = n - np.convolve(n, np.ones(3) / 3, "same")
    env = np.zeros(len(t))
    for d in (0, 0.011, 0.022):
        env += np.where(t >= d, np.exp(-(t - d) / (0.008 if d < 0.02 else 0.11)), 0)
    return n * env * 0.09


def shaker(accent):
    t = tt(int(0.08 * SR))
    n = rng.standard_normal(len(t))
    n = n - np.convolve(n, np.ones(3) / 3, "same")
    return n * np.minimum(1, t / 0.006) * np.exp(-t / 0.022) * (0.05 if accent else 0.025)


def tock():
    t = tt(int(0.09 * SR))
    s = np.sin(2 * np.pi * 1150 * t) * np.exp(-t / 0.018) + 0.3 * np.sin(2 * np.pi * 2300 * t) * np.exp(-t / 0.01)
    return s * np.minimum(1, t / 0.001) * 0.07


# The song: four chords a bar each, F – Am – Dm – B♭, voiced close.
CHORDS = [
    ([53, 57, 60, 64], 41),  # Fmaj7, F
    ([52, 57, 60, 64], 45),  # Am7, A
    ([53, 57, 60, 62], 38),  # Dm7, D
    ([53, 57, 58, 62], 46),  # B♭maj7, B♭
]
KICK = [0, 7, 8, 14]
CLAP = [4, 12]
RIM = [0, 3, 6, 10, 12]
BASS = [(0, 0), (3, 0), (6, 7), (10, 12)]  # (step, semitones over the root)
SKANK = [2, 6, 10, 14]
# The tune, in F major pentatonic, four bars: (bar, step, note).
TUNE = [
    (0, 0, 72), (0, 3, 69), (0, 6, 72), (0, 10, 74), (0, 12, 77),
    (1, 2, 76), (1, 6, 72), (1, 8, 69), (1, 12, 72),
    (2, 0, 74), (2, 3, 72), (2, 6, 69), (2, 10, 67), (2, 12, 65),
    (3, 4, 67), (3, 8, 69), (3, 12, 72), (3, 14, 74),
]

# How loud each part is in each act (scene numbers from script.md; 0 is a
# short's title card).
LAYERS = ["pad", "dark", "keys", "kalimba", "shaker", "kick", "clap", "rim", "bass"]
ACTS = {
    "open":    dict(pad=1.0, kalimba=0.7, shaker=0.35),
    "morning": dict(pad=0.8, keys=0.6, kalimba=0.5, shaker=0.7, kick=0.8, clap=0.5, rim=0.4, bass=0.6),
    "tools":   dict(pad=0.7, keys=0.8, kalimba=0.4, shaker=1.0, kick=1.0, clap=0.8, rim=0.7, bass=1.0),
    "trade":   dict(pad=0.7, keys=0.8, kalimba=0.9, shaker=1.0, kick=1.0, clap=0.9, rim=1.0, bass=1.0),
    "dark":    dict(dark=1.0, kalimba=0.3, shaker=0.25, kick=0.45, bass=0.7),
    "explore": dict(pad=0.8, keys=0.5, kalimba=0.8, shaker=0.8, kick=0.7, clap=0.5, rim=0.6, bass=0.8),
    "deals":   dict(pad=0.7, keys=0.8, kalimba=1.0, shaker=1.0, kick=1.0, clap=1.0, rim=1.0, bass=1.0),
}
ACT_OF = {0: "open", 1: "open", 2: "open", 3: "morning", 4: "morning", 5: "tools", 6: "tools", 7: "tools",
          8: "trade", 9: "trade", 10: "tools", 11: "dark", 12: "explore", 13: "deals", 14: "deals", 15: "deals"}
END = 16  # the end card: the groove stops on a held chord


def place(buf, at, sig, pan=0.0, gain=1.0):
    i = int(round(at * SR))
    if i >= buf.shape[1] or i + len(sig) <= 0:
        return
    j = min(buf.shape[1], i + len(sig))
    s = sig[: j - i] * gain
    buf[0, i:j] += s * np.sqrt((1 - pan) / 2)
    buf[1, i:j] += s * np.sqrt((1 + pan) / 2)


def when(bar, step):
    return bar * BAR + step * STEP + (SWING * STEP if step % 2 else 0)


def smooth(x, seconds):
    k = int(seconds * SR)
    c = np.cumsum(np.concatenate([np.full(k, x[0]), x]))
    return (c[k:] - c[:-k]) / k


def reverb(x, wet=0.2, decay=0.55, length=1.8):
    n = int(length * SR)
    t = tt(n)
    out = np.empty_like(x)
    size = 1 << int(np.ceil(np.log2(x.shape[1] + n)))
    for c in range(2):
        ir = rng.standard_normal(n) * np.exp(-t / decay) * np.minimum(1, t / 0.01)
        ir[: int(0.015 * SR)] = 0  # a little pre-delay
        ir /= np.sqrt(np.sum(ir ** 2))
        y = np.fft.irfft(np.fft.rfft(x[c], size) * np.fft.rfft(ir, size), size)[: x.shape[1]]
        out[c] = x[c] + wet * y
    return out


def moments(scene):
    """Taps and bursts drawn in a scene, in seconds from its start."""
    html = (SCENES / f"{scene}.html").read_text()
    taps = [int(m) / 1000 for m in re.findall(r'class="tap"[^>]*data-at="(\d+)"', html)]
    bursts = [int(m) / 1000 for m in re.findall(r'class="burst"[^>]*data-at="(\d+)"', html)]
    return taps, bursts


def compose(timeline, sfx=True):
    length = timeline["length"]
    scenes = timeline["scenes"]
    n = int((length + 0.1) * SR)
    end_at = next((s["start"] + 0.2 for s in scenes if int(s["scene"][:2]) == END), length)
    bars = int(np.ceil(end_at / BAR)) + 1

    parts = {k: np.zeros((2, n)) for k in LAYERS}
    for b in range(bars):
        chord, root = CHORDS[b % 4]
        for m in chord:
            place(parts["pad"], b * BAR, pad(m, BAR), gain=1)
            place(parts["dark"], b * BAR, pad(m - 12 if m > 58 else m, BAR, bright=3), gain=1.3)
        for st in SKANK:
            for m in chord[1:]:
                place(parts["keys"], when(b, st), keys(m + 12), pan=-0.3)
        for bb, st, m in TUNE:
            if bb == b % 4:
                place(parts["kalimba"], when(b, st), kalimba(m), pan=0.25)
        for st in range(16):
            place(parts["shaker"], when(b, st), shaker(st % 4 == 2), pan=0.45)
        for st in KICK:
            place(parts["kick"], when(b, st), kick())
        for st in CLAP:
            place(parts["clap"], when(b, st), clap(), pan=-0.1)
        for st in RIM:
            place(parts["rim"], when(b, st), rim(), pan=-0.4)
        for st, up in BASS:
            place(parts["bass"], when(b, st), logdrum(root + up))

    # Each part follows the acts, easing between them, and stops for the end card.
    t = tt(n)
    stop = smooth((t < end_at).astype(float), 0.12)
    mix = np.zeros((2, n))
    wet = np.zeros((2, n))
    for k in LAYERS:
        target = np.zeros(n)
        for s in scenes:
            act = ACTS[ACT_OF.get(int(s["scene"][:2]), "open")]
            i, j = int(s["start"] * SR), int((s["start"] + s["duration"]) * SR)
            target[i:j] = act.get(k, 0.0)
        g = smooth(target, 0.6) * stop
        (wet if k in ("pad", "dark", "keys", "kalimba", "clap", "rim") else mix)[:] += parts[k] * g

    # The end card: the home chord, held, and the mark's chime.
    if end_at < length:
        for m in [41, 53, 57, 60, 64, 67]:  # Fmaj9 over F
            place(wet, end_at, pad(m, length - end_at, bright=6), gain=1.4)
        for i, m in enumerate([77, 81, 84, 89]):
            place(wet, end_at + 1.1 + i * 0.09, bell(m, 3.0), pan=0.2 * (i - 1.5), gain=0.09)

    if sfx:
        for s in scenes:
            taps, bursts = moments(s["scene"])
            for a in taps:
                place(mix, s["start"] + a, tock(), gain=1.0)
            for a in bursts:
                for i, m in enumerate([81, 84, 89]):
                    place(wet, s["start"] + a + i * 0.07, bell(m), pan=0.3 * (i - 1), gain=0.08)

    out = mix + reverb(wet)
    out = out[:, : int(length * SR)]
    fade_in = np.minimum(1, tt(out.shape[1]) / 0.4)
    fade_out = np.clip((length - tt(out.shape[1])) / 2.5, 0, 1)
    out *= fade_in * fade_out
    out = np.tanh(out / np.max(np.abs(out)) * 1.2) / np.tanh(1.2) * 10 ** (-1 / 20)
    return out


def main():
    a = sys.argv[1:]
    sfx = "--no-sfx" not in a
    a = [x for x in a if not x.startswith("--")]
    if len(a) != 2:
        print(__doc__)
        sys.exit(2)
    timeline = json.loads(Path(a[0]).read_text())
    out = compose(timeline, sfx)
    pcm = (np.clip(out.T, -1, 1) * 32767).astype("<i2")
    with wave.open(a[1], "wb") as w:
        w.setnchannels(2)
        w.setsampwidth(2)
        w.setframerate(SR)
        w.writeframes(pcm.tobytes())
    print(f"{a[1]} ({timeline['length']:.1f} s)")


if __name__ == "__main__":
    main()
