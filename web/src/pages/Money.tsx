import { useId, useMemo, useState } from "react";
import { ReceiveMoneyCard } from "./ReceiveMoney";
import { Link, NavLink, Navigate, useSearchParams } from "react-router-dom";
import { ApiError, get, post, postOnce, put } from "../api";
import { useAuth } from "../auth";
import { EmptyState, ErrorBox, Field, Items, PageHeader, Row, Skeleton, useAction, useLiveRefresh, useLoad } from "../ui";
import { Icon } from "../icons";
import { MoneyBars, MoneySources } from "./MoneyParts";
import { UNMATCHED_PATH, channelWords, dateLabel, naira, newRequestKey, plainMessage, type UnmatchedMoney } from "../lib/invoices";
import {
  CLOSE_CHOICES,
  COMMON_COLUMNS,
  ENTRIES_PATH,
  ENTRY_KINDS,
  ENTRY_LIMITS,
  EXPORTS,
  FEE_RATES_PATH,
  LEAK_FILTERS,
  MAX_STATEMENT_BYTES,
  NOTE_LIMIT,
  PERIOD_CHOICES,
  STATEMENTS_PATH,
  WAITING_PATH,
  addedLine,
  bpToPercent,
  collectedForLine,
  riderDayLine,
  channelProblem,
  closeProblem,
  closeRequest,
  closedLine,
  columnsForm,
  columnsProblem,
  columnsRequest,
  counted,
  csvHeader,
  exportFileName,
  exportPath,
  feeRatePercent,
  periodChoice,
  periodFor,
  periodProblem,
  periodWords,
  summaryPath,
  type MoneySummary,
  type Period,
  type PeriodChoice,
  emptyClose,
  emptyEntry,
  emptyFee,
  enteredLine,
  entryProblem,
  entryRequest,
  feeForm,
  feeProblem,
  feeRequest,
  knownChannels,
  leakFilter,
  leakKindWords,
  leakPath,
  leakSentence,
  leakStatus,
  leaksPath,
  moneyAccess,
  onlyOptionalMissing,
  rateWords,
  statementFormatPath,
  statementRefusal,
  statementRequest,
  waitingDecision,
  waitingPath,
  whoWords,
  type CloseForm,
  type ColumnsForm,
  type EntryForm,
  type FeeForm,
  type FeeRate,
  type LeakTotals,
  type MoneyLeak,
  type StatementAdded,
  type StatementColumns,
  type StatementRefusal,
  type WaitingLine,
} from "../lib/money";

/** Runs a request and words the server's refusal as a sentence. */
async function plainly<T>(p: Promise<T>): Promise<T> {
  try {
    return await p;
  } catch (e) {
    if (e instanceof ApiError && (e.status === 400 || e.status === 409)) e.message = plainMessage(e.message);
    throw e;
  }
}

/** The periods one press away; the rest are under "Other". */
const QUICK_PERIODS = PERIOD_CHOICES.filter((c) => c.value === "today" || c.value === "week" || c.value === "month");

/** Overview, Leaks, Upload statement, Cash and expenses, Fees. */
function MoneyTabs() {
  return (
    <nav className="tabs" aria-label="Money">
      <NavLink to="/money" end>
        Overview
      </NavLink>
      <NavLink to="/money/leaks">Leaks</NavLink>
      <NavLink to="/money/statements">Upload statement</NavLink>
      <NavLink to="/money/entries">Cash and expenses</NavLink>
      <NavLink to="/money/fees">Fees</NavLink>
    </nav>
  );
}

/** Account names already used, for the account fields' suggestions: the fee rates' and the money waiting to be matched. */
function useKnownChannels(): string[] {
  const rates = useLoad(() => get<{ fee_rates: FeeRate[] | null }>(FEE_RATES_PATH).catch(() => ({ fee_rates: [] })), []);
  const waiting = useLoad(() => get<{ payments: UnmatchedMoney[] | null }>(UNMATCHED_PATH).catch(() => ({ payments: [] })), []);
  return useMemo(
    () => knownChannels((rates.data?.fee_rates ?? []).map((r) => r.channel), (waiting.data?.payments ?? []).map((p) => p.channel)),
    [rates.data, waiting.data],
  );
}

function Channels({ id, list }: { id: string; list: string[] }) {
  return (
    <datalist id={id}>
      {list.map((c) => (
        <option key={c} value={c} />
      ))}
    </datalist>
  );
}

function Problem({ shown, problem }: { shown: boolean; problem: string | null }) {
  if (!shown || !problem) return null;
  return (
    <div className="error" role="alert">
      {problem}
    </div>
  );
}

// --- overview ---

