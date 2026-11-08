// The chat tour (video/README.md, "Recording the real app"): the real app
// on the demo world, driven on a phone-sized screen, act by act. Each act
// is recorded to out/recording/<act>.mp4, and each timed action goes to
// out/recording/timings.jsonl.
//
//   node video/recording/tour.mjs            # every act, in order (on a freshly seeded world)
//   node video/recording/tour.mjs act3 act4  # some of them
//
// The acts change the world as they go (prices, orders, a deal), so a whole
// run wants a fresh one: serve-demo.sh without --keep, then seed.ts.
import { person, record, timed, sleep, BASE } from "./lib.mjs";
import { FILM } from "./order.mjs";


const acts = {};

// 1 · The Taskiem chat: the morning, what needs you, an approval, a question.
acts.act1 = async () => {
  const page = await person("ada");
  // The briefing is held back until 8am Lagos by design; let the real one through now.
  await page.route("**/v1/chat-briefing", async (r) => {
    const res = await r.fetch();
    const body = await res.json();
    await r.fulfill({ response: res, json: { ...body, ready: true } });
  });
  await page.goto(BASE + "/chat");
  await page.getByRole("button", { name: "Remind me later" }).click({ timeout: 4000 }).catch(() => {});
  await page.waitForTimeout(800);
  return record(page, "act1", async () => {
    await sleep(1500);
    await timed("Open the Taskiem chat", () => page.getByTestId("taskiem-chat").click(), () => page.getByTestId("taskiem-stream").waitFor());
    await sleep(1500);
    const brief = page.getByTestId("briefing-card");
    if (await brief.count()) {
      await brief.scrollIntoViewIfNeeded();
      await sleep(3000);
    }
    // What needs you: the cards, one by one.
    for (const name of ["Invoice INV-0002 is overdue", /^New order P-.* from Rivers Foods Ltd/, "Needs your OK: Pay Bayo Rice Mills ₦450,000"]) {
      const card = page.getByRole("article", { name }).first();
      await card.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
      await sleep(1600);
    }
    const ok = page.getByTestId("chat-approval").first();
    await timed("Open the approval", () => ok.getByRole("button", { name: /^Approve/ }).click(), () => page.getByText("Approve it?").waitFor());
    await sleep(2500);
    const sheet = page.locator("section,div[role=dialog]").filter({ hasText: "Approve it?" }).last();
    await sheet.getByRole("textbox").first().pressSequentially("Paid for the rice, thanks Tunde", { delay: 35 }).catch(() => {});
    await sleep(500);
    await timed("Approve a payment", () => sheet.getByRole("button", { name: "Approve", exact: true }).click(), () => page.getByText("Approve it?").waitFor({ state: "detached", timeout: 15000 }));
    await sleep(2500);
    await sleep(2200);
    // A question, answered in the chat.
    const box = page.getByLabel(/^Message|Ask Taskiem/).first();
    await box.click();
    await box.pressSequentially("how did we do this week?", { delay: 55 });
    await sleep(300);
    const before = await page.getByTestId("chart-card").count();
    await timed("Ask how the week went", () => page.keyboard.press("Enter"), () => page.waitForFunction((n) => document.querySelectorAll('[data-testid="chart-card"]').length > n, before, { timeout: 20000 }));
    await page.getByTestId("chart-card").last().scrollIntoViewIfNeeded();
    await sleep(3500);
  });
};

