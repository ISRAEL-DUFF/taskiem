// Payments (P1; docs/spec/chat-pay-people.md): the people paid from the
// business's own payments account, newest first, with Pay someone; what
// each role may pay without the owner's OK (the owner sets it); and one
// payment's page, the link the chat sends to confirm with a passkey.

import { useEffect, useState, type MouseEvent } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { get, post, put } from "../api";
import { useAuth } from "../auth";
import { EmptyState, ErrorBox, Field, Item, Items, Modal, PageHeader, Row, Skeleton, fmtTime, useAction, useLiveRefresh, useLoad } from "../ui";
import { naira } from "../lib/invoices";
import { roleWords } from "../lib/team";
import {
  PAY_FIELDS,
  STATUS_FILTERS,
  limitProblem,
  limitWords,
  outcomeWords,
  payAmountKobo,
  payeeName,
  paymentStatus,
  paymentSub,
  purposeWords,
  type PayLimit,
  type Payment,
} from "../lib/payments";
import { PayCheck, PaySheet, payWords, usePayConfirm } from "./PaySheet";
import { BatchesCard } from "./PayBatch";

function StatusPill({ status }: { status: string }) {
  const s = paymentStatus(status);
  return <span className={`badge sf-status ${s.tone}`}>{s.label}</span>;
}

type Way = "person" | "account";
const WAY_TITLE: Record<Way, string> = { person: "Pay someone", account: "Pay an account number" };

/** /payments: the list, Pay someone, and each role's limit. */
export function PaymentsPage() {
  const [params, setParams] = useSearchParams();
  const status = params.get("status") ?? "";
  const list = useLoad(() => get<{ payments: Payment[] }>(`/v1/payments${status ? `?status=${encodeURIComponent(status)}` : ""}`), [status]);
  useLiveRefresh(list.reload);
  const { me } = useAuth();
  const owner = !!me?.roles.includes("owner");
  const [paying, setPaying] = useState<{ way: Way; from: DOMRect }>();
  const payments = list.data?.payments;
  const open = (way: Way) => (e: MouseEvent<HTMLButtonElement>) => setPaying({ way, from: e.currentTarget.getBoundingClientRect() });
  const buttons = (
    <>
      <button type="button" className="primary" onClick={open("person")} data-testid="pay-someone">
        Pay someone
      </button>
      <button type="button" onClick={open("account")} data-testid="pay-account">
        Pay an account number
      </button>
      <Link className="button" to="/payments/several" data-testid="pay-several">
        Pay several people
      </Link>
    </>
  );
  return (
    <>
      <p className="crumbs">
        <Link to="/money" aria-label="Back to Money">
          Money
        </Link>
      </p>
      <PageHeader
        title="Pay people"
        description="People paid from your business's own payments account. Each one is checked with the bank and confirmed with a passkey."
        actions={buttons}
      />
      <div className="filters" role="search" aria-label="Filter payments">
        <label>
          Show{" "}
          <select value={status} onChange={(e) => setParams(e.target.value ? { status: e.target.value } : {}, { replace: true })}>
            {STATUS_FILTERS.map((f) => (
              <option key={f.label} value={f.value}>
                {f.label}
              </option>
            ))}
          </select>
        </label>
      </div>
      <ErrorBox error={list.error} />
      {payments === undefined && !list.error && <Skeleton rows={4} label="Loading the payments…" />}
      {payments && payments.length === 0 && (
        <EmptyState icon="money">
          {status ? "No payments with this status." : "Nothing paid yet. Pay someone you have saved, or any account number, and it shows here."}
        </EmptyState>
      )}
      {payments && payments.length > 0 && (
        <Items label="Payments">
          {payments.map((x) => (
            <Item
              key={x.id}
              to={`/payments/${encodeURIComponent(x.id)}`}
              icon="money"
              tone={paymentStatus(x.status).tone}
              title={payeeName(x)}
              side={naira(x.amount_kobo)}
              sub={`${paymentSub(x)} · ${fmtTime(x.created_at)}`}
              sideSub={<StatusPill status={x.status} />}
              testid="payment"
            />
          ))}
        </Items>
      )}
      <BatchesCard />
      {owner && <LimitsCard />}
      {paying && (
        <PaySheet
          title={WAY_TITLE[paying.way]}
          actionId={paying.way === "person" ? "pay.person" : "pay.account"}
          fields={PAY_FIELDS[paying.way]}
          initial={{}}
          from="tools"
          rect={paying.from}
          onClose={() => setPaying(undefined)}
          onPaid={list.reload}
        />
      )}
    </>
  );
}

