// How-to 5: staff by phone, and clocking in. Ada adds Kemi, a new cashier,
// from the Taskiem chat with her name and phone number; Kemi joins from the
// WhatsApp message Taskiem sends her; Ada sets her usual hours; Kemi says
// "in" on WhatsApp and Tunde taps Clock in on the web; Ada sees who is in.
//
//   node video/howto/05-staff-and-clocking-in.mjs          # record, then build
//   node video/howto/05-staff-and-clocking-in.mjs build    # build only
//   node video/howto/05-staff-and-clocking-in.mjs hours    # one act again, then build
//
// Wants the world video/howto/world.sh starts (WhatsApp on, against the
// stand-in in fakes.mjs). It needs nothing from how-tos 1 to 4.
import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";
import { says, sentTo, taps } from "./whatsapp.mjs";

const SLUG = "05-staff-and-clocking-in";
const KEMI = { from: "2348055550123", name: "Kemi" };
const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});

async function taskiem(who) {
  const page = await person(who);
  await page.goto(BASE + "/chat?with=taskiem");
  await later(page);
  await page.waitForTimeout(1200);
  await quiet(page);
  return page;
}

// Off camera: what needs Ada (low stock and the like) is put off till
// later, so the conversation is what shows.
async function quiet(page) {
  const later = page.getByTestId("chat-alert").getByRole("button", { name: /^Later/ });
  for (let i = 0; i < 12 && (await later.count()) > 0; i++) {
    await later.first().click();
    await page.waitForTimeout(500);
  }
}

async function write(page, text) {
  const box = page.getByLabel(/^Message|Ask Taskiem/).first();
  await box.click();
  await box.pressSequentially(text, { delay: 60 });
  await sleep(500);
  await box.press("Enter");
}

// 1 · Ada adds Kemi from the chat.
async function add() {
  const page = await taskiem("ada");
  await act(page, "howto05-add", "Ada · Ada's Kitchen, the owner", async () => {
    await say("Ada has a new cashier, Kemi. She adds her from the Taskiem chat: a name, a phone number and what she does.", 1200);
    await write(page, "add Kemi 0805 555 0123 as cashier");
    await page.getByText(/^Invite Kemi/).last().waitFor({ timeout: 15000 });
    await sleep(800);
    await say("Taskiem asks first. Nothing goes to Kemi until Ada says yes.", 2200);
    await write(page, "yes");
    await page.getByText(/^Invitation sent to Kemi/).last().waitFor({ timeout: 15000 });
    await sleep(1000);
    await say("Kemi gets a WhatsApp message from Taskiem: Ada added her to Ada's Kitchen as a cashier, with Join. She needs no email and no password.", 5200);
  });
  await page.context().close();
}

// Off camera: Kemi taps Join.
async function join() {
  const page = await person("ada");
  const invite = (await sentTo(page.request, KEMI.from)).findLast((m) => m.template === "taskiem_invite");
  if (!invite?.payloads?.length) throw new Error("no invitation reached Kemi's number");
  await taps(page.request, BASE, { ...KEMI, payload: invite.payloads[0], title: "Join" });
  await page.waitForTimeout(1500);
  // Where Taskiem's terms are on, she agrees to them first.
  const terms = (await sentTo(page.request, KEMI.from)).at(-1);
  if (terms?.buttons?.length) {
    await taps(page.request, BASE, { ...KEMI, id: terms.buttons[0].id, title: terms.buttons[0].title });
    await page.waitForTimeout(1500);
  }
  const last = (await sentTo(page.request, KEMI.from)).at(-1);
  if (!/You are in/.test(last?.text ?? "")) throw new Error(`Kemi did not join: ${JSON.stringify(last)}`);
  await page.context().close();
}

