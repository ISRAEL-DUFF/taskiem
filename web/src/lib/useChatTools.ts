// Whether this business has chat tools on (docs/spec/chat-tools.md): read
// once, shared by every chat screen, updated when Settings changes it.

import { useEffect, useSyncExternalStore } from "react";
import { get } from "../api";

let value: boolean | undefined;
let loading: Promise<void> | undefined;
const watchers = new Set<() => void>();

function set(v: boolean) {
  value = v;
  watchers.forEach((w) => w());
}

/** Tells every screen the switch changed (Settings, after saving). */
export function setChatTools(v: boolean) {
  set(v);
}

/** Forgets it (sign-out, another business). */
export function forgetChatTools() {
  value = undefined;
  loading = undefined;
  watchers.forEach((w) => w());
}

export function useChatTools(): boolean {
  const v = useSyncExternalStore(
    (cb) => {
      watchers.add(cb);
      return () => watchers.delete(cb);
    },
    () => value,
  );
  useEffect(() => {
    if (value !== undefined || loading) return;
    loading = get<{ chat_tools: boolean }>("/v1/settings/chat-tools").then(
      (r) => set(!!r.chat_tools),
      () => set(false),
    );
  }, []);
  return !!v;
}
