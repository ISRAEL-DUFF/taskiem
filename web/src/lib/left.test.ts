import { describe, expect, it } from "vitest";
import { WAITS, MIN_WAIT, MAX_WAIT, choiceWords, contactWords, rowStatus, waitWords, weekLine, type LeftRow } from "./left";

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
    expect(rowStatus(row({ followed_up_at: "x", came_back_at: "y", channel: "sms" }), false)).toEqual({ words: "Came back and ordered", tone: "ok" });
    expect(rowStatus(row({ followed_up_at: "x", channel: "email" }), false).words).toBe("Followed up by email");
    expect(rowStatus(row({ outcome: "stopped" }), false).words).toBe("Not followed up: they stopped your messages");
    expect(rowStatus(row(), true)).toEqual({ words: "Waiting: follow-ups are held", tone: "warn" });
    expect(rowStatus(row(), false).words).toBe("Waiting to be followed up");
  });
  it("puts the week in one line", () => {
    expect(weekLine({ left: 3, followed: 2, came_back: 1 })).toBe("3 left · 2 followed up · 1 came back");
  });
});