// 2 · Tools over the chat: a product's price, stock in, a typed request.
acts.act2 = async () => {
  const page = await person("ada");
  await page.goto(BASE + "/chat?with=taskiem");
  await page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});
  await page.getByTestId("taskiem-stream").waitFor();
  await page.waitForTimeout(1200);
  const sheet = page.getByTestId("tool-sheet");
  return record(page, "act2", async () => {
    await sleep(1200);
    await timed("Open the tools", () => page.getByTestId("tools-button").click(), () => page.getByRole("region", { name: "Tools" }).waitFor());
    await sleep(1400);
    await timed("Open Products", () => page.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Products/ }).click(), () => sheet.waitFor());
    await sleep(1800);
    const item = sheet.getByRole("button", { name: /Jollof rice/ }).first();
    await item.scrollIntoViewIfNeeded();
    await sleep(600);
    await timed("Open a product", () => item.click(), () => sheet.getByLabel(/Price in naira|Storefront price/).first().waitFor());
    await sleep(1500);
    const price = sheet.getByLabel(/Price in naira|Storefront price/).first();
    await price.click();
    await price.press("Control+a");
    await price.pressSequentially("4,500", { delay: 90 });
    await sleep(700);
    await timed("Save a price (to the diary line)", () => sheet.getByRole("button", { name: /^Save/ }).first().click(), () => page.getByTestId("chat-diary").filter({ hasText: "₦4,000 → ₦4,500" }).last().waitFor({ timeout: 15000 }));
    await sleep(3000);
    // Stock in, from the card that asked for it.
    const low = page.getByRole("article", { name: "Chapman is running low" });
    await low.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(1400);
    await timed("Open stock in from a card", () => low.getByRole("button", { name: /^Record stock in/ }).click(), () => sheet.waitFor());
    await sleep(1500);
    for (let i = 0; i < 4; i++) { await sheet.getByRole("button", { name: /^One more/ }).first().click(); await sleep(350); }
    await sleep(500);
    await timed("Record stock in", () => sheet.getByRole("button", { name: "Record stock in" }).last().click(), () => page.getByTestId("chat-diary").filter({ hasText: /Stock in: Chapman/ }).last().waitFor({ timeout: 15000 }));
    await sleep(3000);
    // A request in plain words becomes the tool, filled in.
    const box = page.getByLabel(/^Message|Ask Taskiem/).first();
    await box.click();
    await box.pressSequentially("change the price of moi moi to 1200", { delay: 50 });
    await timed("A typed request becomes a chip", async () => {}, () => page.getByTestId("request-chip").waitFor({ timeout: 10000 }));
    await sleep(1800);
    await timed("The chip opens the tool, filled in", () => page.getByTestId("request-chip").getByRole("button").first().click(), () => sheet.getByLabel(/Price in naira|Storefront price/).first().waitFor());
    await sleep(2000);
    await timed("Save the request", () => sheet.getByRole("button", { name: /^Save/ }).first().click(), () => page.getByTestId("chat-diary").filter({ hasText: "₦1,000 → ₦1,200" }).last().waitFor({ timeout: 15000 }));
    await sleep(3000);
  });
};

// 3 · A business chat: @ to tag a product, its live card, ordering from it.
acts.act3 = async () => {
  const page = await person("ada");
  await page.goto(BASE + "/chat");
  await page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});
  await page.waitForTimeout(1000);
  return record(page, "act3", async () => {
    await sleep(1000);
    const pane = page.getByTestId("chat-pane");
    await timed("Open a business chat", () => page.getByRole("link", { name: /^Bayo Rice Mills/ }).first().click(), () => pane.waitFor());
    await sleep(2200);
    const box = pane.getByLabel(/^Message/).first();
    await box.click();
    await box.pressSequentially("Need 20 more @ri", { delay: 70 });
    const picker = page.getByTestId("mention-list").first();
    await timed("The @ picker", async () => {}, () => picker.waitFor({ timeout: 8000 }));
    await sleep(1800);
    await picker.getByRole("button", { name: /Rice, 25kg bag/ }).first().click();
    await sleep(1200);
    await timed("Send a message with a product", () => page.keyboard.press("Enter"), () => pane.getByRole("article", { name: /Rice, 25kg bag/ }).last().waitFor({ timeout: 10000 }));
    await sleep(2500);
    // Order from it: Bayo's price list, opened from the card.
    const card = pane.getByRole("article", { name: /Rice, 25kg bag/ }).last();
    const order = page.getByTestId("order-sheet");
    await timed("Open the order sheet from a card", () => card.getByRole("button", { name: /^Order/ }).first().click(), () => order.waitFor());
    await sleep(1800);
    const more = order.getByRole("button", { name: /^One more Rice, 25kg bag/ }).first();
    for (let i = 0; i < 5; i++) { await more.click(); await sleep(260); }
    await sleep(900);
    await timed("Review the order", () => order.getByRole("button", { name: "Review order" }).click(), () => order.getByRole("button", { name: /Place order|Send order/ }).first().waitFor());
    await sleep(2200);
    const cards = await pane.getByTestId("chat-card").count();
    await timed("Place the order (its card in the chat)", () => order.getByRole("button", { name: /Place order|Send order/ }).first().click(), () => page.waitForFunction((n) => document.querySelectorAll('[data-testid="chat-pane"] [data-testid="chat-card"]').length > n, cards, { timeout: 15000 }));
    await sleep(3500);
  });
};

