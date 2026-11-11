import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { GiveThis } from "./Tasks";
import { createPortal } from "react-dom";
import { Link, useNavigate } from "react-router-dom";
import { ApiError, get, post, postOnce } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import {
  KitEmpty,
  KitField,
  KitLoading,
  reducedMotion,
  SavedTick,
  Sheet,
} from "../kit";
import {
  ago,
  ALERT_LOOK,
  reorderWords,
  shownAlerts,
  type ChatAlert,
} from "../lib/alerts";
import { subjectWords } from "../lib/approvals";
import { customerKey } from "../lib/customerchat";
import { chime, soundOn } from "../lib/moments";
import { formatPrice } from "../lib/storefront";
import type { ToolKey } from "../lib/tools";
import { passkeysSupported, stepUpAssert } from "../passkeys";
import { ErrorBox, useAction } from "../ui";
import { OrderSheet } from "./ChatBuyer";

// Alerts you can act on (docs/spec/chat-tools.md §6, the Alerts design): the
// Taskiem conversation ends with what needs the person now, each a card with
// its next step, which acts in place or opens the right tool, filled in.
// Read from GET /v1/chat-alerts while the app is in view; a card dealt with
// leaves, and Later puts it off for a few hours.

const key = () => `alert-${crypto.randomUUID()}`;

// One read of the alerts for the whole app (the conversation and the list
// of conversations), every minute while the tab is in view and at once when
// it comes back.
const store = {
  list: undefined as ChatAlert[] | undefined,
  users: 0,
  timer: 0,
  seen: undefined as Set<string> | undefined,
  listeners: new Set<() => void>(),
};
const tell = () => store.listeners.forEach((f) => f());
// News worth a sound: a new order, money that came in, an approval.
const NEWS = new Set(["new_order", "unmatched", "approval", "deal"]);

function loadAlerts() {
  if (typeof document !== "undefined" && document.visibilityState === "hidden")
    return;
  get<{ alerts: ChatAlert[] }>("/v1/chat-alerts").then(
    (r) => {
      // A card new since the last read comes with the sound the owner can
      // turn off; the first read is quiet.
      if (
        store.seen &&
        soundOn() &&
        r.alerts.some((a) => NEWS.has(a.kind) && !store.seen?.has(a.key))
      )
        chime();
      store.seen = new Set(r.alerts.map((a) => a.key));
      store.list = r.alerts;
      tell();
    },
    () => undefined,
  );
}
const onVisible = () => document.visibilityState === "visible" && loadAlerts();

function start() {
  if (store.users++ > 0) return;
  loadAlerts();
  store.timer = window.setInterval(loadAlerts, 60_000);
  document.addEventListener("visibilitychange", onVisible);
  window.addEventListener("focus", loadAlerts);
}
function stop() {
  if (--store.users > 0) return;
  window.clearInterval(store.timer);
  document.removeEventListener("visibilitychange", onVisible);
  window.removeEventListener("focus", loadAlerts);
  store.list = undefined;
  store.seen = undefined;
}

