import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, get, post, postOnce } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { KitEmpty, KitField, KitLoading, SavedTick, Sheet } from "../kit";
import {
  ago,
  ALERT_LOOK,
  reorderWords,
  shownAlerts,
  type ChatAlert,
} from "../lib/alerts";
import { subjectWords } from "../lib/approvals";
import { formatPrice } from "../lib/storefront";
import type { ToolKey } from "../lib/tools";
import { passkeysSupported, stepUpAssert } from "../passkeys";
import { ErrorBox, useAction } from "../ui";
import { OrderSheet } from "./ChatBuyer";

// Alerts you can act on (docs/spec/chat-tools.md §6, the Alerts design): the
// Taskiem conversation ends with what needs the person now, each a card with
// its next step, which acts in place or opens the right tool, filled in.
// Read from GET /v1/chat-alerts while the conversation is open; a card dealt
// with leaves, and Later puts it off for a few hours.

const key = () => `alert-${crypto.randomUUID()}`;

/** The person's alerts, read now and every minute while chat tools are on. */
export function useChatAlerts(on: boolean) {
  const [list, setList] = useState<ChatAlert[]>();
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (!on) {
      setList([]);
      return;
    }
    let live = true;
    const load = () =>
      get<{ alerts: ChatAlert[] }>("/v1/chat-alerts").then(
        (r) => live && setList(r.alerts),
        () => undefined,
      );
    void load();
    const t = window.setInterval(() => void load(), 60_000);
    return () => {
      live = false;
      window.clearInterval(t);
    };
  }, [on, tick]);
  return { list, reload: () => setTick((n) => n + 1) };
}

type Open = (k: ToolKey, from?: DOMRect, start?: { rowId?: string }) => void;
type SheetFor =
  | { kind: "order" | "rider" | "match"; a: ChatAlert; from?: DOMRect }
  | {
      kind: "approve";
      a: ChatAlert;
      decision: "approved" | "rejected";
      from?: DOMRect;
    };

/** The cards at the end of the conversation, and the sheets they open. */
export function AlertCards({
  alerts,
  onReload,
  onDiary,
  openTool,
}: {
  alerts: ChatAlert[] | undefined;
  onReload: () => void;
  /** Something changed: what is newer in the conversation is pulled. */
  onDiary: () => void;
  openTool: Open;
}) {
  const [gone, setGone] = useState<Set<string>>(new Set());
  const [leaving, setLeaving] = useState<Set<string>>(new Set());
  const [sheet, setSheet] = useState<SheetFor>();
  // Dealt with: it leaves with its motion, then the list is read again.
  const done = (a: ChatAlert) => {
    setLeaving((s) => new Set([...s, a.key]));
    window.setTimeout(() => {
      setGone((s) => new Set([...s, a.key]));
      onDiary();
      onReload();
    }, 280);
  };
  const shown = shownAlerts(alerts, gone);
  if (shown.length === 0 && !sheet) return null;
  return (
    <>
      <ul
        className="tk-alerts"
        aria-label="Needs you"
        data-testid="chat-alerts"
      >
        {shown.map((a) => (
          <li
            key={a.key}
            className={leaving.has(a.key) ? "leaving" : undefined}
          >
            {a.kind === "approval" ? (
              <ApprovalCard
                a={a}
                onDecide={(decision, from) =>
                  setSheet({ kind: "approve", a, decision, from })
                }
              />
            ) : (
              <AlertCard
                a={a}
                onDone={() => done(a)}
                onSheet={(kind, from) => setSheet({ kind, a, from })}
                openTool={openTool}
              />
            )}
          </li>
        ))}
      </ul>
      {sheet?.kind === "order" && sheet.a.reorder && (
        <OrderSheet
          link={sheet.a.reorder.link}
          business={sheet.a.reorder.supplier}
          from={sheet.from}
          start={{
            qty: { [sheet.a.reorder.seller_item_id]: sheet.a.reorder.quantity },
          }}
          onClose={() => setSheet(undefined)}
          onPlaced={() => done(sheet.a)}
        />
      )}
      {sheet?.kind === "rider" && (
        <RiderSheet
          a={sheet.a}
          from={sheet.from}
          onClose={() => setSheet(undefined)}
          onDone={() => done(sheet.a)}
        />
      )}
      {sheet?.kind === "match" && (
        <MatchSheet
          a={sheet.a}
          from={sheet.from}
          onClose={() => setSheet(undefined)}
          onDone={() => done(sheet.a)}
        />
      )}
      {sheet?.kind === "approve" && (
        <ApproveSheet
          a={sheet.a}
          decision={sheet.decision}
          from={sheet.from}
          onClose={() => setSheet(undefined)}
          onDone={() => done(sheet.a)}
        />
      )}
    </>
  );
}

