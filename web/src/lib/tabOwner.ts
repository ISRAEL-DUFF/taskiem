// What a browser tab keeps for the person signed in: their chat drafts,
// the message to Taskiem still in flight, the gate's recent checks (names
// and fee standing) and the business an agent is working in. A session
// that runs out (a 401) keeps them, so the same person signing in again
// carries on; someone else signing in in the tab, or signing out, forgets
// them. Unsaved builder edits are not theirs alone (lib/keep.ts): they stay.

import { setAgentSession } from "../api";
import { clearDrafts } from "./drafts";
import { clearOutbox } from "./outboxStore";
import { clearRecent } from "./schoolClearance";
import { keepInflight } from "./taskiemchat";

const OWNER = "taskiem-owner";

function clearPersonal() {
  clearDrafts();
  clearOutbox();
  keepInflight(undefined);
  clearRecent();
  setAgentSession(null);
}

/** Someone signed in (GET /v1/me): forgets what the tab kept for anyone else. */
export function claimTab(user: string): void {
  let owner: string | null = null;
  try {
    owner = sessionStorage.getItem(OWNER);
  } catch {
    // Storage blocked: nothing kept across a reload either.
  }
  if (owner === user) return;
  clearPersonal();
  try {
    sessionStorage.setItem(OWNER, user);
  } catch {
    // As above.
  }
}

/** Signing out: forgets what the tab kept for the person. */
export function forgetTab(): void {
  clearPersonal();
  try {
    sessionStorage.removeItem(OWNER);
  } catch {
    // Storage blocked: there is nothing kept.
  }
}
