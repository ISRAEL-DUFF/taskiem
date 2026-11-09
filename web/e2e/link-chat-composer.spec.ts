import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// The composer, as WhatsApp Web's: a message shows at once with a clock and
// is retried without ever being sent twice (instant send); and the rest of
// what a WhatsApp user does at the bottom of the screen.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };
const must = <T>(v: T | null | undefined): T => {
  if (v == null) throw new Error("nothing there");
  return v;
};
const letters = () =>
  String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@chat.test`,
      name: `${tenant.split(" ")[0]} Owner`,
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

/** Two linked businesses; Ada (the buyer) is the page under test. */
async function linked(
  browser: Browser,
  ctxOptions: Parameters<Browser["newContext"]>[0] = {},
) {
  const stamp = letters();
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, `Dangote Depot ${stamp}`);
  const buyer = await (
    await browser.newContext({
      viewport: { width: 390, height: 844 },
      ...ctxOptions,
    })
  ).newPage();
  await signUp(buyer, `Ada Kitchen ${stamp}`);
  await seller.request.put("/v1/link-settings", {
    headers: H,
    data: { findable: true },
  });
  const found = await (
    await buyer.request.get(
      `/v1/links/search?q=${encodeURIComponent(`dangote depot ${stamp}`)}`,
      { headers: H },
    )
  ).json();
  await buyer.request.post("/v1/links", {
    headers: H,
    data: { business: found.results[0].business, role: "supplier" },
  });
  const link = (
    await (await seller.request.get("/v1/links", { headers: H })).json()
  ).links[0].id as string;
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

const messagesOf = async (page: Page, link: string) =>
  (
    (
      await (
        await page.request.get(`/v1/link-chats/${link}/messages`, {
          headers: H,
        })
      ).json()
    ).messages as { body: string }[]
  ).map((m) => m.body);

test("a message shows at once with a clock, then the server's copy takes its place", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  let release: () => void = () => undefined;
  const gate = new Promise<void>((r) => (release = r));
  await buyer.route(`**/v1/link-chats/${link}/messages`, async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    await gate; // the connection is slow
    await route.continue();
  });
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane.getByLabel("Message").fill("Ten bags of rice");
  await pane.getByLabel("Message").press("Enter");
  // At once: in the thread, with a clock, and the box already empty.
  const mine = pane.locator('[data-testid="chat-message"][data-mine="true"]');
  await expect(mine).toContainText("Ten bags of rice");
  await expect(mine.getByRole("img", { name: "Sending" })).toBeVisible();
  await expect(pane.getByLabel("Message")).toHaveValue("");
  await axeBoth(buyer, "a message being sent");
  release();
  await expect(mine.getByRole("img", { name: "Delivered" })).toBeVisible();
  await expect(mine).toHaveCount(1);
  expect(await messagesOf(buyer, link)).toEqual(["Ten bags of rice"]);
});

test("a message that cannot be sent says so, and Retry never sends it twice", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  const ids: string[] = [];
  let mode: "offline" | "lost" | "ok" = "offline";
  await buyer.route(`**/v1/link-chats/${link}/messages`, async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    ids.push(JSON.parse(route.request().postData() ?? "{}").client_id);
    if (mode === "offline") return route.abort("connectionfailed");
    if (mode === "lost") {
      await route.fetch(); // it reaches the server, and the answer is lost
      return route.abort("connectionreset");
    }
    return route.continue();
  });
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const mine = pane.locator('[data-testid="chat-message"][data-mine="true"]');

  // Offline: Not sent, with Retry and Delete; the server has nothing.
  await pane.getByLabel("Message").fill("Send 20 bags");
  await pane.getByLabel("Message").press("Enter");
  const failed = pane.getByTestId("chat-failed");
  await expect(failed).toContainText("Not sent");
  await expect(failed).toContainText("no connection");
  await axeBoth(buyer, "a message that was not sent");
  expect(await messagesOf(buyer, link)).toEqual([]);

  // The request got there but the answer did not: Retry (the same send)
  // finds it at the server, and there is one message, not two.
  mode = "lost";
  await failed.getByRole("button", { name: "Retry" }).click();
  await expect(failed).toContainText("Not sent");
  mode = "ok";
  await failed.getByRole("button", { name: "Retry" }).click();
  await expect(mine.getByRole("img", { name: "Delivered" })).toBeVisible();
  await expect(mine).toHaveCount(1);
  await expect(failed).toHaveCount(0);
  expect(await messagesOf(buyer, link)).toEqual(["Send 20 bags"]);
  expect(new Set(ids).size).toBe(1); // every try carried the same id
  expect(ids.length).toBe(3);

  // Delete gives a failed message up.
  mode = "offline";
  await pane.getByLabel("Message").fill("Never mind");
  await pane.getByLabel("Message").press("Enter");
  await expect(failed).toContainText("Not sent");
  await failed.getByRole("button", { name: "Delete" }).click();
  await expect(failed).toHaveCount(0);
  await expect(pane.getByText("Never mind")).toHaveCount(0);
  expect(await messagesOf(buyer, link)).toEqual(["Send 20 bags"]);
});

const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
  "base64",
);

/** The file route, answered as the API answers it (the test server has no scanner). */
async function mockFiles(page: Page, link: string) {
  const sent: { body: string }[] = [];
  let n = 0;
  await page.route(`**/v1/link-chats/${link}/files`, async (route) => {
    const body = route.request().postDataBuffer()?.toString("latin1") ?? "";
    sent.push({ body });
    const caption = /name="text"\r\n\r\n([^\r]*)/.exec(body)?.[1];
    n += 1;
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({
        message: {
          id: `0192e2e0-0000-7000-8000-00000000000${n}`,
          link,
          mine: true,
          kind: "text",
          body: caption,
          attachment: {
            kind: "photo",
            type: "image/png",
            size: PNG.length,
            name: "x.png",
          },
          created_at: new Date().toISOString(),
        },
      }),
    });
  });
  await page.route(`**/v1/link-chats/${link}/files/*`, (route) =>
    route.fulfill({ status: 200, contentType: "image/png", body: PNG }),
  );
  return sent;
}

test("the emoji panel inserts at the cursor, finds by word, and remembers", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const box = pane.getByLabel("Message");
  await box.fill("Hello world");
  await box.evaluate((el: HTMLTextAreaElement) => el.setSelectionRange(5, 5));
  await pane.getByTestId("emoji-button").click();
  const panel = pane.getByTestId("emoji-panel");
  await expect(panel).toBeVisible();
  await expect(
    panel.getByRole("button", { name: "grinning face", exact: true }),
  ).toBeVisible();
  await axeBoth(buyer, "the emoji panel");

  // By a word, in the middle of the text.
  await panel.getByLabel("Search emoji").fill("fire");
  await panel.getByRole("button", { name: "fire", exact: true }).click();
  await expect(box).toHaveValue("Hello🔥 world");
  // The caret is after it: the next one goes there.
  await panel.getByLabel("Search emoji").fill("heart");
  await panel.getByRole("button", { name: "red heart" }).click();
  await expect(box).toHaveValue("Hello🔥❤️ world");

  // Recently used first, next time; Esc closes the panel.
  await buyer.keyboard.press("Escape");
  await expect(panel).toHaveCount(0);
  await pane.getByTestId("emoji-button").click();
  await expect(
    panel
      .getByRole("group", { name: "Recently used" })
      .getByRole("button")
      .first(),
  ).toHaveAccessibleName("red heart");
  await buyer.keyboard.press("Escape");
  await expect(panel).toHaveCount(0);

  // And it sends.
  await box.press("Enter");
  const mine = pane.locator('[data-testid="chat-message"][data-mine="true"]');
  await expect(mine).toContainText("Hello🔥❤️ world");
});

test("a pasted picture and a dropped file wait for a caption, as a photo to send", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  const sent = await mockFiles(buyer, link);
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const box = pane.getByLabel("Message");

  // Pasted.
  await box.evaluate((el, b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    const dt = new DataTransfer();
    dt.items.add(new File([bytes], "pasted.png", { type: "image/png" }));
    el.dispatchEvent(
      new ClipboardEvent("paste", {
        clipboardData: dt,
        bubbles: true,
        cancelable: true,
      }),
    );
  }, PNG.toString("base64"));
  await expect(pane.getByTestId("chat-attaching")).toContainText(
    "Photo: pasted.png",
  );
  await box.fill("The new bags");
  await axeBoth(buyer, "a pasted photo about to be sent");
  await box.press("Enter");
  const mine = pane.locator('[data-testid="chat-message"][data-mine="true"]');
  await expect(mine).toContainText("The new bags");
  await expect(pane.getByTestId("chat-attaching")).toHaveCount(0);
  expect(sent).toHaveLength(1);
  expect(sent[0]?.body).toContain('filename="pasted.png"');
  expect(sent[0]?.body).toMatch(/name="client_id"/);

  // Dropped on the thread: an overlay, then the same waiting for a caption.
  await pane.evaluate((el, b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    const dt = new DataTransfer();
    dt.items.add(new File([bytes], "waybill.png", { type: "image/png" }));
    window.__dt = dt;
    el.dispatchEvent(
      new DragEvent("dragenter", {
        dataTransfer: dt,
        bubbles: true,
        cancelable: true,
      }),
    );
  }, PNG.toString("base64"));
  await expect(pane.getByTestId("chat-drop")).toBeVisible();
  await pane.evaluate((el) =>
    el.dispatchEvent(
      new DragEvent("drop", {
        dataTransfer: window.__dt,
        bubbles: true,
        cancelable: true,
      }),
    ),
  );
  await expect(pane.getByTestId("chat-drop")).toHaveCount(0);
  await expect(pane.getByTestId("chat-attaching")).toContainText(
    "Photo: waybill.png",
  );
  // Esc gives it up.
  await box.focus();
  await buyer.keyboard.press("Escape");
  await expect(pane.getByTestId("chat-attaching")).toHaveCount(0);

  // Something that is not a photo, a PDF or a voice note is refused kindly.
  await pane.getByTestId("chat-file").setInputFiles({
    name: "notes.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("hi"),
  });
  await expect(pane.getByText(/Send a photo/)).toBeVisible();
});

test("the clip offers a photo, a document and the camera, and the camera works on a computer", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  await buyer.addInitScript(() => {
    // A camera that shows a green square (the test browser has none).
    navigator.mediaDevices.getUserMedia = async () => {
      const c = document.createElement("canvas");
      c.width = 320;
      c.height = 240;
      const ctx = c.getContext("2d");
      if (!ctx) throw new Error("no canvas");
      setInterval(() => {
        ctx.fillStyle = "#2f7d5b";
        ctx.fillRect(0, 0, 320, 240);
      }, 50);
      return c.captureStream(15);
    };
  });
  await mockFiles(buyer, link);
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");

  await pane.getByTestId("attach").click();
  const menu = pane.getByTestId("attach-menu");
  for (const name of ["Photo", "Document", "Camera"])
    await expect(menu.getByRole("button", { name })).toBeVisible();
  await axeBoth(buyer, "the attach menu");
  // A photo and a document open the file chooser, each for its own kind.
  const chooser1 = buyer.waitForEvent("filechooser");
  await menu.getByRole("button", { name: "Photo" }).click();
  expect((await chooser1).isMultiple()).toBe(false);
  await expect(pane.getByTestId("chat-file")).toHaveAttribute(
    "accept",
    /image\/png/,
  );
  await pane.getByTestId("attach").click();
  const chooser2 = buyer.waitForEvent("filechooser");
  await menu.getByRole("button", { name: "Document" }).click();
  await chooser2;
  await expect(pane.getByTestId("chat-file")).toHaveAttribute(
    "accept",
    "application/pdf",
  );

  // The camera: a sheet with what it sees, and Take photo.
  await pane.getByTestId("attach").click();
  await menu.getByRole("button", { name: "Camera" }).click();
  const sheet = buyer.getByRole("dialog", { name: "Take a photo" });
  await expect(sheet).toBeVisible();
  await expect(sheet.getByRole("button", { name: "Take photo" })).toBeEnabled();
  await axeBoth(buyer, "the camera");
  await sheet.getByRole("button", { name: "Take photo" }).click();
  await expect(sheet).toHaveCount(0);
  await expect(pane.getByTestId("chat-attaching")).toContainText(
    /Photo: photo-\d+\.jpg/,
  );
});

test("a camera that is not allowed says so and offers a photo instead", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  await buyer.addInitScript(() => {
    navigator.mediaDevices.getUserMedia = async () => {
      throw new DOMException("denied", "NotAllowedError");
    };
  });
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane.getByTestId("attach").click();
  await pane
    .getByTestId("attach-menu")
    .getByRole("button", { name: "Camera" })
    .click();
  const sheet = buyer.getByRole("dialog", { name: "Take a photo" });
  await expect(sheet).toContainText("did not let Taskiem use the camera");
  await expect(sheet.getByRole("button", { name: "Take photo" })).toHaveCount(
    0,
  );
  await axeBoth(buyer, "the camera that is not allowed");
  await sheet.getByRole("button", { name: "Close" }).click();
  await expect(sheet).toHaveCount(0);
});

test("the box grows to six lines then scrolls, and has no drag handle", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  await buyer.goto(`/chat?with=${link}`);
  const box = buyer.getByTestId("chat-pane").getByLabel("Message");
  const height = () => box.evaluate((el) => el.getBoundingClientRect().height);
  expect(await box.evaluate((el) => getComputedStyle(el).resize)).toBe("none");
  const one = await height();
  await box.fill("one\ntwo\nthree");
  const three = await height();
  expect(three).toBeGreaterThan(one + 20);
  await box.fill(
    Array.from({ length: 6 }, (_, i) => `line ${i + 1}`).join("\n"),
  );
  const six = await height();
  await box.fill(
    Array.from({ length: 12 }, (_, i) => `line ${i + 1}`).join("\n"),
  );
  const twelve = await height();
  expect(Math.abs(twelve - six)).toBeLessThan(3); // no taller than six lines
  expect(await box.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(
    true,
  ); // it scrolls
  await box.fill("");
  expect(Math.abs((await height()) - one)).toBeLessThan(3);
});

test("an unsent message is kept per conversation and shown in the list", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane.getByLabel("Message").fill("Ten bags of rice, please");
  // Back to the list: Draft: in its row.
  await pane.getByRole("button", { name: "Back to the chats" }).click();
  const row = buyer.getByTestId("link-linked");
  await expect(row).toContainText("Draft:");
  await expect(row).toContainText("Ten bags of rice, please");
  await axeBoth(buyer, "a list with a draft");
  // Opened again: the words are back; sent, the draft goes.
  await row.click();
  await expect(
    buyer.getByTestId("chat-pane").getByLabel("Message"),
  ).toHaveValue("Ten bags of rice, please");
  await buyer.getByTestId("chat-pane").getByLabel("Message").press("Enter");
  await buyer
    .getByTestId("chat-pane")
    .getByRole("button", { name: "Back to the chats" })
    .click();
  await expect(buyer.getByTestId("link-linked")).not.toContainText("Draft:");
  await expect(buyer.getByTestId("link-linked")).toContainText(
    "Ten bags of rice, please",
  );
});

test("Esc gives up a reply", async ({ browser }) => {
  const { seller, buyer, link } = await linked(browser);
  await seller.request.post(`/v1/link-chats/${link}/messages`, {
    headers: H,
    data: { text: "Rice is back" },
  });
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane
    .getByTestId("chat-message")
    .first()
    .getByRole("button", { name: /^More/ })
    .click();
  await pane
    .getByTestId("chat-more")
    .getByRole("button", { name: "Reply" })
    .click();
  await expect(pane.getByTestId("chat-replying")).toBeVisible();
  await pane.getByLabel("Message").focus();
  await buyer.keyboard.press("Escape");
  await expect(pane.getByTestId("chat-replying")).toHaveCount(0);
});

test("the Taskiem conversation has the emoji panel, a growing box and a kept draft too", async ({
  browser,
}) => {
  const stamp = letters();
  const page = await (
    await browser.newContext({ viewport: { width: 390, height: 844 } })
  ).newPage();
  await signUp(page, `Ada Kitchen ${stamp}`);
  await page.goto("/chat?with=taskiem");
  const pane = page.getByTestId("taskiem-pane");
  const box = pane.getByLabel("Message to Taskiem");
  await box.fill("status");
  await pane.getByRole("button", { name: "Emoji" }).click();
  await pane.getByLabel("Search emoji").fill("thumbs up");
  await pane.getByRole("button", { name: "thumbs up" }).click();
  await expect(box).toHaveValue("status👍");
  await axeBoth(page, "the Taskiem emoji panel");
  await page.keyboard.press("Escape");
  await expect(pane.getByTestId("emoji-panel")).toHaveCount(0);
  expect(await box.evaluate((el) => getComputedStyle(el).resize)).toBe("none");
  // Away and back: the draft is in the list and in the box.
  await pane.getByRole("button", { name: "Back to the chats" }).click();
  await expect(page.getByTestId("taskiem-chat")).toContainText(
    "Draft: status👍",
  );
  await page.getByTestId("taskiem-chat").click();
  await expect(
    page.getByTestId("taskiem-pane").getByLabel("Message to Taskiem"),
  ).toHaveValue("status👍");
});

/** The buyer of a first linked pair, linked to a second supplier too. */
async function twoSuppliers(browser: Browser) {
  const a = await linked(browser);
  const stamp = letters();
  const seller2 = await (await browser.newContext()).newPage();
  await signUp(seller2, `Eko Sacks ${stamp}`);
  await seller2.request.put("/v1/link-settings", {
    headers: H,
    data: { findable: true },
  });
  const found = await (
    await a.buyer.request.get(
      `/v1/links/search?q=${encodeURIComponent(`eko sacks ${stamp}`)}`,
      { headers: H },
    )
  ).json();
  await a.buyer.request.post("/v1/links", {
    headers: H,
    data: { business: found.results[0].business, role: "supplier" },
  });
  const link2 = (
    await (await seller2.request.get("/v1/links", { headers: H })).json()
  ).links[0].id as string;
  await seller2.request.post(`/v1/links/${link2}/accept`, { headers: H });
  await a.buyer.setViewportSize({ width: 1280, height: 800 });
  return { ...a, seller2, link2 };
}

