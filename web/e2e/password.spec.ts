import { createServer, type Server } from "node:net";
import { expect, test, type Page } from "@playwright/test";

// Forgot password → email → reset → sign in → change it under Account.
// Mail goes to a small SMTP sink on the port serve.sh points Taskiem at.

const smtpPort = 12525;
const inbox: string[] = [];
let smtp: Server;

test.beforeAll(async () => {
  smtp = createServer((sock) => {
    let data = false;
    let buf = "";
    let msg = "";
    sock.write("220 sink ESMTP\r\n");
    sock.on("data", (chunk) => {
      buf += chunk.toString("utf8");
      for (let i = buf.indexOf("\r\n"); i >= 0; i = buf.indexOf("\r\n")) {
        const line = buf.slice(0, i);
        buf = buf.slice(i + 2);
        if (data) {
          if (line === ".") {
            data = false;
            inbox.push(msg);
            msg = "";
            sock.write("250 queued\r\n");
          } else msg += line + "\n";
          continue;
        }
        const verb = line.slice(0, 4).toUpperCase();
        if (verb === "EHLO" || verb === "HELO") sock.write("250 sink\r\n");
        else if (verb === "DATA") {
          data = true;
          sock.write("354 go ahead\r\n");
        } else if (verb === "QUIT") sock.end("221 bye\r\n");
        else sock.write("250 ok\r\n");
      }
    });
  });
  await new Promise<void>((resolve) => smtp.listen(smtpPort, "127.0.0.1", resolve));
});

test.afterAll(() => new Promise<void>((resolve) => smtp.close(() => resolve())));

async function signIn(page: Page, email: string, password: string) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

test("forgot password, reset it from the email, then change it", async ({ page }) => {
  const email = `reset-${Date.now()}@e2e.test`;
  await signIn(page, "owner@e2e.test", "correct horse battery");
  const added = await page.request.post("/v1/members", {
    headers: { "X-Taskiem-Request": "1" },
    data: { email, password: "correct horse battery", roles: ["viewer"] },
  });
  expect(added.status()).toBe(201);
  await page.context().clearCookies();

  await page.goto("/login");
  await page.getByRole("link", { name: "Forgot password?" }).click();
  await expect(page.getByRole("heading", { name: "Reset your password" })).toBeVisible();
  await page.getByLabel("Email").fill(email);
  await page.getByRole("button", { name: "Email me a link" }).click();
  await expect(page.getByRole("status")).toContainText("If that email belongs");

  let link = "";
  await expect
    .poll(() => {
      const m = inbox.find((x) => x.includes(`To: ${email}`))?.match(/(http\S+\/reset-password#token=[\w.-]+)/);
      link = m?.[1] ?? "";
      return link;
    })
    .not.toBe("");

  await page.goto(link);
  // The token leaves the address bar.
  await expect(page).toHaveURL(/\/reset-password$/);
  await page.getByLabel("New password", { exact: true }).fill("a much better passphrase");
  await page.getByLabel("New password again").fill("a much better passphrase");
  await page.getByRole("button", { name: "Set password" }).click();
  await expect(page.getByRole("status")).toContainText("Your password is changed");
  // The link worked once. (From another page: only the fragment differs.)
  await page.goto("about:blank");
  await page.goto(link);
  await page.getByLabel("New password", { exact: true }).fill("yet another passphrase");
  await page.getByLabel("New password again").fill("yet another passphrase");
  await page.getByRole("button", { name: "Set password" }).click();
  await expect(page.getByText("this reset link is not valid")).toBeVisible();

  await signIn(page, email, "a much better passphrase");
  await page.goto("/account");
  await page.getByLabel("Current password").fill("a much better passphrase");
  await page.getByLabel("New password", { exact: true }).fill("the third passphrase");
  await page.getByLabel("New password again").fill("the third passphrase");
  await page.getByRole("button", { name: "Change password" }).click();
  await expect(page.getByRole("status")).toContainText("Password saved");
  await expect.poll(() => inbox.filter((x) => x.includes(`To: ${email}`) && x.includes("was just changed")).length).toBe(2);

  await page.getByRole("button", { name: "Sign out" }).click();
  await signIn(page, email, "the third passphrase");
});

// A partner invites someone new into a sub-tenant (decision 0030); they
// follow the emailed link, create a passkey and land in that sub-tenant.
// The partner only ever sees the invitation, never the link.
test("an invited person joins a sub-tenant with a passkey from the emailed link", async ({ page, request }) => {
  const base = "http://localhost:18080"; // WebAuthn needs a host name
  const post = async <T,>(path: string, token: string, data: unknown): Promise<T> => {
    const res = await request.post(path, { headers: token ? { Authorization: `Bearer ${token}` } : {}, data });
    expect(res.status(), `${path}: ${await res.text()}`).toBeLessThan(300);
    return (await res.json()) as T;
  };
  const login = await post<{ token: string }>("/v1/auth/login", "", { email: "owner@e2e.test", password: "correct horse battery", bearer: true });
  const key = (await post<{ key: string }>("/v1/api-keys", login.token, { name: `onboarding ${Date.now()}`, permissions: ["partner.read", "partner.manage"] })).key;
  const company = `Company ${Date.now()}`;
  const sub = (await post<{ id: string }>("/v1/partner/sub-tenants", key, { name: company })).id;
  const email = `holder-${Date.now()}@e2e.test`;
  const invited = await post<Record<string, unknown>>(`/v1/partner/sub-tenants/${sub}/invitations`, key, { email, roles: ["owner"] });
  expect(invited.delivery).toBe("email");

  let link = "";
  await expect
    .poll(() => {
      const m = inbox.find((x) => x.includes(`To: ${email}`))?.match(/(http\S+\/accept-invitation#token=[\w.-]+)/);
      link = m?.[1] ?? "";
      return link;
    })
    .not.toBe("");
  expect(JSON.stringify(invited)).not.toContain(link.split("#token=")[1]);
  link = link.replace(/^http:\/\/[^/]+/, base);

  const cdp = await page.context().newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });
  await page.goto(link);
  await expect(page).toHaveURL(/\/accept-invitation$/);
  await expect(page.getByText(`You are invited to ${company} as owner.`)).toBeVisible();
  await page.getByLabel("Your name").fill("Ada Holder");
  await page.getByRole("button", { name: "Create a passkey and join" }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
  const me = await (await page.request.get(`${base}/v1/me`)).json();
  expect(me.tenant_id).toBe(sub);
  expect(me.auth_method).toBe("passkey");

  // The link works once.
  await page.context().clearCookies();
  await page.goto("about:blank");
  await page.goto(link);
  await expect(page.getByText("this invitation link is not valid")).toBeVisible();
});
