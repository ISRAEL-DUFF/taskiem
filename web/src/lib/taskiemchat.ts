// The Taskiem conversation on the web (business links L3, §5.5): the
// API's shapes, and how its messages merge and read.

export interface TaskiemButton {
  id?: string;
  title: string;
}

export interface TaskiemMessage {
  id: string;
  channel: "whatsapp" | "telegram" | "slack" | "web";
  inbound: boolean;
  kind: string;
  text: string;
  buttons?: TaskiemButton[];
  created_at: string;
}

const NIL = "00000000-0000-0000-0000-000000000000";

/** Messages merged by id, oldest first; one not kept (a code) has no id and stays as sent. */
export function mergeTaskiem(
  a: TaskiemMessage[],
  b: TaskiemMessage[],
): TaskiemMessage[] {
  const seen = new Map<string, TaskiemMessage>();
  let n = 0;
  for (const m of [...a, ...b])
    seen.set(m.id === NIL ? `local-${n++}-${m.created_at}` : m.id, m);
  return [...seen.values()].sort((x, y) =>
    x.created_at.localeCompare(y.created_at),
  );
}

/** A message's text in parts, WhatsApp's *bold* marked. */
export function boldParts(text: string): { text: string; bold: boolean }[] {
  return text
    .split(/(\*[^*\n]+\*)/g)
    .filter((p) => p !== "")
    .map((p) =>
      /^\*[^*\n]+\*$/.test(p)
        ? { text: p.slice(1, -1), bold: true }
        : { text: p, bold: false },
    );
}
