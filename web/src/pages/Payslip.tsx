// Payslips as Taskiem's own cards (P4b-4, docs/spec/chat-pay-people.md):
// the full breakdown, the employer's name and RC number, and the
// payment's state, read from Payrolla when opened and kept nowhere.
// Anyone's from the run for those with payroll.read (/payroll/payslips/:id);
// a linked employee's own on My pay (/payroll/me/payslips/:id), and as a
// card in their Taskiem chat. Save as PDF makes one with the business's
// name when asked; Send a link gives Payrolla's 7-day link, for someone
// not on Taskiem.

import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { ApiError, get, getBlob, post } from "../api";
import { EmptyState, ErrorBox, Items, PageHeader, Row, Skeleton, useAction, useLoad } from "../ui";
import { dateLabel, naira } from "../lib/invoices";
import { lineState, newKey, type PayrollTone } from "../lib/payroll";
import { breakdown, type PayrollItem } from "../lib/payrollrun";
import { payslipFile, payslipPath, payslipReady, type MyPayslipRow, type Payslip } from "../lib/payslip";
import { saveFile } from "../lib/saveFile";

function Pill({ label, tone }: { label: string; tone: PayrollTone }) {
  return <span className={`badge sf-status ${tone}`}>{label}</span>;
}

function Lines({ items, minus }: { items: PayrollItem[]; minus: boolean }) {
  return (
    <>
      {items.map((i, n) => (
        <div className="payroll-item" key={`${i.code}-${n}`}>
          <dt>{i.label}</dt>
          <dd>
            {minus ? "−" : ""}
            {naira(i.amount_kobo)}
          </dd>
        </div>
      ))}
    </>
  );
}

/** A payslip's breakdown: what was earned, what was taken, the net, and
 * what the business pays on top. */
export function PayslipBreakdown({ slip }: { slip: Payslip }) {
  const b = breakdown(slip.items);
  return (
    <div className="payroll-breakdown" data-testid="payslip-breakdown">
      <dl>
        <Lines items={b.earnings} minus={false} />
        <div className="payroll-item total">
          <dt>Gross pay</dt>
          <dd>{naira(slip.total_earnings_kobo || slip.gross_kobo)}</dd>
        </div>
        <Lines items={b.deductions} minus={true} />
        <div className="payroll-item total">
          <dt>Net pay</dt>
          <dd>{naira(slip.net_kobo)}</dd>
        </div>
      </dl>
      {b.employer.length > 0 && (
        <>
          <p className="payroll-item note">The business also pays:</p>
          <dl>
            <Lines items={b.employer} minus={false} />
          </dl>
        </>
      )}
    </div>
  );
}

/** One payslip's page: its hero, the breakdown, Save as PDF, and (for the
 * payer side) Send a link. */
function PayslipView({ id, own }: { id: string; own: boolean }) {
  const view = useLoad(() => get<{ payslip: Payslip }>(payslipPath(id, own)), [id, own]);
  const act = useAction();
  const [link, setLink] = useState<{ url: string; expires_at: string }>();
  const slip = view.data?.payslip;
  const st = slip ? lineState(slip.payment.state) : null;
  const notFinal = view.error instanceof ApiError && view.error.body.code === "payslip_not_final";
  return (
    <>
      <p className="crumbs">
        {own ? (
          <Link to="/payroll/me" aria-label="Back to My pay">
            My pay
          </Link>
        ) : slip ? (
          <Link to={`/payroll/runs/${encodeURIComponent(slip.run_id)}`}>
            Payroll for {slip.period}
          </Link>
        ) : (
          <Link to="/payroll" aria-label="Back to Payroll">
            Payroll
          </Link>
        )}
      </p>
      <PageHeader title={slip ? `Payslip for ${slip.period}` : "Payslip"} />
      {notFinal ? (
        <div className="notice" role="note" data-testid="payslip-not-final">
          This payslip is ready once the payroll is locked.
        </div>
      ) : (
        <ErrorBox error={view.error} />
      )}
      {!slip && !view.error && <Skeleton rows={4} label="Loading the payslip…" />}
      {slip && st && (
        <>
          <section className="hero" aria-label="Net pay" data-testid="payslip-hero">
            <div className="hero-top">
              <span className="hero-label">Net pay</span>
              <span className="hero-tag">{slip.payment.words}</span>
            </div>
            <div className="hero-amount">{naira(slip.net_kobo)}</div>
            <p className="hero-sub">
              {slip.employee.name}
              {slip.employee.department ? ` · ${slip.employee.department}` : ""}
            </p>
            <dl className="hero-foot">
              <div>
                <dt>Pay day</dt>
                <dd className="hero-foot-words">{dateLabel(slip.pay_date) || "Not set"}</dd>
              </div>
              <div>
                <dt>Employer</dt>
                <dd className="hero-foot-words" data-testid="payslip-employer">
                  {slip.employer.name}
                  {slip.employer.rc_number ? ` · ${slip.employer.rc_number}` : ""}
                </dd>
              </div>
            </dl>
          </section>
          <section className="card" aria-labelledby="payslip-h">
            <h2 id="payslip-h">Breakdown</h2>
            <PayslipBreakdown slip={slip} />
            <p>
              <Pill label={slip.payment.words} tone={st.tone} />
            </p>
          </section>
          <ErrorBox error={act.error} />
          {link && (
            <div className="notice" role="status" data-testid="payslip-link">
              <p style={{ marginTop: 0 }}>
                Send this to {slip.employee.name}. It works for 7 days, after they type the last four digits of their phone:
              </p>
              <input readOnly value={link.url} aria-label="The payslip's link" onFocus={(e) => e.currentTarget.select()} />
            </div>
          )}
          <div className="toolbar">
            <button
              type="button"
              className="primary"
              disabled={act.busy}
              data-testid="payslip-pdf"
              onClick={() =>
                void act.run(async () => {
                  saveFile(await getBlob(payslipPath(id, own, "/pdf")), payslipFile(slip.period));
                }, false)
              }
            >
              Save as PDF
            </button>
            {!own && (
              <button
                type="button"
                disabled={act.busy}
                data-testid="payslip-send-link"
                onClick={() =>
                  void act.run(async () => {
                    setLink(await post<{ url: string; expires_at: string }>(payslipPath(id, false, "/link"), { key: newKey() }));
                  }, false)
                }
              >
                Send a link
              </button>
            )}
          </div>
          {!own && <p className="hint">Send a link only to someone not on Taskiem. Staff on Taskiem get their payslip in their own chat.</p>}
        </>
      )}
    </>
  );
}

