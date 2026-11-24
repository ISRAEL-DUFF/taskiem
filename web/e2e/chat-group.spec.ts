import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Make one product with sizes and colours (docs/spec/chat-categories-and-
// variants.md, phase 5): in the Products sheet the owner ticks three
// look-alike items, Taskiem proposes the product's name, its choices and
// each item's values from their names, the owner corrects one, and the
// grouping is sent. A product already in sizes cannot be ticked. serve.sh
// keeps no catalogue, so stock is answered here and what is sent is
// checked (the API is tested in Go, api/stock_group_test.go).
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () =>
  String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 200) => ({
  status,
  contentType: "application/json",
  body: JSON.stringify(body),
});

async function owner(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: `Zuri Atelier ${letters()}`,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@group.test`,
      name: "Zuri Owner",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
  await page.request.put("/v1/settings/chat-tools", {
    headers: H,
    data: { chat_tools: true },
  });
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.evaluate((t) => {
      if (t === "dark") document.documentElement.dataset.theme = "dark";
      else delete document.documentElement.dataset.theme;
    }, scheme);
    await page.waitForTimeout(300);
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
}

const item = (id: string, name: string, stock: number, price: number) => ({
  id,
  name,
  available: true,
  stock,
  unit: "piece",
  price_kobo: price,
  category: "Kaftans",
});
const items = [
  item("a", "Ankara Kaftan S Blue", 4, 2_500_000),
  item("b", "Ankara Kaftan M Blue", 6, 2_500_000),
  item("c", "Ankara Kaftan M Red", 3, 2_800_000),
  item("r", "Rice", 9, 100_000),
  {
    ...item("k", "Air Max 90", 0, 4_500_000),
    choices: [{ name: "Size", values: ["42"] }],
  },
  {
    ...item("k42", "Air Max 90 – 42", 2, 4_500_000),
    variant_of: "k",
    variant: { Size: "42" },
  },
];

for (const viewport of [
  { width: 390, height: 844 },
  { width: 360, height: 780 },
]) {
  test(`three look-alike items made one product at ${viewport.width}`, async ({
    browser,
  }) => {
    const page = await (await browser.newContext({ viewport })).newPage();
    await owner(page);
    const sent: { body: unknown; key: string | null }[] = [];
    await page.route("**/v1/stock", (r) =>
      r.fulfill(json({ keeps_stock: true, catalogue: "products", items })),
    );
    await page.route("**/v1/stock/group", (r: Route) => {
      sent.push({
        body: r.request().postDataJSON(),
        key: r.request().headers()["idempotency-key"] ?? null,
      });
      return r.fulfill(
        json({
          item: { ...items[0], id: "p", name: "Ankara Kaftan" },
          variants: [],
        }),
      );
    });
    await page.goto("/chat?with=taskiem");
    const box = page.getByTestId("taskiem-pane").getByLabel("Message");
    await box.fill("/pro");
    await expect(
      page.getByRole("region", { name: "Tools" }).getByRole("button"),
    ).toHaveCount(1);
    await box.press("Enter");
    await page.keyboard.press("Enter");
    const sheet = page.getByRole("dialog", { name: /Products/ });
    await sheet
      .getByRole("button", { name: "Make one product with sizes and colours" })
      .click();
    await expect(
      sheet.getByRole("button", { name: "Choose at least two" }),
    ).toBeDisabled();
    // One already in sizes cannot be ticked; the three kaftans can.
    await expect(
      sheet.getByRole("checkbox", { name: /Air Max 90/ }).first(),
    ).toBeDisabled();
    for (const n of [
      "Ankara Kaftan S Blue",
      "Ankara Kaftan M Blue",
      "Ankara Kaftan M Red",
    ])
      await sheet.getByRole("checkbox", { name: new RegExp(n) }).check();
    await expect(
      page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).resolves.toBe(true);
    await axeBoth(
      page,
      "the Products sheet, choosing items to make one product",
    );
    await sheet.getByRole("button", { name: "Make one product of 3" }).click();

    // The proposal: the name they share, Size and Colour, each one's values.
    const form = page.getByRole("dialog", { name: "Make one product" });
    await expect(
      form.getByRole("textbox", { name: "The product's name" }),
    ).toHaveValue("Ankara Kaftan");
    await expect(form.getByRole("textbox", { name: /Choice 1/ })).toHaveValue(
      "Size",
    );
    await expect(form.getByRole("textbox", { name: /Choice 2/ })).toHaveValue(
      "Colour",
    );
    const red = form.getByRole("group", { name: "Ankara Kaftan M Red" });
    await expect(red.getByRole("textbox", { name: "Size" })).toHaveValue("M");
    await expect(red.getByRole("textbox", { name: "Colour" })).toHaveValue(
      "Red",
    );
    // Two the same is refused before it is sent; corrected, it goes.
    await red.getByRole("textbox", { name: "Colour" }).fill("Blue");
    await expect(form.getByRole("status")).toHaveText(
      "Two items have the same size and colour: change one.",
    );
    await red.getByRole("textbox", { name: "Colour" }).fill("Wine");
    await expect(
      page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).resolves.toBe(true);
    await axeBoth(page, "Make one product");
    await form.getByRole("button", { name: "Make one product" }).click();
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0]?.key).toBeTruthy();
    expect(sent[0]?.body).toEqual({
      name: "Ankara Kaftan",
      choices: ["Size", "Colour"],
      items: [
        { item_id: "a", variant: { Size: "S", Colour: "Blue" } },
        { item_id: "b", variant: { Size: "M", Colour: "Blue" } },
        { item_id: "c", variant: { Size: "M", Colour: "Wine" } },
      ],
    });
    // Saved: the sheet closes (the diary line is the server's, told by
    // the sizes save it goes through; tested in Go).
    await expect(form).toBeHidden();
  });
}
