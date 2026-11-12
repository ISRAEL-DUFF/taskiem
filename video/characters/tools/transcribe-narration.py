import numpy as np
from faster_whisper import WhisperModel
a=np.fromfile("narr.f32",dtype=np.float32)
m=WhisperModel("small.en",device="cpu",compute_type="int8")
segs,_=m.transcribe(a,word_timestamps=True,language="en")
for s in segs:
    for w in s.words: print(f"{w.start:.2f}\t{w.end:.2f}\t{w.word.strip()}")