/** The person's alerts while chat tools are on (none otherwise). */
export function useChatAlerts(on: boolean) {
  useEffect(() => {
    if (!on) return;
    start();
    return stop;
  }, [on]);
  const list = useSyncExternalStore(
    (f) => (store.listeners.add(f), () => store.listeners.delete(f)),
    () => store.list,
  );
  return { list: on ? list : [], reload: loadAlerts };
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

/** The cards at the end of the conversation, and the sheets they open (out
 * of the conversation's log, so a screen reader does not read them as new
 * messages). */
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
  // Dealt with: it leaves with its motion (at once with motion turned
  // down), then the list is read again.
  const done = (a: ChatAlert) => {
    const go = () => {
      setGone((s) => new Set([...s, a.key]));
      onDiary();
      onReload();
    };
    if (reducedMotion()) return go();
    setLeaving((s) => new Set([...s, a.key]));
    window.setTimeout(go, 280);
  };
  const shown = shownAlerts(alerts, gone);
  const close = () => setSheet(undefined);
  const sheets = sheet && (
    <>
      {sheet.kind === "order" && sheet.a.reorder && (
        <OrderSheet
          link={sheet.a.reorder.link}
          business={sheet.a.reorder.supplier}
          from={sheet.from}
          start={
            sheet.a.reorder.sizes
              ? // The low sizes, in the product's grid.
                {
                  qty: sheet.a.reorder.sizes,
                  itemId: sheet.a.reorder.seller_item_id,
                }
              : {
                  qty: {
                    [sheet.a.reorder.seller_item_id]: sheet.a.reorder.quantity,
                  },
                }
          }
          onClose={close}
          onPlaced={() => done(sheet.a)}
        />
      )}
      {sheet.kind === "rider" && (
        <RiderSheet
          a={sheet.a}
          from={sheet.from}
          onClose={close}
          onDone={() => done(sheet.a)}
        />
      )}
      {sheet.kind === "match" && (
        <MatchSheet
          a={sheet.a}
          from={sheet.from}
          onClose={close}
          onDone={() => done(sheet.a)}
        />
      )}
      {sheet.kind === "approve" && (
        <ApproveSheet
          a={sheet.a}
          decision={sheet.decision}
          from={sheet.from}
          onClose={close}
          onDone={() => done(sheet.a)}
        />
      )}
    </>
  );
  if (shown.length === 0 && !sheet) return null;
  return (
    <>
      {shown.length > 0 && (
        <ul
          className="tk-alerts"
          role="list"
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
      )}
      {sheets && createPortal(sheets, document.body)}
    </>
  );
}

/** An alert: what it is, and its next step with Later. Every button names
 * its card for a screen reader ("Later: Rice is running low"). */
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
  const nav = useNavigate();
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
          // Sent: the card is put off for a day, not shown again at once.
          go: () =>
            void act
              .run(async () => {
                await postOnce(
                  `/v1/invoices/${encodeURIComponent(a.id ?? "")}/send`,
                  key(),
                );
                await post("/v1/chat-alerts/later", { key: a.key, hours: 24 });
              }, `Reminder sent for ${a.reference}`)
              .then((ok) => ok && onDone()),
        };
      case "new_order":
        // The order's lines first, then Accept there.
        return {
          words: "Review order",
          go: () => openTool("orders", from(), { rowId: a.id }),
        };
      case "deal":
        // Staff would agree under the lowest price: the owner's OK.
        if (!can("storefront.manage")) return undefined;
        return {
          words: `Agree${a.amount_kobo !== undefined ? ` ${formatPrice(a.amount_kobo)}` : ""}`,
          go: () =>
            void act
              .run(
                () =>
                  post(
                    `/v1/trade-orders/${encodeURIComponent(a.id ?? "")}/accept`,
                  ),
                `Agreed on ${a.reference}`,
              )
              .then((ok) => ok && onDone()),
        };
      case "rider":
        return { words: "Assign rider", go: () => onSheet("rider", from()) };
      case "confirm":
        // The rider's fee goes out on the owner's word (chat-riders phase 4).
        if (!can("storefront.manage")) return undefined;
        return {
          words: "Confirm, pay the fee",
          go: () =>
            void act
              .run(
                () =>
                  post(
                    `/v1/storefront/orders/${encodeURIComponent(a.reference ?? "")}/confirm`,
                    {},
                  ),
                `Confirmed ${a.reference}`,
              )
              .then((ok) => ok && onDone()),
        };
      case "order_draft":
        // The customer's thread, with the order on its card.
        return {
          words: "Open the order",
          go: () => nav(`/chat?with=${customerKey(a.id ?? "")}`),
        };
      case "unmatched":
        return can("invoice.manage")
          ? { words: "Match it", go: () => onSheet("match", from()) }
          : undefined;
      default:
        return undefined;
    }
  })();
  const named = <span className="sr-only">: {a.title}</span>;
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
            {named}
          </button>
        )}
        {a.kind === "low_stock" && a.id && (
          <GiveThis className="tk-alert-soft" link={{ kind: "stock", id: a.id, name: a.title.replace(/ is running low$/, "") }} />
        )}
        <button
          type="button"
          className="tk-alert-soft"
          disabled={act.busy}
          onClick={later}
        >
          Later{named}
        </button>
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
      <ApprovalNotes ap={ap} />
      <span className="tk-approve-acts">
        <button
          type="button"
          className="tk-alert-soft"
          onClick={() => onDecide("rejected", from())}
        >
          Decline<span className="sr-only">: {a.title}</span>
        </button>
        <button
          type="button"
          className="tk-approve-go"
          onClick={() => onDecide("approved", from())}
        >
          <Icon name="fingerprint" />
          Approve<span className="sr-only">: {a.title}</span>
        </button>
      </span>
    </article>
  );
}

