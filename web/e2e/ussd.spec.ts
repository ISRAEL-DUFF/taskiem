import { expect, test, type APIRequestContext } from "@playwright/test";

// USSD channels under Settings: connect Africa's Talking, see the callback
// URL once, check it is accepted (and a wrong token is not), rotate the
// token, narrow the address allow-list, turn the channel off and remove it.

function callback(request: APIRequestContext, url: string) {
  const u = new URL(url);
  return request.post(u.pathname + u.search, {
    form: { sessionId: `ATUid_e2e${Date.now()}`, serviceCode: "*384*999#", phoneNumber: "+254711000001", text: "", networkCode: "63902" },
  });
}

test("connect a USSD channel, rotate its token, restrict and remove it", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Workflows" })).toBeVisible();

  await page.goto("/settings");
  const card = page.getByTestId("ussd-channels");
  await expect(card.getByRole("heading", { name: "USSD channels" })).toBeVisible();
  await card.getByLabel("Channel environment").selectOption("prod");
  await card.getByLabel("Allowed addresses").fill("127.0.0.1");
  await card.getByRole("button", { name: "Connect" }).click();

  const shown = card.getByTestId("ussd-callback-url");
  await expect(shown).toContainText("/channels/ussd/");
  const first = (await shown.textContent()) ?? "";
  expect(first).toMatch(/\/africastalking\?token=[0-9a-f]{64}$/);
  const channel = card.getByTestId("ussd-channel-africastalking");
  await expect(channel.getByText("active", { exact: true })).toBeVisible();
  await expect(channel.getByLabel("Allowed addresses for africastalking")).toHaveValue("127.0.0.1/32");

  // The callback with the token is answered (no workflow serves the code,
  // so the session ends); with another token it is refused.
  const ok = await callback(page.request, first);
  expect(ok.status()).toBe(200);
  expect(await ok.text()).toMatch(/^END /);
  const forged = await callback(page.request, first.replace(/token=.*/, "token=" + "0".repeat(64)));
  expect(forged.status()).toBe(401);

  // Once dismissed the token is gone from the page for good.
  await card.getByRole("button", { name: "I have copied it" }).click();
  await expect(card.getByTestId("ussd-callback-url")).toHaveCount(0);
  await page.reload();
  await expect(page.getByTestId("ussd-channels").getByText(/token=[0-9a-f]{64}/)).toHaveCount(0);

  // Rotating makes a new URL; the old one stops working.
  page.once("dialog", (d) => void d.accept());
  await page.getByTestId("ussd-channel-africastalking").getByRole("button", { name: "Rotate token" }).click();
  await expect(page.getByTestId("ussd-callback-url")).toBeVisible();
  const second = (await page.getByTestId("ussd-callback-url").textContent()) ?? "";
  expect(second).not.toBe(first);
  expect((await callback(page.request, first)).status()).toBe(401);
  expect((await callback(page.request, second)).status()).toBe(200);

  // An allow-list without this address refuses even the right token.
  const row = page.getByTestId("ussd-channel-africastalking");
  await row.getByLabel("Allowed addresses for africastalking").fill("192.0.2.0/24");
  await row.getByRole("button", { name: "Save" }).click();
  await expect.poll(async () => (await callback(page.request, second)).status()).toBe(401);

  // Turned off: 404.
  await row.getByLabel("Allowed addresses for africastalking").fill("");
  await row.getByLabel(/Turned off/).check();
  await row.getByRole("button", { name: "Save" }).click();
  await expect(row.getByText("disabled", { exact: true })).toBeVisible();
  expect((await callback(page.request, second)).status()).toBe(404);

  page.once("dialog", (d) => void d.accept());
  await row.getByRole("button", { name: "Disconnect" }).click();
  await expect(page.getByTestId("ussd-channel-africastalking")).toHaveCount(0);
  await expect(page.getByTestId("ussd-channels").getByRole("button", { name: "Connect" })).toBeVisible();
});