test("a message still being sent stays with its own conversation when you switch to another", async ({
  browser,
}) => {
  const { buyer, link, link2 } = await twoSuppliers(browser);
  let release: () => void = () => undefined;
  const gate = new Promise<void>((r) => (release = r));
  await buyer.route(`**/v1/link-chats/${link}/messages`, async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    await gate;
    await route.continue();
  });
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane.getByLabel("Message").fill("For the first business");
  await pane.getByLabel("Message").press("Enter");
  await expect(pane.getByTestId("chat-message")).toContainText(
    "For the first business",
  );
  // Straight to the other conversation (the list is beside it on a computer).
  await buyer
    .getByTestId("link-linked")
    .filter({ hasText: "Eko Sacks" })
    .click();
  await expect(buyer.getByTestId("chat-pane")).toContainText("Eko Sacks");
  release();
  await buyer.waitForTimeout(1500);
  await expect(buyer.getByTestId("chat-message")).toHaveCount(0); // not here
  // It is where it was sent.
  await buyer.getByTestId("link-linked").filter({ hasText: "Dangote" }).click();
  const mine = buyer
    .getByTestId("chat-pane")
    .locator('[data-testid="chat-message"][data-mine="true"]');
  await expect(mine).toHaveCount(1);
  await expect(mine).toContainText("For the first business");
  await expect(mine.getByRole("img", { name: "Delivered" })).toBeVisible();
  expect(link2).not.toBe(link);
});

