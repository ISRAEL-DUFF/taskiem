# Taskiem character bible

The single source of truth for how every recurring person looks, sounds and
moves. It is written so it works in **any** tool (an image generator, a video
generator, a 3D tool, an illustrator, a live-action casting call). Nothing here
depends on one tool: the words are the design.

Names, businesses and prices match the demo world (`web/e2e/demo/seed.ts`) and
the chat film's script (`video/films/chat/script.md`).

**Status: v1, for approval.** Items marked **[decide]** are choices I made so
the bible is complete; change them here once and every prompt below inherits it.

---

## 1. How to use this bible

1. Pick the character. Copy their **Master prompt** (section 4 onward) as is.
2. Add a **scene line** after it: what they do, where, the camera, the mood.
3. Add the **Global negative prompt** (section 3).
4. Generate **reference sheets first** (one front, one three-quarter, one side,
   one expression strip per character) and approve them. From then on, feed
   the approved sheet to the tool as a reference image or character reference,
   and keep the master prompt too: image plus words together hold the face.
5. Never paraphrase the master prompt. Do not swap "deep brown" for "dark".
   Each character's **identity words** (bold in the prompt) must appear every time.
6. If a result drifts, fix it with the **Drift checklist** (section 12), not by
   adding adjectives.

Keep every approved sheet, seed, model name and version in
`video/characters/approved/<character>/` with a `NOTES.md` saying the tool and
settings used, so a result can be reproduced or moved to another tool.

---

## 2. World and style lock

**The world.** Lagos, Nigeria, now. Small food and trade businesses: a kitchen
in Yaba, a rice mill, a staff canteen on the Island, a bakery, a cold-chain
van. Warm, busy, real: Lagos light, not a postcard. Phones are always in hand
(the product is a chat app). WhatsApp-green and pine-green UI glows on faces.

**Look (the "house style"). [decide]** Stylised, warm, semi-realistic
character illustration with soft 3D-like shading, in the spirit of a modern
animated feature (clean shapes, believable proportions, expressive faces),
**not** photoreal and **not** a flat icon. Proportions are realistic adult
(about 7.5 heads tall), with slightly enlarged, expressive eyes. Soft
rounded forms, gentle rim light, no harsh outlines.

*Why not the flat vector in `cast-sheet-v1.png`:* AI tools hold a face better
when there is shading and structure to anchor to. The flat sheet stays as the
**in-app avatar style** (chat bubbles, thumbnails), described per character in
"Avatar version" below, so both styles show the same person.

**Palette.** Brand colours appear on every character exactly once as an accent
and are the only saturated colours in wardrobe unless stated:

| Name | Hex | Use |
| --- | --- | --- |
| Pine | `#0d3b2e` | Ada's and staff clothing, UI |
| Pine 2 | `#164a3b` | Tunde's top |
| Lime | `#c8f169` | The one accent on each character |
| Paper | `#efeae2` | Backgrounds, warm neutrals |
| Peach | `#fde3d8` | Soft backgrounds |
| Warn | `#fdf0dc` | Warm backgrounds |
| Ink | `#0e1a14` | Hair, text |

**Skin.** All six are Black Nigerian adults. Skin is rendered **warm,
luminous and true**: never grey, ashy, shiny-oily or washed out; highlights
warm gold, shadows warm umber. Each character has a distinct tone (below) so
the cast reads as individuals. Hex values are the mid-tone under neutral light.

**Lighting.** Soft warm daylight by default (golden Lagos late morning); phone
glow on faces in chat scenes; deep teal-blue evening for the "dark" scenes.
Never harsh flash, never cold fluorescent unless a scene says "canteen office".

**Camera.** Mostly eye-level, medium and close-up, 35mm look, shallow depth of
field. Vertical 9:16 for shorts, 16:9 for films, always keep the face in the
safe middle third.

**Rules for everyone**
- Same face every time: facial structure, skin tone, hair, and the signature
  items below do not change between scenes.
- Respectful, dignified portrayal. No caricature, no exaggerated features, no
  stereotypes of poverty or chaos. These are competent business owners.
- Real Nigerian setting details (Naira notes, jollof, danfo/bike traffic in
  the background, wall tiles, generators) but uncluttered.
- No text or logos generated inside images by the tool (it garbles them). Add
  UI, prices and captions in editing, from the kit.

