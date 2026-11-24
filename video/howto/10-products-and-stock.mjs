// How-to 10: products and stock. Ada adds a new dish with Products in the
// Taskiem chat's tools; then, with Stock, sees what is running low and
// records the rice that came in.
//
//   node video/howto/10-products-and-stock.mjs          # record, then build
//   node video/howto/10-products-and-stock.mjs build    # build only
//   node video/howto/10-products-and-stock.mjs stock    # one act again, then build
//
// Wants a fresh world (video/howto/world.sh); it needs nothing from the
// other how-tos.
import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";

const SLUG = "10-products-and-stock";
const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});

// Off camera: what needs Ada now is put off till later, so the
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
  // The briefing is put away too: Got it.
  await page.getByTestId("briefing-card").getByRole("button", { name: /^Got it/ }).click({ timeout: 2000 }).catch(() => {});
  await page.waitForTimeout(800);
  return page;
}

const tool = async (page, name) => {
  await page.getByTestId("tools-button").click();
  await page.getByRole("region", { name: "Tools" }).getByRole("button", { name: new RegExp("^" + name) }).click();
  const sheet = page.getByTestId("tool-sheet");
  await sheet.waitFor();
  return sheet;
};

const type = async (field, text, delay = 70) => {
  await field.scrollIntoViewIfNeeded();
  await field.click();
  await field.pressSequentially(text, { delay });
};

// 1 · A new product.
async function add() {
  const page = await taskiem("ada");
  await act(page, "howto10-add", "Ada · Ada's Kitchen, the owner", async () => {
    await say("Ada has a new dish. In the chat's tools: Products.", 600);
    const sheet = await tool(page, "Products");
    await sleep(2000);
    await say("Everything she sells, to search or filter. Add product.", 600);
    await sheet.getByRole("button", { name: "Add product" }).click();
    await sheet.getByLabel("Name").waitFor();
    await sleep(800);
    await say("Its name, a category, the price, what it is sold by and how many there are.", 400);
    await type(sheet.getByLabel("Name"), "Fried rice and chicken");
    await type(sheet.getByLabel("Category"), "Rice dishes");
    await type(sheet.getByLabel("Price"), "4500", 120);
    await type(sheet.getByLabel("Sold by the"), "plate");
    const n = sheet.getByRole("textbox", { name: "In stock now" });
    await n.scrollIntoViewIfNeeded();
    await n.fill("");
    await n.pressSequentially("20", { delay: 150 });
    await sleep(1400);
    await sheet.getByRole("button", { name: "Add product" }).last().click();
    await sheet.waitFor({ state: "detached", timeout: 15000 });
    await sleep(200);
    await say("It is in her products, and the chat keeps a line of what changed.", 3600);
  });
  await page.context().close();
}

// 2 · What is low, and goods in.
async function stock() {
  const page = await taskiem("ada");
  await act(page, "howto10-stock", "Ada · Ada's Kitchen, the owner", async () => {
    await say("Stock, in the same tools, puts what is running low first.", 600);
    const sheet = await tool(page, "Stock");
    await sleep(2200);
    await say("Low stock shows only those.", 600);
    await sheet.getByRole("group", { name: "Show" }).getByRole("button", { name: /^Low stock/ }).click();
    await sleep(2200);
    await say("Ten bags of rice have come in. Ada opens the rice.", 600);
    await sheet.getByRole("button", { name: /Rice, 25kg bag/ }).first().click();
    const came = sheet.getByRole("textbox", { name: "Came in" });
    await came.waitFor();
    await sleep(1400);
    await say("How many came in, and a note if she wants one.", 400);
    await came.fill("");
    await came.pressSequentially("10", { delay: 150 });
    await type(sheet.getByLabel("Note"), "From Mama Nkechi, Mile 12", 50);
    await sleep(1000);
    await sheet.getByRole("button", { name: "Record stock in" }).click();
    await sheet.waitFor({ state: "detached", timeout: 15000 });
    await sleep(200);
    await say("The count goes up, and the chat says by how much.", 3600);
  });
  await page.context().close();
}

const PARTS = [
  { title: "How to: *add products and keep stock.*" },
  { title: "1. Add *a product.*" },
  { act: "howto10-add" },
  { title: "2. *Stock* in." },
  { act: "howto10-stock" },
];

// No argument: everything. "build": only join. Act names: record those, then join.
const ACTS = { add, stock };
const which = process.argv.slice(2);
if (which.length === 0) for (const f of Object.values(ACTS)) await f();
else for (const n of which.filter((n) => n !== "build")) await ACTS[n]();
console.log(build(SLUG, PARTS));
process.exit(0);
