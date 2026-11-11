// How-to 8: riders and deliveries. Chioma's WhatsApp order (how-to 4's)
// is confirmed for delivery, paid at the door; the Taskiem chat says it
// needs a rider; Ada offers it to Kunle, one of her riders; Kunle accepts,
// picks up, arrives and delivers with the buttons on WhatsApp; Ada sees
// each step and the delivery on the Deliveries page.
//
//   node video/howto/08-riders-and-deliveries.mjs          # record, then build
//   node video/howto/08-riders-and-deliveries.mjs build    # build only
//
// Wants a fresh world (video/howto/world.sh). Kunle and Ibrahim are the
// demo's riders.
import { URL } from "node:url";
import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";
import { ADA, customerSays, says, sentTo, taps } from "./whatsapp.mjs";

const SLUG = "08-riders-and-deliveries";
const H = { "X-Taskiem-Request": "1" };
const KUNLE = { from: "2348025550201", name: "Kunle" };
const ORDER = "Good afternoon! Please I want 2 jollof rice, 1 peppered chicken and 2 zobo. Deliver to 14 Adeola Odeku, VI.";
const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});
const must = async (r, what) => {
  if (!r.ok()) throw new Error(`${what}: ${r.status()} ${await r.text()}`);
  return r.status() === 204 ? {} : r.json();
};

// Off camera: Chioma's order, confirmed for delivery and paid at the door,
// and Kunle reachable on WhatsApp (he has messaged Taskiem's number once).
async function prepare() {
  const page = await person("ada");
  const rq = page.request;
  await must(await rq.put(BASE + "/v1/whatsapp/number", { headers: H, data: { phone_number_id: ADA.pnid, waba_id: ADA.waba, access_token: ADA.token, own_app: false } }), "own number");
  await customerSays(rq, BASE, { from: "2348031234567", name: "Chioma", text: ORDER });
  await page.waitForTimeout(4000);
  const chats = (await must(await rq.get(BASE + "/v1/customer-chats", { headers: H }), "chats")).chats;
  const chat = chats.find((c) => /Chioma/.test(JSON.stringify(c)));
  const orders = (await must(await rq.get(BASE + `/v1/customer-chats/${chat.id}/orders`, { headers: H }), "orders")).orders;
  let d = orders[0];
  d = await must(await rq.put(BASE + `/v1/order-drafts/${d.id}`, { headers: H, data: { version: d.version, op: "details", delivery_kobo: 150000, pay: "on_delivery" } }), "details");
  d = d.draft ?? d;
  await must(await rq.post(BASE + `/v1/order-drafts/${d.id}/confirm`, { headers: H, data: { version: d.version } }), "confirm");
  // Kunle taps in: the link the Riders page gives (to send him by SMS)
  // opens WhatsApp with a message to Taskiem's number. The public pages
  // answer for the form host's name, on their own port in the demo.
  const tap = (await must(await rq.get(BASE + "/v1/riders", { headers: H }), "riders")).tap_in_url;
  const at = new URL(tap);
  const go = await rq.get(`http://127.0.0.1:18095${at.pathname}/whatsapp`, { headers: { Host: at.host }, maxRedirects: 0 });
  const text = new URL(go.headers()["location"]).searchParams.get("text");
  if (!text) throw new Error(`tap in: ${go.status()} ${await go.text()}`);
  await says(rq, BASE, { ...KUNLE, text });
  await page.waitForTimeout(1500);
  await page.context().close();
}

// Off camera: Kunle taps the newest message's first button (Accept, Picked
// up, Arrived, Delivered).
async function kunleTaps(want) {
  const page = await person("ada");
  for (let i = 0; i < 20; i++) {
    const sent = await sentTo(page.request, KUNLE.from);
    // The offer can come as a template, its Accept a quick reply.
    const offer = want === "Accept" && sent.findLast((m) => m.template === "taskiem_delivery_offer" && m.payloads?.length);
    if (offer) {
      await taps(page.request, BASE, { ...KUNLE, payload: offer.payloads[0], title: "Accept", on: offer.from });
      await page.waitForTimeout(1500);
      await page.context().close();
      return;
    }
    const last = sent.findLast((m) => m.buttons?.some((b) => b.title === want));
    if (last) {
      const b = last.buttons.find((x) => x.title === want);
      await taps(page.request, BASE, { ...KUNLE, id: b.id, title: b.title, on: last.from });
      await page.waitForTimeout(1500);
      await page.context().close();
      return;
    }
    await page.waitForTimeout(1000);
  }
  throw new Error(`Kunle never had ${want}`);
}

