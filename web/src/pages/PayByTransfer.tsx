import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { ApiError, get, post, serverNow } from "../api";
import { Icon } from "../icons";
import { Pill, Sheet } from "../kit";
import { billPath, canPay, type Bill } from "../lib/bills";
import { naira } from "../lib/invoices";
import {
  bareNumber,
  countdown,
  fromCustomer,
  lastWay,
  orderWays,
  pollEvery,
  refusalWords,
  rememberWay,
  shownState,
  spacedNumber,
  transferHead,
  transferPill,
  wayLine,
  waysOf,
  WAY_WORDS,
  type BillTransfer,
  type CustomerTransfer,
  type WayToPay,
} from "../lib/paybytransfer";
import { fmtTime } from "../ui";
import { AppCardView } from "./AppCards";
import type { AppCard } from "../lib/personchat";

// Paying by transfer (the payments flow, part 1;
// docs/spec/chat-payments-flow.md): Pay opens the ways to pay, transfer
// first (or the way last chosen for this seller); Pay by transfer shows an
// account number for this bill alone, how much to send, and for how long,
// and follows it until the money lands. The same card for a bill (the
// bill's page, the chat's bill sheet and its card) and for a customer's
// invoice in their chat with a business.

/** The clock, a second at a time, while `on`. */
function useNow(on: boolean): number {
  const [now, setNow] = useState(serverNow);
  useEffect(() => {
    if (!on) return;
    setNow(serverNow());
    const t = window.setInterval(() => setNow(serverNow()), 1000);
    return () => window.clearInterval(t);
  }, [on]);
  return now;
}

/** One line of the account: what it is, the value, Copy. */
function CopyLine({ label, shown, value, onCopied }: { label: string; shown: string; value: string; onCopied: (what: string) => void }) {
  return (
    <div className="xfer-line">
      <dt>{label}</dt>
      <dd>
        <span className={label === "Account number" ? "xfer-number" : undefined}>{shown}</span>
        <button
          type="button"
          className="xfer-copy"
          onClick={() =>
            void navigator.clipboard?.writeText(value).then(
              () => onCopied(label.toLowerCase()),
              () => onCopied(""),
            )
          }
        >
          <Icon name="copy" />
          Copy<span className="sr-only"> the {label.toLowerCase()}</span>
        </button>
      </dd>
    </div>
  );
}

/** The transfer card: the account, how much exactly, how long it is good
 * for, and where the payment has got to. */
export function TransferCard({
  t,
  paidAt,
  busy = false,
  onAgain,
  onLonger,
  next,
  others,
  testid = "transfer-card",
}: {
  t: BillTransfer;
  /** When it was seen paid. */
  paidAt?: string;
  busy?: boolean;
  /** Get a new number, or one for what is left. */
  onAgain?: () => void;
  /** I need longer: a number good for longer. */
  onLonger?: () => void;
  /** Paid: where the next bill to pay opens. */
  next?: string;
  /** The other ways to pay, when no number could be had. */
  others?: ReactNode;
  testid?: string;
}) {
  const live = t.state === "open";
  const now = useNow(live);
  const state = shownState(t, now);
  const left = state === "open" ? countdown(t.expires_at, now) : null;
  const pill = transferPill(state);
  const [copied, setCopied] = useState("");
  useEffect(() => {
    if (!copied) return;
    const x = window.setTimeout(() => setCopied(""), 2500);
    return () => window.clearTimeout(x);
  }, [copied]);
  return (
    <section className={`xfer xfer-${state}`} aria-label="Pay by transfer" data-testid={testid} data-state={state}>
      <div className="xfer-top">
        <span className="tk-kicker">Pay by transfer</span>
        <Pill tone={pill.tone}>{pill.word}</Pill>
      </div>
      <p className={`xfer-head${state === "paid" ? " ok" : ""}`} data-testid="transfer-head">
        {state === "paid" && <Icon name="tick" />}
        {transferHead(t, state, paidAt, fmtTime)}
      </p>
      {state === "asking" && (
        <p className="xfer-wait" role="status">
          <span className="xfer-dot" aria-hidden="true" />
          This takes a few seconds.
        </p>
      )}
      {state === "open" && (
        <>
          <dl className="xfer-account">
            <CopyLine label="Bank" shown={t.bank_name ?? ""} value={t.bank_name ?? ""} onCopied={setCopied} />
            <CopyLine label="Account number" shown={spacedNumber(t.account_number ?? "")} value={bareNumber(t.account_number ?? "")} onCopied={setCopied} />
            <CopyLine label="Account name" shown={t.account_name ?? ""} value={t.account_name ?? ""} onCopied={setCopied} />
          </dl>
          {left && (
            <p className={`xfer-time${left.warn ? " warn" : ""}`} data-testid="transfer-time">
              <Icon name="clock" />
              {left.words}
            </p>
          )}
          <p className="xfer-next">We'll mark it paid here as soon as it lands.</p>
          <p className="xfer-wait">
            <span className="xfer-dot" aria-hidden="true" />
            Waiting for your transfer
          </p>
          {onLonger && (
            <button type="button" className="link xfer-longer" disabled={busy} onClick={onLonger}>
              I need longer
            </button>
          )}
        </>
      )}
      <span className="sr-only" role="status">
        {copied ? `Copied the ${copied}` : ""}
      </span>
      {state === "paid" && next && (
        <div className="xfer-acts">
          <Link className="tk-btn primary" to={`/bills/${encodeURIComponent(next)}?pay=1`}>
            Pay the next one
          </Link>
        </div>
      )}
      {(state === "part" || state === "expired") && onAgain && (
        <div className="xfer-acts">
          <button type="button" className="tk-btn primary" disabled={busy} onClick={onAgain}>
            {state === "part" ? "Pay the rest" : "Get a new number"}
          </button>
        </div>
      )}
      {state === "failed" && (others || onAgain) && (
        <div className="xfer-acts">
          {onAgain && (
            <button type="button" className="tk-btn soft" disabled={busy} onClick={onAgain}>
              Try again
            </button>
          )}
          {others}
        </div>
      )}
    </section>
  );
}

