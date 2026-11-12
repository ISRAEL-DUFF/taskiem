# Google Flow guide

How to build the cast in Flow so the same people appear in every clip.
Flow has no separate negative-prompt box (as far as I know; check your version), so everything below is
phrased as **what to show**. Flow changes often: treat the menu names as
approximate and verify them in your account.

## 1. Workflow

1. **Make the reference images first (still images, not video).** In Flow, use
   the image generator, not Veo, for the turnaround and portrait. Paste the
   *Image prompt* for the character below. Generate several, keep the best.
2. **Lock the face.** Pick one turnaround and one hero portrait per character.
   Save them to `approved/<character>/` and fill in `NOTES.md` (model, date,
   prompt used).
3. **Build clips from the references.** Use Flow's *Ingredients to Video*
   (reference images drive the character) and add the character's master
   description in the text too. Use *Frames to Video* only when you need an
   exact first frame (for example a hero portrait that comes to life).
4. **One character per clip when you can.** Two or more characters in one
   generation drift much more. Film them separately and cut together; for a
   two-shot, give both references and keep the shot short (4 to 8 seconds).
5. **Reuse, do not re-describe.** The character description text is copied
   verbatim every time. Only the action/setting/camera/audio parts change.
6. **Use Flow's project to keep a library** of approved references, so each
   new clip starts from the same ingredients.

## 2. The "guard" line

Add this at the end of every image prompt below (it replaces the negative prompt):

> Rich, luminous, warm skin tone with warm highlights, true to the description. Exactly one person, exactly as described, natural hands with five fingers. Plain warm paper background. No text, no logos, no watermark.

## 3. Image prompts (turnaround, then portrait)

Paste each in full. If your Flow image model ignores the "four views" layout, generate the front view first, then ask for the side and back views using the front as the reference.

### Ada Obi

**Turnaround**

```
Ada Obi, a 34-year-old Nigerian woman, rich medium-deep brown skin (warm undertone), oval face, high cheekbones, small dimple in left cheek, large warm dark-brown almond eyes, full softly arched brows, small gap between front teeth, wide easy smile; hair fully covered by a marigold-gold headwrap tied in a fan at front left with a small lime-green pin; wearing a pine-green short-sleeved work blouse with sleeves pushed up and a lime-green bib apron with a pocket holding a phone, thin gold stud earrings, faint pale oil-burn scar on back of left hand; strong practical build, brisk and warm, a cook in a busy Lagos kitchen. character turnaround reference sheet on a plain warm paper (#efeae2) background, four full-body views side by side: front, three-quarter left, side profile, back; neutral relaxed pose, arms slightly away from body, even soft lighting, identical face and outfit in every view, full body head to feet. Stylised semi-realistic animated-feature character art, soft 3D-like shading, warm natural light, expressive eyes, no outlines. Rich, luminous, warm skin tone with warm highlights, true to the description. Exactly one person, exactly as described, natural hands with five fingers. Plain warm paper background. No text, no logos, no watermark.
```

**Hero portrait**

```
Ada Obi, a 34-year-old Nigerian woman, rich medium-deep brown skin (warm undertone), oval face, high cheekbones, small dimple in left cheek, large warm dark-brown almond eyes, full softly arched brows, small gap between front teeth, wide easy smile; hair fully covered by a marigold-gold headwrap tied in a fan at front left with a small lime-green pin; wearing a pine-green short-sleeved work blouse with sleeves pushed up and a lime-green bib apron with a pocket holding a phone, thin gold stud earrings, faint pale oil-burn scar on back of left hand; strong practical build, brisk and warm, a cook in a busy Lagos kitchen. single head-and-shoulders portrait, eye-level, soft window light, plain warm paper background, looking slightly off-camera, natural relaxed expression, this is the master face reference. Stylised semi-realistic animated-feature character art, soft 3D-like shading, warm natural light, expressive eyes, no outlines. Rich, luminous, warm skin tone with warm highlights, true to the description. Exactly one person, exactly as described, natural hands with five fingers. Plain warm paper background. No text, no logos, no watermark.
```

### Tunde

**Turnaround**

