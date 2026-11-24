// How-to 6: tasks for staff. Ada gives Tunde, a cashier, a task from the
// Taskiem chat by tagging him, and a task that comes back every day;
// Tunde hears in his chat, and taps Done; Ada sees it done on the Tasks
// page, with the daily task under Repeats.
//
//   node video/howto/06-tasks-for-staff.mjs          # record, then build
//   node video/howto/06-tasks-for-staff.mjs build    # build only
//   node video/howto/06-tasks-for-staff.mjs done     # one act again, then build
//
// Wants a fresh world (video/howto/world.sh); it needs nothing from the
// other how-tos.
import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";

const SLUG = "06-tasks-for-staff";
const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});

async function taskiem(who) {
  const page = await person(who);
  await page.goto(BASE + "/chat?with=taskiem");
  await later(page);
  await page.waitForTimeout(1200);
  await quiet(page);
  return page;
}

// Off camera: what needs them now (low stock and the like) is put off till
// later, so the conversation is what shows.
async function quiet(page) {
  const off = page.getByTestId("chat-alert").getByRole("button", { name: /^Later/ });
  for (let i = 0; i < 12 && (await off.count()) > 0; i++) {
    await off.first().click();
    await page.waitForTimeout(500);
  }
}

async function write(page, text) {
  const box = page.getByLabel(/^Message|Ask Taskiem/).first();
  await box.click();
  await box.pressSequentially(text, { delay: 55 });
  await sleep(500);
  await box.press("Enter");
}

// Taps a reply button on Taskiem's newest message.
async function reply(page, name) {
  await page.getByTestId("taskiem-message").last().getByRole("button", { name, exact: true }).click();
}

// 1 · Ada gives the tasks, by tagging Tunde.
async function give() {
  const page = await taskiem("ada");
  await act(page, "howto06-give", "Ada · Ada's Kitchen, the owner", async () => {
    await say("Ada gives Tunde a task the way she would tell him: @ his name, what to do and by when.", 1000);
    await write(page, "@Tunde restock the drinks fridge by 6pm");
    await page.getByText(/^Make it a task for Tunde/).last().waitFor({ timeout: 15000 });
    await sleep(800);
    await say("Taskiem asks first. Yes makes it a task.", 1200);
    await reply(page, "Yes");
    await page.getByText(/^Task given to Tunde/).last().waitFor({ timeout: 15000 });
    await sleep(2000);
    await say("A task that comes back: say every day, or every Monday, and the time.", 800);
    await write(page, "@Tunde count the cash float every day at 7am");
    await page.getByText(/^Make it a task for Tunde/).last().waitFor({ timeout: 15000 });
    await sleep(1600);
    await reply(page, "Yes");
    await page.getByText(/Each time it falls due/).last().waitFor({ timeout: 15000 });
    await sleep(2400);
    await say("Each day's is a task of its own, given to Tunde at the start of the day.", 3000);
  });
  await page.context().close();
}

// 2 · Tunde hears, and taps Done.
async function done() {
  const page = await taskiem("tunde");
  await act(page, "howto06-done", "Tunde · a cashier at Ada's Kitchen", async () => {
    const task = page.getByTestId("taskiem-message").filter({ hasText: /restock the drinks fridge/i }).last();
    await task.scrollIntoViewIfNeeded();
    await say("Tunde hears in his chat, here or on WhatsApp, with Done and Can't.", 3400);
    await say("His tasks are also under Tasks in the chat's tools.", 600);
    await page.getByTestId("tools-button").click();
    await page.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Tasks/ }).click();
    const sheet = page.getByTestId("tasks-sheet");
    const row = sheet.getByTestId("task").filter({ hasText: /drinks fridge/i });
    await row.waitFor();
    await sleep(2200);
    await say("The fridge is full. Done.", 600);
    await row.getByRole("button", { name: /^Done/ }).click();
    await row.getByText("Done").first().waitFor({ timeout: 15000 });
    await sleep(1200);
    await say("Can't, with a word why (\"No drinks left in the store\"), sends it back to Ada instead.", 3600);
  });
  await page.context().close();
}

// 3 · Ada sees it done.
async function see() {
  const page = await person("ada");
  await page.goto(BASE + "/tasks");
  await later(page);
  await act(page, "howto06-see", "Ada · Ada's Kitchen, the owner", async () => {
    await page.getByRole("button", { name: "Everyone's" }).click();
    const row = page.getByTestId("task").filter({ hasText: /drinks fridge/i });
    await row.waitFor();
    await row.scrollIntoViewIfNeeded();
    await sleep(800);
    await say("On the Tasks page, Everyone's: Tunde did it.", 2800);
    const repeat = page.getByTestId("task-repeat").filter({ hasText: /cash float/i });
    await repeat.scrollIntoViewIfNeeded();
    await sleep(800);
    await say("The daily one is under Repeats: Every day at 07:00, with Stop.", 2800);
    await say("Half an hour before a task is due, Tunde is nudged. If it is late, Ada is told, with Remind.", 3600);
  });
  await page.context().close();
}

const PARTS = [
  { title: "How to: *give staff tasks.*" },
  { title: "1. Tag them *in the chat.*" },
  { act: "howto06-give" },
  { title: "2. They tap *Done.*" },
  { act: "howto06-done" },
  { title: "3. See it *done.*" },
  { act: "howto06-see" },
];

// No argument: everything. "build": only join. Act names: record those, then join.
const ACTS = { give, done, see };
const which = process.argv.slice(2);
if (which.length === 0) for (const f of Object.values(ACTS)) await f();
else for (const n of which.filter((n) => n !== "build")) await ACTS[n]();
console.log(build(SLUG, PARTS));
process.exit(0);
