import { execFileSync } from "node:child_process";
import { createServer as createHttp, type Server as HttpServer } from "node:http";
import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// People groups on the web, stage 5b (docs/spec/people-chatting.md §9, §13;
// "As built: stage 5b, groups (server)" and "…, group safety (server)"), on
// the real server: Ada makes a group with a contact (added at once) and
// someone whose "Who can add me to groups" is Nobody (invited, accepted in
// Requests); they chat with an @mention and "Read by"; a link with Approve
// new members lets Dayo in once Ada approves; the info is the admins' to
// edit; Bola is removed and sees nothing new; Chidi reports a member; a
// moderator closes the group and its members see the closed notice, its
// admin appeals. Phones at 390 and 360 px, both themes, axe at each step.
// The people's codes go out on Taskiem's WhatsApp number to a fake Graph
// API on the port people.spec.ts uses, so this file runs one test at a
// time and apart from the other specs on that port.
test.use({ bypassCSP: true });
test.describe.configure({ mode: "serial" });

const H = { "X-Taskiem-Request": "1" };
const graphPort = 12626;
const sent: { to: string; template?: { components?: { parameters?: { text?: string }[] }[] } }[] = [];
let graph: HttpServer;

test.beforeAll(async () => {
  graph = createHttp((req, res) => {
    let body = "";
    req.on("data", (c: Buffer) => (body += c.toString("utf8")));
    req.on("end", () => {
      res.writeHead(200, { "Content-Type": "application/json" });
      if (req.method === "POST" && req.url?.endsWith("/messages")) {
        const m = JSON.parse(body) as (typeof sent)[number];
        sent.push(m);
        res.end(JSON.stringify({ messaging_product: "whatsapp", contacts: [{ input: m.to, wa_id: m.to.replace(/^\+/, "") }], messages: [{ id: `wamid.gr${sent.length}` }] }));
        return;
      }
      res.end("{}");
    });
  });
  await new Promise<void>((resolve) => graph.listen(graphPort, "127.0.0.1", resolve));
});

test.afterAll(async () => {
  await new Promise<void>((resolve) => graph.close(() => resolve()));
});

function sql(text: string): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}

async function must<T>(r: Promise<{ status(): number; text(): Promise<string>; json(): Promise<unknown> }>, status: number): Promise<T> {
  const res = await r;
  expect(res.status(), await res.text()).toBe(status);
  return (await res.json()) as T;
}

/** axe in the light and the dark theme, and nothing wider than the page. */
async function check(page: Page, where: string, width: number) {
  const found: string[] = [];
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    await page.waitForTimeout(150);
    const vs = await axe(page);
    if (vs.length) found.push(describe(`${where} (${scheme})`, vs));
  }
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  expect(found, found.join("\n\n")).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(width);
}

type Person = { page: Page; e164: string; id: string; width: number };

let n = 0;
/** A person signed in by phone on their own side, on a phone of `width`,
 * their account a month old (a first week's limits are the server's tests'). */
async function person(browser: Browser, name: string, width: number): Promise<Person> {
  const ctx = await browser.newContext({ viewport: { width, height: 800 } });
  const page = await ctx.newPage();
  const rest = (String(Date.now()).slice(-6) + String(++n % 10) + String(Math.floor(Math.random() * 10))).slice(-8);
  const e164 = `+23471${rest}`;
  const before = sent.length;
  await must(page.request.post("/v1/auth/phone/start", { headers: H, data: { phone: e164 } }), 202);
  let code = "";
  await expect
    .poll(() => {
      const m = sent.slice(before).filter((x) => x.to === e164 || x.to === e164.slice(1)).pop();
      code = m?.template?.components?.flatMap((c) => c.parameters ?? []).find((p) => /^\d{6}$/.test(p.text ?? ""))?.text ?? "";
      return code;
    })
    .toMatch(/^\d{6}$/);
  const v = await must<{ ticket: string }>(page.request.post("/v1/auth/phone/verify", { headers: H, data: { phone: e164, code } }), 200);
  await must(page.request.post("/v1/auth/phone/create", { headers: H, data: { ticket: v.ticket, name, adult: true, terms: true } }), 201);
  const me = await must<{ user: { id: string } }>(page.request.get("/v1/me"), 200);
  sql(`UPDATE users SET created_at = now() - interval '30 days' WHERE id = '${me.user.id}'`);
  return { page, e164, id: me.user.id, width };
}

