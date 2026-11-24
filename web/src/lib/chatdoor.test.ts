import { describe, expect, it } from "vitest";
import { chatAddress } from "./chatdoor";

describe("chatAddress", () => {
  it("shows a WhatsApp number whole", () => {
    expect(chatAddress({ channel: "whatsapp", address: "+2348031239771" })).toBe(
      "+2348031239771",
    );
  });
  it("shows a Telegram username, or the account's number without one", () => {
    expect(
      chatAddress({ channel: "telegram", address: "8202", username: "ada" }),
    ).toBe("@ada");
    expect(chatAddress({ channel: "telegram", address: "8202" })).toBe(
      "the Telegram account numbered 8202",
    );
  });
});
