import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { ApiError, get, postOnce } from "../api";
import { useAuth } from "../auth";
import { EmptyState, ErrorBox, Field, PageHeader, Skeleton, fmtTime, useAction, useLoad, Row } from "../ui";
import { naira, newRequestKey } from "../lib/invoices";
import { plainMessage, schoolAccess } from "../lib/schools";
import {
  HOWS,
  decisionLine,
  emptyPayment,
  howWords,
  isApprovalNeeded,
  kindWords,
  paymentProblem,
  paymentRequest,
  postedLine,
  postingStatus,
  requestTitle,
  type PaymentForm,
  type Posting,
} from "../lib/schoolPayments";
import { SchoolTabs } from "./Schools";

// Schools > Payments (docs/spec/schools.md 8, B5; docs/schools.md#cash-and-
// transfers): posting money a family paid at the desk, and the postings
// made, waiting for a second person, or declined.

const FILTERS = [
  { value: "", label: "All" },
  { value: "pending", label: "Waiting" },
  { value: "posted", label: "Posted" },
  { value: "declined", label: "Declined" },
];

/** Schools > Payments: post cash or a transfer, and the list of postings and requests with their state. */
export function SchoolPaymentsPage() {
  const { me, can } = useAuth();
  const may = schoolAccess(can);
  const [params, setParams] = useSearchParams();
  const status = params.get("status") ?? "";
  const list = useLoad(() => get<{ postings: Posting[] }>(`/v1/schools/payments${status ? `?status=${status}` : ""}`), [status], true);
  if (!me?.schools) return <PageHeader title="Payments" />;
  return (
    <>
      <PageHeader
        title="Payments"
        description="Money a family paid at the desk: in cash, by transfer to the school's own account, or by POS. It counts towards the child's open term invoices, oldest first, with a receipt for each; what is left is the family's credit."
      />
      <SchoolTabs />
      {may.manage && <PostPayment student={params.get("student") ?? ""} onPosted={() => list.reload()} />}
      <section className="card" aria-labelledby="postings-heading">
        <div className="toolbar">
          <h2 id="postings-heading" style={{ margin: 0 }} className="grow">
            Postings
          </h2>
          <label>
            Show{" "}
            <select value={status} onChange={(e) => setParams(e.target.value ? { status: e.target.value } : {})}>
              {FILTERS.map((f) => (
                <option key={f.value} value={f.value}>
                  {f.label}
                </option>
              ))}
            </select>
          </label>
        </div>
        <ErrorBox error={list.error} />
        {!list.data && !list.error && <Skeleton rows={3} />}
        {list.data && list.data.postings.length === 0 && <EmptyState icon="schools">Nothing here yet. Money posted at the desk, and anything waiting for a second person, is listed here.</EmptyState>}
        {list.data && list.data.postings.length > 0 && (
          <ul className="items" aria-label="Payments and requests">
            {list.data.postings.map((p) => {
              const st = postingStatus(p.status);
              return (
                <Row
                  key={p.id}
                  testid="posting"
                  attrs={{ "data-status": p.status }}
                  icon={p.kind === "cash_posting" ? "money" : "invoices"}
                  tone={p.status === "pending" ? "warn" : p.status === "posted" ? "ok" : "muted"}
                  title={kindWords(p.kind)}
                  side={<span className="item-figure">{naira(p.amount_kobo)}</span>}
                  sub={
                    <>
                      <span className="sub">
                        {fmtTime(p.requested_at)}
                        {p.student_id && (
                          <>
                            {" · "}
                            <Link to={`/schools/students/${encodeURIComponent(p.student_id)}`}>{p.admission_number}</Link>
                            {p.name && ` ${p.name}`}
                            {p.class && ` · ${p.class}`}
                          </>
                        )}
                        {p.invoice_number && (
                          <>
                            {" · "}
                            <Link to={`/invoices/${encodeURIComponent(p.invoice_id ?? p.invoice_number)}`}>{p.invoice_number}</Link>
                          </>
                        )}
                        {p.kind === "cash_posting" && ` · ${howWords(String(p.details.how ?? "")).toLowerCase()}`}
                      </span>
                      <span className="sub">
                        {p.status === "posted" && p.kind === "cash_posting" && postedLine(p)}
                        {p.status === "posted" && p.kind !== "cash_posting" && "Applied."}
                        {p.status === "pending" && (p.yours ? "Waiting: a second person decides it." : `Waiting; asked by ${p.requested_email || p.requested_by}.`)}
                      </span>
                      {decisionLine(p) && <span className="sub">{decisionLine(p)}</span>}
                      <span className="item-tag">
                        <span className={`badge sf-status ${st.tone}`}>
                          {st.label}
                        </span>
                        {p.status === "pending" && can("school.approve") && !p.yours && <Link to="/approvals">Decide it under Approvals</Link>}
                      </span>
                    </>
                  }
                />
              );
            })}
          </ul>
        )}
      </section>
    </>
  );
}