```
Tunde, a 24-year-old Nigerian man, deep rich dark-brown skin, long oval face, defined cheekbones, calm medium dark-brown eyes with slightly heavy lids, straight thick brows, tiny mole under right eye, shy closed-lip smile; short black low-crop haircut with sharp hairline, clean-shaven; wearing a dark-green (#164a3b) crew-neck T-shirt with a lime-green lanyard and white ID card, dark jeans, white sneakers, slim silver watch on left wrist; lean lanky build, slight stoop, calm and careful, a shop cashier in Lagos. character turnaround reference sheet on a plain warm paper (#efeae2) background, four full-body views side by side: front, three-quarter left, side profile, back; neutral relaxed pose, arms slightly away from body, even soft lighting, identical face and outfit in every view, full body head to feet. Stylised semi-realistic animated-feature character art, soft 3D-like shading, warm natural light, expressive eyes, no outlines. Rich, luminous, warm skin tone with warm highlights, true to the description. Exactly one person, exactly as described, natural hands with five fingers. Plain warm paper background. No text, no logos, no watermark.
```

**Hero portrait**

```
Tunde, a 24-year-old Nigerian man, deep rich dark-brown skin, long oval face, defined cheekbones, calm medium dark-brown eyes with slightly heavy lids, straight thick brows, tiny mole under right eye, shy closed-lip smile; short black low-crop haircut with sharp hairline, clean-shaven; wearing a dark-green (#164a3b) crew-neck T-shirt with a lime-green lanyard and white ID card, dark jeans, white sneakers, slim silver watch on left wrist; lean lanky build, slight stoop, calm and careful, a shop cashier in Lagos. single head-and-shoulders portrait, eye-level, soft window light, plain warm paper background, looking slightly off-camera, natural relaxed expression, this is the master face reference. Stylised semi-realistic animated-feature character art, soft 3D-like shading, warm natural light, expressive eyes, no outlines. Rich, luminous, warm skin tone with warm highlights, true to the description. Exactly one person, exactly as described, natural hands with five fingers. Plain warm paper background. No text, no logos, no watermark.
```

### Bayo Adeyemi

**Turnaround**

```
Bayo Adeyemi, a 52-year-old Nigerian man, medium brown skin with a warm reddish undertone, broad square face, strong jaw, full cheeks, deep-set dark eyes with crow's-feet, thick dark brows with a few grey hairs, broad nose; full short salt-and-pepper beard, grey at the temples under a tan flat cap; wearing a rice-sack brown short-sleeve button shirt with a chest pocket and reading glasses clipped in it, khaki work trousers, brown boots, gold ring on left hand, faint rice dust on sleeves; solid stocky build with a modest belly, deliberate and dry, a rice-mill owner in Lagos. character turnaround reference sheet on a plain warm paper (#efeae2) background, four full-body views side by side: front, three-quarter left, side profile, back; neutral relaxed pose, arms slightly away from body, even soft lighting, identical face and outfit in every view, full body head to feet. Stylised semi-realistic animated-feature character art, soft 3D-like shading, warm natural light, expressive eyes, no outlines. Rich, luminous, warm skin tone with warm highlights, true to the description. Exactly one person, exactly as described, natural hands with five fingers. Plain warm paper background. No text, no logos, no watermark.
```

**Hero portrait**

```
Bayo Adeyemi, a 52-year-old Nigerian man, medium brown skin with a warm reddish undertone, broad square face, strong jaw, full cheeks, deep-set dark eyes with crow's-feet, thick dark brows with a few grey hairs, broad nose; full short salt-and-pepper beard, grey at the temples under a tan flat cap; wearing a rice-sack brown short-sleeve button shirt with a chest pocket and reading glasses clipped in it, khaki work trousers, brown boots, gold ring on left hand, faint rice dust on sleeves; solid stocky build with a modest belly, deliberate and dry, a rice-mill owner in Lagos. single head-and-shoulders portrait, eye-level, soft window light, plain warm paper background, looking slightly off-camera, natural relaxed expression, this is the master face reference. Stylised semi-realistic animated-feature character art, soft 3D-like shading, warm natural light, expressive eyes, no outlines. Rich, luminous, warm skin tone with warm highlights, true to the description. Exactly one person, exactly as described, natural hands with five fingers. Plain warm paper background. No text, no logos, no watermark.
```

### Ibinabo George

**Turnaround**

