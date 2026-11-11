// How-to 4: a customer's order from WhatsApp. Chioma messages Ada's
// Kitchen's own WhatsApp number with an order in her own words; Taskiem
// reads it into an order card in her conversation, matched to Ada's menu
// at Ada's prices; Ada adds the delivery fee, confirms it (Chioma gets the
// order on WhatsApp) and replies.
//
//   node video/howto/04-customer-whatsapp-order.mjs          # record, then build
//   node video/howto/04-customer-whatsapp-order.mjs build    # build only
//
// Wants the world video/howto/world.sh starts (WhatsApp and the model on,
// against the stand-ins in fakes.mjs), after how-tos 1 to 3.
import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";
import { ADA, customerSays } from "./whatsapp.mjs";

const SLUG = "04-customer-whatsapp-order";
const H = { "X-Taskiem-Request": "1" };
const ORDER = "Good afternoon! Please I want 2 jollof rice, 1 peppered chicken and 2 zobo. Deliver to 14 Adeola Odeku, VI.";
const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});

// Off camera: Ada's Kitchen's own WhatsApp number (connecting it is a
// how-to of its own), then Chioma's message to it.
async function prepare() {
  const page = await person("ada");
  const r = await page.request.put(BASE + "/v1/whatsapp/number", {
    headers: H,
    data: { phone_number_id: ADA.pnid, waba_id: ADA.waba, access_token: ADA.token, own_app: false },
  });
  if (r.status() !== 200) throw new Error(`connect: ${r.status()} ${await r.text()}`);
  await customerSays(page.request, BASE, { from: "2348031234567", name: "Chioma", text: ORDER });
  await page.waitForTimeout(4000);
  await page.context().close();
}

async function order() {
  const page = await person("ada");
  await page.goto(BASE + "/chat");
  await later(page);
  await page.waitForTimeout(1200);
  await act(page, "howto04-order", "Ada · Ada's Kitchen, on her own WhatsApp number", async () => {
    await say("Chioma, a customer, has sent Ada's Kitchen's WhatsApp number an order, in her own words.", 3400);
    const row = page.getByTestId("customer-chat").filter({ hasText: "Chioma" }).first();
    await say("Customers' messages come into Ada's chats, beside the businesses she trades with.", 1200);
    await row.click();
    const pane = page.getByTestId("customer-pane");
    await pane.waitFor();
    await sleep(1800);
    await say("Taskiem read the message into an order: each thing she asked for, from Ada's own menu, at Ada's prices.", 3800);
    const card = pane.getByTestId("customer-order");
    await card.getByText(/They wrote/).first().evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(1200);
    await say("Under each line, what she wrote. Change a number with − and +, or add an item she asks for.", 3800);
    const where = card.getByLabel("Deliver to");
    await where.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(1000);
    await say("Delivered, to the address in her message. Ada adds the delivery fee.", 800);
    const fee = card.getByLabel("Delivery fee, ₦");
    await fee.click();
    await fee.pressSequentially("1500", { delay: 120 });
    await sleep(700);
    await card.getByRole("button", { name: "Save" }).click();
    await sleep(1600);
    await say("How she pays: when it arrives, here. (With a payment page, she can pay now by transfer.)", 800);
    const door = card.getByLabel("They pay when it arrives");
    await door.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(600);
    await door.check();
    await sleep(2400);
    await say("Nothing goes to Chioma until Ada taps Confirm.", 1000);
    await card.getByRole("button", { name: "Confirm" }).click();
    await pane.getByText(/^Your order /).last().waitFor({ timeout: 15000 });
    await sleep(800);
    await pane.getByText(/^Your order /).last().scrollIntoViewIfNeeded();
    await say("Chioma gets her order on WhatsApp: each line, the delivery, the total and how she pays. The card says Confirmed.", 4600);
    await say("Ada can write to her in the same conversation.", 600);
    const box = pane.getByRole("textbox").last();
    await box.click();
    await box.pressSequentially("Thank you Chioma! It will be with you within the hour.", { delay: 45 });
    await sleep(500);
    await pane.getByRole("button", { name: "Send" }).click();
    await sleep(1800);
    await say("It goes from Ada's Kitchen's own number. The order is on the Orders page, to pack and send like any other.", 4400);
  });
  await page.context().close();
}

const PARTS = [{ title: "How to: *a customer's order from WhatsApp.*" }, { act: "howto04-order" }];

// No argument: everything. "build": only join.
if (process.argv[2] !== "build") {
  await prepare();
  await order();
}
console.log(build(SLUG, PARTS));
process.exit(0);
