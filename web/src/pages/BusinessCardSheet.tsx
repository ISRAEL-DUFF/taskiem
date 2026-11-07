import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { get, post } from "../api";
import { useAuth } from "../auth";
import { SavedTick, Sheet } from "../kit";
import { pendingTo, type ExploreCard } from "../lib/explore";
import type { BusinessLink } from "../lib/links";
import { ErrorBox, useAction } from "../ui";
import { Card } from "./Explore";

// Chat tools phase 6 (docs/spec/chat-tools.md §8): tagging a business we
// are not linked with shows its Explore card in a sheet over the chat, its
// logo, line, photos and words, with Connect. Connect asks it to link, as
// Explore does (POST /v1/links, both ways); Withdraw takes the ask back.

export function BusinessCardSheet({
  card,
  from,
  onClose,
}: {
  card: ExploreCard;
  from?: DOMRect | null;
  onClose: () => void;
}) {
  const { can } = useAuth();
  const navigate = useNavigate();
  const manage = can("link.manage");
  const act = useAction();
  const [asked, setAsked] = useState<BusinessLink | true>();
  const [state, setState] = useState(card.state);
  const [tick, setTick] = useState<string>();
  const shown = { ...card, state };
  // Asked before: the ask Withdraw takes back.
  useEffect(() => {
    if (card.state !== "requested") return;
    let live = true;
    get<{ links: BusinessLink[] }>("/v1/links").then(
      (r) => {
        const l = pendingTo(r.links, card.business);
        if (live && l) setAsked(l);
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
      setTick(`Asked ${card.name} to link`);
      // The tick says so, then gets out of the way of the card.
      window.setTimeout(() => setTick(undefined), 1400);
    }, false);
  const withdraw = () =>
    void act.run(async () => {
      const l = await pending();
      if (!l) throw new Error("That ask was already answered.");
      await post(`/v1/links/${l.id}/cancel`);
      setAsked(undefined);
      setState("none");
      setTick(undefined);
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
          onConnect={connect}
          onWithdraw={withdraw}
          onMessage={() => navigate("/chat")}
        />
        <p className="tk-x-note">
          Nothing is shared until they accept. Then you can message, see their
          price list and order.
        </p>
        <ErrorBox error={act.error} />
      </div>
    </Sheet>
  );
}