```
Ibinabo George, a 38-year-old Nigerian woman, deep warm brown skin, smooth with a soft glow, heart-shaped face, high forehead, defined cheekbones, pointed soft chin, large dark almond eyes behind round tortoiseshell glasses, neat arched brows, muted rose-brown lipstick, polite half-smile; natural black hair in a high rounded afro puff held by a thin black band; wearing a crisp white tailored shirt, pine-green blazer, black trousers, black loafers, small gold hoop earrings, slim gold watch on right wrist, leather folio; slim tall posture, sharp and polite, a corporate buyer in Lagos. character turnaround reference sheet on a plain warm paper (#efeae2) background, four full-body views side by side: front, three-quarter left, side profile, back; neutral relaxed pose, arms slightly away from body, even soft lighting, identical face and outfit in every view, full body head to feet. Stylised semi-realistic animated-feature character art, soft 3D-like shading, warm natural light, expressive eyes, no outlines. Rich, luminous, warm skin tone with warm highlights, true to the description. Exactly one person, exactly as described, natural hands with five fingers. Plain warm paper background. No text, no logos, no watermark.
```

**Hero portrait**

```
Ibinabo George, a 38-year-old Nigerian woman, deep warm brown skin, smooth with a soft glow, heart-shaped face, high forehead, defined cheekbones, pointed soft chin, large dark almond eyes behind round tortoiseshell glasses, neat arched brows, muted rose-brown lipstick, polite half-smile; natural black hair in a high rounded afro puff held by a thin black band; wearing a crisp white tailored shirt, pine-green blazer, black trousers, black loafers, small gold hoop earrings, slim gold watch on right wrist, leather folio; slim tall posture, sharp and polite, a corporate buyer in Lagos. single head-and-shoulders portrait, eye-level, soft window light, plain warm paper background, looking slightly off-camera, natural relaxed expression, this is the master face reference. Stylised semi-realistic animated-feature character art, soft 3D-like shading, warm natural light, expressive eyes, no outlines. Rich, luminous, warm skin tone with warm highlights, true to the description. Exactly one person, exactly as described, natural hands with five fingers. Plain warm paper background. No text, no logos, no watermark.
```

### Kola Adebayo

**Turnaround**

```
Kola Adebayo, a 29-year-old Nigerian man, warm medium-light brown skin with golden undertone, round friendly face, full cheeks, dimples on both cheeks, bright round dark-brown eyes that crinkle, soft brows, wide easy grin, light even stubble, always a smudge of flour on his left cheek; short black taper-fade haircut with a curly top, pencil behind right ear, silver stud in left ear; wearing a burnt-orange work shirt under a cream apron with flour handprints, dark trousers, white clogs; athletic bouncy build, warm and chatty, a baker in Lagos. character turnaround reference sheet on a plain warm paper (#efeae2) background, four full-body views side by side: front, three-quarter left, side profile, back; neutral relaxed pose, arms slightly away from body, even soft lighting, identical face and outfit in every view, full body head to feet. Stylised semi-realistic animated-feature character art, soft 3D-like shading, warm natural light, expressive eyes, no outlines. Rich, luminous, warm skin tone with warm highlights, true to the description. Exactly one person, exactly as described, natural hands with five fingers. Plain warm paper background. No text, no logos, no watermark.
```

**Hero portrait**

```
Kola Adebayo, a 29-year-old Nigerian man, warm medium-light brown skin with golden undertone, round friendly face, full cheeks, dimples on both cheeks, bright round dark-brown eyes that crinkle, soft brows, wide easy grin, light even stubble, always a smudge of flour on his left cheek; short black taper-fade haircut with a curly top, pencil behind right ear, silver stud in left ear; wearing a burnt-orange work shirt under a cream apron with flour handprints, dark trousers, white clogs; athletic bouncy build, warm and chatty, a baker in Lagos. single head-and-shoulders portrait, eye-level, soft window light, plain warm paper background, looking slightly off-camera, natural relaxed expression, this is the master face reference. Stylised semi-realistic animated-feature character art, soft 3D-like shading, warm natural light, expressive eyes, no outlines. Rich, luminous, warm skin tone with warm highlights, true to the description. Exactly one person, exactly as described, natural hands with five fingers. Plain warm paper background. No text, no logos, no watermark.
```

### Emeka Nwosu

**Turnaround**

