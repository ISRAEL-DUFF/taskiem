// The audit log's rows (pages/Audit.tsx), short enough to scan: ids cut
// to their first characters, a detail as "key value" pairs on one line.

const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi;

/** Each id in `s` as its first 8 characters: "01a15de9…/3". */
export function shortIds(s: string): string {
  return s.replace(UUID, (id) => `${id.slice(0, 8)}…`);
}

/** An entry's detail as "ip 127.0.0.1 · name Leave request"; long values cut. */
export function detailWords(detail: unknown): string {
  if (detail === null || detail === undefined) return "";
  if (typeof detail !== "object" || Array.isArray(detail)) return shortIds(JSON.stringify(detail));
  return Object.entries(detail as Record<string, unknown>)
    .map(([k, v]) => {
      const text = typeof v === "string" ? v : JSON.stringify(v);
      const short = shortIds(text);
      return `${k.replaceAll("_", " ")} ${short.length > 24 ? `${short.slice(0, 24)}…` : short}`;
    })
    .join(" · ");
}
