import { useMemo } from "react";
import { get } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { mightBeRequest, parseRequest, type Request } from "../lib/intents";
import type { StockView } from "../lib/stock";
import type { TradeOrder } from "../lib/tradeorders";
import { useLoad } from "../ui";

// A typed request (docs/spec/chat-tools.md §3): when the words ask for
// something a tool does, a chip over the composer opens its form filled
// in. The words are never acted on by themselves: the form saves only when
// the person taps Save.

export function RequestChip({ text, onOpen }: { text: string; onOpen: (r: Request) => void }) {
  const { can } = useAuth();
  // What requests name is loaded once words look like one.
  const armed = mightBeRequest(text);
  const things = useLoad(
    () =>
      armed
        ? Promise.all([
            can("table.read") ? get<StockView>("/v1/stock").then((r) => r.items, () => []) : Promise.resolve([]),
            can("storefront.manage")
              ? get<{ orders: TradeOrder[] }>("/v1/trade-orders").then((r) => r.orders, () => [])
              : Promise.resolve([]),
          ])
        : Promise.resolve(undefined),
    [armed],
  );
  const req = useMemo(() => {
    if (!armed || !things.data) return null;
    const [items, orders] = things.data;
    return parseRequest(
      text,
      items.map((x) => ({ id: x.id, name: x.name })),
      orders.filter((o) => o.status === "received" || o.status === "changed").map((o) => ({ id: o.id, reference: o.order_reference, buyer: o.buyer_name })),
    );
  }, [armed, things.data, text]);
  if (!req) return null;
  return (
    <div className="tk-request" data-testid="request-chip">
      <button type="button" className="tk-request-btn" onClick={() => onOpen(req)}>
        <span className="tk-request-icon" aria-hidden="true">
          <Icon name="sparkle" />
        </span>
        <span className="tk-request-words">
          <span className="tk-request-kicker">Open the form, filled in</span>
          <span>{req.words}</span>
        </span>
      </button>
    </div>
  );
}
