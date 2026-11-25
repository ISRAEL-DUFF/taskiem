// Payroll settings and the tax and pension schedules (P4b-5,
// docs/spec/chat-pay-people.md): how the payroll is set up in Payrolla,
// read-only with Change in Payrolla (a section of Payroll settings);
// Payroll → Tax and pension, each payroll's PAYE, pension, NHF and NHIS
// schedules; and one schedule with a line per person, its pension PINs and
// PFAs masked until a passkey shows them, and Download as a spreadsheet.
// Read from Payrolla when opened and kept nowhere, as payslips are.

import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { ApiError, get, post } from "../api";
import { EmptyState, ErrorBox, Item, Items, PageHeader, Row, Skeleton, useAction, useLoad } from "../ui";
import { naira } from "../lib/invoices";
import { stepUpAssert } from "../passkeys";
import {
  byMonth,
  peopleWords,
  saveCSV,
  scheduleIcon,
  scheduleSub,
  scheduleTone,
  type PayrollScheduleDetail,
  type PayrollSchedule,
  type PayrollSetupView,
} from "../lib/payrollreports";

function codeOf(e: unknown): string | undefined {
  return e instanceof ApiError ? (e.body.code as string | undefined) : undefined;
}

/** Payrolla hasn't allowed Taskiem to read the schedules: an owner
 * reconnects, others ask the owner. */
function ScopeNote({ error }: { error: unknown }) {
  return (
    <div className="warning" role="note" data-testid="payroll-reports-scope">
      <p style={{ marginTop: 0 }}>{error instanceof Error ? error.message : "Payrolla hasn't allowed Taskiem to read your schedules."}</p>
      <Link className="button" to="/payroll/settings" data-testid="payroll-reports-reconnect">
        Reconnect Payrolla
      </Link>
    </div>
  );
}

function Refused({ error }: { error: unknown }) {
  if (codeOf(error) === "scope_required") {
    const owner = error instanceof ApiError && /Reconnect Payrolla/.test(error.message);
    return owner ? (
      <ScopeNote error={error} />
    ) : (
      <div className="notice" role="note" data-testid="payroll-reports-scope">
        {(error as Error).message}
      </div>
    );
  }
  return <ErrorBox error={error} />;
}

/** The setup in words, read-only, with Change in Payrolla (on Payroll
 * settings). */
export function PayrollSetupSection() {
  const view = useLoad(() => get<PayrollSetupView>("/v1/payroll/settings"), []);
  const s = view.data?.settings;
  return (
    <section className="card" aria-labelledby="payroll-setup-h" data-testid="payroll-setup">
      <h2 id="payroll-setup-h">How your payroll is set up</h2>
      <ErrorBox error={view.error} />
      {!s && !view.error && <Skeleton rows={3} label="Loading…" />}
      {s && (
        <>
          <ul className="items" aria-label="Your payroll's setup">
            {s.rows.map((r) => (
              <Row
                key={r.key}
                icon={r.warn ? "warn" : r.key === "separation_of_duties" ? "lock" : r.key === "pay_frequency" ? "calendar" : "settings"}
                tone={r.warn ? "warn" : undefined}
                title={r.label}
                sub={r.value}
                testid={`payroll-setup-${r.key}`}
              />
            ))}
          </ul>
          <h3>Deductions in force</h3>
          {s.not_set_up && (
            <p className="hint" data-testid="payroll-setup-not-set-up">
              {s.not_set_up}
            </p>
          )}
          {s.deductions.length > 0 && (
            <ul className="items" aria-label="Deductions in force">
              {s.deductions.map((r) => (
                <Row key={r.key} icon="money" tone={r.on ? "ok" : "muted"} title={r.label} sub={r.value} testid={`payroll-setup-${r.key}`} />
              ))}
            </ul>
          )}
          {s.since && <p className="hint">In force since {s.since}.</p>}
          <div className="toolbar">
            {s.change_url ? (
              <a className="button" href={s.change_url} target="_blank" rel="noreferrer" data-testid="payroll-setup-change">
                Change in Payrolla
              </a>
            ) : (
              <p className="hint">Change any of it in Payrolla.</p>
            )}
            <Link className="button quiet" to="/payroll/schedules" data-testid="payroll-setup-schedules">
              Tax and pension
            </Link>
          </div>
        </>
      )}
    </section>
  );
}

function Crumbs({ to = "/payroll", label = "Payroll" }: { to?: string; label?: string }) {
  return (
    <p className="crumbs">
      <Link to={to} aria-label={`Back to ${label}`}>
        {label}
      </Link>
    </p>
  );
}

