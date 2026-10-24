import { execFileSync } from "node:child_process";
import { expect, type Page } from "@playwright/test";

// A school for the family page tests (family.spec.ts,
// accessibility.spec.ts): a business of its own with two siblings, a sent
// term invoice each and a receipt, and a link to the family's page on the
// public host, which serve.sh runs on 18082.

export const H = { "X-Taskiem-Request": "1" };
export const PASSWORD = "correct horse battery";
export const PUBLIC = "http://127.0.0.1:18082";

export function sql(text: string): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}

const STUDENTS = ["Name,Admission number,Class,Guardian name,Phone", "Ebube Eze,FAM/001,JSS 1 F,Ngozi Eze,08035552001", "Chidi Eze,FAM/002,JSS 1 F,Ngozi Eze,08035552001"].join("\n");

/** A school of its own, in a theme, with a sent term invoice per student and a receipt; the page's request context is signed in as its owner. */
export async function familySchool(page: Page, name: string, theme = "clean"): Promise<void> {
  const email = `owner-${Date.now()}-${Math.floor(Math.random() * 1e6)}@familyschool.test`;
  const r = await page.request.post("/v1/signup", { headers: H, data: { tenant: name, email, name: "Owner", password: PASSWORD } });
  expect(r.status(), await r.text()).toBe(201);
  sql(`UPDATE users SET email_verified_at = now() WHERE lower(email) = lower('${email}')`);
  expect((await page.request.put("/v1/settings/schools", { headers: H, data: { enabled: true } })).ok()).toBeTruthy();
  expect((await page.request.put("/v1/settings/brand", { headers: H, data: { theme, color: "#1f3a5f" } })).ok()).toBeTruthy();
  const cl = await page.request.post("/v1/schools/classes", { headers: H, data: { name: "JSS 1", arm: "F", order: 7 } });
  expect(cl.ok(), await cl.text()).toBeTruthy();
  const classID = ((await cl.json()) as { id: string }).id;
  const plan = await page.request.post("/v1/schools/students/import", { headers: H, data: { csv: STUDENTS } });
  expect(plan.ok(), await plan.text()).toBeTruthy();
  const saved = await page.request.post("/v1/schools/students/import", { headers: H, data: { csv: STUDENTS, confirm: ((await plan.json()) as { hash: string }).hash } });
  expect(saved.ok(), await saved.text()).toBeTruthy();
  const sess = await page.request.post("/v1/schools/sessions", { headers: H, data: { label: "2026/2027" } });
  expect(sess.ok(), await sess.text()).toBeTruthy();
  const term = await page.request.post(`/v1/schools/sessions/${((await sess.json()) as { id: string }).id}/terms`, {
    headers: H,
    data: { name: "First term", starts_on: "2026-09-14", ends_on: "2099-12-11", fees_due_on: "2099-09-07" },
  });
  expect(term.ok(), await term.text()).toBeTruthy();
  const termID = ((await term.json()) as { id: string }).id;
  expect((await page.request.post(`/v1/schools/terms/${termID}/start`, { headers: H })).ok()).toBeTruthy();
  const fees = await page.request.put(`/v1/schools/terms/${termID}/fees/${classID}`, {
    headers: H,
    data: {
      items: [
        { name: "Tuition", amount_kobo: 10_000_000, kind: "required" },
        { name: "WAEC fee", amount_kobo: 1_800_000, kind: "required", pass_through_for: "WAEC" },
      ],
    },
  });
  expect(fees.ok(), await fees.text()).toBeTruthy();
  const p = await page.request.post(`/v1/schools/terms/${termID}/invoice`, { headers: H, data: {} });
  expect(p.ok(), await p.text()).toBeTruthy();
  const inv = await page.request.post(`/v1/schools/terms/${termID}/invoice`, { headers: H, data: { confirm: ((await p.json()) as { plan: { hash: string } }).plan.hash } });
  expect(inv.ok(), await inv.text()).toBeTruthy();
  // The e2e server sends no messages to customers: the term's invoices
  // are marked sent in the database (as cash.spec.ts does).
  sql(`UPDATE customer_invoices SET sent_at = now(), updated_at = now() WHERE sent_at IS NULL
    AND tenant_id = (SELECT m.tenant_id FROM memberships m JOIN users u ON u.id = m.user_id WHERE lower(u.email) = lower('${email}') LIMIT 1)`);
  // A payment at the desk, so there is a receipt to show.
  const posted = await page.request.post("/v1/schools/payments", { headers: { ...H, "Idempotency-Key": `fam-${email}` }, data: { student: "FAM/001", amount_kobo: 2_500_050, how: "cash" } });
  expect(posted.status(), await posted.text()).toBe(201);
}

/** Mints a link for the family (as the Families page does) and returns its public address. */
export async function familyLink(page: Page): Promise<string> {
  const fams = await page.request.get("/v1/schools/families", { headers: H });
  expect(fams.ok(), await fams.text()).toBeTruthy();
  const family = ((await fams.json()) as { families: { id: string }[] }).families[0]?.id ?? "";
  expect(family).not.toBe("");
  const sent = await page.request.post(`/v1/schools/families/${family}/link`, { headers: H, data: {} });
  expect(sent.ok(), await sent.text()).toBeTruthy();
  return ((await sent.json()) as { link: string }).link.replace("https://127.0.0.1", PUBLIC);
}
