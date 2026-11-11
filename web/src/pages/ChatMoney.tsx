// The chat's Invoices and Money tools (docs/spec/chat-five-more-flows.md,
// phase 2 additions): an invoice looked at, sent, paid or cancelled, and a
// new one made; the money log's entries, matched to an invoice, and cash
// or an expense added. Each change is told as a diary line by the server.

import { useMemo, useRef, useState } from "react";
import { get, post, postOnce } from "../api";
import { KitField, Pill } from "../kit";
import { ErrorBox, useLoad } from "../ui";
import {
  dateLabel,
  invoiceActions,
  invoiceStatus,
  invoicePath,
  naira,
  toKobo,
  type CustomerInvoice,
} from "../lib/invoices";
import {
  emptyEntry,
  ENTRIES_PATH,
  ENTRY_KINDS,
  entryProblem,
  entryRequest,
  type EntryForm,
  type EntryKind,
} from "../lib/money";
import {
  entryAmount,
  entryName,
  entryPill,
  matchable,
  owing,
  towardsWords,
  type MoneyEntry,
} from "../lib/chatmoney";
import { useAuth } from "../auth";

const newKey = () =>
  typeof crypto !== "undefined" && "randomUUID" in crypto
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random()}`;

/** One invoice: what it is for, what is paid, and what can be done. */
export function InvoiceView({
  invoice: x,
  onSaved,
  canEdit,
}: {
  invoice: CustomerInvoice;
  onSaved: (line: string | null) => void;
  canEdit: boolean;
}) {
  const { can } = useAuth();
  const acts = invoiceActions(x.status, can);
  const [mode, setMode] = useState<"" | "pay" | "void">("");
  const [amount, setAmount] = useState(
    x.due_kobo > 0 ? String(x.due_kobo / 100) : "",
  );
  const [how, setHow] = useState<"cash" | "transfer">("cash");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  // One payment per form: a retry is the same entry, not a second one.
  const payKey = useRef(newKey());
  const sendKey = useRef(newKey());
  const kobo = useMemo(() => toKobo(amount), [amount]);
  const st = invoiceStatus(x.status);
  const run = async (fn: () => Promise<unknown>, words: string) => {
    setBusy(true);
    setError(undefined);
    try {
      await fn();
      onSaved(words);
    } catch (e) {
      setError(e);
      setBusy(false);
    }
  };
  const send = () =>
    run(
      () => postOnce(invoicePath(x.id, "/send"), sendKey.current, {}),
      acts.sendAgain ? `Sent a reminder for ${x.number}` : `Sent ${x.number}`,
    );
  const pay = () =>
    run(async () => {
      const r = await postOnce<{ entry_id: string }>(
        ENTRIES_PATH,
        payKey.current,
        {
          kind: "cash",
          direction: "in",
          amount_kobo: kobo,
          description: `Payment for ${x.number}`,
          channel: how === "cash" ? "cash" : "transfer",
        },
      );
      await post(`/v1/money/entries/${encodeURIComponent(r.entry_id)}/match`, {
        invoice: x.number,
      });
    }, `Recorded ${naira(kobo ?? 0)} paid on ${x.number}`);
  const cancel = () =>
    run(
      () => post(invoicePath(x.id, "/void"), { reason: reason.trim() }),
      `Cancelled ${x.number}`,
    );
  return (
    <>
      <div className="tk-sheet-list tk-form-body">
        <div className="tk-bill-hero" data-testid="invoice-hero">
          <span className="tk-kicker">
            {owing(x) ? "To pay" : "Total"}
            {x.due_on && owing(x) ? ` · due ${dateLabel(x.due_on)}` : ""}
          </span>
          <b>{naira(owing(x) ? x.due_kobo : x.total_kobo)}</b>
          <span className="tk-row-sub">{x.customer.name}</span>
          <Pill
            tone={
              st.tone === "ok"
                ? "lime"
                : st.tone === "warn" || st.tone === "danger"
                  ? "warn"
                  : "plain"
            }
          >
            {st.label}
          </Pill>
        </div>
        {x.paid_kobo > 0 && (
          <p className="tk-form-now">
            {naira(x.paid_kobo)} of {naira(x.total_kobo)} paid.
          </p>
        )}
        <ul className="tk-lines">
          {(x.lines ?? []).map((l, i) => (
            <li key={i}>
              <span>
                {l.quantity} × {l.description}
              </span>
              <b>{naira(Math.round(l.quantity * l.unit_kobo))}</b>
            </li>
          ))}
        </ul>
        {x.status === "void" && x.void_reason && (
          <p className="tk-form-now">Cancelled: {x.void_reason}</p>
        )}
        {mode === "pay" && (
          <>
            <KitField
              label="What they paid (₦)"
              hint="It goes in the money log and counts towards this invoice."
            >
              <input
                inputMode="decimal"
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                placeholder="25000"
              />
            </KitField>
            <KitField label="How">
              <select
                value={how}
                onChange={(e) => setHow(e.target.value as "cash" | "transfer")}
              >
                <option value="cash">In cash</option>
                <option value="transfer">By transfer</option>
              </select>
            </KitField>
          </>
        )}
        {mode === "void" && (
          <KitField
            label={`Why cancel ${x.number}?`}
            hint="Kept with the invoice. The customer can no longer pay it."
          >
            <textarea
              rows={2}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="Made by mistake"
            />
          </KitField>
        )}
        <ErrorBox error={error} />
        {canEdit && mode === "" && (owing(x) || acts.void) && (
          <div className="tk-bill-more">
            {owing(x) && (
              <button
                type="button"
                className="tk-btn soft"
                onClick={() => setMode("pay")}
              >
                Record a payment
              </button>
            )}
            {acts.void && (
              <button
                type="button"
                className="tk-btn soft"
                onClick={() => setMode("void")}
              >
                Cancel invoice
              </button>
            )}
          </div>
        )}
      </div>
      {canEdit && (mode !== "" || acts.send || acts.sendAgain) && (
        <div className="tk-sheet-foot tk-foot-two">
          {mode !== "" && (
            <button
              type="button"
              className="tk-btn ghost"
              disabled={busy}
              onClick={() => (setMode(""), setError(undefined))}
            >
              Not now
            </button>
          )}
          {mode === "pay" ? (
            <button
              type="button"
              className="tk-cta"
              disabled={busy || !(kobo && kobo > 0)}
              onClick={() => void pay()}
            >
              Record {kobo && kobo > 0 ? naira(kobo) : "it"}
            </button>
          ) : mode === "void" ? (
            <button
              type="button"
              className="tk-cta"
              disabled={busy || reason.trim() === ""}
              onClick={() => void cancel()}
            >
              Cancel {x.number}
            </button>
          ) : (
            <button
              type="button"
              className="tk-cta"
              disabled={busy}
              onClick={() => void send()}
            >
              {acts.sendAgain ? "Send a reminder" : "Send it"}
            </button>
          )}
        </div>
      )}
    </>
  );
}

/** A new invoice: who, what for and how much; sent at once or kept. */
export function InvoiceAdd({
  onSaved,
  onCancel,
  to,
}: {
  onSaved: (line: string | null) => void;
  onCancel?: () => void;
  /** A linked business, from its conversation (Invoice them): its name,
   * and its key, so the invoice reaches it on Taskiem as a bill; no phone
   * is asked. */
  to?: { name: string; key: string };
}) {
  const [name, setName] = useState(to?.name ?? "");
  const [phone, setPhone] = useState("");
  const [what, setWhat] = useState("");
  const [amount, setAmount] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const key = useRef(newKey());
  const kobo = toKobo(amount);
  const ready =
    name.trim() !== "" &&
    (!!to || phone.trim() !== "") &&
    what.trim() !== "" &&
    !!kobo &&
    kobo > 0;
  const save = async (send: boolean) => {
    setBusy(true);
    setError(undefined);
    try {
      const x = await postOnce<CustomerInvoice>("/v1/invoices", key.current, {
        customer: to
          ? { name: to.name, key: to.key }
          : { name: name.trim(), phone: phone.trim() },
        description: what.trim(),
        amount_kobo: kobo,
      });
      if (send)
        await postOnce(invoicePath(x.id, "/send"), `${key.current}:send`, {});
      onSaved(send ? `Sent ${x.number}` : `Made ${x.number}`);
    } catch (e) {
      setError(e);
      setBusy(false);
    }
  };
  return (
    <>
      <div className="tk-sheet-list tk-form-body">
        {to ? (
          <p className="tk-form-now">
            To {to.name}: it reaches them on Taskiem, with their bills.
          </p>
        ) : (
          <>
            <KitField label="Customer's name">
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoComplete="off"
                placeholder="Bisi Okafor"
              />
            </KitField>
            <KitField
              label="Their phone number"
              hint="The invoice is sent here."
            >
              <input
                type="tel"
                value={phone}
                onChange={(e) => setPhone(e.target.value)}
                placeholder="0803 000 0000"
              />
            </KitField>
          </>
        )}
        <KitField label="What it is for">
          <input
            value={what}
            onChange={(e) => setWhat(e.target.value)}
            placeholder="Catering for 50"
          />
        </KitField>
        <KitField label="Amount (₦)">
          <input
            inputMode="decimal"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            placeholder="45000"
          />
        </KitField>
        <ErrorBox error={error} />
      </div>
      <div className="tk-sheet-foot tk-foot-two">
        {onCancel ? (
          <button
            type="button"
            className="tk-btn ghost"
            disabled={busy}
            onClick={onCancel}
          >
            Cancel
          </button>
        ) : null}
        <button
          type="button"
          className="tk-btn soft"
          disabled={busy || !ready}
          onClick={() => void save(false)}
        >
          Keep as draft
        </button>
        <button
          type="button"
          className="tk-cta"
          disabled={busy || !ready}
          onClick={() => void save(true)}
        >
          Send it
        </button>
      </div>
    </>
  );
}

/** One entry of the money log, and matching it to an invoice. */
export function MoneyEntryView({
  entry: e,
  onSaved,
  canEdit,
}: {
  entry: MoneyEntry;
  onSaved: (line: string | null) => void;
  canEdit: boolean;
}) {
  const [picking, setPicking] = useState(false);
  const owed = useLoad(
    () =>
      picking
        ? get<{ invoices: CustomerInvoice[] }>("/v1/invoices?limit=200").then(
            (r) => (r.invoices ?? []).filter(owing),
          )
        : Promise.resolve([] as CustomerInvoice[]),
    [picking],
  );
  const [chosen, setChosen] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const pill = entryPill(e);
  const match = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await post(`/v1/money/entries/${encodeURIComponent(e.id)}/match`, {
        invoice: chosen,
      });
      onSaved(`Matched ${entryAmount(e)} to ${chosen}`);
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  };
  const words = towardsWords(e);
  return (
    <>
      <div className="tk-sheet-list tk-form-body">
        <div className="tk-bill-hero" data-testid="money-hero">
          <span className="tk-kicker">
            {e.direction === "in" ? "Came in" : "Went out"} ·{" "}
            {dateLabel(e.when)}
          </span>
          <b>{entryAmount(e)}</b>
          <span className="tk-row-sub">{entryName(e)}</span>
          {pill && <Pill tone={pill.tone}>{pill.text}</Pill>}
        </div>
        <ul className="tk-lines">
          {e.channel && (
            <li>
              <span>Where</span>
              <b>{e.channel === "cash" ? "Cash" : e.channel}</b>
            </li>
          )}
          {e.reference && (
            <li>
              <span>Reference</span>
              <b className="mono">{e.reference}</b>
            </li>
          )}
          {e.vendor && (
            <li>
              <span>Paid to</span>
              <b>{e.vendor}</b>
            </li>
          )}
          {e.category && (
            <li>
              <span>Kind of spending</span>
              <b>{e.category}</b>
            </li>
          )}
          {e.fee_kobo !== undefined && e.fee_kobo > 0 && (
            <li>
              <span>Fee</span>
              <b>{naira(e.fee_kobo)}</b>
            </li>
          )}
          {e.left_kobo !== undefined &&
            e.amount_kobo !== undefined &&
            e.left_kobo > 0 &&
            e.left_kobo < e.amount_kobo && (
              <li>
                <span>Left to match</span>
                <b>{naira(e.left_kobo)}</b>
              </li>
            )}
        </ul>
        {words && <p className="tk-form-now">{words}</p>}
        {picking &&
          (owed.data && owed.data.length === 0 ? (
            <p className="tk-form-now">No invoices are waiting to be paid.</p>
          ) : (
            <KitField label="The invoice it pays">
              <select
                value={chosen}
                onChange={(ev) => setChosen(ev.target.value)}
                disabled={!owed.data}
              >
                <option value="">
                  {owed.data ? "Choose an invoice" : "Loading invoices…"}
                </option>
                {(owed.data ?? []).map((x) => (
                  <option key={x.id} value={x.number}>
                    {x.number} · {x.customer.name} · {naira(x.due_kobo)} to pay
                  </option>
                ))}
              </select>
            </KitField>
          ))}
        <ErrorBox error={error ?? owed.error} />
      </div>
      {canEdit && matchable(e) && (
        <div className="tk-sheet-foot tk-foot-two">
          {picking ? (
            <>
              <button
                type="button"
                className="tk-btn ghost"
                disabled={busy}
                onClick={() => (setPicking(false), setError(undefined))}
              >
                Not now
              </button>
              <button
                type="button"
                className="tk-cta"
                disabled={busy || chosen === ""}
                onClick={() => void match()}
              >
                Match it
              </button>
            </>
          ) : (
            <button
              type="button"
              className="tk-cta"
              onClick={() => setPicking(true)}
            >
              Match to an invoice
            </button>
          )}
        </div>
      )}
    </>
  );
}

/** Cash in or out, or an expense, added to the money log. */
export function MoneyAdd({
  onSaved,
  onCancel,
}: {
  onSaved: (line: string | null) => void;
  onCancel?: () => void;
}) {
  const [f, setF] = useState<EntryForm>(() => emptyEntry());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [tried, setTried] = useState(false);
  const key = useRef(newKey());
  const problem = entryProblem(f);
  const set = (k: keyof EntryForm) => (v: string) => setF({ ...f, [k]: v });
  const save = async () => {
    setTried(true);
    if (problem) return;
    setBusy(true);
    setError(undefined);
    try {
      await postOnce(ENTRIES_PATH, key.current, entryRequest(f));
      const kind = ENTRY_KINDS.find((k) => k.value === f.kind)?.label ?? "Entry";
      onSaved(`${kind} of ${naira(toKobo(f.amount) ?? 0)} added`);
    } catch (e) {
      setError(e);
      setBusy(false);
    }
  };
  return (
    <>
      <div className="tk-sheet-list tk-form-body">
        <KitField
          label="What it is"
          hint={ENTRY_KINDS.find((k) => k.value === f.kind)?.help}
        >
          <select
            value={f.kind}
            onChange={(e) => set("kind")(e.target.value as EntryKind)}
          >
            {ENTRY_KINDS.map((k) => (
              <option key={k.value} value={k.value}>
                {k.label}
              </option>
            ))}
          </select>
        </KitField>
        <KitField label="Amount (₦)">
          <input
            inputMode="decimal"
            value={f.amount}
            onChange={(e) => set("amount")(e.target.value)}
            placeholder="12000"
          />
        </KitField>
        <KitField label="What it was for">
          <input
            value={f.description}
            onChange={(e) => set("description")(e.target.value)}
            placeholder={f.kind === "expense" ? "Diesel for the generator" : "Cash sales"}
          />
        </KitField>
        {f.kind === "expense" && (
          <KitField label="Paid to (if you like)">
            <input
              value={f.vendor}
              onChange={(e) => set("vendor")(e.target.value)}
              placeholder="Total filling station"
            />
          </KitField>
        )}
        <KitField label="Date">
          <input
            type="date"
            value={f.date}
            onChange={(e) => set("date")(e.target.value)}
          />
        </KitField>
        {tried && problem && (
          <p className="tk-field-error" role="alert">
            {problem}
          </p>
        )}
        <ErrorBox error={error} />
      </div>
      <div className="tk-sheet-foot tk-foot-two">
        {onCancel ? (
          <button
            type="button"
            className="tk-btn ghost"
            disabled={busy}
            onClick={onCancel}
          >
            Cancel
          </button>
        ) : null}
        <button
          type="button"
          className="tk-cta"
          disabled={busy}
          onClick={() => void save()}
        >
          Add it
        </button>
      </div>
    </>
  );
}