/** The ways to pay, as buttons: the first is the one to press. */
export function WayChoices({
  ways,
  who,
  payLink,
  onPick,
  busy = false,
}: {
  ways: WayToPay[];
  /** The seller, as the lines say them. */
  who: string;
  payLink?: string;
  onPick: (w: WayToPay) => void;
  busy?: boolean;
}) {
  return (
    <ul className="xfer-ways" aria-label="Ways to pay" data-testid="pay-ways">
      {ways.map((w, i) => {
        const inner = (
          <>
            <span className="tk-row-tile" aria-hidden="true">
              <Icon name={w === "transfer" ? "money" : w === "taskiem" ? "bolt" : "card"} />
            </span>
            <span className="xfer-way-words">
              <span className="xfer-way-name">{WAY_WORDS[w]}</span>
              <span className="tk-row-sub">{wayLine(w, who)}</span>
            </span>
            <Icon name="chevron" />
          </>
        );
        return (
          <li key={w}>
            {w === "checkout" && payLink ? (
              <a className={`xfer-way${i === 0 ? " first" : ""}`} href={payLink} target="_blank" rel="noreferrer" onClick={() => onPick(w)}>
                {inner}
                <span className="sr-only"> (opens a new tab)</span>
              </a>
            ) : (
              <button type="button" className={`xfer-way${i === 0 ? " first" : ""}`} disabled={busy} onClick={() => onPick(w)}>
                {inner}
              </button>
            )}
          </li>
        );
      })}
    </ul>
  );
}

function refusal(e: unknown): BillTransfer {
  const code = e instanceof ApiError ? (e.body.code as string | undefined) : undefined;
  return { state: "failed", problem: refusalWords(code, e instanceof Error ? e.message : undefined) };
}

/** A bill with its account number, followed: asked for, read again every
 * few seconds while it is coming or open. `ref` is the bill's id or "Seller
 * INV-0042". */
