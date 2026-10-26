import { useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { get, getBlob, post } from "../api";
import { useAuth } from "../auth";
import { EmptyState, ErrorBox, Field, Items, PageHeader, Row, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { naira } from "../lib/invoices";
import { dateLabel, plainMessage, type SchoolClass, type SchoolSession, type SchoolTerm } from "../lib/schools";
import {
  GATE_MIN_LETTERS,
  GATE_WORDS,
  canSearch,
  checkTime,
  clearancePath,
  clearanceStatus,
  clearRecent,
  hasAmountField,
  loadRecent,
  overrideLine,
  overrideProblem,
  reasonWords,
  remember,
  saveRecent,
  totalsLine,
  type Clearance,
  type ClearanceList,
  type ClearanceOverride,
  type GateAnswer,
  type GateHit,
  type RecentCheck,
  type StudentClearance,
} from "../lib/schoolClearance";
import { SchoolTabs } from "./Schools";

// Clearance and the gate (docs/spec/schools.md 12, B8; docs/schools.md
// "Clearance"): the clearance list per term and class, computed as it is
// read; overrides by people with school.approve, with a reason and an end
// date; and the gate screen, one screen for the person at the gate: a
// name search, a tap, CLEARED or SEE THE BURSAR, a short list of recent
// checks, and never an amount.

function StatusBadge({ status }: { status: string }) {
  const s = clearanceStatus(status);
  return <span className={`badge sf-status ${s.tone}`}>{s.label}</span>;
}

/** The form that clears a student by hand: a reason, and the last day it counts. */
function OverrideForm({ student, term, onDone, onCancel }: { student: string; term?: string; onDone: (o: ClearanceOverride) => void; onCancel: () => void }) {
  const [reason, setReason] = useState("");
  const [endsOn, setEndsOn] = useState("");
  const act = useAction();
  const problem = overrideProblem({ reason, ends_on: endsOn });
  const [touched, setTouched] = useState(false);
  return (
    <form
      className="stack"
      aria-label="Clear by override"
      onSubmit={(e) => {
        e.preventDefault();
        setTouched(true);
        if (problem) return;
        void act.run(async () => {
          const body: Record<string, string> = { reason: reason.trim() };
          if (endsOn) body.ends_on = endsOn;
          if (term) body.term_id = term;
          const out = await post<{ override: ClearanceOverride }>(`/v1/schools/students/${encodeURIComponent(student)}/clearance-override`, body).catch((err: unknown) => {
            if (err instanceof Error) err.message = plainMessage(err.message);
            throw err;
          });
          onDone(out.override);
        }, false);
      }}
    >
      <Field label="Reason" hint="Why the student is cleared although the fees are not settled (bereavement in the family). Kept with the override and shown on the clearance page.">
        <input value={reason} onChange={(e) => setReason(e.target.value)} maxLength={300} required autoFocus />
      </Field>
      <Field label="Ends on (optional)" hint="The last day the override counts. Empty: the end of the term.">
        <input type="date" value={endsOn} onChange={(e) => setEndsOn(e.target.value)} />
      </Field>
      {touched && problem && <p className="error-text" role="alert">{problem}</p>}
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button type="submit" className="primary" disabled={act.busy}>
          Clear the student
        </button>
        <button type="button" className="secondary" onClick={onCancel} disabled={act.busy}>
          Cancel
        </button>
      </div>
    </form>
  );
}

function WithdrawButton({ id, onDone }: { id: string; onDone: () => void }) {
  const act = useAction();
  return (
    <>
      <button
        type="button"
        className="secondary"
        disabled={act.busy}
        onClick={() =>
          void act.run(async () => {
            await post(`/v1/schools/clearance-overrides/${encodeURIComponent(id)}/withdraw`, {});
            onDone();
          }, false)
        }
      >
        Withdraw
      </button>
      <ErrorBox error={act.error} />
    </>
  );
}

/** Schools > Clearance. */
export function SchoolClearancePage() {
  const { me, can } = useAuth();
  const approve = can("school.approve");
  const reveal0 = can("pii.reveal");
  const [params, setParams] = useSearchParams();
  const filter = { term: params.get("term") ?? "", class: params.get("class") ?? "" };
  const [reveal, setReveal] = useState(false);
  const sessions = useLoad(() => get<{ sessions: SchoolSession[] }>("/v1/schools/sessions"), []);
  const classes = useLoad(() => get<{ classes: SchoolClass[] }>("/v1/schools/classes"), []);
  const list = useLoad(() => get<ClearanceList>(clearancePath(filter, undefined, reveal)), [filter.term, filter.class, reveal]);
  const [adding, setAdding] = useState("");
  const [said, setSaid] = useState("");
  const act = useAction();
  const setFilter = (next: Partial<typeof filter>) => {
    const q: Record<string, string> = {};
    for (const [k, v] of Object.entries({ ...filter, ...next })) if (v) q[k] = v;
    setParams(q);
  };
  if (!me?.schools) return <PageHeader title="Clearance" />;
  const terms: SchoolTerm[] = (sessions.data?.sessions ?? []).flatMap((s) => s.terms);
  const rows = list.data?.students ?? [];
  const download = () =>
    act.run(async () => {
      const blob = await getBlob(clearancePath(filter, "csv", reveal));
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `clearance-${list.data?.today ?? "today"}.csv`;
      a.click();
      URL.revokeObjectURL(url);
    }, false);
  return (
    <>
      <PageHeader
        title="Clearance"
        description="Who is cleared for the term, by the rule: the term's fees paid in full, a payment plan in good standing, or an override in force. Partly paid is never cleared. Nothing here is set by hand; it is read from the money as it stands."
        actions={
          <>
            <Link to="/schools/gate">Open the gate screen</Link>
            <button type="button" className="secondary" onClick={() => void download()} disabled={act.busy || !list.data}>
              Download CSV
            </button>
          </>
        }
      />
      <SchoolTabs />
      <div role="status">{said && <div className="notice">{said}</div>}</div>
      <section className="card" aria-labelledby="clearance-heading">
        <div className="toolbar">
          <h2 id="clearance-heading" style={{ margin: 0 }} className="grow">
            {list.data ? list.data.term : "The term"}
          </h2>
          <label>
            Term{" "}
            <select value={filter.term} onChange={(e) => setFilter({ term: e.target.value })}>
              <option value="">The current term</option>
              {terms.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name} {t.session}
                  {t.state === "current" ? " (current)" : ""}
                </option>
              ))}
            </select>
          </label>
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
          {!list.data?.revealed && reveal0 && (
            <button type="button" className="secondary" onClick={() => setReveal(true)}>
              Show names
            </button>
          )}
        </div>
        <ErrorBox error={list.error} />
        <ErrorBox error={act.error} />
        {!list.data && !list.error && <Skeleton rows={3} label="Reading the clearance list…" />}
        {list.data && (
          <p className="hint" data-testid="clearance-totals">
            {totalsLine(list.data.totals)} As of {dateLabel(list.data.today)}.
          </p>
        )}
        {list.data && rows.length === 0 && <EmptyState icon="schools">No students are enrolled in this term{filter.class ? " in this class" : ""}.</EmptyState>}
        {rows.length > 0 && (
          <Items label="Students">
            {rows.map((r) => {
              const st = clearanceStatus(r.status);
              return (
                <Row
                  key={r.student_id}
                  testid="clearance-row"
                  attrs={{ "data-status": r.status }}
                  letter={r.name || r.admission_number}
                  tone={st.tone === "ok" || st.tone === "warn" || st.tone === "danger" ? st.tone : "muted"}
                  title={
                    <>
                      <Link to={`/schools/students/${encodeURIComponent(r.student_id)}`}>{r.admission_number}</Link>
                      {r.name ? ` ${r.name}` : ""}
                    </>
                  }
                  side={<StatusBadge status={r.status} />}
                  sub={
                    <span className="sub">
                      {r.class} · balance {naira(r.balance_kobo)}
                      {r.student_status !== "active" && ` · ${r.student_status}`}
                      <span className="block">{reasonWords(r)}</span>
                    </span>
                  }
                >
                  {r.override && (
                    <>
                      <span className="sub" data-testid="override-line">
                        Override: {r.override.reason} · until {dateLabel(r.override.ends_on)}
                      </span>
                      {approve && (
                        <WithdrawButton
                          id={r.override.id}
                          onDone={() => {
                            setSaid(`Override withdrawn for ${r.admission_number}.`);
                            list.reload();
                          }}
                        />
                      )}
                    </>
                  )}
                  {!r.override && approve && r.status !== "cleared" && adding !== r.student_id && (
                    <button type="button" className="secondary" onClick={() => setAdding(r.student_id)}>
                      Clear by override
                    </button>
                  )}
                  {adding === r.student_id && (
                    <OverrideForm
                      student={r.student_id}
                      term={list.data?.term_id}
                      onDone={(o) => {
                        setAdding("");
                        setSaid(`${r.admission_number} cleared by override until ${dateLabel(o.ends_on)}.`);
                        list.reload();
                      }}
                      onCancel={() => setAdding("")}
                    />
                  )}
                </Row>
              );
            })}
          </Items>
        )}
      </section>
      <OverridesCard term={list.data?.term_id} approve={approve} version={said} />
    </>
  );
}

