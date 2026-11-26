import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Files in chats (docs/spec/chat-previews-and-editing.md §1): a PDF shows
// the picture of its first page, from Taskiem's own address, and opens in
// Taskiem's viewer (the browser's own reader in a sandboxed frame), which
// Escape closes; a Word file is a card with its type's icon that
// downloads; the picker refuses a program before anything is uploaded.
// The server's side (drawing the picture, checking the bytes) is tested
// in Go; this server has no storage for files, so the files and their
// facts are stood in for.
// The page's Content-Security-Policy is bypassed only so axe can be added.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@files.test`, name: `${tenant.split(" ")[0]} Owner`, password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

/** Two linked businesses; Ada (the buyer) is the page under test. */
async function linked(browser: Browser) {
  const stamp = letters();
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, `Dangote Depot ${stamp}`);
  const buyer = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
  await signUp(buyer, `Ada Kitchen ${stamp}`);
  await seller.request.put("/v1/link-settings", { headers: H, data: { findable: true } });
  const found = await (await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`dangote depot ${stamp}`)}`, { headers: H })).json();
  await buyer.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "supplier" } });
  const link = (await (await seller.request.get("/v1/links", { headers: H })).json()).links[0].id as string;
  await seller.request.post(`/v1/links/${link}/accept`, { headers: H });
  return { seller, buyer, link };
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.evaluate((t) => {
      if (t === "dark") document.documentElement.dataset.theme = "dark";
      else delete document.documentElement.dataset.theme;
    }, scheme);
    await page.waitForTimeout(400);
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
}

async function fits(page: Page, where: string) {
  for (const width of [390, 360]) {
    await page.setViewportSize({ width, height: 800 });
    await page.waitForTimeout(200);
    expect(await page.evaluate(() => document.documentElement.scrollWidth), `${where} at ${width}`).toBeLessThanOrEqual(width);
  }
  await page.setViewportSize({ width: 390, height: 844 });
}

// A 2 by 1 PNG standing in for the picture of a PDF's first page.
const PICTURE = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAIAAAABCAYAAAD0In+KAAAAEUlEQVR4nGNgYPj/n4GBgQEAEAEB/cg9C3cAAAAASUVORK5CYII=",
  "base64",
);
const PDF = Buffer.from("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\n%%EOF\n");

const ATTACHMENTS: Record<string, Record<string, unknown>> = {
  "the price list": { kind: "file", type: "application/pdf", size: 1258291, name: "Price list.pdf", pages: 3, preview: "ready", w: 640, h: 905 },
  "the order form": {
    kind: "file",
    type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
    size: 86016,
    name: "Order form.docx",
    doc: "word",
  },
};

/** The seller's messages, each given its file (the stand-in for a file
 * the server kept), and the files' routes. */
async function withFiles(buyer: Page, seller: Page, link: string) {
  for (const words of Object.keys(ATTACHMENTS)) {
    const r = await seller.request.post(`/v1/link-chats/${link}/messages`, { headers: H, data: { text: words } });
    expect(r.status()).toBe(201);
  }
  await buyer.route(`**/v1/link-chats/${link}/messages*`, async (route) => {
    if (route.request().method() !== "GET") return route.continue();
    const r = await route.fetch();
    const body = await r.json();
    for (const m of body.messages ?? []) if (ATTACHMENTS[m.body]) m.attachment = ATTACHMENTS[m.body];
    await route.fulfill({ response: r, json: body });
  });
  const asked: string[] = [];
  await buyer.route(`**/v1/link-chats/${link}/files/*`, (route) => {
    const u = new URL(route.request().url());
    asked.push(u.search);
    if (u.searchParams.get("preview") === "1") return route.fulfill({ contentType: "image/png", body: PICTURE });
    if (u.searchParams.get("view") === "1")
      return route.fulfill({ contentType: "application/pdf", body: PDF, headers: { "Content-Disposition": "inline", "X-Content-Type-Options": "nosniff" } });
    return route.fulfill({
      contentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
      body: Buffer.from("PK\u0003\u0004 a Word file"),
      headers: { "Content-Disposition": `attachment; filename="Order form.docx"` },
    });
  });
  return asked;
}

test("a PDF shows its first page and opens in the viewer; Escape closes it", async ({ browser }) => {
  const { seller, buyer, link } = await linked(browser);
  const asked = await withFiles(buyer, seller, link);
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const pdf = pane.locator('[data-testid="chat-message"]').filter({ hasText: "Price list.pdf" });
  const card = pdf.getByTestId("chat-file-card");
  await expect(card).toContainText("PDF · 3 pages · 1.2 MB");
  const pic = card.locator(".file-picture img");
  await expect(pic).toBeVisible();
  const src = await pic.evaluate((el: HTMLImageElement) => el.currentSrc || el.src);
  expect(new URL(src).origin).toBe(new URL(buyer.url()).origin);
  expect(new URL(src).search).toBe("?preview=1");
  await buyer.mouse.move(1, 1);
  await axeBoth(buyer, "a PDF and a Word file in the chat");
  await fits(buyer, "a PDF and a Word file in the chat");

  // The viewer: the name, who sent it, Download, and the sandboxed reader.
  await card.click();
  const viewer = buyer.getByTestId("pdf-viewer");
  await expect(viewer).toBeVisible();
  await expect(viewer.getByRole("heading", { name: "Price list.pdf" })).toBeVisible();
  await expect(viewer.getByRole("link", { name: "Download" })).toHaveAttribute("download", "Price list.pdf");
  const frame = viewer.locator("iframe");
  if (await frame.count()) {
    await expect(frame).toHaveAttribute("sandbox", "");
    expect(new URL((await frame.getAttribute("src")) ?? "", buyer.url()).search).toBe("?view=1");
  } else {
    await expect(viewer.getByRole("link", { name: "Open in a new tab" })).toBeVisible();
  }
  // The bubble behind keeps no hover from the tap that opened it.
  await buyer.mouse.move(1, 1);
  await axeBoth(buyer, "the PDF viewer");
  await fits(buyer, "the PDF viewer");
  await buyer.keyboard.press("Escape");
  await expect(viewer).toHaveCount(0);
  await expect(card).toBeFocused();
  expect(asked).toContain("?preview=1");
});

test("a Word file is a card that downloads", async ({ browser }) => {
  const { seller, buyer, link } = await linked(browser);
  await withFiles(buyer, seller, link);
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const word = pane.locator('[data-testid="chat-message"]').filter({ hasText: "Order form.docx" }).getByTestId("chat-file-card");
  await expect(word).toContainText("Word · 84 KB");
  await expect(word.locator(".file-type.file-word")).toHaveCount(1);
  await expect(word.locator("img")).toHaveCount(0);
  const [download] = await Promise.all([buyer.waitForEvent("download"), word.click()]);
  expect(download.suggestedFilename()).toBe("Order form.docx");
});

test("the picker refuses a program before it is uploaded", async ({ browser }) => {
  const { buyer, link } = await linked(browser);
  let uploads = 0;
  await buyer.route(`**/v1/link-chats/${link}/files`, (route) => {
    uploads++;
    return route.continue();
  });
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane.locator('input[type="file"][data-testid$="-file"]').first().setInputFiles({ name: "invoice.pdf.exe", mimeType: "application/x-msdownload", buffer: Buffer.from("MZ\u0090\u0000") });
  await expect(pane.getByText("Taskiem can send photos, voice notes, PDFs and Word, Excel, PowerPoint and text documents.")).toBeVisible();
  // A Word file is taken: its card shows above the box before sending.
  await pane.locator('input[type="file"][data-testid$="-file"]').first().setInputFiles({
    name: "Order form.docx",
    mimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
    buffer: Buffer.from("PK\u0003\u0004"),
  });
  const pending = pane.locator(".pending-file");
  await expect(pending).toContainText("Order form.docx · Word");
  await expect(pending.locator(".file-type.file-word")).toHaveCount(1);
  expect(uploads).toBe(0);
});