/** What else an approval says: a first payment, the confirmation it needs,
 * and where it is among several levels or approvers. */
function ApprovalNotes({
  ap,
  stepUp = ap.step_up,
}: {
  ap: NonNullable<ChatAlert["approval"]>;
  stepUp?: string | null;
}) {
  const notes = [
    ap.first_payment && (
      <span key="first" className="tk-pill warn">
        First payment to this account
      </span>
    ),
    stepUp && (
      <span key="up" className="tk-pill plain">
        <Icon name="lock" />
        {stepUp === "passkey"
          ? "Needs your passkey"
          : "Needs your passkey or a code"}
      </span>
    ),
    ap.levels > 1 && (
      <span key="level" className="tk-pill plain">
        Level {ap.level} of {ap.levels}
      </span>
    ),
    ap.required > 1 && (
      <span key="req" className="tk-pill plain">
        {ap.approvals} of {ap.required} approvals
      </span>
    ),
  ].filter(Boolean);
  return notes.length > 0 ? (
    <span className="tk-approve-notes">{notes}</span>
  ) : null;
}

/** Approve or Decline, confirmed: what it is, a comment, and the passkey
 * or code its rules ask for (for a decline too); the server refuses it
 * without them, and what it asks for is what the sheet asks next. */
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
  // The confirmation asked for: the approval's rule, or what the server
  // says it needs now.
  const [stepUp, setStepUp] = useState<string | null>(ap.step_up);
  const passkeyOnly = stepUp === "passkey";
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
      if (e instanceof ApiError && typeof e.body.step_up === "string")
        setStepUp(e.body.step_up);
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
  const usePasskey = !!stepUp && passkeysSupported() && code === "";
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
        <ApprovalNotes ap={ap} stepUp={stepUp} />
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
        {stepUp && !passkeyOnly && (
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
        {passkeyOnly && !passkeysSupported() && (
          <p className="tk-form-now">
            This needs your passkey, and this browser cannot use one. Decide it
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
        {usePasskey ? (
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
            className={approve ? "tk-cta" : "tk-cta tk-cta-danger"}
            disabled={
              act.busy || (!!stepUp && (passkeyOnly || code.length !== 6))
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

/** What an offer came to, in words, when it did not go out to a rider. */
const offerWords: Record<string, string> = {
  held: "A rider has it already",
  no_rider: "No rider could take it. You are told when one is free",
  asked_owner: "You are asked who takes it",
};

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
  const [note, setNote] = useState<string>();
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
    void act.run(async () => {
      setNote(undefined);
      const out = await post<{ status: string; rider?: string }>(
        `/v1/storefront/orders/${encodeURIComponent(a.reference ?? "")}/offer`,
        r ? { rider_id: r.id } : {},
      );
      // Only an offer out to a rider is done; the rest say why not.
      if (out.status !== "offered") {
        setNote(offerWords[out.status] ?? "It was not offered");
        return;
      }
      setSaved(`Offered to ${out.rider ?? r?.name ?? "a rider"}`);
      window.setTimeout(() => {
        setShut(true);
        onDone();
      }, 1200);
    }, false);
  return (
    <Sheet
      title={`A rider for ${a.reference}`}
      from={from}
      onClose={onClose}
      shut={shut}
      testid="rider-sheet"
    >
      {saved && <SavedTick words={saved} />}
      <p className="tk-form-now tk-alert-pad">{a.line}</p>
      {note && (
        <p className="tk-form-now tk-alert-pad" role="status">
          {note}
        </p>
      )}
      <ErrorBox error={error ?? act.error} />
      {!riders ? (
        !error && <KitLoading rows={2} label="Loading your riders…" />
      ) : riders.length === 0 ? (
        <KitEmpty icon="store" title="No riders on duty">
          Add riders on the Deliveries page.
        </KitEmpty>
      ) : (
        <ul className="tk-sheet-list tk-alert-rows">
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

interface MatchOption {
  kind: "invoice" | "order";
  number: string;
  customer: string;
  due_kobo: number;
}

/** Match a payment: every open invoice and order, the same amount first;
 * found by number, amount or name. */
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
  const [list, setList] = useState<MatchOption[]>();
  const [error, setError] = useState<unknown>();
  const [search, setSearch] = useState("");
  const act = useAction();
  const [saved, setSaved] = useState<string>();
  const [shut, setShut] = useState(false);
  useEffect(() => {
    get<{ options: MatchOption[] }>("/v1/chat-alerts/match-options").then(
      (r) =>
        setList(
          [...r.options].sort(
            (x, y) =>
              Math.abs(x.due_kobo - (a.amount_kobo ?? 0)) -
              Math.abs(y.due_kobo - (a.amount_kobo ?? 0)),
          ),
        ),
      setError,
    );
  }, []);
  const q = search
    .trim()
    .toLowerCase()
    .replace(/[₦,\s]/g, "");
  const shown = (list ?? []).filter(
    (x) =>
      !q ||
      x.number.toLowerCase().includes(q) ||
      x.customer.toLowerCase().includes(q) ||
      String(x.due_kobo / 100).includes(q),
  );
  const match = (x: MatchOption) =>
    void act.run(async () => {
      const out = await post<{ code?: string }>(
        `/v1/money/entries/${encodeURIComponent(a.id ?? "")}/match`,
        x.kind === "order" ? { order: x.number } : { invoice: x.number },
      );
      // Held for a second person (a school's cash threshold).
      setSaved(
        out?.code === "needs_approval"
          ? "Sent for a second person to approve"
          : `Matched to ${x.number}`,
      );
      window.setTimeout(() => {
        setShut(true);
        onDone();
      }, 1200);
    }, false);
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
      <p className="tk-form-now tk-alert-pad">{a.line}. What does it pay?</p>
      <label className="tk-search">
        <Icon name="search" />
        <span className="sr-only">Search by number, amount or name</span>
        <input
          type="search"
          placeholder="Number, amount or name"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </label>
      <ErrorBox error={error ?? act.error} />
      {!list ? (
        !error && <KitLoading rows={3} label="Loading what is open…" />
      ) : shown.length === 0 ? (
        <KitEmpty
          icon="invoices"
          title={q ? "Nothing open like that" : "Nothing open to match"}
        />
      ) : (
        <ul className="tk-sheet-list tk-alert-rows">
          {shown.map((x) => (
            <li key={`${x.kind}-${x.number}`}>
              <button
                type="button"
                className="tk-row"
                disabled={act.busy}
                onClick={() => match(x)}
              >
                <span className="tk-row-tile" aria-hidden="true">
                  <Icon name={x.kind === "order" ? "orders" : "invoices"} />
                </span>
                <span className="tk-row-words">
                  <span className="tk-row-name">
                    {x.kind === "order" ? "Order " : ""}
                    {x.number}
                  </span>
                  <span className="tk-row-sub">
                    {x.customer} · {formatPrice(x.due_kobo)} to pay
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
