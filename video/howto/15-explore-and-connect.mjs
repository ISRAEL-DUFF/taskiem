// How-to 15: finding a business in Explore and connecting. Ada searches
// Explore for a suya seller near her and asks Mallam Musa Suya Spot to
// link; Mallam Musa accepts, and they have a chat of their own. Kola's
// Bakery has asked Ada; she accepts from her chat list.
//
//   node video/howto/15-explore-and-connect.mjs          # record, then build
//   node video/howto/15-explore-and-connect.mjs build    # build only
//
// Wants a fresh world (video/howto/world.sh): Kola's Bakery has asked Ada
// to link, and Mallam Musa Suya Spot is findable but not linked.
import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";

const SLUG = "15-explore-and-connect";
const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});
const ADA = "Ada · Ada's Kitchen, the owner";
const MUSA = "Mallam Musa · Mallam Musa Suya Spot";

async function chats(who) {
  const page = await person(who);
  await page.goto(BASE + "/chat");
  await later(page);
  await page.waitForTimeout(1200);
  return page;
}

// 1 · Ada finds a business and asks to link.
async function find() {
  const page = await chats("ada");
  await act(page, "howto15-find", ADA, async () => {
    await say("Ada wants a suya seller for her small chops trays. In Chat: Find a business.", 600);
    await page.getByRole("button", { name: "Find a business to link with" }).first().click();
    const ex = page.getByTestId("explore");
    await ex.waitFor();
    await sleep(1600);
    await say("Explore shows businesses near her first. She searches by what they sell.", 300);
    const q = ex.getByTestId("explore-search");
    await q.click();
    await q.pressSequentially("suya", { delay: 120 });
    const card = ex.getByTestId("explore-card").filter({ hasText: "Mallam Musa Suya Spot" }).first();
    await card.waitFor({ timeout: 10000 });
    await sleep(1400);
    await card.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await say("What they sell, a word about them, and their photos.", 3400);
    await say("Connect asks them to link.", 600);
    await card.getByRole("button", { name: /^Connect/ }).click();
    await card.getByText("Requested", { exact: true }).waitFor({ timeout: 10000 });
    await sleep(800);
    await say("Requested: they are asked to accept. Withdraw takes it back.", 3400);
  });
  await page.context().close();
}

// 2 · Mallam Musa accepts.
async function accept() {
  const page = await chats("suya");
  const yes = page.getByRole("button", { name: /^Accept Ada's Kitchen/ }).first();
  await yes.waitFor({ timeout: 15000 });
  await act(page, "howto15-accept", MUSA, async () => {
    await say("Mallam Musa sees Ada's Kitchen's request at the top of his chats.", 3400);
    await say("Accept.", 600);
    await yes.click();
    await page.getByRole("link", { name: /^Ada's Kitchen/ }).first().waitFor({ timeout: 15000 });
    await sleep(1000);
    await say("Linked: Ada's Kitchen is in his chats.", 4200);
  });
  await page.context().close();
}

// 3 · Ada answers Kola's Bakery, who asked her.
async function asked() {
  const page = await chats("ada");
  const yes = page.getByRole("button", { name: "Accept Kola's Bakery" }).first();
  await yes.waitFor({ timeout: 15000 });
  await act(page, "howto15-asked", ADA, async () => {
    await say("Businesses can find Ada too. Kola's Bakery asked to link: Accept, Decline or Block.", 4000);
    await yes.click();
    await page.getByRole("link", { name: /^Kola's Bakery/ }).first().waitFor({ timeout: 15000 });
    await sleep(800);
    await say("And Mallam Musa Suya Spot accepted hers: both are in her chats now.", 4200);
  });
  await page.context().close();
}

const PARTS = [
  { title: "How to: *find a business and connect.*" },
  { act: "howto15-find" },
  { title: "They *accept.*" },
  { act: "howto15-accept" },
  { title: "When a business *asks you.*" },
  { act: "howto15-asked" },
];

// No argument: everything. "build": only join. Act names: record those, then join.
const ACTS = { find, accept, asked };
const which = process.argv.slice(2);
if (which.length === 0) for (const f of Object.values(ACTS)) await f();
else for (const n of which.filter((n) => n !== "build")) await ACTS[n]();
console.log(build(SLUG, PARTS));
process.exit(0);
