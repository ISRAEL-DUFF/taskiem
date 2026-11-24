import { Link, useParams, useSearchParams } from "react-router-dom";
import { get, post, put } from "../api";
import { useAuth } from "../auth";
import { billHero, billPath, billsDue, billStatus, canPay, sentLine, type Bill, type BillPaySettings } from "../lib/bills";
import { dateLabel, naira, toKobo } from "../lib/invoices";
import { useEffect, useRef, useState } from "react";
import { DocThread } from "./Conversation";
import { useBillPay } from "./PayByTransfer";
import { useBillMarks } from "./TreatLine";
import { EmptyState, ErrorBox, Field, fmtTime, Item, Items, Modal, PageHeader, Skeleton, useAction, useLoad } from "../ui";

// Bills (business links L1; docs/spec/business-links.md §6): linked
// suppliers' invoices, in the buyer's business. The supplier's figures
// are its invoice's, kept up to date by Taskiem.

function StatusPill({ status }: { status: Bill["status"] }) {
  const s = billStatus(status);
  return <span className={`badge sf-status ${s.tone}`}>{s.label}</span>;
}

/** The bills from linked suppliers: those still to pay, by due date. */
export function BillsPage() {
  const open = useLoad(() => get<{ bills: Bill[] }>("/v1/bills"), []);
  const done = useLoad(() => get<{ bills: Bill[] }>("/v1/bills?status=paid"), []);
  const list = open.data?.bills;
  return (
    <>
      <PageHeader title="Bills" description="Invoices from linked suppliers on Taskiem. What they say is the supplier's own, kept up to date." />
      <ErrorBox error={open.error ?? done.error} />
      {!list ? (
        <Skeleton rows={3} label="Loading the bills…" />
      ) : (
        <>
          <section className="hero" aria-label="Left to pay" data-testid="bills-hero">
            <div className="hero-top">
              <span className="hero-label">Left to pay</span>
              <span className="hero-tag">
                {list.length} {list.length === 1 ? "bill" : "bills"}
              </span>
            </div>
            <div className="hero-amount">{naira(billsDue(list))}</div>
            <p className="hero-sub">To linked suppliers on Taskiem</p>
          </section>
          {list.length === 0 ? (
            <EmptyState icon="invoices" action={<Link className="button" to="/chat">Linked businesses</Link>}>
              No bills to pay. An invoice a linked supplier sends you arrives here.
            </EmptyState>
          ) : (
            <Items label="Bills to pay">
              {list.map((b) => (
                <BillRow key={b.id} b={b} />
              ))}
            </Items>
          )}
          {(done.data?.bills.length ?? 0) > 0 && (
            <section aria-label="Paid">
              <h2>Paid</h2>
              <Items label="Bills paid">
                {done.data?.bills.map((b) => <BillRow key={b.id} b={b} />)}
              </Items>
            </section>
          )}
        </>
      )}
    </>
  );
}

function BillRow({ b }: { b: Bill }) {
  const due = b.due_on ? `due ${dateLabel(b.due_on)}` : "no due date";
  return (
    <Item
      to={`/bills/${encodeURIComponent(b.id)}`}
      icon="invoices"
      tone={billStatus(b.status).tone}
      title={b.supplier_name}
      side={naira(b.status === "paid" ? b.amount_kobo : b.due_kobo)}
      sub={`${b.reference} · ${due}`}
      sideSub={<StatusPill status={b.status} />}
      testid="bill"
    />
  );
}