// 4 · Both sides of a trade: Bayo accepts Ada's order; Ada accepts Rivers'.
acts.act4 = async () => {
  const page = await person("bayo");
  await page.goto(BASE + "/chat");
  await page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});
  await page.waitForTimeout(1000);
  return record(page, "act4", async () => {
    await sleep(1000);
    const pane = page.getByTestId("chat-pane");
    await timed("Bayo opens Ada's chat", () => page.getByRole("link", { name: /^Ada's Kitchen/ }).first().click(), () => pane.waitFor());
    await sleep(1200);
    const card = pane.getByTestId("chat-card").filter({ has: page.getByRole("button", { name: /^Accept/ }) }).last();
    await card.waitFor();
    const ref = (await card.innerText()).match(/P-[A-Z0-9]{6}/)[0];
    await card.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(2200);
    await timed("Bayo accepts an order", () => card.getByRole("button", { name: /^Accept/ }).click(), () => page.getByText(`${ref} accepted`).first().waitFor({ timeout: 15000 }));
    await sleep(3500);
  });
};

acts.act4b = async () => {
  const page = await person("ada");
  await page.goto(BASE + "/chat");
  await page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});
  await page.waitForTimeout(1000);
  return record(page, "act4b", async () => {
    await sleep(800);
    const pane = page.getByTestId("chat-pane");
    await page.getByRole("link", { name: /^Bayo Rice Mills/ }).first().click();
    await pane.waitFor();
    const mine = pane.getByTestId("chat-card").last();
    await mine.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(3000);
    await page.getByRole("button", { name: "Back to the chats" }).click();
    await sleep(1000);
    await timed("Ada opens Rivers' chat", () => page.getByRole("link", { name: /^Rivers Foods/ }).first().click(), () => pane.waitFor());
    await sleep(1000);
    const card = pane.getByTestId("chat-card").filter({ has: page.getByRole("button", { name: /^Accept/ }) }).last();
    await card.waitFor();
    const ref = (await card.innerText()).match(/P-[A-Z0-9]{6}/)[0];
    await card.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(2200);
    await timed("Ada accepts Rivers' order", () => card.getByRole("button", { name: /^Accept/ }).click(), () => page.getByText(`${ref} accepted`).first().waitFor({ timeout: 15000 }));
    await sleep(3500);
  });
};