/** An alert: what it is, and its next step with Later. */
function AlertCard({
  a,
  onDone,
  onSheet,
  openTool,
}: {
  a: ChatAlert;
  onDone: () => void;
  onSheet: (kind: "order" | "rider" | "match", from: DOMRect) => void;
  openTool: Open;
}) {
  const { can } = useAuth();
  const act = useAction();
  const card = useRef<HTMLElement>(null);
  const look = ALERT_LOOK[a.kind];
  const from = () => card.current?.getBoundingClientRect() ?? new DOMRect();
  const later = () =>
    void act
      .run(() => post("/v1/chat-alerts/later", { key: a.key }), false)
      .then((ok) => ok && onDone());
  const step = (() => {
    switch (a.kind) {
      case "low_stock":
        if (a.reorder)
          return {
            words: reorderWords(a.reorder),
            go: () => onSheet("order", from()),
          };
        if (can("table.edit"))
          return {
            words: "Record stock in",
            go: () => openTool("stock", from(), { rowId: a.id }),
          };
        return {
          words: "Open stock",
          go: () => openTool("stock", from(), { rowId: a.id }),
        };
      case "overdue":
        if (!can("invoice.manage")) return undefined;
        return {
          words: "Send reminder",
          go: () =>
            void act
              .run(
                () =>
                  postOnce(
                    `/v1/invoices/${encodeURIComponent(a.id ?? "")}/send`,
                    key(),
                  ),
                `Reminder sent for ${a.reference}`,
              )
              .then((ok) => ok && onDone()),
        };
      case "new_order":
        return {
          words: "Accept",
          go: () =>
            void act
              .run(
                () =>
                  post(
                    `/v1/trade-orders/${encodeURIComponent(a.id ?? "")}/accept`,
                  ),
                `${a.reference} accepted`,
              )
              .then((ok) => ok && onDone()),
        };
      case "rider":
        return { words: "Assign rider", go: () => onSheet("rider", from()) };
      case "unmatched":
        return can("invoice.manage")
          ? { words: "Match it", go: () => onSheet("match", from()) }
          : undefined;
      default:
        return undefined;
    }
  })();
  return (
    <article
      ref={card}
      className="tk-alert"
      aria-label={a.title}
      data-testid="chat-alert"
      data-kind={a.kind}
    >
      <span className="tk-alert-top">
        <span className={`tk-alert-tile ${look.tone}`} aria-hidden="true">
          <Icon name={look.icon} />
        </span>
        <span className="tk-alert-words">
          <span className="tk-alert-title">{a.title}</span>
          <span className="tk-alert-line">{a.line}</span>
        </span>
      </span>
      <span className="tk-alert-acts">
        {step && (
          <button
            type="button"
            className="tk-alert-go"
            disabled={act.busy}
            onClick={step.go}
          >
            {step.words}
          </button>
        )}
        {a.kind === "new_order" ? (
          <button
            type="button"
            className="tk-alert-soft"
            onClick={() => openTool("orders", from(), { rowId: a.id })}
          >
            Open<span className="sr-only"> {a.reference}</span>
          </button>
        ) : (
          <button
            type="button"
            className="tk-alert-soft"
            disabled={act.busy}
            onClick={later}
          >
            Later
          </button>
        )}
      </span>
      <ErrorBox error={act.error} />
    </article>
  );
}