```
Emeka Nwosu, a 31-year-old Nigerian man, very deep dark-brown skin with warm bronze highlights, long angular face, strong brow ridge, high cheekbones, narrow jaw, narrow alert dark eyes, straight dark brows, small scar through left eyebrow, thin neat goatee; wearing a lime-green open-face motorcycle helmet with a pine stripe, pine-green riding jacket with grey reflective strips, black cargo trousers, black boots, fingerless gloves, a lime insulated delivery cool box on his back; lean wiry build, calm and watchful, a delivery rider in Lagos. character turnaround reference sheet on a plain warm paper (#efeae2) background, four full-body views side by side: front, three-quarter left, side profile, back; neutral relaxed pose, arms slightly away from body, even soft lighting, identical face and outfit in every view, full body head to feet. Stylised semi-realistic animated-feature character art, soft 3D-like shading, warm natural light, expressive eyes, no outlines. Rich, luminous, warm skin tone with warm highlights, true to the description. Exactly one person, exactly as described, natural hands with five fingers. Plain warm paper background. No text, no logos, no watermark.
```

**Hero portrait**

```
Emeka Nwosu, a 31-year-old Nigerian man, very deep dark-brown skin with warm bronze highlights, long angular face, strong brow ridge, high cheekbones, narrow jaw, narrow alert dark eyes, straight dark brows, small scar through left eyebrow, thin neat goatee; wearing a lime-green open-face motorcycle helmet with a pine stripe, pine-green riding jacket with grey reflective strips, black cargo trousers, black boots, fingerless gloves, a lime insulated delivery cool box on his back; lean wiry build, calm and watchful, a delivery rider in Lagos. single head-and-shoulders portrait, eye-level, soft window light, plain warm paper background, looking slightly off-camera, natural relaxed expression, this is the master face reference. Stylised semi-realistic animated-feature character art, soft 3D-like shading, warm natural light, expressive eyes, no outlines. Rich, luminous, warm skin tone with warm highlights, true to the description. Exactly one person, exactly as described, natural hands with five fingers. Plain warm paper background. No text, no logos, no watermark.
```

## 4. Video prompt template (Veo in Flow)

Veo follows a simple order: **who, what they do, where, camera, light/style, sound**.
Put the character's master description first, then:

```
[MASTER DESCRIPTION, verbatim]. [One action, present tense, e.g. "stirs a large steel pot with one hand while checking her phone with the other"]. Setting: [place from bible section 11]. Camera: [e.g. "slow push-in, eye-level, medium shot"]. Light: warm late-morning Lagos daylight. Style: stylised semi-realistic animated-feature look, soft 3D-like shading. Audio: [kitchen sounds, gentle Afro-pop in the distance]. She says: "[one short line]".
```

Rules that keep it consistent:
- One action and one camera move per clip. Clips are short (about 8 seconds).
- Keep spoken lines short (under about 12 words) and put them in quotes, with the speaker named.
- Do **not** ask Veo to show app screens, prices or captions: it garbles text. Film the person looking at the phone and add the UI and captions in editing from `video/kit/`.
- For group shots, name each character's colour-block items (bible section 10).
- If a face drifts, re-attach the approved reference and re-state the master description; do not add adjectives (bible section 12).

### Example: Ada, scene 3 of the chat film (the morning card)

```
Ada Obi, a 34-year-old Nigerian woman, rich medium-deep brown skin (warm undertone), oval face, high cheekbones, small dimple in left cheek, large warm dark-brown almond eyes, full softly arched brows, small gap between front teeth, wide easy smile; hair fully covered by a marigold-gold headwrap tied in a fan at front left with a small lime-green pin; wearing a pine-green short-sleeved work blouse with sleeves pushed up and a lime-green bib apron with a pocket holding a phone, thin gold stud earrings, faint pale oil-burn scar on back of left hand; strong practical build, brisk and warm, a cook in a busy Lagos kitchen [paste the rest of Ada's master description]. She opens her phone, reads it, and her face relaxes into a pleased smile. Setting: Ada's Kitchen in Yaba, white tiles, steel counters, steaming pots, bright window. Camera: slow push-in from medium to close-up, eye-level, shallow depth of field. Light: warm late-morning daylight with a soft green glow from the phone on her face. Style: stylised semi-realistic animated-feature look. Audio: kitchen hum, a soft chime, gentle Afro-pop. She says: "Okay, okay. Good morning to us."
```
