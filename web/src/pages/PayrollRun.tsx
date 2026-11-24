// This month's payroll in Taskiem (P4b-3, docs/spec/chat-pay-people.md):
// one payroll's page, with each person's pay and its breakdown, a bonus or
// a deduction, leaving someone out and putting them back, Recalculate and
// Cancel before approval, the wallet against the total, and Approve,
// Reject and Lock and pay, each step that commits money confirmed with the
// code Payrolla sends to the approver. Also the Payroll page's This month
// panel, and the payroll's card in the Taskiem chat. The code is sent to
// Payrolla once and kept nowhere.

import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ApiError, del, get, post } from "../api";
import { EmptyState, ErrorBox, Field, Items, Modal, PageHeader, Row, Skeleton, useAction, useLoad } from "../ui";
import { dateLabel, naira } from "../lib/invoices";
import {
  codeDigits,
  countsWords,
  lineState,
  newKey,
  peopleCount,
  periodLabel,
  runStatus,
  sortLines,
  type PayrollRun,
  type PayrollTone,
} from "../lib/payroll";
import { paymentCounts, paymentTone, type RunPayment } from "../lib/payslip";
import {
  CODE_AGAIN,
  actionPath,
  actionWords,
  adjustmentBody,
  adjustmentWords,
  breakdown,
  changesWords,
  koboOf,
  lockWarning,
  nextDecision,
  openToChange,
  type PayrollLineFull,
  type PayrollRunFull,
  type RunAction,
  type RunChanged,
  type RunCodeSent,
  type RunDecided,
  type ThisMonth,
} from "../lib/payrollrun";

function Pill({ label, tone, testid }: { label: string; tone: PayrollTone; testid?: string }) {
  return (
    <span className={`badge sf-status ${tone}`} data-testid={testid}>
      {label}
    </span>
  );
}

const path = (run: string, rest = "") => `/v1/payroll/runs/${encodeURIComponent(run)}${rest}`;

/** Approve or Lock and pay: Payrolla sends the person a code, they type
 * it, and it is sent to Payrolla once. */
