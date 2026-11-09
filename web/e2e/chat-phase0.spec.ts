import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Five more flows, phase 0 (docs/spec/chat-five-more-flows.md): the chat's
// tools, motion and tables put right before the new flows add to them.
// The tools menu fits the screen and rests on the message bar; a sheet
// opened from a tile arrives in one motion; a tap draws no box; the
// message box's hint is one line; the Tables tool shows a table with a
// pine header, search and pages. serve.sh keeps no data tables, so the
// table is answered here and what is asked for is checked.

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
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@zero.test`,
      name: "Zuri Owner",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
  await page.request.put("/v1/settings/areas", {
    headers: H,
    data: {
      areas: {
        storefront: true,
        orders: true,
        stock: true,
        money: true,
        links: true,
      },
    },
  });
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

for (const vp of [
  { width: 360, height: 640 },
  { width: 390, height: 844 },
]) {
  test.describe(`at ${vp.width} × ${vp.height}`, () => {
    test.use({
      viewport: vp,
      hasTouch: true,
      isMobile: true,
      reducedMotion: "no-preference",
    });

    test("the tools menu fits above the message bar, a sheet rises once, and a tap draws no box", async ({
      page,
    }) => {
      await owner(page);
      await page.goto("/chat?with=taskiem");
      const pane = page.getByTestId("taskiem-pane");
      // The hint is one line on a phone.
      await expect(pane.getByLabel("Message")).toHaveAttribute(
        "placeholder",
        /^(Ask Taskiem|Type a message)$/,
      );
      await page.getByTestId("tools-button").tap();
      const panel = page.getByTestId("tools-panel");
      await expect(panel.getByRole("heading", { name: "Tools" })).toBeVisible();
      await page.waitForTimeout(600);
      const geo = await page.evaluate(() => {
        const p = document
          .querySelector('[data-testid="tools-panel"]')
          ?.getBoundingClientRect();
        const bar = document
          .querySelector(".composer")
          ?.getBoundingClientRect();
        const tiles = [...document.querySelectorAll(".tk-tools .tk-tile")].map(
          (t) => t.getBoundingClientRect(),
        );
        return {
          top: p?.top ?? -1,
          bottom: p?.bottom ?? -1,
          bar: bar?.top ?? -1,
          last: tiles.at(-1)?.bottom ?? 1e9,
          tiles: tiles.length,
        };
      });
      // Its heading and every tile on the screen, resting on the bar.
      expect(geo.tiles).toBeGreaterThan(6);
      expect(geo.top).toBeGreaterThanOrEqual(0);
      expect(geo.last).toBeLessThanOrEqual(geo.bar);
      expect(Math.abs(geo.bottom - geo.bar)).toBeLessThanOrEqual(1);
      // No box over what is tapped.
      const highlight = await page.evaluate(() =>
        [".tk-tile", '[data-testid="tools-button"]', "a[href]"].map((s) => {
          const el = document.querySelector(s);
          return el
            ? getComputedStyle(el).getPropertyValue(
                "-webkit-tap-highlight-color",
              )
            : "rgba(0, 0, 0, 0)";
        }),
      );
      expect(highlight.every((c) => c === "rgba(0, 0, 0, 0)")).toBe(true);
      await fits(page);
      await axeBoth(page, "the tools menu");
      // A sheet from a tile: its top edge, every frame, never leaves its
      // place once it has reached it (it used to drop and rise again).
      await page.evaluate(() => {
        const w = window as unknown as { tops: number[] };
        w.tops = [];
        const t0 = performance.now();
        const tick = () => {
          const s = document.querySelector(".tk-sheet");
          if (s) w.tops.push(Math.round(s.getBoundingClientRect().top));
          if (performance.now() - t0 < 1500) requestAnimationFrame(tick);
        };
        requestAnimationFrame(tick);
      });
      await panel.getByRole("button", { name: /Products/ }).tap();
      await expect(
        page.getByRole("dialog", { name: /Products/ }),
      ).toBeVisible();
      await page.waitForTimeout(1700);
      const tops = await page.evaluate(
        () => (window as unknown as { tops: number[] }).tops,
      );
      const rest = tops.at(-1) ?? 0;
      const arrived = tops.findIndex((t) => Math.abs(t - rest) <= 2);
      expect(arrived).toBeGreaterThanOrEqual(0);
      expect(
        tops.slice(arrived).every((t) => Math.abs(t - rest) <= 3),
        `the sheet's top, frame by frame: ${tops.join(" ")}`,
      ).toBe(true);
      // Nor does it start off the screen.
      expect(Math.min(...tops)).toBeGreaterThanOrEqual(0);
    });

    test("the Tables tool shows a table with a pine header, search and pages", async ({
      page,
    }) => {
      await owner(page);
      const table = {
        id: "t-orders",
        name: "orders",
        title: "Orders",
        row_count: 60,
        columns: [
          { name: "customer", title: "Customer", type: "text", required: true },
          { name: "status", title: "Status", type: "text" },
          { name: "total_kobo", title: "Total", type: "integer" },
          { name: "paid", title: "Paid", type: "boolean" },
          { name: "due_on", title: "Due", type: "date" },
          { name: "note", title: "Note", type: "text" },
        ],
      };
      const all = Array.from({ length: 60 }, (_, i) => ({
        id: 60 - i,
        customer: `Customer ${60 - i}`,
        status: ["Paid", "Pending", "Cancelled"][i % 3],
        total_kobo: 1250000 + i * 1000,
        paid: i % 3 === 0,
        due_on: "2026-11-20",
        note: i === 0 ? "Deliver to Yaba before 1pm, gate 2" : "",
      }));
      const asked: Record<string, unknown>[] = [];
      await page.route("**/v1/tables", (r) =>
        r.fulfill(json({ tables: [table] })),
      );
      await page.route("**/v1/tables/t-orders/rows/query", (r) => {
        const body = r.request().postDataJSON() as {
          cursor?: string;
          filters?: { column: string; value: string }[];
        };
        asked.push(body);
        const want = body.filters?.[0]?.value.replace(/%/g, "").toLowerCase();
        const hit = want
          ? all.filter((x) => x.customer.toLowerCase().includes(want))
          : all;
        const from = Number(body.cursor ?? 0);
        const page = hit.slice(from, from + 25);
        return r.fulfill(
          json({
            rows: page,
            next_cursor: from + 25 < hit.length ? String(from + 25) : null,
            count: hit.length,
          }),
        );
      });
      await page.goto("/chat?with=taskiem");
      const box = page.getByTestId("taskiem-pane").getByLabel("Message");
      await box.fill("/tab");
      // The tools are on once their list shows (the setting loads first).
      await expect(
        page
          .getByRole("region", { name: "Tools" })
          .getByRole("button", { name: /Tables/ }),
      ).toBeVisible();
      await box.press("Enter");
      await page.keyboard.press("Enter");
      await page
        .getByRole("dialog", { name: /Tables/ })
        .getByRole("button", { name: /Orders/ })
        .click();
      const sheet = page.getByRole("dialog", { name: /Orders/ });
      const rows = sheet.getByRole("region", { name: "Orders: its rows" });
      await expect(rows.getByRole("columnheader")).toHaveText([
        "Customer",
        "Status",
        "Total",
        "Paid",
        "Due",
        "Note",
      ]);
      // A pine header; naira; pills for status and yes/no; the date.
      const head = await rows
        .getByRole("columnheader", { name: "Customer" })
        .evaluate((el) => getComputedStyle(el).backgroundColor);
      const pine = await page.evaluate(() => {
        const p = document.createElement("span");
        p.style.background = "var(--brand)";
        document.body.append(p);
        const c = getComputedStyle(p).backgroundColor;
        p.remove();
        return c;
      });
      expect(head).toBe(pine);
      const first = rows.getByRole("row", { name: /Customer 60/ });
      await expect(first).toContainText("₦12,500");
      await expect(first.locator(".tk-pill")).toHaveText(["Paid", "Yes"]);
      await expect(first).toContainText("20 Nov 2026");
      // Pages of 25.
      const pages = sheet.getByRole("navigation", { name: "Pages" });
      await expect(pages.getByRole("status")).toHaveText("1–25 of 60");
      await expect(
        pages.getByRole("button", { name: "Previous page" }),
      ).toBeDisabled();
      await pages.getByRole("button", { name: "Next page" }).click();
      await expect(pages.getByRole("status")).toHaveText("26–50 of 60");
      await expect(
        rows.getByRole("row", { name: /Customer 35/ }),
      ).toBeVisible();
      await pages.getByRole("button", { name: "Previous page" }).click();
      await expect(pages.getByRole("status")).toHaveText("1–25 of 60");
      // The table scrolls inside its frame; the page never sideways.
      await fits(page);
      await axeBoth(page, "a table in the Tables tool");
      // Search: by the first text column, the page back to the first.
      await sheet
        .getByRole("searchbox", { name: "Search rows" })
        .fill("customer 5");
      await expect(pages.getByRole("status")).toHaveText("1–11 of 11");
      expect(asked.at(-1)).toMatchObject({
        filters: [{ column: "customer", op: "ilike", value: "%customer 5%" }],
        limit: 25,
        count: "exact",
      });
      await expect(
        sheet.getByRole("combobox", { name: "Search in" }),
      ).toHaveValue("customer");
      // A row opens to be changed.
      await rows.getByRole("button", { name: "Customer 59" }).click();
      await expect(
        sheet.getByRole("textbox", { name: /^Customer/ }),
      ).toHaveValue("Customer 59");
    });
  });
}

test("on a computer the hint is whole", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await owner(page);
  await page.goto("/chat?with=taskiem");
  await expect(
    page.getByTestId("taskiem-pane").getByLabel("Message"),
  ).toHaveAttribute(
    "placeholder",
    /^(Ask Taskiem, or @ to tag|Type a message)$/,
  );
});