// 5 · Staff: Tunde the cashier sees his role's tools and sells.
acts.act5 = async () => {
  const page = await person("tunde");
  await page.goto(BASE + "/chat?with=taskiem");
  await page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});
  await page.getByTestId("taskiem-stream").waitFor();
  await page.waitForTimeout(1200);
  return record(page, "act5", async () => {
    await sleep(1500);
    await page.getByTestId("tools-button").click();
    await page.getByRole("region", { name: "Tools" }).waitFor();
    await sleep(2200);
    await timed("Open Sell", () => page.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Sell/ }).click(), () => page.getByTestId("sell-row").first().waitFor());
    await sleep(1200);
    const search = page.getByPlaceholder(/Search what you sell/);
    await search.pressSequentially("moi", { delay: 90 });
    await sleep(800);
    for (let i = 0; i < 3; i++) { await page.getByRole("button", { name: "One more Moi moi" }).click(); await sleep(300); }
    await search.fill("");
    await search.pressSequentially("meat", { delay: 90 });
    await sleep(700);
    for (let i = 0; i < 2; i++) { await page.getByRole("button", { name: "One more Meat pie" }).click(); await sleep(300); }
    await sleep(900);
    await page.getByTestId("sell-bag").getByRole("button", { name: "Next" }).click();
    await sleep(1800);
    const cash = page.getByRole("button", { name: /^Cash/ }).or(page.getByRole("radio", { name: /^Cash/ })).first();
    await cash.click().catch(() => {});
    await sleep(900);
    await timed("Record a sale (to the diary line)", () => page.getByRole("button", { name: "Record the sale" }).first().click(), () => page.getByTestId("chat-diary").filter({ hasText: "₦6,000" }).last().waitFor({ timeout: 15000 }));
    await sleep(3500);
  });
};

// 6 · Dark: picked on the Account page, then the same chat at night.
acts.act6 = async () => {
  const page = await person("ada");
  await page.goto(BASE + "/account");
  await page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});
  const theme = page.getByRole("radiogroup", { name: "Theme" });
  await theme.waitFor();
  await theme.evaluate((el) => el.scrollIntoView({ block: "center" }));
  await page.waitForTimeout(800);
  return record(page, "act6", async () => {
    await sleep(1500);
    await timed("Switch to dark", () => theme.getByRole("radio", { name: "Dark" }).click(), () => page.waitForFunction(() => document.documentElement.dataset.theme === "dark"));
    await sleep(1800);
    await page.goto(BASE + "/chat");
    await sleep(1800);
    await page.getByTestId("taskiem-chat").click();
    await page.getByTestId("taskiem-stream").waitFor();
    await sleep(1200);
    await page.getByTestId("chart-card").last().evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(2600);
    await page.getByTestId("tools-button").click();
    await sleep(1600);
    await page.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Products/ }).click();
    await sleep(2600);
    await page.getByTestId("tool-sheet").getByRole("button", { name: "Close" }).first().click().catch(() => {});
    await sleep(1200);
    // Back to light, as the business works by day.
    await page.goto(BASE + "/account");
    await theme.evaluate((el) => el.scrollIntoView({ block: "center" }));
    await sleep(900);
    await theme.getByRole("radio", { name: "Light" }).click();
    await sleep(1500);
  });
};

