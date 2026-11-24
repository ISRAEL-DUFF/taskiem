import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Device fields, build-plan-gaps H3: a code scanned with the camera where
// the browser has a barcode reader (BarcodeDetector, stood in here; no
// outside library), typed elsewhere; and a members' tool that takes a
// location, a photo and a signature on its sheet, the photo and the pad's
// drawing sent first and named by their ids in the start.
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
      tenant: `Ada Stores ${letters()}`,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@h3.test`,
      name: "Ada Obi",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

/** The browser's barcode reader and a camera, stood in: the reader finds
 * an EAN-13 on the first frame. */
async function barcodeReader(page: Page) {
  await page.addInitScript(() => {
    class Reader {
      static getSupportedFormats() {
        return Promise.resolve(["qr_code", "ean_13"]);
      }
      detect() {
        return Promise.resolve([{ rawValue: "6151100093217", format: "ean_13" }]);
      }
    }
    (window as unknown as { BarcodeDetector: unknown }).BarcodeDetector = Reader;
    navigator.mediaDevices.getUserMedia = () => {
      const c = document.createElement("canvas");
      c.width = 64;
      c.height = 48;
      c.getContext("2d")?.fillRect(0, 0, 64, 48);
      return Promise.resolve(c.captureStream(5));
    };
  });
}

async function noSidewaysScroll(page: Page) {
  await page.setViewportSize({ width: 390, height: 844 });
  const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(over).toBeLessThanOrEqual(0);
}

const RID = "0192e2e0-0000-7000-8000-0000000000a3";

test("a form step's code scanned with the camera where the browser can read one", async ({ page }) => {
  await owner(page);
  await barcodeReader(page);
  const sent: string[] = [];
  await page.route(`**/v1/form-requests/${RID}`, (r) =>
    r.fulfill(
      json({
        id: RID,
        run_id: "0192e2e0-0000-7000-8000-0000000000b3",
        title: "Count the shelf",
        description: null,
        role: "",
        from: "Ada Stores",
        status: "pending",
        created_at: new Date(Date.now() - 60_000).toISOString(),
        expires_at: new Date(Date.now() + 3_600_000).toISOString(),
        schema: {
          type: "object",
          required: ["item"],
          properties: { item: { type: "scan", why: "the code on the item's pack", formats: ["ean_13"] } },
        },
      }),
    ),
  );
  await page.route(`**/v1/form-requests/${RID}/answers`, async (r: Route) => {
    sent.push(r.request().postData() ?? "");
    await r.fulfill(
      json({ submission: { id: "s1", reference: "F-H3", status: "answered", channel: "app", answers: {}, respondent: { kind: "member" }, created_at: new Date().toISOString() } }, 201),
    );
  });
  await page.goto(`/forms/requests/${RID}`);
  const item = page.getByRole("group", { name: /Item/ });
  await expect(item.getByText("Why: the code on the item's pack")).toBeVisible();
  // Typed still works; the camera is asked only at the tap.
  await expect(item.getByLabel("Item: type the code")).toBeVisible();
  await item.getByRole("button", { name: "Scan with the camera" }).click();
  await expect(item.getByText("Code scanned: 6151100093217")).toBeVisible();
  await expect(item.getByLabel("Item: type the code")).toHaveValue("6151100093217");
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const vs = await axe(page);
    expect(vs, describe(`a form step's code scanned (${scheme})`, vs)).toEqual([]);
  }
  await noSidewaysScroll(page);
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByText("Thank you. The workflow carries on")).toBeVisible();
  expect(sent[0]).toContain('"item":{"text":"6151100093217","format":"ean_13"}');
});

test("without a barcode reader the code is typed", async ({ page }) => {
  await owner(page);
  await page.route(`**/v1/form-requests/${RID}`, (r) =>
    r.fulfill(
      json({
        id: RID,
        run_id: "0192e2e0-0000-7000-8000-0000000000b3",
        title: "Count the shelf",
        description: null,
        role: "",
        from: "Ada Stores",
        status: "pending",
        created_at: new Date().toISOString(),
        expires_at: new Date(Date.now() + 3_600_000).toISOString(),
        schema: { type: "object", properties: { item: { type: "scan", why: "the code" } } },
      }),
    ),
  );
  await page.addInitScript(() => {
    delete (window as unknown as { BarcodeDetector?: unknown }).BarcodeDetector;
  });
  await page.goto(`/forms/requests/${RID}`);
  const item = page.getByRole("group", { name: /Item/ });
  await expect(item.getByLabel("Item: type the code")).toBeVisible();
  await expect(item.getByRole("button", { name: "Scan with the camera" })).toHaveCount(0);
});

