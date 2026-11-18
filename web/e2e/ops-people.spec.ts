import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// People chats' safety in the operator console, on the real server
// (docs/spec/people-chatting.md "As built: stage 5a gaps, safety and
// moderation"; docs/runbooks/people-moderators.md): a moderator opens a report only saying
// why (recorded in the platform audit with their name and reason), marks it
// reviewed with the passkey, warns and suspends the person reported (the
// suspended state set), overturns their appeal (lifted); a lawful request
// recorded by one moderator is approved by a second (never the first) and
// exported; the published count, signed out, says so. Passkeys are Chrome's
// virtual authenticators; WebAuthn needs a host name, so this browses
// localhost. Held messages are sealed by the server alone, so they are gone
// through in the Go tests (TestPeopleHeldForReview) and accessibility.spec.ts.
test.use({ bypassCSP: true });
test.describe.configure({ mode: "serial" });
const base = "http://localhost:18080";

function sql(text: string): string {
  const db = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!db) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [db.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}

const signedIn: Record<string, Page> = {};

/** A moderator signed in with a passkey they enrol from their one-time link
 * (once: the link works once, so the page is kept for the next test). */
async function moderator(browser: Browser, who: "mod1" | "mod2", width = 390): Promise<Page> {
  const had = signedIn[who];
  if (had) {
    await had.goto(`${base}/ops`);
    return had;
  }
  const ctx = await browser.newContext({ viewport: { width, height: 900 }, acceptDownloads: true });
  const page = await ctx.newPage();
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });
  const link = readFileSync(fileURLToPath(new URL(`../../bin/e2e-ops-${who}-enrol.txt`, import.meta.url)), "utf8").trim();
  await page.goto(link);
  await page.getByLabel("Name for this passkey").fill("Virtual key");
  await page.getByRole("button", { name: "Create passkey" }).click();
  await expect(page.getByRole("heading", { name: "Review queue" })).toBeVisible();
  signedIn[who] = page;
  return page;
}

async function check(page: Page, where: string, width = 390) {
  const found: string[] = [];
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    if (vs.length) found.push(describe(`${where} (${scheme})`, vs));
  }
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  expect(found, found.join("\n\n")).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(width);
}

const stamp = Date.now().toString(36);
let dayo = "";
let report = "";

test.beforeAll(() => {
  // Two people (accounts as the server makes them; a person's profile
  // follows from the trigger) and a report Ada made of Dayo, kept as before
  // 00480, newest message first (the safety job seals it later; it opens
  // the same either way).
  const ada = sql(`INSERT INTO users (id, email, name) VALUES (gen_random_uuid(), 'ada-${stamp}@people.test', 'Ada Obi') RETURNING id`);
  dayo = sql(`INSERT INTO users (id, email, name, created_at) VALUES (gen_random_uuid(), 'dayo-${stamp}@people.test', 'Dayo Ola ${stamp}', now() - interval '40 days') RETURNING id`);
  report = sql(
    `INSERT INTO people_reports (id, person_id, reported_id, reason, note, consent, messages, from_request) VALUES (gen_random_uuid(), '${ada}', '${dayo}', 'scam', 'He wants money first', true, ` +
      `'[{"from_reporter": true, "kind": "text", "body": "Who is this?", "at": "2026-11-17T10:01:00Z"}, {"from_reporter": false, "kind": "text", "body": "You have won a prize, send the code", "at": "2026-11-17T10:00:00Z"}]', true) RETURNING id`,
  );
});

