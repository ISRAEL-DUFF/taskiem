import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { get } from "../api";
import { useAuth } from "../auth";
import { EmptyState, ErrorBox, PageHeader, Skeleton, fmtTime, useLoad } from "../ui";
import { naira } from "../lib/invoices";
import { receiptHow, receiptPDF, receiptQuery, receiptStatus, type Receipt } from "../lib/schoolPayments";
import { SchoolTabs } from "./Schools";

// Schools > Receipts (docs/spec/schools.md 9, B5; docs/schools.md#receipts):
// every receipt issued, found by number, opened as PDF, voided ones
// marked; and the receipts of one student or one invoice on their pages.

/** Schools > Receipts: the latest receipts, a search by number, each with its PDF and state. */
export function SchoolReceiptsPage() {
  const { me } = useAuth();
  const [params, setParams] = useSearchParams();
  const number = params.get("number") ?? "";
  const [typed, setTyped] = useState(number);
  const list = useLoad(() => get<{ receipts: Receipt[] }>(`/v1/schools/receipts${number ? `?number=${encodeURIComponent(number)}` : ""}`), [number]);
  if (!me?.schools) return <PageHeader title="Receipts" />;
  return (
    <>
      <PageHeader
        title="Receipts"
        description="A receipt for every payment counted towards a term's fees, numbered per term. One whose payment was undone is voided, never deleted; the QR code on each opens a page that says whether it stands."
      />
      <SchoolTabs />
      <section className="card" aria-labelledby="find-receipt">
        <h2 id="find-receipt" style={{ marginTop: 0 }}>
          Find a receipt
        </h2>
        <form
          className="row"
          aria-label="Find a receipt"
          onSubmit={(e) => {
            e.preventDefault();
            const q = receiptQuery(typed);
            setParams(q ? { number: q } : {});
          }}
        >
          <input aria-label="Receipt number" value={typed} onChange={(e) => setTyped(e.target.value)} placeholder="RCT-2026T1-0079" />
          <button type="submit" style={{ flex: "0 0 auto" }}>
            Find
          </button>
          {number && (
            <button
              type="button"
              style={{ flex: "0 0 auto" }}
              onClick={() => {
                setTyped("");
                setParams({});
              }}
            >
              Show the latest
            </button>
          )}
        </form>
      </section>
      <section className="card" aria-labelledby="receipts-heading">
        <h2 id="receipts-heading" style={{ marginTop: 0 }}>
          {number ? `Receipt ${number}` : "Latest receipts"}
        </h2>
        <ErrorBox error={list.error} />
        {!list.data && !list.error && <Skeleton rows={3} />}
        {list.data && list.data.receipts.length === 0 && (
          <EmptyState icon="schools">{number ? `No receipt is numbered ${number}.` : "No receipts yet. One is issued the moment a payment is counted towards a term invoice."}</EmptyState>
        )}
        {list.data && list.data.receipts.length > 0 && <ReceiptTable receipts={list.data.receipts} />}
      </section>
    </>
  );
}

/** Receipts as a table: number, student, amount, date, invoice, state, the PDF. */
export function ReceiptTable({ receipts, student }: { receipts: Receipt[]; student?: boolean }) {
  return (
    <table className="cards">
      <thead>
        <tr>
          <th scope="col">Number</th>
          {!student && <th scope="col">Student</th>}
          <th scope="col">Amount</th>
          <th scope="col">Paid</th>
          <th scope="col">Invoice</th>
          <th scope="col">State</th>
          <th scope="col">
            <span className="sr-only">Actions</span>
          </th>
        </tr>
      </thead>
      <tbody>
        {receipts.map((r) => {
          const st = receiptStatus(r);
          return (
            <tr key={r.id} data-testid="receipt" data-voided={r.voided}>
              <td className="primary" data-label="Number">
                {r.number}
                <div className="hint">{receiptHow(r.how)}</div>
              </td>
              {!student && (
                <td data-label="Student">
                  <Link to={`/schools/students/${encodeURIComponent(r.student_id)}`}>{r.admission_number}</Link> {r.name}
                  <div className="hint">{r.class}</div>
                </td>
              )}
              <td className="sf-price" data-label="Amount">
                {naira(r.amount_kobo)}
                <div className="hint">{r.balance_after_kobo === 0 ? "Paid in full after it" : `${naira(r.balance_after_kobo)} left after it`}</div>
              </td>
              <td data-label="Paid">{fmtTime(r.paid_at)}</td>
              <td data-label="Invoice">
                <Link to={`/invoices/${encodeURIComponent(r.invoice_id)}`}>{r.invoice_number}</Link>
                <div className="hint">{r.term}</div>
              </td>
              <td data-label="State">
                <span className={`badge sf-status ${st.tone}`}>{st.label}</span>
                {r.voided && r.void_reason && <div className="hint">{r.void_reason}</div>}
              </td>
              <td data-label="">
                <a href={receiptPDF(r)} target="_blank" rel="noopener" aria-label={`Open PDF of receipt ${r.number}`}>
                  Open PDF
                </a>
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

/** A student's receipts, on their page. */
export function StudentReceiptsPanel({ studentID }: { studentID: string }) {
  const list = useLoad(() => get<{ receipts: Receipt[] }>(`/v1/schools/receipts?student=${encodeURIComponent(studentID)}`), [studentID]);
  return (
    <section className="card" aria-labelledby="student-receipts">
      <div className="toolbar">
        <h2 id="student-receipts" style={{ margin: 0 }} className="grow">
          Receipts
        </h2>
        <Link to={`/schools/payments?student=${encodeURIComponent(studentID)}`}>Post a payment</Link>
      </div>
      <ErrorBox error={list.error} />
      {!list.data && !list.error && <Skeleton rows={2} />}
      {list.data && list.data.receipts.length === 0 && <p className="hint">No receipts yet. One is issued for every payment counted towards the student's term invoices.</p>}
      {list.data && list.data.receipts.length > 0 && <ReceiptTable receipts={list.data.receipts} student />}
    </section>
  );
}
