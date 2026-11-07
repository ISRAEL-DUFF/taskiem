import { useEffect, useMemo, useState } from "react";
import { get, post } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { mightBeRequest, parseRequest, type Request } from "../lib/intents";
import type { StockView } from "../lib/stock";
import type { TradeOrder } from "../lib/tradeorders";
import { useLoad } from "../ui";

// A typed request (docs/spec/chat-tools.md §4): when the words ask for
// something a tool does, a chip over the composer opens its form filled
// in. The words are read here by patterns first; what they do not know,
// the model reads (POST /v1/chat-tools/request) once typing pauses. The
// words are never acted on by themselves: the form saves only when the
// person taps Save.

/** The server's request, as the composer opens it. */
interface ServerRequest {
  tool: Request["tool"];
  row_id?: string;
  add?: boolean;
  prefill: { price_kobo?: number; count?: number; name?: string; minutes?: number };
  words: string;
}

export const fromServer = (r: ServerRequest): Request => ({
  tool: r.tool,
  rowId: r.row_id,
  add: r.add,
  prefill: { priceKobo: r.prefill.price_kobo, count: r.prefill.count, name: r.prefill.name, minutes: r.prefill.minutes },
  words: r.words,
});

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
  // What the patterns do not know, the model reads, once typing pauses.
  const [read, setRead] = useState<{ text: string; req: Request | null }>();
  useEffect(() => {
    if (!armed || req || text.trim().length < 8) return;
    let live = true;
    const t = window.setTimeout(() => {
      post<{ request: ServerRequest | null }>("/v1/chat-tools/request", { text }).then(
        (r) => live && setRead({ text, req: r.request ? fromServer(r.request) : null }),
        () => undefined,
      );
    }, 700);
    return () => {
      live = false;
      window.clearTimeout(t);
    };
  }, [armed, req, text]);
  const shown = req ?? (read?.text === text ? read.req : null);
  if (!shown) return null;
  return (
    <div className="tk-request" data-testid="request-chip">
      <button type="button" className="tk-request-btn" onClick={() => onOpen(shown)}>
        <span className="tk-request-icon" aria-hidden="true">
          <Icon name="sparkle" />
        </span>
        <span className="tk-request-words">
          <span className="tk-request-kicker">Open the form, filled in</span>
          <span>{shown.words}</span>
        </span>
      </button>
    </div>
  );
}