export function RunCodeSheet({
  run,
  action,
  onClose,
  onDone,
}: {
  run: PayrollRun;
  action: RunAction;
  onClose: () => void;
  onDone: (words: string) => void;
}) {
  const [sent, setSent] = useState<RunCodeSent>();
  const [code, setCode] = useState("");
  const [again, setAgain] = useState(false);
  const [notLinked, setNotLinked] = useState(false);
  const send = useAction();
  const confirm = useAction();
  const payDay = dateLabel(run.pay_date) || "pay day";
  const sendCode = () =>
    void send.run(async () => {
      setAgain(false);
      setCode("");
      confirm.clear();
      try {
        setSent(await post<RunCodeSent>(path(run.run_id, "/confirmations"), { action, key: newKey() }));
      } catch (e) {
        if (e instanceof ApiError && e.body.code === "not_linked") setNotLinked(true);
        throw e;
      }
    }, false);
  const typed = codeDigits(code);
  const submit = () =>
    void confirm.run(async () => {
      try {
        // Each try at a code is its own submission.
        const d = await post<RunDecided>(path(run.run_id, `/${actionPath(action)}`), { challenge_id: sent?.challenge.challenge_id, code: typed, key: newKey() });
        onDone(d.words);
      } catch (e) {
        if (e instanceof ApiError && CODE_AGAIN.has(String(e.body.code ?? ""))) setAgain(true);
        throw e;
      }
    }, false);
  const title = `${actionWords(action)}: ${periodLabel(run)} payroll`;
  return (
    <Modal title={title} onClose={onClose}>
      <div data-testid="payroll-code-sheet" data-action={action}>
        {action === "lock" && (
          <p className="warning" data-testid="payroll-lock-warning">
            {lockWarning(payDay)}
          </p>
        )}
        {action === "pay_now" && (
          <p className="warning" data-testid="payroll-pay-now-warning">
            Payrolla pays everyone now instead of on {payDay}.
          </p>
        )}
        {action === "retry_failed" && (
          <p className="hint" data-testid="payroll-retry-note">
            Each payment that failed goes again to the person's current account. Payments the bank returned are not sent again.
          </p>
        )}
        {!sent && (
          <>
            <p>
              Payrolla sends a code to your own phone, email or authenticator app for this one step: {peopleCount(run.employee_count)},{" "}
              {naira(run.total_net_kobo)}.
            </p>
            <ErrorBox error={send.error} />
            {notLinked && (
              <p>
                <Link to="/payroll/settings" data-testid="payroll-link-my-account">
                  Link my Payrolla account
                </Link>
              </p>
            )}
            <div className="toolbar">
              <button type="button" className="primary" disabled={send.busy} onClick={sendCode} data-testid="payroll-code-send">
                {send.busy ? "Sending…" : "Send me a code"}
              </button>
              <button type="button" onClick={onClose}>
                Cancel
              </button>
            </div>
          </>
        )}
        {sent && (
          <>
            <p role="status" data-testid="payroll-code-sent">
              {sent.sent_to} Type it here.
            </p>
            {sent.safer && <p className="hint">A code from an authenticator app is safer: set one up in Payrolla.</p>}
            <Field label="The code from Payrolla">
              <input
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={7}
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/[^\d ]/g, ""))}
                data-testid="payroll-code"
              />
            </Field>
            <ErrorBox error={confirm.error} />
            <div className="toolbar">
              {!again && (
                <button type="button" className="primary" disabled={confirm.busy || typed.length !== 6} onClick={submit} data-testid="payroll-code-confirm">
                  {actionWords(action)}
                </button>
              )}
              <button type="button" className={again ? "primary" : "quiet"} disabled={send.busy} onClick={sendCode} data-testid="payroll-code-resend">
                Send a new code
              </button>
            </div>
            <ErrorBox error={send.error} />
          </>
        )}
      </div>
    </Modal>
  );
}

