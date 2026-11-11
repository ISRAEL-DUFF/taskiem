// How-to 7: the morning briefing. Ada opens the Taskiem chat in the
// morning to the day's card: yesterday's sales against the same day last
// week, what she is owed, low stock, today's deliveries and tasks. A tile
// opens what it counts; Got it puts the card away for the day, and the
// Briefing tool brings it back; "how did we do this week" draws the week.
//
//   node video/howto/07-morning-briefing.mjs          # record, then build
//   node video/howto/07-morning-briefing.mjs build    # build only
//
// Wants a fresh world (video/howto/world.sh), recorded after 8:00 Lagos
// time (the card shows from then). It needs nothing from the other how-tos.
import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";

const SLUG = "07-morning-briefing";
const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});

// Off camera: what needs Ada now (low stock and the like) is put off till
// later, so the briefing is what shows.
async function quiet(page) {
  const off = page.getByTestId("chat-alert").getByRole("button", { name: /^Later/ });
  for (let i = 0; i < 12 && (await off.count()) > 0; i++) {
    await off.first().click();
    await page.waitForTimeout(500);
  }
}

async function brief() {
  const page = await person("ada");
  await page.goto(BASE + "/chat?with=taskiem");
  await later(page);
  await page.waitForTimeout(1500);
  await quiet(page);
  const card = page.getByTestId("briefing-card");
  await card.waitFor();
  await card.scrollIntoViewIfNeeded();
  await act(page, "howto07-brief", "Ada · Ada's Kitchen, the owner", async () => {
    await say("Every morning from 8, Ada's Taskiem chat opens on the day's briefing.", 3200);
    await say("Sales yesterday, against the same day last week, with the last seven days as bars.", 3600);
    await say("Then what she is owed, what is running low, today's deliveries and tasks.", 3400);
    await say("Each one opens what it counts. Low stock opens the Stock tool.", 600);
    await card.getByRole("button", { name: /Low stock/ }).click();
    const sheet = page.getByTestId("tool-sheet");
    await sheet.waitFor();
    await sleep(2600);
    await sheet.getByRole("button", { name: /^Close/ }).click();
    await sheet.waitFor({ state: "detached" });
    await sleep(800);
    await say("Got it puts the card away for the day.", 600);
    await card.getByRole("button", { name: /^Got it/ }).click();
    await card.waitFor({ state: "detached" });
    await sleep(1600);
    await say("It is back any time from the chat's tools: Briefing. Or type briefing.", 600);
    await page.getByTestId("tools-button").click();
    await page.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Briefing/ }).click();
    await card.waitFor();
    await sleep(2400);
  });
  await page.context().close();
}

async function week() {
  const page = await person("ada");
  await page.goto(BASE + "/chat?with=taskiem");
  await later(page);
  await page.waitForTimeout(1500);
  await quiet(page);
  await act(page, "howto07-week", "Ada · Ada's Kitchen, the owner", async () => {
    await say("Ada can ask about any stretch in her own words.", 600);
    const box = page.getByLabel(/^Message|Ask Taskiem/).first();
    await box.click();
    await box.pressSequentially("how did we do this week", { delay: 60 });
    await sleep(500);
    await box.press("Enter");
    const chart = page.getByTestId("chart-card").last();
    await chart.waitFor({ timeout: 15000 });
    await chart.scrollIntoViewIfNeeded();
    await sleep(800);
    await say("The week's sales, day by day, and how they compare with the week before.", 4200);
    await say("Tapping Sales yesterday on the card asks the same about yesterday.", 3400);
  });
  await page.context().close();
}

const PARTS = [
  { title: "How to: *read the morning briefing.*" },
  { act: "howto07-brief" },
  { title: "Ask *how you did.*" },
  { act: "howto07-week" },
];

// No argument: everything. "build": only join. Act names: record those, then join.
const ACTS = { brief, week };
const which = process.argv.slice(2);
if (which.length === 0) for (const f of Object.values(ACTS)) await f();
else for (const n of which.filter((n) => n !== "build")) await ACTS[n]();
console.log(build(SLUG, PARTS));
process.exit(0);
