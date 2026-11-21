// The Pay sheet (docs/spec/chat-pay-people.md, "Pay one person"): who,
// how much and what for; then the payment prepared (POST /v1/payments)
// and shown with the bank's name for the account large and what to know
// (a changed account, a first payment, the same payment again); then
// confirmed with a passkey for that payment alone (operation money.pay,
// target payment:<id>). Nothing is paid by the form's words: the chat's
// pay.person and pay.account open this sheet instead of sending their
// command, and the payment's own page (/payments/<id>, the link the chat
// sends) confirms with the same parts.

import { useEffect, useId, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, get, post } from "../api";
import { KitField, Sheet } from "../kit";
import { passkeysSupported, stepUpAssert } from "../passkeys";
import { ErrorBox } from "../ui";
import { naira } from "../lib/invoices";
import { BANK_SEARCH_FROM, filterBanks, type BankList } from "../lib/saved";
import {
  outcomeWords,
  payAmountKobo,
  payBody,
  payButtonWords,
  payFieldProblem,
  payWarnings,
  paymentStatus,
  purposeOf,
  purposeWords,
  type PayField,
  type Payment,
} from "../lib/payments";

/** The prepared payment as the person confirming sees it: the bank's name
 * for the account large, then who, where, how much and what for, and
 * what they must know first. */
