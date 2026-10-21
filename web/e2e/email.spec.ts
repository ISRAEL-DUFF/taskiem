import { expect, test, type Page } from "@playwright/test";
import { createConnection } from "node:net";
import { axe, describe } from "./a11y";

// axe is injected into the page, which the Content Security Policy would refuse.
test.use({ bypassCSP: true });

/** Delivers a message to the mail role as another mail server would. */
async function deliver(to: string, from: string, body: string): Promise<string[]> {
  const sock = createConnection({ host: "127.0.0.1", port: 12825 });
  sock.setEncoding("utf8");
  let buf = "";
  const replies: string[] = [];
  const reply = () =>
    new Promise<string>((resolve, reject) => {
      const check = () => {
        // A reply ends with a line whose code is followed by a space.
        const lines = buf.split("\r\n");
        const end = lines.findIndex((l) => /^\d{3} /.test(l));
        if (end >= 0) {
          const r = lines.slice(0, end + 1).join("\n");
          buf = lines.slice(end + 1).join("\r\n");
          sock.off("data", onData);
          replies.push(r);
          resolve(r);
        }
      };
      const onData = (d: string) => {
        buf += d;
        check();
      };
      sock.on("data", onData);
      sock.once("error", reject);
      check();
    });
  const say = async (line: string) => {
    sock.write(line + "\r\n");
    return reply();
  };
  await reply();
  await say("EHLO sender.e2e.test");
  await say(`MAIL FROM:<${from}>`);
  await say(`RCPT TO:<${to}>`);
  await say("DATA");
  await say(body.replace(/\n/g, "\r\n") + "\r\n.");
  await say("QUIT");
  sock.end();
  return replies;
}

const signIn = async (page: Page) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
};

test("an email starts a workflow at its own address", async ({ page }) => {
  await signIn(page);
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Workflows" }).click();
  await page.getByRole("button", { name: "New workflow" }).click();
  await page.getByLabel("Name").fill("Bank alerts by email");
  await page.getByRole("button", { name: "Create" }).click();
  await expect(page.getByTestId("node-start")).toBeVisible();

  await page.getByRole("tab", { name: "Trigger & settings" }).click();
  await page.getByRole("combobox", { name: "Type" }).first().selectOption("email");
  await page.getByLabel("Only take mail from").fill("firstbank.example");
  await expect(page.getByText("Publish the workflow to get its email address.")).toBeVisible();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(/is published; new runs use it/)).toBeVisible({ timeout: 30_000 });

  // Its address shows once it is live, waiting for mail.
  const address = page.locator(".email-address code");
  await expect(address).toHaveText(/^[a-z0-9]{12}@in\.e2e\.test$/, { timeout: 15_000 });
  await expect(page.getByText("Waiting for your first email…")).toBeVisible();
  const to = (await address.textContent()) ?? "";
  const vs = await axe(page);
  expect(vs, describe("email trigger", vs)).toEqual([]);

  // Mail for an address that is not a workflow's is refused at once.
  const refused = await deliver("nobodyhere12@in.e2e.test", "a@b.example", "Subject: x\n\nx").catch(() => []);
  expect(refused.join("\n")).toMatch(/550/);

  // A real delivery over SMTP: the bank's address cannot be proven here
  // (no DKIM signature), so the senders list does not trust it, and the
  // page says so as soon as it arrives.
  const replies = await deliver(
    to,
    "alerts@firstbank.example",
    `From: First Bank <alerts@firstbank.example>\nTo: ${to}\nSubject: Credit alert\nMessage-ID: <e2e-${Date.now()}@firstbank.example>\n\nAmount: 50,000.00`,
  );
  // Greeting, EHLO, MAIL, RCPT, DATA (354), the message, QUIT.
  expect(replies[5]).toMatch(/^250/);
  await expect(page.getByText(/could not be proven/)).toBeVisible({ timeout: 15_000 });

  // A sample email runs it, and opens the run.
  await page.getByRole("button", { name: "Try it with a sample email" }).click();
  await expect(page).toHaveURL(/\/runs\/[0-9a-f-]+$/);
  await expect(page.locator("h1 + .badge")).toHaveText(/completed|running|queued/, { timeout: 15_000 });
});