// 7 · Explore: a business asks to link; find one you don't trade with yet.
acts.act7 = async () => {
  const page = await person("ada");
  await page.goto(BASE + "/chat");
  await page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});
  await page.waitForTimeout(1000);
  return record(page, "act7", async () => {
    await sleep(1500);
    await timed("Accept a link request", () => page.getByRole("button", { name: "Accept Kola's Bakery" }).click(), () => page.getByRole("link", { name: /^Kola's Bakery/ }).first().waitFor({ timeout: 15000 }));
    await sleep(2500);
    await page.getByTestId("taskiem-chat").click();
    await page.getByTestId("taskiem-stream").waitFor();
    await sleep(1000);
    const box = page.getByLabel(/^Message|Ask Taskiem/).first();
    await box.click();
    await box.pressSequentially("@suya", { delay: 90 });
    const picker = page.getByTestId("mention-list").first();
    await picker.waitFor();
    await sleep(1800);
    await timed("A business's Explore card from @", () => picker.getByRole("button", { name: /Suya/ }).first().click(), () => page.getByRole("button", { name: /^Connect/ }).first().waitFor({ timeout: 10000 }));
    await sleep(3000);
    await timed("Connect", () => page.getByRole("button", { name: /^Connect/ }).first().click(), () => page.getByText("Requested", { exact: true }).first().waitFor({ timeout: 10000 }));
    await sleep(3000);
  });
};

// 8 · Deals. a: Ada's quantity price and lowest price.
acts.act8a = async () => {
  const page = await person("ada");
  await page.goto(BASE + "/storefront/price-lists");
  await page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});
  await page.getByRole("button", { name: /^Items of/ }).first().waitFor();
  await page.waitForTimeout(800);
  return record(page, "act8a", async () => {
    await sleep(1500);
    await page.getByRole("button", { name: /^Items of/ }).first().click();
    const modal = page.getByRole("dialog");
    await sleep(1200);
    const add = modal.getByRole("button", { name: "+ Lower price for more of Small chops pack" });
    await add.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(1300);
    await add.click();
    await sleep(700);
    const brk = modal.getByRole("group", { name: "Small chops pack: quantity price 1" });
    await brk.getByLabel("From").pressSequentially("50", { delay: 120 });
    await brk.getByLabel(/^Price/).pressSequentially("2000", { delay: 120 });
    await sleep(1200);
    await timed("Save a quantity price", () => modal.getByRole("button", { name: "Save" }).click(), () => modal.waitFor({ state: "detached" }));
    await sleep(1500);
    // The lowest price, in the product's sheet in the chat.
    await page.goto(BASE + "/chat?with=taskiem");
    await page.getByTestId("taskiem-stream").waitFor();
    await sleep(1000);
    await page.getByTestId("tools-button").click();
    await sleep(900);
    await page.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Products/ }).click();
    const sheet = page.getByTestId("tool-sheet");
    await sleep(1000);
    await sheet.getByPlaceholder(/Search/).first().pressSequentially("small", { delay: 90 });
    await sleep(800);
    await sheet.getByRole("button", { name: /Small chops pack/ }).first().click();
    await sleep(1200);
    const floor = sheet.getByLabel("Lowest price in naira");
    await floor.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(900);
    await floor.click();
    await floor.pressSequentially("1900", { delay: 120 });
    await sleep(1500);
    await timed("Save a lowest price", () => sheet.getByRole("button", { name: /^Save/ }).first().click(), () => page.getByText("Saved", { exact: true }).first().waitFor({ timeout: 15000 }));
    await sleep(3000);
  });
};

const chatWith = async (page, name) => {
  await page.goto(BASE + "/chat");
  await page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});
  await page.getByRole("link", { name: new RegExp("^" + name) }).first().waitFor();
  await page.waitForTimeout(800);
};
const dealCard = (page) => page.getByTestId("chat-pane").getByRole("article", { name: /^Order · / }).last();

// b: Rivers orders 50 packs and makes an offer for the whole order.
acts.act8b = async () => {
  const page = await person("rivers");
  await chatWith(page, "Ada's Kitchen");
  return record(page, "act8b", async () => {
    await sleep(1000);
    const pane = page.getByTestId("chat-pane");
    await page.getByRole("link", { name: /^Ada's Kitchen/ }).first().click();
    await pane.waitFor();
    await sleep(1500);
    await pane.getByRole("button", { name: "Open tools" }).click();
    await sleep(1300);
    const order = page.getByTestId("order-sheet");
    await timed("Open a supplier's price list", () => page.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Order/ }).first().click(), () => order.waitFor());
    await sleep(1200);
    await order.getByPlaceholder(/Search/).pressSequentially("small", { delay: 90 });
    await sleep(900);
    const more = order.getByRole("button", { name: /^One more Small chops pack/ });
    await more.click();
    await sleep(600);
    for (let i = 0; i < 30; i++) { await more.click(); await sleep(110); }
    await sleep(1800);
    await order.getByRole("button", { name: "Review order" }).click();
    await sleep(2000);
    await order.getByRole("button", { name: "Make an offer" }).click();
    await sleep(1000);
    await order.getByLabel("Your price for the whole order").pressSequentially("88k", { delay: 150 });
    await sleep(1800);
    await timed("Send an offer (its card in the chat)", () => order.getByRole("button", { name: "Send offer" }).click(), () => dealCard(page).waitFor({ timeout: 15000 }));
    await sleep(1200);
    await dealCard(page).evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(3000);
  });
};