---

## 3. Global prompts

**Style suffix (add to every image/video prompt):**

> stylised semi-realistic animated-feature character art, soft 3D-like shading,
> warm Lagos daylight, shallow depth of field, clean shapes, expressive eyes,
> natural proportions, rich warm skin tones, no outlines, high detail, 35mm

**Global negative prompt (add to every generation):**

> photorealistic skin pores, uncanny valley, plastic skin, ashy or grey skin,
> washed-out skin, shiny oily skin, lightened or bleached skin, exaggerated lips
> or features, caricature, racial stereotype, extra fingers, malformed hands,
> deformed phone, garbled text, random logos, watermark, subtitles in image,
> changed hairstyle, different face, different age, different outfit, western
> suburban setting, snow, cold blue fluorescent lighting, cluttered background,
> cartoon outlines, anime, chibi

---

## 4. Ada Obi: the hero

**Who she is.** Owner and head cook of **Ada's Kitchen**, Yaba, Lagos. Cooks
fresh every morning for offices and party orders. Fast, warm, a little
overworked, always mid-task, always three messages behind, and good-humoured
about it. She is the audience: a small-business owner who runs everything
through chat. Never a victim; the product makes her calmer, not rescued.

**Age and build.** 34. 1.68 m (5'6"). Strong, practical build, broad
shoulders from lifting pots; moves briskly. Posture: upright, leaning slightly
forward when working.

**Skin.** Rich medium-deep brown, warm undertone, mid-tone `#7a4a2e`. Warm
gold highlights on cheekbones and nose bridge; slightly flushed from kitchen
heat on cheeks and forehead (subtle, never sweaty-shiny).

**Face.** Oval face, high rounded cheekbones, soft strong jaw, small dimple in
the left cheek when she smiles. Forehead average height. Never a thin or
sharp face.

**Eyes.** Large, warm dark-brown almond eyes, slightly upturned outer corners,
thick natural lashes (no heavy makeup). Eyes are the most expressive part:
they narrow when she is calculating, widen when pleasantly surprised.

**Brows.** Full, softly arched, dark brown-black, natural; the right brow lifts
a little higher than the left when she is sceptical or amused (her tell).

**Nose and mouth.** Medium nose, softly rounded tip, balanced width. Full lips
with natural warm-brown colour, no lipstick on kitchen days; wide easy smile
showing top teeth, a small gap between the two front teeth.

**Hair.** Always covered by a **headwrap (gele-style kitchen wrap) in
warm marigold-gold fabric**, tied in a soft fan at the front left, a small
**lime-green enamel pin** at the knot. A few short natural coils show at the
temples. The wrap colour is constant in every scene (marigold `#e8a33d`);
only the pin is lime.

**Marks and details.** Small scar of an old oil burn on the back of her left
hand (a faint pale mark, 2 cm). Short, clean, unpainted nails. Thin gold stud
earrings. No necklace.

**Signature outfit (the default; use in 90% of scenes).** Pine-green
(`#0d3b2e`) short-sleeved work blouse, sleeves pushed up to the forearm,
a **lime-green (`#c8f169`) bib apron** with one square pocket holding her
phone, tied at the waist. Plain dark trousers. Flat closed shoes.
*Alternates (use only when the scene needs):* **Party day**: same wrap, cream
lace blouse, pine apron. **Evening**: wrap off, hair in short neat natural
twists, pine cardigan over the blouse, no apron.

**Props.** A phone in a pine-green case (always the chat app). A wooden
spoon when cooking. A steel pot with a lid for jollof scenes.

**Gestures and body.** Taps the phone with her thumb while stirring with the
other hand. Tucks the phone under her chin to free her hands. Says "okay,
okay" with a quick double nod. Wipes her forearm across her brow.

**Expressions (the set).** *Neutral/working:* focused, lips slightly pressed.
*Relief:* long exhale, shoulders drop, soft smile. *Delight:* wide smile, dimple,
eyes crinkle. *Worry:* brows tilt up in the middle, lower lip tucked. *Haggling
(poker face):* head tilted, one brow up, half smile. *Pride:* chin lifts a
little, small closed smile.

**Voice.** Warm alto, brisk pace, Lagos English with light Yoruba rhythm;
upward lilt on questions; laughs easily. Calls people "my dear" and "boss".

**Avatar version (flat, for the app).** Round head, marigold wrap with a
lime pin, pine top, lime apron, dot eyes, wide smile. As in `sheet.html`.

**Master prompt (copy exactly):**

> **Ada Obi**, a 34-year-old **Nigerian woman**, **rich medium-deep brown skin
> (warm undertone)**, **oval face, high cheekbones, small dimple in left cheek**,
> large warm dark-brown almond eyes, full softly arched brows, small gap between
> front teeth, wide easy smile; **hair fully covered by a marigold-gold
> headwrap tied in a fan at front left with a small lime-green pin**; wearing a
> **pine-green short-sleeved work blouse with sleeves pushed up and a
> lime-green bib apron with a pocket holding a phone**, thin gold stud earrings,
> faint pale oil-burn scar on back of left hand; strong practical build,
> brisk and warm, a cook in a busy Lagos kitchen

---

## 5. Tunde: the cashier

**Who he is.** Ada's cashier, hired recently. Learns fast, careful with
money, proud of the trust Ada gives him. He sees only what his role needs
(Sell, Stock, Orders) and is relaxed about the limits ("that's above my
limit, let me ask Madam"). Calm under pressure; the steady one. Full name
in the app is just "Tunde".

**Age and build.** 24. 1.80 m (5'11"). Lean, slightly lanky, long arms; a
slight stoop from leaning over the counter. Moves smoothly, unhurried.

**Skin.** Deep, rich dark brown, cool-warm balanced undertone, mid-tone
`#5b3622`. Highlights are warm bronze; keep the skin luminous, never grey.

**Face.** Long oval, defined cheekbones, narrow chin, smooth jaw. Youthful.

**Eyes.** Medium, dark-brown, calm and level, a little heavy-lidded (relaxed,
attentive, not sleepy). Short natural lashes.

**Brows.** Straight, thick, neatly kept. They knit slightly when counting.

**Nose and mouth.** Straight nose, medium width. Lips medium-full, relaxed;
a small, shy, closed-lip smile by default, a bigger toothy grin when proud.

**Hair.** Short, black, neatly cut **low crop with a clean sharp hairline**
and a subtle fade at the sides; a slight natural curl on top. No facial hair
(clean-shaven, smooth).

**Marks and details.** Tiny mole under the right eye. Slim silver
wristwatch on the left wrist. Short clean nails.

**Signature outfit.** **Pine-2 (`#164a3b`) crew-neck T-shirt** with a small
lime stitched chest mark; a **lime-green lanyard** with a white ID card
("Tunde, Cashier", handled as an editing overlay, not generated text);
**dark jeans**; **clean white sneakers**.
*Alternates:* **Closing shift**: pine hoodie, lanyard off, backpack over one
shoulder. **Busy day**: sleeves rolled, ID card flipped over his chest.

**Props.** A phone (black case) held in one hand while the other counts
notes or taps the till. A stack of Naira notes squared neatly. A small
receipt printer on the counter.

**Gestures and body.** Taps twice on the counter before confirming. Counts
with a thumb flick. Touches the lanyard card when about to ask for approval.
Gentle nod, "yes ma".

**Expressions.** *Default:* calm, closed-lip half smile. *Focus:* brows knit,
lips pressed. *Pleased/proud:* wide toothy grin, looks down shyly. *Unsure
(over limit):* eyes flick to the phone, brows up, small "hmm" mouth.
*Relieved:* slow exhale, shoulders drop.

**Voice.** Mid-low baritone, soft and steady, slower than Ada; polite,
Lagos English with a gentle Yoruba lilt. "Yes ma." "No wahala." "Let me check."

**Avatar version.** Short dark hair, pine top, lime lanyard, dot eyes,
calm smile.

**Master prompt (copy exactly):**

> **Tunde**, a 24-year-old **Nigerian man**, **deep rich dark-brown skin**,
> **long oval face, defined cheekbones**, calm medium dark-brown eyes with
> slightly heavy lids, straight thick brows, tiny mole under right eye,
> shy closed-lip smile; **short black low-crop haircut with sharp hairline,
> clean-shaven**; wearing a **dark-green (#164a3b) crew-neck T-shirt with a
> lime-green lanyard and white ID card, dark jeans, white sneakers**, slim
> silver watch on left wrist; lean lanky build, slight stoop, calm and
> careful, a shop cashier in Lagos

---

## 6. Bayo Adeyemi: the supplier

**Who he is.** Owner of **Bayo Rice Mills**, supplier of rice and grains.
Sells by the bag, in bulk. Dry humour, patient, old-school but now on his
phone because his customers are. The reliable elder: slow to promise, never
misses a delivery. Warm underneath the gruffness.

**Age and build.** 52. 1.75 m (5'9"). Solid, stocky, barrel chest, a modest
belly, thick forearms; moves deliberately, hands on hips when thinking.

**Skin.** Medium brown with a warm reddish undertone, mid-tone `#8a5634`;
sun-weathered, light laugh lines. Warm, healthy, never grey.

**Face.** Broad, square-ish face, strong jaw, full cheeks, a slightly
furrowed forehead.

**Eyes.** Smallish, deep-set dark eyes with crow's-feet; the eyes twinkle
when he teases. A habit of looking over his glasses (he holds reading
glasses in his pocket, not on his face).

**Brows.** Thick, dark with a few grey hairs; they do most of his acting
(one rises when he is unconvinced).

**Nose and mouth.** Broad nose, strong bridge. Lips medium, mostly set in a
straight line; a rare slow, wide, warm smile.

**Hair and beard.** Short, black hair with **grey at the temples** under a
**flat-top cap**. A **full short beard, salt-and-pepper (mostly black with
grey at the chin)**, neatly trimmed along the cheek line.

**Marks and details.** Reading glasses clipped in his shirt pocket. Gold
band ring on the left hand. Rolled sleeves, flour-like rice dust on forearms.

**Signature outfit.** **Rice-sack brown (`#b08968`) short-sleeve button
shirt** with a chest pocket, **tan flat cap (`#7f5539`)**, **khaki work
trousers**, **sturdy brown boots**. A faint dusting of rice powder on his
sleeves.
*Alternates:* **Formal (deal day)**: agbada-style plain cream kaftan top.
**Warehouse**: add a sleeveless safety vest, cap unchanged.

**Props.** A phone held at arm's length (he reads the screen far away). A
scoop and a 25 kg bag. A small notebook he still keeps despite the app.

**Gestures and body.** Arms folded when listening. Strokes his beard before
naming a price. A slow, single nod. Taps the bag twice: "good rice".

**Expressions.** *Default:* dry, straight mouth, one brow slightly up.
*Amused:* eyes crinkle, mouth stays straight, shoulders shake once.
*Convinced:* slow wide smile. *Firm (price):* arms folded, jaw set.

**Voice.** Deep baritone, gravelly, slow, unhurried; Yoruba-inflected English.
Short sentences. "Ah-ah, that price?" "Okay, for you."

**Avatar version.** Tan cap, beard, rice-brown top, dot eyes, straight mouth
with a slight smile.

**Master prompt (copy exactly):**

> **Bayo Adeyemi**, a 52-year-old **Nigerian man**, **medium brown skin with a
> warm reddish undertone**, **broad square face, strong jaw, full cheeks,
> deep-set dark eyes with crow's-feet**, thick dark brows with a few grey hairs,
> broad nose; **full short salt-and-pepper beard, grey at the temples under a
> tan flat cap**; wearing a **rice-sack brown short-sleeve button shirt with a
> chest pocket and reading glasses clipped in it, khaki work trousers, brown
> boots**, gold ring on left hand, faint rice dust on sleeves; solid stocky
> build with a modest belly, deliberate and dry, a rice-mill owner in Lagos

---

## 7. Ibinabo George: the buyer

**Who she is.** Procurement lead at **Rivers Foods Ltd**, which runs staff
canteens for banks and offices on the Island. Sharp, polished, polite and
immovable on price; she wins by being pleasant. The negotiating counterpart
to Ada, never an antagonist: both leave happy.

**Age and build.** 38. 1.72 m (5'8"). Slim, tall posture, deliberate
movements, always seems to have a plan.

**Skin.** Deep warm brown, mid-tone `#6a3f27`, smooth with a soft glow;
golden highlights; healthy.

**Face.** Heart-shaped face, high forehead, strong defined cheekbones,
pointed but soft chin.

**Eyes.** Large, dark, almond, **always framed by round tortoiseshell
glasses** (thin acetate frame, brown-amber). Direct gaze.

**Brows.** Neat, defined, arched with precision.

**Nose and mouth.** Slim nose. Lips medium, in a **muted rose-brown
lipstick** (always). A polite half-smile; a full smile is rare and lands.

**Hair.** Natural black hair worn as a **high, rounded afro puff** with a
neat shaped hairline, volume held up by a **thin black band** at the
base; clean sides. Always the same shape.

**Marks and details.** Small gold hoop earrings. Slim gold watch on the
right wrist. Manicured short nails in a neutral nude. A slim leather
folio.

**Signature outfit.** **Crisp white tailored shirt** with the collar open
and a **pine-green blazer thrown over her shoulders or worn on**, **black
tailored trousers**, **low black loafers**. Minimal, sharp.
*Alternates:* **Site visit**: white shirt, sleeves cuffed, no blazer, a
canteen-staff lanyard. **Evening**: black fitted dress, same earrings and
glasses.

**Props.** Phone with a white case; a leather folio; a pen she clicks. On
WhatsApp she is brisk and lower-case.

**Gestures and body.** Taps a pen on the folio while thinking. Pushes her
glasses up with one finger. Tilts her head and says nothing (the pause is
her power). Small, deliberate hand gestures.

**Expressions.** *Default:* polite half-smile. *Negotiating:* head tilted,
one brow up, eyes steady. *Agreement:* full warm smile, glasses catch light.
*Considering:* looks up-left, taps pen, lips pressed.

**Voice.** Smooth mezzo, measured, precise; Nigerian English with polished
corporate clarity (Port Harcourt roots, a soft rolling "r"). "Let's meet in
the middle." "Eight hundred and twenty. Final."

**Avatar version.** Round puff hair, round glasses, white top with a pine
collar, dot eyes behind the glasses.

**Master prompt (copy exactly):**

> **Ibinabo George**, a 38-year-old **Nigerian woman**, **deep warm brown
> skin, smooth with a soft glow**, **heart-shaped face, high forehead, defined
> cheekbones, pointed soft chin**, large dark almond eyes behind **round
> tortoiseshell glasses**, neat arched brows, muted rose-brown lipstick,
> polite half-smile; **natural black hair in a high rounded afro puff held by
> a thin black band**; wearing a **crisp white tailored shirt, pine-green
> blazer, black trousers, black loafers**, small gold hoop earrings, slim gold
> watch on right wrist, leather folio; slim tall posture, sharp and polite,
> a corporate buyer in Lagos

---

## 8. Kola Adebayo: the baker

**Who he is.** Owner of **Kola's Bakery**, Ada's neighbour in Lagos whom she
finds through Explore. Friendly, chatty, generous, a little scatterbrained,
always dusted in flour. Represents "the business next door you should be
trading with".

**Age and build.** 29. 1.77 m (5'10"). Athletic from kneading dough; broad
shoulders, strong forearms; bouncy, energetic stride.

**Skin.** Warm medium-light brown with golden undertone, mid-tone `#9a6a45`;
freckle-free, sun-kissed. Flushed cheeks near the oven. Healthy, never
sallow or grey.

**Face.** Round, friendly face, soft jaw, full cheeks, boyish.

**Eyes.** Bright, round, dark-brown eyes that crinkle at the corners; very
expressive, a "told you so" sparkle.

**Brows.** Medium, soft, quick to jump when he is excited.

**Nose and mouth.** Rounded nose, small flat bridge. Wide, easy, gap-free
grin; dimples on both cheeks.

**Hair.** Short black hair in a **neat taper fade with a slightly longer
curly top**, often with a speck of flour. **Light stubble** on jaw, kept
short and even.

**Marks and details.** Flour smudge on the left cheek, always (a character
signature, redraw each time). A pencil behind the right ear. Silver
earring stud, left ear. A flour-dusted wristband.

**Signature outfit.** **Burnt-orange (`#bc6c25`) short-sleeve work shirt**
covered by a **cream apron with flour handprints**, **dark loose trousers**,
**white kitchen clogs**. A small white paper baker's hat folded in his back
pocket (rarely worn).
*Alternates:* **Delivery**: same shirt, no apron, a canvas tote of bread.
**Meet-up**: navy polo, flour mark stays.

**Props.** A phone with a wipe-clean case; fresh-baked loaves, meat pies,
a bread peel; a flour-dusted tray.

**Gestures and body.** Talks with both hands, flour flying. Pats his apron
before shaking hands. Leans in conspiratorially. Laughs with his head back.

**Expressions.** *Default:* big friendly grin. *Excited:* eyes wide, brows up,
mouth open. *Sheepish:* rubs back of neck. *Proud:* chest out, holds a
loaf up. *Surprised:* flour puffs from his hair.

**Voice.** Bright tenor, quick, enthusiastic; light, laughing Lagos English
with Yoruba exclamations ("Oya!" "Haba!"). Talks fast, laughs mid-sentence.

**Avatar version.** Short dark hair with a fade, orange top with flour
dots, dot eyes, wide grin.

**Master prompt (copy exactly):**

> **Kola Adebayo**, a 29-year-old **Nigerian man**, **warm medium-light brown
> skin with golden undertone**, **round friendly face, full cheeks, dimples
> on both cheeks, bright round dark-brown eyes that crinkle**, soft brows,
> wide easy grin, light even stubble, **always a smudge of flour on his left
> cheek**; **short black taper-fade haircut with a curly top**, pencil behind
> right ear, silver stud in left ear; wearing a **burnt-orange work shirt
> under a cream apron with flour handprints, dark trousers, white clogs**;
> athletic bouncy build, warm and chatty, a baker in Lagos

---

## 9. Emeka Nwosu: the rider

**Who he is.** Runs **CoolBox Logistics**, chilled vans and riders with
cool boxes for kitchens and caterers; he rides himself too. Always moving,
always on time, plain-spoken, proud of his reputation for same-day. The
Deliveries part of the app is his.

**Age and build.** 31. 1.82 m (6'0"). Lean, wiry, fit; quick, efficient
movements, relaxed in the saddle of a bike.

**Skin.** Very deep dark brown, cool-neutral undertone, mid-tone `#4d2f1e`;
lit with warm bronze highlights so he never reads as flat. Skin stays rich
and luminous in low light (key for evening bike scenes).

**Face.** Long angular face, strong brow ridge, high cheekbones, narrow
jaw; a calm, watchful face.

**Eyes.** Narrow, dark, alert, a hint of tiredness; he scans constantly.

**Brows.** Straight, dark, low; always slightly furrowed (concentration).

**Nose and mouth.** Straight, defined nose. Lips medium; a rare quick
side-grin with one corner lifting.

**Hair and beard.** Hair is a **short, very close crop under a helmet**
(rarely visible); **a thin neat goatee** with a clean jaw line.

**Marks and details.** A **small scar through the left eyebrow** (old bike
fall). Silver chain with a small cross under the shirt (visible only at
the collar). Fingerless riding gloves.

**Signature outfit.** **Lime (`#c8f169`) open-face riding helmet** with a
pine stripe, **pine-green (`#0d3b2e`) hi-vis-style riding jacket** with
reflective grey strips, **black cargo trousers**, **black riding boots**,
fingerless gloves, and a **lime insulated delivery cool box** (a recognisable
shape: boxy backpack) on his back or on the bike.
*Alternates:* **Van day**: no helmet, pine polo with a lime CoolBox patch,
sunglasses on the collar. **Evening**: reflective strips glow in lights.

**Props.** A phone in a handlebar mount; the lime cool box; a motorcycle
(clean, small-engine, dark pine paint); a delivery slip (editing overlay).

**Gestures and body.** Checks the phone mount and nods. Two-finger salute.
Lifts the cool box lid and shows the contents like a magician. Idles the
engine while reading.

**Expressions.** *Default:* watchful, brows low. *Satisfied (delivered):*
slow side-grin, one corner. *Stressed (traffic):* jaw tight, eyes flick.
*Friendly:* two-finger salute, grin.

**Voice.** Mid baritone, clipped, efficient; Igbo-inflected English, plain
and direct. Few words. "On my way." "Delivered." "Two minutes."

**Avatar version.** Lime helmet with a pine stripe, dark skin, dot eyes,
goatee, small smile.

**Master prompt (copy exactly):**

> **Emeka Nwosu**, a 31-year-old **Nigerian man**, **very deep dark-brown skin
> with warm bronze highlights**, **long angular face, strong brow ridge,
> high cheekbones, narrow jaw**, narrow alert dark eyes, straight dark brows,
> small scar through left eyebrow, **thin neat goatee**; wearing a **lime-green
> open-face motorcycle helmet with a pine stripe, pine-green riding jacket
> with grey reflective strips, black cargo trousers, black boots, fingerless
> gloves, a lime insulated delivery cool box on his back**; lean wiry build,
> calm and watchful, a delivery rider in Lagos

---

## 10. Group notes: staging, scale and chemistry

**Relative heights** (so group shots stay consistent): Emeka 1.82 > Tunde
1.80 > Kola 1.77 > Bayo 1.75 > Ibinabo 1.72 > Ada 1.68. (Ibinabo is a head
above Ada in heels; wear flats for parity.)

**Colour blocking in a group** (so you can find each person at a glance):
Ada = **gold wrap + lime apron**; Tunde = **pine tee + lime lanyard**;
Bayo = **brown shirt + tan cap**; Ibinabo = **white shirt + pine blazer**;
Kola = **orange shirt + cream apron**; Emeka = **lime helmet + pine jacket**.

**Relationships (for blocking and eye lines).** Ada trusts Tunde and lets him
run the till. Ada and Bayo are old trading partners (warm teasing). Ada and
Ibinabo respect each other as hard bargainers. Kola is Ada's friendly
neighbour who she meets through the app. Emeka serves everyone; seen last, in
motion, on the way out of frame.

**Three-shot rule.** In any shot with three or more characters, give each
only their colour-block items clearly; keep faces readable and at least one
signature item visible for each.

---

## 11. Shared props, places and key shots

**Props to describe once and reuse:** the phone UI is **never** generated;
screens are laid in during editing from the kit. Reuse: Ada's pine phone case,
Tunde's black case, Bayo's old Android with a cracked screen protector, Ibinabo's
white case, Kola's flour-wiped case, Emeka's mounted phone.

**Places (consistent sets):**
1. **Ada's Kitchen, Yaba**: bright, white-tiled, steel counters, big pots,
   a window onto a yellow-painted street wall, green-and-white food packs.
2. **Bayo Rice Mills**: warehouse, burlap rice sacks stacked high, a
   hanging scale, dust in sunlight.
3. **Rivers Foods canteen office, Victoria Island**: clean, modern, glass,
   neutral greys with a pine accent.
4. **Kola's Bakery**: warm, brick oven, trays of loaves, steam, flour.
5. **The street**: a typical Lagos road, yellow danfo buses, okada bikes,
   green and orange painted walls, mid-distance blur.

---

## 12. Drift checklist (when a result looks wrong)

Compare against the approved reference sheet for the character.

| If it changes... | Fix |
| --- | --- |
| Skin lighter/greyer | Re-state the exact skin line from the master prompt; add "warm bronze highlights, rich luminous skin" and put "lightened skin, ashy" in negative |
| Hair/wrap/cap | Re-state the hair line verbatim; add the colour hex |
| Face shape | Re-state the face line and the **age**; pass the reference image |
| Age (too young/old) | State age as a number and add "adult, mid-thirties" etc. |
| Outfit | Re-state the signature outfit verbatim; remove other clothing words from the scene line |
| Extra people | Say "only one person in frame" |
| Mark missing | Re-add the mark (Ada's scar, Kola's flour smudge, Emeka's brow scar, Tunde's mole) |

Always check **hands** (five fingers, phone not warped) and **eye line** (looking
at the phone or the person, not the lens, unless the shot calls for it).

---

## 13. Open decisions

1. **[decide] House style.** Stylised semi-realistic vs the flat vector in
   `cast-sheet-v1.png`. This bible assumes semi-realistic for films and flat
   for app avatars; change section 2 to switch.
2. **[decide] Ages and tones.** All adults 24 to 52. Skin tones are specified
   in hex but must be checked by eye on approved sheets, and ideally reviewed
   by Nigerian team members for authenticity and respect.
3. **[decide] Voices.** Described but not cast. Choose voice actors or a
   synthetic voice and record the voice IDs here.
4. **[decide] Names.** "Taskiem" is a working name pending a trademark
   search; the business and character names are from the demo world and
   are fictional.
5. **Missing cast.** A WhatsApp customer, a second staff member (a rider's
   dispatcher), and the "Taskiem" assistant (does it have a face, or is it
   the relay mark only?). Ask before adding.