/** Money > Overview (GET /v1/money/summary, /v1/money/exports): a period's money in and out, fees, invoices paid, leaks, and the CSV exports. */
export function MoneyOverview() {
  const { can } = useAuth();
  const [params, setParams] = useSearchParams();
  // Links from before the overview (/money?status=closed) were to the leaks.
  const oldLeaks = params.get("status");
  const choice = periodChoice(params.get("period"));
  const custom = { from: params.get("from") ?? "", to: params.get("to") ?? "" };
  const p = periodFor(choice, new Date(), custom);
  const problem = choice === "custom" ? periodProblem(p) : null;
  const key = problem ? "" : summaryPath(p);
  const sum = useLoad(() => (key ? get<MoneySummary>(key) : Promise.resolve(undefined)), [key]);
  const unmatched = useLoad(() => get<{ payments: UnmatchedMoney[] | null }>(UNMATCHED_PATH).catch(() => ({ payments: [] })), []);
  useLiveRefresh(sum.reload);
  const [reveal, setReveal] = useState(false);
  const fromId = useId();
  const toId = useId();
  if (oldLeaks !== null) return <Navigate to={`/money/leaks?${new URLSearchParams({ status: oldLeaks })}`} replace />;
  const choose = (v: PeriodChoice) => {
    if (v === "today") setParams({}, { replace: true });
    else if (v === "custom") setParams({ period: v, from: custom.from || p.from, to: custom.to || p.to }, { replace: true });
    else setParams({ period: v }, { replace: true });
  };
  const setDate = (patch: Partial<Period>) => setParams({ period: "custom", ...custom, ...patch }, { replace: true });
  const s = sum.data && !problem ? sum.data : undefined;
  const channels = s?.channels ?? [];
  const rate = feeRatePercent(s?.fee_rate_bp);
  const waiting = unmatched.data?.payments ?? [];
  return (
    <>
      <PageHeader title="Money" description="Your business's money for a period: what came in and went out, settled and pending, the fees taken, invoices paid and leaks." />
      <MoneyTabs />
      <ReceiveMoneyCard />
      <div className="period-bar">
        <div className="segmented" role="group" aria-label="Period">
          {QUICK_PERIODS.map((c) => (
            <button key={c.value} type="button" aria-pressed={choice === c.value} onClick={() => choose(c.value)}>
              {c.label}
            </button>
          ))}
        </div>
        <label>
          <span className="sr-only">Other period</span>
          <select aria-label="Other period" value={QUICK_PERIODS.some((c) => c.value === choice) ? "" : choice} onChange={(e) => e.target.value && choose(e.target.value as PeriodChoice)}>
            <option value="">Other…</option>
            {PERIOD_CHOICES.filter((c) => !QUICK_PERIODS.some((q) => q.value === c.value)).map((c) => (
              <option key={c.value} value={c.value}>
                {c.label}
              </option>
            ))}
          </select>
        </label>
        {choice === "custom" && (
          <>
            <label htmlFor={fromId}>From</label>
            <input id={fromId} type="date" value={custom.from} max={custom.to || undefined} onChange={(e) => setDate({ from: e.target.value })} />
            <label htmlFor={toId}>To</label>
            <input id={toId} type="date" value={custom.to} min={custom.from || undefined} onChange={(e) => setDate({ to: e.target.value })} />
          </>
        )}
      </div>
      {problem ? (
        <div className="error" role="alert">
          {problem}
        </div>
      ) : (
        <p className="hint" data-testid="period-words">
          {periodWords(p)}, in Lagos time.
        </p>
      )}
      <ErrorBox error={sum.error} />
      {!problem && !s && !sum.error && <Skeleton rows={3} label="Loading the period's money…" />}
      {s && (
        <>
          <section className="money-panel" aria-label="The period's money" data-testid="money-totals">
            <dl className="money-sum">
              <div data-testid="money-in">
                <dt>
                  <span className="swatch in" aria-hidden="true" />
                  Money in
                </dt>
                <dd>
                  {naira(s.in_settled_kobo)}
                  {!!s.in_pending_kobo && <span className="pending">{naira(s.in_pending_kobo)} more pending</span>}
                </dd>
              </div>
              <div data-testid="money-out">
                <dt>
                  <span className="swatch out" aria-hidden="true" />
                  Money out
                </dt>
                <dd>
                  {naira(s.out_settled_kobo)}
                  {!!s.out_pending_kobo && <span className="pending">{naira(s.out_pending_kobo)} more pending</span>}
                </dd>
              </div>
              <div className="kept">
                <dt>Kept</dt>
                <dd>{naira(s.in_settled_kobo - s.out_settled_kobo)}</dd>
              </div>
            </dl>
            {s.unit && s.buckets && s.buckets.length > 1 && <MoneyBars key={key} buckets={s.buckets} unit={s.unit} caption={`Money in and out, ${periodWords(p)}`} />}
          </section>
          <MoneySources channels={channels} total={s.in_settled_kobo} />
          <section className="money-panel to-look" aria-labelledby="look-h" data-testid="overview-leaks">
            <h2 id="look-h">To look at</h2>
            {waiting.length > 0 && (
              <Link className="list-row" to="/invoices/match">
                <span className="list-icon warn">
                  <Icon name="money" />
                </span>
                <span className="grow">
                  <span className="title">{waiting.length === 1 ? "1 payment not matched" : `${waiting.length} payments not matched`}</span>
                  <span className="sub">{naira(waiting.reduce((n, w) => n + (w.left_kobo ?? w.amount_kobo ?? 0), 0))} with no invoice yet</span>
                </span>
                <span className="button">Match</span>
              </Link>
            )}
            <Link className="list-row" to="/money/leaks">
              <span className={s.leaks_open > 0 ? "list-icon warn" : "list-icon"}>
                <Icon name="alerts" />
              </span>
              <span className="grow">
                <span className="title">See the leaks</span>
                <span className="sub">
                  Open now: {counted(s.leaks_open, "leak")}, {naira(s.leaks_open_kobo)} · opened in the period: {counted(s.leaks_opened, "leak")}
                  {s.leaks_opened ? `, ${naira(s.leaks_opened_kobo)}` : ""} · recovered so far: {naira(s.recovered_kobo)}
                </span>
              </span>
              <Icon name="chevron" className="go" />
            </Link>
            <Link className="list-row" to="/money/fees" data-testid="money-fees">
              <span className="list-icon">
                <Icon name="invoices" />
              </span>
              <span className="grow">
                <span className="title">Fees taken: {naira(s.fees_kobo)}</span>
                <span className="sub">{rate ? `${rate} of the money they were taken on` : "What each payments account charges"}</span>
              </span>
              <Icon name="chevron" className="go" />
            </Link>
            <Link className="list-row" to="/invoices" data-testid="invoices-paid">
              <span className="list-icon">
                <Icon name="invoices" />
              </span>
              <span className="grow">
                <span className="title">Invoices paid: {naira(s.invoices_paid_kobo)}</span>
                <span className="sub">{counted(s.invoices_paid, "invoice")}</span>
              </span>
              <Icon name="chevron" className="go" />
            </Link>
            {!!s.collected_for_others_kobo && (
              <div className="list-row" data-testid="collected-for-others">
                <span className="list-icon">
                  <Icon name="schools" />
                </span>
                <span className="grow">
                  <span className="title">Collected for others: {naira(s.collected_for_others_kobo)}</span>
                  <span className="sub">
                    {collectedForLine(s)}: not the business's own. Its own money in: {naira(s.own_in_settled_kobo ?? 0)}.
                  </span>
                </span>
              </div>
            )}
          </section>
          <h2>By channel</h2>
          {channels.length === 0 ? (
            <EmptyState icon="money">No money moved in this period.</EmptyState>
          ) : (
            <table className="cards" data-testid="channels">
              <caption className="sr-only">Money by channel, {periodWords(p)}</caption>
              <thead>
                <tr>
                  <th scope="col">Channel</th>
                  <th scope="col">In</th>
                  <th scope="col">Pending</th>
                  <th scope="col">Out</th>
                  <th scope="col">Fees</th>
                  <th scope="col">Fee rate</th>
                  <th scope="col">Movements</th>
                </tr>
              </thead>
              <tbody>
                {channels.map((c) => (
                  <tr key={c.channel} data-testid="channel-row">
                    <th scope="row" className="primary" data-label="Channel">
                      {channelWords(c.channel)} <span className="hint">{c.channel}</span>
                    </th>
                    <td data-label="In" className="num">
                      {naira(c.in_settled_kobo)}
                    </td>
                    <td data-label="Pending" className="num">
                      {c.in_pending_kobo || c.out_pending_kobo ? [c.in_pending_kobo ? `${naira(c.in_pending_kobo)} in` : "", c.out_pending_kobo ? `${naira(c.out_pending_kobo)} out` : ""].filter(Boolean).join(", ") : <span className="hint">None</span>}
                    </td>
                    <td data-label="Out" className="num">
                      {naira(c.out_settled_kobo)}
                    </td>
                    <td data-label="Fees" className="num">
                      {naira(c.fees_kobo)}
                    </td>
                    <td data-label="Fee rate" className="num">
                      {feeRatePercent(c.fee_rate_bp) || <span className="hint">None</span>}
                    </td>
                    <td data-label="Movements" className="num">
                      {c.movements}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {s.riders && s.riders.length > 0 && (
            <>
              <h2>Riders</h2>
              <Items label={`Riders, ${periodWords(p)}`}>
                {s.riders.map((r) => (
                  <Row
                    key={r.rider_id}
                    letter={r.rider}
                    title={r.rider}
                    side={r.owed_kobo > 0 ? <span className="badge warn">owes {naira(r.owed_kobo)}</span> : undefined}
                    sub={<span className="sub">{riderDayLine(r)}</span>}
                    testid="rider-day"
                  />
                ))}
              </Items>
            </>
          )}
        </>
      )}
      {!problem && (
        <section className="card" aria-labelledby="exports-h" data-testid="exports">
          <h2 id="exports-h">Exports</h2>
          <p className="hint">Download {periodWords(p)} as a CSV file, for your accountant or a spreadsheet.</p>
          {can("pii.reveal") && (
            <label className="inline" title="Adds each customer's name to the invoices export; the access is audited">
              <input type="checkbox" checked={reveal} onChange={(e) => setReveal(e.target.checked)} /> Invoices with customers' names
            </label>
          )}
          <ul className="exports">
            {EXPORTS.map((x) => (
              <li key={x.kind}>
                <a className="button" href={exportPath(x.kind, p, reveal)} download={exportFileName(x.kind, p)}>
                  Download {x.label.toLowerCase()}
                </a>{" "}
                <span className="hint">{x.help}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  );
}

// --- leaks ---

/** Money > Leaks (GET /v1/money/leaks): what Taskiem found, the totals, and closing them. */
export function MoneyLeaks() {
  const { can, me } = useAuth();
  const access = moneyAccess(can);
  const [params, setParams] = useSearchParams();
  const status = leakFilter(params.get("status"));
  const list = useLoad(() => get<{ leaks: MoneyLeak[] | null; totals: LeakTotals }>(leaksPath(status)), [status]);
  useLiveRefresh(list.reload);
  // Members by name, for those who may list them; others see "you" and "a member".
  const members = useLoad(
    () => (can("member.manage") ? get<{ members: { user_id: string; name: string; email: string }[] }>("/v1/members") : Promise.resolve({ members: [] })),
    [],
  );
  const people = members.data?.members ?? [];
  const names = useMemo(() => new Map(people.map((m) => [m.user_id, m.name || m.email])), [people]);
  const [said, setSaid] = useState("");
  const leaks = list.data ? (list.data.leaks ?? []) : undefined;
  const t = list.data?.totals;
  return (
    <>
      <PageHeader title="Leaks" description="Money your business may be losing, found by Taskiem: payments with no invoice, overdue invoices, money paid twice or too much, and fees above what you agreed." />
      <MoneyTabs />
      {t && (
        <section className="stats" aria-label="Leaks at a glance" data-testid="leak-totals">
          <div className="stat">
            <span className="stat-label">Open leaks</span>
            <span className="stat-value">{t.open}</span>
          </div>
          <div className="stat">
            <span className="stat-label">Open, in naira</span>
            <span className="stat-value">{naira(t.open_kobo)}</span>
          </div>
          <div className="stat">
            <span className="stat-label">Recovered so far</span>
            <span className="stat-value">{naira(t.recovered_kobo)}</span>
          </div>
        </section>
      )}
      <div className="filters" role="search" aria-label="Filter leaks">
        <label>
          Show{" "}
          <select value={status} onChange={(e) => setParams(e.target.value === "open" ? {} : { status: e.target.value }, { replace: true })}>
            {LEAK_FILTERS.map((f) => (
              <option key={f.value} value={f.value}>
                {f.label}
              </option>
            ))}
          </select>
        </label>
        <button type="button" onClick={list.reload}>
          Refresh
        </button>
      </div>
      <div role="status" data-testid="leak-status">
        {said && <div className="notice">{said}</div>}
      </div>
      <ErrorBox error={list.error} />
      {leaks === undefined && !list.error && <Skeleton rows={3} label="Loading leaks…" />}
      {leaks && leaks.length === 0 && (
        <EmptyState icon="money">
          {status === "open" ? "No open leaks. Taskiem looks every 15 minutes and lists here anything that may be costing you money." : "No leaks to show."}
        </EmptyState>
      )}
      {leaks?.map((l) => (
        <LeakCard
          key={l.id}
          leak={l}
          manage={access.manage}
          me={me?.user ? { id: me.user.id, name: me.user.name || me.user.email } : undefined}
          people={people}
          names={names}
          onDone={(line) => {
            setSaid(line);
            list.reload();
          }}
        />
      ))}
      {!access.manage && leaks && leaks.length > 0 && <p className="hint">You can see these. Someone who manages invoices can assign and close them.</p>}
    </>
  );
}

function LeakCard({
  leak: l,
  manage,
  me,
  people,
  names,
  onDone,
}: {
  leak: MoneyLeak;
  manage: boolean;
  me?: { id: string; name: string };
  people: { user_id: string; name: string; email: string }[];
  names: Map<string, string>;
  onDone: (line: string) => void;
}) {
  const id = useId();
  const act = useAction();
  const [to, setTo] = useState(l.assigned_to ?? "");
  const [closing, setClosing] = useState(false);
  const [form, setForm] = useState<CloseForm>(emptyClose);
  const [tried, setTried] = useState(false);
  const s = leakStatus(l.status);
  const open = l.status === "open";
  const kind = leakKindWords(l.kind);
  const problem = closeProblem(form);
  // Whom a leak can be given to: the members listed, else the signed-in person.
  const choices = people.length > 0 ? people.map((m) => ({ id: m.user_id, name: m.name || m.email })) : me ? [me] : [];
  if (l.assigned_to && !choices.some((c) => c.id === l.assigned_to)) choices.push({ id: l.assigned_to, name: whoWords(l.assigned_to, names, me?.id) });
  return (
    <section className="card leak-card" aria-labelledby={`${id}-h`} data-testid="leak">
      <h2 id={`${id}-h`}>
        <span className="sf-price">{naira(l.amount_kobo)}</span> <span>{kind}</span>{" "}
        <span className={`badge sf-status ${s.tone}`}>{s.label}</span>
      </h2>
      <p>{leakSentence(l.detail)}</p>
      <dl className="kv">
        <dt>Opened</dt>
        <dd>{dateLabel(l.opened_at)}</dd>
        {l.invoice_id && (
          <>
            <dt>Invoice</dt>
            <dd>
              <Link to={`/invoices/${encodeURIComponent(l.invoice_id)}`}>See the invoice</Link>
            </dd>
          </>
        )}
        {open && (
          <>
            <dt>Assigned to</dt>
            <dd data-testid="assigned">{l.assigned_to ? whoWords(l.assigned_to, names, me?.id) : <span className="hint">No one</span>}</dd>
          </>
        )}
        {!open && (
          <>
            <dt>Closed</dt>
            <dd data-testid="closed-how">
              {closedLine(l, names, me?.id)}
              {l.closed_at && <span className="hint"> on {dateLabel(l.closed_at)}</span>}
            </dd>
          </>
        )}
        {l.note && (
          <>
            <dt>Note</dt>
            <dd>{l.note}</dd>
          </>
        )}
      </dl>
      {manage && open && (
        <div className="leak-actions">
          <form
            className="row leak-assign"
            noValidate
            onSubmit={(e) => {
              e.preventDefault();
              void act.run(async () => {
                await plainly(post(leakPath(l.id, "assign"), { member: to || null }));
                const who = to === me?.id ? "you" : (choices.find((c) => c.id === to)?.name ?? "a member");
                onDone(to ? `${kind} assigned to ${who}.` : `${kind} assigned to no one.`);
              }, false);
            }}
          >
            <Field label="Assign to">
              <select value={to} onChange={(e) => setTo(e.target.value)}>
                <option value="">No one</option>
                {choices.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.id === me?.id ? `Me (${c.name})` : c.name}
                  </option>
                ))}
              </select>
            </Field>
            <button type="submit" disabled={act.busy}>
              Assign
            </button>
          </form>
          {!closing && (
            <div className="toolbar">
              <button type="button" onClick={() => setClosing(true)}>
                Close…
              </button>
            </div>
          )}
          {closing && (
            <form
              className="leak-close"
              noValidate
              aria-label={`Close: ${kind}`}
              onSubmit={(e) => {
                e.preventDefault();
                setTried(true);
                if (problem) return;
                void act.run(async () => {
                  await plainly(post(leakPath(l.id, "close"), closeRequest(form)));
                  onDone(form.status === "recovered" ? `${kind} closed: recovered ${naira(closeRequest(form).recovered_kobo ?? 0)}.` : `${kind} closed as ${leakStatus(form.status).label.toLowerCase()}.`);
                }, false);
              }}
            >
              <fieldset className="sf-choices">
                <legend>How it was closed</legend>
                {CLOSE_CHOICES.map((c) => (
                  <label className="row" key={c.value}>
                    <input type="radio" name={`${id}-close`} checked={form.status === c.value} onChange={() => setForm((f) => ({ ...f, status: c.value }))} />
                    <span>
                      {c.label} <span className="hint">{c.help}</span>
                    </span>
                  </label>
                ))}
              </fieldset>
              {form.status === "recovered" && (
                <Field label="Amount recovered (₦)" hint={`The leak is ${naira(l.amount_kobo)}.`}>
                  <input inputMode="decimal" value={form.recovered} onChange={(e) => setForm((f) => ({ ...f, recovered: e.target.value }))} />
                </Field>
              )}
              <Field label="Note (optional)">
                <textarea rows={2} maxLength={NOTE_LIMIT} value={form.note} onChange={(e) => setForm((f) => ({ ...f, note: e.target.value }))} />
              </Field>
              <Problem shown={tried} problem={problem} />
              <div className="toolbar">
                <button type="submit" className="primary" disabled={act.busy}>
                  Close leak
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setClosing(false);
                    setTried(false);
                    setForm(emptyClose());
                  }}
                >
                  Cancel
                </button>
              </div>
            </form>
          )}
        </div>
      )}
      <ErrorBox error={act.error} />
    </section>
  );
}

// --- statements ---

const FIELDS: { key: keyof ColumnsForm; label: string; optional?: boolean; split?: boolean }[] = [
  { key: "date", label: "Date" },
  { key: "description", label: "Description" },
  { key: "amount", label: "Amount", split: false },
  { key: "in", label: "Money in", split: true },
  { key: "out", label: "Money out", split: true },
  { key: "reference", label: "Reference", optional: true },
  { key: "balance", label: "Balance", optional: true },
];

/** Money > Upload statement (POST /v1/money/statements): a bank statement as a CSV, its columns matched once per account. */
export function MoneyStatements() {
  const { can } = useAuth();
  const manage = moneyAccess(can).manage;
  const channels = useKnownChannels();
  const listId = useId();
  const [channel, setChannel] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [fileKey, setFileKey] = useState(0);
  const [headers, setHeaders] = useState<string[] | null>(null);
  const [cols, setCols] = useState<ColumnsForm | null>(null);
  const [refusal, setRefusal] = useState<StatementRefusal | null>(null);
  const [said, setSaid] = useState("");
  const [tried, setTried] = useState(false);
  const act = useAction();
  const fileProblem = !file ? "Choose the statement file." : file.size > MAX_STATEMENT_BYTES ? "The file is larger than 5 MB; upload it in parts." : null;
  const problem = channelProblem(channel) ?? fileProblem ?? (cols ? columnsProblem(cols) : null);

  /** The column matching for the file, from the account's saved matching (the common format's when none). */
  const matching = async (text: string) => {
    const h = csvHeader(text);
    let saved: StatementColumns | null = null;
    try {
      saved = (await get<{ columns: StatementColumns }>(statementFormatPath(channel.trim()))).columns;
    } catch {
      // nothing saved can be read: guess from the names
    }
    return { headers: h, used: saved ?? COMMON_COLUMNS, form: columnsForm(h, saved) };
  };
  const matchColumns = async (text: string) => {
    const m = await matching(text);
    setHeaders(m.headers);
    setCols(m.form);
  };

  const send = (text: string, columns?: StatementColumns) => plainly(post<StatementAdded>(STATEMENTS_PATH, statementRequest(channel, text, columns)));
  const refusalOf = (e: unknown) => (e instanceof ApiError ? statementRefusal(e.status, e.body, e.message) : null);

  const waiting = useLoad(() => get<{ lines: WaitingLine[] | null }>(WAITING_PATH).catch(() => ({ lines: [] })), []);
  const done = (r: StatementAdded) => {
    setSaid(addedLine(r));
    if (r.waiting) waiting.reload();
    setCols(null);
    setHeaders(null);
    setFile(null);
    setFileKey((k) => k + 1);
    setTried(false);
  };

  const upload = () =>
    act.run(async () => {
      if (!file) return;
      setSaid("");
      setRefusal(null);
      const text = await file.text();
      try {
        done(await send(text, cols ? columnsRequest(cols) : undefined));
        return;
      } catch (e) {
        let why = refusalOf(e);
        if (!why) throw e;
        if (why.missing.length > 0 && !cols) {
          const m = await matching(text);
          // Only an optional column is missing (a file in the common format
          // with no reference): send it again with the columns it has.
          if (onlyOptionalMissing(why.missing, m.used) && !columnsProblem(m.form)) {
            try {
              done(await send(text, columnsRequest(m.form)));
              return;
            } catch (e2) {
              why = refusalOf(e2);
              if (!why) throw e2;
            }
          }
          if (why.missing.length > 0) {
            setHeaders(m.headers);
            setCols(m.form);
          }
        }
        setRefusal(why);
      }
    }, false);

  if (!manage)
    return (
      <>
        <PageHeader title="Upload a statement" />
        <MoneyTabs />
        <p className="hint">You can see money, but not add to it. Someone who manages invoices can upload statements.</p>
      </>
    );
  return (
    <>
      <PageHeader
        title="Upload a statement"
        description="For a bank account Taskiem has no connection to: download its statement as a CSV and upload it here. Lines already in the money log are skipped, so uploading the same days again adds nothing twice."
      />
      <MoneyTabs />
      <div role="status" data-testid="statement-status">
        {said && <div className="notice">{said}</div>}
      </div>
      <WaitingLines lines={waiting.data?.lines ?? []} reload={waiting.reload} />
      <form
        className="card"
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          setTried(true);
          if (problem) return;
          void upload();
        }}
      >
        <Field label="Account" hint="The bank and the account, in small letters with a / between them: gtbank/current, access/savings.">
          <input value={channel} list={listId} autoComplete="off" spellCheck={false} onChange={(e) => setChannel(e.target.value)} placeholder="gtbank/current" />
        </Field>
        <Channels id={listId} list={channels} />
        <Field
          label="Statement file (CSV)"
          hint="The common format has the columns date, description and amount (money in positive, money out negative, in naira), and optionally reference and balance. A file with other columns is matched once, then remembered for this account."
        >
          <input
            key={fileKey}
            type="file"
            accept=".csv,text/csv"
            onChange={(e) => {
              setFile(e.target.files?.[0] ?? null);
              setRefusal(null);
              setSaid("");
            }}
          />
        </Field>
        {cols && headers && (
          <fieldset className="sf-choices column-match" data-testid="column-match">
            <legend>Match the file's columns</legend>
            <p className="hint">Choose which of the file's columns holds each part of a line. This is remembered for {channel.trim() || "this account"}.</p>
            <fieldset className="sf-choices">
              <legend>Amounts</legend>
              <label className="row">
                <input type="radio" name="amount-mode" checked={!cols.split} onChange={() => setCols({ ...cols, split: false })} />
                <span>One column, money out negative</span>
              </label>
              <label className="row">
                <input type="radio" name="amount-mode" checked={cols.split} onChange={() => setCols({ ...cols, split: true })} />
                <span>Two columns: money in and money out</span>
              </label>
            </fieldset>
            <div className="column-grid">
              {FIELDS.filter((f) => f.split === undefined || f.split === cols.split).map((f) => (
                <Field key={f.key} label={f.optional ? `${f.label} (optional)` : f.label}>
                  <select value={String(cols[f.key])} onChange={(e) => setCols({ ...cols, [f.key]: e.target.value })}>
                    <option value="">{f.optional ? "Not in the file" : "Choose a column"}</option>
                    {headers.map((h) => (
                      <option key={h} value={h}>
                        {h}
                      </option>
                    ))}
                  </select>
                </Field>
              ))}
            </div>
          </fieldset>
        )}
        <Problem shown={tried} problem={problem} />
        {refusal && (
          <div className="error" role="alert" data-testid="statement-refusal">
            {refusal.message}
            {refusal.missing.length > 0 && cols && <div>Match the file's columns above, then upload again.</div>}
            {refusal.problems.length > 0 && (
              <ul aria-label="Lines that cannot be read">
                {refusal.problems.map((p) => (
                  <li key={p.row}>
                    Line {p.row}: {p.why}
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy}>
            Upload statement
          </button>
          {!cols && file && channel.trim() && !channelProblem(channel) && (
            <button type="button" disabled={act.busy} onClick={() => void act.run(async () => matchColumns(await file.text()), false)}>
              Match columns myself
            </button>
          )}
        </div>
      </form>
    </>
  );
}

/** Statement lines that may be a transfer someone entered by hand in chat ("Ada paid 2,000 transfer"): each waits for Same money or Different money. */
function WaitingLines({ lines, reload }: { lines: WaitingLine[]; reload: () => void }) {
  const act = useAction();
  if (lines.length === 0) return null;
  const decide = (id: string, entry: string | null) =>
    act.run(async () => {
      await plainly(post(waitingPath(id), waitingDecision(entry)));
      reload();
    }, false);
  return (
    <section className="card" aria-labelledby="waiting-h" data-testid="statement-waiting">
      <h2 id="waiting-h">Transfers to confirm</h2>
      <p className="hint">
        These lines of your statement may be money someone already recorded by hand. Same money counts it once; Different money adds it as new money.
      </p>
      <ErrorBox error={act.error} />
      <Items label="Transfers to confirm">
        {lines.map((l) => (
          <Row
            key={l.id}
            icon="money"
            tone="warn"
            title={`${naira(l.amount_kobo)} on ${dateLabel(l.occurred_at)}`}
            side={<span className="hint">{l.channel}</span>}
            sub={<span className="sub">{l.description || "No description"}</span>}
            testid="waiting-line"
          >
            {l.candidates.map((c) => (
              <button key={c.entry_id} type="button" disabled={act.busy} onClick={() => void decide(l.id, c.entry_id)}>
                Same money: {naira(c.amount_kobo)} on {dateLabel(c.occurred_at)}
                {c.description ? `, ${c.description}` : ""}
              </button>
            ))}
            <button type="button" disabled={act.busy} onClick={() => void decide(l.id, null)}>
              Different money
            </button>
          </Row>
        ))}
      </Items>
    </section>
  );
}

// --- cash and expenses ---

/** Money > Cash and expenses (POST /v1/money/entries): what the money log cannot hear from a connection. */
export function MoneyEntries() {
  const { can } = useAuth();
  const manage = moneyAccess(can).manage;
  const channels = useKnownChannels();
  const listId = useId();
  const [form, setForm] = useState<EntryForm>(() => emptyEntry());
  // One key per form: sending it again (a double click, a retry) adds nothing twice.
  const [key, setKey] = useState(newRequestKey);
  const [tried, setTried] = useState(false);
  const [said, setSaid] = useState("");
  const act = useAction();
  const set = (patch: Partial<EntryForm>) => setForm((f) => ({ ...f, ...patch }));
  const problem = entryProblem(form);
  if (!manage)
    return (
      <>
        <PageHeader title="Cash and expenses" />
        <MoneyTabs />
        <p className="hint">You can see money, but not add to it. Someone who manages invoices can enter cash and expenses.</p>
      </>
    );
  return (
    <>
      <PageHeader title="Cash and expenses" description="Money that moves without a connection Taskiem hears from: cash taken and paid out, and what the business spends. Each entry is added to the money log under your name." />
      <MoneyTabs />
      <div role="status" data-testid="entry-status">
        {said && <div className="notice">{said}</div>}
      </div>
      <form
        className="card"
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          setTried(true);
          if (problem) return;
          void act.run(async () => {
            const r = await plainly(postOnce<{ entry_id: string; created: boolean }>(ENTRIES_PATH, key, entryRequest(form)));
            setSaid(enteredLine(form, r.created));
            setForm({ ...emptyEntry(), kind: form.kind, channel: form.channel });
            setKey(newRequestKey());
            setTried(false);
          }, false);
        }}
      >
        <fieldset className="sf-choices">
          <legend>What it is</legend>
          {ENTRY_KINDS.map((k) => (
            <label className="row" key={k.value}>
              <input type="radio" name="entry-kind" checked={form.kind === k.value} onChange={() => set({ kind: k.value })} />
              <span>
                {k.label} <span className="hint">{k.help}</span>
              </span>
            </label>
          ))}
        </fieldset>
        <div className="row">
          <Field label="Amount (₦)">
            <input inputMode="decimal" value={form.amount} onChange={(e) => set({ amount: e.target.value })} placeholder="12000" />
          </Field>
          <Field label="Date">
            <input type="date" value={form.date} onChange={(e) => set({ date: e.target.value })} />
          </Field>
        </div>
        <Field label="Description" hint="What it was for: diesel for the generator, the day's cash sales.">
          <input value={form.description} maxLength={ENTRY_LIMITS.description} onChange={(e) => set({ description: e.target.value })} />
        </Field>
        {form.kind === "expense" && (
          <div className="row">
            <Field label="Vendor (optional)" hint="Who was paid.">
              <input value={form.vendor} maxLength={ENTRY_LIMITS.vendor} onChange={(e) => set({ vendor: e.target.value })} />
            </Field>
            <Field label="Category (optional)" hint="Such as fuel, rent or supplies.">
              <input value={form.category} maxLength={ENTRY_LIMITS.category} onChange={(e) => set({ category: e.target.value })} />
            </Field>
          </div>
        )}
        <Field label="Account (optional)" hint="Where the money moved. Leave it empty for cash; or name an account, such as gtbank/current.">
          <input value={form.channel} list={listId} autoComplete="off" spellCheck={false} onChange={(e) => set({ channel: e.target.value })} placeholder="cash" />
        </Field>
        <Channels id={listId} list={channels} />
        <Problem shown={tried} problem={problem} />
        <ErrorBox error={act.error} />
        <button type="submit" className="primary" disabled={act.busy}>
          Add to the money log
        </button>
      </form>
    </>
  );
}

// --- fees ---

/** Money > Fees (GET/PUT /v1/money/fee-rates): the fee agreed with each connection. */
export function MoneyFees() {
  const { can } = useAuth();
  const manage = moneyAccess(can).manage;
  const rates = useLoad(() => get<{ fee_rates: FeeRate[] | null }>(FEE_RATES_PATH), []);
  const channels = useKnownChannels();
  const listId = useId();
  const [form, setForm] = useState<FeeForm>(emptyFee);
  const [editing, setEditing] = useState(false);
  const [tried, setTried] = useState(false);
  const [said, setSaid] = useState("");
  const act = useAction();
  const set = (patch: Partial<FeeForm>) => setForm((f) => ({ ...f, ...patch }));
  const problem = feeProblem(form);
  const list = rates.data ? (rates.data.fee_rates ?? []) : undefined;
  return (
    <>
      <PageHeader title="Fees" description="The fee you agreed with each payments connection: a percent of the amount, a flat fee, or both, up to a cap." />
      <MoneyTabs />
      <p className="notice">A fee above the agreed rate becomes a leak, so you can claim the difference back.</p>
      <div role="status" data-testid="fee-status">
        {said && <div className="notice">{said}</div>}
      </div>
      <ErrorBox error={rates.error} />
      {list === undefined && !rates.error && <Skeleton rows={2} label="Loading the fees…" />}
      {list && list.length === 0 && <EmptyState icon="money">No fees agreed yet. Add the rate from your agreement with each connection.</EmptyState>}
      {list && list.length > 0 && (
        <table className="cards">
          <thead>
            <tr>
              <th scope="col">Connection</th>
              <th scope="col">Percent</th>
              <th scope="col">Flat fee</th>
              <th scope="col">Cap</th>
              <th scope="col">
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {list.map((r) => (
              <tr key={r.channel} data-testid="fee-rate">
                <td className="primary" data-label="Connection">
                  {channelWords(r.channel)} <span className="hint">{r.channel}</span>
                </td>
                <td data-label="Percent">{bpToPercent(r.percent_bp)}%</td>
                <td data-label="Flat fee" className="sf-price">
                  {naira(r.flat_kobo)}
                </td>
                <td data-label="Cap" className="sf-price">
                  {r.cap_kobo !== undefined && r.cap_kobo !== null ? naira(r.cap_kobo) : <span className="hint">None</span>}
                </td>
                <td data-label="">
                  {manage && (
                    <button
                      type="button"
                      aria-label={`Change the fee for ${r.channel}`}
                      onClick={() => {
                        setForm(feeForm(r));
                        setEditing(true);
                        setTried(false);
                      }}
                    >
                      Change
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {manage ? (
        <form
          className="card"
          noValidate
          aria-labelledby="fee-form-h"
          onSubmit={(e) => {
            e.preventDefault();
            setTried(true);
            if (problem) return;
            void act.run(async () => {
              const r = await plainly(put<FeeRate>(FEE_RATES_PATH, feeRequest(form)));
              setSaid(`Fee for ${r.channel} saved: ${rateWords(r)}.`);
              setForm(emptyFee());
              setEditing(false);
              setTried(false);
              rates.reload();
            }, false);
          }}
        >
          <h2 id="fee-form-h">{editing ? `Change the fee for ${form.channel}` : "Add a fee"}</h2>
          <Field label="Connection" hint="As the money log names it: the rail and the connection, such as paystack/main.">
            <input value={form.channel} list={listId} autoComplete="off" spellCheck={false} readOnly={editing} onChange={(e) => set({ channel: e.target.value })} placeholder="paystack/main" />
          </Field>
          <Channels id={listId} list={channels} />
          <div className="row">
            <Field label="Percent of the amount" hint="Such as 1.5.">
              <input inputMode="decimal" value={form.percent} onChange={(e) => set({ percent: e.target.value })} />
            </Field>
            <Field label="Flat fee (₦)" hint="Added to each payment.">
              <input inputMode="decimal" value={form.flat} onChange={(e) => set({ flat: e.target.value })} />
            </Field>
            <Field label="Cap (₦, optional)" hint="The most one payment's fee can be.">
              <input inputMode="decimal" value={form.cap} onChange={(e) => set({ cap: e.target.value })} />
            </Field>
          </div>
          <Problem shown={tried} problem={problem} />
          <ErrorBox error={act.error} />
          <div className="toolbar">
            <button type="submit" className="primary" disabled={act.busy}>
              Save fee
            </button>
            {editing && (
              <button
                type="button"
                onClick={() => {
                  setForm(emptyFee());
                  setEditing(false);
                  setTried(false);
                }}
              >
                Cancel
              </button>
            )}
          </div>
        </form>
      ) : (
        <p className="hint">Someone who manages invoices can add and change fees.</p>
      )}
    </>
  );
}