// 2 · Ada is told, and sets Kemi's usual hours.
async function hours() {
  const page = await person("ada");
  await later(page);
  await page.waitForTimeout(1200);
  await act(page, "howto05-hours", "Ada · Ada's Kitchen, the owner", async () => {
    await say("Kemi tapped Join on her phone. Ada is told: she has a new notification.", 2400);
    await page.getByRole("button", { name: /^Notifications/ }).first().click();
    const panel = page.getByRole("dialog", { name: "Notifications" });
    const told = panel.getByText(/^Kemi joined/).first();
    await told.waitFor();
    await sleep(800);
    await say("Kemi joined Ada's Kitchen, as cashier. It opens the Staff page, where everyone who works there is.", 2600);
    await told.click();
    await page.waitForURL(/\/staff/);
    const kemi = page.getByTestId("staff-person").filter({ hasText: "Kemi" });
    await kemi.waitFor();
    await kemi.scrollIntoViewIfNeeded();
    await sleep(2400);
    await say("Ada opens Kemi to set her usual hours: Monday to Saturday, 8:00 to 4:00.", 600);
    await kemi.getByRole("link", { name: "Kemi" }).click();
    await page.getByRole("heading", { name: "Kemi", level: 1 }).waitFor();
    await sleep(1000);
    await page.getByRole("button", { name: "Change" }).click();
    const edit = page.getByRole("dialog", { name: /^Change Kemi/ });
    await edit.waitFor();
    for (const day of ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"]) {
      const box = edit.getByRole("checkbox", { name: day });
      await box.scrollIntoViewIfNeeded();
      await box.check();
      await sleep(250);
    }
    await sleep(800);
    await edit.getByRole("button", { name: "Save" }).click();
    await edit.waitFor({ state: "detached" });
    await page.getByTestId("person-facts").scrollIntoViewIfNeeded();
    await sleep(800);
    await say("If Kemi hasn't clocked in 15 minutes after 8:00, Ada is told: \"Kemi has not clocked in. Due at 08:00.\"", 4600);
  });
  await page.context().close();
}

// Off camera: Kemi starts her shift with one word on WhatsApp.
async function kemiIn() {
  const page = await person("ada");
  await says(page.request, BASE, { ...KEMI, text: "in" });
  await page.waitForTimeout(1500);
  const last = (await sentTo(page.request, KEMI.from)).at(-1);
  if (!/[Cc]locked in at/.test(last?.text ?? "")) throw new Error(`Kemi did not clock in: ${JSON.stringify(last)}`);
  await page.context().close();
}

// 3 · Tunde clocks in from the web.
async function tile() {
  const page = await taskiem("tunde");
  await act(page, "howto05-in", "Tunde · a cashier at Ada's Kitchen", async () => {
    await say("Kemi starts her shift with one word on WhatsApp: in. Tunde, another cashier, uses Taskiem on the web.", 3400);
    await page.getByTestId("tools-button").click();
    const tile = page.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Clock in/ });
    await tile.waitFor();
    await sleep(1200);
    await say("In the chat's tools: Clock in.", 600);
    await tile.click();
    await page.getByText(/^Clocked in at/).last().waitFor({ timeout: 15000 });
    await sleep(800);
    await say("He is clocked in. At the end of the day: out, or Clock out.", 3600);
  });
  await page.context().close();
}

// 4 · Ada sees who is in.
async function who() {
  const page = await person("ada");
  await page.goto(BASE + "/staff");
  await later(page);
  await act(page, "howto05-who", "Ada · Ada's Kitchen, the owner", async () => {
    const today = page.getByTestId("staff-shift").first();
    await today.waitFor();
    await today.scrollIntoViewIfNeeded();
    await sleep(800);
    await say("On the Staff page, Today: who is in now, and since when.", 3400);
    await say("From any chat, Ada can ask: who is in.", 600);
    await page.goto(BASE + "/chat?with=taskiem");
    await later(page);
    await quiet(page);
    await sleep(800);
    await write(page, "who is in");
    await page.getByText(/^In now/).last().waitFor({ timeout: 15000 });
    await sleep(800);
    await say("The morning briefing says it too, and who is not in yet.", 3600);
  });
  await page.context().close();
}

const PARTS = [
  { title: "How to: *add staff by phone, and clock in.*" },
  { title: "1. Add *by phone number.*" },
  { act: "howto05-add" },
  { title: "2. Kemi *joins.*" },
  { act: "howto05-hours" },
  { title: "3. Clock *in.*" },
  { act: "howto05-in" },
  { act: "howto05-who" },
];

// No argument: everything. "build": only join. Act names: record those, then join.
const ACTS = { add, join, hours, kemiIn, tile, who };
const which = process.argv.slice(2);
if (which.length === 0) for (const f of Object.values(ACTS)) await f();
else for (const n of which.filter((n) => n !== "build")) await ACTS[n]();
console.log(build(SLUG, PARTS));
process.exit(0);