interface Role {
  name: string;
  permissions: string[];
  members: number;
}

/** What each role may pay without the owner's OK: one payment and a day's
 * total. Roles that may pay are listed; a role with no limit set pays
 * nothing without the owner's OK. */
function LimitsCard() {
  const limits = useLoad(() => get<{ limits: PayLimit[] }>("/v1/pay-limits"), []);
  const roles = useLoad(() => get<{ roles: Role[] }>("/v1/roles").catch(() => ({ roles: [] as Role[] })), []);
  const [editing, setEditing] = useState<string>();
  const of = (role: string) => limits.data?.limits.find((l) => l.role === role);
  const names = [
    ...new Set([
      ...(roles.data?.roles ?? []).filter((r) => r.name !== "owner" && r.permissions.includes("money.pay")).map((r) => r.name),
      ...(limits.data?.limits ?? []).map((l) => l.role),
    ]),
  ].sort();
  return (
    <section className="card" aria-labelledby="pay-limits-h" data-testid="pay-limits">
      <h2 id="pay-limits-h">What staff may pay</h2>
      <p className="hint">Over a role's limit, a payment waits for your OK in Approvals. Owners have no limit.</p>
      <ErrorBox error={limits.error} />
      {!limits.data && !limits.error && <Skeleton rows={2} label="Loading the limits…" />}
      {limits.data && roles.data && names.length === 0 && (
        <p className="hint" data-testid="pay-limits-none">
          Only owners may pay. To let a role pay, allow it to pay people under <Link to="/members">Members</Link>, then set its limit here.
        </p>
      )}
      {limits.data && names.length > 0 && (
        <Items label="Limits by role">
          {names.map((name) => (
            <Row key={name} icon="members" title={roleWords(name)} sub={<span className="sub">{limitWords(of(name))}</span>} testid="pay-limit">
              <button type="button" onClick={() => setEditing(name)}>
                Change<span className="sr-only"> the limit for {roleWords(name)}</span>
              </button>
            </Row>
          ))}
        </Items>
      )}
      {editing && (
        <LimitForm
          role={editing}
          limit={of(editing)}
          onClose={() => setEditing(undefined)}
          onSaved={() => {
            setEditing(undefined);
            limits.reload();
          }}
        />
      )}
    </section>
  );
}

const nairaInput = (kobo: number | undefined) => (kobo === undefined ? "" : String(kobo / 100));