// c: Tunde, for Ada's Kitchen, counters.
acts.act8c = async () => {
  const page = await person("tunde");
  await chatWith(page, "Rivers Foods");
  return record(page, "act8c", async () => {
    await sleep(1000);
    await page.getByRole("link", { name: /^Rivers Foods/ }).first().click();
    const card = dealCard(page);
    await card.waitFor();
    await card.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(2800);
    await card.getByRole("button", { name: /^Counter/ }).click();
    await sleep(1000);
    await card.getByLabel(/Your price for the whole order|Your counter/).pressSequentially("97k", { delay: 150 });
    await sleep(500);
    await card.getByLabel(/A word with it/).pressSequentially("Best I can do for 50", { delay: 45 }).catch(() => {});
    await sleep(1200);
    await timed("Send a counter", () => card.getByRole("button", { name: /^Send counter/ }).click(), () => page.getByText(/97,000/).last().waitFor({ timeout: 15000 }));
    await sleep(3500);
  });
};

// d: Rivers counters again, under Ada's lowest price.
acts.act8d = async () => {
  const page = await person("rivers");
  await chatWith(page, "Ada's Kitchen");
  return record(page, "act8d", async () => {
    await sleep(1000);
    await page.getByRole("link", { name: /^Ada's Kitchen/ }).first().click();
    const card = dealCard(page);
    await card.waitFor();
    await card.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(2800);
    await card.getByRole("button", { name: /^Counter/ }).click();
    await sleep(1000);
    await card.getByLabel(/Your counter|Your price for the whole order/).pressSequentially("93,000", { delay: 130 });
    await sleep(1200);
    await timed("Counter again", () => card.getByRole("button", { name: /^Send counter/ }).click(), () => page.getByText(/93,000/).last().waitFor({ timeout: 15000 }));
    await sleep(3500);
  });
};

// e: Tunde accepts: under the lowest price, so it waits for Ada.
acts.act8e = async () => {
  const page = await person("tunde");
  await chatWith(page, "Rivers Foods");
  return record(page, "act8e", async () => {
    await sleep(1000);
    await page.getByRole("link", { name: /^Rivers Foods/ }).first().click();
    const card = dealCard(page);
    await card.waitFor();
    await card.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(3000);
    await timed("Accept under the lowest price (waits for the owner)", () => card.getByRole("button", { name: /^Accept ₦93,000/ }).click(), () => page.getByText(/waits for the owner|Needs .*OK|owner's OK/).first().waitFor({ timeout: 15000 }));
    await sleep(4000);
  });
};

// f: Ada agrees from her Taskiem chat.
acts.act8f = async () => {
  const page = await person("ada");
  await page.goto(BASE + "/chat");
  await page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});
  await page.waitForTimeout(800);
  return record(page, "act8f", async () => {
    await sleep(1000);
    await page.getByTestId("taskiem-chat").click();
    await page.getByTestId("taskiem-stream").waitFor();
    const ask = page.getByTestId("chat-alert").filter({ hasText: /would agree/ }).last();
    await ask.waitFor({ timeout: 10000 });
    await ask.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(3500);
    await timed("Agree a deal", () => ask.getByRole("button", { name: /^(Agree|Approve)/ }).first().click(), () => page.getByText(/Agreed ₦93,000/).first().waitFor({ timeout: 15000 }));
    await sleep(4000);
  });
};

const wanted = process.argv.slice(2);
const run = wanted.length ? wanted : FILM.flatMap((f) => f.acts ?? []);
for (const a of run) {
  const t = Date.now();
  try {
    console.log(a, await acts[a](), `${((Date.now() - t) / 1000).toFixed(0)} s`);
  } catch (e) {
    console.log(a, "FAILED:", e.message.split("\n").slice(0, 3).join(" | "));
  }
}
process.exit(0);
