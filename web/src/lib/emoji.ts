// The emoji panel's characters: Unicode's own, grouped and named here, so
// the app asks for no image set and no outside host (a device draws them as
// it draws any text, so they look a little different on each).

export interface EmojiItem {
  char: string;
  name: string;
  /** More words that find it: "happy" finds 😀. */
  words: string;
}

export interface EmojiGroup {
  id: string;
  label: string;
  icon: string;
  items: EmojiItem[];
}

// One emoji a line: the character, its name, then words that find it after "|".
const SOURCE: [string, string, string, string][] = [
  [
    "smileys",
    "Smileys",
    "😀",
    `😀 grinning face|happy smile
😃 smiling face|happy smile
😄 smiling eyes|happy laugh
😁 beaming face|grin happy
😆 laughing|haha lol
😅 sweat smile|relief nervous
🤣 rolling on the floor laughing|lol haha funny
😂 tears of joy|lol laugh funny
🙂 slight smile|ok fine
😉 wink|flirt
😊 blushing smile|happy pleased
😇 halo|angel innocent
🥰 smiling with hearts|love adore
😍 heart eyes|love like
🤩 star struck|wow amazing
😘 blowing a kiss|love kiss
😋 yum|tasty delicious food
😛 tongue out|silly
😜 winking tongue|joke silly
🤔 thinking|hmm wonder
🤨 raised eyebrow|doubt sceptical
😐 neutral face|meh
😑 expressionless|blank
😶 no mouth|silent speechless
🙄 rolling eyes|annoyed whatever
😏 smirk|smug
😬 grimace|awkward oops
😌 relieved|calm
😔 pensive|sad thoughtful
😴 sleeping|tired sleep
😷 face mask|sick ill
🤒 thermometer face|sick fever
🤕 bandage|hurt injured
🥵 hot face|heat sweating
🥶 cold face|freezing
😎 sunglasses|cool
🤓 nerd|clever
😕 confused|puzzled
😟 worried|concerned
🙁 frown|sad
😮 open mouth|surprised
😲 astonished|shock
😳 flushed|embarrassed
🥺 pleading|please beg
😢 crying|sad tear
😭 loudly crying|sob sad
😱 screaming|shock fear
😖 confounded|frustrated
😞 disappointed|sad let down
😓 downcast sweat|stress
😩 weary|tired
😤 steam from nose|angry determined
😡 angry|mad rage
🤬 cursing|swearing angry
💀 skull|dead
🤡 clown|silly
👻 ghost|boo
👍 thumbs up|yes ok good approve
👎 thumbs down|no bad
👌 ok hand|fine perfect
✌️ victory|peace
🤞 crossed fingers|luck hope
🤝 handshake|deal agree
👏 clapping|well done applause
🙌 raised hands|celebrate hooray
🙏 folded hands|please thanks pray
💪 flexed biceps|strong
👋 waving hand|hello bye
✋ raised hand|stop high five
👈 pointing left|
👉 pointing right|
👆 pointing up|
👇 pointing down|
☝️ index up|one
🫡 salute|respect
🤷 shrug|dont know
🤦 facepalm|oh no
💁 information desk|tell
🙋 raising hand|question me
👀 eyes|look see
❤️ red heart|love
🧡 orange heart|love
💛 yellow heart|love
💚 green heart|love
💙 blue heart|love
💜 purple heart|love
🖤 black heart|love
🤍 white heart|love
💔 broken heart|sad
💕 two hearts|love
💯 hundred|perfect score
🔥 fire|hot lit
✨ sparkles|new shiny
🎉 party popper|celebrate congratulations
🎊 confetti|celebrate`,
  ],
  [
    "nature",
    "Animals and nature",
    "🐶",
    `🐶 dog|puppy pet
🐱 cat|kitten pet
🐭 mouse|
🐹 hamster|
🐰 rabbit|bunny
🦊 fox|
🐻 bear|
🐼 panda|
🐨 koala|
🐯 tiger|
🦁 lion|
🐮 cow|
🐷 pig|
🐸 frog|
🐵 monkey|
🐔 chicken|hen
🐧 penguin|
🐦 bird|
🐤 chick|
🦅 eagle|
🦆 duck|
🦉 owl|
🐴 horse|
🐝 bee|honey
🦋 butterfly|
🐢 turtle|
🐍 snake|
🐠 fish|
🐟 fish|
🐬 dolphin|
🐳 whale|
🐘 elephant|
🐐 goat|
🐑 sheep|
🐓 rooster|
🌳 tree|
🌴 palm tree|
🌱 seedling|plant grow
🌿 herb|plant
🍀 four leaf clover|luck
🌹 rose|flower
🌻 sunflower|flower
🌼 blossom|flower
🌷 tulip|flower
🌞 sun with face|sunny
☀️ sun|sunny
🌙 crescent moon|night
⭐ star|favourite
🌈 rainbow|
☁️ cloud|
🌧️ rain|rainy
⛈️ thunderstorm|
❄️ snowflake|cold
💧 droplet|water
🌊 wave|sea`,
  ],
  [
    "food",
    "Food and drink",
    "🍚",
    `🍚 cooked rice|rice
🍛 curry rice|
🍲 pot of food|stew soup
🍜 noodles|
🍝 spaghetti|pasta
🍞 bread|
🥖 baguette|bread
🥚 egg|
🍳 cooking|fried egg
🍗 chicken leg|meat
🍖 meat on bone|
🥩 steak|meat
🍔 burger|
🍟 fries|chips
🍕 pizza|
🌮 taco|
🥗 salad|
🍅 tomato|
🌶️ pepper|chilli hot
🧅 onion|
🥔 potato|
🍠 sweet potato|yam
🌽 corn|maize
🥕 carrot|
🍌 banana|plantain
🍎 apple|
🍊 orange|
🍋 lemon|
🍉 watermelon|
🍍 pineapple|
🥭 mango|
🍇 grapes|
🥥 coconut|
🍪 cookie|biscuit
🍰 cake|
🎂 birthday cake|
🍫 chocolate|
🍬 candy|sweet
🍦 ice cream|
☕ coffee|tea hot drink
🍵 tea|
🥤 cup with straw|drink soda
🍺 beer|
🍷 wine|
🥂 clinking glasses|cheers
🍾 champagne|celebrate
🧃 juice box|
🥛 milk|
💦 water|sweat`,
  ],
  [
    "activity",
    "Activities and travel",
    "🚚",
    `🚚 delivery truck|lorry transport
🚛 lorry|truck transport
🚗 car|
🚕 taxi|
🚌 bus|
🏍️ motorcycle|bike okada
🚲 bicycle|
✈️ aeroplane|flight plane
🚢 ship|boat
🏠 house|home
🏢 office building|
🏪 shop|store convenience
🏬 department store|market
🏭 factory|
🏗️ construction|building site
⛽ fuel pump|petrol
🗺️ map|
📍 pin|location place
⏰ alarm clock|time
⌛ hourglass|wait
📅 calendar|date
⚽ football|soccer
🏀 basketball|
🏆 trophy|win
🥇 gold medal|first winner
🎯 bullseye|target
🎵 music note|song
🎤 microphone|
🎬 clapper|film
🎁 gift|present
🎈 balloon|party
🎓 graduation cap|school
🕌 mosque|
⛪ church|
🇳🇬 Nigeria flag|nigeria`,
  ],
  [
    "objects",
    "Objects",
    "💰",
    `💰 money bag|cash
💵 dollar|cash money
💳 credit card|card pay
🪙 coin|money
💸 money flying|spend
🏧 cash machine|atm
🧾 receipt|invoice bill
📦 package|parcel box order
📫 mailbox|post
📨 incoming envelope|email message
✉️ envelope|email letter
📝 memo|note write
📋 clipboard|list
📎 paperclip|attach
📌 pushpin|pin
📊 bar chart|report stats
📈 chart up|growth profit
📉 chart down|loss
🗂️ index dividers|files
📁 folder|
📞 telephone|call phone
📱 mobile phone|phone
💻 laptop|computer
🖨️ printer|
📷 camera|photo
🔑 key|
🔒 locked|secure
🔓 unlocked|
🔔 bell|notification
🔕 bell off|mute
💡 light bulb|idea
🔧 wrench|tool
🛠️ tools|repair
⚙️ gear|settings
🧰 toolbox|
🛒 shopping cart|buy
🛍️ shopping bags|
⚖️ scales|balance weight
🧮 abacus|count
✂️ scissors|cut
🪑 chair|
🛏️ bed|
🚪 door|
👕 t-shirt|clothes
👗 dress|clothes
👟 sneaker|shoe
👜 handbag|bag
🧵 thread|sewing
🧶 yarn|
🪡 needle|sewing
🧴 lotion bottle|cosmetics
💊 pill|medicine
🔬 microscope|`,
  ],
  [
    "symbols",
    "Symbols",
    "✅",
    `✅ check mark|done yes correct
❌ cross mark|no wrong cancel
❎ cross mark button|no
✔️ check|yes tick
✖️ multiply|cross
➕ plus|add
➖ minus|subtract
➗ divide|
❓ question mark|
❔ white question|
❗ exclamation|important
❕ white exclamation|
‼️ double exclamation|
⚠️ warning|careful
🚫 prohibited|not allowed
⛔ no entry|
🔴 red circle|
🟠 orange circle|
🟡 yellow circle|
🟢 green circle|
🔵 blue circle|
⚫ black circle|
⚪ white circle|
🔺 red triangle|
▶️ play|
⏸️ pause|
🔁 repeat|
🔄 counterclockwise arrows|refresh
⬆️ up arrow|
⬇️ down arrow|
⬅️ left arrow|
➡️ right arrow|
↩️ return|back
🆗 OK button|
🆕 NEW button|
🆓 FREE button|
🔝 TOP arrow|
💲 heavy dollar sign|price
™️ trade mark|
©️ copyright|
®️ registered|
♻️ recycling|
💬 speech balloon|chat message
💭 thought balloon|think
🗯️ anger bubble|
🔜 soon arrow|
✳️ eight spoked asterisk|star
〰️ wavy dash|
➰ curly loop|`,
  ],
];

