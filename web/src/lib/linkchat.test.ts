import { describe, expect, it } from "vitest";
import { attachmentLabel, byDay, cardHref, cardLine, cardSub, dayLabel, fileSize, fileURL, listTime, merge, previewLine, type LinkMessage } from "./linkchat";

const msg = (id: string, at: string, extra: Partial<LinkMessage> = {}): LinkMessage => ({ id, link: "l1", mine: false, kind: "text", body: id, created_at: at, ...extra });

describe("the conversation with a linked business", () => {
  const now = new Date("2026-10-31T15:00:00");
  it("says a card in words, from either side", () => {
    expect(cardLine({ doc: "bill", event: "bill", reference: "INV-0042" }, false, "Dangote Stores")).toBe("Dangote Stores sent invoice INV-0042");
    expect(cardLine({ doc: "purchase", event: "accept", reference: "P-7QX4KD" }, true, "Dangote Stores")).toBe("You accepted order P-7QX4KD");
    expect(cardLine({ doc: "order", event: "new_thing", reference: "P-7QX4KD" }, true, "X")).toBe("You new thing order P-7QX4KD");
    expect(cardSub({ doc: "bill", event: "queried", reference: "INV-1", amount_kobo: 2_500_000, note: "Two bags torn" })).toBe("₦25,000 · “Two bags torn”");
  });
  it("opens a card's document", () => {
    expect(cardHref({ doc: "order", event: "x", reference: "P-1", id: "o1" })).toBe("/trade-orders/o1");
    expect(cardHref({ doc: "purchase", event: "x", reference: "P-1", id: "p1" })).toBe("/purchases/p1");
    expect(cardHref({ doc: "invoice", event: "x", reference: "INV-1" })).toBe("/invoices/INV-1");
    expect(cardHref({ doc: "bill", event: "x", reference: "INV-1" })).toBe("/bills/INV-1");
  });
  it("previews a message", () => {
    expect(previewLine({ mine: true, kind: "text", body: "Hello" }, "D")).toBe("You: Hello");
    expect(previewLine({ mine: false, kind: "text", body: "Hello" }, "D")).toBe("Hello");
    expect(previewLine({ mine: false, kind: "card", card: { doc: "bill", event: "bill", reference: "INV-1" } }, "D")).toBe("D sent invoice INV-1");
  });
  it("groups by day, and says the day", () => {
    expect(dayLabel("2026-10-31T09:00:00", now)).toBe("Today");
    expect(dayLabel("2026-10-30T23:00:00", now)).toBe("Yesterday");
    expect(dayLabel("2026-10-28T10:00:00", now)).toBe("Wednesday");
    expect(dayLabel("2026-09-01T10:00:00", now)).toBe("1 September 2026");
    expect(listTime("2026-10-31T09:05:00", now)).toBe("09:05");
    const days = byDay([msg("a", "2026-10-30T10:00:00"), msg("b", "2026-10-31T09:00:00"), msg("c", "2026-10-31T10:00:00")], now);
    expect(days.map((d) => [d.day, d.messages.length])).toEqual([
      ["Yesterday", 1],
      ["Today", 2],
    ]);
  });
  it("merges pages by id, oldest first", () => {
    const a = [msg("a", "2026-10-31T09:00:00"), msg("b", "2026-10-31T09:01:00")];
    const b = [msg("b", "2026-10-31T09:01:00", { read_at: "x" }), msg("c", "2026-10-31T09:02:00")];
    const m = merge(a, b);
    expect(m.map((x) => x.id)).toEqual(["a", "b", "c"]);
    expect(m[1]?.read_at).toBe("x");
  });
});

describe("files in the conversation", () => {
  it("says what came, in a word, with the caption", () => {
    expect(attachmentLabel({ kind: "photo", type: "image/jpeg", size: 1 })).toBe("Photo");
    expect(attachmentLabel({ kind: "voice", type: "audio/ogg", size: 1 })).toBe("Voice note");
    expect(attachmentLabel({ kind: "file", type: "application/pdf", size: 1, name: "waybill.pdf" })).toBe("waybill.pdf");
    expect(previewLine({ mine: false, kind: "text", attachment: { kind: "photo", type: "image/jpeg", size: 1 } }, "D")).toBe("Photo");
    expect(previewLine({ mine: true, kind: "text", body: "The bags", attachment: { kind: "photo", type: "image/jpeg", size: 1 } }, "D")).toBe("You: Photo: The bags");
  });
  it("sizes and streams it", () => {
    expect(fileSize(300)).toBe("1 KB");
    expect(fileSize(820 * 1024)).toBe("820 KB");
    expect(fileSize(1.4 * 1024 * 1024)).toBe("1.4 MB");
    expect(fileURL({ link: "L1", id: "M1" })).toBe("/v1/link-chats/L1/files/M1");
  });
});