export function useBillTransfer(ref: string, initial?: Bill, onSettled?: () => void) {
  const [bill, setBill] = useState<Bill | undefined>(initial);
  const [failed, setFailed] = useState<BillTransfer>();
  const [busy, setBusy] = useState(false);
  const settled = useRef(onSettled);
  settled.current = onSettled;
  useEffect(() => {
    if (initial) setBill(initial);
  }, [initial]);
  useEffect(() => {
    if (initial || !ref) return;
    let live = true;
    get<Bill>(`${billPath(ref)}/transfer`).then(
      (b) => live && setBill(b),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [ref, initial]);
  const t = failed ?? bill?.transfer ?? undefined;
  const state = t?.state;
  const id = bill?.id ?? ref;
  useEffect(() => {
    const every = failed ? null : pollEvery(state);
    if (every === null) return;
    let live = true;
    const x = window.setTimeout(() => {
      get<Bill>(`${billPath(id)}/transfer`).then(
        (b) => {
          if (!live) return;
          const was = state;
          setBill(b);
          const now = b.transfer?.state;
          if (now !== was && (now === "paid" || now === "part")) settled.current?.();
        },
        () => live && setBill((b) => (b ? { ...b } : b)),
      );
    }, every);
    return () => {
      live = false;
      window.clearTimeout(x);
    };
  }, [state, id, failed, bill]);
  const ask = async (longer = false) => {
    setBusy(true);
    setFailed(undefined);
    setBill((b) => (b ? { ...b, transfer: { state: "asking" } } : b));
    try {
      setBill(await post<Bill>(`${billPath(id)}/transfer`, longer ? { longer: true } : {}));
    } catch (e) {
      setFailed(refusal(e));
    } finally {
      setBusy(false);
    }
  };
  return { bill, t, busy, ask };
}

/** Pay on a bill: the ways (or straight to the only one), the transfer
 * card, the way chosen remembered for this seller. `onTaskiem` pays from
 * Taskiem (the caller's own, as its page does it). */
export function useBillPay(bill: Bill, onTaskiem: () => void, onSettled?: () => void) {
  // From Taskiem pays to the account the seller gave this business (canPay).
  const ways = useMemo(
    () => orderWays(waysOf(bill), lastWay(bill.supplier)).filter((w) => w !== "taskiem" || canPay(bill)),
    [bill],
  );
  const [step, setStep] = useState<"choose" | "transfer" | null>(null);
  const x = useBillTransfer(bill.id, bill, onSettled);
  const [longer, setLonger] = useState(false);
  const pick = (w: WayToPay) => {
    rememberWay(bill.supplier, w);
    if (w === "transfer") {
      setStep("transfer");
      void x.ask(false);
    } else {
      setStep(null);
      if (w === "taskiem") onTaskiem();
    }
  };
  /** Pay pressed: the only way at once, else the choice. */
  const start = () => {
    const only = ways.length === 1 ? ways[0] : undefined;
    if (only && only !== "checkout") pick(only);
    else setStep("choose");
  };
  const others = (exclude: WayToPay) => {
    const rest = ways.filter((w) => w !== exclude);
    return rest.length > 0 ? <WayChoices ways={rest} who={bill.supplier_name} payLink={bill.pay_link} onPick={pick} /> : undefined;
  };
  const card = x.t ? (
    <TransferCard
      t={x.t}
      paidAt={x.t.state === "paid" ? x.bill?.mirrored_at : undefined}
      busy={x.busy}
      onAgain={() => void x.ask(false)}
      onLonger={longer ? undefined : () => (setLonger(true), void x.ask(true))}
      next={x.t.next}
      others={others("transfer")}
    />
  ) : null;
  const content =
    step === "choose" ? (
      <WayChoices ways={ways} who={bill.supplier_name} payLink={bill.pay_link} onPick={pick} busy={x.busy} />
    ) : step === "transfer" ? (
      (card ?? <TransferCard t={{ state: "asking" }} />)
    ) : null;
  return {
    ways,
    /** Pay is a link straight to their checkout: their only way. */
    checkoutOnly: ways.length === 1 && ways[0] === "checkout" ? bill.pay_link : undefined,
    start,
    step,
    close: () => setStep(null),
    content,
    /** The number on its own, outside the sheet, once one was asked for. */
    card: step ? null : card,
    transfer: x.t,
  };
}

/** A bill's card in a business chat, once a number was given: the number
 * as it is now, followed. */
export function BillTransferLive({ seller, reference }: { seller: string; reference: string }) {
  const x = useBillTransfer(`${seller} ${reference}`);
  if (!x.t || !x.bill) return null;
  const b = x.bill;
  return (
    <TransferCard
      t={x.t}
      paidAt={x.t.state === "paid" ? b.mirrored_at : undefined}
      busy={x.busy}
      onAgain={() => void x.ask(false)}
      next={x.t.next}
      testid="chat-transfer-card"
      others={
        b.pay_link ? (
          <a className="tk-btn soft" href={b.pay_link} target="_blank" rel="noreferrer">
            Pay on their checkout<span className="sr-only"> (opens a new tab)</span>
          </a>
        ) : (
          <Link className="tk-btn soft" to={`/bills/${encodeURIComponent(b.id)}`}>
            Other ways to pay
          </Link>
        )
      }
    />
  );
}

const customerPath = (chat: string, invoice: string) =>
  `/v1/me/chats/${encodeURIComponent(chat)}/invoices/${encodeURIComponent(invoice)}/transfer`;

/** A customer's invoice card in their chat with a business: Pay (by
 * transfer first, or on the invoice's page), and the number once given. */
export function CustomerInvoiceCard({ chat, business, card, mine }: { chat: string; business: string; card: AppCard; mine: boolean }) {
  const invoice = card.invoice_id ?? "";
  const [c, setC] = useState<CustomerTransfer>();
  const [failed, setFailed] = useState<BillTransfer>();
  const [busy, setBusy] = useState(false);
  const [choosing, setChoosing] = useState(false);
  const [longer, setLonger] = useState(false);
  const [paidAt, setPaidAt] = useState<string>();
  useEffect(() => {
    if (!invoice) return;
    let live = true;
    get<CustomerTransfer>(customerPath(chat, invoice)).then(
      (x) => live && setC(x),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [chat, invoice]);
  const state = c?.state;
  useEffect(() => {
    if (state !== "open" || failed) return;
    let live = true;
    const x = window.setTimeout(() => {
      get<CustomerTransfer>(customerPath(chat, invoice)).then(
        (n) => {
          if (!live) return;
          if (n.state === "paid") setPaidAt(new Date(serverNow()).toISOString());
          setC(n);
        },
        () => live && setC((o) => (o ? { ...o } : o)),
      );
    }, 10_000);
    return () => {
      live = false;
      window.clearTimeout(x);
    };
  }, [state, chat, invoice, failed, c]);
  const ways: WayToPay[] = orderWays(card.url ? ["transfer", "checkout"] : ["transfer"], lastWay(business));
  const ask = async (more = false) => {
    rememberWay(business, "transfer");
    setChoosing(false);
    setBusy(true);
    setFailed({ state: "asking" });
    try {
      const n = await post<CustomerTransfer>(customerPath(chat, invoice), more ? { longer: true } : {});
      setC(n);
      setFailed(n.state === "none" ? { state: "failed", problem: "We couldn't get an account number just now. Try again, or pay on the invoice's page." } : undefined);
    } catch (e) {
      setFailed({ state: "failed", problem: e instanceof Error ? e.message : undefined });
    } finally {
      setBusy(false);
    }
  };
  const t = failed ?? (c ? fromCustomer(c) : null);
  const due = c ? c.due_kobo > 0 && c.state !== "paid" : card.status !== "paid";
  const checkout = card.url ? (
    <a className="tk-btn soft" href={card.url} target="_blank" rel="noopener noreferrer" onClick={() => rememberWay(business, "checkout")}>
      Pay on their page<span className="sr-only"> (opens a new tab)</span>
    </a>
  ) : undefined;
  // A number open or coming: the card is the way to pay.
  const showPay = invoice && due && !(t && (t.state === "open" || t.state === "asking"));
  return (
    <>
      <AppCardView
        card={card}
        mine={mine}
        actions={
          showPay ? (
            <button
              type="button"
              className="tk-btn primary"
              disabled={busy}
              onClick={() => (ways.length === 1 ? void ask() : setChoosing(true))}
            >
              Pay{c && c.due_kobo > 0 ? ` ${naira(c.due_kobo)}` : ""}
              <span className="sr-only"> {card.title}</span>
            </button>
          ) : !invoice && card.url ? (
            <a className="tk-btn primary" href={card.url} target="_blank" rel="noopener noreferrer">
              Pay<span className="sr-only"> {card.title}</span>
            </a>
          ) : undefined
        }
      />
      {t && (
        <TransferCard
          t={t}
          paidAt={paidAt}
          busy={busy}
          onAgain={() => void ask()}
          onLonger={longer ? undefined : () => (setLonger(true), void ask(true))}
          others={checkout}
          testid="customer-transfer-card"
        />
      )}
      {choosing && (
        <Sheet title={`Pay ${card.title}`} onClose={() => setChoosing(false)} testid="pay-sheet">
          <WayChoices
            ways={ways}
            who="the business"
            payLink={card.url}
            onPick={(w) => (w === "transfer" ? void ask() : (rememberWay(business, w), setChoosing(false)))}
          />
        </Sheet>
      )}
    </>
  );
}
