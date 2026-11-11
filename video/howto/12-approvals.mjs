// How-to 12: approvals. Tunde has asked to pay Bayo Rice Mills ₦450,000;
// the Taskiem chat shows Ada the request as a card that needs her OK.
// She checks it and approves it with a comment.
//
//   node video/howto/12-approvals.mjs          # record, then build
//   node video/howto/12-approvals.mjs build    # build only
//
// Wants a fresh world (video/howto/world.sh): the approval is the seed's.

import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";

const SLUG = "12-approvals";
const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});

// Off camera: what needs them now is put off till later, so the
// conversation is what shows.
async function quiet(page) {
  const off = page.getByTestId("chat-alert").getByRole("button", { name: /^Later/ });
  for (let i = 0; i < 12 && (await off.count()) > 0; i++) {
    await off.first().click();
    await page.waitForTimeout(500);
  }
}

async function taskiem(who) {
  const page = await person(who);
  await page.goto(BASE + "/chat?with=taskiem");
  await later(page);
  await page.waitForTimeout(1500);
  await quiet(page);
  return page;
}

// 1 · Ada approves.
async function approve() {
  const page = await taskiem("ada");
  const card = page.getByTestId("chat-approval").first();
  await card.waitFor();
  await card.scrollIntoViewIfNeeded();
  await act(page, "howto12-approve", "Ada · Ada's Kitchen, the owner", async () => {
    await say("Tunde asked to pay Bayo Rice Mills. The Taskiem chat shows Ada a card: Needs your OK.", 3800);
    await say("Who asks, for what, how much and to which account, with Decline and Approve.", 3200);
    await card.getByRole("button", { name: /^Approve/ }).click();
    const sheet = page.getByTestId("approve-sheet");
    await sheet.waitFor();
    await say("Approve asks once more. It is the first payment to this account, so Taskiem says to check the number and name first.", 5200);
    const note = sheet.getByLabel("A comment (optional, recorded)");
    await note.scrollIntoViewIfNeeded();
    await say("A comment if she wants one. Her decision and what she was shown are recorded.", 400);
    await note.click();
    await note.pressSequentially("OK, for this week's rice", { delay: 55 });
    await sleep(1200);
    await sheet.getByRole("button", { name: "Approve", exact: true }).click();
    await sheet.waitFor({ state: "detached", timeout: 15000 });
    await sleep(200);
    await say("The card goes, and the chat keeps a line of what she approved.", 3600);
  });
  await page.context().close();
}

const PARTS = [
  { title: "How to: *approve what needs your OK.*" },
  { act: "howto12-approve" },
];

// No argument: everything. "build": only join.
if (process.argv[2] !== "build") await approve();
console.log(build(SLUG, PARTS));
process.exit(0);