test("a message that was not sent is still there when you come back, and goes again when the connection is", async ({
  browser,
}) => {
  const { buyer, link } = await twoSuppliers(browser);
  const ids: string[] = [];
  let up = false;
  await buyer.route(`**/v1/link-chats/${link}/messages`, async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    ids.push(JSON.parse(route.request().postData() ?? "{}").client_id);
    return up ? route.continue() : route.abort("connectionfailed");
  });
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane.getByLabel("Message").fill("Send 20 bags");
  await pane.getByLabel("Message").press("Enter");
  await expect(pane.getByTestId("chat-failed")).toContainText("Not sent");
  // Away and back: not lost.
  await buyer
    .getByTestId("link-linked")
    .filter({ hasText: "Eko Sacks" })
    .click();
  await expect(buyer.getByTestId("chat-pane")).toContainText("Eko Sacks");
  await buyer.getByTestId("link-linked").filter({ hasText: "Dangote" }).click();
  await expect(
    buyer.getByTestId("chat-pane").getByTestId("chat-failed"),
  ).toContainText("Not sent");
  // The connection is back: it goes by itself, once.
  up = true;
  await buyer.evaluate(() => window.dispatchEvent(new Event("online")));
  const mine = buyer
    .getByTestId("chat-pane")
    .locator('[data-testid="chat-message"][data-mine="true"]');
  await expect(mine.getByRole("img", { name: "Delivered" })).toBeVisible();
  await expect(mine).toHaveCount(1);
  expect(await messagesOf(buyer, link)).toEqual(["Send 20 bags"]);
  expect(new Set(ids).size).toBe(1);
});

