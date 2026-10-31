import { Link, useParams } from "react-router-dom";
import { get } from "../api";
import { billHero, billPath, billsDue, billStatus, type Bill } from "../lib/bills";
import { dateLabel, naira } from "../lib/invoices";
import { EmptyState, ErrorBox, fmtTime, Item, Items, PageHeader, Skeleton, useLoad } from "../ui";

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
  const { data: b, error } = useLoad(() => get<Bill>(billPath(id)), [id]);
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
          {b.due_kobo > 0 && (b.pay_account_number || b.pay_link) && (
            <section className="card" aria-labelledby="bill-pay" data-testid="bill-pay">
              <h2 id="bill-pay">How to pay</h2>
              {b.pay_account_number && (
                <p>
                  Transfer to <strong className="mono">{b.pay_account_number}</strong>
                  {b.pay_bank_name ? ` (${b.pay_bank_name})` : ""}, the account {b.supplier_name} gave your business. What you pay into it is counted towards your
                  invoices from {b.supplier_name}, oldest first.
                </p>
              )}
              {b.pay_link && (
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
                    <td>{l.description}</td>
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
        </>
      )}
    </>
  );
}
