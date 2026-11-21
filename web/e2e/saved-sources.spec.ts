import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";
import { addStep, doneWithStep, newWorkflow } from "./builder";

// A business's saved things as a workflow's values (saved objects S3):
// a step's value picker lists them under Saved, each field as
// "@price-list · Text", kept as saved.price_list.body; and publishing a
// workflow that reads one no longer saved says so, with the Saved page a
// tap away.
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

// SCREENSHOT_DIR=... saves screenshots for review.
const shot = async (page: Page, name: string) => {
  if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: `${process.env.SCREENSHOT_DIR}/${name}.png` });
};

test("a saved thing's field is a value a step can use", async ({ page }) => {
  test.setTimeout(120_000);
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant: `Ada Kitchen ${letters()}`, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@saved.test`, name: "Ada Obi", password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
  const made = await page.request.post("/v1/saved", { headers: H, data: { name: "price-list", title: "Price list", kind: "text", details: { body: "Rice ₦75,000 a bag" } } });
  expect(made.status(), await made.text()).toBe(201);
  const id = ((await made.json()) as { saved: { id: string } }).saved.id;
  // The list is read once, when a step is first opened; never a reveal.
  let reads = 0;
  let reveals = 0;
  page.on("request", (q) => {
    const path = new URL(q.url()).pathname;
    if (path === "/v1/saved") reads++;
    if (path.endsWith("/reveal") || path.startsWith("/v1/me/saved")) reveals++;
  });

  await page.goto("/workflows");
  await newWorkflow(page, "Price check");
  await addStep(page, "if", /^If Go one way/);
  const win = page.getByTestId("step-window");
  // Before, with nothing earlier, an If had no values to compare; now the saved things are there.
  await win.locator("button", { hasText: "Add a rule" }).click();
  const value = win.getByLabel("Rule 1: the value");
  // Under Saved, each field by its words.
  await expect(value.locator("optgroup[label='Saved'] option")).toContainText(["@price-list · Title", "@price-list · Text", "@price-list · Note"]);
  await value.selectOption({ label: "@price-list · Text" });
  await win.getByLabel("Rule 1: how").selectOption("contains");
  await win.getByLabel("Rule 1: what").fill("Rice");
  await expect(value).toHaveValue("=saved.price_list.body");
  await shot(page, "saved-sources-rule");
  const vs = await axe(page);
  expect(vs, describe("a rule on a saved thing", vs)).toEqual([]);
  await doneWithStep(page);

  // Opened again, and another step: the same list, not read again.
  await page.getByTestId("node-check").click();
  await expect(win.getByLabel("Rule 1: the value")).toHaveValue("=saved.price_list.body");
  await doneWithStep(page);
  await page.getByRole("button", { name: "Add a step to this path" }).first().click();
  const picker = page.getByRole("dialog", { name: "What happens next?" });
  await picker.getByLabel("Search").fill("wait");
  await picker.getByRole("button", { name: /^Wait An hour/ }).click();
  await doneWithStep(page);
  expect(reads).toBe(1);
  // A person's own saved things are never offered, and nothing is revealed.
  expect(reveals).toBe(0);

  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.locator(".builder-saved")).toHaveText("Saved");
  const wf = page.url().split("/workflows/")[1]?.split("?")[0] ?? "";
  const doc = await page.request.get(`/v1/workflows/${wf}/versions/2`);
  expect(JSON.stringify(((await doc.json()) as { definition: unknown }).definition)).toContain("saved.price_list.body.contains('Rice')");

  // Removed from Saved: publishing says to save it again, with the page to do it on.
  expect((await page.request.delete(`/v1/saved/${id}`, { headers: H })).status()).toBe(204);
  await page.getByRole("button", { name: "Publish" }).click();
  const needs = page.getByRole("region", { name: "Before it can run" });
  await expect(needs).toContainText("@price-list");
  await expect(needs.getByTestId("need-saved").getByRole("link", { name: /Open Saved/ })).toHaveAttribute("href", "/settings/saved");
  await shot(page, "saved-sources-need");

  // A name no saved thing can have (stubbed until the server says so): its
  // message only, nothing to open.
  await page.route(/\/v1\/workflows\/[^/]+\/versions\/\d+\/publish$/, (q) =>
    q.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({
        error: "Not ready",
        code: "not_ready",
        needs: [
          {
            kind: "saved",
            name: "Price List!",
            problem: "invalid",
            environment: "prod",
            steps: [{ id: "check", name: "Check" }],
            blocking: true,
            message: "@Price List! is not a name a saved thing can have: change the step to use one.",
          },
        ],
      }),
    }),
  );
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(needs).toContainText("is not a name a saved thing can have");
  await expect(needs.getByTestId("need-saved")).toHaveCount(0);
  await expect(needs.getByRole("link", { name: /Open Saved/ })).toHaveCount(0);
});