/** One bill: what is left to pay first, then its lines. */
export function BillPage() {
  const { id = "" } = useParams();
  const { data: b, error, reload } = useLoad(() => get<Bill>(billPath(id)), [id]);
  const { can } = useAuth();
  // Each line's label, and where it went, from our order (the buyer's side).
  const mark = useBillMarks(b?.lines);
  const h = b ? billHero(b) : undefined;
  const pct = h ? Math.round(h.paid * 100) : 0;
  return (
    <>
      <p className="crumbs">
        <Link to="/bills" aria-label="Back to Bills">
          Bills
        </Link>
      </p>
      <PageHeader title={b ? `${b.reference} from ${b.supplier_name}` : "Bill"} />
      <ErrorBox error={error} />
      {!b || !h ? (
        !error && <Skeleton rows={3} label="Loading the bill…" />
      ) : (
        <>
          <section className="hero" aria-label="What is left to pay" data-testid="bill-hero">
            <div className="hero-top">
              <span className="hero-label">{h.label}</span>
              <span className="hero-tag">{billStatus(b.status).label}</span>
            </div>
            <div className="hero-amount">{naira(h.kobo)}</div>
            <p className="hero-sub">{h.sub}</p>
            {b.status !== "void" && (
              <div className="hero-bar" role="img" aria-label={`${pct}% paid`}>
                <span style={{ width: `${pct}%` }} />
              </div>
            )}
            <dl className="hero-foot">
              <div>
                <dt>Supplier</dt>
                <dd>{b.supplier_name}</dd>
              </div>
              <div>
                <dt>Due</dt>
                <dd>{dateLabel(b.due_on) || "No date set"}</dd>
              </div>
            </dl>
          </section>
          {b.paying_kobo > 0 && (
            <div className="notice" role="status" data-testid="bill-paying">
              A payment of {naira(b.paying_kobo)} from Taskiem is under way: it waits for approval, then for the bank.
            </div>
          )}
          {sentLine(b) && <p className="hint">{sentLine(b)}</p>}
          {can("invoice.manage") && <BillPay bill={b} onDone={reload} />}
          {can("invoice.manage") && canPay(b) && <PayFromTaskiem bill={b} onDone={reload} />}
          {b.disputed && (
            <div className="warning" role="note" data-testid="bill-queried">
              You queried this bill. It is not paid from Taskiem until {b.supplier_name} settles the query.
            </div>
          )}
          {b.due_kobo > 0 && (b.pay_account_number || (b.pay_link && !can("invoice.manage"))) && (
            <section className="card" aria-labelledby="bill-pay" data-testid="bill-pay">
              <h2 id="bill-pay">How to pay</h2>
              {b.pay_account_number && (
                <p>
                  Transfer to <strong className="mono">{b.pay_account_number}</strong>
                  {b.pay_bank_name ? ` (${b.pay_bank_name})` : ""}, the account {b.supplier_name} gave your business, with <strong>{b.reference}</strong> in the
                  transfer's note: it is counted towards this invoice by itself. Without it, it counts towards your oldest invoice from {b.supplier_name} first.
                </p>
              )}
              {b.pay_link && !can("invoice.manage") && (
                <p>
                  <a className="button" href={b.pay_link} target="_blank" rel="noreferrer">
                    Pay on the invoice's page
                  </a>
                </p>
              )}
            </section>
          )}
          <section className="card" aria-labelledby="bill-lines">
            <h2 id="bill-lines">What it is for</h2>
            <table>
              <thead>
                <tr>
                  <th scope="col">Description</th>
                  <th scope="col">Quantity</th>
                  <th scope="col">Price each</th>
                  <th scope="col">Amount</th>
                </tr>
              </thead>
              <tbody>
                {b.lines.map((l, i) => (
                  <tr key={i}>
                    <td>
                      {l.description}
                      {mark?.(l)}
                    </td>
                    <td>{l.quantity}</td>
                    <td className="sf-price">{naira(l.unit_kobo)}</td>
                    <td className="sf-price">{naira(l.quantity * l.unit_kobo)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <p className="lines-total">
              <span>Total</span>
              <strong className="sf-price">{naira(b.amount_kobo)}</strong>
            </p>
            <p className="hint">Brought up to date {fmtTime(b.mirrored_at)}. The supplier's invoice is the record: a change to it shows here.</p>
          </section>
          {can("invoice.manage") && b.status !== "void" && b.status !== "paid" && <BillActions bill={b} onDone={reload} />}
          <DocThread doc="bill" reference={b.reference} />
        </>
      )}
    </>
  );
}

/** Pay on a bill (the payments flow, part 1): the ways to pay in a sheet,
 * transfer first, or straight to the only one; the account number on the
 * page once one was given. */
function BillPay({ bill, onDone }: { bill: Bill; onDone: () => void }) {
  const act = useAction();
  const pay = useBillPay(
    bill,
    () => void act.run(async () => (await post(`${billPath(bill.id)}/pay`), onDone()), "Sent for approval"),
    onDone,
  );
  // "Pay the next one" opens a bill with ?pay=1: its Pay is pressed.
  const [params, setParams] = useSearchParams();
  const started = useRef(false);
  const { start, ways, checkoutOnly } = pay;
  useEffect(() => {
    if (started.current || params.get("pay") !== "1") return;
    started.current = true;
    setParams({}, { replace: true });
    if (bill.due_kobo > 0 && ways.length > 0 && !checkoutOnly) start();
  }, [params, setParams, bill.due_kobo, ways, checkoutOnly, start]);
  const live = pay.transfer && (pay.transfer.state === "open" || pay.transfer.state === "asking");
  const due = bill.due_kobo > 0 && ways.length > 0;
  return (
    <>
      <ErrorBox error={act.error} />
      {due && !live && (
        <p className="row-actions" data-testid="bill-pay-button">
          {checkoutOnly ? (
            <a className="button primary" href={checkoutOnly} target="_blank" rel="noreferrer">
              Pay {naira(bill.due_kobo)}
              <span className="sr-only"> on {bill.supplier_name}'s checkout (opens a new tab)</span>
            </a>
          ) : (
            <button type="button" className="primary" disabled={act.busy} onClick={start}>
              Pay {naira(bill.due_kobo)}
            </button>
          )}
        </p>
      )}
      {pay.card}
      {pay.step && (
        <Modal title={pay.step === "transfer" ? "Pay by transfer" : `Pay ${naira(bill.due_kobo)}`} onClose={pay.close}>
          {pay.content}
          <p className="row-actions">
            <button type="button" onClick={pay.close}>
              {pay.step === "transfer" ? "Done" : "Not now"}
            </button>
          </p>
        </Modal>
      )}
    </>
  );
}

/** Paying a bill from the business's own Taskiem wallet, after its
 * approval: set up once, then Pay. */
function PayFromTaskiem({ bill, onDone }: { bill: Bill; onDone: () => void }) {
  const settings = useLoad(() => get<BillPaySettings>("/v1/bills/pay"), []);
  const act = useAction();
  if (!settings.data) return <ErrorBox error={settings.error} />;
  const ready = !!settings.data.workflow_id;
  // Once set up, Pay from Taskiem is one of the ways to pay (BillPay).
  if (ready && bill.ways_to_pay) return null;
  return (
    <section className="card" aria-labelledby="bill-pay-taskiem" data-testid="bill-pay-taskiem">
      <h2 id="bill-pay-taskiem">Pay from Taskiem</h2>
      <ErrorBox error={act.error} />
      {ready ? (
        <>
          <p className="hint">
            It is paid from your Taskiem wallet once someone with the {settings.data.approver_role} role approves it, and {bill.supplier_name}'s Taskiem counts it towards {bill.reference} by itself.
          </p>
          <button
            type="button"
            className="primary"
            disabled={act.busy}
            onClick={() => void act.run(async () => (await post(`${billPath(bill.id)}/pay`), onDone()), "Sent for approval")}
          >
            Pay {naira(bill.due_kobo)}
          </button>
        </>
      ) : (
        <>
          <p className="hint">Pay bills from linked suppliers from your Taskiem wallet, each after one approval.</p>
          <button type="button" disabled={act.busy} onClick={() => void act.run(async () => (await put("/v1/bills/pay/setup", {}), settings.reload()), "Paying bills is set up")}>
            Set up paying bills
          </button>
        </>
      )}
    </section>
  );
}

/** What the business says about a bill: paid outside Taskiem (its own
 * record, the supplier told), or a query (the supplier told why). */
function BillActions({ bill, onDone }: { bill: Bill; onDone: () => void }) {
  const [open, setOpen] = useState<"declare" | "query">();
  const [amount, setAmount] = useState("");
  const [how, setHow] = useState("transfer");
  const [reason, setReason] = useState("");
  const act = useAction();
  const close = () => (setOpen(undefined), act.clear());
  return (
    <section className="card" aria-labelledby="bill-more">
      <h2 id="bill-more">Something else</h2>
      {bill.declared_paid_kobo > 0 && <p className="hint">You said you paid {naira(bill.declared_paid_kobo)} outside Taskiem.</p>}
      <div className="row-actions">
        <button type="button" onClick={() => setOpen("declare")}>
          Paid outside Taskiem
        </button>
        {!bill.disputed && (
          <button type="button" onClick={() => setOpen("query")}>
            Query this bill
          </button>
        )}
      </div>
      {open === "declare" && (
        <Modal title="Paid outside Taskiem" onClose={close}>
          <p className="hint">Your own record, and {bill.supplier_name} is told. It counts as paid when their Taskiem matches the money.</p>
          <Field label="Amount (₦)">
            <input inputMode="decimal" value={amount} placeholder="25,000" onChange={(e) => setAmount(e.target.value)} />
          </Field>
          <Field label="How">
            <select value={how} onChange={(e) => setHow(e.target.value)}>
              <option value="transfer">By transfer</option>
              <option value="cash">In cash</option>
              <option value="other">Another way</option>
            </select>
          </Field>
          <ErrorBox error={act.error} />
          <button
            type="button"
            className="primary"
            disabled={act.busy}
            onClick={() =>
              void act
                .run(async () => (await post(`${billPath(bill.id)}/declare`, { amount_kobo: toKobo(amount) ?? 0, how }), onDone()), "Recorded, and the supplier is told")
                .then((ok) => ok && close())
            }
          >
            Record it
          </button>
        </Modal>
      )}
      {open === "query" && (
        <Modal title="Query this bill" onClose={close}>
          <p className="hint">{bill.supplier_name} is told why. Until they settle it, the bill is not paid from Taskiem.</p>
          <Field label="What is wrong">
            <textarea rows={3} maxLength={500} value={reason} onChange={(e) => setReason(e.target.value)} />
          </Field>
          <ErrorBox error={act.error} />
          <button
            type="button"
            className="primary"
            disabled={act.busy || !reason.trim()}
            onClick={() => void act.run(async () => (await post(`${billPath(bill.id)}/query`, { reason: reason.trim() }), onDone()), "Query sent").then((ok) => ok && close())}
          >
            Send the query
          </button>
        </Modal>
      )}
    </section>
  );
}
