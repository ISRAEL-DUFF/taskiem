import { expect, test, type Page } from "@playwright/test";

// The app's content policy allows no blob: images (img-src 'self' data:), and
// the other chat specs run with it off so axe can be injected. This one runs
// with it ON: a picture waiting for its caption, a photo being sent and the
// camera must all work under the real policy, with nothing refused.
test.use({ bypassCSP: false, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };
const letters = () =>
  String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
  "base64",
);

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

test("a picture to send, a photo being sent and the camera, under the real content policy", async ({
  browser,
}) => {
  const stamp = letters();
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, `Dangote Depot ${stamp}`);
  const ctx = await browser.newContext({
    viewport: { width: 390, height: 844 },
  });
  const buyer = await ctx.newPage();
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

  const refused: string[] = [];
  buyer.on(
    "console",
    (m) =>
      /Content Security Policy|violates/i.test(m.text()) &&
      refused.push(m.text()),
  );
  buyer.on(
    "pageerror",
    (e) =>
      /Content Security Policy/i.test(e.message) && refused.push(e.message),
  );
  await buyer.addInitScript(() => {
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
  // The test server has no malware scanner: the file route is answered as the API does.
  await buyer.route(`**/v1/link-chats/${link}/files`, (route) =>
    route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({
        message: {
          id: "0192e2e0-0000-7000-8000-0000000000f2",
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
    }),
  );
  await buyer.route(`**/v1/link-chats/${link}/files/*`, (route) =>
    route.fulfill({ status: 200, contentType: "image/png", body: PNG }),
  );

  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const box = pane.getByLabel("Message");

  // A picture waiting for its caption is shown, not just named.
  await pane
    .getByTestId("chat-file")
    .setInputFiles({ name: "bags.png", mimeType: "image/png", buffer: PNG });
  const waiting = pane.getByTestId("chat-attaching");
  await expect(waiting).toContainText("Photo: bags.png");
  const preview = waiting.getByRole("img", { name: "Preview of bags.png" });
  await expect(preview).toBeVisible();
  expect(
    await preview.evaluate(
      (el: HTMLImageElement) => el.complete && el.naturalWidth,
    ),
  ).toBeGreaterThan(0);

  // Sent: its own preview shows at once, then the server's copy.
  await box.fill("The new bags");
  await box.press("Enter");
  const mine = pane.locator('[data-testid="chat-message"][data-mine="true"]');
  await expect(mine.getByRole("img", { name: "Your photo" })).toBeVisible();
  expect(
    await mine
      .getByRole("img", { name: "Your photo" })
      .evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth),
  ).toBeGreaterThan(0);

  // The camera, and the photo it takes.
  await pane.getByTestId("attach").click();
  await pane
    .getByTestId("attach-menu")
    .getByRole("button", { name: "Camera" })
    .click();
  const sheet = buyer.getByRole("dialog", { name: "Take a photo" });
  await expect(sheet.getByRole("button", { name: "Take photo" })).toBeEnabled();
  await sheet.getByRole("button", { name: "Take photo" }).click();
  const taken = pane
    .getByTestId("chat-attaching")
    .getByRole("img", { name: /Preview of photo-\d+\.jpg/ });
  await expect(taken).toBeVisible();
  expect(
    await taken.evaluate(
      (el: HTMLImageElement) => el.complete && el.naturalWidth,
    ),
  ).toBeGreaterThan(0);

  expect(refused).toEqual([]);
});
