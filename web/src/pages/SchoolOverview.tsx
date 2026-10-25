import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { get, post } from "../api";
import { useAuth } from "../auth";
import { ErrorBox, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { naira } from "../lib/invoices";
import { studentPath, type SchoolSession, type SchoolTerm } from "../lib/schools";
import { BUCKETS } from "../lib/schoolChasing";
import {
  HOW_PAID_WORDS,
  ITEM_WORDS,
  clearanceLine,
  closeDayLabel,
  closeMatchedLine,
  closeMoneyLine,
  closeSections,
  closeStatus,
  closesPath,
  collectedLine,
  dueByNowLine,
  lastCloseLine,
  matchedLine,
  overviewPath,
  type CloseBrief,
  type OverviewResponse,
  type SchoolClose,
} from "../lib/schoolOverview";
import { SchoolTabs } from "./Schools";

// The proprietor (docs/spec/schools.md 13, B9; docs/schools.md
// "Overview", "The daily close"): the term's money as it stands, on a
// phone first; the daily close, taken once a day by Taskiem after
// midnight or now by hand, with everything that waits or does not
// reconcile and where to fix it.

function Tile({ label, value, hint, testid }: { label: string; value: string; hint?: string; testid: string }) {
  return (
    <div className="stat" data-testid={testid}>
      <span className="stat-label">{label}</span>
      <span className="stat-value">{value}</span>
      {hint && <span className="stat-hint">{hint}</span>}
    </div>
  );
}

/** One close, in full: the day's figures and its lists, each item linking to where it is fixed. */
export function CloseReport({ c }: { c: SchoolClose }) {
  const st = closeStatus(c);
  return (
    <div className="stack" data-testid="close-report">
      <p>
        <span className={`badge sf-status ${st.tone}`} data-testid="close-status">
          {st.label}
        </span>{" "}
        <span className="hint">
          {closeDayLabel(c.day)}
          {c.term ? `, ${c.term}` : ""}; taken {fmtTime(c.ran_at)} by {c.ran_by === "platform" ? "Taskiem" : c.ran_by}.
        </span>
      </p>
      <ul>
        <li>{closeMoneyLine(c)}.</li>
        <li>{closeMatchedLine(c)}.</li>
        {c.lookback_found.count > 0 && (
          <li>
            Found only by the look-back: {naira(c.lookback_found.kobo)} ({c.lookback_found.count}).
          </li>
        )}
        <li>
          Waiting for a second person: {c.pending.length}. Families who say they paid and were not found: {c.claims.length}.
        </li>
      </ul>
      {closeSections(c).map((s) => (
        <section key={s.head} aria-label={s.head} data-testid={s.problem ? "close-problems" : "close-items"}>
          <h3 style={{ margin: "8px 0 4px" }}>{s.head}</h3>
          <ul className="list">
            {s.items.map((it, i) => (
              <li key={`${it.kind}-${i}`} className="list-row">
                <div className="grow">
                  <strong>{ITEM_WORDS[it.kind]}</strong>
                  <div className="hint">{it.message}</div>
                </div>
                <Link to={it.path}>Fix it</Link>
              </li>
            ))}
          </ul>
        </section>
      ))}
      {closeSections(c).length === 0 && <p className="hint">Nothing waits and everything reconciles.</p>}
    </div>
  );
}

/** The daily closes: the latest in full, the rest to open; a button to take yesterday's now. */
function Closes({ manage, onTaken }: { manage: boolean; onTaken: () => void }) {
  const list = useLoad(() => get<{ closes: CloseBrief[] }>(closesPath()), []);
  const [open, setOpen] = useState<string>("");
  const detail = useLoad(() => (open ? get<{ close: SchoolClose }>(`/v1/schools/closes/${open}`) : Promise.resolve(undefined)), [open]);
  const act = useAction();
  const [taken, setTaken] = useState<SchoolClose | null>(null);
  const run = () =>
    act.run(async () => {
      const r = await post<{ close: SchoolClose; taken_now: boolean }>("/v1/schools/close", {});
      setTaken(r.close);
      setOpen(r.close.day);
      list.reload();
      onTaken();
    }, false);
  const closes = list.data?.closes ?? [];
  return (
    <section className="card" aria-labelledby="closes-heading" data-testid="closes">
      <div className="toolbar">
        <h2 id="closes-heading" style={{ margin: 0 }} className="grow">
          The daily close
        </h2>
        {manage && (
          <button type="button" className="secondary" onClick={() => void run()} disabled={act.busy} data-testid="close-run">
            {act.busy ? "Closing…" : "Close yesterday now"}
          </button>
        )}
      </div>
      <p className="hint">
        Taskiem closes each day after midnight, once: what landed on students, what matched by itself and by a person, what waits for a second person, what families say they
        paid, and anything that does not reconcile, each with where to fix it. A close is never recomputed.
      </p>
      <ErrorBox error={act.error ?? list.error ?? detail.error} />
      {taken && (
        <p role="status" className="notice">
          {closeDayLabel(taken.day)} is closed: {closeStatus(taken).label.toLowerCase()}.
        </p>
      )}
      {!list.data && !list.error && <Skeleton rows={2} label="Reading the closes…" />}
      {list.data && closes.length === 0 && <p className="hint" data-testid="closes-none">No close taken yet.</p>}
      {closes.length > 0 && (
        <ul className="list" data-testid="closes-list">
          {closes.map((c) => {
            const st = closeStatus(c);
            return (
              <li key={c.id} className="list-row" data-testid="close-row">
                <div className="grow">
                  <strong>{closeDayLabel(c.day)}</strong> <span className={`badge sf-status ${st.tone}`}>{st.label}</span>
                  <div className="hint">Taken {fmtTime(c.ran_at)}.</div>
                </div>
                <button type="button" className="secondary" onClick={() => setOpen(open === c.day ? "" : c.day)} aria-expanded={open === c.day}>
                  {open === c.day ? "Hide" : "Open"}
                </button>
              </li>
            );
          })}
        </ul>
      )}
      {open && !detail.data && !detail.error && <Skeleton rows={2} label="Reading the close…" />}
      {open && detail.data && <CloseReport c={detail.data.close} />}
    </section>
  );
}

/** Schools > Overview. */
export function SchoolOverviewPage() {
  const { me, can } = useAuth();
  const manage = can("school.manage");
  const reveal0 = can("pii.reveal");
  const [params, setParams] = useSearchParams();
  const term = params.get("term") ?? "";
  const [reveal, setReveal] = useState(false);
  const sessions = useLoad(() => get<{ sessions: SchoolSession[] }>("/v1/schools/sessions"), []);
  const ov = useLoad(() => get<OverviewResponse>(overviewPath(term, reveal)), [term, reveal]);
  if (!me?.schools) return <PageHeader title="Overview" />;
  const terms: SchoolTerm[] = (sessions.data?.sessions ?? []).flatMap((s) => s.terms);
  const o = ov.data?.overview;
  return (
    <>
      <PageHeader
        title="Overview"
        description="The term's fees as they stand: what was expected, what came in and how, what is still owed, who owes most, who is cleared, and the last daily close."
      />
      <SchoolTabs />
      <div className="toolbar">
        <h2 style={{ margin: 0 }} className="grow" data-testid="overview-term">
          {o ? o.term : "The term"}
        </h2>
        <label>
          Term{" "}
          <select
            value={term}
            onChange={(e) => {
              const q: Record<string, string> = {};
              if (e.target.value) q.term = e.target.value;
              setParams(q);
            }}
          >
            <option value="">The current term</option>
            {terms.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name} {t.session}
                {t.state === "current" ? " (current)" : ""}
              </option>
            ))}
          </select>
        </label>
        {!ov.data?.revealed && reveal0 && (
          <button type="button" className="secondary" onClick={() => setReveal(true)}>
            Show names
          </button>
        )}
      </div>
      <ErrorBox error={ov.error} />
      {!o && !ov.error && <Skeleton rows={4} label="Reading the term's money…" />}
      {o && (
        <>
          <section className="card" aria-label="Collected against expected" data-testid="overview-collected">
            <div className="toolbar">
              <strong className="grow">{collectedLine(o)}</strong>
              <span className="hint">
                {o.invoiced} of {o.enrolled} students invoiced
              </span>
            </div>
            <div className="progress" aria-hidden="true">
              <span style={{ width: `${o.collected_percent}%` }} />
            </div>
            <p className="hint" style={{ marginBottom: 0 }}>
              {dueByNowLine(o)}
            </p>
          </section>
          <section className="stats" aria-label="The term's money" data-testid="overview-tiles">
            <Tile label="This week" value={naira(o.this_week_kobo)} hint="Counted since Monday" testid="overview-week" />
            <Tile label="Outstanding" value={naira(o.outstanding_kobo)} hint={o.overdue_kobo > 0 ? `${naira(o.overdue_kobo)} of it past due` : "None past due"} testid="overview-outstanding" />
            <Tile label="Matched by itself" value={`${o.matched.auto_percent}%`} hint={matchedLine(o.matched)} testid="overview-matched" />
            <Tile
              label="Collected for others"
              value={naira(o.pass_through_kobo)}
              hint={o.pass_through.length > 0 ? o.pass_through.map((b) => `${b.body} ${naira(b.kobo)}`).join(", ") + ": not the school's own" : "No pass-through items"}
              testid="overview-pass-through"
            />
            <Tile label="Cleared" value={`${o.clearance.cleared} of ${o.clearance.students}`} hint={clearanceLine(o.clearance)} testid="overview-clearance" />
            <Tile label="Last close" value={o.last_close ? closeDayLabel(o.last_close.day) : "None yet"} hint={lastCloseLine(o.last_close)} testid="overview-last-close" />
          </section>
          <div className="row" style={{ alignItems: "flex-start", flexWrap: "wrap", gap: 16 }}>
            <section className="card" style={{ flex: 1, minWidth: 260 }} aria-labelledby="how-paid-heading" data-testid="overview-how-paid">
              <h2 id="how-paid-heading" style={{ marginTop: 0 }}>
                How parents paid
              </h2>
              {o.how_paid.length === 0 && <p className="hint">Nothing counted yet.</p>}
              <ul>
                {o.how_paid.map((h) => (
                  <li key={h.how}>
                    {HOW_PAID_WORDS[h.how]}: {naira(h.kobo)} ({h.payments} payment{h.payments === 1 ? "" : "s"})
                  </li>
                ))}
              </ul>
            </section>
            <section className="card" style={{ flex: 1, minWidth: 260 }} aria-labelledby="aging-heading" data-testid="overview-aging">
              <h2 id="aging-heading" style={{ marginTop: 0 }}>
                Aging
              </h2>
              <p className="hint">
                {o.debtors} student{o.debtors === 1 ? "" : "s"} owe {naira(o.debtors_kobo)} on open invoices of every term. <Link to="/schools/debtors">Debtors</Link>
              </p>
              <ul>
                {BUCKETS.map((b) => (
                  <li key={b.value}>
                    {b.label}: {o.aging[b.value]?.students ?? 0} student{(o.aging[b.value]?.students ?? 0) === 1 ? "" : "s"}, {naira(o.aging[b.value]?.balance_kobo ?? 0)}
                  </li>
                ))}
              </ul>
            </section>
          </div>
          <section className="card" aria-labelledby="top-debtors-heading" data-testid="overview-top-debtors">
            <h2 id="top-debtors-heading" style={{ marginTop: 0 }}>
              Top debtors
            </h2>
            {o.top_debtors.length === 0 && <p className="hint">Nobody owes anything.</p>}
            {o.top_debtors.length > 0 && (
              <ul className="list">
                {o.top_debtors.map((d) => (
                  <li key={d.student_id} className="list-row" data-testid="top-debtor">
                    <div className="grow">
                      <Link to={studentPath(d.student_id)}>{d.admission_number}</Link> <span>{d.name}</span>
                      <div className="hint">
                        {d.class}; {d.days_past_due > 0 ? `${d.days_past_due} days past due` : "not yet due"}
                      </div>
                    </div>
                    <strong>{naira(d.balance_kobo)}</strong>
                  </li>
                ))}
              </ul>
            )}
          </section>
          <Closes manage={manage} onTaken={ov.reload} />
        </>
      )}
    </>
  );
}
