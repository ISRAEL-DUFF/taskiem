# Chat film, hybrid cut: scene-by-scene plan

The chat explainer (`films/chat/script.md`), re-cut with **character cutaways
made in Google Flow** and **a narrator voice**. The product scenes are
unchanged (the real screens, prices and captions come from our renders); Flow
supplies only short people shots between them. Total about **2:50**
(the original is 2:22; the cutaways add about 28 s).

## 1. Parts and where each comes from

| Layer | Made by | Notes |
| --- | --- | --- |
| Product scenes 1 to 16 | `node video/render.mjs` / `assemble.mjs` (already built) | One MP4 per scene: `out/chat/<scene>-16x9.mp4`. The film's captions stay burned in. |
| Character cutaways C1 to C8 | Google Flow (Veo), from the approved references | 3 to 4 s each, no speech, ambient sound only |
| Narrator | A text-to-speech tool or a voice actor | Lines below, one file per scene |
| Music | The film's own track (`compose.py`) | Keep it; duck it under the narrator |

## 2. Narrator brief

- **Voice:** warm, calm, friendly, unhurried; a smile you can hear. Nigerian
  English accent (a Lagos speaker). Sounds like a trusted neighbour, never an
  announcer or a salesperson.
- **Pace:** about 2.3 words a second. Pauses at full stops; do not rush to
  fill the time. Every line is written to leave a small gap at the end.
- **Say it this way:** ₦ is "naira"; Taskiem is "TASK-ee-em" (confirm once,
  then keep the same pronunciation file for every line); "OK" is "okay".
