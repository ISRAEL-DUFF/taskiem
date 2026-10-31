import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Files in the conversation between linked businesses (business links
// L3, §5.5), on a phone's width, passing axe in both themes. Chromium's
// fake microphone records the voice note.
test.use({
  bypassCSP: true,
  viewport: { width: 390, height: 844 },
  launchOptions: { args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream"] },
});

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@chat.test`, name: `${tenant.split(" ")[0]} Owner`, password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
}

// A photo, a PDF and a voice note from the composer (L3, §5.5): the e2e
// server has no file storage, so the two file routes are answered here;
// the API's own test checks each business keeps its own copy.
test("a photo with a caption, a PDF and a voice note", async ({ browser }) => {
  const stamp = letters();
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, `Eko Sacks ${stamp}`);
  const ctx = await browser.newContext({ permissions: ["microphone"] });
  const buyer = await ctx.newPage();
  await signUp(buyer, `Funke Foods ${stamp}`);
  expect((await seller.request.put("/v1/link-settings", { headers: H, data: { findable: true } })).status()).toBe(200);
  const found = await (await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`eko sacks ${stamp}`)}`, { headers: H })).json();
  expect((await buyer.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "supplier" } })).status()).toBe(201);
  const link = (await (await seller.request.get("/v1/links", { headers: H })).json()).links[0].id as string;
  expect((await seller.request.post(`/v1/links/${link}/accept`, { headers: H })).status()).toBe(200);

  // The file routes, answered as the API answers them.
  const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==", "base64");
  const sent: string[] = [];
  let n = 0;
  await buyer.route(`**/v1/link-chats/${link}/files`, async (route) => {
    const body = route.request().postDataBuffer()?.toString("latin1") ?? "";
    sent.push(body);
    const caption = /name="text"\r\n\r\n([^\r]*)/.exec(body)?.[1];
    const attachment = body.includes("Content-Type: application/pdf")
      ? { kind: "file", type: "application/pdf", size: 2048, name: "waybill.pdf" }
      : body.includes("Content-Type: audio/")
        ? { kind: "voice", type: "audio/webm", size: 4096, name: "voice-note.webm" }
        : { kind: "photo", type: "image/png", size: png.length, name: "bags.png" };
    n += 1;
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({
        message: { id: `0192e2e0-0000-7000-8000-00000000000${n}`, link, mine: true, kind: "text", body: caption, attachment, created_at: new Date().toISOString() },
      }),
    });
  });
  await buyer.route(`**/v1/link-chats/${link}/files/*`, (route) => route.fulfill({ status: 200, contentType: "image/png", body: png }));

  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await expect(pane.getByRole("button", { name: "Record a voice note" })).toBeVisible();

  // A photo with a caption.
  await pane.getByTestId("chat-file").setInputFiles({ name: "bags.png", mimeType: "image/png", buffer: png });
  await expect(pane.getByTestId("chat-attaching")).toContainText("Photo: bags.png");
  await pane.getByLabel("Message").fill("The new bags");
  await axeBoth(buyer, "a photo about to be sent");
  await pane.getByLabel("Message").press("Enter");
  const mine = pane.locator('[data-testid="chat-message"][data-mine="true"]');
  await expect(mine.getByRole("img", { name: "Your photo" })).toBeVisible();
  await expect(mine).toContainText("The new bags");
  await expect(pane.getByTestId("chat-attaching")).toHaveCount(0);
  expect(sent[0]).toContain('filename="bags.png"');

  // A PDF, with no words: Send without a caption.
  await pane.getByTestId("chat-file").setInputFiles({ name: "waybill.pdf", mimeType: "application/pdf", buffer: Buffer.from("%PDF-1.4\n%%EOF\n") });
  await expect(pane.getByTestId("chat-attaching")).toContainText("waybill.pdf");
  await pane.getByRole("button", { name: "Send" }).click();
  await expect(mine.getByRole("link", { name: /waybill\.pdf/ })).toBeVisible();

  // A voice note: recorded, done, then sent.
  await pane.getByRole("button", { name: "Record a voice note" }).click();
  await expect(pane.getByTestId("chat-recording")).toContainText("Recording 0:01", { timeout: 5_000 });
  await axeBoth(buyer, "recording a voice note");
  await pane.getByRole("button", { name: "Done" }).click();
  await expect(pane.getByTestId("chat-attaching")).toContainText("Voice note");
  await pane.getByRole("button", { name: "Send" }).click();
  await expect(mine.getByLabel("Your voice note")).toBeVisible();
  expect(sent[2]).toMatch(/filename="voice-note\.(webm|ogg|m4a)"/);

  // Held, released: sent at once, with no Done to press.
  const mic = pane.getByTestId("chat-mic");
  await mic.hover();
  await buyer.mouse.down();
  await expect(pane.getByTestId("chat-recording")).toContainText("release to send", { timeout: 5_000 });
  await buyer.waitForTimeout(1200);
  await buyer.mouse.up();
  await expect(mine.getByLabel("Your voice note")).toHaveCount(2);
  expect(sent).toHaveLength(4);
  // Held, slid left: cancelled, nothing sent.
  await mic.hover();
  const box = (await mic.boundingBox()) ?? { x: 0, y: 0, width: 0, height: 0 };
  await buyer.mouse.down();
  await expect(pane.getByTestId("chat-recording")).toBeVisible({ timeout: 5_000 });
  await buyer.waitForTimeout(600);
  await buyer.mouse.move(box.x - 120, box.y + box.height / 2, { steps: 4 });
  await buyer.mouse.up();
  await expect(pane.getByTestId("chat-recording")).toHaveCount(0);
  await buyer.waitForTimeout(500);
  expect(sent).toHaveLength(4);
  await axeBoth(buyer, "a photo, a PDF and a voice note");
});

test("a voice note and a photo to Taskiem on the web", async ({ browser }) => {
  const ctx = await browser.newContext({ permissions: ["microphone"] });
  const page = await ctx.newPage();
  await signUp(page, `Hauwa Hair ${letters()}`);
  await page.goto("/chat?with=taskiem");
  const pane = page.getByTestId("taskiem-pane");
  const mic = pane.getByTestId("chat-mic");
  await mic.hover();
  await page.mouse.down();
  await expect(pane.getByTestId("chat-recording")).toBeVisible({ timeout: 5_000 });
  await page.waitForTimeout(1200);
  await page.mouse.up();
  // Voice notes are off on this server: Taskiem says so, as on WhatsApp.
  const mine = pane.locator('[data-testid="taskiem-message"][data-mine="true"]');
  await expect(mine.last()).toContainText("Voice note");
  await expect(pane.locator('[data-testid="taskiem-message"][data-mine="false"]').last()).toContainText("Voice notes are not available");

  // A photo with a caption.
  const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==", "base64");
  await pane.getByTestId("taskiem-file").setInputFiles({ name: "braids.png", mimeType: "image/png", buffer: png });
  await expect(pane.getByTestId("taskiem-attaching")).toContainText("Photo: braids.png");
  await pane.getByLabel("Message to Taskiem").fill("Box braids 15000");
  await axeBoth(page, "a photo to Taskiem");
  await pane.getByRole("button", { name: "Send", exact: true }).click();
  await expect(mine.last()).toContainText("Photo Box braids 15000");
  await expect(pane.getByTestId("taskiem-message")).toHaveCount(4);
  await axeBoth(page, "Taskiem's answers to a voice note and a photo");
});