test("a photo that was not sent can be sent again without a second copy", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  const sent = await mockFiles(buyer, link);
  const ids: string[] = [];
  let up = false;
  await buyer.unroute(`**/v1/link-chats/${link}/files`);
  await buyer.route(`**/v1/link-chats/${link}/files`, async (route) => {
    const body = route.request().postDataBuffer()?.toString("latin1") ?? "";
    ids.push(/name="client_id"\r\n\r\n([^\r]*)/.exec(body)?.[1] ?? "");
    if (!up) return route.abort("connectionreset");
    sent.push({ body });
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({
        message: {
          id: "0192e2e0-0000-7000-8000-0000000000f1",
          link,
          mine: true,
          kind: "text",
          body: "The new bags",
          attachment: {
            kind: "photo",
            type: "image/png",
            size: PNG.length,
            name: "bags.png",
          },
          created_at: new Date().toISOString(),
        },
      }),
    });
  });
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane
    .getByTestId("chat-file")
    .setInputFiles({ name: "bags.png", mimeType: "image/png", buffer: PNG });
  await pane.getByLabel("Message").fill("The new bags");
  await pane.getByLabel("Message").press("Enter");
  const failed = pane.getByTestId("chat-failed");
  await expect(failed).toContainText("Not sent");
  await expect(pane.getByRole("img", { name: "Your photo" })).toBeVisible(); // its own preview, meanwhile
  up = true;
  await failed.getByRole("button", { name: "Retry" }).click();
  const mine = pane.locator('[data-testid="chat-message"][data-mine="true"]');
  await expect(mine.getByRole("img", { name: "Delivered" })).toBeVisible();
  await expect(mine).toHaveCount(1);
  expect(sent).toHaveLength(1);
  expect(ids.length).toBe(2);
  expect(new Set(ids).size).toBe(1);
});

