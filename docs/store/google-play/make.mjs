// Google Play listing images for the Taskiem app, drawn with Chromium.
//
//   cd mobile && STORE_DIR=/tmp/shots flutter test test/conversation_test.dart \
//     test/today_tab_test.dart test/money_page_test.dart test/one_customer_test.dart \
//     test/orders_tab_test.dart test/school_test.dart test/stock_page_test.dart
//   node docs/store/google-play/make.mjs /tmp/shots
//
// Writes icon-512.png, feature-1024x500.png and phone-N.png (1080 x 1920)
// beside this file. The screens are the app's own, from its screenshot
// tests (mobile/test/looks.dart, STORE_DIR) at three pixels to a point.
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

// Playwright is installed once for the machine, not in the repository.
const { chromium } = createRequire(import.meta.url)("playwright");
const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "../../..");
const shots = process.argv[2];
if (!shots) throw new Error("usage: node make.mjs <dir of screens from STORE_DIR>");

const pine = "#0D3B2E";
const lime = "#C8F169";
// Embedded: a page set from a string may not load local files.
const font = (f) => `data:font/ttf;base64,${readFileSync(join(root, "mobile/assets/fonts", f)).toString("base64")}`;
const faces = `
  @font-face { font-family: Tight; src: url(${font("InterTight-700.ttf")}); font-weight: 700; }
  @font-face { font-family: Inter; src: url(${font("Inter-latin.ttf")}); }
  * { margin: 0; box-sizing: border-box; }
  body { background: ${pine}; overflow: hidden; }`;
const glyph = `<path d="M27 40H61" stroke="${lime}" stroke-width="11" stroke-linecap="round"/><path d="M42 22V58Q42 74 58 74H64" stroke="${lime}" stroke-width="11" stroke-linecap="round" fill="none"/><circle cx="78" cy="74" r="7" fill="#ffffff"/>`;
const wordmark = readFileSync(join(root, "docs/brand/relay/wordmark-reversed.svg"), "utf8");
const img = (f) => `data:image/png;base64,${readFileSync(join(shots, f)).toString("base64")}`;

// The order a person scrolls through them: the day, then each kind of work.
const phones = [
  ["today-light.png", "Your business, every morning", "Money in, orders and what needs you, at a glance."],
  ["chats-light.png", "Everyone you work with", "Customers, suppliers and Taskiem, with what needs you first."],
  ["conversation-light.png", "Buy and sell in the chat", "Orders, prices and invoices with the businesses you deal with."],
  ["money-light.png", "Know where your money is", "What came in, what went out, and what is leaking."],
  ["orders-light.png", "No order slips through", "From customers and businesses, new to on the way."],
  ["stock-light.png", "Stock that keeps count", "Every sale counted, low items flagged before they run out."],
  ["customer-chat-light.png", "One customer, any line", "WhatsApp or the app: reply where they wrote. Nothing is missed."],
  ["school-light.png", "School fees, collected", "Each student's own account number, reminders and receipts."],
];

const browser = await chromium.launch();
const page = await browser.newPage();
async function shoot(w, h, html, out) {
  await page.setViewportSize({ width: w, height: h });
  await page.setContent(`<!doctype html><html><head><style>${faces}</style></head><body>${html}</body></html>`);
  await page.evaluate(() => globalThis.document.fonts.ready);
  await page.screenshot({ path: join(here, out), clip: { x: 0, y: 0, width: w, height: h } });
}

// The icon: the mark on pine, full bleed (Play rounds the corners itself).
await shoot(512, 512, `<svg width="512" height="512" viewBox="0 0 100 100"><rect width="100" height="100" fill="${pine}"/>${glyph}</svg>`, "icon-512.png");

// The feature graphic: the mark and the name, the promise, and a phone.
await shoot(1024, 500, `
  <div style="position:absolute;left:72px;top:118px;width:520px">
    <div style="display:flex;align-items:center;gap:22px">
      <svg width="92" height="92" viewBox="0 0 100 100"><rect width="100" height="100" rx="26" fill="#0a2f24"/>${glyph}</svg>
      <div style="height:58px">${wordmark.replace("<svg", '<svg height="58" width="230"')}</div>
    </div>
    <div style="font:700 50px/1.08 Tight;color:#fff;margin-top:34px;letter-spacing:-1px">Run your business<br>from the <span style="color:${lime}">chat</span>.</div>
    <div style="font:400 22px/1.4 Inter;color:rgba(255,255,255,.72);margin-top:16px">Orders, money, stock and staff, automated.</div>
  </div>
  <div style="position:absolute;left:640px;top:46px;width:330px;height:560px;border-radius:44px;background:#082219;padding:11px;box-shadow:0 30px 60px rgba(0,0,0,.35)">
    <div style="width:100%;height:100%;border-radius:34px;overflow:hidden;background:#fff">
      <img src="${img("today-light.png")}" style="width:100%;display:block">
    </div>
  </div>`, "feature-1024x500.png");

// The phones: a headline over the app's own screen, the phone running off the foot.
for (const [i, [file, title, sub]] of phones.entries()) {
  await shoot(1080, 1920, `
    <div style="position:absolute;left:90px;right:90px;top:112px;text-align:center">
      <div style="font:700 68px/1.08 Tight;color:#fff;letter-spacing:-1.4px">${title}</div>
      <div style="font:400 34px/1.38 Inter;color:rgba(255,255,255,.74);margin-top:22px">${sub}</div>
    </div>
    <div style="position:absolute;left:90px;top:470px;width:900px;height:1600px;border-radius:72px;background:#082219;padding:18px;box-shadow:0 40px 80px rgba(0,0,0,.35)">
      <div style="width:100%;height:100%;border-radius:56px;overflow:hidden;background:#fff">
        <img src="${img(file)}" style="width:100%;display:block">
      </div>
    </div>`, `phone-${i + 1}.png`);
}
await browser.close();
