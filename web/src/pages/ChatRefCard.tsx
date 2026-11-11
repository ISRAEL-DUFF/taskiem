// A thing tagged in a message, as a card (docs/spec/chat-five-more-flows.md,
// phase 2 additions): an invoice, a bill, an order, an order to a supplier
// or a customer, read now, so a payment made since shows as paid. Open
// shows it where it is worked on; the card's one action (send, remind,
// pay, message) is there for those who may.

import { useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { get, postOnce } from "../api";
import { Icon } from "../icons";
import { Pill } from "../kit";
import { toast } from "../toast";
import { naira } from "../lib/invoices";
import type { ToolKey } from "../lib/tools";
import {
  ACTION_WORD,
  cardPill,
  KIND_WORD,
  paidShare,
  shortDate,
  type ChatCard,
} from "../lib/refs";
import { customerKey } from "../lib/customerchat";
import { GiveThis } from "./Tasks";

type OpenTool = (k: ToolKey, from?: DOMRect, start?: { rowId?: string }) => void;

const TOOL: Partial<Record<ChatCard["kind"], ToolKey>> = {
  invoice: "invoices",
  bill: "bills",
  purchase: "purchases",
};

const ICON: Record<ChatCard["kind"], string> = {
  invoice: "invoices",
  bill: "money",
  order: "orders",
  purchase: "orders",
  customer: "members",
};

/** A card read from url, live: again when it is acted on. */
export function RefCard({
  url,
  openTool,
}: {
  /** /v1/chat-card?… or /v1/link-chats/{link}/card?… */
  url: string;
  openTool?: OpenTool;
}) {
  const [c, setC] = useState<ChatCard | null>();
  const [busy, setBusy] = useState(false);
  const [tick, setTick] = useState(0);
  const key = useRef(`card-${Math.random().toString(36).slice(2)}`);
  const navigate = useNavigate();
  useEffect(() => {
    let live = true;
    get<ChatCard>(url).then(
      (x) => live && setC(x),
      // Not one this person may see, or gone: the words stay as they are.
      () => live && setC(null),
    );
    return () => {
      live = false;
    };
  }, [url, tick]);
  if (c === null) return null;
  if (!c)
    return (
      <div className="tk-ref tk-ref-loading" aria-busy="true">
        <span className="sr-only">Loading…</span>
        <span className="tk-shimmer" />
        <span className="tk-shimmer" />
      </div>
    );
  const pill = cardPill(c);
  const tool = TOOL[c.kind];
  const open = (from: DOMRect) => {
    if (c.kind === "order") navigate(`/storefront/orders/${encodeURIComponent(c.name)}`);
    else if (c.kind === "customer") navigate(`/chat?with=${customerKey(c.id)}`);
    else if (tool && openTool) openTool(tool, from, { rowId: c.id });
    else navigate(c.kind === "invoice" ? `/invoices/${c.id}` : c.kind === "bill" ? `/bills/${c.id}` : `/purchases/${c.id}`);
  };
  const act = async (from: DOMRect) => {
    if (c.action === "pay") return open(from);
    if (c.action === "message") return navigate(`/chat?with=${customerKey(c.id)}`);
    setBusy(true);
    try {
      await postOnce(`/v1/invoices/${encodeURIComponent(c.id)}/send`, `${key.current}:${tick}`, {});
      toast(c.action === "remind" ? `Reminder sent for ${c.name}` : `${c.name} sent`, "success");
      setTick((n) => n + 1);
    } catch (e) {
      toast(e instanceof Error ? e.message : "That didn't go through. Try again.", "error");
    } finally {
      setBusy(false);
    }
  };
  const share = paidShare(c);
  const label = `${KIND_WORD[c.kind]} ${c.name}`;
  return (
    <article className={`tk-ref tk-ref-${c.kind}`} aria-label={label} data-testid="ref-card">
      <span className="tk-ref-top">
        <span className={`tk-ref-tile ${c.kind}`} aria-hidden="true">
          <Icon name={ICON[c.kind]} />
        </span>
        <span className="tk-ref-words">
          <span className="tk-kicker">{KIND_WORD[c.kind]}</span>
          <span className="tk-ref-name">{c.name}</span>
          <span className="tk-ref-sub">
            {[c.party, c.kind === "customer" ? `last wrote ${shortDate(c.when)}` : shortDate(c.when)]
              .filter(Boolean)
              .join(" · ")}
          </span>
        </span>
        {pill && (
          <span className="tk-ref-pill">
            <Pill tone={pill.tone}>{pill.text}</Pill>
          </span>
        )}
      </span>
      {c.kind === "customer" ? (
        <span className="tk-ref-stats">
          <span>
            <span>Orders</span>
            <b>{c.orders ?? 0}</b>
          </span>
          {c.owes_kobo !== undefined && (
            <span>
              <span>Owes</span>
              <b className={c.owes_kobo > 0 ? "warn" : undefined}>{naira(c.owes_kobo)}</b>
            </span>
          )}
          {c.last_order && (
            <span className="wide">
              <span>Last order</span>
              <b>
                {c.last_order.reference} · {naira(c.last_order.total_kobo)}
              </b>
              <span className="tk-ref-when">{shortDate(c.last_order.when)}</span>
            </span>
          )}
        </span>
      ) : (
        <>
          <span className="tk-ref-money">
            <b>{naira(c.due_kobo && c.due_kobo > 0 ? c.due_kobo : (c.total_kobo ?? 0))}</b>
            <span>
              {c.due_kobo && c.due_kobo > 0
                ? `to pay of ${naira(c.total_kobo ?? 0)}${c.due_on ? ` · due ${shortDate(c.due_on)}` : ""}`
                : "total"}
            </span>
          </span>
          {(c.paid_kobo ?? 0) > 0 && (c.total_kobo ?? 0) > 0 && (
            <span className="tk-ref-paid">
              <span className="tk-ref-bar" aria-hidden="true">
                <span style={{ width: `${Math.round(share * 100)}%` }} />
              </span>
              <span>{naira(c.paid_kobo ?? 0)} paid</span>
            </span>
          )}
          {c.lines && c.lines.length > 0 && (
            <ul className="tk-ref-lines">
              {c.lines.map((l, i) => (
                <li key={i}>
                  <span>
                    {l.quantity} × {l.name}
                  </span>
                  <b>{naira(l.total_kobo)}</b>
                </li>
              ))}
              {(c.more ?? 0) > 0 && (
                <li className="tk-ref-more">
                  and {c.more} more {c.more === 1 ? "line" : "lines"}
                </li>
              )}
            </ul>
          )}
        </>
      )}
      {(c.kind !== "customer" || c.action) && (
      <span className="tk-ref-foot">
        {c.kind !== "customer" &&
          (c.kind === "order" ? (
            <Link className="tk-btn soft" to={`/storefront/orders/${encodeURIComponent(c.name)}`}>
              Open<span className="sr-only"> {label}</span>
            </Link>
          ) : (
            <button type="button" className="tk-btn soft" onClick={(e) => open(e.currentTarget.getBoundingClientRect())}>
              Open<span className="sr-only"> {label}</span>
            </button>
          ))}
        {c.kind !== "customer" && <GiveThis className="tk-btn soft" link={{ kind: c.kind, id: c.id, name: c.name }} />}
        {c.action && (
          <button
            type="button"
            className="tk-btn primary"
            disabled={busy}
            onClick={(e) => void act(e.currentTarget.getBoundingClientRect())}
          >
            {ACTION_WORD[c.action]}
            <span className="sr-only"> {label}</span>
          </button>
        )}
      </span>
      )}
    </article>
  );
}