function LimitForm({ role, limit, onClose, onSaved }: { role: string; limit?: PayLimit; onClose: () => void; onSaved: () => void }) {
  const [one, setOne] = useState(nairaInput(limit?.one_kobo ?? 0));
  const [day, setDay] = useState(nairaInput(limit?.day_kobo ?? 0));
  const [problem, setProblem] = useState<string>();
  const act = useAction();
  const save = () => {
    const p = limitProblem(one, day);
    setProblem(p ?? undefined);
    if (p) return;
    void act.run(async () => {
      await put(`/v1/pay-limits/${encodeURIComponent(role)}`, { one_kobo: payAmountKobo(one), day_kobo: payAmountKobo(day) });
      onSaved();
    }, "Limit saved");
  };
  return (
    <Modal title={`What ${roleWords(role)} may pay`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          save();
        }}
        data-testid="pay-limit-form"
      >
        <Field label="One payment, at most" hint="In naira. ₦0: every payment waits for your OK.">
          <input inputMode="decimal" value={one} autoComplete="off" onChange={(e) => setOne(e.target.value)} />
        </Field>
        <Field label="A day's total, at most" hint="Everything this role pays in a day, added up.">
          <input inputMode="decimal" value={day} autoComplete="off" onChange={(e) => setDay(e.target.value)} />
        </Field>
        {problem && (
          <p className="error" role="alert">
            {problem}
          </p>
        )}
        <ErrorBox error={act.error} />
        <div className="row-actions">
          <button type="submit" className="primary" disabled={act.busy}>
            {act.busy ? "Saving…" : "Save the limit"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** /payments/:id: one payment. Prepared, it is checked and confirmed here
 * with a passkey (or cancelled); otherwise it says how it went. */
export function PaymentPage() {
  const { id = "" } = useParams();
  const { data, error, reload } = useLoad(() => get<{ payment: Payment }>(`/v1/payments/${encodeURIComponent(id)}`), [id]);
  useLiveRefresh(reload);
  // What confirming or cancelling answered, until the page is read again.
  const [now, setNow] = useState<Payment>();
  useEffect(() => setNow(undefined), [data]);
  const x = now && now.id === id ? now : data?.payment;
  const done = (p: Payment) => {
    setNow(p);
    reload();
  };
  const pay = usePayConfirm(x, done);
  const cancel = useAction();
  const st = x ? paymentStatus(x.status) : undefined;
  return (
    <>
      <p className="crumbs">
        <Link to="/payments" aria-label="Back to Pay people">
          Pay people
        </Link>
      </p>
      <PageHeader title={x ? `Pay ${payeeName(x)}` : "Payment"} />
      <ErrorBox error={error} />
      {!x && !error && <Skeleton rows={4} label="Loading the payment…" />}
      {x && st && (
        <>
          <section className="hero" aria-label="The payment" data-testid="payment-hero">
            <div className="hero-top">
              <span className="hero-label">{x.status === "paid" ? "Paid" : x.status === "prepared" ? "To pay" : "Payment"}</span>
              <span className="hero-tag" data-testid="payment-status">
                {st.label}
              </span>
            </div>
            <div className="hero-amount">{naira(x.amount_kobo)}</div>
            <p className="hero-sub">{outcomeWords(x)}</p>
            <dl className="hero-foot">
              <div>
                <dt>What for</dt>
                <dd className="hero-foot-words">{purposeWords(x)}</dd>
              </div>
              <div>
                <dt>{x.confirmed_at ? "Confirmed" : "Prepared"}</dt>
                <dd className="hero-foot-words">{fmtTime(x.confirmed_at ?? x.created_at)}</dd>
              </div>
              {x.fee_kobo !== null && (
                <div>
                  <dt>Fee</dt>
                  <dd className="hero-foot-words">{naira(x.fee_kobo)}</dd>
                </div>
              )}
              {x.finished_at && (
                <div>
                  <dt>Finished</dt>
                  <dd className="hero-foot-words">{fmtTime(x.finished_at)}</dd>
                </div>
              )}
            </dl>
          </section>
          <section className="card" aria-labelledby="pay-to-h">
            <h2 id="pay-to-h">Who is paid</h2>
            <PayCheck x={x} />
            {x.status === "prepared" && pay.view}
            {x.status === "prepared" && (
              <div className="toolbar">
                <button type="button" className="primary" disabled={pay.busy || cancel.busy} onClick={() => void pay.confirm(pay.again)} data-testid="payment-confirm">
                  {pay.busy ? "Confirming…" : `${payWords(x, pay.again)} with your passkey`}
                </button>
                <button
                  type="button"
                  className="danger"
                  disabled={pay.busy || cancel.busy}
                  onClick={() =>
                    void cancel.run(async () => {
                      const r = await post<{ payment: Payment }>(`/v1/payments/${encodeURIComponent(x.id)}/cancel`);
                      done(r.payment);
                    }, "Payment cancelled")
                  }
                  data-testid="payment-cancel"
                >
                  Cancel the payment
                </button>
              </div>
            )}
            <ErrorBox error={cancel.error} />
            {x.status === "sending" && x.awaits_your_ok && (
              <>
                <p className="pay-warn warn" data-testid="payment-awaits">
                  Nothing was paid yet: your OK did not reach this payment. Confirm it again.
                </p>
                {pay.view}
                <div className="toolbar">
                  <button type="button" className="primary" disabled={pay.busy} onClick={() => void pay.confirm()} data-testid="payment-confirm-again">
                    {pay.busy ? "Confirming…" : "Confirm again with your passkey"}
                  </button>
                </div>
              </>
            )}
            {x.status === "waiting_ok" && (
              <p>
                <Link to="/approvals">Open Approvals</Link>
              </p>
            )}
          </section>
        </>
      )}
    </>
  );
}