/** The form: a student, an amount, how it was paid, the date and a note. */
function PostPayment({ student, onPosted }: { student: string; onPosted: () => void }) {
  const [f, setF] = useState<PaymentForm>(() => emptyPayment(student));
  const [key, setKey] = useState(newRequestKey);
  const [tried, setTried] = useState(false);
  const [said, setSaid] = useState<{ text: string; pending?: Posting }>();
  const act = useAction();
  const problem = paymentProblem(f);
  return (
    <section className="card" aria-labelledby="post-heading">
      <h2 id="post-heading" style={{ marginTop: 0 }}>
        Post a payment
      </h2>
      <p className="hint">
        For money the school received itself. A payment into the child's own account number needs no posting: it lands by itself. At or above the school's cash threshold a posting waits for a
        second person; until they approve it, it is not on the child's fees.
      </p>
      <form
        aria-label="Post a payment"
        onSubmit={(e) => {
          e.preventDefault();
          setTried(true);
          if (problem) return;
          void act.run(async () => {
            let out: unknown;
            try {
              out = await postOnce<unknown>("/v1/schools/payments", key, paymentRequest(f));
            } catch (err) {
              if (err instanceof ApiError && (err.status === 400 || err.status === 409 || err.status === 404)) err.message = plainMessage(err.message);
              throw err;
            }
            if (isApprovalNeeded(out)) {
              setSaid({ text: `${requestTitle(out.request)} is recorded and waits for a second person to approve it. It is not on the child's fees until then.`, pending: out.request });
            } else {
              const p = (out as { posting: Posting }).posting;
              setSaid({ text: postedLine(p) });
            }
            setF(emptyPayment());
            setKey(newRequestKey());
            setTried(false);
            onPosted();
          }, false);
        }}
      >
        <div className="row" style={{ flexWrap: "wrap", gap: 8 }}>
          <Field label="Student (admission number)">
            <input value={f.student} onChange={(e) => setF({ ...f, student: e.target.value })} placeholder="GCA/001" />
          </Field>
          <Field label="Amount (₦)">
            <input inputMode="decimal" value={f.amount} onChange={(e) => setF({ ...f, amount: e.target.value })} placeholder="25,000" />
          </Field>
          <Field label="How it was paid">
            <select value={f.how} onChange={(e) => setF({ ...f, how: e.target.value as PaymentForm["how"] })}>
              {HOWS.map((h) => (
                <option key={h.value} value={h.value}>
                  {h.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Date paid" hint="Today when left empty">
            <input type="date" value={f.date} onChange={(e) => setF({ ...f, date: e.target.value })} />
          </Field>
          <Field label="Note">
            <input value={f.note} maxLength={200} onChange={(e) => setF({ ...f, note: e.target.value })} placeholder="Paid by her uncle" />
          </Field>
        </div>
        {tried && problem && (
          <div className="error" role="alert">
            {problem}
          </div>
        )}
        <ErrorBox error={act.error} />
        <button type="submit" className="primary" disabled={act.busy}>
          Post the payment
        </button>
      </form>
      <div role="status" aria-live="polite">
        {said && (
          <div className="notice" data-testid="posted">
            {said.text}
            {said.pending && <div className="hint">Approvers are told in the app and on the chat app they linked.</div>}
          </div>
        )}
      </div>
    </section>
  );
}
