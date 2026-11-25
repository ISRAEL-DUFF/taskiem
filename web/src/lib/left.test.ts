import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { WAITS, MIN_WAIT, MAX_WAIT, CONSENT_LINE, CONSENT_LINE_WHATSAPP, choiceWords, consentWords, contactWords, followupWords, rowStatus, waitWords, weekLine, whatsappWords, type LeftRow } from "./left";

const row = (over: Partial<LeftRow> = {}): LeftRow => ({
  id: "r1",
  contact_kind: "phone",
  contact: "… 1234",
  product: "Smart Ear Cleaner",
  package: 2,
  zone: "Lekki",
  at: "2026-11-25T10:00:00Z",
  ...over,
});

describe("forms left half-filled", () => {
  it("says the wait in plain words, within 10 minutes to 24 hours", () => {
    expect(waitWords(30)).toBe("30 minutes");
    expect(waitWords(60)).toBe("1 hour");
    expect(waitWords(1440)).toBe("24 hours");
    expect(WAITS.every((m) => m >= MIN_WAIT && m <= MAX_WAIT)).toBe(true);
    expect(WAITS).toContain(30);
  });
  it("says what a row was buying", () => {
    expect(choiceWords(row())).toBe("Smart Ear Cleaner, 2 items, to Lekki");
    expect(choiceWords(row({ package: 1, zone: undefined }))).toBe("Smart Ear Cleaner, 1 item");
    expect(choiceWords(row({ product: "", package: undefined, zone: undefined }))).toBe("Your order form");
  });
  it("shows the contact masked, never more", () => {
    expect(contactWords(row())).toBe("Phone … 1234");
    expect(contactWords(row({ contact_kind: "email", contact: "…@example.com" }))).toBe("Email …@example.com");
  });
  it("says where each row is", () => {
    expect(rowStatus(row({ followed_up_at: "x", came_back_at: "y", channel: "sms" }), false)).toEqual({ words: "Came back and ordered, followed up by text", tone: "ok" });
    expect(rowStatus(row({ followed_up_at: "x", came_back_at: "y", channel: "whatsapp" }), false).words).toBe("Came back and ordered, followed up on WhatsApp");
    expect(rowStatus(row({ followed_up_at: "x", channel: "whatsapp" }), false).words).toBe("Followed up on WhatsApp");
    expect(rowStatus(row({ followed_up_at: "x", channel: "email" }), false).words).toBe("Followed up by email");
    expect(rowStatus(row({ outcome: "stopped" }), false).words).toBe("Not followed up: they stopped your messages");
    expect(rowStatus(row(), true)).toEqual({ words: "Waiting: follow-ups are held", tone: "warn" });
    expect(rowStatus(row(), false).words).toBe("Waiting to be followed up");
  });
  it("puts the week in one line", () => {
    expect(weekLine({ left: 3, followed: 2, came_back: 1 })).toBe("3 left · 2 followed up · 1 came back");
  });
});

describe("follow-ups on WhatsApp (L4)", () => {
  const base = { on: true, wait_minutes: 30 };
  it("says how the follow-up goes and what the form says", () => {
    // Follow-ups go by text, email or WhatsApp: WhatsApp is one channel, never the feature.
    expect(followupWords(base)).toContain("by text (or by email");
    expect(followupWords({ ...base, whatsapp: true })).toContain("one message 30 minutes after they leave, by text, email or WhatsApp: on WhatsApp from your number");
    expect(followupWords({ ...base, whatsapp: true, whatsapp_opt_in: true })).toContain("when they tick the box");
    expect(consentWords(base)).toBe(`“${CONSENT_LINE}”`);
    expect(consentWords({ ...base, whatsapp: true })).toBe(`“${CONSENT_LINE_WHATSAPP}”`);
    expect(consentWords({ ...base, whatsapp: true, whatsapp_opt_in: true })).toContain("Send that message on WhatsApp instead");
  });
  it("says the owner's WhatsApp choice, and how to get it", () => {
    expect(whatsappWords(base)).toContain("connect your own WhatsApp number");
    expect(whatsappWords({ ...base, whatsapp_ready: true, whatsapp_on: false })).toBe("Off: follow-ups go by text or email only.");
    expect(whatsappWords({ ...base, whatsapp_ready: true, whatsapp_on: true, whatsapp: true })).toContain("go on WhatsApp from your number");
    expect(whatsappWords({ ...base, whatsapp_ready: true, whatsapp_on: true, whatsapp: true, whatsapp_opt_in: true })).toContain("tick the box");
  });
  it("keeps the words the server draws", () => {
    // Read from disk, not imported: the image's web stage has no engine/.
    const en = JSON.parse(readFileSync(resolve(__dirname, "../../../engine/lang/messages/en.json"), "utf8")) as { messages?: Record<string, { text: string }> } & Record<string, unknown>;
    const msgs = (en.messages ?? en) as Record<string, { text: string }>;
    expect(msgs["form.followup.consent"]?.text).toBe(CONSENT_LINE);
    expect(msgs["form.followup.consent_whatsapp"]?.text).toBe(CONSENT_LINE_WHATSAPP);
  });
});
