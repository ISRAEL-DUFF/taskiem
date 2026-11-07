import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { get, post } from "../api";
import { useAuth } from "../auth";
import { SavedTick, Sheet } from "../kit";
import { pendingTo, type ExploreCard } from "../lib/explore";
import type { Conversation } from "../lib/linkchat";
import type { BusinessLink } from "../lib/links";
import { ErrorBox, useAction } from "../ui";
import { Card } from "./Explore";

// Chat tools phase 6 (docs/spec/chat-tools.md §8): tagging a business we
// are not linked with shows its Explore card in a sheet over the chat, its
// logo, line, photos and words, with Connect. Connect asks it to link, as
// Explore does (POST /v1/links, both ways); Withdraw takes the ask back; a
// business that asked us is answered here (Accept, Not now). Each is a
// diary line on the server.

export function BusinessCardSheet({
  card,
  from,
  onClose,
  onChanged,
}: {
  card: ExploreCard;
  from?: DOMRect | null;
  onClose: () => void;
  /** Where the link stands now, for the picker's row. */
  onChanged?: (business: string, state: ExploreCard["state"]) => void;
}) {
  const { can } = useAuth();
  const navigate = useNavigate();
  const manage = can("link.manage");
  const act = useAction();
  // Our ask (Withdraw takes it back), or theirs (Accept, Not now).
  const [asked, setAsked] = useState<BusinessLink | true>();
  const [incoming, setIncoming] = useState<BusinessLink>();
  const [state, setState] = useState(card.state);
  const [tick, setTick] = useState<string>();
  const timer = useRef(0);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const shown = { ...card, state };
  const say = (words: string) => {
    setTick(words);
    // The tick says so, then gets out of the way of the card.
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setTick(undefined), 1400);
  };
  const moved = (next: ExploreCard["state"]) => {
    setState(next);
    onChanged?.(card.business, next);
  };
  // An ask already made, either way: the link it is.
  useEffect(() => {
    if (card.state !== "requested" && card.state !== "asked_us") return;
    let live = true;
    get<{ links: BusinessLink[] }>("/v1/links").then(
      (r) => {
        if (!live) return;
        if (card.state === "requested") {
          const l = pendingTo(r.links, card.business);
          if (l) setAsked(l);
        } else {
          setIncoming(
            r.links.find(
              (l) => l.business === card.business && l.state === "asked_us",
            ),
          );
        }
      },
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [card.state, card.business]);
  const pending = async (): Promise<BusinessLink | undefined> => {
    if (asked && asked !== true) return asked;
    const r = await get<{ links: BusinessLink[] }>("/v1/links");
    return pendingTo(r.links, card.business);
  };
  const connect = () =>
    void act.run(async () => {
      const out = await post<{ link?: BusinessLink }>("/v1/links", {
        business: card.business,
        role: "both",
      });
      setAsked(out.link ?? true);
      onChanged?.(card.business, "requested");
      say(`Asked ${card.name} to link`);
    }, false);
  const withdraw = () =>
    void act.run(async () => {
      const l = await pending();
      if (!l) throw new Error("That ask was already answered.");
      await post(`/v1/links/${l.id}/cancel`);
      setAsked(undefined);
      setTick(undefined);
      moved("none");
    }, false);
  const answer = (l: BusinessLink, a: "accept" | "decline") =>
    void act.run(async () => {
      await post(`/v1/links/${l.id}/${a}`);
      setIncoming(undefined);
      moved(a === "accept" ? "active" : "none");
      say(a === "accept" ? `Linked with ${card.name}` : "Request declined");
    }, false);
  // Linked now: their conversation (the list may not have it yet).
  const message = () =>
    void act.run(async () => {
      const r = await get<{ conversations: Conversation[] }>("/v1/link-chats");
      const conv = r.conversations.find((x) => x.business === card.business);
      navigate(conv ? `/chat?with=${conv.link}` : "/chat");
    }, false);
  return (
    <Sheet
      title="Explore"
      from={from}
      onClose={onClose}
      testid="business-card-sheet"
    >
      {tick && <SavedTick key={tick} words={tick} />}
      <div className="tk-x-sheet">
        <Card
          as="div"
          card={shown}
          manage={manage}
          busy={act.busy}
          asked={!!asked}
          pending={asked && asked !== true ? asked : undefined}
          incoming={incoming}
          onAnswer={answer}
          onConnect={connect}
          onWithdraw={withdraw}
          onMessage={message}
        />
        {state !== "active" && state !== "asked_us" && (
          <p className="tk-x-note">
            Nothing is shared until they accept. Then you can message, see
            their price list and order.
          </p>
        )}
        <ErrorBox error={act.error} />
      </div>
    </Sheet>
  );
}
