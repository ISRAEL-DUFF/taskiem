import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Device fields D1 (docs/spec/device-fields.md §4): a form step on the
// web asks the browser at the tap, after the field's why. Its location is
// the browser's own prompt, allowed or refused (a refusal is an answer); a
// photo is the camera where the browser offers it, else a file, marked
// sent. The request and its answer are answered here
// (api/forms_device_d1_test.go reads the real ones); the Confirm card
// shows the delivery's proof.
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () =>
  String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 200) => ({
  status,
  contentType: "application/json",
  body: JSON.stringify(body),
});
const RID = "0192e2e0-0000-7000-8000-00000000d1d1";

async function owner(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: `Musa Rides ${letters()}`,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@device.test`,
      name: "Musa Bello",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

const request = {
  id: RID,
  run_id: "0192e2e0-0000-7000-8000-00000000d1d2",
  title: "Delivered",
  description: null,
  role: "",
  from: "Ada Stores",
  status: "pending",
  created_at: new Date(Date.now() - 60_000).toISOString(),
  expires_at: new Date(Date.now() + 3_600_000).toISOString(),
  schema: {
    type: "object",
    required: ["where", "proof"],
    properties: {
      where: {
        type: "location",
        why: "so the delivery is recorded where it was handed over",
      },
      proof: {
        type: "photo",
        why: "a photo of the parcel at the door, for the shop's records",
      },
    },
  },
};

/** The request, and what the page sent as its answer. */
async function answer(page: Page) {
  const sent: { type: string; body: string }[] = [];
  await page.route(`**/v1/form-requests/${RID}`, (r) =>
    r.fulfill(json(request)),
  );
  await page.route(`**/v1/form-requests/${RID}/answers`, async (r: Route) => {
    const q = r.request();
    sent.push({
      type: q.headers()["content-type"] ?? "",
      body: q.postDataBuffer()?.toString("latin1") ?? "",
    });
    await r.fulfill(
      json(
        {
          submission: {
            id: "s1",
            reference: "F-D1D1",
            status: "answered",
            channel: "app",
            answers: {},
            respondent: { kind: "linked" },
            created_at: new Date().toISOString(),
          },
        },
        201,
      ),
    );
  });
  return sent;
}

const photo = {
  name: "door.jpg",
  mimeType: "image/jpeg",
  buffer: Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46]),
};

test("a form step's location allowed, and a photo chosen marked sent", async ({
  page,
  context,
}) => {
  await owner(page);
  const sent = await answer(page);
  await context.grantPermissions(["geolocation"]);
  await context.setGeolocation({
    latitude: 6.4281,
    longitude: 3.4219,
    accuracy: 9,
  });
  await page.goto(`/forms/requests/${RID}`);
  await expect(page.getByRole("heading", { name: "Delivered" })).toBeVisible();
  // The why is read before the browser is asked; nothing is asked yet.
  const where = page.getByRole("group", { name: /Where/ });
  await expect(
    where.getByText("Why: so the delivery is recorded"),
  ).toBeVisible();
  await expect(page.getByText(/Asked by Ada Stores/)).toBeVisible();
  // Sending with nothing answered holds the form.
  await page.getByRole("button", { name: "Send" }).click();
  await expect(
    where.getByText("Answer this, or press I can't answer this."),
  ).toBeVisible();
  await where.getByRole("button", { name: "Use my location" }).click();
  await expect(where.getByText("Location read, to within 9 m.")).toBeVisible();
  const proof = page.getByRole("group", { name: /Proof/ });
  const input = proof.getByLabel("Proof: take or choose a photo");
  await expect(input).toHaveAttribute("capture", "environment");
  await input.setInputFiles(photo);
  await expect(
    proof.getByText("marked as sent, not taken at the tap"),
  ).toBeVisible();
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const vs = await axe(page);
    expect(
      vs,
      describe(`the form step with device fields (${scheme})`, vs),
    ).toEqual([]);
  }
  await page.getByRole("button", { name: "Send" }).click();
  await expect(
    page.getByText("Thank you. The workflow carries on"),
  ).toBeVisible();
  expect(sent).toHaveLength(1);
  expect(sent[0]?.type).toContain("multipart/form-data");
  expect(sent[0]?.body).toContain('name="proof"; filename="door.jpg"');
  expect(sent[0]?.body).toMatch(
    /"where":\{"lat":6\.4281,"lng":3\.4219,"at":"[^"]+","accuracy_m":9\}/,
  );
  expect(sent[0]?.body).toContain('"app":"web"');
});

test("a form step's location refused is an answer", async ({
  page,
  context,
}) => {
  await owner(page);
  const sent = await answer(page);
  await context.clearPermissions(); // the browser says no to the prompt
  await page.goto(`/forms/requests/${RID}`);
  const where = page.getByRole("group", { name: /Where/ });
  await where.getByRole("button", { name: "Use my location" }).click();
  await expect(
    where.getByText(/was not allowed to read where you are/),
  ).toBeVisible();
  await expect(
    where.getByText("You said you cannot answer this.", { exact: true }),
  ).toBeVisible();
  // The photo: the person cannot answer it either.
  await page
    .getByRole("group", { name: /Proof/ })
    .getByRole("button", { name: "I can't answer this" })
    .click();
  await page.getByRole("button", { name: "Send" }).click();
  await expect(
    page.getByText("Thank you. The workflow carries on"),
  ).toBeVisible();
  expect(sent[0]?.type).toContain("application/json");
  const body = JSON.parse(sent[0]?.body ?? "{}") as {
    answers: Record<string, { state: string }>;
  };
  expect(body.answers.where?.state).toBe("refused");
  expect(body.answers.proof?.state).toBe("refused");
});

test("the Confirm card shows the pin and the photo, and how each was got", async ({
  page,
}) => {
  await owner(page);
  await page.request.put("/v1/settings/chat-tools", {
    headers: H,
    data: { chat_tools: true },
  });
  const png =
    "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==";
  await page.route("**/v1/chat-alerts", (r) =>
    r.fulfill(
      json({
        alerts: [
          {
            key: "confirm:d1",
            kind: "confirm",
            at: new Date().toISOString(),
            title: "Confirm AD-7Q4K2P, delivered by Musa Bello",
            line: "₦1,000 fee waits on your word · location sent by the rider · photo sent",
            id: "d1",
            reference: "AD-7Q4K2P",
            amount_kobo: 100_000,
            proof: {
              where: { lat: 6.428, lng: 3.422, source: "sent" },
              mark: "said_by_rider",
              photo_url: png,
              photo_source: "camera",
            },
          },
        ],
      }),
    ),
  );
  await page.goto("/chat?with=taskiem");
  const proof = page.getByTestId("delivery-proof");
  await expect(
    proof.getByRole("img", { name: "The photo of delivery AD-7Q4K2P" }),
  ).toBeVisible();
  const pin = proof.getByRole("link", { name: /Location sent by the rider/ });
  await expect(pin).toHaveAttribute(
    "href",
    "https://www.google.com/maps?q=6.428,3.422",
  );
  await expect(proof.getByText("Photo taken in the app")).toBeVisible();
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const vs = await axe(page);
    expect(
      vs,
      describe(`the Confirm card with its proof (${scheme})`, vs),
    ).toEqual([]);
  }
});