test("signing out forgets what was half written", async ({ browser }) => {
  const { buyer, link } = await linked(browser);
  await buyer.setViewportSize({ width: 1280, height: 800 });
  await buyer.goto(`/chat?with=${link}`);
  await buyer
    .getByTestId("chat-pane")
    .getByLabel("Message")
    .fill("Not for the next person");
  await buyer.goto("/home"); // the draft is kept for this tab
  expect(
    await buyer.evaluate(() =>
      Object.keys(sessionStorage).filter((k) => k.startsWith("taskiem-draft:")),
    ),
  ).toHaveLength(1);
  await buyer.getByRole("button", { name: "Sign out" }).click();
  await expect(
    buyer.getByRole("heading", { name: "Sign in to Taskiem" }),
  ).toBeVisible();
  expect(
    await buyer.evaluate(() =>
      Object.keys(sessionStorage).filter((k) => k.startsWith("taskiem-draft:")),
    ),
  ).toHaveLength(0);
});

test("the emoji panel works from the keyboard, and the hands take a skin tone", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const box = pane.getByLabel("Message");
  await pane.getByTestId("emoji-button").click();
  const panel = pane.getByTestId("emoji-panel");
  // One emoji is in the tab order, not hundreds.
  await expect(panel.locator('.emoji[tabindex="0"]')).toHaveCount(1);
  expect(await panel.locator(".emoji").count()).toBeGreaterThan(40);
  expect(
    await panel
      .locator('.emoji:not([tabindex="0"])')
      .evaluateAll((els) =>
        els.every((e) => e.getAttribute("tabindex") === "-1"),
      ),
  ).toBe(true);
  // Down from the search box goes into the grid; the arrows move about it.
  await panel.getByLabel("Search emoji").press("ArrowDown");
  const focused = () =>
    buyer.evaluate(() => document.activeElement?.getAttribute("aria-label"));
  const names = await panel
    .locator(".emoji")
    .evaluateAll((els) =>
      els.slice(0, 40).map((e) => e.getAttribute("aria-label")),
    );
  expect(await focused()).toBe("grinning face");
  expect(names[0]).toBe("grinning face");
  await buyer.keyboard.press("ArrowRight");
  expect(await focused()).toBe(names[1]);
  await buyer.keyboard.press("ArrowDown"); // a row down
  const down = await focused();
  expect(down).not.toBe(names[1]);
  await buyer.keyboard.press("ArrowUp");
  expect(await focused()).toBe(names[1]);
  await buyer.keyboard.press("End");
  const last = await focused();
  await buyer.keyboard.press("Home");
  expect(await focused()).toBe("grinning face");
  expect(last).not.toBe("grinning face");
  // Enter picks it, and the box has it.
  await buyer.keyboard.press("Enter");
  await expect(box).toHaveValue("😀");
  await axeBoth(buyer, "the emoji panel from the keyboard");

  // The hands take a tone, and keep it.
  await box.fill(""); // the panel stays open after one is picked
  await expect(panel).toBeVisible();
  await panel.getByLabel("Search emoji").fill("thumbs up");
  await panel.getByRole("button", { name: "Medium skin tone" }).click();
  await expect(panel.getByRole("button", { name: "thumbs up" })).toHaveText(
    "👍🏽",
  );
  await axeBoth(buyer, "the emoji panel with skin tones");
  await panel.getByRole("button", { name: "thumbs up" }).click();
  await expect(box).toHaveValue("👍🏽");
  // The flags are there too.
  await panel.getByLabel("Search emoji").fill("nigeria");
  await expect(panel.getByRole("button", { name: "Nigeria" })).toHaveText("🇳🇬");
});