/** The term's overrides, in force and past, with who gave each. */
function OverridesCard({ term, approve, version }: { term?: string; approve: boolean; version: string }) {
  const list = useLoad(() => (term ? get<{ overrides: ClearanceOverride[] }>(`/v1/schools/clearance-overrides?term=${encodeURIComponent(term)}`) : Promise.resolve({ overrides: [] })), [term, version]);
  const [said, setSaid] = useState("");
  return (
    <section className="card" aria-labelledby="overrides-heading">
      <h2 id="overrides-heading">Overrides</h2>
      <p className="hint">Students cleared by hand for this term, with the reason and the day each ends. Each is in the audit log. An override past its end no longer counts.</p>
      <div role="status">{said && <div className="notice">{said}</div>}</div>
      <ErrorBox error={list.error} />
      {list.data && list.data.overrides.length === 0 && <p className="hint">No override for this term.</p>}
      {list.data && list.data.overrides.length > 0 && (
        <ul className="items" data-testid="overrides" aria-label="Overrides">
          {list.data.overrides.map((o) => {
            const state = o.withdrawn_at ? "Withdrawn" : o.in_force ? "In force" : "Ended";
            return (
              <Row
                key={o.id}
                testid="override"
                attrs={{ "data-in-force": String(o.in_force) }}
                icon="lock"
                tone={o.in_force && !o.withdrawn_at ? "ok" : "muted"}
                title={
                  <>
                    <Link to={`/schools/students/${encodeURIComponent(o.student_id)}`}>{o.admission_number}</Link>: {o.reason}
                  </>
                }
                side={<span className={`badge sf-status ${o.withdrawn_at ? "muted" : o.in_force ? "ok" : "muted"}`}>{state}</span>}
                sub={
                  <span className="sub">
                    {dateLabel(o.starts_on)} to {dateLabel(o.ends_on)} · by {o.created_by.replace(/^user:/, "")} {fmtTime(o.created_at)}
                  </span>
                }
              >
                {approve && o.in_force && (
                  <WithdrawButton
                    id={o.id}
                    onDone={() => {
                      setSaid(`Override withdrawn for ${o.admission_number}.`);
                      list.reload();
                    }}
                  />
                )}
              </Row>
            );
          })}
        </ul>
      )}
    </section>
  );
}