/** An approval: who asks for what, the amount, Decline and Approve. */
function ApprovalCard({
  a,
  onDecide,
}: {
  a: ChatAlert;
  onDecide: (d: "approved" | "rejected", from: DOMRect) => void;
}) {
  const ap = a.approval;
  const card = useRef<HTMLElement>(null);
  if (!ap) return null;
  const w = subjectWords(ap.subject);
  const from = () => card.current?.getBoundingClientRect() ?? new DOMRect();
  return (
    <article
      ref={card}
      className="tk-approve"
      aria-label={`Needs your OK: ${a.title}`}
      data-testid="chat-approval"
    >
      <span className="tk-approve-head">
        <span className="tk-kicker">Needs your OK</span>
        <time dateTime={a.at}>{ago(a.at)}</time>
      </span>
      <span className="tk-approve-ask">
        {a.who ? `${a.who} asks: ${a.title}` : a.title}
      </span>
      <span className="tk-approve-money">
        {w.amount && <b>{w.amount}</b>}
        {(w.to || w.account) && (
          <span>
            {w.to && <>to {w.to}</>}
            {w.to && w.account && <br />}
            {w.account}
          </span>
        )}
      </span>
      {(ap.first_payment || ap.step_up) && (
        <span className="tk-approve-notes">
          {ap.first_payment && (
            <span className="tk-pill warn">First payment to this account</span>
          )}
          {ap.step_up && (
            <span className="tk-pill plain">
              <Icon name="lock" />
              {ap.step_up === "passkey"
                ? "Needs your passkey"
                : "Needs your passkey or a code"}
            </span>
          )}
        </span>
      )}
      <span className="tk-approve-acts">
        <button
          type="button"
          className="tk-alert-soft"
          onClick={() => onDecide("rejected", from())}
        >
          Decline<span className="sr-only"> {a.title}</span>
        </button>
        <button
          type="button"
          className="tk-approve-go"
          onClick={() => onDecide("approved", from())}
        >
          <Icon name="fingerprint" />
          Approve<span className="sr-only"> {a.title}</span>
        </button>
      </span>
    </article>
  );
}

/** Approve or Decline, confirmed: what it is, a comment, and the passkey
 * or code its rules ask for; the server refuses it without them. */
function ApproveSheet({
  a,
  decision,
  from,
  onClose,
  onDone,
}: {
  a: ChatAlert;
  decision: "approved" | "rejected";
  from?: DOMRect;
  onClose: () => void;
  onDone: () => void;
}) {
  const ap = a.approval as NonNullable<ChatAlert["approval"]>;
  const approve = decision === "approved";
  const w = subjectWords(ap.subject);
  const [comment, setComment] = useState("");
  const [code, setCode] = useState("");
  const [needsCode, setNeedsCode] = useState(Boolean(ap.step_up) && approve);
  const passkeyOnly = ap.step_up === "passkey";
  const act = useAction();
  const [saved, setSaved] = useState(false);
  const [shut, setShut] = useState(false);
  const send = async (extra: object) => {
    try {
      await post(
        `/v1/approvals/${ap.run_id}/${encodeURIComponent(ap.step_id)}`,
        {
          decision,
          ...(comment.trim() && { comment: comment.trim() }),
          ...extra,
        },
      );
    } catch (e) {
      if (e instanceof ApiError && e.body.step_up) setNeedsCode(true);
      throw e;
    }
    setSaved(true);
    window.setTimeout(() => {
      setShut(true);
      onDone();
    }, 1200);
  };
  const confirm = () =>
    void act.run(() => send(code ? { totp: code } : {}), false);
  // The passkey is asked for this decision on this approval only.
  const withPasskey = () =>
    void act.run(
      async () =>
        send({
          passkey: await stepUpAssert(
            "approval.decide",
            `${ap.run_id}/${ap.step_id}/${decision}`,
          ),
        }),
      false,
    );
  const rows: [string, string][] = [
    ...(w.amount ? [["Amount", w.amount] as [string, string]] : []),
    ...(w.to ? [["To", w.to] as [string, string]] : []),
    ...(w.account ? [["Account", w.account] as [string, string]] : []),
    ...w.rest,
  ];
  return (
    <Sheet
      title={approve ? "Approve it?" : "Decline it?"}
      from={from}
      onClose={onClose}
      shut={shut}
      testid="approve-sheet"
    >
      {saved && <SavedTick words={approve ? "Approved" : "Declined"} />}
      <div className="tk-sheet-list tk-form-body">
        <p className="tk-approve-ask">
          {a.who ? `${a.who} asks: ${a.title}` : a.title}
        </p>
        <p className="tk-form-now">
          From {ap.workflow}. Your decision and what you were shown are
          recorded.
        </p>
        {rows.length > 0 && (
          <ul className="tk-lines">
            {rows.map(([k, v]) => (
              <li key={k}>
                <span>{k}</span>
                <b>{v}</b>
              </li>
            ))}
          </ul>
        )}
        {ap.first_payment && approve && (
          <p className="tk-form-now" role="note">
            <b>First payment to this account.</b> Your business has not paid it
            before. Check the account number and name with the person you are
            paying before you approve.
          </p>
        )}
        <KitField label="A comment (optional, recorded)">
          <textarea
            rows={2}
            value={comment}
            onChange={(e) => setComment(e.target.value)}
          />
        </KitField>
        {needsCode && !passkeyOnly && (
          <KitField
            label="Or a code from your authenticator app"
            hint={
              <>
                No authenticator yet? Add one under{" "}
                <Link to="/account">Account</Link>.
              </>
            }
          >
            <input
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
            />
          </KitField>
        )}
        {needsCode && !passkeysSupported() && passkeyOnly && (
          <p className="tk-form-now">
            This needs your passkey, and this browser cannot use one. Approve it
            on a device with your passkey.
          </p>
        )}
        <ErrorBox error={act.error} />
      </div>
      <div className="tk-sheet-foot tk-foot-two">
        <button
          type="button"
          className="tk-btn ghost"
          disabled={act.busy}
          onClick={onClose}
        >
          Not now
        </button>
        {needsCode && passkeysSupported() && code === "" ? (
          <button
            type="button"
            className="tk-cta"
            disabled={act.busy}
            onClick={withPasskey}
          >
            <Icon name="fingerprint" />
            Confirm with your passkey
          </button>
        ) : (
          <button
            type="button"
            className={approve ? "tk-cta" : "tk-cta tk-danger"}
            disabled={
              act.busy || (needsCode && (passkeyOnly || code.length !== 6))
            }
            onClick={confirm}
          >
            {approve ? "Approve" : "Decline"}
          </button>
        )}
      </div>
    </Sheet>
  );
}

