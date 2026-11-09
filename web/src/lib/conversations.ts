// Words for the conversations page (docs/conversations.md): what happened
// to a message, why a reply was held back, and a template's blanks.

export interface TurnStatus {
  role: "customer" | "assistant" | "member" | "held";
  status?: string;
  reason?: string;
}

/** Seconds as a short time. */
export function seconds(n: number | null | undefined): string {
  if (n == null) return "—";
  if (n < 60) return `${Math.round(n)} s`;
  return `${Math.round(n / 6) / 10} min`;
}

const held: Record<string, string> = {
  empty: "it was empty",
  placeholder: "it had a blank the model did not fill",
  price: "it gave a price your material does not state",
  link: "it had a link to a site you have not allowed",
  contact: "it had a number or email that is not yours",
  secret: "it looked like it had a password or key in it",
  promise:
    "it promised something free, a discount or a refund you do not offer",
  internal: "it had something you asked never to tell customers",
};

/** Why the check held a reply back, in words ("price, link" → reasons). */
export function heldWords(reason: string | undefined): string {
  const words = (reason ?? "")
    .split(",")
    .map((k) => k.trim())
    .filter(Boolean)
    .map((k) => held[k] ?? "it did not pass the check");
  return [...new Set(words)].join("; ");
}

/** What happened to a message, in words. */
export function statusWords(t: TurnStatus): string {
  if (t.role === "held") {
    const why = heldWords(t.reason);
    return `Held back, not sent${why ? `: ${why}` : ""}. The customer was told someone will get back to them`;
  }
  if (t.role === "member")
    switch (t.status) {
      case "failed":
        return "Not sent";
      case "unknown":
        return "May not have been sent";
      case "sending":
        return "Sending…";
      default:
        return "";
    }
  if (t.role !== "customer") return "";
  switch (t.status) {
    case "quiet":
      return t.reason === "paused"
        ? "Not answered: your assistant was paused"
        : "Not answered: a person was answering";
    case "dropped":
      switch (t.reason) {
        case "limited":
          return "Not answered: too many messages";
        case "stopped":
          return "Not answered: they had written STOP";
        case "kind":
          return "Not answered: a kind of message your assistant does not read";
        case "busy":
          return "Not answered: your assistant was too busy";
        default:
          return "Not answered";
      }
    case "reply":
      switch (t.reason) {
        case "stopped":
          return "They wrote STOP: told they will get no more messages";
        case "started":
          return "They wrote START: they get messages again";
        case "handover":
          return "They asked for a person: told someone will answer";
        case "limited":
          return "Too many messages: told to wait a little";
        case "kind":
          return "A kind of message your assistant does not read: told so";
        default:
          return "Answered with a fixed message";
      }
  }
  return "";
}

/** A kind of message, in words ("" for text). */
export function kindWords(kind: string): string {
  return (
    (
      {
        voice: "Voice note",
        audio: "Audio",
        image: "Picture",
        video: "Video",
        document: "Document",
        sticker: "Sticker",
        location: "Place",
        contact: "Contact",
        reaction: "Reaction",
        button: "Button",
        interactive: "Buttons",
      } as Record<string, string>
    )[kind] ?? ""
  );
}

/** How many blanks ({{1}}, {{2}}…) a template's body has. */
export function templateSlots(body: string): number {
  let n = 0;
  for (const m of body.matchAll(/\{\{(\d+)\}\}/g))
    n = Math.max(n, Number(m[1]));
  return n;
}

/** A template's body with its blanks filled in. */
export function fillTemplate(body: string, values: string[]): string {
  return body.replace(
    /\{\{(\d+)\}\}/g,
    (all, i) => values[Number(i) - 1] || all,
  );
}
