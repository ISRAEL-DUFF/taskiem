// Your tools (made tools M1): the business's tools the person may use, read
// once from GET /v1/tools and shared by every chat screen; read again when
// one is opened from a page or an owner changes one.

import { useEffect, useSyncExternalStore } from "react";
import { get } from "../api";
import type { MadeTool } from "./tools";

let value: MadeTool[] | undefined;
let loading: Promise<void> | undefined;
const watchers = new Set<() => void>();

function set(v: MadeTool[]) {
  value = v;
  watchers.forEach((w) => w());
}

/** Reads them again (a tool published, or made more careful). */
export function reloadMadeTools(): Promise<void> {
  loading = get<{ tools: MadeTool[] }>("/v1/tools").then(
    (r) => set(r.tools ?? []),
    () => set(value ?? []),
  );
  return loading;
}

/** Forgets them (sign-out, another business). */
export function forgetMadeTools() {
  value = undefined;
  loading = undefined;
  watchers.forEach((w) => w());
}

/** The person's tools; empty while they are read, or for someone who may
 * not start runs. */
export function useMadeTools(may = true): MadeTool[] {
  const v = useSyncExternalStore(
    (cb) => {
      watchers.add(cb);
      return () => watchers.delete(cb);
    },
    () => value,
  );
  useEffect(() => {
    if (!may || value !== undefined || loading) return;
    void reloadMadeTools();
  }, [may]);
  return may ? (v ?? EMPTY) : EMPTY;
}

const EMPTY: MadeTool[] = [];
