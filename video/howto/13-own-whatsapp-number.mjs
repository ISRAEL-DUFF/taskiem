// How-to 13: connecting your own WhatsApp number. Messages come from
// Taskiem's shared number until Ada connects Ada's Kitchen's own; in
// Settings she follows the guide, puts in the ids and keys from Meta,
// and connects. Then a customer's message to that number reaches her chat.
//
//   node video/howto/13-own-whatsapp-number.mjs          # record, then build
//   node video/howto/13-own-whatsapp-number.mjs build    # build only
//
// Wants a fresh world (video/howto/world.sh): Ada's number not yet
// connected. The WhatsApp stand-in (fakes.mjs) plays Meta.
import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";
import { ADA, ADA_APP_SECRET, customerSays } from "./whatsapp.mjs";

const SLUG = "13-own-whatsapp-number";
const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});

const type = async (field, text, delay = 60) => {
  await field.scrollIntoViewIfNeeded();
  await field.click();
  await field.pressSequentially(text, { delay });
};

// 1 · The guide, and connecting.
async function connect() {
  const page = await person("ada");
  await page.goto(BASE + "/settings");
  await later(page);
  const box = page.getByTestId("whatsapp-own-number");
  await box.waitFor();
  await box.scrollIntoViewIfNeeded();
  await page.waitForTimeout(800);
  await act(page, "howto13-connect", "Ada · Ada's Kitchen, the owner", async () => {
    await say("Until now, Ada's customers and staff hear from Taskiem's shared number. In Settings: WhatsApp number.", 4200);
    await say("Most businesses never need more. To send as Ada's Kitchen, the guide gives each step on Meta's side.", 600);
    await box.getByText("How to use your own WhatsApp number").click();
    await sleep(1200);
    for (let i = 0; i < 4; i++) {
      await page.mouse.wheel(0, 220);
      await sleep(900);
    }
    await sleep(800);
    await say("From Meta: the phone number id, the WhatsApp Business Account id and a system user's token.", 300);
    await type(box.getByLabel("Phone number id"), ADA.pnid, 70);
    await type(box.getByLabel("WhatsApp Business Account id"), ADA.waba, 70);
    await type(box.getByLabel("System user access token"), ADA.token, 90);
    await sleep(600);
    await say("On her own Meta app, the app secret too, and a verify token she makes up.", 300);
    await type(box.getByLabel("App secret"), ADA_APP_SECRET, 30);
    await type(box.getByLabel("Verify token"), "adas-kitchen-verify-2026", 40);
    await sleep(800);
    await say("Connect.", 600);
    await box.getByRole("button", { name: "Connect" }).click();
    await box.getByText(/^Connected:/).waitFor({ timeout: 15000 });
    await box.getByText(/^Connected:/).scrollIntoViewIfNeeded();
    await sleep(1400);
    await say("Connected. Taskiem shows the two addresses to paste back into the Meta app.", 4200);
    await box.getByTestId("whatsapp-templates").scrollIntoViewIfNeeded();
    await sleep(600);
    await say("And the message templates Meta must approve, for messages outside the 24-hour window.", 4200);
  });
  await page.context().close();
}

// 2 · A customer writes to the new number.
async function hear() {
  const page = await person("ada");
  await customerSays(page.request, BASE, { from: "2348039876543", name: "Funmi", text: "Good evening, do you deliver to Yaba?", ownApp: true });
  await page.goto(BASE + "/chat");
  await later(page);
  const row = page.getByRole("link", { name: /Funmi/ }).first();
  await row.waitFor({ timeout: 15000 });
  await act(page, "howto13-hear", "Ada · Ada's Kitchen, the owner", async () => {
    await sleep(600);
    await say("Funmi writes to Ada's Kitchen's own number. Her message is in Ada's Chat, under Customers.", 600);
    await row.click();
    await page.getByText("Good evening, do you deliver to Yaba?").last().waitFor();
    await sleep(3400);
    await say("Ada's reply goes from her own number.", 300);
    const reply = page.getByRole("textbox").last();
    await reply.click();
    await reply.pressSequentially("Yes we do, ₦1,500 to Yaba. What would you like?", { delay: 45 });
    await reply.press("Enter");
    await sleep(3200);
  });
  await page.context().close();
}

const PARTS = [
  { title: "How to: *connect your own WhatsApp number.*" },
  { act: "howto13-connect" },
  { title: "Customers *write to it.*" },
  { act: "howto13-hear" },
];

// No argument: everything. "build": only join. Act names: record those, then join.
const ACTS = { connect, hear };
const which = process.argv.slice(2);
if (which.length === 0) for (const f of Object.values(ACTS)) await f();
else for (const n of which.filter((n) => n !== "build")) await ACTS[n]();
console.log(build(SLUG, PARTS));
process.exit(0);