test("the attach menu works from the keyboard, and Esc gives up a waiting file or a reply from where focus is", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await seller.request.post(`/v1/link-chats/${link}/messages`, {
    headers: H,
    data: { text: "Rice is back" },
  });
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const focused = () =>
    buyer.evaluate(
      () =>
        document.activeElement?.textContent?.trim() ||
        document.activeElement?.getAttribute("aria-label"),
    );
  // The clip: Enter opens, focus is on Photo, the arrows move, Esc closes and gives focus back.
  await pane.getByTestId("attach").focus();
  await buyer.keyboard.press("Enter");
  await expect(pane.getByTestId("attach-menu")).toBeVisible();
  expect(await focused()).toBe("Photo");
  await buyer.keyboard.press("ArrowDown");
  expect(await focused()).toBe("Document");
  await buyer.keyboard.press("ArrowUp");
  await buyer.keyboard.press("ArrowUp");
  expect(await focused()).toBe("Camera"); // round the end
  await buyer.keyboard.press("Escape");
  await expect(pane.getByTestId("attach-menu")).toHaveCount(0);
  expect(await focused()).toBe("Attach");

  // A waiting file: Esc from its Remove button gives it up.
  await pane
    .getByTestId("chat-file")
    .setInputFiles({ name: "bags.png", mimeType: "image/png", buffer: PNG });
  await expect(pane.getByTestId("chat-attaching")).toBeVisible();
  await pane
    .getByTestId("chat-attaching")
    .getByRole("button", { name: "Remove" })
    .focus();
  await buyer.keyboard.press("Escape");
  await expect(pane.getByTestId("chat-attaching")).toHaveCount(0);

  // A reply: Esc from its Cancel button gives it up.
  await pane
    .getByTestId("chat-message")
    .first()
    .getByRole("button", { name: /^More/ })
    .click();
  await pane
    .getByTestId("chat-more")
    .getByRole("button", { name: "Reply" })
    .click();
  await expect(pane.getByTestId("chat-replying")).toBeVisible();
  await pane
    .getByTestId("chat-replying")
    .getByRole("button", { name: /Cancel/ })
    .focus();
  await buyer.keyboard.press("Escape");
  await expect(pane.getByTestId("chat-replying")).toHaveCount(0);
});

test("more than one file dropped: the first is taken, and it says so", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane.evaluate((el, b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    const dt = new DataTransfer();
    dt.items.add(new File([bytes], "one.png", { type: "image/png" }));
    dt.items.add(new File([bytes], "two.png", { type: "image/png" }));
    el.dispatchEvent(
      new DragEvent("dragenter", {
        dataTransfer: dt,
        bubbles: true,
        cancelable: true,
      }),
    );
    el.dispatchEvent(
      new DragEvent("drop", {
        dataTransfer: dt,
        bubbles: true,
        cancelable: true,
      }),
    );
  }, PNG.toString("base64"));
  await expect(pane.getByTestId("chat-attaching")).toContainText("one.png");
  await expect(
    pane.getByText(/Only one file can be sent at a time/),
  ).toBeVisible();
});

