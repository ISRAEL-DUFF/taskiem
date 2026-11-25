import { useEffect, useId, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { get } from "../api";
import { useAuth } from "../auth";
import { EmptyState, ErrorBox, Item, Items, PageHeader, Row, Skeleton, fmtTime, useLiveRefresh, useLoad } from "../ui";
import { naira } from "../lib/invoices";
import { exportFileName, exportPath, periodWords } from "../lib/money";
import {
  DIRECTIONS,
  MATCHED,
  TX_PERIODS,
  byDay,
  countPath,
  countTitle,
  kindWords,
  narrowed,
  otherLine,
  pendingLine,
  sourceWords,
  statusWord,
  totalsHero,
  txAmount,
  txEntryPath,
  txFilter,
  txHero,
  txParams,
  txPath,
  txPeriod,
  txPeriodWords,
  txProblem,
  txStatus,
  txSub,
  txTitle,
  whereWords,
  type Movement,
  type MovementAnswer,
  type MovementPage,
  type TxFilter,
  type TxPeriod,
} from "../lib/transactions";
import { MoneyTabs } from "./Money";

/** The periods one press away; the rest are under "Other". */
const QUICK = TX_PERIODS.filter((c) => c.value === "today" || c.value === "week" || c.value === "month");

function TxBadge({ m }: { m: Movement }) {
  const s = txStatus(m);
  return <span className={`badge sf-status ${s.tone}`}>{s.label}</span>;
}

/** Money › Transactions (GET /v1/money/entries?movements=true): every
 * movement of the money log, newest first, filtered and searched, with
 * the totals for the filter and the period's CSV. */
export function MoneyTransactions() {
  const [params, setParams] = useSearchParams();
  const f = txFilter(params);
  const problem = txProblem(f);
  const key = problem ? "" : txPath(f);
  const first = useLoad(() => (key ? get<MovementPage>(key) : Promise.resolve(undefined)), [key]);
  useLiveRefresh(first.reload);
  // Later pages, read on "Show more"; dropped when the filter changes.
  const [more, setMore] = useState<{ key: string; rows: Movement[]; next?: string }>({ key: "", rows: [] });
  const [busy, setBusy] = useState(false);
  const [moreError, setMoreError] = useState<unknown>();
  const [typed, setTyped] = useState(f.q);
  const searchId = useId();
  const fromId = useId();
  const toId = useId();
  const set = (patch: Partial<TxFilter>) => setParams(txParams({ ...f, ...patch }), { replace: true });
  // The search is sent a moment after typing stops.
  useEffect(() => {
    if (typed === f.q) return;
    const t = setTimeout(() => setParams(txParams({ ...f, q: typed }), { replace: true }), 350);
    return () => clearTimeout(t);
  }, [typed, f, setParams]);
  const page = first.data;
  const extra = more.key === key ? more : { key, rows: [], next: undefined };
  const rows = page ? [...page.entries, ...extra.rows] : undefined;
  const next = extra.rows.length ? extra.next : page?.next;
  const loadMore = async () => {
    if (!next) return;
    setBusy(true);
    setMoreError(undefined);
    try {
      const r = await get<MovementPage>(txPath(f, next));
      setMore({ key, rows: [...extra.rows, ...r.entries], next: r.next });
    } catch (e) {
      setMoreError(e);
    } finally {
      setBusy(false);
    }
  };
  const choose = (v: TxPeriod) => {
    if (v === "custom") {
      const p = txPeriod(f) ?? txPeriod({ ...f, period: "month" });
      set({ period: v, from: f.from || p?.from || "", to: f.to || p?.to || "" });
    } else set({ period: v, from: "", to: "" });
  };
  const period = txPeriod(f);
  const t = page?.totals;
  const hero = t ? totalsHero(t, f) : undefined;
  const channels = page?.channels ?? [];
  return (
    <>
      <PageHeader title="Transactions" description="Every payment in and out of the business, newest first. Open one to see its fee, its reference and what it paid for." />
      <MoneyTabs />
      <div className="period-bar">
        <div className="segmented" role="group" aria-label="Period">
          {QUICK.map((c) => (
            <button key={c.value} type="button" aria-pressed={f.period === c.value} onClick={() => choose(c.value)}>
              {c.label}
            </button>
          ))}
        </div>
        <label>
          <span className="sr-only">Other period</span>
          <select aria-label="Other period" value={QUICK.some((c) => c.value === f.period) ? "" : f.period} onChange={(e) => e.target.value && choose(e.target.value as TxPeriod)}>
            <option value="">Other…</option>
            {TX_PERIODS.filter((c) => !QUICK.some((q) => q.value === c.value)).map((c) => (
              <option key={c.value} value={c.value}>
                {c.label}
              </option>
            ))}
          </select>
        </label>
        {f.period === "custom" && (
          <>
            <label htmlFor={fromId}>From</label>
            <input id={fromId} type="date" value={f.from} max={f.to || undefined} onChange={(e) => set({ from: e.target.value })} />
            <label htmlFor={toId}>To</label>
            <input id={toId} type="date" value={f.to} min={f.from || undefined} onChange={(e) => set({ to: e.target.value })} />
          </>
        )}
      </div>
      <div className="filters" role="search" aria-label="Filter transactions">
        <label className="search" htmlFor={searchId}>
          <span className="sr-only">Search</span>
          <input id={searchId} type="search" placeholder="Who, reference or amount" value={typed} maxLength={100} onChange={(e) => setTyped(e.target.value)} />
        </label>
        <label>
          <span className="sr-only">In or out</span>
          <select aria-label="In or out" value={f.direction} onChange={(e) => set({ direction: e.target.value as TxFilter["direction"] })}>
            {DIRECTIONS.map((d) => (
              <option key={d.value} value={d.value}>
                {d.label}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span className="sr-only">Where</span>
          <select aria-label="Where" value={f.channel} onChange={(e) => set({ channel: e.target.value })}>
            <option value="">Every account</option>
            {[...new Set([...channels, ...(f.channel ? [f.channel] : [])])].map((c) => (
              <option key={c} value={c}>
                {whereWords(c)}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span className="sr-only">Matched</span>
          <select aria-label="Matched" value={f.matched} onChange={(e) => set({ matched: e.target.value as TxFilter["matched"] })}>
            {MATCHED.map((m) => (
              <option key={m.value} value={m.value}>
                {m.label}
              </option>
            ))}
          </select>
        </label>
      </div>
      {problem && (
        <div className="error" role="alert">
          {problem}
        </div>
      )}
      <ErrorBox error={first.error} />
      {hero && t && (
        <section className="hero" aria-label={`${hero.label}, ${txPeriodWords(f)}`} data-testid="tx-totals">
          <div className="hero-top">
            <span className="hero-label">
              {hero.label} · {txPeriodWords(f)}
            </span>
            {narrowed(f) && <span className="hero-tag">Filtered</span>}
          </div>
          <div className="hero-amount">{hero.amount}</div>
          <p className="hero-sub">{hero.sub}</p>
          {(pendingLine(t) || otherLine(t)) && <p className="hero-sub">{[pendingLine(t), otherLine(t)].filter(Boolean).join(". ")}</p>}
        </section>
      )}
      {!problem && !rows && !first.error && <Skeleton rows={5} label="Loading the transactions…" />}
      {rows && rows.length === 0 && (
        <EmptyState icon="money">{narrowed(f) ? "Nothing matches these filters." : "No money moved in this period."}</EmptyState>
      )}
      {rows &&
        byDay(rows).map((g) => (
          <section key={g.day} aria-label={g.day} className="tx-day">
            <h2 className="tx-day-head">{g.day}</h2>
            <Items label={`Transactions, ${g.day}`}>
              {g.rows.map((m) => {
                const s = txStatus(m);
                return (
                  <Item
                    key={m.movement}
                    testid="tx-row"
                    to={`/money/transactions/${encodeURIComponent(m.id)}`}
                    icon={m.direction === "in" ? "money" : "send"}
                    tone={s.tone === "ok" ? undefined : s.tone}
                    title={txTitle(m)}
                    side={<span className={m.direction === "in" ? "tx-in" : undefined}>{txAmount(m)}</span>}
                    sub={[txSub(m), fmtTime(m.when)].filter(Boolean).join(" · ")}
                    sideSub={<TxBadge m={m} />}
                  />
                );
              })}
            </Items>
          </section>
        ))}
      <ErrorBox error={moreError} />
      {next && (
        <p className="tx-more">
          <button type="button" onClick={() => void loadMore()} disabled={busy}>
            {busy ? "Loading…" : "Show more"}
          </button>
        </p>
      )}
      {period && !problem && (
        <section className="card" aria-labelledby="tx-csv-h" data-testid="tx-csv">
          <h2 id="tx-csv-h">Download</h2>
          <p className="hint">Every transaction of {periodWords(period)} as a CSV file, for your accountant or a spreadsheet, whatever the filters.</p>
          <a className="button" href={exportPath("log", period)} download={exportFileName("log", period)}>
            Download the period
          </a>
        </section>
      )}
    </>
  );
}

/** One transaction (GET /v1/money/entries/{id}): its amount on a hero, its
 * details, what it counts towards and each status it had. */
export function MoneyTransaction() {
  const { id = "" } = useParams();
  const { can } = useAuth();
  const [reveal, setReveal] = useState(false);
  const one = useLoad(() => get<MovementAnswer>(txEntryPath(id, reveal)), [id, reveal]);
  const a = one.data;
  const m = a?.entry;
  const hero = m ? txHero(m) : undefined;
  const who = a?.payer || m?.vendor || "";
  return (
    <>
      <p className="crumbs">
        <Link to="/money/transactions" aria-label="Back to Transactions">
          Transactions
        </Link>
      </p>
      <PageHeader title={m ? txTitle(m) : "Transaction"} />
      <ErrorBox error={one.error} />
      {!m && !one.error && <Skeleton rows={4} label="Loading the transaction…" />}
      {m && hero && (
        <>
          <section className="hero" aria-label={`${hero.label}: ${hero.amount}`} data-testid="tx-hero">
            <div className="hero-top">
              <span className="hero-label">{hero.label}</span>
              <span className="hero-tag">{hero.tag}</span>
            </div>
            <div className="hero-amount">{hero.amount}</div>
            <p className="hero-sub">{hero.sub}</p>
            <dl className="hero-foot">
              <div>
                <dt>When</dt>
                <dd className="hero-foot-words">{fmtTime(m.when)}</dd>
              </div>
              <div>
                <dt>Fee</dt>
                <dd className="hero-foot-words">{m.fee_kobo ? naira(m.fee_kobo) : "None"}</dd>
              </div>
            </dl>
          </section>
          {m.to_match && can("invoice.manage") && (
            <p>
              <Link className="button primary" to="/invoices/match">
                Match it to an invoice
              </Link>
            </p>
          )}
          <section className="card" aria-labelledby="tx-details-h" data-testid="tx-details">
            <h2 id="tx-details-h">Details</h2>
            <dl className="kv">
              <dt>What</dt>
              <dd>{kindWords(m.kind)}</dd>
              {who && (
                <>
                  <dt>{m.direction === "in" ? "From" : "To"}</dt>
                  <dd data-testid="tx-who">
                    {who}
                    {a?.payer_account ? ` · ${a.payer_account}` : ""}
                    {a?.payer && !a.revealed && can("pii.reveal") && (
                      <>
                        {" "}
                        <button type="button" className="link" onClick={() => setReveal(true)} title="Seeing who paid is recorded in the audit log">
                          Show who paid
                        </button>
                      </>
                    )}
                  </dd>
                </>
              )}
              <dt>Where</dt>
              <dd>{whereWords(m.channel) || "—"}</dd>
              {m.reference && (
                <>
                  <dt>Reference</dt>
                  <dd>{m.reference}</dd>
                </>
              )}
              {m.description && m.description !== txTitle(m) ? (
                <>
                  <dt>Description</dt>
                  <dd>{m.description}</dd>
                </>
              ) : null}
              {m.category && (
                <>
                  <dt>Label</dt>
                  <dd>{m.category}</dd>
                </>
              )}
              {m.note && (
                <>
                  <dt>Note</dt>
                  <dd>{m.note}</dd>
                </>
              )}
              {m.direction === "in" && !m.currency && m.left_kobo !== undefined && m.left_kobo > 0 && (
                <>
                  <dt>Not counted yet</dt>
                  <dd>{naira(m.left_kobo)}</dd>
                </>
              )}
              {m.currency && (
                <>
                  <dt>Currency</dt>
                  <dd>{m.currency}: not in your naira totals</dd>
                </>
              )}
              <dt>Heard</dt>
              <dd>
                {fmtTime(m.recorded_at)} · {sourceWords(m.source)}
              </dd>
            </dl>
          </section>
          <section className="card" aria-labelledby="tx-counts-h" data-testid="tx-counts">
            <h2 id="tx-counts-h">What it paid for</h2>
            {m.counts.length === 0 ? (
              <p className="hint">{m.direction === "in" ? "Not matched to an invoice or order yet." : "Nothing: money out is not matched."}</p>
            ) : (
              <Items label="What it paid for">
                {m.counts.map((c) => {
                  const to = countPath(c);
                  return to ? (
                    <Item key={c.id} to={to} icon={c.what === "invoice" ? "invoices" : "orders"} title={countTitle(c)} side={naira(c.amount_kobo)} testid="tx-count" />
                  ) : (
                    <Row key={c.id} icon="tag" title={countTitle(c)} side={naira(c.amount_kobo)} testid="tx-count" />
                  );
                })}
              </Items>
            )}
          </section>
          {m.history.length > 1 && (
            <section className="card" aria-labelledby="tx-history-h" data-testid="tx-history">
              <h2 id="tx-history-h">What happened</h2>
              <Items label="What happened">
                {m.history.map((h, i) => (
                  <Row
                    key={i}
                    icon="clock"
                    tone={h.status === "failed" ? "danger" : h.status === "pending" ? "info" : h.status === "reversed" ? "muted" : "ok"}
                    title={statusWord(h.status)}
                    side={h.amount_kobo !== undefined ? naira(h.amount_kobo) : undefined}
                    sub={<span className="sub">{fmtTime(h.at)}</span>}
                  />
                ))}
              </Items>
            </section>
          )}
        </>
      )}
    </>
  );
}
