import { useEffect, useState } from "react";

// An unsent message kept per conversation, in sessionStorage: it survives
// opening another chat and coming back (and a reload of the tab), and goes
// when the tab closes. The list shows "Draft: …" in its row.

const PREFIX = "taskiem-draft:";
const EVENT = "taskiem-draft";

export function getDraft(key: string): string {
  try {
    return sessionStorage.getItem(PREFIX + key) ?? "";
  } catch {
    return "";
  }
}

/** Keeps the draft (an empty one is removed) and tells the list. */
export function setDraft(key: string, text: string): void {
  try {
    if (text.trim() === "") sessionStorage.removeItem(PREFIX + key);
    else sessionStorage.setItem(PREFIX + key, text);
  } catch {
    // Storage may be blocked: the draft is then only kept while it is open.
  }
  window.dispatchEvent(new Event(EVENT));
}

/** Forgets every draft (sign-out). */
export function clearDrafts(): void {
  try {
    const keys: string[] = [];
    for (let i = 0; i < sessionStorage.length; i++) {
      const k = sessionStorage.key(i);
      if (k?.startsWith(PREFIX)) keys.push(k);
    }
    keys.forEach((k) => sessionStorage.removeItem(k));
  } catch {
    // Storage may be blocked: there is then nothing kept.
  }
  window.dispatchEvent(new Event(EVENT));
}

/** The drafts, by conversation key, following changes. */
export function useDrafts(): Record<string, string> {
  const read = (): Record<string, string> => {
    const out: Record<string, string> = {};
    try {
      for (let i = 0; i < sessionStorage.length; i++) {
        const k = sessionStorage.key(i);
        if (k?.startsWith(PREFIX))
          out[k.slice(PREFIX.length)] = sessionStorage.getItem(k) ?? "";
      }
    } catch {
      // none
    }
    return out;
  };
  const [drafts, setDrafts] = useState(read);
  useEffect(() => {
    const on = () => setDrafts(read());
    window.addEventListener(EVENT, on);
    return () => window.removeEventListener(EVENT, on);
  }, []);
  return drafts;
}
