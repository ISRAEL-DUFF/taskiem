import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { get, getBlob, post, postOnce, put } from "../api";
import { useAuth } from "../auth";
import { EmptyState, ErrorBox, Field, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { naira, newRequestKey } from "../lib/invoices";
import { dateLabel, schoolAccess, type SchoolClass, type SchoolSettings } from "../lib/schools";
import { instalmentsLine } from "../lib/schoolFamily";
import {
  BUCKETS,
  HAS_FILTERS,
  MAX_REMIND,
  PLACEHOLDERS,
  RUNGS,
  bucketLabel,
  channelsLine,
  checkedLine,
  daysWords,
  debtorFlags,
  debtorsPath,
  graceProblem,
  outcomeLine,
  planLine,
  planStateWords,
  promiseLine,
  promiseProblem,
  promiseStatus,
  remindedLine,
  renderPreview,
  rungLabel,
  selectUpToCap,
  selectionCounter,
  toggleSelection,
  totalLine,
  wordingProblem,
  type Checked,
  type Debtor,
  type DebtorsResponse,
  type PromiseToPay,
  type Reminded,
  type StudentChasing,
} from "../lib/schoolChasing";
import { SchoolTabs } from "./Schools";

// Schools > Debtors (docs/spec/schools.md 11, B7; docs/schools.md#debtors):
// every student with a balance, aged by days past the due date, with the
// last reminder, any promise to pay and the standing of any payment plan;
// the bursar selects up to fifty and a rung and sends; promises are
// recorded here and on the student's page; the ladder's wording and the
// plan grace are the owner's, in Settings.

/** Schools > Debtors. */
export function SchoolDebtorsPage() {
  const { me, can } = useAuth();
  const may = schoolAccess(can);
  const [params, setParams] = useSearchParams();
  const filter = { class: params.get("class") ?? "", bucket: params.get("bucket") ?? "", has: params.get("has") ?? "" };
  const [reveal, setReveal] = useState(false);
  const list = useLoad(() => get<DebtorsResponse>(debtorsPath(filter) + (reveal ? (debtorsPath(filter).includes("?") ? "&" : "?") + "reveal=true" : "")), [filter.class, filter.bucket, filter.has, reveal]);
  const classes = useLoad(() => get<{ classes: SchoolClass[] }>("/v1/schools/classes"), []);
  const [selected, setSelected] = useState<string[]>([]);
  const [rung, setRung] = useState(1);
  const [result, setResult] = useState<Reminded>();
  const [checked, setChecked] = useState<Checked>();
  const [capHit, setCapHit] = useState(false);
  const act = useAction();
  const check = useAction();
  const setFilter = (next: Partial<typeof filter>) => {
    const q: Record<string, string> = {};
    for (const [k, v] of Object.entries({ ...filter, ...next })) if (v) q[k] = v;
    setParams(q);
    setSelected([]);
  };
  if (!me?.schools) return <PageHeader title="Debtors" />;
  const debtors = list.data?.debtors ?? [];
  const send = () =>
    act.run(async () => {
      const out = await postOnce<Reminded>("/v1/schools/reminders", newRequestKey(), { students: selected, rung });
      setResult(out);
      setSelected([]);
      list.reload();
    }, false);
  const download = () =>
    act.run(async () => {
      const blob = await getBlob(debtorsPath(filter, "csv") + (reveal ? "&reveal=true" : ""));
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `debtors-${list.data?.today ?? "today"}.csv`;
      a.click();
      URL.revokeObjectURL(url);
    }, false);
  return (
    <>
      <PageHeader
        title="Debtors"
        description="Every student with a balance on a sent term invoice, aged by days past the due date. Select up to fifty, choose a rung, and each family gets one message per guardian with a fresh link to its fees page. Families who promised to pay, or keep to a payment plan, are skipped."
        actions={
          <>
            <button type="button" className="secondary" onClick={() => void download()} disabled={act.busy || !list.data}>
              Download CSV
            </button>
            {may.manage && (
              <button
                type="button"
                className="secondary"
                disabled={check.busy}
                onClick={() =>
                  void check.run(async () => {
                    setChecked(await post<Checked>("/v1/schools/chasing/check", {}));
                    list.reload();
                  }, false)
                }
              >
                Check promises and plans now
              </button>
            )}
          </>
        }
      />
      <SchoolTabs />
      <div role="status">{checked && <div className="notice">{checkedLine(checked)}</div>}</div>
      <ErrorBox error={check.error} />
      <section className="card" aria-labelledby="aging-heading">
        <h2 id="aging-heading">Aging</h2>
        <p className="hint">Days past the term's due date{list.data ? `, as of ${dateLabel(list.data.today)}` : ""}. Choose a bucket to see only those students.</p>
        <ErrorBox error={list.error} />
        {!list.data && !list.error && <Skeleton rows={1} />}
        {list.data && (
          <ul className="plain-list aging" data-testid="aging-totals">
            {BUCKETS.map((b) => (
              <li key={b.value}>
                <button type="button" className={filter.bucket === b.value ? "" : "secondary"} aria-pressed={filter.bucket === b.value} onClick={() => setFilter({ bucket: filter.bucket === b.value ? "" : b.value })}>
                  <strong>{b.label}</strong>
                  <br />
                  <span data-testid={`total-${b.value}`}>{totalLine(list.data?.totals[b.value])}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
        {list.data && (
          <p className="hint">
            {list.data.students} student{list.data.students === 1 ? "" : "s"} owe {naira(list.data.balance_kobo)} in all.
          </p>
        )}
      </section>
      <section className="card" aria-labelledby="debtors-heading">
        <div className="toolbar">
          <h2 id="debtors-heading" style={{ margin: 0 }} className="grow">
            Students with a balance
          </h2>
          <label>
            Class{" "}
            <select value={filter.class} onChange={(e) => setFilter({ class: e.target.value })}>
              <option value="">All classes</option>
              {(classes.data?.classes ?? []).map((c) => (
                <option key={c.id} value={c.id}>
                  {c.label}
                </option>
              ))}
            </select>
          </label>
          <label>
            Show{" "}
            <select value={filter.has} onChange={(e) => setFilter({ has: e.target.value })}>
              {HAS_FILTERS.map((f) => (
                <option key={f.value} value={f.value}>
                  {f.label}
                </option>
              ))}
            </select>
          </label>
          {!list.data?.revealed && may.reveal && (
            <button type="button" className="secondary" onClick={() => setReveal(true)}>
              Show names
            </button>
          )}
        </div>
        {may.manage && list.data && debtors.length > 0 && (
          <div className="toolbar" data-testid="send-bar">
            <span className="grow" aria-live="polite" data-testid="selection-counter">
              {selectionCounter(selected.length)}
              {capHit && <span className="error-text"> · At most {MAX_REMIND} per send, so a slip cannot message the whole school.</span>}
            </span>
            <button type="button" className="secondary" onClick={() => setSelected(selectUpToCap(selected, debtors.map((d) => d.student_id)))}>
              Select the first {Math.min(MAX_REMIND, debtors.length)}
            </button>
            <button type="button" className="secondary" onClick={() => setSelected([])} disabled={selected.length === 0}>
              Clear
            </button>
            <label>
              Rung{" "}
              <select value={rung} onChange={(e) => setRung(Number(e.target.value))} aria-describedby="rung-tone">
                {RUNGS.map((r) => (
                  <option key={r.value} value={r.value}>
                    {r.label}
                  </option>
                ))}
              </select>
            </label>
            <span id="rung-tone" className="hint">
              {RUNGS.find((r) => r.value === rung)?.tone}
            </span>
            <button type="button" onClick={() => void send()} disabled={act.busy || selected.length === 0}>
              Send reminder {rung} to {selected.length} {selected.length === 1 ? "family" : "families"}
            </button>
          </div>
        )}
        <ErrorBox error={act.error} />
        <div role="status">
          {result && (
            <div className="notice" data-testid="reminder-results">
              <strong>{remindedLine(result)}</strong>
              <ul className="plain-list">
                {result.results.map((o) => (
                  <li key={o.student_id} data-testid="reminder-result" data-status={o.status}>
                    {outcomeLine(o)}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
        {!list.data && !list.error && <Skeleton rows={3} />}
        {list.data && debtors.length === 0 && <EmptyState icon="schools">Nobody owes anything here. Students with a balance on a sent term invoice appear as soon as there is one.</EmptyState>}
        {list.data && debtors.length > 0 && (
          <table className="cards" data-testid="debtors">
            <thead>
              <tr>
                {may.manage && (
                  <th scope="col">
                    <span className="sr-only">Select</span>
                  </th>
                )}
                <th scope="col">Student</th>
                <th scope="col">Class</th>
                <th scope="col">Balance</th>
                <th scope="col">Past due</th>
                <th scope="col">Last reminder</th>
                <th scope="col">Promise or plan</th>
                {may.manage && <th scope="col">Promise</th>}
              </tr>
            </thead>
            <tbody>
              {debtors.map((d) => (
                <DebtorRow
                  key={d.student_id}
                  d={d}
                  manage={may.manage}
                  selected={selected.includes(d.student_id)}
                  onToggle={() => {
                    const next = toggleSelection(selected, d.student_id);
                    setCapHit(next === null);
                    if (next) setSelected(next);
                  }}
                  onChange={() => list.reload()}
                />
              ))}
            </tbody>
          </table>
        )}
      </section>
    </>
  );
}

function DebtorRow({ d, manage, selected, onToggle, onChange }: { d: Debtor; manage: boolean; selected: boolean; onToggle: () => void; onChange: () => void }) {
  const [promising, setPromising] = useState(false);
  return (
    <tr data-testid="debtor" data-bucket={d.bucket} data-student={d.admission_number}>
      {manage && (
        <td data-label="Select">
          <input type="checkbox" aria-label={`Select ${d.admission_number}`} checked={selected} onChange={onToggle} />
        </td>
      )}
      <td className="primary" data-label="Student">
        <Link to={`/schools/students/${d.student_id}`}>{d.admission_number}</Link> {d.name}
      </td>
      <td data-label="Class">{d.class}</td>
      <td data-label="Balance">{naira(d.balance_kobo)}</td>
      <td data-label="Past due">
        {daysWords(d)}
        <br />
        <span className="hint">{bucketLabel(d.bucket)}</span>
      </td>
      <td data-label="Last reminder">{d.last_reminder ? `Rung ${d.last_reminder.rung}, ${fmtTime(d.last_reminder.at)}${d.last_reminder.nobody ? " (nobody reached)" : ""}` : "None"}</td>
      <td data-label="Promise or plan">
        {debtorFlags(d).length === 0 && <span className="hint">None</span>}
        {debtorFlags(d).map((f) => (
          <span key={f.label} className={`badge sf-status ${f.tone}`} style={{ marginRight: 4 }}>
            {f.label}
          </span>
        ))}
        {d.plan && (
          <>
            <br />
            <span className="hint">{planLine(d.plan)}</span>
          </>
        )}
      </td>
      {manage && (
        <td data-label="Promise">
          {!promising && d.promise?.status !== "open" && (
            <button type="button" className="secondary" onClick={() => setPromising(true)}>
              Record a promise
            </button>
          )}
          {!promising && d.promise?.status === "open" && <span className="hint">{promiseLine(d.promise)}</span>}
          {promising && (
            <PromiseForm
              student={d.student_id}
              onDone={() => {
                setPromising(false);
                onChange();
              }}
              onCancel={() => setPromising(false)}
            />
          )}
        </td>
      )}
    </tr>
  );
}

/** Records a promise to pay for a student: a date and a note. */
function PromiseForm({ student, onDone, onCancel }: { student: string; onDone: () => void; onCancel: () => void }) {
  const [date, setDate] = useState("");
  const [note, setNote] = useState("");
  const act = useAction();
  const problem = promiseProblem(date, note);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (problem) return;
        void act.run(async () => {
          await post(`/v1/schools/students/${encodeURIComponent(student)}/promise`, { date, note: note.trim() });
          onDone();
        }, "Promise recorded");
      }}
    >
      <Field label="Promised to pay by">
        <input type="date" value={date} onChange={(e) => setDate(e.target.value)} required />
      </Field>
      <Field label="Note (optional)" hint="Salary comes on the 25th">
        <input value={note} onChange={(e) => setNote(e.target.value)} maxLength={300} />
      </Field>
      {problem && date && (
        <div className="error" role="alert">
          {problem}
        </div>
      )}
      <div className="toolbar">
        <button type="submit" disabled={act.busy || problem !== null}>
          Save the promise
        </button>
        <button type="button" className="secondary" onClick={onCancel}>
          Cancel
        </button>
      </div>
      <ErrorBox error={act.error} />
    </form>
  );
}

/** A student's page: the balance and aging, promises (recorded and withdrawn here), reminders sent, and the plan's standing. */
export function StudentChasingPanel({ studentID, manage }: { studentID: string; manage: boolean }) {
  const one = useLoad(() => get<StudentChasing>(`/v1/schools/students/${encodeURIComponent(studentID)}/chasing`), [studentID]);
  const [promising, setPromising] = useState(false);
  const act = useAction();
  const c = one.data;
  const open = c?.promises.find((p) => p.status === "open");
  const plan = c?.plan ?? undefined;
  const planWords = planStateWords(plan);
  return (
    <section className="card" aria-labelledby="student-chasing">
      <h2 id="student-chasing">Chasing</h2>
      <ErrorBox error={one.error} />
      {!c && !one.error && <Skeleton rows={2} />}
      {c && (
        <>
          <dl className="kv">
            <dt>Balance</dt>
            <dd data-testid="chasing-balance">{naira(c.balance_kobo)}</dd>
            <dt>Past due</dt>
            <dd>
              {c.balance_kobo > 0 ? daysWords(c) : "Nothing due"}
              {c.balance_kobo > 0 && <span className="hint"> · {bucketLabel(c.bucket)}</span>}
            </dd>
            {planWords && plan && (
              <>
                <dt>Payment plan</dt>
                <dd data-testid="plan-standing">
                  <span className={`badge sf-status ${planWords.tone}`}>{planWords.label}</span> {planLine(plan)}
                  <br />
                  <span className="hint">
                    Approved {fmtTime(plan.approved_at)}: {instalmentsLine(plan.instalments)}. {naira(plan.paid_since_kobo)} paid since
                    {plan.grace_days > 0 ? `; ${plan.grace_days} days of grace` : ""}.
                  </span>
                </dd>
              </>
            )}
          </dl>
          {c.arrears.length > 0 && (
            <p className="hint" data-testid="arrears">
              Instalments missed: {c.arrears.map((a) => `instalment ${a.instalment} due ${dateLabel(a.due_on)} (${naira(a.short_kobo)} short, marked ${fmtTime(a.marked_at)})`).join("; ")}.
            </p>
          )}
          <h3>Promises to pay</h3>
          {c.promises.length === 0 && <p className="hint">No promise recorded.</p>}
          {c.promises.length > 0 && (
            <ul className="plain-list" data-testid="promises">
              {c.promises.map((p) => (
                <PromiseRow key={p.id} p={p} manage={manage} onChange={() => one.reload()} />
              ))}
            </ul>
          )}
          {manage && !open && c.balance_kobo > 0 && !promising && (
            <button type="button" className="secondary" onClick={() => setPromising(true)}>
              Record a promise
            </button>
          )}
          {promising && (
            <PromiseForm
              student={studentID}
              onDone={() => {
                setPromising(false);
                one.reload();
              }}
              onCancel={() => setPromising(false)}
            />
          )}
          <h3>Reminders sent</h3>
          {c.reminders.length === 0 && <p className="hint">No reminder sent yet.</p>}
          {c.reminders.length > 0 && (
            <ul className="plain-list" data-testid="reminders">
              {c.reminders.map((r) => (
                <li key={r.id}>
                  {rungLabel(r.rung)} on {dateLabel(r.sent_on)}: {naira(r.balance_kobo)} due; {channelsLine(r.channels, r.nobody).toLowerCase()}
                </li>
              ))}
            </ul>
          )}
          <ErrorBox error={act.error} />
        </>
      )}
    </section>
  );
}

function PromiseRow({ p, manage, onChange }: { p: PromiseToPay; manage: boolean; onChange: () => void }) {
  const act = useAction();
  const st = promiseStatus(p.status);
  return (
    <li data-testid="promise" data-status={p.status}>
      <div className="toolbar">
        <span className="grow">
          {promiseLine(p)} <span className={`badge sf-status ${st.tone}`}>{st.label}</span>
        </span>
        <span className="hint">
          {naira(p.balance_kobo)} due when made · {fmtTime(p.created_at)}
        </span>
        {manage && p.status === "open" && (
          <button
            type="button"
            className="secondary"
            disabled={act.busy}
            onClick={() =>
              void act.run(async () => {
                await post(`/v1/schools/promises/${p.id}/withdraw`, {});
                onChange();
              }, "Promise withdrawn")
            }
          >
            Withdraw
          </button>
        )}
      </div>
      <ErrorBox error={act.error} />
    </li>
  );
}

/** The owner's reminder ladder: each rung's wording with placeholders, previewed as a family reads it. */
export function ReminderWording() {
  const settings = useLoad(() => get<SchoolSettings>("/v1/settings/schools"), []);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const act = useAction();
  const value = (r: number) => drafts[String(r)] ?? settings.data?.reminder_wording?.[String(r)] ?? "";
  const shown = (r: number) => value(r) || settings.data?.reminder_defaults?.[String(r)] || "";
  const problems = [1, 2, 3].map((r) => wordingProblem(value(r)));
  const bad = problems.some((p) => p !== null);
  return (
    <section className="card" aria-labelledby="reminder-wording">
      <h2 id="reminder-wording" style={{ marginTop: 0 }}>
        Reminder wording
      </h2>
      <p className="hint">
        What a family reads at each rung of the ladder. Use {PLACEHOLDERS.map((p) => `{${p}}`).join(", ")}; the school's name goes before every message, and the link is added when it is
        left out. Leave a rung empty for the default wording.
      </p>
      <ErrorBox error={settings.error} />
      {!settings.data && !settings.error && <Skeleton rows={3} />}
      {settings.data && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (bad) return;
            void act.run(async () => {
              const wording: Record<string, string> = {};
              for (const r of [1, 2, 3]) if (drafts[String(r)] !== undefined) wording[String(r)] = drafts[String(r)] ?? "";
              await put("/v1/settings/schools", { enabled: settings.data?.enabled ?? true, reminder_wording: wording });
              setDrafts({});
              settings.reload();
            }, "Wording saved");
          }}
        >
          {RUNGS.map((r, i) => (
            <div key={r.value} className="panel">
              <Field label={`Rung ${r.label}`} hint={r.tone}>
                <textarea rows={3} value={value(r.value)} placeholder={settings.data?.reminder_defaults?.[String(r.value)]} onChange={(e) => setDrafts({ ...drafts, [String(r.value)]: e.target.value })} />
              </Field>
              {problems[i] && (
                <div className="error" role="alert">
                  {problems[i]}
                </div>
              )}
              <p className="hint" data-testid={`preview-${r.value}`}>
                Preview: {renderPreview(shown(r.value))}
              </p>
            </div>
          ))}
          <button type="submit" disabled={act.busy || bad || Object.keys(drafts).length === 0}>
            Save the wording
          </button>
        </form>
      )}
      <ErrorBox error={act.error} />
    </section>
  );
}

/** The owner's plan grace: days past an instalment's date a plan stays in good standing. */
export function PlanGrace() {
  const settings = useLoad(() => get<SchoolSettings>("/v1/settings/schools"), []);
  const [value, setValue] = useState<string>();
  const act = useAction();
  const shown = value ?? String(settings.data?.plan_grace_days ?? 0);
  const problem = graceProblem(shown);
  return (
    <section className="card" aria-labelledby="plan-grace">
      <h2 id="plan-grace" style={{ marginTop: 0 }}>
        Payment plan grace
      </h2>
      <p className="hint">
        An approved plan is in good standing while what the family has paid since it was approved covers every instalment past its date, plus this many days of grace. A plan in arrears puts
        the student back on Debtors, and the bursar is told each time an instalment is missed.
      </p>
      <ErrorBox error={settings.error} />
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (problem || !settings.data) return;
          void act.run(async () => {
            await put("/v1/settings/schools", { enabled: settings.data?.enabled ?? true, plan_grace_days: Number(shown) });
            setValue(undefined);
            settings.reload();
          }, "Grace saved");
        }}
      >
        <Field label="Days of grace" hint="0 to 60; 0 by default">
          <input inputMode="numeric" value={shown} onChange={(e) => setValue(e.target.value)} />
        </Field>
        {problem && value !== undefined && (
          <div className="error" role="alert">
            {problem}
          </div>
        )}
        <button type="submit" disabled={act.busy || problem !== null || !settings.data}>
          Save the grace
        </button>
      </form>
      <ErrorBox error={act.error} />
    </section>
  );
}
