import { useState } from "react";
import { get } from "../api";
import { useAuth } from "../auth";
import { useLoad } from "../ui";
import { savedSuggestions, type SavedList } from "../lib/saved";
import type { Suggestion } from "../lib/mentions";

// The saved things a chat's "@" offers (saved objects S1, part 2): the
// person's own (any session reaches them) and, in a business, the
// business's. Read the first time "@" is typed in the chat, not when it
// opens; a list that cannot be read offers nothing rather than stopping
// the chat.

const none: SavedList = { saved: [], limit: 0 };

/** The saved things to offer, every kind (the Taskiem chat, which reads an
 * @name) or texts only (chats with other people, until cards), and want:
 * what the picker calls when "@" is first typed. */
export function useSavedPicks(scope: "all" | "texts"): { saved: Suggestion[]; want: () => void } {
  const { me } = useAuth();
  const [wanted, setWanted] = useState(false);
  const inBusiness = !!me?.tenant_id && !me.personal;
  const mine = useLoad(() => (wanted ? get<SavedList>("/v1/me/saved").catch(() => none) : Promise.resolve(none)), [wanted]);
  const business = useLoad(() => (wanted && inBusiness ? get<SavedList>("/v1/saved").catch(() => none) : Promise.resolve(none)), [wanted, inBusiness]);
  return {
    saved: savedSuggestions(mine.data?.saved ?? [], business.data?.saved ?? [], scope === "texts"),
    want: () => {
      if (!wanted) setWanted(true);
    },
  };
}