/** `a` adds `b` to their contacts, by b's own link. */
async function addContact(a: Person, b: Person, name?: string) {
  const link = await must<{ code: string }>(b.page.request.get("/v1/me/link"), 200);
  const f = await must<{ person: { id: string }; add_token: string }>(a.page.request.get(`/v1/people/links/${link.code}`), 200);
  expect(f.person.id).toBe(b.id);
  await must(a.page.request.post("/v1/me/contacts", { headers: H, data: { person_id: b.id, add_token: f.add_token, from: "link", name } }), 201);
}

/** Opens a bubble's menu and picks an entry. */
async function bubbleMenu(page: Page, text: string, entry: string) {
  const bubble = page.getByTestId("group-message").filter({ hasText: text }).last();
  await bubble.hover();
  await bubble.locator(".bubble-chevron").click({ force: true });
  await page.getByTestId("chat-more").getByRole("button", { name: entry, exact: true }).click();
}

/** Shared between the tests, in order. */
const st: { ada?: Person; bola?: Person; chidi?: Person; dayo?: Person; group?: string } = {};

test("a group is made with a contact added and someone invited, and they chat with a mention and Read by", async ({ browser }) => {
  test.setTimeout(240_000);
  const ada = await person(browser, "Ada Obi", 390);
  const bola = await person(browser, "Bola Ade", 360);
  const chidi = await person(browser, "Chidi Eze", 390);
  Object.assign(st, { ada, bola, chidi });
  // Ada and Bola added each other; Ada added Chidi, who lets nobody add them.
  await addContact(ada, bola);
  await addContact(bola, ada);
  await addContact(ada, chidi);

  await chidi.page.goto("/you/settings/people");
  const groupsAdd = chidi.page.getByTestId("privacy-groups_add");
  await expect(groupsAdd.getByLabel("My contacts")).toBeChecked();
  await expect(groupsAdd).toContainText("People you haven't added send you an invite instead.");
  const saved = chidi.page.waitForResponse((r) => r.url().endsWith("/v1/me/privacy/people") && r.request().method() === "PUT");
  await groupsAdd.getByLabel("Nobody").check();
  expect((await saved).status()).toBe(200);
  await check(chidi.page, "/you/settings/people with groups", 390);

  // Ada makes the group: Bola added at once, Chidi invited; nothing more said.
  await ada.page.goto("/you");
  await ada.page.getByTestId("group-new-open").click();
  const sheet = ada.page.getByTestId("group-new");
  await sheet.getByTestId("group-new-name").fill("Yaba friends");
  await sheet.getByTestId("group-new-description").fill("Saturday football");
  await sheet.getByTestId("group-pick").getByLabel(/Bola Ade/).check();
  await sheet.getByTestId("group-pick").getByLabel(/Chidi Eze/).check();
  await check(ada.page, "New group", 390);
  await sheet.getByTestId("group-new-make").click();
  await expect(sheet.getByTestId("group-added-row").filter({ hasText: "Bola Ade" })).toHaveText("Bola Ade: Added");
  await expect(sheet.getByTestId("group-added-row").filter({ hasText: "Chidi Eze" })).toHaveText("Chidi Eze: Invited: they'll see it in their Requests");
  await check(ada.page, "New group made", 390);
  await sheet.getByTestId("group-new-open").click();
  await expect(ada.page).toHaveURL(/\/you\/groups\/[0-9a-f-]{36}$/);
  st.group = /\/you\/groups\/([0-9a-f-]{36})$/.exec(ada.page.url())?.[1];
  await expect(ada.page.getByTestId("group-event").first()).toBeVisible();
  await expect(ada.page.getByTestId("group-head-line")).toHaveText("2 members");

  // Bola has it in the one Chats list, marked as a group.
  await bola.page.goto("/you");
  const row = bola.page.getByTestId("group-chat").filter({ hasText: "Yaba friends" });
  await expect(row).toBeVisible();
  await expect(row.getByTestId("chat-kind-group")).toContainText("Group");
  await check(bola.page, "/you with a group", 360);

  // Chidi accepts the invite in Requests.
  await chidi.page.goto("/you");
  await expect(chidi.page.getByTestId("people-requests-entry")).toContainText("1");
  await chidi.page.getByTestId("people-requests-entry").click();
  const invite = chidi.page.getByTestId("group-invite").filter({ hasText: "Yaba friends" });
  await expect(invite).toContainText("Ada Obi invited you");
  await check(chidi.page, "Requests with a group invite", 390);
  await invite.getByTestId("group-invite-accept").click();
  await expect(chidi.page).toHaveURL(new RegExp(`/you/groups/${st.group}$`));
  await expect(ada.page.getByTestId("group-head-line")).toHaveText("3 members", { timeout: 20_000 });

  // Ada mentions Bola from the members' picker.
  const box = ada.page.getByLabel("Message Yaba friends");
  await box.fill("Hi @Bo");
  await expect(ada.page.getByTestId("mention-list")).toHaveAttribute("aria-label", "Members to mention");
  await ada.page.getByTestId("mention-list").getByRole("button", { name: /Bola Ade/ }).click();
  await box.pressSequentially("are you coming on Saturday?");
  await box.press("Enter");
  const mine = ada.page.getByTestId("group-message").filter({ hasText: "are you coming on Saturday?" });
  await expect(mine.getByTestId("people-ticks")).toHaveAttribute("data-ticks", /^(sent|delivered)$/);

  // Bola sees it, the mention of them marked; Chidi sees it unmarked.
  await row.click();
  const theirs = bola.page.getByTestId("group-message").filter({ hasText: "are you coming on Saturday?" });
  await expect(theirs.getByTestId("group-mentions-me")).toBeVisible({ timeout: 20_000 });
  await expect(theirs.getByTestId("group-sender")).toContainText("Ada Obi");
  await check(bola.page, "a group with a mention", 360);
  const chidis = chidi.page.getByTestId("group-message").filter({ hasText: "are you coming on Saturday?" });
  await expect(chidis).toBeVisible({ timeout: 20_000 });
  await expect(chidis.getByTestId("group-mentions-me")).toHaveCount(0);
  await expect(chidis.getByTestId("group-mention")).toHaveText("@Bola Ade");

  // Bola answers with a reaction too; both read it: Ada's ticks read, Read by lists them.
  await bola.page.getByLabel("Message Yaba friends").fill("Yes! See you there");
  await bola.page.getByLabel("Message Yaba friends").press("Enter");
  await expect(ada.page.getByTestId("group-message").filter({ hasText: "See you there" })).toBeVisible({ timeout: 20_000 });
  await expect(mine.getByTestId("people-ticks")).toHaveAttribute("data-ticks", "read", { timeout: 30_000 });
  await bubbleMenu(ada.page, "are you coming on Saturday?", "Message info");
  const readBy = ada.page.getByTestId("group-read-by-list");
  await expect(readBy).toContainText("Bola Ade");
  await expect(readBy).toContainText("Chidi Eze");
  await check(ada.page, "Read by", 390);
  await ada.page.getByTestId("group-read-by").getByRole("button", { name: /Close/ }).first().click();
  await check(ada.page, "/you/groups/{id}", 390);
});

