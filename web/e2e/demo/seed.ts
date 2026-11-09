// Seeds the designer's demo world into the Taskiem that serve-demo.sh
// runs (http://localhost:18090), through the public API; psql only for
// what the API cannot do (confirming emails, back-dating the week's sales).
// Writes web/e2e/demo/accounts.json with every account and useful ids.
//
//   node web/e2e/demo/seed.ts        (Node 22.18+ runs TypeScript as is)
//
// Run it once on a fresh database (serve-demo.sh without --keep). On a
// database where ada@demo.test already signs in it does nothing.
import { execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const BASE = process.env.DEMO_URL ?? "http://localhost:18090";
const SUPER = process.env.TASKIEM_TEST_DATABASE_URL ?? "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable";
const DSN = SUPER.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_demo$1");
const PASSWORD = "correct horse battery";
const here = dirname(fileURLToPath(import.meta.url));

type J = Record<string, any>;

function sql(text: string): string {
  return execFileSync("psql", [DSN, "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}
const q = (s: string) => "'" + s.replace(/'/g, "''") + "'";
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const naira = (n: number) => Math.round(n * 100);

/** A signed-in person: their cookies, and calls that fail loudly. */
class Client {
  cookies = new Map<string, string>();
  name: string;
  tenant = "";
  constructor(name: string) {
    this.name = name;
  }
  async raw(method: string, path: string, body?: unknown, headers: Record<string, string> = {}): Promise<{ status: number; json: J; text: string }> {
    const h: Record<string, string> = { "X-Taskiem-Request": "1", ...headers };
    if (this.cookies.size) h.Cookie = [...this.cookies].map(([k, v]) => `${k}=${v}`).join("; ");
    let payload: BodyInit | undefined;
    if (body instanceof Uint8Array) {
      payload = body;
      h["Content-Type"] ??= "image/png";
    } else if (body !== undefined) {
      payload = JSON.stringify(body);
      h["Content-Type"] = "application/json";
    }
    for (let attempt = 0; ; attempt++) {
      const r = await fetch(BASE + path, { method, headers: h, body: payload });
      for (const c of r.headers.getSetCookie()) {
        const [kv] = c.split(";");
        const i = kv.indexOf("=");
        this.cookies.set(kv.slice(0, i), kv.slice(i + 1));
      }
      const text = await r.text();
      if ((r.status === 429 || r.status === 503) && attempt < 5) {
        await sleep(1500);
        continue;
      }
      let json: J = {};
      try {
        json = text ? JSON.parse(text) : {};
      } catch {
        /* not JSON */
      }
      return { status: r.status, json, text };
    }
  }
  async must(method: string, path: string, body?: unknown, headers: Record<string, string> = {}): Promise<J> {
    const r = await this.raw(method, path, body, headers);
    if (r.status >= 300) throw new Error(`${this.name}: ${method} ${path} -> ${r.status} ${r.text.slice(0, 400)}`);
    return r.json;
  }
  /** As must, but a failure is reported and skipped. */
  async try(what: string, method: string, path: string, body?: unknown, headers: Record<string, string> = {}): Promise<J | null> {
    const r = await this.raw(method, path, body, headers);
    if (r.status >= 300) {
      skipped.push(`${what}: ${method} ${path} -> ${r.status} ${r.text.slice(0, 300)}`);
      console.warn("  ! " + skipped[skipped.length - 1]);
      return null;
    }
    return r.json;
  }
}

const skipped: string[] = [];
let keySeq = 0;
const idem = (k: string) => ({ "Idempotency-Key": `demo-${k}-${++keySeq}` });

async function login(name: string, email: string): Promise<Client> {
  const c = new Client(name);
  await c.must("POST", "/v1/auth/login", { email, password: PASSWORD });
  c.tenant = (await c.must("GET", "/v1/me")).tenant_id ?? "";
  return c;
}

async function business(tenant: string, owner: string, email: string): Promise<Client> {
  const c = new Client(tenant);
  const r = await c.raw("POST", "/v1/signup", { tenant, email, name: owner, password: PASSWORD, accept_terms: true });
  if (r.status !== 201) throw new Error(`signup ${tenant}: ${r.status} ${r.text}`);
  sql(`UPDATE users SET email_verified_at = now() WHERE lower(email) = lower(${q(email)})`);
  c.tenant = r.json.tenant_id;
  return c;
}

// ---------------------------------------------------------------- pictures

type Pic = { emoji: string; from: string; to: string; label?: string; pattern?: string };

/** Renders pictures with Chromium: gradients, a pattern, big emoji. */
async function renderPictures(pics: Record<string, Pic & { w: number; h: number }>): Promise<Record<string, Uint8Array>> {
  // @playwright/test re-exports the browsers (web/node_modules has no bare playwright).
  const { chromium } = await import("@playwright/test");
  const browser = await chromium.launch();
  const page = await browser.newPage({ deviceScaleFactor: 1 });
  const out: Record<string, Uint8Array> = {};
  for (const [key, p] of Object.entries(pics)) {
    await page.setViewportSize({ width: p.w, height: p.h });
    const big = Math.round(Math.min(p.w, p.h) * (p.label ? 0.42 : 0.55));
    const emojis = [...p.emoji];
    const scatter = (p.pattern ?? "")
      .split(" ")
      .filter(Boolean)
      .map((e, i) => {
        const x = (i * 37 + 11) % 100;
        const y = (i * 53 + 23) % 100;
        const r = ((i * 47) % 50) - 25;
        return `<span style="position:absolute;left:${x}%;top:${y}%;font-size:${Math.round(big * 0.32)}px;opacity:.28;transform:rotate(${r}deg)">${e}</span>`;
      })
      .join("");
    const html = `<!doctype html><html><body style="margin:0">
      <div style="position:relative;width:${p.w}px;height:${p.h}px;overflow:hidden;
        background:radial-gradient(circle at 25% 20%, rgba(255,255,255,.45), transparent 45%),
                   radial-gradient(circle at 80% 85%, rgba(0,0,0,.25), transparent 50%),
                   linear-gradient(135deg, ${p.from}, ${p.to});
        font-family:'Noto Color Emoji', sans-serif;display:flex;align-items:center;justify-content:center;flex-direction:column">
        ${scatter}
        <div style="position:relative;font-size:${big}px;line-height:1;filter:drop-shadow(0 ${Math.round(big / 14)}px ${Math.round(big / 10)}px rgba(0,0,0,.35))">${emojis.join("")}</div>
        ${p.label ? `<div style="position:relative;margin-top:${Math.round(big * 0.12)}px;font:700 ${Math.round(big * 0.2)}px/1 Georgia, serif;color:#fff;letter-spacing:.02em;text-shadow:0 2px 6px rgba(0,0,0,.35)">${p.label}</div>` : ""}
      </div></body></html>`;
    await page.setContent(html);
    out[key] = await page.screenshot({ type: "jpeg", quality: 88 });
  }
  await browser.close();
  return out;
}

// ---------------------------------------------------------------- the world

type Biz = {
  key: string;
  name: string;
  owner: string;
  email: string;
  tagline: string;
  area: string;
  about: string;
  logo: Pic;
  photos: (Pic & { caption: string })[];
};

const ADA: Biz = {
  key: "ada",
  name: "Ada's Kitchen",
  owner: "Ada Obi",
  email: "ada@demo.test",
  tagline: "Home-cooked Nigerian food",
  area: "Yaba, Lagos",
  about: "Lunch packs and party trays for offices and events around Yaba, cooked fresh every morning.",
  logo: { emoji: "🍲", from: "#0f5132", to: "#2f8f5b", label: "Ada's" },
  photos: [
    { emoji: "🍛", from: "#e85d04", to: "#9d0208", pattern: "🌶️ 🍅 🧅 🌶️ 🍅", caption: "Party jollof, smoky from the firewood pot" },
    { emoji: "🥘", from: "#ffba08", to: "#dc2f02", pattern: "🥬 🌶️ 🥬 🌶️", caption: "Egusi with pounded yam" },
    { emoji: "🍱", from: "#2a9d8f", to: "#264653", pattern: "🍗 🍚 🍗 🍚", caption: "Office lunch packs, ready by 12" },
  ],
};

const BAYO: Biz = {
  key: "bayo",
  name: "Bayo Rice Mills",
  owner: "Bayo Adeyemi",
  email: "bayo@demo.test",
  tagline: "Rice, milled and bagged in Lagos",
  area: "Ikorodu",
  about: "We mill and bag local rice in Ikorodu and deliver across Lagos every week. Ofada, parboiled and basmati, by the bag or the truck.",
  logo: { emoji: "🌾", from: "#b08968", to: "#7f5539", label: "Bayo" },
  photos: [
    { emoji: "🍚", from: "#ede0d4", to: "#b08968", pattern: "🌾 🌾 🌾 🌾 🌾", caption: "New harvest, milled this week" },
    { emoji: "🚚", from: "#588157", to: "#344e41", pattern: "🌾 📦 🌾 📦", caption: "Deliveries leave Ikorodu at 6am" },
    { emoji: "🌾", from: "#dda15e", to: "#bc6c25", pattern: "☀️ 🌾 ☀️", caption: "Our paddies at Itoikin" },
  ],
};

const RIVERS: Biz = {
  key: "rivers",
  name: "Rivers Foods Ltd",
  owner: "Ibinabo George",
  email: "rivers@demo.test",
  tagline: "Staff canteens and catering",
  area: "Victoria Island, Lagos",
  about: "We run staff canteens for banks and offices on the Island, and buy trays from trusted kitchens for events.",
  logo: { emoji: "🌊", from: "#0077b6", to: "#023e8a", label: "Rivers" },
  photos: [{ emoji: "🍽️", from: "#48cae4", to: "#0077b6", pattern: "🥗 🍗 🥗", caption: "The canteen at Adeola Odeku" }],
};

const MAMA: Biz = {
  key: "mama",
  name: "Mama Tunde's Market",
  owner: "Funke Bakare",
  email: "mama@demo.test",
  tagline: "Fresh produce from Mile 12",
  area: "Mile 12, Lagos",
  about: "Tomatoes, peppers, onions and leaves, picked up at Mile 12 at dawn and at your kitchen door by 9.",
  logo: { emoji: "🍅", from: "#d00000", to: "#6a040f", label: "Mama T" },
  photos: [
    { emoji: "🌶️", from: "#e63946", to: "#9d0208", pattern: "🍅 🧅 🌶️ 🍅 🧅", caption: "Tatashe and rodo, sorted" },
    { emoji: "🥬", from: "#70e000", to: "#007200", pattern: "🥬 🥬 🥬", caption: "Ugu and efo, cut this morning" },
  ],
};

const COOLBOX: Biz = {
  key: "coolbox",
  name: "CoolBox Logistics",
  owner: "Emeka Nwosu",
  email: "coolbox@demo.test",
  tagline: "Cold-chain deliveries across Lagos",
  area: "Ikeja, Lagos",
  about: "Chilled vans and riders with cool boxes for kitchens, pharmacies and caterers. Same-day across the mainland.",
  logo: { emoji: "🧊", from: "#4cc9f0", to: "#4361ee", label: "CoolBox" },
  photos: [{ emoji: "🚐", from: "#4895ef", to: "#3a0ca3", pattern: "🧊 📦 🧊", caption: "Our chilled vans" }],
};

const EKO: Biz = {
  key: "eko",
  name: "Eko Events Co.",
  owner: "Tolu Martins",
  email: "eko@demo.test",
  tagline: "Weddings, launches and owambe",
  area: "Lekki, Lagos",
  about: "Event planners for weddings and product launches. We bring the canopies, the DJ and the best caterers in Lagos.",
  logo: { emoji: "🎉", from: "#f72585", to: "#7209b7", label: "Eko" },
  photos: [{ emoji: "💃", from: "#f72585", to: "#b5179e", pattern: "🎊 🎶 🎊", caption: "An owambe in Lekki" }],
};

// Explore: findable, not linked with Ada.
const EXPLORE: Biz[] = [
  {
    key: "kola",
    name: "Kola's Bakery",
    owner: "Kola Adebayo",
    email: "kola@demo.test",
    tagline: "Bread and cakes, baked at dawn",
    area: "Yaba, Lagos",
    about: "A family bakery since 2009. We supply hotels, cafés and shops around Yaba and Surulere every morning.",
    logo: { emoji: "🥖", from: "#bc6c25", to: "#6f1d1b", label: "Kola's" },
    photos: [
      { emoji: "🍞", from: "#dda15e", to: "#bc6c25", pattern: "🌾 🥐 🌾 🥐", caption: "Agege bread, out of the oven at 5am" },
      { emoji: "🎂", from: "#ffafcc", to: "#cdb4db", pattern: "🍓 🕯️ 🍓", caption: "Birthday cakes to order" },
      { emoji: "🥐", from: "#f4a261", to: "#e76f51", pattern: "☕ 🥐 ☕", caption: "Croissants for cafés" },
      { emoji: "🧁", from: "#ffc8dd", to: "#bde0fe", pattern: "🍫 🍓 🍫", caption: "Cupcakes for parties" },
    ],
  },
  {
    key: "zuri",
    name: "Zuri Atelier",
    owner: "Zainab Yusuf",
    email: "zuri@demo.test",
    tagline: "Made-to-measure clothing",
    area: "Surulere, Lagos",
    about: "Ankara, aso-oke and lace, cut to measure. Uniforms for restaurants and hotels, and outfits for every owambe.",
    logo: { emoji: "🧵", from: "#9d4edd", to: "#3c096c", label: "Zuri" },
    photos: [
      { emoji: "👗", from: "#ff7b00", to: "#c9184a", pattern: "🧵 ✂️ 🧵", caption: "Ankara dresses, made to measure" },
      { emoji: "👔", from: "#3a86ff", to: "#023e8a", pattern: "📏 ✂️ 📏", caption: "Staff uniforms for kitchens and hotels" },
      { emoji: "🪡", from: "#e9c46a", to: "#e76f51", pattern: "🧶 🧵 🧶", caption: "The workroom in Surulere" },
    ],
  },
  {
    key: "ifeoma",
    name: "Ifeoma Packaging",
    owner: "Ifeoma Okafor",
    email: "ifeoma@demo.test",
    tagline: "Food packs, cups and bags",
    area: "Lagos Island",
    about: "Takeaway packs, paper bags, cups and branded boxes for restaurants and bakeries. Next-day delivery on the mainland.",
    logo: { emoji: "📦", from: "#2a9d8f", to: "#1d3557", label: "Ifeoma" },
    photos: [
      { emoji: "🥡", from: "#e9edc9", to: "#a3b18a", pattern: "📦 🥤 📦", caption: "Takeaway packs, by the carton" },
      { emoji: "🛍️", from: "#ffb703", to: "#fb8500", pattern: "🛍️ 🛍️", caption: "Paper bags with your logo" },
      { emoji: "🥤", from: "#8ecae6", to: "#219ebc", pattern: "🧊 🥤 🧊", caption: "Cups and lids" },
    ],
  },
  {
    key: "suya",
    name: "Mallam Musa Suya Spot",
    owner: "Musa Ibrahim",
    email: "suya@demo.test",
    tagline: "Suya, kilishi and yaji spice",
    area: "Yaba, Lagos",
    about: "Beef and chicken suya grilled every evening on Herbert Macaulay Way. Yaji spice by the kilo for kitchens.",
    logo: { emoji: "🍢", from: "#9a031e", to: "#5f0f40", label: "Musa" },
    photos: [
      { emoji: "🔥", from: "#f48c06", to: "#9d0208", pattern: "🍢 🌶️ 🍢", caption: "The grill, from 5pm" },
      { emoji: "🌶️", from: "#dc2f02", to: "#6a040f", pattern: "🥜 🌶️ 🥜", caption: "Yaji spice, ground fresh" },
    ],
  },
  {
    key: "chops",
    name: "Small Chops by Bisi",
    owner: "Bisi Coker",
    email: "bisi@demo.test",
    tagline: "Puff-puff, samosa and spring rolls",
    area: "Yaba, Lagos",
    about: "Small chops trays for parties and office meetings. Puff-puff, samosa, spring rolls, gizdodo and peppered snail.",
    logo: { emoji: "🥟", from: "#ffb703", to: "#fb5607", label: "Bisi" },
    photos: [
      { emoji: "🍩", from: "#ffd166", to: "#f77f00", pattern: "🥟 🍢 🥟", caption: "A tray for 50" },
      { emoji: "🥟", from: "#f4a261", to: "#bc6c25", pattern: "🌶️ 🥟 🌶️", caption: "Samosa and spring rolls" },
    ],
  },
  {
    key: "fresh",
    name: "FreshPress Juices",
    owner: "Temi Ogun",
    email: "fresh@demo.test",
    tagline: "Cold-pressed juice and smoothies",
    area: "Yaba, Lagos",
    about: "Cold-pressed juice from Nigerian fruit: pineapple, watermelon, tiger nut and zobo. Bottled daily for gyms and cafés.",
    logo: { emoji: "🍍", from: "#80ed99", to: "#38a3a5", label: "Fresh" },
    photos: [
      { emoji: "🥤", from: "#ff595e", to: "#ffca3a", pattern: "🍉 🍍 🍉", caption: "Watermelon and ginger" },
      { emoji: "🍊", from: "#ff9f1c", to: "#ff4040", pattern: "🍊 🍋 🍊", caption: "Orange and carrot" },
    ],
  },
  {
    key: "gas",
    name: "Yaba Gas Refill",
    owner: "Chidi Okeke",
    email: "gas@demo.test",
    tagline: "Cooking gas, delivered in an hour",
    area: "Yaba, Lagos",
    about: "12.5kg and 50kg cylinders refilled and delivered around Yaba, Akoka and Ebute Metta. Open from 7am.",
    logo: { emoji: "🔥", from: "#457b9d", to: "#1d3557", label: "Gas" },
    photos: [{ emoji: "🛵", from: "#a8dadc", to: "#457b9d", pattern: "🔥 🛵 🔥", caption: "Our riders bring the cylinder to you" }],
  },
  {
    key: "print",
    name: "Akoka Print Hub",
    owner: "Segun Alabi",
    email: "print@demo.test",
    tagline: "Menus, flyers and banners",
    area: "Yaba, Lagos",
    about: "Menus, flyers, stickers and roll-up banners, printed while you wait near UNILAG gate.",
    logo: { emoji: "🖨️", from: "#4361ee", to: "#3a0ca3", label: "Print" },
    photos: [{ emoji: "📜", from: "#f72585", to: "#4361ee", pattern: "🖨️ 📄 🖨️", caption: "Menus, laminated" }],
  },
  {
    key: "fish",
    name: "Epe Fresh Fish",
    owner: "Kunle Balogun",
    email: "fish@demo.test",
    tagline: "Catfish, croaker and prawns",
    area: "Epe, Lagos",
    about: "Live catfish and fresh croaker from the Epe lagoon, cleaned and cut for restaurants.",
    logo: { emoji: "🐟", from: "#00b4d8", to: "#03045e", label: "Epe" },
    photos: [
      { emoji: "🦐", from: "#90e0ef", to: "#0077b6", pattern: "🐟 🦐 🐟", caption: "Prawns from Epe" },
      { emoji: "🐟", from: "#48cae4", to: "#023e8a", pattern: "🌊 🐟 🌊", caption: "Catfish, live or smoked" },
    ],
  },
  {
    key: "palm",
    name: "Ogun Palm Oil Co.",
    owner: "Dayo Sowande",
    email: "palm@demo.test",
    tagline: "Red palm oil, pressed in Ogun",
    area: "Abeokuta, Ogun",
    about: "Pure red palm oil in 5L, 25L and drums, pressed at our mill in Abeokuta and delivered to Lagos weekly.",
    logo: { emoji: "🌴", from: "#e85d04", to: "#6a040f", label: "Ogun" },
    photos: [{ emoji: "🛢️", from: "#f48c06", to: "#9d0208", pattern: "🌴 🌴 🌴", caption: "25L kegs, sealed" }],
  },
  {
    key: "spice",
    name: "Spice Route Abuja",
    owner: "Hauwa Bello",
    email: "spice@demo.test",
    tagline: "Spices and seasonings, wholesale",
    area: "Wuse, Abuja",
    about: "Crayfish, ogiri, iru, curry and thyme by the kilo for kitchens across Nigeria.",
    logo: { emoji: "🧂", from: "#ca6702", to: "#9b2226", label: "Spice" },
    photos: [{ emoji: "🌶️", from: "#bb3e03", to: "#9b2226", pattern: "🧄 🧂 🧄", caption: "Our spice room" }],
  },
  {
    key: "cater",
    name: "Grace Catering Supplies",
    owner: "Grace Udo",
    email: "grace@demo.test",
    tagline: "Chafing dishes, coolers and trays",
    area: "Surulere, Lagos",
    about: "Chafing dishes, food warmers, coolers and trays to buy or hire for events.",
    logo: { emoji: "🍽️", from: "#6c757d", to: "#212529", label: "Grace" },
    photos: [{ emoji: "🥘", from: "#adb5bd", to: "#495057", pattern: "🍽️ 🥄 🍽️", caption: "Chafing dishes to hire" }],
  },
];

// Ada's products: [name, price ₦, stock at the end, unit, reorder level].
const PRODUCTS: [string, number, number, string, number][] = [
  ["Jollof rice", 4000, 34, "plate", 10],
  ["Fried rice and turkey", 5200, 12, "plate", 5],
  ["Pepper soup", 3500, 3, "bowl", 5],
  ["Egusi and pounded yam", 4800, 20, "plate", 5],
  ["Zobo", 800, 60, "bottle", 12],
  ["Chapman", 1200, 2, "glass", 6],
  ["Ofada rice and ayamase", 4500, 18, "plate", 5],
  ["Amala and ewedu", 3800, 15, "plate", 5],
  ["Efo riro and semo", 4200, 16, "plate", 5],
  ["Afang soup and fufu", 5000, 9, "plate", 4],
  ["Banga soup and starch", 5500, 7, "plate", 4],
  ["Ofe nsala", 5200, 8, "plate", 4],
  ["Moi moi", 1000, 40, "wrap", 10],
  ["Fried plantain (dodo)", 1000, 45, "portion", 10],
  ["Peppered chicken", 2500, 25, "piece", 8],
  ["Grilled croaker fish", 6500, 6, "fish", 3],
  ["Asun", 3500, 14, "portion", 5],
  ["Nkwobi", 4500, 10, "bowl", 4],
  ["Beans and plantain", 3000, 22, "plate", 5],
  ["Yam porridge (asaro)", 3200, 13, "plate", 5],
  ["Puff-puff (6 pieces)", 1000, 30, "pack", 10],
  ["Meat pie", 1200, 24, "piece", 8],
  ["Small chops pack", 2500, 20, "pack", 6],
  ["Fruit salad", 1800, 11, "cup", 5],
  ["Bottled water", 300, 96, "bottle", 24],
  ["Coke (50cl)", 500, 48, "bottle", 12],
];
// Hidden from customers (available off).
const HIDDEN = ["Ofe nsala", "Grilled croaker fish"];
// What Ada's Kitchen buys in: [name, stock, unit, reorder level, target, supplier].
const SUPPLIES: [string, number, string, number, number, string][] = [
  ["Rice, 25kg bag", 4, "bag", 10, 24, "Bayo Rice Mills"],
  ["Rice, 50kg bag", 6, "bag", 3, 10, "Bayo Rice Mills"],
  ["Palm oil 25L", 2, "keg", 2, 6, "Ogun Palm Oil"],
  ["Tomatoes basket", 3, "basket", 4, 10, "Mama Tunde's Market"],
  ["Tatashe pepper bag", 5, "bag", 3, 8, "Mama Tunde's Market"],
  ["Onions bag", 4, "bag", 2, 6, "Mama Tunde's Market"],
  ["Vegetable oil 25L", 3, "keg", 2, 5, ""],
  ["Takeaway packs (carton)", 7, "carton", 5, 15, ""],
];
// Ada's services: [name, price ₦, minutes].
const SERVICES: [string, number, number][] = [
  ["Party tray for 50", 85000, 180],
  ["Office lunch delivery", 2500, 30],
  ["Small chops tray for 30", 45000, 120],
  ["Outdoor catering (per guest)", 6500, 240],
  ["Cooking class: jollof masterclass", 25000, 90],
];
// Bayo's price list for Ada: [name, price ₦, min, stock].
const BAYO_ITEMS: [string, number, number, number][] = [
  ["Rice, 50kg bag", 78000, 2, 140],
  ["Rice, 25kg bag", 41000, 5, 260],
  ["Ofada rice, 25kg", 52000, 5, 80],
  ["Basmati rice, 5kg", 14500, 10, 120],
  ["Brown rice, 10kg", 19000, 5, 60],
  ["Parboiled rice, 10kg", 16500, 5, 150],
  ["Ofada rice, 5kg", 11500, 10, 90],
  ["Broken rice, 50kg", 52000, 2, 40],
  ["Rice bran, 25kg", 6500, 4, 70],
  ["Jasmine rice, 5kg", 15800, 10, 50],
];

async function main() {
  {
    const probe = new Client("probe");
    const r = await probe.raw("POST", "/v1/auth/login", { email: ADA.email, password: PASSWORD });
    if (r.status === 200) {
      console.log("ada@demo.test already signs in: the world is seeded (start serve-demo.sh without --keep to seed afresh).");
      return;
    }
  }
  const all = [ADA, BAYO, RIVERS, MAMA, COOLBOX, EKO, ...EXPLORE];
  console.log("pictures…");
  const want: Record<string, Pic & { w: number; h: number }> = {};
  for (const b of all) {
    want[b.key + ":logo"] = { ...b.logo, w: 400, h: 400 };
    b.photos.forEach((p, i) => (want[`${b.key}:${i}`] = { ...p, w: 900, h: 600 }));
  }
  const pics = await renderPictures(want);
  const dir = mkdtempSync(join(tmpdir(), "taskiem-demo-"));
  for (const [k, v] of Object.entries(pics)) writeFileSync(join(dir, k.replace(":", "-") + ".jpg"), v);

  console.log("businesses…");
  const c: Record<string, Client> = {};
  for (const b of all) {
    c[b.key] = await business(b.name, b.owner, b.email);
    await c[b.key].must("PUT", "/v1/business-profile", { tagline: b.tagline, area: b.area, about: b.about });
    await c[b.key].must("PUT", "/v1/link-settings", { findable: true });
    await c[b.key].try(`${b.name} logo`, "POST", "/v1/business-profile/logo", pics[b.key + ":logo"], { "Content-Type": "image/jpeg" });
    for (const [i, p] of b.photos.entries()) {
      await c[b.key].try(`${b.name} photo`, "POST", "/v1/business-profile/gallery?caption=" + encodeURIComponent(p.caption), pics[`${b.key}:${i}`], {
        "Content-Type": "image/jpeg",
      });
    }
  }
  const ada = c.ada;
  await ada.must("PUT", "/v1/settings/chat-tools", { chat_tools: true });
  for (const k of ["bayo", "rivers", "mama", "coolbox", "eko"]) await c[k].must("PUT", "/v1/settings/chat-tools", { chat_tools: true });

  console.log("staff…");
  await ada.must("POST", "/v1/members", { email: "tunde@demo.test", name: "Tunde", password: PASSWORD, roles: ["cashier"] });
  await ada.must("POST", "/v1/members", { email: "sam@demo.test", name: "Sam", password: PASSWORD, roles: ["shift_manager"] });

  // ---- links: Ada and the businesses she trades with.
  console.log("links…");
  const linkIds: Record<string, { ada: string; theirs: string }> = {};
  async function link(other: string, roleForAda: "supplier" | "customer" | "both") {
    const before = new Set(((await c[other].must("GET", "/v1/links")).links ?? []).map((l: J) => l.id));
    await ada.must("POST", "/v1/links", { business: c[other].tenant, role: roleForAda });
    const theirs = ((await c[other].must("GET", "/v1/links")).links as J[]).find((l) => !before.has(l.id));
    if (!theirs) throw new Error("no link request reached " + other);
    await c[other].must("POST", `/v1/links/${theirs.id}/accept`);
    const mine = ((await ada.must("GET", "/v1/links")).links as J[]).find((l) => JSON.stringify(l).includes(c[other].tenant));
    linkIds[other] = { ada: mine?.id ?? "", theirs: theirs.id };
  }
  await link("bayo", "supplier");
  await link("mama", "supplier");
  await link("coolbox", "supplier");
  await link("rivers", "customer");
  await link("eko", "customer");
  // Explore: Kola's Bakery asked Ada; Ada asked Ifeoma Packaging.
  await c.kola.must("POST", "/v1/links", { business: ada.tenant, role: "customer" });
  await ada.must("POST", "/v1/links", { business: c.ifeoma.tenant, role: "supplier" });

  // ---- stock: Bayo's rice, Ada's menu and supplies.
  console.log("stock…");
  const bayo = c.bayo;
  await bayo.must("POST", "/v1/stock/setup");
  const bayoIds: Record<string, string> = {};
  for (const [name, price, , stock] of BAYO_ITEMS) {
    const out = await bayo.must("POST", "/v1/stock/items", { name, quantity: stock, unit: "bag", price_kobo: naira(price) }, idem("b"));
    bayoIds[name] = out.item.id;
  }
  const bayoList = (await bayo.must("POST", "/v1/price-lists", { name: "Kitchens and restaurants" })).list.id;
  await bayo.must("PUT", `/v1/price-lists/${bayoList}/items`, {
    items: BAYO_ITEMS.map(([name, price, min]) => ({ item_id: bayoIds[name], price_kobo: naira(price), min_quantity: min })),
  });
  await bayo.must("PUT", `/v1/price-lists/${bayoList}/links`, { links: [linkIds.bayo.theirs] });

  // ---- Zuri Atelier: uniforms and clothing sold in sizes and colours
  // (docs/spec/chat-categories-and-variants.md, phase 5). Kitchen clogs in
  // seven sizes, black and white, on a trade list Ada's Kitchen orders
  // from (36 pairs in 7 sizes; the 50-pair price); a chef jacket and an
  // Ankara kaftan; three aprons still sold as items of their own, for
  // "Make one product with sizes and colours".
  console.log("sizes and colours…");
  const zuri = c.zuri;
  await zuri.must("PUT", "/v1/settings/chat-tools", { chat_tools: true });
  await link("zuri", "supplier");
  await zuri.must("POST", "/v1/stock/setup");
  const sized = async (
    name: string,
    unit: string,
    price: number,
    category: string,
    choices: { name: string; values: string[] }[],
    count: (v: Record<string, string>) => number,
  ) => {
    const id = (await zuri.must("POST", "/v1/stock/items", { name, quantity: 0, unit, price_kobo: naira(price), category, reorder_level: 2 }, idem("z")))
      .item.id as string;
    const cells: { variant: Record<string, string>; count: number }[] = [];
    const walk = (i: number, v: Record<string, string>) => {
      const ch = choices[i];
      if (!ch) {
        cells.push({ variant: { ...v }, count: count(v) });
        return;
      }
      for (const x of ch.values) walk(i + 1, { ...v, [ch.name]: x });
    };
    walk(0, {});
    await zuri.must("PUT", `/v1/stock/items/${id}/variants`, { choices, cells }, idem("zv"));
    return id;
  };
  const clogs = await sized(
    "Kitchen Clogs",
    "pair",
    12_000,
    "Footwear",
    [
      { name: "Size", values: ["38", "39", "40", "41", "42", "43", "44"] },
      { name: "Colour", values: ["Black", "White"] },
    ],
    (v) => (v.Colour === "Black" ? (v.Size === "42" ? 2 : 10) : 6),
  );
  const jacket = await sized(
    "Chef Jacket",
    "piece",
    18_000,
    "Uniforms",
    [
      { name: "Size", values: ["S", "M", "L", "XL"] },
      { name: "Colour", values: ["White", "Black"] },
    ],
    (v) => (v.Colour === "White" ? 8 : 4),
  );
  await sized(
    "Ankara Kaftan",
    "piece",
    25_000,
    "Kaftans",
    [
      { name: "Size", values: ["S", "M", "L"] },
      { name: "Colour", values: ["Blue", "Red"] },
    ],
    () => 3,
  );
  for (const colour of ["Red", "Blue", "Black"])
    await zuri.must("POST", "/v1/stock/items", { name: `Kitchen Apron ${colour}`, quantity: 12, unit: "piece", price_kobo: naira(4_500), category: "Uniforms" }, idem("za"));
  const zuriList = (await zuri.must("POST", "/v1/price-lists", { name: "Restaurants and hotels" })).list.id;
  await zuri.must("PUT", `/v1/price-lists/${zuriList}/items`, {
    items: [
      { item_id: clogs, price_kobo: naira(11_000), min_quantity: 12, breaks: [{ min_quantity: 50, price_kobo: naira(10_000) }] },
      { item_id: jacket, price_kobo: naira(16_500), min_quantity: 4 },
    ],
  });
  await zuri.must("PUT", `/v1/price-lists/${zuriList}/links`, { links: [linkIds.zuri.theirs] });

  // ---- services (before stock: the storefront PUT names no products catalogue).
  console.log("services…");
  const slug = "adas-kitchen-" + ada.tenant.replace(/-/g, "").slice(-6);
  await ada.try("storefront", "PUT", "/v1/storefront", { slug, draft: { name: "Ada's Kitchen" } });
  const svc = await ada.try("services catalogue", "POST", "/v1/storefront/catalogue/make", { kind: "services" });
  if (svc?.table_id) {
    for (const [name, price, minutes] of SERVICES)
      await ada.try("service " + name, "POST", `/v1/tables/${svc.table_id}/rows`, { row: { name, price, duration_minutes: minutes } });
    await ada.try("services refresh", "POST", "/v1/storefront/catalogue/refresh");
  }

  await ada.must("POST", "/v1/stock/setup");
  const adaIds: Record<string, string> = {};
  // The week's sales, planned first: stock at the end is what PRODUCTS say.
  const plan = salesPlan();
  const sold: Record<string, number> = {};
  for (const day of plan) for (const s of day.sales) for (const [n, qty] of s.lines) sold[n] = (sold[n] ?? 0) + qty;
  for (const [name, price, end, unit, reorder] of PRODUCTS) {
    const out = await ada.must(
      "POST",
      "/v1/stock/items",
      { name, quantity: end + (sold[name] ?? 0), unit, price_kobo: naira(price), cost_kobo: naira(Math.round(price * 0.45)), reorder_level: reorder, target_level: reorder * 4 },
      idem("a"),
    );
    adaIds[name] = out.item.id;
  }
  for (const name of HIDDEN) await ada.try("hide " + name, "PATCH", `/v1/stock/items/${adaIds[name]}`, { available: false }, idem("h"));
  for (const [name, qty, unit, reorder, target, supplier] of SUPPLIES) {
    const out = await ada.must("POST", "/v1/stock/items", { name, quantity: qty, unit, reorder_level: reorder, target_level: target, supplier }, idem("s"));
    adaIds[name] = out.item.id;
    await ada.try("supply off the menu " + name, "PATCH", `/v1/stock/items/${adaIds[name]}`, { available: false }, idem("h"));
  }

  // ---- sales over the last week, back-dated.
  console.log("sales…");
  const saleKeys: { key: string; at: string }[] = [];
  for (const day of plan) {
    for (const [i, s] of day.sales.entries()) {
      const k = `demo-sale-${day.offset}-${i}`;
      const lines = s.lines.map(([n, qty]) => ({ item_id: adaIds[n], quantity: qty }));
      const by = s.by === "tunde" ? "tunde" : "ada";
      const who = by === "tunde" ? (c.tunde ??= await login("Tunde", "tunde@demo.test")) : ada;
      await who.must("POST", "/v1/stock/sales", { lines, paid_how: s.how }, { "Idempotency-Key": k });
      saleKeys.push({ key: k, at: s.at });
    }
  }
  backdateSales(ada.tenant, saleKeys);

  // ---- Ada's supplier records, linked, and her order from Bayo.
  console.log("orders…");
  const sup = async (name: string, phone: string, other: string) => {
    const id = (await ada.must("POST", "/v1/stock/suppliers", { name, phone })).supplier.id;
    await ada.must("PUT", `/v1/stock/suppliers/${id}/link`, { link_id: linkIds[other].ada });
    return id;
  };
  await sup("Bayo Rice Mills", "0803 555 0101", "bayo");
  await sup("Mama Tunde's Market", "0805 555 0102", "mama");
  await sup("Zuri Atelier", "0809 555 0108", "zuri");
  const order = (
    await ada.must("POST", `/v1/supplier-prices/${linkIds.bayo.ada}/order`, {
      lines: [{ seller_item_id: bayoIds["Rice, 25kg bag"], quantity: 20, item_id: adaIds["Rice, 25kg bag"] }],
      note: "For the Saturday wedding, please deliver by Friday",
    }, idem("o"))
  ).purchase;
  await ada.must("POST", `/v1/stock/purchases/${order.id}/send`);
  const bayoOrderRef = order.reference;
  await waitFor(async () => {
    const got = ((await bayo.must("GET", "/v1/trade-orders")).orders as J[]).find((o) => o.reference === bayoOrderRef);
    return got && got.status === "received";
  }, "Bayo receives Ada's order");
  await bayo.must("POST", `/v1/trade-orders/${bayoOrderRef}/accept`);

  // Rivers buys from Ada: Ada's trade prices, shared; Rivers' order waits for Ada.
  const rivers = c.rivers;
  const adaList = (await ada.must("POST", "/v1/price-lists", { name: "Trade prices" })).list.id;
  const trade: [string, number, number][] = [
    ["Jollof rice", 3600, 20],
    ["Fried rice and turkey", 4700, 20],
    ["Egusi and pounded yam", 4300, 10],
    ["Moi moi", 900, 20],
    ["Peppered chicken", 2200, 20],
    ["Zobo", 700, 24],
    ["Small chops pack", 2200, 20],
  ];
  await ada.must("PUT", `/v1/price-lists/${adaList}/items`, { items: trade.map(([n, p, m]) => ({ item_id: adaIds[n], price_kobo: naira(p), min_quantity: m })) });
  await ada.must("PUT", `/v1/price-lists/${adaList}/links`, { links: [linkIds.rivers.ada, linkIds.eko.ada] });
  await rivers.must("POST", "/v1/stock/setup");
  const rSup = (await rivers.must("POST", "/v1/stock/suppliers", { name: "Ada's Kitchen", phone: "0809 555 0110" })).supplier.id;
  await rivers.must("PUT", `/v1/stock/suppliers/${rSup}/link`, { link_id: linkIds.rivers.theirs });
  const rItem = async (name: string, unit: string) =>
    (await rivers.must("POST", "/v1/stock/items", { name, quantity: 0, unit }, idem("ri"))).item.id as string;
  const rJollof = await rItem("Jollof rice packs", "pack");
  const rChicken = await rItem("Chicken, peppered", "piece");
  const rZobo = await rItem("Zobo drink", "bottle");
  const rOrder = (
    await rivers.must("POST", `/v1/supplier-prices/${linkIds.rivers.theirs}/order`, {
      lines: [
        { seller_item_id: adaIds["Jollof rice"], quantity: 40, item_id: rJollof },
        { seller_item_id: adaIds["Peppered chicken"], quantity: 40, item_id: rChicken },
        { seller_item_id: adaIds["Zobo"], quantity: 40, item_id: rZobo },
      ],
      note: "Staff lunch for Thursday, 40 people. Delivery to Adeola Odeku by 12:30.",
    }, idem("r"))
  ).purchase;
  await rivers.must("POST", `/v1/stock/purchases/${rOrder.id}/send`);
  const riversOrderRef = rOrder.reference;
  await waitFor(async () => ((await ada.must("GET", "/v1/trade-orders")).orders as J[]).some((o) => o.reference === riversOrderRef), "Ada receives Rivers' order");

  // ---- conversations.
  console.log("conversations…");
  const say = async (who: string, other: string, text: string) => {
    const l = who === "ada" ? linkIds[other].ada : linkIds[other].theirs;
    await c[who].must("POST", `/v1/link-chats/${l}/messages`, { text });
    await sleep(120);
  };
  await say("bayo", "bayo", "Morning Ada! New harvest just landed, prices are up a little.");
  await say("ada", "bayo", "Morning Bayo. How much is the 25kg now?");
  await say("bayo", "bayo", "₦41,000 a bag, minimum 5. The 50kg is ₦78,000. I've updated the list you can see.");
  await say("ada", "bayo", "OK. I've sent an order for 20 bags of the 25kg. Can you deliver by Friday?");
  await say("bayo", "bayo", "Got it, accepted. Our van will be in Yaba Friday morning.");
  await say("ada", "bayo", "Perfect, thank you 🙏 I'll pay the last invoice this week too.");
  await say("mama", "mama", "Good morning ma, tomatoes are cheaper today. ₦28,000 a basket.");
  await say("ada", "mama", "Bring 4 baskets and 2 bags of tatashe please.");
  await say("mama", "mama", "No problem. My boy will be there before 9.");
  await say("coolbox", "coolbox", "Hi Ada, we can take the Rivers delivery on Thursday at 11:45. Cool box included.");
  await say("ada", "coolbox", "Great, please confirm the rider's number on the day.");
  await say("rivers", "rivers", "Hi Ada, we've sent Thursday's order: 40 packs for the staff lunch.");
  await say("rivers", "rivers", "Can the jollof be less spicy this time? Some of the team complained 😅");
  await say("ada", "rivers", "Noted! I'll keep the pepper on the side.");
  await say("eko", "eko", "Ada, we have a wedding on the 21st, 300 guests. Can you do jollof and small chops?");
  await say("ada", "eko", "Yes we can. I'll send you a quote for 6 party trays and 10 small chops trays.");

  // ---- invoices Ada is owed, and the bill she owes Bayo.
  console.log("invoices…");
  const invoices: J[] = [];
  const invoice = async (from: Client, body: J, label: string) => {
    const inv = await from.try("invoice " + label, "POST", "/v1/invoices", body, idem("i"));
    if (!inv) return null;
    await from.try("send invoice " + label, "POST", `/v1/invoices/${inv.id}/send`, undefined, idem("is"));
    invoices.push({ label, id: inv.id, number: inv.number });
    return inv;
  };
  await invoice(ada, {
    customer: { name: "Rivers Foods Ltd", key: "link:" + rivers.tenant },
    lines: [
      { description: "Jollof rice (staff lunch, 20 Oct)", quantity: 20, unit_kobo: naira(3600) },
      { description: "Fried rice and turkey (staff lunch, 20 Oct)", quantity: 5, unit_kobo: naira(4800) },
    ],
    due_on: futureDate(10),
  }, "Rivers Foods");
  await invoice(ada, {
    customer: { name: "Lagos Business Hub canteen", phone: "+2348035550123" },
    lines: [
      { description: "Office lunch packs, week of 13 Oct", quantity: 15, unit_kobo: naira(4000) },
      { description: "Zobo", quantity: 10, unit_kobo: naira(800) },
      { description: "Delivery", quantity: 1, unit_kobo: naira(500) },
    ],
    due_on: futureDate(-9),
  }, "Lagos Business Hub (overdue)");
  await invoice(ada, {
    customer: { name: "Mrs Funmi Bello", phone: "+2348035550456" },
    lines: [
      { description: "Small chops tray for 30 (birthday)", quantity: 1, unit_kobo: naira(45000) },
      { description: "Chapman", quantity: 2, unit_kobo: naira(1250) },
    ],
    due_on: futureDate(5),
  }, "Mrs Bello");
  await invoice(bayo, {
    customer: { name: "Ada's Kitchen", key: "link:" + ada.tenant },
    lines: [
      { description: "Rice, 50kg bag", quantity: 5, unit_kobo: naira(78000) },
      { description: "Ofada rice, 25kg", quantity: 1, unit_kobo: naira(52000) },
      { description: "Delivery to Yaba", quantity: 1, unit_kobo: naira(8000) },
    ],
    due_on: futureDate(3),
  }, "Bayo to Ada (bill)");

  // ---- a payment in the bank that matches nothing yet (a statement upload).
  await ada.try("bank statement", "POST", "/v1/money/statements", {
    channel: "gtbank/current",
    csv: `date,description,amount,reference,balance\n${futureDate(-1)},TRF FROM OKONKWO C/ LUNCH,37500,FT${futureDate(-1).replace(/-/g, "").slice(2)}0042,1250000\n`,
  });

  // ---- riders.
  console.log("riders…");
  const riders: J[] = [];
  for (const r of [
    { name: "Kunle", phone: "0802 555 0201", zones: ["Yaba", "Surulere", "Ebute Metta"], fee_kobo: naira(1500) },
    { name: "Ibrahim", phone: "0812 555 0202", zones: ["Victoria Island", "Ikoyi", "Lekki"], fee_kobo: naira(2500) },
  ]) {
    const out = await ada.try("rider " + r.name, "POST", "/v1/riders", r);
    if (out) riders.push(out.rider ?? out);
  }

  // ---- something that needs Ada's OK: Tunde asks to pay Bayo.
  console.log("approval…");
  const flow = `{"schema":"wd/v1","id":"wf_payBayo","version":1,"name":"Pay a supplier","trigger":{"type":"manual"},
    "steps":[{"id":"ok","type":"approval","name":"Pay Bayo Rice Mills ₦450,000","config":{"role":"owner","subject":{
      "amount_kobo":"=trigger.body.amount","account_name":"Bayo Rice Mills","account_number":"=trigger.body.account","bank_code":"058"}}}]}`;
  const wf = await ada.try("approval workflow", "POST", "/v1/workflows", { name: "Pay a supplier", definition: JSON.parse(flow) });
  if (wf?.id) {
    await ada.try("publish workflow", "POST", `/v1/workflows/${wf.id}/versions/1/publish`);
    c.tunde ??= await login("Tunde", "tunde@demo.test");
    const run = await c.tunde.try("Tunde asks to pay Bayo (as cashier)", "POST", `/v1/workflows/${wf.id}/runs`, {
      input: { amount: naira(450000), account: "0123456789" },
    });
    if (!run) {
      // A cashier may not start workflows: Tunde is also made an operator.
      skipped.push("a cashier cannot start a run: Tunde was given the operator role as well");
      await ada.try("Tunde operator", "POST", "/v1/members", { email: "tunde@demo.test", roles: ["operator"] });
      c.tunde = await login("Tunde", "tunde@demo.test");
      await c.tunde.try("Tunde asks to pay Bayo", "POST", `/v1/workflows/${wf.id}/runs`, { input: { amount: naira(450000), account: "0123456789" } });
    }
  }

  // ---- the Taskiem conversation: how did we do?
  console.log("taskiem chat…");
  await ada.try("how did we do", "POST", "/v1/taskiem-chat", { text: "how did we do this week?" });

  const bills = (await ada.try("bills", "GET", "/v1/bills"))?.bills ?? [];
  const out = {
    url: BASE,
    password: PASSWORD,
    accounts: Object.fromEntries(
      [...all.map((b) => [b.key, { business: b.name, owner: b.owner, email: b.email, tenant_id: c[b.key].tenant }]),
        ["tunde", { business: ADA.name, owner: "Tunde (cashier)", email: "tunde@demo.test" }],
        ["sam", { business: ADA.name, owner: "Sam (shift manager)", email: "sam@demo.test" }]],
    ),
    links: Object.fromEntries(Object.entries(linkIds).map(([k, v]) => [k, { ada_link_id: v.ada, their_link_id: v.theirs }])),
    orders: { ada_to_bayo: bayoOrderRef, rivers_to_ada: riversOrderRef },
    price_lists: { bayo_for_kitchens: bayoList, ada_trade_prices: adaList },
    invoices,
    bills: bills.map((b: J) => ({ id: b.id, reference: b.reference, supplier: b.supplier_name, amount_kobo: b.amount_kobo })),
    riders,
    skipped,
  };
  writeFileSync(join(here, "accounts.json"), JSON.stringify(out, null, 2) + "\n");
  console.log(`done: ${join(here, "accounts.json")}`);
  if (skipped.length) console.log("skipped:\n  " + skipped.join("\n  "));
}

function futureDate(days: number): string {
  const d = new Date(Date.now() + days * 86400000);
  return d.toISOString().slice(0, 10);
}

async function waitFor(check: () => Promise<boolean>, what: string) {
  for (let i = 0; i < 60; i++) {
    if (await check()) return;
    await sleep(1000);
  }
  throw new Error("timed out: " + what);
}

type Sale = { lines: [string, number][]; how: "cash" | "transfer" | "pos"; at: string; by: "ada" | "tunde" };

/** The last two weeks' sales (yesterday ₦486,500), deterministic. */
function salesPlan(): { offset: number; sales: Sale[] }[] {
  // Days back from today → the day's total in naira.
  const totals: [number, number][] = [
    [14, 352000],
    [13, 287500],
    [12, 401000],
    [11, 334500],
    [10, 456000],
    [9, 389000],
    [8, 268000],
    [7, 312500],
    [6, 405000],
    [5, 298000],
    [4, 520500],
    [3, 441000],
    [2, 389500],
    [1, 486500],
    [0, 118500],
  ];
  const price = Object.fromEntries(PRODUCTS.map(([n, p]) => [n, p]));
  const menu = PRODUCTS.filter(([n]) => !HIDDEN.includes(n) && !["Pepper soup", "Chapman"].includes(n)).map(([n]) => n);
  let seed = 7;
  const rnd = () => ((seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648);
  const hows: Sale["how"][] = ["cash", "transfer", "pos", "transfer", "cash"];
  const out: { offset: number; sales: Sale[] }[] = [];
  for (const [offset, total] of totals) {
    const sales: Sale[] = [];
    let left = total;
    let hour = 9;
    while (left > 30000) {
      const lines: [string, number][] = [];
      const n = 1 + Math.floor(rnd() * 3);
      for (let i = 0; i < n; i++) {
        const name = menu[Math.floor(rnd() * menu.length)];
        if (lines.some(([x]) => x === name)) continue;
        const qty = Math.max(1, Math.min(Math.floor((left * 0.3) / price[name]), 1 + Math.floor(rnd() * 6)));
        if (qty * price[name] > left - 5000) continue;
        lines.push([name, qty]);
        left -= qty * price[name];
      }
      if (!lines.length) break;
      sales.push({ lines, how: hows[Math.floor(rnd() * hows.length)], at: `${offset}|${hour}`, by: rnd() < 0.5 ? "tunde" : "ada" });
      hour = Math.min(20, hour + (rnd() < 0.6 ? 1 : 0));
    }
    // The rest exactly: Pepper soup (₦3,500), Chapman (₦1,200), Zobo (₦800), water (₦300) ...
    const fill = exact(left, [
      ["Pepper soup", 3500],
      ["Chapman", 1200],
      ["Zobo", 800],
      ["Coke (50cl)", 500],
      ["Bottled water", 300],
      ["Jollof rice", 4000],
    ]);
    if (fill.length) sales.push({ lines: fill, how: "cash", at: `${offset}|${Math.min(hour + 1, 21)}`, by: "ada" });
    out.push({ offset, sales });
  }
  return out;
}

/** Lines that add up to exactly left naira (best effort). */
function exact(left: number, items: [string, number][]): [string, number][] {
  const [a, b, c2, d, e, f] = items;
  for (let i = 0; i <= 6; i++)
    for (let j = 0; j <= 6; j++)
      for (let k = 0; k <= 10; k++)
        for (let m = 0; m <= 10; m++)
          for (let n = 0; n <= 20; n++) {
            const rest = left - i * a[1] - j * b[1] - k * c2[1] - m * d[1] - n * e[1];
            if (rest < 0 || rest % f[1] !== 0) continue;
            const lines: [string, number][] = [];
            const add = (x: [string, number], q: number) => q > 0 && lines.push([x[0], q]);
            add(a, i), add(b, j), add(c2, k), add(d, m), add(e, n), add(f, rest / f[1]);
            return lines;
          }
  return [];
}

/** Moves each sale to its day and hour in Lagos (sales are never changed
 * through the API, so the record's own trigger is bypassed here). */
function backdateSales(tenant: string, sales: { key: string; at: string }[]) {
  const values = sales
    .map(({ key, at }, i) => {
      const [offset, hour] = at.split("|").map(Number);
      const minute = (i * 17) % 60;
      return `(${q(key)}, ${offset}, ${hour}, ${minute})`;
    })
    .join(",");
  // Today's sales stay before now.
  const text = `SET session_replication_role = replica;
    UPDATE stock_sales s SET sold_at = LEAST(now() - interval '5 minutes',
      ((date_trunc('day', now() AT TIME ZONE 'Africa/Lagos') - make_interval(days => v.d) + make_interval(hours => v.h, mins => v.m)) AT TIME ZONE 'Africa/Lagos'))
    FROM (VALUES ${values}) AS v(k, d, h, m)
    WHERE s.tenant_id = ${q(tenant)} AND s.key = 'api-' || v.k;
    UPDATE stock_movements m SET created_at = s.sold_at, done_at = s.sold_at FROM stock_sales s
      WHERE m.tenant_id = s.tenant_id AND s.tenant_id = ${q(tenant)} AND m.reference = s.reference;
    SET session_replication_role = origin;`;
  try {
    console.log("  back-dated: " + sql(text));
  } catch (e) {
    // stock_movements may not carry the sale: the sales alone, then.
    skipped.push("back-dating stock movements: " + String(e).slice(0, 200));
    sql(text.replace(/UPDATE stock_movements[\s\S]*?m.reference = s.reference;/, ""));
  }
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