export function PayCheck({ x }: { x: Payment }) {
  const warnings = payWarnings(x);
  return (
    <div className="pay-check" data-testid="pay-check">
      <span className="pay-check-label">
        {x.account_name ? "The bank's name for this account" : "Paying"}
      </span>
      <strong className="pay-check-name" data-testid="pay-name">
        {x.account_name || x.payee}
      </strong>
      <span className="pay-check-sub">
        {x.account_name && x.payee !== x.account_name ? `${x.payee} · ` : ""}
        {x.bank_name} · {x.account}
      </span>
      <dl className="pay-check-facts">
        <div>
          <dt>Amount</dt>
          <dd data-testid="pay-amount">{naira(x.amount_kobo)}</dd>
        </div>
        <div>
          <dt>What for</dt>
          <dd>{purposeWords(x)}</dd>
        </div>
        {x.note && (
          <div>
            <dt>On their statement</dt>
            <dd>{x.note}</dd>
          </div>
        )}
      </dl>
      {warnings.length > 0 && (
        <ul className="pay-warns" aria-label="Before you pay">
          {warnings.map((w) => (
            <li
              key={w.key}
              className={`pay-warn ${w.tone}`}
              data-testid={`pay-warn-${w.key}`}
            >
              {w.words}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

interface Ask {
  methods: string[];
  message: string;
}

/**
 * Confirming a prepared payment: POST /v1/payments/{id}/confirm, first
 * without a proof so the server can say "pay again?" (409 pay_again) or
 * which proof it wants (403 step_up), then with a passkey asked for this
 * payment alone, or an authenticator code typed here. Shown inside a
 * sheet or on a page, so it opens no dialog of its own.
 */
export function usePayConfirm(
  x: Payment | undefined,
  onConfirmed: (p: Payment) => void,
) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [again, setAgain] = useState<string>();
  const [ask, setAsk] = useState<Ask>();
  const [code, setCode] = useState("");
  const yes = useRef(false);
  // A yes to paying again is for that payment only.
  useEffect(() => {
    yes.current = false;
  }, [x?.id]);

  const send = (proof: Record<string, unknown>) =>
    post<{ payment: Payment }>(
      `/v1/payments/${encodeURIComponent(x?.id ?? "")}/confirm`,
      { ...proof, again: yes.current },
    );

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError(undefined);
    try {
      await fn();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  const confirm = (payAgain = false) =>
    run(async () => {
      if (!x) return;
      if (payAgain) yes.current = true;
      setAgain(undefined);
      try {
        onConfirmed((await send({})).payment);
        return;
      } catch (e) {
        if (!(e instanceof ApiError)) throw e;
        if (e.status === 409 && e.body.code === "pay_again")
          return setAgain(e.message);
        if (e.status !== 403 || !e.body.step_up) throw e;
        const methods = Array.isArray(e.body.methods)
          ? (e.body.methods as string[])
          : [];
        const op =
          typeof e.body.operation === "string" ? e.body.operation : "money.pay";
        const target =
          typeof e.body.target === "string" ? e.body.target : `payment:${x.id}`;
        if (methods.includes("passkey") && passkeysSupported()) {
          onConfirmed(
            (await send({ passkey: await stepUpAssert(op, target) })).payment,
          );
          return;
        }
        setAsk({ methods, message: e.message });
      }
    });

  const withCode = () =>
    run(async () => {
      onConfirmed((await send({ totp: code })).payment);
      setAsk(undefined);
      setCode("");
    });

  /** What confirming needs said on the screen: "pay again?", a code to
   * type, or that a passkey must be added first. */
  const view = (
    <>
      {again && (
        <div className="pay-warn warn" role="alert" data-testid="pay-again">
          {again}
        </div>
      )}
      {ask && ask.methods.includes("totp") && (
        <form
          className="pay-code"
          onSubmit={(e) => {
            e.preventDefault();
            void withCode();
          }}
        >
          <KitField
            label="Code from your authenticator app"
            hint="Good for this payment only."
          >
            <input
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
              data-testid="pay-code"
            />
          </KitField>
          <button
            type="submit"
            className="tk-btn primary"
            disabled={busy || code.length !== 6}
          >
            Confirm with the code
          </button>
        </form>
      )}
      {ask && !ask.methods.includes("totp") && (
        <div
          className="pay-warn warn"
          role="alert"
          data-testid="pay-needs-passkey"
        >
          {ask.methods.length === 0
            ? "Paying needs a passkey: add one under Account first."
            : "This browser cannot use your passkey here."}{" "}
          <Link to="/account">Go to Account</Link>
        </div>
      )}
      <ErrorBox error={error} />
    </>
  );
  return { busy, again: !!again, confirm, view };
}

/** The confirming foot's words: "Pay ₦45,000", "Pay again". */
export const payWords = (x: Payment, again: boolean) =>
  again ? `Pay ${naira(x.amount_kobo)} again` : `Pay ${naira(x.amount_kobo)}`;

/** Where it went, in a few words, after it was confirmed. */
function Sent({ x }: { x: Payment }) {
  const st = paymentStatus(x.status);
  return (
    <div className="pay-sent" role="status" data-testid="pay-sent">
      <span className={`badge sf-status ${st.tone}`}>
        {x.status === "waiting_ok" ? "Sent for the owner's OK" : st.label}
      </span>
      <p>{outcomeWords(x)}</p>
      <Link to={`/payments/${encodeURIComponent(x.id)}`}>See the payment</Link>
    </div>
  );
}

/** The bank: picked from the banks the business's payments account pays
 * to; typed when that list cannot be read (it then matches by name). */
function BankPicker({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
}) {
  const [list, setList] = useState<BankList | null>();
  const [search, setSearch] = useState("");
  useEffect(() => {
    let live = true;
    get<BankList>("/v1/payments/banks").then(
      (r) => live && setList(r.banks?.length ? r : null),
      () => live && setList(null),
    );
    return () => {
      live = false;
    };
  }, []);
  // A bank typed in the chat ("gtbank"): the list's first match chosen.
  const matched = useRef(false);
  useEffect(() => {
    if (!list || matched.current) return;
    matched.current = true;
    if (!value || list.banks.some((b) => b.name === value)) return;
    const found = filterBanks(list.banks, value);
    setSearch(value);
    if (found[0]) onChange(found[0].name);
  }, [list, value, onChange]);
  if (list === null)
    return (
      <KitField label={label} hint="Its name, such as GTBank or Opay.">
        <input
          value={value}
          autoComplete="off"
          onChange={(e) => onChange(e.target.value)}
          data-testid="pay-bank-typed"
        />
      </KitField>
    );
  const shown = filterBanks(list?.banks ?? [], search);
  return (
    <>
      {(list?.banks.length ?? 0) > BANK_SEARCH_FROM && (
        <KitField
          label="Find the bank"
          hint="Part of its name, such as guaranty or kuda."
        >
          <input
            type="search"
            value={search}
            autoComplete="off"
            onChange={(e) => setSearch(e.target.value)}
            data-testid="pay-bank-search"
          />
        </KitField>
      )}
      <KitField label={label}>
        <select
          value={value}
          onChange={(e) => onChange(e.target.value)}
          disabled={!list}
          data-testid="pay-bank"
        >
          <option value="">
            {!list
              ? "Loading the banks…"
              : shown.length === 0
                ? "No bank has that name"
                : "Choose the bank"}
          </option>
          {value && !shown.some((b) => b.name === value) && (
            <option value={value}>{value}</option>
          )}
          {shown.map((b) => (
            <option key={`${b.code}/${b.name}`} value={b.name}>
              {b.name}
            </option>
          ))}
        </select>
      </KitField>
    </>
  );
}

/** One field of a pay form, by its kind: saved accounts as @name chips,
 * an account number, a bank, naira, a choice as chips, or words. */
export function PayFieldInput({
  f,
  value,
  offered,
  set,
  shown,
}: {
  f: PayField;
  value: string;
  offered: string[];
  set: (v: string) => void;
  /** Problems are said once the person has typed or tried to go on. */
  shown: boolean;
}) {
  const label = f.optional ? `${f.label} (optional)` : f.label;
  const problem = shown
    ? (payFieldProblem(f.kind, value) ?? undefined)
    : undefined;
  const id = useId();
  const chips = offered.length > 0 && (
    <span className="tk-chips tk-form-chips" role="group" aria-label={f.label}>
      {offered.map((c) => (
        <button
          key={c}
          type="button"
          aria-pressed={value === c}
          onClick={() => set(c)}
        >
          {c}
        </button>
      ))}
    </span>
  );
  // Chips are not inside a <label>: a label names the first control in it,
  // which would be the first chip rather than the box.
  if (
    f.kind === "choice" ||
    (chips && f.kind !== "bank" && f.kind !== "money" && f.kind !== "account")
  )
    return (
      <div className="tk-field">
        {f.kind === "choice" ? (
          <span className="tk-field-label">{label}</span>
        ) : (
          <label className="tk-field-label" htmlFor={id}>
            {label}
          </label>
        )}
        {chips}
        {f.kind !== "choice" && (
          <input
            id={id}
            value={value}
            autoCapitalize="none"
            autoComplete="off"
            maxLength={100}
            placeholder={f.kind === "payee" ? "@name" : undefined}
            onChange={(e) => set(e.target.value)}
            data-testid={f.kind === "payee" ? "pay-field-who" : undefined}
          />
        )}
        {problem && (
          <span className="tk-field-error" role="alert">
            {problem}
          </span>
        )}
        {f.hint && <span className="tk-field-hint">{f.hint}</span>}
      </div>
    );
  switch (f.kind) {
    case "bank":
      return <BankPicker label={label} value={value} onChange={set} />;
    case "money":
      return (
        <KitField
          label={label}
          hint={f.hint ?? "Such as 45,000 or 45k."}
          error={problem}
        >
          <span className="tk-money">
            <span aria-hidden="true">₦</span>
            <input
              inputMode="decimal"
              value={value}
              autoComplete="off"
              aria-label={`${f.label} in naira`}
              onChange={(e) => set(e.target.value)}
              data-testid="pay-field-amount"
            />
          </span>
        </KitField>
      );
    case "account":
      return (
        <KitField label={label} hint={f.hint} error={problem}>
          <input
            inputMode="numeric"
            value={value}
            maxLength={13}
            autoComplete="off"
            onChange={(e) => set(e.target.value)}
            data-testid="pay-field-number"
          />
        </KitField>
      );
    case "payee":
      return (
        <KitField label={label} hint={f.hint} error={problem}>
          <input
            value={value}
            autoCapitalize="none"
            autoComplete="off"
            placeholder="@name"
            onChange={(e) => set(e.target.value)}
            data-testid="pay-field-who"
          />
        </KitField>
      );
    default:
      return (
        <KitField label={label} hint={f.hint}>
          <input
            value={value}
            autoComplete="off"
            maxLength={100}
            onChange={(e) => set(e.target.value)}
          />
        </KitField>
      );
  }
}

/** Whether a pay form's values are enough to prepare the payment. */
export function payReady(
  fields: PayField[],
  values: Record<string, string>,
): boolean {
  return fields.every((f) => {
    const v = (values[f.key] ?? "").trim();
    if (!v) return !!f.optional;
    return payFieldProblem(f.kind, v) === null;
  });
}

/**
 * The Pay sheet: the form (filled with what the chat took from the
 * message), then the prepared payment to confirm, then where it went.
 * actionId names the catalogue's action whose choices (the saved accounts
 * as @names) fill the chips.
 */
export function PaySheet({
  title,
  actionId,
  fields,
  initial,
  from,
  rect,
  onClose,
  onPaid,
}: {
  title: string;
  actionId: string;
  fields: PayField[];
  initial: Record<string, string>;
  /** Where it started, as the payment records it: chat or tools. */
  from: "chat" | "tools";
  rect?: DOMRect | null;
  onClose: () => void;
  /** Confirmed: sent, or sent for the owner's OK. */
  onPaid?: (x: Payment) => void;
}) {
  const [values, setValues] = useState<Record<string, string>>(initial);
  const [choices, setChoices] = useState<Record<string, string[]>>({});
  const [tried, setTried] = useState(false);
  const [preparing, setPreparing] = useState(false);
  const [error, setError] = useState<unknown>();
  const [prepared, setPrepared] = useState<Payment>();
  const [sent, setSent] = useState<Payment>();
  useEffect(() => {
    if (!fields.some((f) => f.kind === "payee")) return;
    let live = true;
    get<{ fields: Record<string, string[]> }>(
      `/v1/chat/actions/${encodeURIComponent(actionId)}/choices`,
    )
      .then((r) => live && setChoices(r.fields ?? {}))
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [actionId, fields]);
  const set = (k: string, v: string) =>
    setValues((cur) => ({ ...cur, [k]: v }));
  const ready = payReady(fields, values);
  const other =
    fields.some((f) => f.key === "for") &&
    purposeOf(values.for ?? "") === "other";
  const pay = usePayConfirm(prepared, (x) => {
    setSent(x);
    setPrepared(undefined);
    onPaid?.(x);
  });

  const prepare = async () => {
    setTried(true);
    if (!ready || preparing) return;
    setPreparing(true);
    setError(undefined);
    try {
      const r = await post<{ payment: Payment }>(
        "/v1/payments",
        payBody(values, from),
      );
      setPrepared(r.payment);
    } catch (e) {
      setError(e);
    } finally {
      setPreparing(false);
    }
  };
  // Back to the form: the prepared payment is cancelled, so it never waits
  // in the list to be confirmed by mistake.
  const change = () => {
    const x = prepared;
    setPrepared(undefined);
    if (x)
      void post(`/v1/payments/${encodeURIComponent(x.id)}/cancel`).catch(
        () => undefined,
      );
  };
  const close = () => {
    if (prepared)
      void post(`/v1/payments/${encodeURIComponent(prepared.id)}/cancel`).catch(
        () => undefined,
      );
    onClose();
  };
  const amount = payAmountKobo(values.amount ?? "");

  if (sent)
    return (
      <Sheet title={title} from={rect} onClose={onClose} testid="pay-sheet">
        <div className="tk-form-body">
          <PayCheck x={sent} />
          <Sent x={sent} />
        </div>
        <div className="tk-sheet-foot">
          <button type="button" className="tk-cta" onClick={onClose}>
            Done
          </button>
        </div>
      </Sheet>
    );

  if (prepared)
    return (
      <Sheet
        title="Check and pay"
        from={rect}
        onClose={close}
        onBack={change}
        testid="pay-sheet"
      >
        <div className="tk-form-body">
          <PayCheck x={prepared} />
          {pay.view}
        </div>
        <div className="tk-sheet-foot">
          <div className="tk-form-foot-two">
            <button
              type="button"
              className="tk-btn soft"
              onClick={change}
              disabled={pay.busy}
            >
              Change
            </button>
            <button
              type="button"
              className="tk-cta"
              onClick={() => void pay.confirm(pay.again)}
              disabled={pay.busy}
              data-testid="pay-confirm"
            >
              {pay.busy ? "Confirming…" : payWords(prepared, pay.again)}
            </button>
          </div>
          <p className="pay-foot-hint">
            You confirm with your passkey. Nothing is paid before.
          </p>
        </div>
      </Sheet>
    );

  return (
    <Sheet title={title} from={rect} onClose={onClose} testid="pay-sheet">
      <form
        className="tk-form"
        onSubmit={(e) => {
          e.preventDefault();
          void prepare();
        }}
      >
        <fieldset className="tk-sheet-list tk-form-body">
          {fields.map((f) => (
            <PayFieldInput
              key={f.key}
              f={f}
              value={values[f.key] ?? ""}
              offered={f.choices ?? choices[f.key] ?? []}
              set={(v) => set(f.key, v)}
              shown={tried || (values[f.key] ?? "") !== (initial[f.key] ?? "")}
            />
          ))}
          {other && (
            <KitField
              label="What for, in a few words"
              hint="Such as generator fuel."
            >
              <input
                value={values.for_note ?? ""}
                maxLength={80}
                autoComplete="off"
                onChange={(e) => set("for_note", e.target.value)}
              />
            </KitField>
          )}
          {tried && !ready && (
            <p className="tk-field-error" role="alert">
              Fill in who, how much and what it is for.
            </p>
          )}
          <ErrorBox error={error} />
        </fieldset>
        <div className="tk-sheet-foot">
          <button
            type="submit"
            className="tk-cta"
            disabled={preparing}
            data-testid="pay-prepare"
          >
            {preparing ? "Checking with the bank…" : payButtonWords(values)}
          </button>
          {amount !== undefined && !Number.isNaN(amount) && (
            <p className="pay-foot-hint">
              The bank's name is checked before you confirm.
            </p>
          )}
        </div>
      </form>
    </Sheet>
  );
}