/** Reject: back to whoever prepared it, with a short note. No code. */
export function RejectSheet({ run, onClose, onDone }: { run: PayrollRun; onClose: () => void; onDone: (words: string) => void }) {
  const [note, setNote] = useState("");
  const [key] = useState(newKey);
  const act = useAction();
  return (
    <Modal title={`Send ${periodLabel(run)} payroll back`} onClose={onClose}>
      <p>Whoever prepared it is told, with your note. Nothing is paid.</p>
      <Field label="What needs fixing">
        <input value={note} maxLength={300} onChange={(e) => setNote(e.target.value)} data-testid="payroll-reject-note" />
      </Field>
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button
          type="button"
          className="primary"
          disabled={act.busy || !note.trim()}
          data-testid="payroll-reject-send"
          onClick={() =>
            void act.run(async () => {
              const r = await post<{ words: string }>(path(run.run_id, "/reject"), { note: note.trim(), key });
              onDone(r.words);
            }, false)
          }
        >
          Send it back
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}

/** Add a bonus or a deduction for one person, with why. */
function AdjustSheet({ run, lines, onClose, onDone }: { run: string; lines: PayrollLineFull[]; onClose: () => void; onDone: (r: RunChanged) => void }) {
  const [who, setWho] = useState(lines[0]?.employee_id ?? "");
  const [type, setType] = useState<"bonus" | "deduction">("bonus");
  const [amount, setAmount] = useState("");
  const [reason, setReason] = useState("");
  const [key] = useState(newKey);
  const act = useAction();
  const kobo = koboOf(amount);
  return (
    <Modal title="Add a bonus or deduction" onClose={onClose}>
      <Field label="Who">
        <select value={who} onChange={(e) => setWho(e.target.value)} data-testid="payroll-adjust-who">
          {lines.map((l) => (
            <option key={l.employee_id} value={l.employee_id}>
              {l.name}
            </option>
          ))}
        </select>
      </Field>
      <fieldset className="field">
        <legend>Which</legend>
        <label className="batch-pick">
          <input type="radio" name="payroll-adjust-type" checked={type === "bonus"} onChange={() => setType("bonus")} data-testid="payroll-adjust-bonus" /> A bonus
        </label>
        <label className="batch-pick">
          <input type="radio" name="payroll-adjust-type" checked={type === "deduction"} onChange={() => setType("deduction")} data-testid="payroll-adjust-deduction" /> A
          deduction (an advance repaid is one)
        </label>
      </fieldset>
      <Field label="How much" hint="Before tax: tax and pension apply as usual.">
        <input inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} data-testid="payroll-adjust-amount" />
      </Field>
      <Field label="Why">
        <input value={reason} maxLength={200} onChange={(e) => setReason(e.target.value)} data-testid="payroll-adjust-reason" />
      </Field>
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button
          type="button"
          className="primary"
          disabled={act.busy || !who || !kobo || !reason.trim()}
          data-testid="payroll-adjust-save"
          onClick={() =>
            void act.run(async () => {
              if (!kobo) return;
              onDone(await post<RunChanged>(path(run, "/adjustments"), adjustmentBody(who, type, kobo, reason, key)));
            }, type === "bonus" ? "Bonus added" : "Deduction added")
          }
        >
          {type === "bonus" ? "Add the bonus" : "Add the deduction"}
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}

/** Leave someone out this month, with why. */
function LeaveOutSheet({ run, line, onClose, onDone }: { run: string; line: PayrollLineFull; onClose: () => void; onDone: (r: RunChanged) => void }) {
  const [reason, setReason] = useState("");
  const [key] = useState(newKey);
  const act = useAction();
  return (
    <Modal title={`Leave ${line.name} out this month`} onClose={onClose}>
      <p>They're not paid in this payroll only. Next month they're in it again.</p>
      <Field label="Why">
        <input value={reason} maxLength={200} onChange={(e) => setReason(e.target.value)} data-testid="payroll-leave-reason" />
      </Field>
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button
          type="button"
          className="primary"
          disabled={act.busy || !reason.trim()}
          data-testid="payroll-leave-save"
          onClick={() =>
            void act.run(async () => {
              onDone(await post<RunChanged>(path(run, "/exclusions"), { employee_id: line.employee_id, reason: reason.trim(), key }));
            }, `${line.name} left out this month`)
          }
        >
          Leave out
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}

/** One person's pay: earnings, what is taken, and the net. */
function Breakdown({ line }: { line: PayrollLineFull }) {
  const b = breakdown(line.items);
  if (!line.items || line.items.length === 0) return null;
  const rows = (items: typeof b.earnings, minus: boolean) =>
    items.map((i, n) => (
      <div className="payroll-item" key={`${i.code}-${n}`}>
        <dt>{i.label}</dt>
        <dd>
          {minus ? "−" : ""}
          {naira(i.amount_kobo)}
        </dd>
      </div>
    ));
  return (
    <details className="payroll-breakdown" data-testid="payroll-breakdown">
      <summary>See the breakdown</summary>
      <dl>
        {rows(b.earnings, false)}
        <div className="payroll-item total">
          <dt>Gross</dt>
          <dd>{naira(line.total_earnings_kobo ?? line.gross_kobo ?? 0)}</dd>
        </div>
        {rows(b.deductions, true)}
        <div className="payroll-item total">
          <dt>Net pay</dt>
          <dd>{naira(line.net_kobo)}</dd>
        </div>
      </dl>
      {b.employer.length > 0 && (
        <>
          <p className="payroll-item note">The business also pays:</p>
          <dl>{rows(b.employer, false)}</dl>
        </>
      )}
    </details>
  );
}

/** A locked payroll's payment: its state in words, the counts, Pay now
 * before the date and Pay the failed ones again, each with a code. */
function PaymentPanel({ payment, onAct }: { payment: RunPayment; onAct: (a: "pay_now" | "retry_failed") => void }) {
  const counts = paymentCounts(payment);
  return (
    <section className="card" aria-labelledby="payroll-payment-h" data-testid="payroll-payment" data-status={payment.status}>
      <h2 id="payroll-payment-h">Payment</h2>
      <p>
        <Pill label={payment.words} tone={paymentTone(payment)} testid="payroll-payment-words" />
      </p>
      {counts && <p className="hint">{counts}</p>}
      {payment.status === "AWAITING_FUNDS" && (
        <p className="hint" data-testid="payroll-add-money">
          Add money to your Payrolla wallet. Payrolla pays as soon as it's in.
        </p>
      )}
      {payment.cannot_pay && (payment.status === "SCHEDULED" || payment.failed > 0) && (
        <p className="hint" data-testid="payroll-cannot-pay">
          In Payrolla you can't send payments. Ask the owner.
        </p>
      )}
      {(payment.may_pay_now || payment.may_retry) && (
        <div className="toolbar">
          {payment.may_pay_now && (
            <button type="button" className="primary" onClick={() => onAct("pay_now")} data-testid="payroll-pay-now">
              Pay now
            </button>
          )}
          {payment.may_retry && (
            <button type="button" className="primary" onClick={() => onAct("retry_failed")} data-testid="payroll-retry">
              Pay the failed ones again
            </button>
          )}
        </div>
      )}
    </section>
  );
}

/** /payroll/runs/:run: one payroll, each person's pay and its breakdown,
 * the changes to it, and approving and locking it with Payrolla's code. */
export function PayrollRunPage() {
  const { run = "" } = useParams();
  const navigate = useNavigate();
  const view = useLoad(() => get<PayrollRunFull>(path(run)), [run]);
  const [sheet, setSheet] = useState<RunAction | "reject" | "adjust" | "cancel" | { leave: PayrollLineFull }>();
  const [said, setSaid] = useState<string>();
  const [problem, setProblem] = useState<string>();
  const act = useAction();
  const d = view.data;
  const s = d?.run.summary;
  const notConnected = view.error instanceof ApiError && view.error.body.code === "not_connected";
  const changed = (r: RunChanged | { compute_error?: string }) => {
    setSheet(undefined);
    setProblem(r.compute_error);
    view.reload();
  };
  const decided = (words: string) => {
    setSheet(undefined);
    setSaid(words);
    view.reload();
  };
  const may = d?.may;
  const open = !!s && openToChange(s.status);
  const prep = !!may?.prepare && open;
  const next = s ? nextDecision(s.status) : null;
  return (
    <>
      <p className="crumbs">
        <Link to="/payroll" aria-label="Back to Payroll">
          Payroll
        </Link>
      </p>
      <PageHeader title={s ? `Payroll for ${periodLabel(s)}` : "Payroll"} />
      {notConnected ? (
        <div className="warning" role="note">
          Connect Payrolla first. <Link to="/payroll/settings">Payroll settings</Link>
        </div>
      ) : (
        <ErrorBox error={view.error} />
      )}
      {!d && !view.error && <Skeleton rows={4} label="Loading the payroll…" />}
      {d && s && (
        <>
          <section className="hero" aria-label="Net pay" data-testid="payroll-run-hero">
            <div className="hero-top">
              <span className="hero-label">Net pay</span>
              <span className="hero-tag" data-testid="payroll-hero-status">
                {runStatus(s.status).label}
              </span>
            </div>
            <div className="hero-amount">{naira(s.total_net_kobo)}</div>
            <p className="hero-sub">
              {periodLabel(s)} · {peopleCount(s.employee_count)}
            </p>
            <dl className="hero-foot">
              <div>
                <dt>Pay day</dt>
                <dd className="hero-foot-words">{dateLabel(s.pay_date) || "Not set"}</dd>
              </div>
              <div>
                <dt>Before deductions</dt>
                <dd className="hero-foot-words">{naira(s.total_gross_kobo)}</dd>
              </div>
            </dl>
          </section>
          {said && (
            <div className="notice" role="status" data-testid="payroll-said">
              {said}
            </div>
          )}
          {d.pays_on && !said && (
            <div className="notice" data-testid="payroll-pays-on">
              {d.pays_on}. Payrolla pays everyone then.
            </div>
          )}
          {(problem || d.not_worked_out) && (
            <div className="warning" role="note" data-testid="payroll-not-worked-out">
              <p style={{ marginTop: 0 }}>{problem || d.not_worked_out}</p>
              {may?.prepare && (
                <button
                  type="button"
                  disabled={act.busy}
                  data-testid="payroll-recalculate-now"
                  onClick={() => void act.run(async () => changed(await post<RunChanged>(path(run, "/compute"), { key: newKey() })), false)}
                >
                  Recalculate
                </button>
              )}
            </div>
          )}
          {d.payment && d.payment.status !== "NOT_SCHEDULED" && <PaymentPanel payment={d.payment} onAct={(a) => setSheet(a)} />}
          {d.funding && d.funding.short_by_kobo > 0 && d.funding.words && !d.payment && (
            <div className="warning" role="note" data-testid="payroll-short">
              {d.funding.words}
            </div>
          )}
          {may?.approve && next && (
            <section className="card" aria-labelledby="payroll-decide-h" data-testid="payroll-decide">
              <h2 id="payroll-decide-h">{next === "approve" ? "Waiting for approval" : "Approved: lock and pay next"}</h2>
              {next === "lock" && <p className="hint">{lockWarning(dateLabel(s.pay_date) || "pay day")}</p>}
              {!may.linked && (
                <p className="hint" data-testid="payroll-not-linked">
                  Link your Payrolla account first. It takes one code. <Link to="/payroll/settings">Link my Payrolla account</Link>
                </p>
              )}
              <div className="toolbar">
                <button type="button" className="primary" onClick={() => setSheet(next)} data-testid={`payroll-${next}`}>
                  {actionWords(next)}
                </button>
                {next === "approve" && (
                  <button type="button" onClick={() => setSheet("reject")} data-testid="payroll-reject">
                    Reject
                  </button>
                )}
              </div>
            </section>
          )}
          {!may?.approve && d.approval_url && (
            <section className="card" aria-labelledby="payroll-approve-h" data-testid="payroll-approve">
              <h2 id="payroll-approve-h">Waiting for approval</h2>
              <p className="hint">Approving happens in Payrolla for now. Check the figures there, approve, and Payrolla pays everyone on pay day.</p>
              <div className="toolbar">
                <a className="button primary" href={d.approval_url} target="_blank" rel="noopener noreferrer" data-testid="payroll-approve-open">
                  Review and approve in Payrolla
                  <span className="sr-only"> (opens in a new tab)</span>
                </a>
              </div>
            </section>
          )}
          {may?.reconnect && (
            <p className="hint" data-testid="payroll-reconnect-here">
              Reconnect Payrolla to do this here. <Link to="/payroll/settings">Payroll settings</Link>
            </p>
          )}
          {changesWords(d.changes).length > 0 && (
            <section className="card" aria-labelledby="payroll-changes-h" data-testid="payroll-changes">
              <h2 id="payroll-changes-h">Since {d.changes?.since || "last month"}</h2>
              <ul className="plain">
                {changesWords(d.changes).map((w) => (
                  <li key={w}>{w}</li>
                ))}
              </ul>
            </section>
          )}
          <ErrorBox error={act.error} />
          <section className="card" aria-labelledby="payroll-lines-h">
            <h2 id="payroll-lines-h">Each person</h2>
            {countsWords(d.counts) && !open && <p className="hint">{countsWords(d.counts)}</p>}
            {d.lines.length === 0 && <EmptyState icon="members">No one in this payroll yet.</EmptyState>}
            {d.lines.length > 0 && (
              <Items label="Each person's pay">
                {sortLines(d.lines).map((l) => {
                  const st = lineState(l.state);
                  const line = l as PayrollLineFull;
                  return (
                    <Row
                      key={l.payslip_id || l.employee_id}
                      letter={l.name || "?"}
                      tone={(open && line.items?.length) || st.tone === "ok" ? undefined : st.tone}
                      title={l.name || "Someone"}
                      side={<span className="item-figure">{naira(l.net_kobo)}</span>}
                      testid="payroll-line"
                      attrs={{ "data-state": l.state }}
                      sub={
                        <span className="sub">
                          {(!open || !line.items?.length) && <Pill label={st.label} tone={st.tone} />}
                          {(l.state === "failed" || l.state === "returned") && l.reason && (
                            <span className="block" data-testid="payroll-line-reason">
                              {l.reason}
                            </span>
                          )}
                          {l.staff && (
                            <span className="block">
                              <Link to={`/staff/${encodeURIComponent(l.staff.user_id)}`}>Staff: {l.staff.name}</Link>
                            </span>
                          )}
                          {d.payment?.may_payslips && l.payslip_id && (
                            <span className="block">
                              <Link to={`/payroll/payslips/${encodeURIComponent(l.payslip_id)}`} data-testid="payroll-line-payslip">
                                Payslip<span className="sr-only">: {l.name}</span>
                              </Link>
                            </span>
                          )}
                        </span>
                      }
                    >
                      <Breakdown line={line} />
                      {prep && (
                        <div className="toolbar">
                          <button type="button" className="small quiet" onClick={() => setSheet({ leave: line })} data-testid="payroll-leave-out">
                            Leave out this month<span className="sr-only">: {l.name}</span>
                          </button>
                        </div>
                      )}
                    </Row>
                  );
                })}
              </Items>
            )}
          </section>
          {((d.adjustments ?? []).length > 0 || prep) && (
            <section className="card" aria-labelledby="payroll-adj-h" data-testid="payroll-adjustments">
              <h2 id="payroll-adj-h">Bonuses and deductions</h2>
              {(d.adjustments ?? []).length === 0 && <p className="hint">None this month.</p>}
              {(d.adjustments ?? []).length > 0 && (
                <Items label="Bonuses and deductions">
                  {(d.adjustments ?? []).map((a) => (
                    <Row key={a.id} icon={a.type === "bonus" ? "plus" : "minus"} title={a.name || "Someone"} sub={<span className="sub">{adjustmentWords(a)}</span>} testid="payroll-adjustment">
                      {prep && (
                        <div className="toolbar">
                          <button
                            type="button"
                            className="small quiet"
                            disabled={act.busy}
                            data-testid="payroll-adjustment-remove"
                            onClick={() => void act.run(async () => changed(await del<RunChanged>(path(run, `/adjustments/${encodeURIComponent(a.id)}?key=${encodeURIComponent(newKey())}`))), "Removed")}
                          >
                            Remove<span className="sr-only"> {adjustmentWords(a)}</span>
                          </button>
                        </div>
                      )}
                    </Row>
                  ))}
                </Items>
              )}
              {prep && (
                <div className="toolbar">
                  <button type="button" onClick={() => setSheet("adjust")} data-testid="payroll-adjust">
                    Add a bonus or deduction
                  </button>
                </div>
              )}
            </section>
          )}
          {(d.exclusions ?? []).length > 0 && (
            <section className="card" aria-labelledby="payroll-out-h" data-testid="payroll-left-out">
              <h2 id="payroll-out-h">Left out this month</h2>
              <Items label="Left out this month">
                {(d.exclusions ?? []).map((x) => (
                  <Row key={x.id} letter={x.name} title={x.name} sub={<span className="sub">{x.reason}</span>} testid="payroll-excluded">
                    {prep && (
                      <div className="toolbar">
                        <button
                          type="button"
                          className="small"
                          disabled={act.busy}
                          data-testid="payroll-put-back"
                          onClick={() =>
                            void act.run(async () => changed(await del<RunChanged>(path(run, `/exclusions/${encodeURIComponent(x.id)}?key=${encodeURIComponent(newKey())}`))), `${x.name} is back in`)
                          }
                        >
                          Put back<span className="sr-only"> {x.name}</span>
                        </button>
                      </div>
                    )}
                  </Row>
                ))}
              </Items>
            </section>
          )}
          {prep && (
            <div className="toolbar">
              <button
                type="button"
                disabled={act.busy}
                data-testid="payroll-recalculate"
                onClick={() => void act.run(async () => changed(await post<RunChanged>(path(run, "/compute"), { key: newKey() })), "Worked out again")}
              >
                Recalculate
              </button>
              <button type="button" className="quiet danger" onClick={() => setSheet("cancel")} data-testid="payroll-cancel">
                Cancel this payroll
              </button>
            </div>
          )}
          {(sheet === "approve" || sheet === "lock" || sheet === "pay_now" || sheet === "retry_failed") && (
            <RunCodeSheet run={s} action={sheet} onClose={() => setSheet(undefined)} onDone={decided} />
          )}
          {sheet === "reject" && <RejectSheet run={s} onClose={() => setSheet(undefined)} onDone={decided} />}
          {sheet === "adjust" && <AdjustSheet run={run} lines={d.lines} onClose={() => setSheet(undefined)} onDone={changed} />}
          {typeof sheet === "object" && <LeaveOutSheet run={run} line={sheet.leave} onClose={() => setSheet(undefined)} onDone={changed} />}
          {sheet === "cancel" && (
            <Modal title={`Cancel ${periodLabel(s)} payroll?`} onClose={() => setSheet(undefined)}>
              <p>No one is paid from it. You can prepare it again.</p>
              <ErrorBox error={act.error} />
              <div className="toolbar">
                <button
                  type="button"
                  className="primary danger"
                  disabled={act.busy}
                  data-testid="payroll-cancel-yes"
                  onClick={() =>
                    void act.run(async () => {
                      await post(path(run, "/cancel"), { key: newKey() });
                      setSheet(undefined);
                      navigate("/payroll");
                    }, "Payroll cancelled")
                  }
                >
                  Cancel this payroll
                </button>
                <button type="button" onClick={() => setSheet(undefined)}>
                  Keep it
                </button>
              </div>
            </Modal>
          )}
        </>
      )}
    </>
  );
}

/** The Payroll page's This month panel: this month's payroll, or Prepare
 * for those who may. */
export function ThisMonthPanel({ month, mayPrepare }: { month: ThisMonth; mayPrepare: boolean }) {
  const navigate = useNavigate();
  const act = useAction();
  const [key] = useState(newKey);
  const [existing, setExisting] = useState<string>();
  const r = month.run;
  return (
    <section className="card" aria-labelledby="payroll-month-h" data-testid="payroll-this-month">
      <h2 id="payroll-month-h">This month</h2>
      {r ? (
        <p>
          <Link to={`/payroll/runs/${encodeURIComponent(r.run_id)}`} data-testid="payroll-this-month-open">
            {month.label} payroll
          </Link>{" "}
          · {runStatus(r.status).label}
        </p>
      ) : mayPrepare ? (
        <>
          <p className="hint">Payrolla works it out: each person's pay, tax and pension. You check it, then it's approved with a code.</p>
          <ErrorBox error={act.error} />
          {existing && (
            <p>
              <Link to={`/payroll/runs/${encodeURIComponent(existing)}`} data-testid="payroll-open-existing">
                Open it
              </Link>
            </p>
          )}
          <div className="toolbar">
            <button
              type="button"
              className="primary"
              disabled={act.busy}
              data-testid="payroll-prepare"
              onClick={() =>
                void act.run(async () => {
                  try {
                    const out = await post<RunChanged>("/v1/payroll/runs", { period: month.period, key });
                    navigate(`/payroll/runs/${encodeURIComponent(out.run.run_id)}`);
                  } catch (e) {
                    if (e instanceof ApiError && e.body.code === "duplicate_run" && typeof e.body.run_id === "string") setExisting(e.body.run_id);
                    throw e;
                  }
                }, false)
              }
            >
              {act.busy ? "Preparing…" : `Prepare ${month.month} payroll`}
            </button>
          </div>
        </>
      ) : (
        <p className="hint">No payroll for {month.label} yet.</p>
      )}
    </section>
  );
}

/** A payroll's card in the Taskiem chat, drawn from Payrolla when shown:
 * its period, people, total, pay day, the change from last month and the
 * wallet's shortfall, with Approve, Reject and Open. */
export function PayrollRunCard({ id, period }: { id: string; period: string }) {
  const view = useLoad(() => get<PayrollRunFull>(path(id)), [id]);
  const [sheet, setSheet] = useState<RunAction | "reject">();
  const [said, setSaid] = useState<string>();
  const d = view.data;
  const s = d?.run.summary;
  const next: RunAction | null = s && d?.may?.approve ? (nextDecision(s.status) ?? (d.payment?.may_retry ? "retry_failed" : null)) : null;
  const done = (words: string) => {
    setSheet(undefined);
    setSaid(words);
    view.reload();
  };
  return (
    <section className="card payroll-card" aria-label={`${period || "This month's"} payroll`} data-testid="payroll-card">
      <h3>{period || (s ? periodLabel(s) : "Payroll")} payroll</h3>
      {!d && !view.error && <Skeleton rows={2} label="Loading the payroll…" />}
      {!!view.error && <p className="hint">You can't see this payroll here.</p>}
      {s && (
        <>
          <p className="payroll-card-figure">{naira(s.total_net_kobo)}</p>
          <p className="sub">
            {peopleCount(s.employee_count)} · pay day {dateLabel(s.pay_date)} · {d?.pays_on ?? runStatus(s.status).label}
          </p>
          {changesWords(d?.changes).map((w) => (
            <p className="sub" key={w}>
              {w}
            </p>
          ))}
          {d?.funding && d.funding.short_by_kobo > 0 && !d.payment && <p className="warning">Your Payrolla wallet is short by {naira(d.funding.short_by_kobo)}.</p>}
          {d?.payment && d.payment.status !== "NOT_SCHEDULED" && d.payment.status !== "SCHEDULED" && (
            <p className="sub" data-testid="payroll-card-payment">
              <Pill label={d.payment.words} tone={paymentTone(d.payment)} />
            </p>
          )}
          {said && (
            <p role="status" data-testid="payroll-card-said">
              {said}
            </p>
          )}
          <div className="toolbar">
            {next && (
              <button type="button" className="small primary" onClick={() => setSheet(next)} data-testid="payroll-card-decide">
                {actionWords(next)}
              </button>
            )}
            {next === "approve" && (
              <button type="button" className="small" onClick={() => setSheet("reject")} data-testid="payroll-card-reject">
                Reject
              </button>
            )}
            <Link className="button small" to={`/payroll/runs/${encodeURIComponent(id)}`} data-testid="payroll-card-open">
              Open
            </Link>
          </div>
          {sheet && sheet !== "reject" && <RunCodeSheet run={s} action={sheet} onClose={() => setSheet(undefined)} onDone={done} />}
          {sheet === "reject" && <RejectSheet run={s} onClose={() => setSheet(undefined)} onDone={done} />}
        </>
      )}
    </section>
  );
}