test("on a phone the camera sits beside the microphone; on a computer it is in the clip's menu", async ({
  browser,
}) => {
  const phone = await linked(browser, { hasTouch: true, isMobile: true });
  await phone.buyer.goto(`/chat?with=${phone.link}`);
  const quick = phone.buyer
    .getByTestId("chat-pane")
    .getByTestId("camera-quick");
  await expect(quick).toBeVisible();
  const chooser = phone.buyer.waitForEvent("filechooser");
  await quick.click();
  await chooser; // the camera app, through the file input
  // It stays while words are typed, beside the clip, as the boards draw
  // the composer; a computer has it too (DeskSeller).
  await phone.buyer.getByTestId("chat-pane").getByLabel("Message").fill("hi");
  await expect(quick).toBeVisible();

  const computer = await linked(browser);
  await computer.buyer.goto(`/chat?with=${computer.link}`);
  await expect(
    computer.buyer.getByTestId("chat-pane").getByTestId("camera-quick"),
  ).toBeVisible();
});

test("Esc gives up a reply from anywhere in the chat, but closes an open menu first", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await seller.request.post(`/v1/link-chats/${link}/messages`, {
    headers: H,
    data: { text: "Rice is back" },
  });
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane
    .getByTestId("chat-message")
    .first()
    .getByRole("button", { name: /^More/ })
    .click();
  await pane
    .getByTestId("chat-more")
    .getByRole("button", { name: "Reply" })
    .click();
  await expect(pane.getByTestId("chat-replying")).toBeVisible();
  // Focus is on the thread, not the box: Esc still gives the reply up.
  await pane.getByTestId("chat-stream").focus();
  await buyer.keyboard.press("Escape");
  await expect(pane.getByTestId("chat-replying")).toHaveCount(0);

  // With a menu open, Esc closes the menu and leaves the reply alone.
  await pane
    .getByTestId("chat-message")
    .first()
    .getByRole("button", { name: /^More/ })
    .click();
  await pane
    .getByTestId("chat-more")
    .getByRole("button", { name: "Reply" })
    .click();
  await pane
    .getByTestId("chat-message")
    .first()
    .getByRole("button", { name: /^More/ })
    .click();
  await expect(pane.getByTestId("chat-more")).toBeVisible();
  await buyer.keyboard.press("Escape");
  await expect(pane.getByTestId("chat-more")).toHaveCount(0);
  await expect(pane.getByTestId("chat-replying")).toBeVisible();
  await buyer.keyboard.press("Escape");
  await expect(pane.getByTestId("chat-replying")).toHaveCount(0);

  // A waiting file, with focus on the thread.
  await pane
    .getByTestId("chat-file")
    .setInputFiles({ name: "bags.png", mimeType: "image/png", buffer: PNG });
  await expect(pane.getByTestId("chat-attaching")).toBeVisible();
  await pane.getByTestId("chat-stream").focus();
  await buyer.keyboard.press("Escape");
  await expect(pane.getByTestId("chat-attaching")).toHaveCount(0);
});

test("an iPhone's HEIC photo is made a JPEG where the browser can read it, and explained where it cannot", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const heic = {
    name: "IMG_0042.HEIC",
    mimeType: "image/heic",
    buffer: Buffer.from("not really a picture"),
  };

  // This browser cannot read HEIC: what to do, in words.
  await pane.getByTestId("chat-file").setInputFiles(heic);
  await expect(pane.getByText(/cannot read HEIC photos/)).toBeVisible();
  await expect(pane.getByTestId("chat-attaching")).toHaveCount(0);

  // Safari can: here the decoder is stood in for, and the photo is a JPEG.
  await buyer.evaluate(() => {
    const real = window.createImageBitmap.bind(window);
    window.createImageBitmap = (src: ImageBitmapSource) => {
      if (src instanceof File && /hei[cf]/i.test(src.type)) {
        const c = document.createElement("canvas");
        c.width = 40;
        c.height = 30;
        c.getContext("2d")?.fillRect(0, 0, 40, 30);
        return real(c);
      }
      return real(src);
    };
  });
  await pane.getByTestId("chat-file").setInputFiles(heic);
  await expect(pane.getByTestId("chat-attaching")).toContainText(
    "Photo: IMG_0042.jpg",
  );
  await expect(
    pane
      .getByTestId("chat-attaching")
      .getByRole("img", { name: "Preview of IMG_0042.jpg" }),
  ).toBeVisible();
});