// Off camera: what else needs Ada is put off till later, so the rider card
// is what shows.
async function quiet(page) {
  const off = page.getByTestId("chat-alert").filter({ hasNotText: /needs a rider/ }).getByRole("button", { name: /^Later/ });
  for (let i = 0; i < 12 && (await off.count()) > 0; i++) {
    await off.first().click();
    await page.waitForTimeout(500);
  }
}

// 1 · Ada offers the order to a rider.
async function assign() {
  const page = await person("ada");
  await page.goto(BASE + "/chat?with=taskiem");
  await later(page);
  await page.waitForTimeout(1500);
  await quiet(page);
  const card = page.getByTestId("chat-alert").filter({ hasText: /needs a rider/ }).first();
  await card.waitFor();
  await card.scrollIntoViewIfNeeded();
  await act(page, "howto08-assign", "Ada · Ada's Kitchen, the owner", async () => {
    await say("Chioma's order is confirmed for delivery, paid at the door. The Taskiem chat says it needs a rider.", 3600);
    await say("Assign rider shows Ada's riders and the areas they cover.", 600);
    await card.getByRole("button", { name: /^Assign rider/ }).click();
    const sheet = page.getByTestId("rider-sheet");
    await sheet.waitFor();
    await sleep(2400);
    await say("Next in turn offers it to each rider in turn. Ada picks Kunle.", 1000);
    await sheet.getByRole("button", { name: /Kunle/ }).first().click();
    await sheet.getByText(/Offered to Kunle/).waitFor({ timeout: 15000 });
    await sleep(1600);
    await say("Kunle gets it on WhatsApp: the order, the area and his fee, with Accept and Can't. The address comes once he accepts.", 4600);
  });
  await page.context().close();
}

// 2 · Ada follows it.
async function follow() {
  const page = await person("ada");
  await page.goto(BASE + "/storefront/deliveries");
  await later(page);
  await act(page, "howto08-follow", "Ada · Ada's Kitchen, the owner", async () => {
    await page.waitForTimeout(1200);
    await say("Kunle tapped Accept (and got the address), Picked up and Arrived on WhatsApp, one button after the other.", 3800);
    await say("At the door he was asked for the ₦13,600. He took it in cash and tapped Cash received: delivered.", 3800);
    await say("On the Deliveries page: who has which order, and how far it has got.", 3600);
    await say("Chioma heard on WhatsApp when it left with Kunle, and when it was delivered, with a link to rate it.", 3800);
  });
  await page.context().close();
}

const PARTS = [
  { title: "How to: *send an order with a rider.*" },
  { act: "howto08-assign" },
  { title: "Kunle *delivers.*" },
  { act: "howto08-follow" },
];

// No argument: everything. "build": only join.
// Kunle's buttons in turn. The order is paid at the door: at Arrived he is
// asked for it, and Cash received delivers it.
const STEPS = ["Accept", "Picked up", "Arrived", "Cash received"];
if (process.argv[2] === "follow") {
  await follow();
} else if (process.argv[2] === "rest") {
  // After the assign act: Kunle's taps (from a step on, if named) and the follow act.
  for (const step of STEPS.slice(Math.max(0, STEPS.indexOf(process.argv[3] ?? "Accept")))) await kunleTaps(step);
  await follow();
} else if (process.argv[2] !== "build") {
  await prepare();
  await assign();
  for (const step of STEPS) await kunleTaps(step);
  await follow();
}
console.log(build(SLUG, PARTS));
process.exit(0);
