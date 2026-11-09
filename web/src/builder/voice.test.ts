import { describe, expect, it } from "vitest";
import {
  plainPanel,
  plainSchema,
  voiceEntries,
  voiceStep,
  type PlainPanel,
  type VoiceEntry,
} from "./voice";

function must<T>(v: T | undefined): T {
  if (v === undefined) throw new Error("missing");
  return v;
}

const connectors = [
  { id: "speech", ref: "speech@1" },
  { id: "whatsapp", ref: "whatsapp@1" },
  { id: "telegram", ref: "telegram@1" },
];

describe("voice and chat steps in the builder", () => {
  it("offers hearing, speaking, voice notes and typing for the services this Taskiem has", () => {
    expect(voiceEntries(connectors).map((e) => e.key)).toEqual([
      "voice:hear",
      "voice:speak",
      "voice:send_whatsapp",
      "voice:typing_whatsapp",
      "voice:send_telegram",
      "voice:typing_telegram",
    ]);
    expect(
      voiceEntries([{ id: "telegram", ref: "telegram@1" }]).map((e) => e.key),
    ).toEqual(["voice:send_telegram", "voice:typing_telegram"]);
  });

  it("starts each from a customer's message", () => {
    const hear = must<VoiceEntry>(
      voiceEntries(connectors).find((e) => e.key === "voice:hear"),
    );
    expect(voiceStep(hear, "hear")).toEqual({
      id: "hear",
      type: "connector",
      name: "Hear a voice note",
      connector: "speech@1",
      action: "transcribe",
      input: {
        file: "=trigger.guard.file",
        language: "=trigger.guard.language",
      },
    });
    const send = must<VoiceEntry>(
      voiceEntries(connectors).find((e) => e.key === "voice:send_whatsapp"),
    );
    expect(send.input).toMatchObject({ media_type: "audio", voice: true });
  });

  it("shows only what an owner fills, in plain words", () => {
    const panel = must<PlainPanel>(plainPanel("speech@1", "speak"));
    const schema = {
      type: "object",
      required: ["text"],
      properties: {
        text: { type: "string" },
        voice: { type: "string" },
        format: { type: "string" },
        customer_kind: { type: "string" },
      },
    };
    expect(plainSchema(schema, panel)).toEqual({
      type: "object",
      required: ["text"],
      properties: {
        text: { type: "string", title: "What to say" },
        voice: {
          type: "string",
          title: "Voice",
          description: "Leave it empty for the usual voice",
        },
      },
    });
  });

  it("knows a WhatsApp voice note from other media, and leaves other steps alone", () => {
    expect(
      plainPanel("whatsapp@1", "send_media", { voice: true })?.fields,
    ).toHaveProperty("file");
    expect(
      plainPanel("whatsapp@1", "send_media", { media_type: "image" }),
    ).toBeUndefined();
    expect(plainPanel("whatsapp@1", "send_text")).toBeUndefined();
    expect(plainPanel("telegram@1", "send_chat_action")?.note).toMatch(
      /on its way/,
    );
  });
});