test("a link with Approve new members lets someone in once an admin says so; the info is the admins' to edit", async ({ browser }) => {
  test.setTimeout(180_000);
  const { ada, bola } = st;
  if (!ada || !bola || !st.group) throw new Error("the first test makes the group");
  const dayo = await person(browser, "Dayo Ola", 360);
  st.dayo = dayo;

  // Ada turns the link on, and Approve new members.
  await ada.page.goto(`/you/groups/${st.group}/info`);
  await ada.page.getByTestId("group-link-on").click();
  await expect(ada.page.getByTestId("group-qr")).toBeVisible();
  const url = (await ada.page.getByTestId("group-link-url").textContent()) ?? "";
  const code = /\/g\/([A-Za-z0-9]+)$/.exec(url)?.[1] ?? "";
  expect(code).not.toBe("");
  const approved = ada.page.waitForResponse((r) => r.url().endsWith(`/v1/me/groups/${st.group}`) && r.request().method() === "PUT");
  await ada.page.getByTestId("group-approve").check();
  expect((await approved).status()).toBe(200);
  await check(ada.page, "/you/groups/{id}/info as an admin", 390);

  // Signed out, the link shows the group and asks to sign in, then comes back.
  const out = await browser.newContext({ viewport: { width: 360, height: 800 } });
  const outPage = await out.newPage();
  await outPage.goto(`/g/${code}`);
  await expect(outPage.getByTestId("group-link-name")).toHaveText("Yaba friends");
  await expect(outPage.getByTestId("group-link-count")).toContainText("3 members");
  await check(outPage, "/g/{code} signed out", 360);
  await outPage.getByTestId("group-link-signin").click();
  await expect(outPage).toHaveURL(new RegExp(`/login\\?next=${encodeURIComponent(`/g/${code}`).replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`));
  await out.close();

  // Dayo joins: it waits for an admin.
  await dayo.page.goto(`/g/${code}`);
  await dayo.page.getByTestId("group-link-join").click();
  await expect(dayo.page.getByTestId("group-link-waiting")).toContainText("waits for an admin");
  await check(dayo.page, "/g/{code}, waiting", 360);
  // Ada lets Dayo in from the waiting list.
  await ada.page.reload();
  const waiting = ada.page.getByTestId("group-waiting").filter({ hasText: "Dayo Ola" });
  await expect(waiting).toBeVisible();
  await waiting.getByTestId("group-waiting-approve").click();
  await expect(ada.page.getByTestId("group-member").filter({ hasText: "Dayo Ola" })).toBeVisible();
  await dayo.page.goto("/you");
  await expect(dayo.page.getByTestId("group-chat").filter({ hasText: "Yaba friends" })).toBeVisible({ timeout: 20_000 });

  // A member can't edit the info while it is the admins'; the admin can.
  await bola.page.goto(`/you/groups/${st.group}/info`);
  await expect(bola.page.getByTestId("group-edit-admins")).toBeVisible();
  await expect(bola.page.getByTestId("group-edit")).toHaveCount(0);
  await expect(bola.page.getByTestId("group-settings")).toHaveCount(0);
  await check(bola.page, "/you/groups/{id}/info as a member", 360);
  await ada.page.getByTestId("group-edit").click();
  await ada.page.getByTestId("group-edit-name").fill("Yaba friends FC");
  await ada.page.getByTestId("group-edit-save").click();
  await expect(ada.page.getByRole("heading", { name: "Yaba friends FC", level: 1 })).toBeVisible();
  await bola.page.reload();
  await expect(bola.page.getByRole("heading", { name: "Yaba friends FC", level: 1 })).toBeVisible();
});