test("the emoji panel has the whole of Unicode's, by group, and finds as people here say it", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const box = pane.getByLabel("Message");
  await pane.getByTestId("emoji-button").click();
  const panel = pane.getByTestId("emoji-panel");
  await expect(panel.getByLabel("Search emoji")).toBeVisible(); // the list is loaded when it is first opened
  // Nine groups, and every group is large.
  const groups = panel
    .getByRole("group", { name: "Kinds of emoji" })
    .getByRole("button");
  await expect(groups).toHaveCount(9);
  let total = 0;
  for (let i = 0; i < 9; i++) {
    await groups.nth(i).click();
    const n = await panel.locator(".emoji").count();
    expect(n).toBeGreaterThan(60);
    total += n;
  }
  expect(total).toBeGreaterThan(1700);
  // The flags, and a big group, pass axe.
  await panel.getByRole("button", { name: "Flags", exact: true }).click();
  await expect(
    panel.getByRole("button", { name: "flag: Nigeria" }),
  ).toBeVisible();
  await axeBoth(buyer, "the flags");
  await panel.getByRole("button", { name: "People", exact: true }).click();
  await axeBoth(buyer, "people and body");
  // As people here say it.
  await panel.getByLabel("Search emoji").fill("okada");
  await panel.getByRole("button", { name: "motorcycle" }).click();
  await panel.getByLabel("Search emoji").fill("naija");
  await panel.getByRole("button", { name: "flag: Nigeria" }).click();
  await expect(box).toHaveValue("🏍️🇳🇬");
  // A longer emoji takes its tone after the person.
  await panel.getByLabel("Search emoji").fill("woman cook");
  await panel.getByRole("button", { name: "Medium skin tone" }).click();
  await expect(panel.getByRole("button", { name: "woman cook" })).toHaveText(
    "👩🏽‍🍳",
  );
});

declare global {
  interface Window {
    __dt: DataTransfer;
  }
}

test("an emoji put in the box tells the other side we are typing, as typing does (the shared composer)", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  await buyer.goto(`/chat?with=${link}`);
  let told = 0;
  await buyer.route(`**/v1/link-chats/${link}/typing`, (route) => {
    told++;
    return route.fulfill({ status: 204 });
  });
  const pane = buyer.getByTestId("chat-pane");
  await pane.getByTestId("emoji-button").click();
  await pane.getByTestId("emoji-panel").locator(".emoji").first().click();
  await expect.poll(() => told).toBe(1);
  await expect(pane.getByLabel("Message")).not.toHaveValue("");
});

test("a message that was not sent keeps its two icon buttons on the screen, beside the bubble, on a phone: text and a photo with a caption, light and dark", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  await buyer.setViewportSize({ width: 360, height: 740 });
  await buyer.route(`**/v1/link-chats/${link}/messages`, (route) =>
    route.request().method() === "POST"
      ? route.abort("connectionfailed")
      : route.continue(),
  );
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane
    .getByLabel("Message")
    .fill("A long message that wraps onto several lines so the bubble is as wide as it can be on a phone");
  await pane.getByLabel("Message").press("Enter");
  await pane
    .getByTestId("chat-file")
    .setInputFiles({ name: "bags.png", mimeType: "image/png", buffer: PNG });
  await pane.getByLabel("Message").fill("The new bags");
  await pane.getByLabel("Message").press("Enter");
  const failed = pane.getByTestId("chat-failed");
  await expect(failed).toHaveCount(2);
  for (const scheme of ["light", "dark"] as const) {
    await buyer.evaluate((t) => {
      if (t === "dark") document.documentElement.dataset.theme = "dark";
      else delete document.documentElement.dataset.theme;
    }, scheme);
    await buyer.waitForTimeout(300);
    for (let i = 0; i < 2; i++) {
      const row = failed.nth(i);
      const bubble = row.locator("xpath=ancestor::*[@data-testid='chat-message'][1]");
      const b = must(await bubble.boundingBox());
      for (const name of ["Retry", "Delete"]) {
        const btn = row.getByRole("button", { name });
        await expect(btn).toBeVisible();
        const r = must(await btn.boundingBox());
        expect(r.x, `${name} starts on the screen`).toBeGreaterThanOrEqual(0);
        expect(r.x + r.width, `${name} is left of the bubble`).toBeLessThanOrEqual(b.x + 0.5);
        expect(r.width).toBeGreaterThanOrEqual(32);
      }
    }
    await axeBoth(buyer, "failed messages");
  }
  await buyer.screenshot({ path: "/tmp/claude-0/-home-user-taskier/ee15d754-4291-5a7a-bc3f-fed0af824ab6/scratchpad/failed-360.png" });
  expect(
    await buyer.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1),
  ).toBe(false);
});

test("after an emoji is picked, going to the search box keeps the typing there, however slow the screen", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane.getByTestId("emoji-button").click();
  const panel = pane.getByTestId("emoji-panel");
  const search = panel.getByLabel("Search emoji");
  await expect(search).toBeVisible();
  // A slow screen: the message box's focus after the pick comes late.
  await buyer.evaluate(() => {
    const raf = window.requestAnimationFrame.bind(window);
    window.requestAnimationFrame = (cb) => (setTimeout(() => raf(cb), 400), 0);
  });
  await search.fill("okada");
  await panel.getByRole("button", { name: "motorcycle" }).click();
  await search.click();
  await buyer.waitForTimeout(700);
  await buyer.keyboard.type(" bike");
  await expect(search).toHaveValue("okada bike");
  await expect(pane.getByLabel("Message")).toHaveValue("🏍️");
});