interface Rider {
  id: string;
  name: string;
  active: boolean;
  zones: string[];
}

/** Assign rider: the next in turn, or one by name. */
function RiderSheet({
  a,
  from,
  onClose,
  onDone,
}: {
  a: ChatAlert;
  from?: DOMRect;
  onClose: () => void;
  onDone: () => void;
}) {
  const [riders, setRiders] = useState<Rider[]>();
  const [error, setError] = useState<unknown>();
  const act = useAction();
  const [saved, setSaved] = useState<string>();
  const [shut, setShut] = useState(false);
  useEffect(() => {
    get<{ riders: Rider[] }>("/v1/riders").then(
      (r) => setRiders(r.riders.filter((x) => x.active)),
      setError,
    );
  }, []);
  const offer = (r?: Rider) =>
    void act
      .run(
        () =>
          post<{ rider?: string }>(
            `/v1/storefront/orders/${encodeURIComponent(a.reference ?? "")}/offer`,
            r ? { rider_id: r.id } : {},
          ),
        false,
      )
      .then((ok) => {
        if (!ok) return;
        setSaved(r ? `Offered to ${r.name}` : "Offered to the next rider");
        window.setTimeout(() => {
          setShut(true);
          onDone();
        }, 1200);
      });
  return (
    <Sheet
      title={`A rider for ${a.reference}`}
      from={from}
      onClose={onClose}
      shut={shut}
      testid="rider-sheet"
    >
      {saved && <SavedTick words={saved} />}
      <p className="tk-form-now tk-pad">{a.line}</p>
      <ErrorBox error={error ?? act.error} />
      {!riders ? (
        !error && <KitLoading rows={2} label="Loading your riders…" />
      ) : riders.length === 0 ? (
        <KitEmpty icon="store" title="No riders on duty">
          Add riders on the Deliveries page.
        </KitEmpty>
      ) : (
        <ul className="tk-sheet-list tk-rows">
          <li>
            <button
              type="button"
              className="tk-row"
              disabled={act.busy}
              onClick={() => offer()}
            >
              <span className="tk-row-tile" aria-hidden="true">
                <Icon name="loop" />
              </span>
              <span className="tk-row-words">
                <span className="tk-row-name">Next in turn</span>
                <span className="tk-row-sub">
                  Riders are asked one after another
                </span>
              </span>
            </button>
          </li>
          {riders.map((r) => (
            <li key={r.id}>
              <button
                type="button"
                className="tk-row"
                disabled={act.busy}
                onClick={() => offer(r)}
              >
                <span className="tk-row-tile" aria-hidden="true">
                  <Icon name="store" />
                </span>
                <span className="tk-row-words">
                  <span className="tk-row-name">{r.name}</span>
                  <span className="tk-row-sub">
                    {r.zones.join(", ") || "Any area"}
                  </span>
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </Sheet>
  );
}

interface OpenInvoice {
  id: string;
  number: string;
  status: string;
  customer: { name: string };
  due_kobo: number;
}

/** Match a payment: the open invoices, the one of the same amount first. */
function MatchSheet({
  a,
  from,
  onClose,
  onDone,
}: {
  a: ChatAlert;
  from?: DOMRect;
  onClose: () => void;
  onDone: () => void;
}) {
  const [list, setList] = useState<OpenInvoice[]>();
  const [error, setError] = useState<unknown>();
  const [search, setSearch] = useState("");
  const act = useAction();
  const [saved, setSaved] = useState<string>();
  const [shut, setShut] = useState(false);
  useEffect(() => {
    get<{ invoices: OpenInvoice[] }>("/v1/invoices?limit=200").then(
      (r) =>
        setList(
          r.invoices
            .filter(
              (x) =>
                x.due_kobo > 0 &&
                ["recorded", "sent", "part_paid", "overdue"].includes(x.status),
            )
            .sort(
              (x, y) =>
                Math.abs(x.due_kobo - (a.amount_kobo ?? 0)) -
                Math.abs(y.due_kobo - (a.amount_kobo ?? 0)),
            ),
        ),
      setError,
    );
  }, []);
  const q = search.trim().toLowerCase();
  const shown = (list ?? []).filter(
    (x) =>
      !q ||
      x.number.toLowerCase().includes(q) ||
      x.customer.name.toLowerCase().includes(q),
  );
  const match = (x: OpenInvoice) =>
    void act
      .run(
        () =>
          post(`/v1/money/entries/${encodeURIComponent(a.id ?? "")}/match`, {
            invoice: x.number,
          }),
        false,
      )
      .then((ok) => {
        if (!ok) return;
        setSaved(`Matched to ${x.number}`);
        window.setTimeout(() => {
          setShut(true);
          onDone();
        }, 1200);
      });
  return (
    <Sheet
      title={
        a.amount_kobo !== undefined
          ? `Match ${formatPrice(a.amount_kobo)}`
          : "Match the payment"
      }
      from={from}
      onClose={onClose}
      shut={shut}
      testid="match-sheet"
    >
      {saved && <SavedTick words={saved} />}
      <p className="tk-form-now tk-pad">{a.line}. Which invoice does it pay?</p>
      <label className="tk-search">
        <Icon name="search" />
        <span className="sr-only">Search open invoices</span>
        <input
          type="search"
          placeholder="Search open invoices"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </label>
      <ErrorBox error={error ?? act.error} />
      {!list ? (
        !error && <KitLoading rows={3} label="Loading open invoices…" />
      ) : shown.length === 0 ? (
        <KitEmpty
          icon="invoices"
          title={q ? "No open invoice like that" : "No open invoices"}
        />
      ) : (
        <ul className="tk-sheet-list tk-rows">
          {shown.map((x) => (
            <li key={x.id}>
              <button
                type="button"
                className="tk-row"
                disabled={act.busy}
                onClick={() => match(x)}
              >
                <span className="tk-row-tile" aria-hidden="true">
                  <Icon name="invoices" />
                </span>
                <span className="tk-row-words">
                  <span className="tk-row-name">{x.number}</span>
                  <span className="tk-row-sub">
                    {x.customer.name} · {formatPrice(x.due_kobo)} to pay
                  </span>
                </span>
                {x.due_kobo === a.amount_kobo && (
                  <span className="tk-pill lime">Same amount</span>
                )}
              </button>
            </li>
          ))}
        </ul>
      )}
    </Sheet>
  );
}