/** The student's page: their clearance for the current term and their overrides. */
export function StudentClearancePanel({ studentID, approve }: { studentID: string; approve: boolean }) {
  const one = useLoad(() => get<StudentClearance>(`/v1/schools/students/${encodeURIComponent(studentID)}/clearance`), [studentID]);
  const [adding, setAdding] = useState(false);
  const [said, setSaid] = useState("");
  const c: Clearance | undefined = one.data?.clearance;
  const noTerm = one.error instanceof Error && /no term is current/.test(one.error.message);
  return (
    <section className="card" aria-labelledby="student-clearance">
      <h2 id="student-clearance">Clearance</h2>
      <div role="status">{said && <div className="notice">{said}</div>}</div>
      {noTerm && <p className="hint">No term is current: clearance is read for the current term.</p>}
      {!noTerm && <ErrorBox error={one.error} />}
      {!c && !one.error && <Skeleton rows={2} />}
      {c && (
        <>
          <dl className="kv">
            <dt>This term</dt>
            <dd data-testid="clearance-status">
              <StatusBadge status={c.status} /> {reasonWords(c)}
            </dd>
            <dt>Balance</dt>
            <dd>
              {naira(c.balance_kobo)}
              {c.invoiced ? ` (${naira(c.paid_kobo)} of ${naira(c.total_kobo)} paid)` : " (no invoice for the term)"}
            </dd>
          </dl>
          {approve && c.status !== "cleared" && !adding && (
            <button type="button" className="secondary" onClick={() => setAdding(true)}>
              Clear by override
            </button>
          )}
          {adding && (
            <OverrideForm
              student={studentID}
              term={c.term_id}
              onDone={(o) => {
                setAdding(false);
                setSaid(`Cleared by override until ${dateLabel(o.ends_on)}.`);
                one.reload();
              }}
              onCancel={() => setAdding(false)}
            />
          )}
        </>
      )}
      {one.data && one.data.overrides.length > 0 && (
        <>
          <h3>Overrides</h3>
          <ul className="plain-list" data-testid="student-overrides">
            {one.data.overrides.map((o) => (
              <li key={o.id}>
                <div className="toolbar">
                  <span className="grow">{overrideLine(o)}</span>
                  <span className="hint">by {o.created_by.replace(/^user:/, "")}</span>
                  {approve && o.in_force && (
                    <WithdrawButton
                      id={o.id}
                      onDone={() => {
                        setSaid("Override withdrawn.");
                        one.reload();
                      }}
                    />
                  )}
                </div>
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}

// --- the gate ---

/** The gate screen: a name, a tap, CLEARED or SEE THE BURSAR. No amount, ever. */
export function GateScreen({ standalone }: { standalone?: boolean }) {
  const { me, logout } = useAuth();
  const [q, setQ] = useState("");
  const [hits, setHits] = useState<GateHit[]>();
  const [searching, setSearching] = useState(false);
  const [searchError, setSearchError] = useState<unknown>();
  const [answer, setAnswer] = useState<GateAnswer>();
  const [recent, setRecent] = useState<RecentCheck[]>(() => loadRecent());
  const check = useAction();
  const input = useRef<HTMLInputElement>(null);
  // The search follows the typing, a third of a second behind, from three letters.
  useEffect(() => {
    if (!canSearch(q)) {
      setHits(undefined);
      setSearching(false);
      return;
    }
    let live = true;
    setSearching(true);
    const t = setTimeout(() => {
      get<{ students: GateHit[] }>(`/v1/gate/search?q=${encodeURIComponent(q.trim())}`).then(
        (r) => {
          if (!live) return;
          setSearchError(undefined);
          setHits(hasAmountField(r) ? [] : r.students);
          setSearching(false);
        },
        (err: unknown) => {
          if (!live) return;
          setSearchError(err);
          setHits([]);
          setSearching(false);
        },
      );
    }, 300);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [q]);
  const look = (id: string) =>
    check.run(async () => {
      const a = await get<GateAnswer>(`/v1/gate/students/${encodeURIComponent(id)}`);
      if (hasAmountField(a)) throw new Error("The answer carried more than the gate may show. Tell the school.");
      setAnswer(a);
      const next = remember(recent, a);
      setRecent(next);
      saveRecent(next);
    }, false);
  const again = () => {
    setAnswer(undefined);
    setQ("");
    setHits(undefined);
    setTimeout(() => input.current?.focus(), 0);
  };
  const words = answer ? GATE_WORDS[answer.status] : undefined;
  return (
    <div className={`gate${standalone ? " standalone" : ""}`} data-testid="gate-screen">
      <header className="gate-head">
        <h1>
          {me?.tenant_name || "The school"} <span className="gate-dot">·</span> Gate
        </h1>
        {standalone && (
          <button
            type="button"
            className="secondary gate-signout"
            onClick={() => {
              clearRecent();
              void logout();
            }}
          >
            Sign out
          </button>
        )}
      </header>
      {!answer && (
        <section className="gate-search" aria-labelledby="gate-search-heading">
          <h2 id="gate-search-heading" className="sr-only">
            Find the child
          </h2>
          <label className="gate-label" htmlFor="gate-q">
            Child's name
          </label>
          <input
            id="gate-q"
            ref={input}
            className="gate-input"
            type="search"
            inputMode="search"
            autoComplete="off"
            autoCapitalize="words"
            enterKeyHint="search"
            placeholder={`Type ${GATE_MIN_LETTERS} letters`}
            value={q}
            onChange={(e) => setQ(e.target.value)}
            aria-describedby="gate-hint"
            autoFocus
          />
          <p id="gate-hint" className="gate-hint">
            {canSearch(q) ? (searching ? "Looking…" : hits && hits.length === 0 ? "Nobody by that name. Try another part of the name." : hits ? `Tap the child. ${hits.length === 10 ? "Type more letters to narrow it." : ""}` : "") : `Type at least ${GATE_MIN_LETTERS} letters of the child's name.`}
          </p>
          <ErrorBox error={searchError} />
          <ErrorBox error={check.error} />
          {hits && hits.length > 0 && (
            <ul className="gate-hits" aria-label="Children found" data-testid="gate-hits">
              {hits.map((h) => (
                <li key={h.student_id}>
                  <button type="button" className="gate-hit" disabled={check.busy} onClick={() => void look(h.student_id)}>
                    <span className="gate-hit-name">{h.name}</span>
                    <span className="gate-hit-class">{h.class}</span>
                    <span className="gate-hit-go" aria-hidden="true">
                      ›
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
      {answer && words && (
        <section className={`gate-answer ${words.tone}`} aria-live="assertive" data-testid="gate-answer" data-status={answer.status}>
          <p className="gate-who">
            <strong>{answer.name}</strong> · {answer.class}
          </p>
          <p className="gate-mark" aria-hidden="true">
            {words.mark}
          </p>
          <h2 className="gate-verdict">{words.label}</h2>
          <p className="gate-hint">{words.hint}</p>
          <button type="button" className="primary gate-again" onClick={again} autoFocus>
            Check another child
          </button>
        </section>
      )}
      {recent.length > 0 && (
        <section className="gate-recent" aria-labelledby="gate-recent-heading">
          <h2 id="gate-recent-heading">Just checked</h2>
          <ul data-testid="gate-recent">
            {recent.map((r) => (
              <li key={r.student_id} data-status={r.status}>
                <span className={`gate-recent-mark ${GATE_WORDS[r.status].tone}`} aria-hidden="true">
                  {GATE_WORDS[r.status].mark}
                </span>
                <span className="grow">
                  {r.name} · {r.class}
                </span>
                <span className="gate-recent-what">
                  {GATE_WORDS[r.status].label.toLowerCase()} {checkTime(r.at)}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}
      <p className="gate-foot">No amounts are shown at the gate. For the fees themselves, the bursar.</p>
    </div>
  );
}

/** Schools > Gate: the gate screen for a bursar, inside the Schools area. */
export function SchoolGatePage() {
  const { me } = useAuth();
  if (!me?.schools) return <PageHeader title="The gate" />;
  return (
    <>
      <PageHeader title="The gate" description="The screen the person at the gate sees: a name, a tap, cleared or see the bursar, and never an amount. Give someone the Gate role under Members & keys and this is all they get." actions={<Link to="/schools/clearance">Clearance list</Link>} />
      <SchoolTabs />
      <GateScreen />
    </>
  );
}

/** A gate session's home: the gate screen and nothing else; any address shows it. */
export function GateHome() {
  return (
    <main className="gate-main" id="main">
      <GateScreen standalone />
    </main>
  );
}
