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

/** The saved things to offer, and want: what the picker calls when "@" is
 * first typed. Scopes: all (the Taskiem chat, which reads an @name); texts
 * (chats that cannot take a card yet); share (every kind, one's own and
 * the business's, the rest sent as cards: a business's customer chats);
 * share-mine (every kind, one's own only: people chats and groups). */
/** complete: both lists were read (Save "mum"… is offered only then, so a
 * name is never offered as new because the list failed); reload: read
 * again, as after something is saved. */
export function useSavedPicks(scope: "all" | "texts" | "share" | "share-mine"): { saved: Suggestion[]; want: () => void; complete: boolean; reload: () => void } {
  const { me } = useAuth();
  const [wanted, setWanted] = useState(false);
  const inBusiness = !!me?.tenant_id && !me.personal;
  const mine = useLoad(() => (wanted ? get<SavedList>("/v1/me/saved") : Promise.resolve(none)), [wanted]);
  const withBusiness = inBusiness && scope !== "share-mine";
  const business = useLoad(() => (wanted && withBusiness ? get<SavedList>("/v1/saved") : Promise.resolve(none)), [wanted, withBusiness]);
  return {
    saved: savedSuggestions(mine.error ? [] : (mine.data?.saved ?? []), business.error ? [] : (business.data?.saved ?? []), scope === "texts"),
    want: () => {
      if (!wanted) setWanted(true);
    },
    complete: wanted && !!mine.data && !mine.error && !!business.data && !business.error,
    reload: () => {
      mine.reload();
      business.reload();
    },
  };
}