/** /payroll/payslips/:id: anyone's payslip, for those with payroll.read. */
export function PayslipPage() {
  const { id = "" } = useParams();
  return <PayslipView id={id} own={false} />;
}

/** /payroll/me/payslips/:id: one's own payslip. */
export function MyPayslipPage() {
  const { id = "" } = useParams();
  return <PayslipView id={id} own={true} />;
}

/** My pay's Payslips: one's own, newest first. */
export function MyPayslips() {
  const view = useLoad(() => get<{ linked: boolean; payslips: MyPayslipRow[] }>("/v1/payroll/me/payslips"), []);
  if (view.data && !view.data.linked) return null;
  const list = view.data?.payslips ?? [];
  return (
    <section className="card" aria-labelledby="mypay-slips-h" data-testid="mypay-payslips">
      <h2 id="mypay-slips-h" style={{ marginTop: 0 }}>
        Payslips
      </h2>
      <ErrorBox error={view.error} />
      {!view.data && !view.error && <Skeleton rows={2} label="Loading your payslips…" />}
      {view.data && list.length === 0 && <EmptyState icon="billing">No payslips yet. They come once a payroll is locked.</EmptyState>}
      {list.length > 0 && (
        <Items label="Your payslips">
          {list.map((x) => {
            const st = lineState(x.state);
            return (
              <Row
                key={x.payslip_id}
                icon="billing"
                title={
                  <Link to={`/payroll/me/payslips/${encodeURIComponent(x.payslip_id)}`} data-testid="mypay-payslip-open">
                    {x.period}
                  </Link>
                }
                side={<span className="item-figure">{naira(x.net_kobo)}</span>}
                sub={
                  <span className="sub">
                    <Pill label={x.words} tone={st.tone} /> {x.pay_date ? `· ${dateLabel(x.pay_date)}` : ""}
                  </span>
                }
                testid="mypay-payslip"
              />
            );
          })}
        </Items>
      )}
    </section>
  );
}

/** A payslip's card in the employee's own Taskiem chat, drawn from
 * Payrolla when shown, for them only. */
export function PayslipChatCard({ id, period }: { id: string; period: string }) {
  const view = useLoad(() => get<{ payslip: Payslip }>(payslipPath(id, true)), [id]);
  const slip = view.data?.payslip;
  return (
    <section className="card payroll-card" aria-label={`Payslip for ${period || "this month"}`} data-testid="payslip-card">
      <h3>{payslipReady(slip?.month || period.split(" ")[0] || "")}</h3>
      {!slip && !view.error && <Skeleton rows={2} label="Loading your payslip…" />}
      {!!view.error && <p className="hint">You can't see this payslip here.</p>}
      {slip && (
        <>
          <p className="payroll-card-figure">{naira(slip.net_kobo)}</p>
          <p className="sub">
            {slip.period} · {slip.employer.name} · {slip.payment.words}
          </p>
          <details className="payroll-breakdown">
            <summary>See the breakdown</summary>
            <PayslipBreakdown slip={slip} />
          </details>
          <div className="toolbar">
            <Link className="button small" to={`/payroll/me/payslips/${encodeURIComponent(id)}`} data-testid="payslip-card-open">
              Open
            </Link>
          </div>
        </>
      )}
    </section>
  );
}