function parse(): EmojiGroup[] {
  return SOURCE.map(([id, label, icon, text]) => ({
    id,
    label,
    icon,
    items: text.split("\n").map((line) => {
      const space = line.indexOf(" ");
      const char = line.slice(0, space);
      const [name = "", words = ""] = line.slice(space + 1).split("|");
      return { char, name: name.trim(), words: words.trim() };
    }),
  }));
}

export const EMOJI_GROUPS: EmojiGroup[] = parse();

/** The emoji whose name or words contain every word typed. */
export function searchEmoji(query: string): EmojiItem[] {
  const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (terms.length === 0) return [];
  const seen = new Set<string>();
  const out: EmojiItem[] = [];
  for (const g of EMOJI_GROUPS)
    for (const it of g.items) {
      const hay = `${it.name} ${it.words}`.toLowerCase();
      if (terms.every((t) => hay.includes(t)) && !seen.has(it.char)) {
        seen.add(it.char);
        out.push(it);
      }
    }
  return out;
}

/** The text with `insert` in place of the selection, and where the caret goes. */
export function insertAt(
  text: string,
  start: number,
  end: number,
  insert: string,
  max = 4000,
): { text: string; caret: number } {
  const a = Math.max(0, Math.min(start, text.length));
  const b = Math.max(a, Math.min(end, text.length));
  const next = text.slice(0, a) + insert + text.slice(b);
  if (next.length > max) return { text, caret: a };
  return { text: next, caret: a + insert.length };
}

/** The recent emoji with `char` first, no repeats, at most `max`. */
export function addRecent(list: string[], char: string, max = 24): string[] {
  return [char, ...list.filter((c) => c !== char)].slice(0, max);
}
