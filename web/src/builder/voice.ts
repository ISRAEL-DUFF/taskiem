// Voice and chat steps in plain words (decision 0062 V8, R5, R6): hearing
// a voice note, speaking a reply, sending it as a voice note, and showing
// a customer that a reply is coming. Each gets an entry in the builder's
// list and a panel with only the fields an owner fills, named for what
// they are; the rest keep the action's defaults (Advanced shows them).

import type { Step } from "@sdk/wd";

export interface PlainField {
  title: string;
  description?: string;
}

export interface PlainPanel {
  /** What the step does, said once above its fields. */
  note: string;
  fields: Record<string, PlainField>;
}

/** The plain panel for a connector step, when it is one of the voice and chat steps. */
export function plainPanel(
  connector: string,
  action: string,
  input?: Record<string, unknown>,
): PlainPanel | undefined {
  const id = connector.split("@")[0];
  switch (`${id}:${action}`) {
    case "speech:transcribe":
      return {
        note: "Turns a customer's voice note into words, for the next steps to read and answer.",
        fields: {
          file: {
            title: "The voice note",
            description: "From the customer's message, or an earlier step",
          },
          language: {
            title: "Their language",
            description: "Leave it empty to have it worked out",
          },
        },
      };
    case "speech:speak":
      return {
        note: "Turns a reply into a voice note, to send to the customer in the next step.",
        fields: {
          text: { title: "What to say" },
          voice: {
            title: "Voice",
            description: "Leave it empty for the usual voice",
          },
        },
      };
    case "whatsapp:send_media":
      if (input?.voice !== true) return undefined;
      return {
        note: "Sends a spoken reply as a WhatsApp voice note, which plays in the chat.",
        fields: {
          to: { title: "To", description: "The customer's number" },
          file: {
            title: "The spoken reply",
            description: "From the step that spoke it",
          },
        },
      };
    case "telegram:send_voice":
      return {
        note: "Sends a spoken reply as a Telegram voice message.",
        fields: {
          chat_id: { title: "To", description: "The customer's chat" },
          file: {
            title: "The spoken reply",
            description: "From the step that spoke it",
          },
        },
      };
    case "whatsapp:send_typing":
      return {
        note: "Shows the customer that a reply is being written, and marks their message as read. It lasts until the reply is sent, or 25 seconds.",
        fields: {
          message_id: {
            title: "The message being answered",
            description: "The customer's message",
          },
        },
      };
    case "telegram:send_chat_action":
      return {
        note: "Shows the customer that a reply is on its way, for a few seconds.",
        fields: {
          chat_id: { title: "To", description: "The customer's chat" },
        },
      };
  }
  return undefined;
}

interface Schema {
  properties?: Record<string, object>;
  required?: string[];
}

/** An action's input schema cut to a plain panel's fields, with their plain titles. */
export function plainSchema<S extends Schema>(
  schema: S | undefined,
  panel: PlainPanel,
): S | undefined {
  if (!schema?.properties) return schema;
  const properties: Record<string, object> = {};
  for (const [k, f] of Object.entries(panel.fields)) {
    const p = schema.properties[k];
    if (!p) continue;
    properties[k] = {
      ...p,
      title: f.title,
      ...(f.description ? { description: f.description } : {}),
    };
  }
  return {
    ...schema,
    properties,
    required: (schema.required ?? []).filter((r) => r in properties),
  };
}

export interface VoiceEntry {
  key: string;
  title: string;
  sub: string;
  connector: string;
  action: string;
  input: Record<string, unknown>;
}

/**
 * The voice and chat steps to offer, as far as this Taskiem connects to
 * their services. Their inputs start from a customer's message as the
 * customer guard gives it (trigger.guard), which an assistant's chat
 * trigger has.
 */
export function voiceEntries(
  connectors: { id: string; ref: string }[],
): VoiceEntry[] {
  const ref = (id: string) => connectors.find((c) => c.id === id)?.ref;
  const out: VoiceEntry[] = [];
  const speech = ref("speech");
  const wa = ref("whatsapp");
  const tg = ref("telegram");
  if (speech) {
    out.push(
      {
        key: "voice:hear",
        title: "Hear a voice note",
        sub: "Turn what a customer said into words",
        connector: speech,
        action: "transcribe",
        input: {
          file: "=trigger.guard.file",
          language: "=trigger.guard.language",
        },
      },
      {
        key: "voice:speak",
        title: "Speak a reply",
        sub: "Turn a reply into a voice note",
        connector: speech,
        action: "speak",
        input: {
          text: "",
          customer: "=trigger.guard.sender",
          // Filed under the customer as erasing them names them: a
          // Telegram chat, or a WhatsApp number.
          customer_kind:
            "=trigger.guard.channel.startsWith('telegram@') ? 'chat' : 'phone'",
        },
      },
    );
  }
  if (wa) {
    out.push(
      {
        key: "voice:send_whatsapp",
        title: "Send a voice note on WhatsApp",
        sub: "A spoken reply that plays in the chat",
        connector: wa,
        action: "send_media",
        input: {
          to: "='+' + trigger.guard.sender",
          media_type: "audio",
          voice: true,
          file: "",
        },
      },
      {
        key: "voice:typing_whatsapp",
        title: "Show you are typing on WhatsApp",
        sub: "So the customer sees a reply is coming",
        connector: wa,
        action: "send_typing",
        input: { message_id: "=trigger.item.message.id" },
      },
    );
  }
  if (tg) {
    out.push(
      {
        key: "voice:send_telegram",
        title: "Send a voice note on Telegram",
        sub: "A spoken reply as a voice message",
        connector: tg,
        action: "send_voice",
        input: { chat_id: "=trigger.guard.sender", file: "" },
      },
      {
        key: "voice:typing_telegram",
        title: "Show you are typing on Telegram",
        sub: "So the customer sees a reply is coming",
        connector: tg,
        action: "send_chat_action",
        input: { chat_id: "=trigger.guard.sender", action: "typing" },
      },
    );
  }
  return out;
}

/** A voice entry as a new step. */
export function voiceStep(e: VoiceEntry, id: string): Step {
  return {
    id,
    type: "connector",
    name: e.title,
    connector: e.connector,
    action: e.action,
    input: { ...e.input },
  } as Step;
}