test("a member removed sees nothing new; a member is reported; a moderator closes the group", async ({ browser }) => {
  test.setTimeout(180_000);
  void browser;
  const { ada, bola, chidi, dayo } = st;
  if (!ada || !bola || !chidi || !dayo || !st.group) throw new Error("the first tests make the group");

  // Ada removes Bola.
  await ada.page.goto(`/you/groups/${st.group}/info`);
  const bolaRow = ada.page.getByTestId("group-member").filter({ hasText: "Bola Ade" });
  await bolaRow.getByTestId("group-member-remove").click();
  await ada.page.getByTestId("confirm-ok").click();
  await expect(bolaRow).toHaveCount(0);
  await ada.page.goto(`/you/groups/${st.group}`);
  await ada.page.getByLabel("Message Yaba friends FC").fill("Only for those still here");
  await ada.page.getByLabel("Message Yaba friends FC").press("Enter");
  await expect(ada.page.getByTestId("group-message").filter({ hasText: "Only for those still here" })).toBeVisible();
  // Bola keeps what came before, sees nothing new and can't write.
  await bola.page.goto(`/you/groups/${st.group}`);
  await expect(bola.page.getByTestId("group-left")).toContainText("You were removed from this group");
  await expect(bola.page.getByTestId("group-message").filter({ hasText: "See you there" })).toBeVisible();
  await expect(bola.page.getByLabel(/^Message /)).toHaveCount(0);
  await bola.page.waitForTimeout(2000);
  await bola.page.reload();
  await expect(bola.page.getByTestId("group-message").filter({ hasText: "See you there" })).toBeVisible();
  await expect(bola.page.getByTestId("group-message").filter({ hasText: "Only for those still here" })).toHaveCount(0);
  await check(bola.page, "a group you were removed from", 360);

  // Chidi reports Dayo, with consent.
  await chidi.page.goto(`/you/groups/${st.group}/info`);
  await chidi.page.getByTestId("group-member").filter({ hasText: "Dayo Ola" }).getByTestId("group-member-report").click();
  const report = chidi.page.getByTestId("group-report-sheet");
  await report.getByLabel("Messages I don't want").check();
  await expect(report.getByTestId("group-report-send")).toBeDisabled();
  await expect(report).toContainText("The last 20 messages you can see, with their photos and files, go to Taskiem's moderators");
  await report.getByTestId("group-report-consent").check();
  await check(chidi.page, "report a member", 390);
  await report.getByTestId("group-report-send").click();
  await expect(report.getByTestId("group-report-said")).toBeVisible();
  await expect(report.getByTestId("group-report-block")).toBeVisible();
  expect(sql(`SELECT count(*) FROM people_reports WHERE group_id = '${st.group}' AND reported_id = '${dayo.id}' AND person_id = '${chidi.id}'`)).toBe("1");

  // A moderator closes the group (as the console's definer does).
  sql(`INSERT INTO people_moderation (id, group_id, action, reason, operator, until) VALUES (gen_random_uuid(), '${st.group}', 'group_close', 'Scams in the group', 'mod1@taskiem.test', now() + interval '30 days')`);
  sql(`SELECT taskiem_group_standing_sync('${st.group}')`);
  await chidi.page.goto(`/you/groups/${st.group}`);
  await expect(chidi.page.getByTestId("group-closed")).toContainText("Taskiem's moderators closed this group");
  await expect(chidi.page.getByLabel(/^Message /)).toHaveCount(0);
  await expect(chidi.page.getByTestId("group-event").filter({ hasText: "Taskiem" }).last()).toBeVisible();
  await check(chidi.page, "a closed group", 390);
  // Its admin appeals from the info.
  await ada.page.goto(`/you/groups/${st.group}`);
  await expect(ada.page.getByTestId("group-closed")).toBeVisible();
  await ada.page.getByTestId("group-appeal-open").click();
  await ada.page.getByTestId("group-appeal-text").fill("It was one member, and we removed them.");
  await ada.page.getByTestId("group-appeal-send").click();
  await expect(ada.page.getByTestId("group-appeal-said")).toBeVisible();
  await check(ada.page, "a closed group's appeal", 390);
});
