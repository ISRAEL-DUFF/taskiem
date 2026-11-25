// The Pay sheet (docs/spec/chat-pay-people.md, "Pay one person"): who,
// how much and what for; then the payment prepared (POST /v1/payments)
// and shown with the bank's name for the account large and what to know
// (a changed account, a first payment, the same payment again); then
// confirmed with a passkey for that payment alone (operation money.pay,
// target payment:<id>), or, when the server offers it (within the PIN's
// limits, docs/spec/transaction-pin.md), the person's 4-digit PIN typed
// here. Nothing is paid by the form's words: the chat's
// pay.person and pay.account open this sheet instead of sending their
// command, and the payment's own page (/payments/<id>, the link the chat
// sends) confirms with the same parts.

import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { ApiError, get, post } from "../api";
import { useAuth } from "../auth";
import { KitField, Sheet } from "../kit";
import { passkeysSupported, stepUpAssert } from "../passkeys";
import { ErrorBox } from "../ui";
import { PIN_DIGITS } from "../lib/pin";
import { naira } from "../lib/invoices";
import { BANK_SEARCH_FROM, filterBanks, type BankList } from "../lib/saved";
import {
  answerWords,
  askToSave,
  nameWarning,
  noAccountInPhoto,
  photoFill,
  photoOffered,
  type NameAnswer,
  type PayReading,
  type SaidIn,
} from "../lib/paymessage";
import { toast } from "../toast";
import { AIReadOffer, PhotoReadButton, readingPhoto, usePhotoAI } from "./PhotoRead";
import {
  accountDigits,
  balanceAfter,
  beneficiaryName,
  feeWords,
  outcomeWords,
  payAmountKobo,
  payBody,
  payButtonWords,
  payFieldProblem,
  payWarnings,
  paymentStatus,
  purposeOf,
  purposeWords,
  type PayBalance,
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
        {x.account_name && x.payee !== x.account_name && !x.payee.includes(x.account) ? `${x.payee} · ` : ""}
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
        {x.status === "prepared" && (
          <div>
            <dt>{x.route === "wallet" ? "How" : "Fee"}</dt>
            <dd data-testid="pay-fee">{feeWords(x)}</dd>
          </div>
        )}
        {x.status === "prepared" && balanceAfter(x) !== null && (
          <div>
            <dt>Balance after</dt>
            <dd data-testid="pay-balance-after">{naira(balanceAfter(x) ?? 0)}</dd>
          </div>
        )}
        {x.status !== "prepared" && x.route === "wallet" && (
          <div>
            <dt>How</dt>
            <dd>Taskiem wallet</dd>
          </div>
        )}
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
  /** What the PIN may still pay in one payment now, when it is offered. */
  pinLimit?: number;
}

/**
 * Confirming a prepared payment: POST /v1/payments/{id}/confirm, first
 * without a proof so the server can say "pay again?" (409 pay_again) or
 * which proof it wants (403 step_up), then with a passkey asked for this
 * payment alone, an authenticator code typed here, or the person's PIN
 * when the server offers it ("pin" among the methods: the passkey first
 * where this browser has one, the PIN when it is put away). Shown inside a
 * sheet or on a page, so it opens no dialog of its own.
 */
