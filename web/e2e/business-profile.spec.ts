import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Your business in Explore: the words are saved on the real server and kept;
// the logo and the photos (the test server has no photo storage) run against
// a stand-in for the photo routes, which checks what the page asks for: the
// count, the order, a caption on leaving its box, a removal, and twelve at
// most. The real uploads, limits and privacy are in api/business_profile_test.go.
test.use({ bypassCSP: true, viewport: { width: 1280, height: 900 } });

const H = { "X-Taskiem-Request": "1" };
const letters = () =>
  String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
  "base64",
);

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: `Dangote Depot ${letters()}`,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@profile.test`,
      name: "Dangote Owner",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
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

test("the words are saved and kept; the page is reached from Chat and from Linked businesses; accessible in both themes", async ({
  page,
}) => {
  await signUp(page);
  await page.goto("/chat");
  await page.getByRole("button", { name: "More about the chats" }).click();
  await page.getByRole("link", { name: "Your business in Explore" }).click();
  await expect(page).toHaveURL(/\/settings\/links\/profile$/);
  await expect(
    page.getByRole("heading", { name: "Your business in Explore" }),
  ).toBeVisible();

  // Nothing said yet: a letter where the logo goes, and the card says so.
  await expect(page.getByTestId("profile-name")).toHaveValue("");
  await expect(page.getByTestId("profile-save")).toBeDisabled();
  await expect(page.getByTestId("profile-count")).toContainText(
    "0 of 12 photos",
  );

  await page.getByTestId("profile-name").fill("Dangote Cement & Sons");
  await page.getByTestId("profile-tagline").fill("Cement and sand, delivered");
  await page.getByTestId("profile-area").fill("Ikeja");
  await page.getByTestId("profile-about").fill("Family-run since 1998.");
  await expect(
    page.getByRole("group", { name: "How your card looks" }),
  ).toContainText("Dangote Cement & Sons");
  await page.getByTestId("profile-save").click();
  await expect(page.getByTestId("profile-save")).toBeDisabled();
  await page.reload();
  await expect(page.getByTestId("profile-name")).toHaveValue(
    "Dangote Cement & Sons",
  );
  await expect(page.getByTestId("profile-about")).toHaveValue(
    "Family-run since 1998.",
  );
  // Being found is switched on here, and kept.
  const found = page.getByTestId("profile-findable");
  await expect(found).not.toBeChecked();
  await found.click();
  await expect(found).toBeChecked();
  await page.reload();
  await expect(page.getByTestId("profile-findable")).toBeChecked();
  await axeBoth(page, "your business in Explore");

  // From Linked businesses too.
  await page.goto("/settings/links");
  await page
    .getByRole("link", { name: "Edit your business in Explore" })
    .click();
  await expect(page).toHaveURL(/\/settings\/links\/profile$/);
});

test("photos: added, counted, captioned, put in order, removed; twelve at most; a phone fits", async ({
  page,
}) => {
  await signUp(page);
  // A stand-in for the photo routes.
  type G = {
    id: string;
    caption: string;
    photo: {
      id: string;
      width: number;
      height: number;
      urls: Record<string, string>;
      moderation: string;
    };
  };
  const gallery: G[] = [];
  const log: string[] = [];
  let n = 0;
  const view = () => ({
    name: "",
    tagline: "",
    area: "",
    about: "",
    logo: null,
    gallery,
    max_gallery: 12,
    delivers: false,
    vehicle: "",
    delivery_areas: [],
  });
  const json = (route: Route, body: unknown, status = 200) =>
    route.fulfill({
      status,
      contentType: "application/json",
      body: JSON.stringify(body),
    });
  await page.route("**/v1/business-profile/image**", (r) =>
    r.fulfill({ status: 200, contentType: "image/png", body: PNG }),
  );
  await page.route("**/v1/business-profile/gallery**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (req.method() === "POST") {
      n++;
      log.push("add");
      gallery.push({
        id: `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`,
        caption: "",
        photo: {
          id: `m${n}`,
          width: 1,
          height: 1,
          moderation: "unreviewed",
          urls: { "320": `https://x/${n}.png` },
        },
      });
      return json(route, view(), 201);
    }
    if (req.method() === "PUT") {
      const ids = (req.postDataJSON() as { ids: string[] }).ids;
      log.push("order " + ids.map((i) => Number(i.slice(-12))).join(","));
      gallery.sort((a, b) => ids.indexOf(a.id) - ids.indexOf(b.id));
      return json(route, view());
    }
    const id = path.split("/").pop() ?? "";
    const at = gallery.findIndex((g) => g.id === id);
    if (req.method() === "PATCH" && gallery[at]) {
      gallery[at].caption = (req.postDataJSON() as { caption: string }).caption;
      log.push("caption " + gallery[at].caption);
      return json(route, view());
    }
    if (req.method() === "DELETE" && at >= 0) {
      gallery.splice(at, 1);
      log.push("remove");
      return json(route, view());
    }
    return route.continue();
  });
  await page.route(/\/v1\/business-profile$/, (route) =>
    route.request().method() === "GET" ? json(route, view()) : route.continue(),
  );

  await page.goto("/settings/links/profile");
  const count = page.getByTestId("profile-count");
  await expect(count).toContainText("0 of 12 photos");
  const input = page.getByTestId("profile-photo-input");
  await input.setInputFiles([
    { name: "a.png", mimeType: "image/png", buffer: PNG },
    { name: "b.png", mimeType: "image/png", buffer: PNG },
    { name: "c.png", mimeType: "image/png", buffer: PNG },
  ]);
  await expect(count).toContainText("3 of 12 photos");
  const tiles = page.getByTestId("profile-photo");
  await expect(tiles).toHaveCount(3);

  // The first cannot go earlier, the last cannot go later.
  await expect(
    page.getByRole("button", { name: "Move photo 1 earlier" }),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "Move photo 3 later" }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Move photo 3 earlier" }).click();
  await expect.poll(() => log.at(-1)).toBe("order 1,3,2");

  // A caption is saved when its box is left.
  const box = page.getByLabel("Caption for photo 1");
  await box.fill("The shop front");
  await box.blur();
  await expect.poll(() => log.at(-1)).toBe("caption The shop front");
  await expect(page.getByLabel("Caption for photo 1")).toHaveValue(
    "The shop front",
  );

  // Typing a caption and going straight to Remove: the caption's save (on
  // leaving the box) must not swallow the click.
  await page.getByLabel("Caption for photo 2").fill("Delete me");
  await page.getByRole("button", { name: "Remove photo 2" }).click();
  await expect
    .poll(() => log.slice(-2).join("|"))
    .toBe("caption Delete me|remove");
  await expect(count).toContainText("2 of 12 photos");
  await axeBoth(page, "the gallery");

  // Twelve at most: the button gives up.
  for (let i = 2; i < 12; i++)
    gallery.push({
      id: `00000000-0000-4000-8000-${String(100 + i).padStart(12, "0")}`,
      caption: "",
      photo: {
        id: `p${i}`,
        width: 1,
        height: 1,
        moderation: i === 5 ? "rejected" : "unreviewed",
        urls: { "320": `https://x/p${i}.png` },
      },
    });
  await page.reload();
  await expect(count).toContainText("12 of 12 photos");
  await expect(page.getByRole("button", { name: "Add a photo" })).toBeDisabled();
  // A photo the moderator refused says so, and can be removed.
  await expect(page.getByText("Not accepted for a public page")).toHaveCount(1);

  // On a phone it fits, nothing scrolls sideways.
  await page.setViewportSize({ width: 360, height: 740 });
  await expect(tiles.first()).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth + 1,
    ),
  ).toBe(false);
});
