import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// The composer, as WhatsApp Web's: a message shows at once with a clock and
// is retried without ever being sent twice (instant send); and the rest of
// what a WhatsApp user does at the bottom of the screen.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };
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
async function linked(browser: Browser) {
  const stamp = letters();
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, `Dangote Depot ${stamp}`);
  const buyer = await (
    await browser.newContext({ viewport: { width: 390, height: 844 } })
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
    panel.getByRole("button", { name: "grinning face" }),
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
  expect(sent[0]!.body).toContain('filename="pasted.png"');
  expect(sent[0]!.body).toMatch(/name="client_id"/);

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
      const ctx = c.getContext("2d")!;
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

declare global {
  interface Window {
    __dt: DataTransfer;
  }
}