export function usePayConfirm(
  x: Payment | undefined,
  onConfirmed: (p: Payment) => void,
  /** What else goes with the confirmation: the sheet's ticks. */
  extra?: () => Record<string, unknown>,
) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [again, setAgain] = useState<string>();
  const [ask, setAsk] = useState<Ask>();
  const [code, setCode] = useState("");
  const [pin, setPin] = useState("");
  const yes = useRef(false);
  // A yes to paying again is for that payment only.
  useEffect(() => {
    yes.current = false;
  }, [x?.id]);

  const send = (proof: Record<string, unknown>) =>
    post<{ payment: Payment }>(
      `/v1/payments/${encodeURIComponent(x?.id ?? "")}/confirm`,
      { ...(extra?.() ?? {}), ...proof, again: yes.current },
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
        const pinLimit =
          typeof e.body.pin_limit_kobo === "number"
            ? e.body.pin_limit_kobo
            : undefined;
        if (methods.includes("passkey") && passkeysSupported()) {
          let passkey: unknown;
          try {
            passkey = await stepUpAssert(op, target);
          } catch (pe) {
            // Put away: the PIN, when it may pay this one.
            if (!methods.includes("pin")) throw pe;
            setAsk({ methods, message: e.message, pinLimit });
            return;
          }
          onConfirmed((await send({ passkey })).payment);
          return;
        }
        setAsk({ methods, message: e.message, pinLimit });
      }
    });

  const withCode = () =>
    run(async () => {
      onConfirmed((await send({ totp: code })).payment);
      setAsk(undefined);
      setCode("");
    });

  const withPin = () =>
    run(async () => {
      try {
        onConfirmed((await send({ pin })).payment);
        setAsk(undefined);
      } catch (e) {
        // A wrong PIN: typed again. Anything else the PIN cannot pay
        // (locked, over its limit, a changed account): said, and the pad
        // goes; paying again asks for the passkey.
        if (!(e instanceof ApiError) || e.body.code !== "pin_wrong")
          setAsk(undefined);
        throw e;
      } finally {
        setPin("");
      }
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
      {ask && ask.methods.includes("pin") && (
        <form
          className="pay-code"
          onSubmit={(e) => {
            e.preventDefault();
            void withPin();
          }}
        >
          <KitField
            label="Your PIN"
            hint={
              (ask.pinLimit !== undefined
                ? `Pays up to ${naira(ask.pinLimit)} now. `
                : "") + "Taskiem never asks for your PIN in a message."
            }
          >
            <input
              type="password"
              inputMode="numeric"
              autoComplete="off"
              maxLength={PIN_DIGITS}
              value={pin}
              onChange={(e) => setPin(e.target.value.replace(/\D/g, ""))}
              data-testid="pay-pin"
            />
          </KitField>
          <button
            type="submit"
            className="tk-btn primary"
            disabled={busy || pin.length !== PIN_DIGITS}
            data-testid="pay-pin-confirm"
          >
            Pay with my PIN
          </button>
        </form>
      )}
      {ask && !ask.methods.includes("pin") && ask.methods.includes("totp") && (
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
      {ask && !ask.methods.includes("totp") && !ask.methods.includes("pin") && (
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
function Sent({ x: first, where }: { x: Payment; where?: string }) {
  // Followed while it is on its way, so it turns to "Received by the
  // bank" here, with its receipt.
  const [x, setX] = useState(first);
  useEffect(() => setX(first), [first]);
  useEffect(() => {
    if (!["sending", "unknown"].includes(x.status)) return;
    const t = window.setTimeout(() => {
      get<{ payment: Payment }>(`/v1/payments/${encodeURIComponent(x.id)}`)
        .then((r) => setX(r.payment))
        .catch(() => undefined);
    }, 3000);
    return () => window.clearTimeout(t);
  }, [x]);
  const st = paymentStatus(x.status);
  return (
    <div className="pay-sent" role="status" data-testid="pay-sent">
      <span className={`badge sf-status ${st.tone}`}>
        {x.status === "waiting_ok" ? "Sent for the owner's OK" : x.status === "paid" ? "Received by the bank" : st.label}
      </span>
      <p>{outcomeWords(x)}</p>
      {x.status !== "paid" && x.route !== "wallet" && x.fee_quote_kobo == null && (
        <p className="pay-foot-hint">The fee shows after paying.</p>
      )}
      {x.status === "paid" && <ReceiptActions x={x} where={where} />}
      <Link to={`/payments/${encodeURIComponent(x.id)}`}>See the payment</Link>
    </div>
  );
}

/** A paid payment's receipt: its PDF to keep or send, and sent back into
 * the chat it was paid from (the person who sent the account details
 * sees it was paid). */
export function ReceiptActions({ x, where }: { x: Pick<Payment, "id">; where?: string }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [sent, setSent] = useState(false);
  const pdf = `/v1/payments/${encodeURIComponent(x.id)}/receipt/pdf`;
  const share = async () => {
    setBusy(true);
    setError(undefined);
    try {
      // The receipt's own link, for a chat outside Taskiem (WhatsApp).
      const r = await get<{ url: string }>(`/v1/payments/${encodeURIComponent(x.id)}/receipt`);
      const nav = navigator as Navigator & { share?: (d: ShareData) => Promise<void> };
      if (r.url && nav.share) await nav.share({ title: "Receipt", url: r.url });
      else if (r.url && navigator.clipboard) {
        await navigator.clipboard.writeText(r.url);
        toast("The receipt's link is copied: paste it in their chat.", "success");
      }
    } catch (e) {
      if (!(e instanceof DOMException && e.name === "AbortError")) setError(e);
    } finally {
      setBusy(false);
    }
  };
  const toChat = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await post(`/v1/payments/${encodeURIComponent(x.id)}/share`, { where: where ?? "taskiem" });
      setSent(true);
      toast("The receipt is in the chat.", "success");
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="pay-receipt" data-testid="pay-receipt">
      <span className="pay-receipt-acts">
        <a className="tk-btn soft" href={pdf} target="_blank" rel="noreferrer" data-testid="pay-receipt-pdf">
          Receipt (PDF)
        </a>
        <button type="button" className="tk-btn soft" onClick={() => void share()} disabled={busy} data-testid="pay-receipt-send">
          Send them the receipt
        </button>
        {!sent && (
          <button type="button" className="tk-btn soft" onClick={() => void toChat()} disabled={busy} data-testid="pay-receipt-chat">
            Put it in the chat
          </button>
        )}
      </span>
      <ErrorBox error={error} />
    </div>
  );
}

/** What people are paid from, at the top of the Pay sheet (Settled 2):
 * the balance, and Add money: the Taskiem wallet's own account number. */
export function PayBalanceLine() {
  const [b, setB] = useState<PayBalance | null>();
  const [open, setOpen] = useState(false);
  useEffect(() => {
    let live = true;
    get<PayBalance>("/v1/payments/balance").then(
      (r) => live && setB(r),
      () => live && setB(null),
    );
    return () => {
      live = false;
    };
  }, []);
  if (!b || !b.from) return null;
  return (
    <div className="pay-balance" data-testid="pay-balance">
      <span className="pay-balance-line">
        <span>
          {b.available_kobo != null ? (
            <>
              Balance <strong data-testid="pay-balance-amount">{naira(b.available_kobo)}</strong>
            </>
          ) : (
            "Paid from"
          )}{" "}
          <span className="pay-balance-from">· {b.from}</span>
        </span>
        {b.add_money && (
          <button type="button" className="link" aria-expanded={open} onClick={() => setOpen(!open)} data-testid="pay-add-money">
            Add money
          </button>
        )}
      </span>
      {open && b.add_money && (
        <div className="pay-add" data-testid="pay-add-details">
          <p>Send money from your bank to your Taskiem wallet:</p>
          <dl className="pay-check-facts">
            <div>
              <dt>Bank</dt>
              <dd>{b.add_money.bank_name}</dd>
            </div>
            <div>
              <dt>Account number</dt>
              <dd>
                <strong>{b.add_money.account_number}</strong>{" "}
                <button
                  type="button"
                  className="link"
                  onClick={() =>
                    void navigator.clipboard
                      ?.writeText(b.add_money?.account_number ?? "")
                      .then(() => toast("Account number copied.", "success"))
                  }
                >
                  Copy
                </button>
              </dd>
            </div>
            <div>
              <dt>Name</dt>
              <dd>{b.add_money.account_name}</dd>
            </div>
          </dl>
        </div>
      )}
    </div>
  );
}

/** The bank: picked from the banks the business's payments account pays
 * to; typed when that list cannot be read (it then matches by name). For
 * an account number with no bank, the banks it could be with come first
 * (its check digit fits their code; those the business pays most ahead),
 * the full list one tap away; and "Check the likely banks for me" asks
 * the bank's name at up to three of them at once, to pick the right one. */
export function BankPicker({
  label,
  value,
  onChange,
  account = "",
  expectName,
  saidIn,
  onList,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  /** The account number typed (or read from the message). */
  account?: string;
  /** The name the message gave, to compare with the bank's. */
  expectName?: string;
  /** Where that name was read: a message, or a photo. */
  saidIn?: SaidIn;
  /** The banks, once read (null when they cannot be: the bank typed), for
   * what saves a bank's code with the name (Save as…). */
  onList?: (l: BankList | null) => void;
}) {
  const digits = accountDigits(account);
  const number = /^\d{10}$/.test(digits) ? digits : "";
  const owner = !!useAuth().me?.roles.includes("owner");
  const [list, setList] = useState<BankList | null>();
  useEffect(() => {
    if (list !== undefined) onList?.(list);
  }, [list, onList]);
  const [search, setSearch] = useState("");
  const [all, setAll] = useState(false);
  const [answers, setAnswers] = useState<NameAnswer[]>();
  const [checking, setChecking] = useState(false);
  const [checkError, setCheckError] = useState<unknown>();
  useEffect(() => {
    let live = true;
    setAnswers(undefined);
    setCheckError(undefined);
    get<BankList>(
      `/v1/payments/banks${number ? `?account=${number}` : ""}`,
    ).then(
      (r) => live && setList(r.banks?.length ? r : null),
      () => live && setList((cur) => (cur === undefined ? null : cur)),
    );
    return () => {
      live = false;
    };
  }, [number]);
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
  const checkId = useId();
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
  const likely = number ? (list?.banks ?? []).filter((b) => b.likely) : [];
  // Owners only (staff pay saved accounts, whose bank is known).
  const canCheck = owner && !!list?.from && likely.some((b) => b.code);
  const check = async () => {
    setChecking(true);
    setCheckError(undefined);
    try {
      const r = await post<{ banks: NameAnswer[] }>(
        "/v1/payments/name-check",
        { account_number: number },
      );
      setAnswers(r.banks ?? []);
    } catch (e) {
      setCheckError(e);
    } finally {
      setChecking(false);
    }
  };
  const picked = answers?.find((a) => a.bank_name === value);
  const differs = picked?.account_name
    ? nameWarning(expectName, picked.account_name, saidIn)
    : null;
  const full = all || likely.length === 0 || (!!value && !likely.some((b) => b.name === value));
  const shown = filterBanks(list?.banks ?? [], search);
  return (
    <>
      {likely.length > 0 && (
        <div className="tk-field pay-likely" data-testid="pay-likely">
          <span className="tk-field-label">{label}</span>
          <span
            className="tk-chips tk-form-chips"
            role="group"
            aria-label="Banks this number could be with"
          >
            {likely.slice(0, 6).map((b) => (
              <button
                key={`${b.code}/${b.name}`}
                type="button"
                aria-pressed={value === b.name}
                onClick={() => onChange(b.name)}
              >
                {b.name}
              </button>
            ))}
          </span>
          <span className="tk-field-hint">
            The banks this number could be with, those you pay most first.
          </span>
          <span className="pay-likely-acts">
            {canCheck && (
              <button
                type="button"
                className="tk-btn soft"
                onClick={() => void check()}
                disabled={checking}
                aria-describedby={checkId}
                data-testid="pay-check-banks"
              >
                {checking ? "Asking the banks…" : "Check the likely banks for me"}
              </button>
            )}
            {!full || all ? (
              <button
                type="button"
                className="link"
                aria-expanded={all}
                onClick={() => setAll(!all)}
                data-testid="pay-all-banks"
              >
                {all ? "Fewer banks" : "All banks"}
              </button>
            ) : null}
          </span>
          {canCheck && (
            <span id={checkId} className="sr-only">
              Asks up to three banks for their name on this account. Nothing
              is paid.
            </span>
          )}
          {answers && (
            <ul
              className="pay-answers"
              aria-label="What the banks say"
              data-testid="pay-answers"
            >
              {answers.map((a) => (
                <li key={a.bank_code || a.bank_name}>
                  {a.account_name ? (
                    <button
                      type="button"
                      aria-pressed={value === a.bank_name}
                      onClick={() => onChange(a.bank_name)}
                      data-testid="pay-answer"
                    >
                      {answerWords(a)}
                    </button>
                  ) : (
                    <span className="pay-answer-none" data-testid="pay-answer-none">
                      {answerWords(a)}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          )}
          {answers && !picked && answers.some((a) => a.account_name) && (
            <span className="tk-field-hint">Pick the one that is right.</span>
          )}
          {differs && (
            <div className="pay-warn warn" role="note" data-testid="pay-name-differs">
              {differs}
            </div>
          )}
          <ErrorBox error={checkError} />
        </div>
      )}
      {full && (list?.banks.length ?? 0) > BANK_SEARCH_FROM && (
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
      {full && (
        <KitField label={likely.length > 0 ? "Or choose from all banks" : label}>
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
      )}
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
  account,
  expectName,
  saidIn,
}: {
  f: PayField;
  value: string;
  offered: string[];
  set: (v: string) => void;
  /** Problems are said once the person has typed or tried to go on. */
  shown: boolean;
  /** For the bank: the account number typed, for the likely banks. */
  account?: string;
  /** For the bank: the name a message gave, to compare with the bank's. */
  expectName?: string;
  /** Where that name was read: a message, or a photo. */
  saidIn?: SaidIn;
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
      return (
        <BankPicker
          label={label}
          value={value}
          onChange={set}
          account={account}
          expectName={expectName}
          saidIn={saidIn}
        />
      );
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
  lead,
  expectName: said,
  saidIn: saidWhere = "message",
  where,
  orderReference,
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
  /** Above the fields: who is paid when the form does not ask (a rider). */
  lead?: ReactNode;
  /** The name the message gave: said in amber when the bank's differs. */
  expectName?: string;
  /** Where that name was read: a message, or a photo. */
  saidIn?: SaidIn;
  /** The chat its diary line goes to: a linked business's (its link);
   * the person's Taskiem chat when none is given. */
  where?: string;
  /** An order whose refund this pays back (Pay it back): the server caps
   * it at what is owed and marks the order refunded once paid. */
  orderReference?: string;
}) {
  const [start, setStart] = useState<Record<string, string>>(initial);
  const { can } = useAuth();
  // The sheet's ticks (Settled 6), and Save as a beneficiary for an
  // account typed or read (on by default, with a name to call them by).
  const [tell, setTell] = useState(true);
  const [receipt, setReceipt] = useState(false);
  const [saveAs, setSaveAs] = useState(true);
  const [saveName, setSaveName] = useState("");
  const tickRef = useRef({ tell, receipt });
  tickRef.current = { tell, receipt };
  const [values, setValues] = useState<Record<string, string>>(initial);
  // The name read for the account, and where: a photo read here replaces
  // the message's.
  const [expectName, setExpectName] = useState(said);
  const [saidIn, setSaidIn] = useState<SaidIn>(saidWhere);
  // Filled from a photo: the fields start again from what it says.
  const [filled, setFilled] = useState(0);
  const [photoBusy, setPhotoBusy] = useState(false);
  const [offerPhoto] = useState(() => photoOffered(fields, initial));
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
  const newAccount =
    !!prepared &&
    !prepared.saved_id &&
    !prepared.rider_id &&
    !prepared.staff_id &&
    !prepared.personal &&
    /^\d{10}$/.test(accountDigits(values.number ?? "")) &&
    can("saved.manage");
  const pay = usePayConfirm(
    prepared,
    (x) => {
      setSent(x);
      setPrepared(undefined);
      onPaid?.(x);
      // Saved as a beneficiary: the bank, the number and the checked name,
      // so next time it is one tap.
      if (newAccount && saveAs && saveName.trim())
        void post("/v1/saved", {
          kind: "bank_account",
          name: saveName.trim().replace(/^@/, ""),
          title: x.account_name || x.payee,
          bank_name: (values.bank ?? "").trim() || x.bank_name,
          account_number: accountDigits(values.number ?? ""),
        }).then(
          () => toast(`Saved as @${saveName.trim().replace(/^@/, "")}.`, "success"),
          (e: unknown) =>
            toast(e instanceof Error ? `Not saved: ${e.message}` : "Not saved.", "error"),
        );
    },
    // Only a tick changed from how it was prepared goes with it.
    () => {
      const out: Record<string, unknown> = {};
      if (prepared?.may_tell && tickRef.current.tell !== (prepared.tell_payee ?? true)) out.tell_payee = tickRef.current.tell;
      if (!prepared?.may_tell && tickRef.current.receipt !== (prepared?.send_receipt ?? false)) out.send_receipt = tickRef.current.receipt;
      return out;
    },
  );

  const prepare = async () => {
    setTried(true);
    if (!ready || preparing) return;
    setPreparing(true);
    setError(undefined);
    try {
      const r = await post<{ payment: Payment }>(
        "/v1/payments",
        {
          ...payBody(values, from, where),
          // Paying back an order's refund: the server holds it to the order.
          ...(orderReference ? { order_reference: orderReference } : {}),
        },
      );
      setPrepared(r.payment);
      setTell(r.payment.tell_payee ?? true);
      setReceipt(r.payment.send_receipt ?? false);
      setSaveName((cur) => cur || beneficiaryName(r.payment.account_name));
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

  // From a photo (reading account details from photos, I2): read on this
  // device, the words read by Taskiem (from: "photo"), and the form
  // filled in as the chip under a message fills it.
  // Nothing found on this device: "Read it with AI" where an owner turned
  // it on (I3), the words it gives used as the device's are.
  const photoAI = usePhotoAI();
  // What the fields hold when the words come back (the AI's come later).
  const valuesNow = useRef(values);
  valuesNow.current = values;
  const takeWords = async (words: string): Promise<string | null> => {
    const values = valuesNow.current;
    const r = await post<PayReading>("/v1/payments/read", {
      text: words,
      from: "photo",
    });
    if (!r.found) return noAccountInPhoto;
    const fill = photoFill(fields, r);
    if (!fill) {
      toast(askToSave, "info");
      return null;
    }
    const next = { ...values, ...fill };
    // A new number: the bank it is with is chosen again.
    for (const f of fields)
      if (f.kind === "bank" && !fill[f.key]) next[f.key] = "";
    setStart(next);
    setValues(next);
    setExpectName(r.name?.trim() || undefined);
    setSaidIn("photo");
    setFilled((n) => n + 1);
    toast("Filled in from the photo. Check it before you pay.", "success");
    return null;
  };
  const fromPhoto = async (photo: File) => {
    setPhotoBusy(true);
    setError(undefined);
    photoAI.clear();
    try {
      await photoAI.read(photo, takeWords);
    } catch (e) {
      setError(e);
    } finally {
      setPhotoBusy(false);
    }
  };
  if (sent)
    return (
      <Sheet title={title} from={rect} onClose={onClose} testid="pay-sheet">
        <div className="tk-form-body">
          <PayCheck x={sent} />
          <Sent x={sent} where={where} />
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
          {nameWarning(expectName, prepared.account_name, saidIn) && (
            <div
              className="pay-warn warn"
              role="note"
              data-testid="pay-name-differs"
            >
              {nameWarning(expectName, prepared.account_name, saidIn)}
            </div>
          )}
          <fieldset className="pay-ticks">
            <legend className="sr-only">After paying</legend>
            {prepared.may_tell ? (
              <label className="tk-check">
                <input type="checkbox" checked={tell} onChange={(e) => setTell(e.target.checked)} data-testid="pay-tell" />
                <span>
                  Tell them in the chat
                  <span className="tk-field-hint">Once the bank has it: the amount, what for and the day. Never your balance or the fee.</span>
                </span>
              </label>
            ) : (
              <label className="tk-check">
                <input type="checkbox" checked={receipt} onChange={(e) => setReceipt(e.target.checked)} data-testid="pay-send-receipt" />
                <span>
                  Send them a receipt
                  <span className="tk-field-hint">For someone not on Taskiem: a receipt to send them once it is paid.</span>
                </span>
              </label>
            )}
            {newAccount && (
              <label className="tk-check">
                <input type="checkbox" checked={saveAs} onChange={(e) => setSaveAs(e.target.checked)} data-testid="pay-save-as" />
                <span>Save as a beneficiary</span>
              </label>
            )}
            {newAccount && saveAs && (
              <KitField label="Call them" hint="Such as chidi or rider-chidi: next time, pay @chidi in one tap.">
                <input
                  value={saveName}
                  maxLength={32}
                  autoCapitalize="none"
                  autoComplete="off"
                  onChange={(e) => setSaveName(e.target.value)}
                  data-testid="pay-save-name"
                />
              </KitField>
            )}
          </fieldset>
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
            You confirm with your passkey, or your PIN for small amounts.
            Nothing is paid before.
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
          <PayBalanceLine />
          {lead}
          {offerPhoto && (
            <PhotoReadButton
              label="From a photo"
              describe="Fill this in from a photo of the account details. Read on this device, not sent."
              busy={photoBusy}
              disabled={preparing}
              onPhoto={(p) => {
                if (!readingPhoto()) void fromPhoto(p);
              }}
              testid="pay-photo"
            />
          )}
          {photoAI.offer && (
            <AIReadOffer offer={photoAI.offer} onClose={photoAI.clear} />
          )}
          {fields.map((f) => (
            <PayFieldInput
              key={`${f.key}/${filled}`}
              f={f}
              value={values[f.key] ?? ""}
              offered={f.choices ?? choices[f.key] ?? []}
              set={(v) => set(f.key, v)}
              shown={tried || (values[f.key] ?? "") !== (start[f.key] ?? "")}
              account={values.number}
              expectName={expectName}
              saidIn={saidIn}
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