test("a moderator handles a report: opened with a reason, reviewed, the person warned, suspended and their appeal overturned", async ({ browser }) => {
  const page = await moderator(browser, "mod1");
  await page.getByRole("link", { name: "People safety" }).click();
  await expect(page.getByRole("heading", { name: "People safety" })).toBeVisible();
  await expect(page.getByTestId("ops-people-storage")).toContainText("people_messages");
  await check(page, "/ops/people");

  await page.getByTestId("ops-people-open-reports").click();
  const row = page.getByTestId("ops-people-report").filter({ hasText: `Dayo Ola ${stamp}` });
  await expect(row).toContainText("A scam or fraud");
  await expect(row).not.toContainText("prize");
  await row.click();
  // Nothing is shown before a reason is given.
  await expect(page.getByTestId("ops-why")).toBeVisible();
  await expect(page.getByText("You have won a prize")).toHaveCount(0);
  await page.getByLabel(/Your reason/).fill("Handling the queue");
  await page.getByRole("button", { name: "Open report" }).click();
  await expect(page.getByTestId("ops-report-message").first()).toContainText("You have won a prize");
  await check(page, "/ops/people/reports/{id}");
  expect(sql(`SELECT actor_id || ' ' || (detail->>'why') FROM audit_log WHERE action = 'ops.people.report.open' AND target = '${report}'`)).toBe("operator:mod1@taskiem.test Handling the queue");

  await page.getByLabel(/What you did/).fill("Warned and suspended");
  await page.getByRole("button", { name: "Mark reviewed with passkey" }).click();
  await expect(page.getByTestId("ops-report-reviewed")).toContainText("Reviewed by mod1@taskiem.test");
  expect(sql(`SELECT status || ' ' || reviewed_by FROM people_reports WHERE id = '${report}'`)).toBe("reviewed operator:mod1@taskiem.test");

  // From the report to the person: warned, then suspended for 3 days.
  await page.getByRole("link", { name: /Warn, limit, suspend or close/ }).click();
  await expect(page.getByTestId("ops-person-signals")).toContainText("Reported by 1 people");
  await check(page, "/ops/people/persons/{id}");
  await page.getByLabel(/Reason \(they are told it/).fill("Asking strangers for codes is not allowed.");
  await page.getByRole("button", { name: "Warn with passkey" }).click();
  await expect(page.getByTestId("ops-person-action").filter({ hasText: "Warn" })).toContainText("Asking strangers for codes");
  await page.getByRole("combobox", { name: /^Action/ }).selectOption("suspend");
  await page.getByLabel(/Reason \(they are told it/).fill("You kept asking strangers for codes after a warning.");
  await page.getByLabel("For how many days").fill("3");
  page.once("dialog", (d) => void d.accept());
  await page.getByRole("button", { name: "Suspend with passkey" }).click();
  await expect(page.getByTestId("ops-person-action").filter({ hasText: "Suspend" })).toContainText("by mod1@taskiem.test");
  expect(sql(`SELECT suspended::text || ' ' || active::text FROM person_profiles WHERE person_id = '${dayo}'`)).toBe("true false");
  expect(sql(`SELECT count(*) FROM people_moderation WHERE person_id = '${dayo}' AND operator = 'operator:mod1@taskiem.test' AND report_id = '${report}'`)).toBe("2");
  expect(sql(`SELECT count(*) FROM audit_log WHERE action IN ('ops.people.warn', 'ops.people.suspend') AND target = '${dayo}'`)).toBe("2");

  // Dayo appeals (as his own API would write it); the other moderator overturns it.
  const suspension = sql(`SELECT id FROM people_moderation WHERE person_id = '${dayo}' AND action = 'suspend'`);
  sql(`INSERT INTO people_appeals (id, action_id, person_id, text) VALUES (gen_random_uuid(), '${suspension}', '${dayo}', 'I was told the code was mine.')`);
  await page.goto(`${base}/ops/people/appeals`);
  const appeal = page.getByTestId("ops-appeal").filter({ hasText: `Dayo Ola ${stamp}` });
  await expect(appeal).toContainText("I was told the code was mine.");
  await check(page, "/ops/people/appeals");
  // Not by the moderator who suspended him: by the other one.
  await appeal.getByLabel(/Your answer/).fill("My own decision");
  await appeal.getByRole("button", { name: "Overturn with passkey" }).click();
  await expect(appeal.getByRole("alert")).toContainText("a different moderator answers an appeal");
  const mod2 = await moderator(browser, "mod2");
  await mod2.goto(`${base}/ops/people/appeals`);
  const theirs = mod2.getByTestId("ops-appeal").filter({ hasText: `Dayo Ola ${stamp}` });
  await theirs.getByLabel(/Your answer/).fill("We looked again: lifted.");
  await theirs.getByRole("button", { name: "Overturn with passkey" }).click();
  await expect(mod2.getByTestId("ops-appeal").filter({ hasText: `Dayo Ola ${stamp}` })).toHaveCount(0);
  expect(sql(`SELECT suspended::text || ' ' || active::text FROM person_profiles WHERE person_id = '${dayo}'`)).toBe("false true");
  expect(sql(`SELECT state FROM people_appeals WHERE action_id = '${suspension}'`)).toBe("overturned");
});

test("a lawful request: recorded by one moderator, approved by a second, exported, and counted in public", async ({ browser }) => {
  const mod1 = await moderator(browser, "mod1");
  await mod1.getByRole("link", { name: "Lawful requests" }).click();
  await mod1.getByRole("button", { name: "Record a request" }).click();
  const form = mod1.getByTestId("ops-lawful-record");
  await form.getByLabel("Who asked (the authority)").fill("Nigeria Police Force");
  await form.getByLabel("Their reference").fill(`NPF/CID/${stamp}`);
  await form.getByLabel(/The law and the order/).fill("Cybercrimes Act 2015 s.38, court order of 10 Nov 2026");
  await form.getByLabel("The person's account id").fill(dayo);
  await form.getByLabel("Messages from (day)").fill("2026-11-01");
  await form.getByLabel(/Messages to/).fill("2026-11-18");
  await form.getByLabel("Received on").fill("2026-11-18");
  await check(mod1, "record a lawful request");
  await form.getByRole("button", { name: "Record with passkey" }).click();
  const item = mod1.getByTestId("ops-lawful").filter({ hasText: `NPF/CID/${stamp}` });
  await expect(item).toContainText("Waiting for a second operator");
  await item.click();
  // The one who recorded it cannot approve it.
  await expect(mod1.getByRole("button", { name: "Approve with passkey" })).toBeDisabled();
  await check(mod1, "/ops/lawful/{id}");
  const url = mod1.url();

  const mod2 = await moderator(browser, "mod2");
  await mod2.goto(url);
  await mod2.getByRole("button", { name: "Approve with passkey" }).click();
  await expect(mod2.getByTestId("ops-lawful-detail")).toContainText("Approved by mod2@taskiem.test");

  await mod1.reload();
  const download = mod1.waitForEvent("download");
  await mod1.getByRole("button", { name: "Export with passkey" }).click();
  expect((await download).suggestedFilename()).toMatch(/^lawful-request-.*\.json$/);
  await expect(mod1.getByRole("status").filter({ hasText: "Exported" })).toBeVisible();
  await mod1.getByLabel("Or: why they are not told").fill("The court order forbids telling him for 90 days.");
  await mod1.getByRole("button", { name: "Record why not, with passkey" }).click();
  await expect(mod1.getByTestId("ops-lawful-told")).toContainText("Not told: The court order forbids");
  const id = url.split("/").pop();
  expect(sql(`SELECT state || ' ' || recorded_by || ' ' || approved_by FROM lawful_requests WHERE id = '${id}'`)).toBe("answered operator:mod1@taskiem.test operator:mod2@taskiem.test");
  expect(sql(`SELECT count(*) FROM audit_log WHERE target = '${id}' AND action IN ('ops.lawful.record', 'ops.lawful.approve', 'ops.lawful.export', 'ops.lawful.told')`)).toBe("4");

  // Signed out: the count per half-year, only once a half-year has ended:
  // this one, received now, is not counted in public yet.
  const anon = await browser.newPage({ viewport: { width: 360, height: 800 } });
  await anon.goto(`${base}/transparency`);
  const lagos = new Date(Date.now() + 60 * 60 * 1000);
  const now = lagos.getUTCMonth() < 6 ? `January to June ${lagos.getUTCFullYear()}` : `July to December ${lagos.getUTCFullYear()}`;
  await expect(anon.getByTestId("transparency-current")).toContainText(`${now} shows here once it ends.`);
  await expect(anon.getByTestId("transparency-period").filter({ hasText: now })).toHaveCount(0);
  await expect(anon.getByTestId("transparency")).not.toContainText("still being looked at");
  await check(anon, "/transparency", 360);
});