/** /payroll/schedules: each payroll's schedules, newest first. */
export function PayrollSchedulesPage() {
  const view = useLoad(() => get<{ schedules: PayrollSchedule[] }>("/v1/payroll/schedules"), []);
  const months = view.data ? byMonth(view.data.schedules) : [];
  return (
    <>
      <Crumbs />
      <PageHeader title="Tax and pension" description="The PAYE, pension, NHF and NHIS to pay for each payroll, worked out by Payrolla once the payroll is locked." />
      {view.error !== undefined && <Refused error={view.error} />}
      {!view.data && !view.error && <Skeleton rows={4} label="Loading…" />}
      {view.data && months.length === 0 && (
        <EmptyState icon="reports">No tax or pension to pay yet. It's worked out when a payroll is locked.</EmptyState>
      )}
      {months.map((m) => (
        <section key={m.period} className="card" aria-labelledby={`sch-${m.period}`} data-testid="payroll-schedule-month">
          <h2 id={`sch-${m.period}`} style={{ marginTop: 0 }}>
            {m.words}
          </h2>
          <Items label={`${m.words} schedules`}>
            {m.schedules.map((s) => (
              <Item
                key={s.id}
                to={`/payroll/schedules/${encodeURIComponent(s.id)}`}
                icon={scheduleIcon(s.kind)}
                tone={scheduleTone(s)}
                title={s.title}
                sub={scheduleSub(s)}
                side={naira(s.total_kobo)}
                sideSub={s.state_words}
                testid={`payroll-schedule-${s.kind}`}
              />
            ))}
          </Items>
        </section>
      ))}
    </>
  );
}

/** /payroll/schedules/:id: one schedule, a line per person. */
export function PayrollSchedulePage() {
  const { id = "" } = useParams();
  const view = useLoad(() => get<PayrollScheduleDetail>(`/v1/payroll/schedules/${encodeURIComponent(id)}`), [id]);
  const [full, setFull] = useState<PayrollScheduleDetail>();
  const act = useAction();
  const d = full ?? view.data;
  const s = d?.schedule;
  const pension = s?.kind === "pension";
  const reveal = () =>
    void act.run(async () => {
      const passkey = await stepUpAssert("payroll.reveal", id);
      setFull(await post<PayrollScheduleDetail>(`/v1/payroll/schedules/${encodeURIComponent(id)}/reveal`, { passkey }));
    }, false);
  const download = () =>
    void act.run(async () => {
      const body = pension ? { passkey: await stepUpAssert("payroll.reveal", id) } : {};
      const r = await post<{ filename: string; csv: string }>(`/v1/payroll/schedules/${encodeURIComponent(id)}/download`, body);
      saveCSV(r.filename, r.csv);
    }, "Downloaded");
  return (
    <>
      <Crumbs to="/payroll/schedules" label="Tax and pension" />
      <PageHeader
        title={s ? `${s.title}, ${s.period_words}` : "Schedule"}
        description="Each person's part. Pension PINs are hidden until you confirm with your passkey; nothing of this is kept in Taskiem."
      />
      {view.error !== undefined && <Refused error={view.error} />}
      {!d && !view.error && <Skeleton rows={4} label="Loading…" />}
      {s && d && (
        <>
          <section className="hero" aria-label={`${s.title} to pay`} data-testid="payroll-schedule-hero">
            <div className="hero-top">
              <span className="hero-label">{s.title}</span>
              <span className="hero-tag" data-testid="payroll-schedule-state">
                {s.state_words}
              </span>
            </div>
            <div className="hero-amount">{naira(s.total_kobo)}</div>
            <p className="hero-sub">
              {s.period_words} · {peopleWords(s.people)}
            </p>
            <dl className="hero-foot">
              <div>
                <dt>Paid to</dt>
                <dd className="hero-foot-words">{s.to}</dd>
              </div>
              {s.when && (
                <div>
                  <dt>{s.state === "paid" ? "Paid" : "Due"}</dt>
                  <dd className="hero-foot-words">{s.when.replace(/^(Due|Paid) /, "")}</dd>
                </div>
              )}
            </dl>
          </section>
          <ErrorBox error={act.error} />
          <div className="toolbar">
            {pension && d.masked && (
              <button type="button" className="small" disabled={act.busy} onClick={reveal} data-testid="payroll-schedule-reveal">
                Show PINs
              </button>
            )}
            <button type="button" className="small" disabled={act.busy} onClick={download} data-testid="payroll-schedule-download">
              Download
            </button>
          </div>
          <Items label="Each person">
            {d.lines.map((l) => (
              <Row
                key={l.employee_id}
                letter={l.name.slice(0, 1)}
                title={l.name}
                side={naira(l.amount_kobo)}
                sub={pension ? `PIN ${l.pin || "not on file"} · PFA ${l.pfa || "not on file"}` : undefined}
                testid="payroll-schedule-line"
              />
            ))}
          </Items>
        </>
      )}
    </>
  );
}
