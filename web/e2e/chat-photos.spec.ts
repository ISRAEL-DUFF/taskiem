import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Product photos from the chat (docs/spec/chat-categories-and-variants.md,
// Part C): Add product takes photos from the camera (Chromium's fake one),
// the computer's pictures and a link, held until the product exists and
// then sent, a large one made small first; one that does not go leaves the
// product added and the form open to send it again. The product form has
// Make main and Remove; the Tables tool's photo column takes photos, not
// text. serve.sh keeps no photos, so the photo calls are answered here and
// what is sent is checked (the link's fetch is tested in Go).
test.use({
  bypassCSP: true,
  launchOptions: {
    args: [
      "--use-fake-device-for-media-stream",
      "--use-fake-ui-for-media-stream",
    ],
  },
});

const H = { "X-Taskiem-Request": "1" };
const letters = () =>
  String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 200) => ({
  status,
  contentType: "application/json",
  body: JSON.stringify(body),
});

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: `Zuri Atelier ${letters()}`,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@photo.test`,
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

const fits = async (page: Page) =>
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);

/** A camera-sized picture made in the page, over the 10 MB upload limit
 * as it is (noise does not compress), as a phone's photo can be. */
async function bigPicture(page: Page): Promise<Buffer> {
  const b64 = await page.evaluate(async () => {
    const c = document.createElement("canvas");
    c.width = 2600;
    c.height = 1700;
    const g = c.getContext("2d");
    if (!g) throw new Error("no canvas");
    const img = g.createImageData(c.width, c.height);
    for (let i = 0; i < img.data.length; i += 4) {
      img.data[i] = (Math.random() * 256) | 0;
      img.data[i + 1] = (Math.random() * 256) | 0;
      img.data[i + 2] = (Math.random() * 256) | 0;
      img.data[i + 3] = 255;
    }
    g.putImageData(img, 0, 0);
    const blob = await new Promise<Blob | null>((r) =>
      c.toBlob(r, "image/png"),
    );
    if (!blob) throw new Error("no picture");
    const bytes = new Uint8Array(await blob.arrayBuffer());
    let s = "";
    for (let i = 0; i < bytes.length; i += 0x8000)
      s += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
    return btoa(s);
  });
  return Buffer.from(b64, "base64");
}

type Sent = {
  method: string;
  path: string;
  type: string;
  size: number;
  body?: unknown;
};

/** The stock and the photo calls, answered and remembered. */
async function answer(
  page: Page,
  opts: { linkFails?: () => boolean; slow?: boolean } = {},
) {
  const sent: Sent[] = [];
  const remember = (r: Route) => {
    const q = r.request();
    const u = new URL(q.url());
    const type = q.headers()["content-type"] ?? "";
    const buf = q.postDataBuffer();
    sent.push({
      method: q.method(),
      path: u.pathname + u.search,
      type,
      size: buf?.length ?? 0,
      body:
        type.includes("json") && buf ? JSON.parse(buf.toString()) : undefined,
    });
  };
  await page.route("**/v1/stock", (r) =>
    r.fulfill(
      json({
        keeps_stock: true,
        catalogue: "products",
        items: [
          {
            id: "k1",
            name: "Air Max 90",
            available: true,
            stock: 4,
            price_kobo: 4_500_000,
          },
        ],
      }),
    ),
  );
  await page.route(
    "**/v1/stock/items",
    (r) => (
      remember(r),
      r.fulfill(
        json(
          { item: { id: "k9", name: "Slides", available: true, stock: 2 } },
          201,
        ),
      )
    ),
  );
  await page.route("**/v1/storefront/catalogue/refresh", (r) =>
    r.fulfill(json({ items: [] })),
  );
  await page.route("**/v1/storefront/catalogue/rows/**", async (r) => {
    remember(r);
    // A slow line, so what is shown while photos go can be seen.
    if (opts.slow) await new Promise((x) => setTimeout(x, 600));
    const last = sent[sent.length - 1];
    if (
      opts.linkFails?.() &&
      last &&
      (last.body as { url?: string } | undefined)?.url
    )
      return r.fulfill(
        json(
          { error: "invalid request: that link is not a picture we can use" },
          400,
        ),
      );
    return r.request().method() === "POST"
      ? r.fulfill(json({ id: "m1" }))
      : r.fulfill({ status: 204 });
  });
  return sent;
}

for (const viewport of [
  { width: 390, height: 844 },
  { width: 360, height: 780 },
]) {
  test(`a new product with a photo from the camera, the computer and a link at ${viewport.width}`, async ({
    browser,
  }) => {
    const page = await (await browser.newContext({ viewport })).newPage();
    await signUp(page);
    const sent = await answer(page, { slow: true });
    await page.goto("/chat?with=taskiem");
    const box = page.getByTestId("taskiem-pane").getByLabel("Message");
    await box.fill("/pro");
    await box.press("Enter");
    await page.keyboard.press("Enter");
    const sheet = page.getByRole("dialog", { name: /Products/ });
    await sheet.getByRole("button", { name: "Add product" }).click();
    const form = page.getByRole("dialog", { name: /Add product/ });
    const ways = form.getByRole("group", { name: "Add a photo" });
    await expect(ways.getByRole("button")).toHaveText([
      "Take a photo",
      "From your computer",
      "Paste a link",
    ]);
    await fits(page);
    await axeBoth(page, "Add product with its photos");

    // The camera (Chromium's fake one): the photo is held, not yet sent.
    await ways.getByRole("button", { name: "Take a photo" }).click();
    const cam = page.getByRole("dialog", { name: "Take a photo" });
    await cam.getByRole("button", { name: /Take photo/ }).click();
    await expect(cam).toHaveCount(0);
    // The computer's pictures: one over the 10 MB limit as it is, which
    // goes because it is made small first.
    const big = await bigPicture(page);
    expect(big.length).toBeGreaterThan(10 << 20);
    await form.getByTestId("photo-files").setInputFiles({
      name: "slides.png",
      mimeType: "image/png",
      buffer: big,
    });
    // A link: checked before it is held.
    await ways.getByRole("button", { name: "Paste a link" }).click();
    const link = form.getByRole("textbox", { name: /Link to a picture/ });
    await link.fill("slides.jpg");
    await form.getByRole("button", { name: "Add this photo" }).click();
    await expect(
      form.getByText("That is not a link: it starts with https://"),
    ).toBeVisible();
    await link.fill("https://supplier.test/slides.jpg");
    await form.getByRole("button", { name: "Add this photo" }).click();
    const waiting = form
      .getByRole("list", { name: "Photos to add" })
      .getByRole("listitem");
    await expect(waiting).toHaveCount(3);
    await expect(waiting.nth(2)).toContainText("supplier.test");
    expect(sent.filter((x) => x.path.includes("/photo"))).toEqual([]);
    await fits(page);
    await axeBoth(page, "Add product with photos waiting");
    // One taken out again, then put back.
    await form.getByRole("button", { name: "Remove photo 3" }).click();
    await expect(waiting).toHaveCount(2);
    await ways.getByRole("button", { name: "Paste a link" }).click();
    await link.fill("https://supplier.test/slides.jpg");
    await link.press("Enter");
    await expect(waiting).toHaveCount(3);

    await form.getByRole("textbox", { name: "Name" }).fill("Slides");
    await form.getByRole("textbox", { name: /Price/ }).fill("15000");
    await form.getByRole("button", { name: "Add product" }).click();
    // While they go, the form says which of how many.
    await expect(form.getByRole("status")).toContainText(
      /Sending photo [12] of 3/,
    );
    await expect(form).toHaveCount(0, { timeout: 10_000 });
    const photos = sent.filter((x) => x.path.includes("/photo"));
    expect(photos.map((p) => p.path)).toEqual([
      "/v1/storefront/catalogue/rows/k9/photo",
      "/v1/storefront/catalogue/rows/k9/photo?add=1",
      "/v1/storefront/catalogue/rows/k9/photo?add=1",
    ]);
    expect(photos[0]?.type).toBe("image/jpeg");
    // The large one went made small, as a JPEG.
    expect(photos[1]?.type).toBe("image/jpeg");
    expect(photos[1]?.size).toBeLessThan(10 << 20);
    expect(photos[2]?.body).toEqual({
      url: "https://supplier.test/slides.jpg",
    });
  });
}

test("a photo that does not go leaves the product added, to send again", async ({
  browser,
}) => {
  const page = await (
    await browser.newContext({ viewport: { width: 390, height: 844 } })
  ).newPage();
  await signUp(page);
  let failing = true;
  const sent = await answer(page, { linkFails: () => failing });
  await page.goto("/chat?with=taskiem");
  const box = page.getByTestId("taskiem-pane").getByLabel("Message");
  await box.fill("/pro");
  await box.press("Enter");
  await page.keyboard.press("Enter");
  await page
    .getByRole("dialog", { name: /Products/ })
    .getByRole("button", { name: "Add product" })
    .click();
  const form = page.getByRole("dialog", { name: /Add product/ });
  await form.getByRole("button", { name: "Paste a link" }).click();
  await form
    .getByRole("textbox", { name: /Link to a picture/ })
    .fill("https://supplier.test/page");
  await form.getByRole("button", { name: "Add this photo" }).click();
  await form.getByRole("textbox", { name: "Name" }).fill("Slides");
  await form.getByRole("textbox", { name: /Price/ }).fill("15000");
  await form.getByRole("button", { name: "Add product" }).click();
  await expect(
    form.getByText("Slides is added, but a photo did not go."),
  ).toBeVisible();
  await expect(
    form.getByText(
      "supplier.test: invalid request: that link is not a picture we can use",
    ),
  ).toBeVisible();
  await axeBoth(page, "a photo that did not go");
  // The product is not added twice: only the photo goes again.
  failing = false;
  await form.getByRole("button", { name: "Send the photos again" }).click();
  await expect(form).toHaveCount(0, { timeout: 10_000 });
  expect(sent.filter((x) => x.path === "/v1/stock/items")).toHaveLength(1);
  expect(
    sent.filter((x) => x.path.includes("/photo")).map((x) => x.path),
  ).toEqual([
    "/v1/storefront/catalogue/rows/k9/photo",
    "/v1/storefront/catalogue/rows/k9/photo",
  ]);
});

test("a product's photos: make one the main photo, remove one, add one", async ({
  browser,
}) => {
  const page = await (
    await browser.newContext({ viewport: { width: 390, height: 844 } })
  ).newPage();
  await signUp(page);
  const sent = await answer(page);
  const img = {
    "320": "https://cdn.test/a-320.jpg",
    "800": "https://cdn.test/a-800.jpg",
  };
  await page.route("**/v1/storefront/catalogue", (r) =>
    r.fulfill(
      json({
        items: [
          {
            id: "k1",
            name: "Air Max 90",
            available: true,
            photo: img,
            gallery: [img, img],
            photo_ids: ["m1", "m2", "m3"],
          },
        ],
      }),
    ),
  );
  await page.route("**/v1/storefront/image?**", (r) =>
    r.fulfill({
      status: 200,
      contentType: "image/svg+xml",
      body: "<svg xmlns='http://www.w3.org/2000/svg' width='10' height='10'/>",
    }),
  );
  await page.goto("/chat?with=taskiem");
  const box = page.getByTestId("taskiem-pane").getByLabel("Message");
  await box.fill("/pro");
  await box.press("Enter");
  await page.keyboard.press("Enter");
  await page
    .getByRole("dialog", { name: /Products/ })
    .getByRole("button", { name: /Air Max 90/ })
    .click();
  const form = page.getByRole("dialog", { name: /Air Max 90/ });
  const photos = form.getByRole("list", { name: "Photos of Air Max 90" });
  await expect(photos.getByRole("img")).toHaveCount(3);
  await axeBoth(page, "a product's photos");
  await fits(page);
  await form.getByRole("button", { name: /^Make main\s*, photo 3$/ }).click();
  await form.getByRole("button", { name: /^Remove\s*, photo 2$/ }).click();
  await form.getByRole("button", { name: "Paste a link" }).click();
  await form
    .getByRole("textbox", { name: /Link to a picture/ })
    .fill("https://supplier.test/side.jpg");
  await form.getByRole("button", { name: "Add this photo" }).click();
  await expect
    .poll(() =>
      sent
        .filter((x) => x.path.includes("/photo"))
        .map((x) => `${x.method} ${x.path}`),
    )
    .toEqual([
      "PUT /v1/storefront/catalogue/rows/k1/photo/main?media=m3",
      "DELETE /v1/storefront/catalogue/rows/k1/photo?media=m2",
      "POST /v1/storefront/catalogue/rows/k1/photo?add=1",
    ]);
});

test("the Tables tool's photo column takes photos, not a web address", async ({
  browser,
}) => {
  const page = await (
    await browser.newContext({ viewport: { width: 390, height: 844 } })
  ).newPage();
  await signUp(page);
  const sent = await answer(page);
  const table = {
    id: "t-products",
    name: "products",
    title: "Products",
    row_count: 0,
    columns: [
      { name: "name", type: "text", required: true },
      { name: "photo", type: "text" },
      { name: "price", type: "number" },
    ],
  };
  await page.route("**/v1/tables", (r) => r.fulfill(json({ tables: [table] })));
  await page.route("**/v1/tables/t-products/rows?**", (r) =>
    r.fulfill(
      json({
        rows: [{ id: 3, name: "Air Max 90", photo: "m1,m2", price: 45000 }],
      }),
    ),
  );
  await page.route("**/v1/tables/t-products/rows", (r) => {
    const q = r.request();
    sent.push({
      method: q.method(),
      path: "/v1/tables/t-products/rows",
      type: "",
      size: 0,
      body: q.postDataJSON(),
    });
    return r.fulfill(json({ rows: [{ id: 7, name: "Slides" }] }, 201));
  });
  await page.route("**/v1/storefront", (r) =>
    r.fulfill(
      json({
        slug: "zuri",
        draft: { name: "Zuri" },
        status: "draft",
        catalogue_table: "t-products",
        catalogue_columns: { name: "name", photo: "photo", price: "price" },
      }),
    ),
  );
  await page.goto("/chat?with=taskiem");
  const box = page.getByTestId("taskiem-pane").getByLabel("Message");
  await box.fill("/tab");
  await box.press("Enter");
  await page.keyboard.press("Enter");
  await page
    .getByRole("dialog", { name: /Tables/ })
    .getByRole("button", { name: /Products/ })
    .click();
  const sheet = page.getByRole("dialog", { name: /Products/ });
  // The list says a row's photos as a count, not their references.
  await expect(sheet.getByRole("button", { name: /Air Max 90/ })).toContainText(
    "2 photos",
  );
  await expect(
    sheet.getByRole("button", { name: /Air Max 90/ }),
  ).not.toContainText("m1,m2");
  await sheet.getByRole("button", { name: "Add a row" }).click();
  await expect(sheet.getByRole("textbox", { name: /photo/i })).toHaveCount(0);
  await expect(sheet.getByRole("group", { name: "Add a photo" })).toBeVisible();
  await axeBoth(page, "a products row's photo");
  await fits(page);
  await sheet.getByRole("textbox", { name: /^name/i }).fill("Slides");
  await sheet.getByRole("button", { name: "Paste a link" }).click();
  await sheet
    .getByRole("textbox", { name: /Link to a picture/ })
    .fill("https://supplier.test/slides.jpg");
  await sheet.getByRole("button", { name: "Add this photo" }).click();
  await sheet.getByRole("button", { name: "Add the row" }).click();
  await expect
    .poll(() => sent.map((x) => `${x.method} ${x.path}`))
    .toEqual([
      "POST /v1/tables/t-products/rows",
      "POST /v1/storefront/catalogue/rows/7/photo",
    ]);
  // The photo is not written into the row as text.
  expect((sent[0]?.body as { row: Record<string, unknown> }).row).toEqual({
    name: "Slides",
  });
});