test("a site visit takes a location, a photo and a signature on the tool's sheet", async ({ page, context }) => {
  test.setTimeout(90_000);
  await owner(page);
  const definition = {
    schema: "wd/v1",
    id: "wf_visit",
    version: 1,
    name: "Add a site visit",
    trigger: { type: "manual" },
    inputs: {
      schema: {
        type: "object",
        required: ["where", "proof"],
        properties: {
          where: { type: "location", why: "so the visit is recorded where it was" },
          proof: { type: "photo", why: "a photo of the work" },
          signed: { type: "signature", why: "the site manager signs it off", ask_name: true },
          item: { type: "scan", why: "the meter's code" },
          note: { type: "string", title: "Note" },
        },
      },
    },
    outputs: [{ name: "said", type: "string", title: "Said", description: "What was recorded", value: "=steps.done.output.said" }],
    tool: { name: "Add a site visit", command: "visit", about: "Record a visit to a site", answer: { type: "text", text: "=outputs.said" } },
    steps: [{ id: "done", type: "transform", config: { output: { said: "visit" } } }],
  };
  const made = await page.request.post("/v1/workflows", { headers: H, data: { name: "Add a site visit", definition } });
  expect(made.ok(), await made.text()).toBeTruthy();
  const id = ((await made.json()) as { id: string }).id;
  const pub = await page.request.post(`/v1/workflows/${id}/versions/1/publish`, { headers: H });
  expect(pub.ok(), await pub.text()).toBeTruthy();

  // The files and the start are answered here (api/tools_device_test.go
  // takes the real ones); what the sheet sent is kept.
  const files: string[] = [];
  let start = "";
  await page.route("**/v1/tools/visit/files", async (r) => {
    const body = r.request().postDataBuffer()?.toString("latin1") ?? "";
    files.push(body);
    const field = /name="(\w+)"; filename/.exec(body)?.[1] ?? "x";
    await r.fulfill(json({ field, file: { id: `file-${field}`, name: "f", type: "image/png", size: 1, sha256: "00" } }, 201));
  });
  await page.route("**/v1/tools/visit/start", async (r) => {
    start = r.request().postData() ?? "";
    await r.fulfill(
      json({ run_id: "run-h3", care: "answers", created: true, values: [{ name: "where", label: "where", value: "your location, read now" }, { name: "proof", label: "proof", value: "a photo" }] }, 201),
    );
  });
  await page.route("**/v1/runs/run-h3/answer", (r) => r.fulfill(json({ type: "text", text: "Visit recorded", status: "completed" })));

  await context.grantPermissions(["geolocation"]);
  await context.setGeolocation({ latitude: 6.5243, longitude: 3.3792, accuracy: 12 });
  await page.goto("/tools/visit");
  const sheet = page.getByTestId("made-tool-sheet");
  const where = sheet.getByRole("group", { name: /Where/ });
  await expect(where.getByText("Why: so the visit is recorded where it was")).toBeVisible();
  await where.getByRole("button", { name: "Use my location" }).click();
  await expect(where.getByText("Location read, to within 12 m.")).toBeVisible();
  const proof = sheet.getByRole("group", { name: /Proof/ });
  await expect(proof.getByLabel("Proof: take or choose a photo")).toHaveAttribute("capture", "environment");
  await proof.getByLabel("Proof: take or choose a photo").setInputFiles({ name: "work.jpg", mimeType: "image/jpeg", buffer: Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0, 16]) });
  const signed = sheet.getByRole("group", { name: /Signed/ });
  const pad = signed.getByRole("img", { name: /sign here/ });
  const box = await pad.boundingBox();
  if (!box) throw new Error("no pad");
  await page.mouse.move(box.x + 20, box.y + 40);
  await page.mouse.down();
  await page.mouse.move(box.x + 120, box.y + 80, { steps: 6 });
  await page.mouse.up();
  await signed.getByLabel("Name of who signed").fill("Emeka Obi");
  await expect(signed.getByText("Signed by Emeka Obi.")).toBeVisible();
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const vs = await axe(page);
    expect(vs, describe(`a tool's sheet with device inputs (${scheme})`, vs)).toEqual([]);
  }
  await noSidewaysScroll(page);
  await sheet.getByRole("button", { name: "Go" }).click();
  await expect(sheet.getByTestId("tool-answer")).toContainText("Visit recorded");
  expect(files).toHaveLength(2);
  expect(files.join("")).toContain('name="proof"; filename="work.jpg"');
  expect(files.join("")).toContain('name="signed"; filename="signature.png"');
  const body = JSON.parse(start) as { input: Record<string, unknown> };
  expect(body.input.proof).toEqual({ file: "file-proof" });
  expect(body.input.signed).toEqual({ file: "file-signed", signed_by: "Emeka Obi" });
  expect(body.input.where).toMatchObject({ lat: 6.5243, lng: 3.3792, accuracy_m: 12 });
});