- **TTS style note (paste into the tool's instructions if it has one):**
  "Warm, calm Nigerian English narrator, friendly and reassuring, medium-slow
  pace, natural pauses, light smile in the voice."
- Record or generate **one file per scene** (`narration/01.wav` and so on), so
  a line can be redone without touching the rest. Keep the voice, version and
  settings in `narration/NOTES.md`.

## 3. Timeline

Times are for the **16:9 cut**. Cutaways go in *before* the scene shown.
The scenes keep their original 0.4 s dissolve; cut cutaways in with a
straight cut or a short dissolve.

| Time | Layer | Shot | Length | Narrator line |
| --- | --- | --- | --- | --- |
| 0:00 | Flow | C1 | 4 s | (no narration; ambient sound) |
| 0:04 | Product | 01-open | 7 s | Your business already runs on chat. Orders, voice notes, prices, all in one pile. |
| 0:11 | Product | 02-intro | 5 s | What if the chat could run it with you? |
| 0:16 | Product | 03-briefing | 12 s | Every morning at eight, Taskiem sends one card. Yesterday's sales, who owes you, what's running low, and what goes out today. |
| 0:28 | Product | 04-alerts | 8 s | What needs you comes with the next step on it. Reorder the rice. Approve a bill. |
| 0:36 | Flow | C2 | 3 s | (no narration; ambient sound) |
| 0:39 | Product | 05-picker | 10 s | Writing to a supplier? Tag a product, a service, or a business, right in the message. |
| 0:49 | Product | 06-tools | 12 s | Open any tool without leaving the chat. Change a price, tap Save. |
| 1:01 | Product | 07-diary | 6 s | Every change is written down, wherever you work. |
| 1:07 | Flow | C3 | 3 s | (no narration; ambient sound) |
| 1:10 | Product | 08-trade | 12 s | Orders and invoices between businesses arrive as cards. Accept, and follow it from placed to delivered. |
| 1:22 | Flow | C4 | 3 s | (no narration; ambient sound) |
| 1:25 | Product | 09-both | 8 s | Both sides see the same order. Nothing to retype. |
| 1:33 | Flow | C5 | 4 s | (no narration; ambient sound) |
| 1:37 | Product | 10-staff | 12 s | Your staff see what their role needs. Tunde sells, counts stock, and hands over the shift, within the limits you set. |
| 1:49 | Product | 11-dark | 6 s | Light by day, dark by night, and always, how did we do. |
| 1:55 | Flow | C6 | 3 s | (no narration; ambient sound) |
| 1:58 | Product | 12-explore | 10 s | Find businesses near you, connect, and trade with them. |
| 2:08 | Flow | C7 | 4 s | (no narration; ambient sound) |
| 2:12 | Product | 13-offer | 10 s | Rivers Foods wants twenty party packs. From ten packs it is forty-three thousand each. They offer eight hundred thousand. |
| 2:22 | Product | 14-counter | 10 s | Tunde counters at eight hundred and forty. They answer on WhatsApp: eight twenty. Offers and counters, from the app or WhatsApp. |
| 2:32 | Product | 15-deal | 7 s | Eight twenty is under Ada's lowest price, so it waits for her OK. She agrees. |
| 2:39 | Flow | C8 | 4 s | (no narration; ambient sound) |
| 2:43 | Product | 16-end | 7 s | Run your business from the chat. Turn on Chat tools. |

Fit check: each line is at most about 2 words a second, so none overruns its
scene. If a generated voice runs long, trim the line before speeding up the voice.

## 4. Flow prompts (cutaways)

Run each in Flow with **Ingredients to Video** and attach the character's
approved turnaround and hero portrait. Paste the prompt in full. Generate
16:9 first; do 9:16 later for the shorts. If a clip drifts, use the drift
checklist in `bible.md` section 12.

### C1: Cold open: the person the film is about, overloaded (before scene 1, 4 s)

```
Ada Obi, a 34-year-old Nigerian woman, rich medium-deep brown skin (warm undertone), oval face, high cheekbones, small dimple in left cheek, large warm dark-brown almond eyes, full softly arched brows, small gap between front teeth, wide easy smile; hair fully covered by a marigold-gold headwrap tied in a fan at front left with a small lime-green pin; wearing a pine-green short-sleeved work blouse with sleeves pushed up and a lime-green bib apron with a pocket holding a phone, thin gold stud earrings, faint pale oil-burn scar on back of left hand; strong practical build, brisk and warm, a cook in a busy Lagos kitchen. Stirs a large steel pot of jollof with one hand while her phone buzzes on the counter; she glances at it, then exhales with a tired, amused half-smile. Setting: Ada's Kitchen in Yaba: white tiles, steel counters, big pots, steam, bright window. Camera: slow push-in, eye-level, medium shot. Clip length: about 4 seconds. Style: stylised semi-realistic animated-feature look, soft 3D-like shading, warm natural light. Audio: natural ambient sound only, no speech, no music. No text, logos or screens with readable writing; exactly one person in frame; natural hands.
```

### C2: After the morning card and alerts: calm (before scene 5, 3 s)

```
Ada Obi, a 34-year-old Nigerian woman, rich medium-deep brown skin (warm undertone), oval face, high cheekbones, small dimple in left cheek, large warm dark-brown almond eyes, full softly arched brows, small gap between front teeth, wide easy smile; hair fully covered by a marigold-gold headwrap tied in a fan at front left with a small lime-green pin; wearing a pine-green short-sleeved work blouse with sleeves pushed up and a lime-green bib apron with a pocket holding a phone, thin gold stud earrings, faint pale oil-burn scar on back of left hand; strong practical build, brisk and warm, a cook in a busy Lagos kitchen. Reads her phone, relief spreading across her face, a small nod; her right brow lifts slightly. Setting: the same kitchen, morning light. Camera: close-up, shallow depth of field, slight handheld feel. Clip length: about 3 seconds. Style: stylised semi-realistic animated-feature look, soft 3D-like shading, warm natural light. Audio: natural ambient sound only, no speech, no music. No text, logos or screens with readable writing; exactly one person in frame; natural hands.
```

### C3: The order is on the way (before scene 8, 3 s)

```
Emeka Nwosu, a 31-year-old Nigerian man, very deep dark-brown skin with warm bronze highlights, long angular face, strong brow ridge, high cheekbones, narrow jaw, narrow alert dark eyes, straight dark brows, small scar through left eyebrow, thin neat goatee; wearing a lime-green open-face motorcycle helmet with a pine stripe, pine-green riding jacket with grey reflective strips, black cargo trousers, black boots, fingerless gloves, a lime insulated delivery cool box on his back; lean wiry build, calm and watchful, a delivery rider in Lagos. Rides a small dark-pine motorcycle through a busy Lagos street with the lime cool box on his back, slows, gives a two-finger salute. Setting: a Lagos street with yellow danfo buses and painted walls, late-morning sun. Camera: tracking shot beside the bike, eye-level. Clip length: about 3 seconds. Style: stylised semi-realistic animated-feature look, soft 3D-like shading, warm natural light. Audio: natural ambient sound only, no speech, no music. No text, logos or screens with readable writing; exactly one person in frame; natural hands.
```

### C4: Bayo sees the order arrive (before scene 9, 3 s)

```
Bayo Adeyemi, a 52-year-old Nigerian man, medium brown skin with a warm reddish undertone, broad square face, strong jaw, full cheeks, deep-set dark eyes with crow's-feet, thick dark brows with a few grey hairs, broad nose; full short salt-and-pepper beard, grey at the temples under a tan flat cap; wearing a rice-sack brown short-sleeve button shirt with a chest pocket and reading glasses clipped in it, khaki work trousers, brown boots, gold ring on left hand, faint rice dust on sleeves; solid stocky build with a modest belly, deliberate and dry, a rice-mill owner in Lagos. Holds his phone at arm's length, reads it, then slowly nods and strokes his beard; one brow lifts. Setting: Bayo Rice Mills warehouse: stacked burlap rice sacks, a hanging scale, dust in a shaft of sunlight. Camera: medium close-up, locked-off. Clip length: about 3 seconds. Style: stylised semi-realistic animated-feature look, soft 3D-like shading, warm natural light. Audio: natural ambient sound only, no speech, no music. No text, logos or screens with readable writing; exactly one person in frame; natural hands.
```

### C5: Staff, within limits (before scene 10, 4 s)

```
Tunde, a 24-year-old Nigerian man, deep rich dark-brown skin, long oval face, defined cheekbones, calm medium dark-brown eyes with slightly heavy lids, straight thick brows, tiny mole under right eye, shy closed-lip smile; short black low-crop haircut with sharp hairline, clean-shaven; wearing a dark-green (#164a3b) crew-neck T-shirt with a lime-green lanyard and white ID card, dark jeans, white sneakers, slim silver watch on left wrist; lean lanky build, slight stoop, calm and careful, a shop cashier in Lagos. Taps twice on the counter, squares a stack of Naira notes, checks his phone, and gives a small shy closed-lip smile. Setting: a neat shop counter in Ada's Kitchen with a small receipt printer. Camera: medium shot, eye-level, gentle push-in. Clip length: about 4 seconds. Style: stylised semi-realistic animated-feature look, soft 3D-like shading, warm natural light. Audio: natural ambient sound only, no speech, no music. No text, logos or screens with readable writing; exactly one person in frame; natural hands.
```

### C6: The friendly neighbour (before scene 12, 3 s)

```
Kola Adebayo, a 29-year-old Nigerian man, warm medium-light brown skin with golden undertone, round friendly face, full cheeks, dimples on both cheeks, bright round dark-brown eyes that crinkle, soft brows, wide easy grin, light even stubble, always a smudge of flour on his left cheek; short black taper-fade haircut with a curly top, pencil behind right ear, silver stud in left ear; wearing a burnt-orange work shirt under a cream apron with flour handprints, dark trousers, white clogs; athletic bouncy build, warm and chatty, a baker in Lagos. Waves with a flour-dusted hand and grins, flour puffing off his apron, holding up a fresh loaf. Setting: Kola's Bakery: brick oven, trays of loaves, warm steam. Camera: medium shot, slight low angle, bouncy energy. Clip length: about 3 seconds. Style: stylised semi-realistic animated-feature look, soft 3D-like shading, warm natural light. Audio: natural ambient sound only, no speech, no music. No text, logos or screens with readable writing; exactly one person in frame; natural hands.
```

### C7: The buyer opens the haggle (before scene 13, 4 s)

```
Ibinabo George, a 38-year-old Nigerian woman, deep warm brown skin, smooth with a soft glow, heart-shaped face, high forehead, defined cheekbones, pointed soft chin, large dark almond eyes behind round tortoiseshell glasses, neat arched brows, muted rose-brown lipstick, polite half-smile; natural black hair in a high rounded afro puff held by a thin black band; wearing a crisp white tailored shirt, pine-green blazer, black trousers, black loafers, small gold hoop earrings, slim gold watch on right wrist, leather folio; slim tall posture, sharp and polite, a corporate buyer in Lagos. Taps a pen on a leather folio, looks up, tilts her head, one brow up, polite half-smile. Setting: a modern canteen office on Victoria Island: glass, neutral greys, a pine accent. Camera: medium close-up, eye-level, locked-off, shallow depth of field. Clip length: about 4 seconds. Style: stylised semi-realistic animated-feature look, soft 3D-like shading, warm natural light. Audio: natural ambient sound only, no speech, no music. No text, logos or screens with readable writing; exactly one person in frame; natural hands.
```

### C8: Deal agreed; closing (before scene 16, 4 s)

```
Ada Obi, a 34-year-old Nigerian woman, rich medium-deep brown skin (warm undertone), oval face, high cheekbones, small dimple in left cheek, large warm dark-brown almond eyes, full softly arched brows, small gap between front teeth, wide easy smile; hair fully covered by a marigold-gold headwrap tied in a fan at front left with a small lime-green pin; wearing a pine-green short-sleeved work blouse with sleeves pushed up and a lime-green bib apron with a pocket holding a phone, thin gold stud earrings, faint pale oil-burn scar on back of left hand; strong practical build, brisk and warm, a cook in a busy Lagos kitchen. Reads her phone, her face breaks into a wide smile with the dimple, she looks up and gives a double nod, 'okay, okay'. Setting: Ada's Kitchen in the evening, warm lamp light, steam. Camera: close-up pushing in slowly, eye-level. Clip length: about 4 seconds. Style: stylised semi-realistic animated-feature look, soft 3D-like shading, warm natural light. Audio: natural ambient sound only, no speech, no music. No text, logos or screens with readable writing; exactly one person in frame; natural hands.
```

## 5. Assembly (in your editor)

1. Render the product scenes: `node video/assemble.mjs chat --name explainer`
   (the picture and per-scene MP4s land in `out/chat/`).
2. Import the 16 scene MP4s, the 8 Flow clips and the 16 narrator files.
3. Lay them out in the order of the timeline above. Narration starts about
   0.3 s after its scene starts, so the first words land after the picture.
4. Mute the Flow clips' sound except the ambient bed at low level.
5. Music under everything, ducked about 10 dB while the narrator speaks.
6. Export 16:9. For 9:16 cuts, render the product scenes with `--size 9x16`
   and generate the cutaways in 9:16.
7. Check it muted: the burned-in captions must still tell the story.

## 6. First pass to try

Start small to learn Flow before doing all eight:
1. Approve Ada's references (`approved/ada/`).
2. Generate **C1** and **C8** (both Ada, same kitchen). Check that she is the
   same person in both, the first test of the bible.
3. Generate the narration for scenes 1 to 3 only and cut a 40 s test.
4. Send me the frames and I will compare them with the bible, then tune.
