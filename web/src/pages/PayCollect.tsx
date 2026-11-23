// Pay several people from your list (collecting from several messages,
// P3; docs/spec/chat-pay-people.md, "Pay several people", From messages):
// the rows added from messages with "Add to the list", kept by the server
// across chats (/v1/pay-collection). Each row says the message it came
// from, with a link back to its chat; its amount and what it is for can be
// changed (PATCH), and it can be removed. Check makes the list a batch
// (POST /v1/pay-collection/batch) and goes on to the batch's page, where it
// is confirmed with one passkey as any list is.

import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { ApiError, del, get, patch, post } from "../api";
import { useAuth } from "../auth";
import { naira } from "../lib/invoices";
import { amountTyped } from "../lib/paymessage";
import { peopleWords, type PaymentBatch } from "../lib/batches";
import {
  amountProblem,
  chatNames,
  chatPath,
  footWords,
  listBlocked,
  rowAccount,
  rowWho,
  sourceWords,
  typedKobo,
  type CollectedRow,
  type Collection,
} from "../lib/paycollect";
import { EmptyState, ErrorBox, Field, Items, PageHeader, Row, Skeleton, useLoad } from "../ui";
import { toast } from "../toast";

/** "What for", as a payment's purpose and in words. */
const PURPOSES: [string, string][] = [
  ["bill", "A bill"],
  ["wages", "Wages"],
  ["delivery", "A delivery"],
  ["refund", "A refund"],
  ["stock", "Stock bought"],
  ["other", "Other"],
];

/** The list's count, for Pay several's toolbar ("From your list (3)"). */
export function useCollectionCount(): Collection | undefined {
  const { can } = useAuth();
  const on = can("money.pay");
  const list = useLoad(
    () => (on ? get<Collection>("/v1/pay-collection").catch(() => undefined) : Promise.resolve(undefined)),
    [on],
  );
  return list.data;
}

/** /payments/several/list: the person's list from messages. */
export function CollectedListPage() {
  const navigate = useNavigate();
  const first = useLoad(() => get<Collection>("/v1/pay-collection"), []);
  const [list, setList] = useState<Collection>();
  useEffect(() => {
    if (first.data) setList(first.data);
  }, [first.data]);
  // Amounts as typed, by row, until saved.
  const [typed, setTyped] = useState<Record<string, string>>({});
  const [tried, setTried] = useState(false);
  const [title, setTitle] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const names = useMemo(() => chatNames(), []);

  const amountOf = (x: CollectedRow) => typed[x.id] ?? amountTyped(x.amount_kobo ?? undefined);
  const call = async (fn: () => Promise<Collection>) => {
    setError(undefined);
    try {
      const got = await fn();
      setList(got);
      return got;
    } catch (e) {
      setError(e);
      return undefined;
    }
  };
  /** Saves a row's amount as typed, when it is one and has changed. */
  const saveAmount = async (x: CollectedRow): Promise<Collection | undefined> => {
    const t = typed[x.id];
    if (t === undefined) return list;
    const k = typedKobo(t);
    if (k === undefined || k === x.amount_kobo) return list;
    const got = await call(() => patch<Collection>(`/v1/pay-collection/${encodeURIComponent(x.id)}`, { amount_kobo: k }));
    if (got)
      setTyped((cur) => Object.fromEntries(Object.entries(cur).filter(([k]) => k !== x.id)));
    return got;
  };
  // What it is for; an amount typed and not saved yet goes with it.
  const setPurpose = (x: CollectedRow, purpose: string) => {
    const t = typed[x.id];
    const k = t === undefined ? undefined : typedKobo(t);
    void call(() =>
      patch<Collection>(`/v1/pay-collection/${encodeURIComponent(x.id)}`, k !== undefined && k !== x.amount_kobo ? { purpose, amount_kobo: k } : { purpose }),
    );
  };
  const remove = (x: CollectedRow) =>
    void call(() => del<Collection>(`/v1/pay-collection/${encodeURIComponent(x.id)}`));
  const clear = () => {
    if (!list || !window.confirm(`Clear the list? ${list.count === 1 ? "1 person comes" : `${list.count} people come`} off it, and nobody is paid.`))
      return;
    void call(() => del<Collection>("/v1/pay-collection")).then((got) => got && toast("The list is cleared", "success"));
  };

  const check = async () => {
    setTried(true);
    if (!list || busy) return;
    setBusy(true);
    try {
      // What is typed and not saved yet is saved first.
      let now: Collection | undefined = list;
      for (const x of list.rows) {
        const t = typed[x.id];
        if (t === undefined) continue;
        if (amountProblem(t)) return;
        const got = await saveAmount(x);
        if (!got) return;
        // An amount unchanged answers the list as it was first: keep the
        // newest one an earlier row's save answered.
        if (got !== list) now = got;
      }
      if (!now || listBlocked(now)) return;
      setError(undefined);
      const r = await post<{ batch: PaymentBatch }>("/v1/pay-collection/batch", title.trim() ? { title: title.trim() } : {});
      navigate(`/payments/batches/${encodeURIComponent(r.batch.id)}`, {
        state: { held: r.batch.held ?? [], collected: true },
      });
    } catch (e) {
      // The list changed elsewhere (another chat, another device): read it again.
      if (e instanceof ApiError) first.reload();
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  const crumbs = (
    <p className="crumbs">
      <Link to="/payments/several" aria-label="Back to Pay several people">
        Pay several people
      </Link>
    </p>
  );
  if (!list)
    return (
      <>
        {crumbs}
        <PageHeader title="Your list to pay" />
        <ErrorBox error={first.error} />
        {!first.error && <Skeleton rows={3} label="Loading your list…" />}
      </>
    );
  const w = footWords(list);
  const blocked = listBlocked(list);
  const rowProblems = list.rows.map((x) => {
    const t = amountOf(x);
    return t || tried ? amountProblem(t) : null;
  });
  return (
    <>
      {crumbs}
      <PageHeader
        title="Your list to pay"
        description="The people you added from messages with Add to the list, in any chat. Each person is checked with the bank; you confirm them all with one passkey."
      />
      <section className="hero" aria-label="Your list" data-testid="collect-hero">
        <div className="hero-top">
          <span className="hero-label">To pay</span>
          <span className="hero-tag">{list.in_batch ? "Waiting to be sent" : "Not sent"}</span>
        </div>
        <div className="hero-amount" data-testid="collect-total">
          {naira(list.total_kobo)}
        </div>
        <p className="hero-sub" data-testid="collect-sub">
          {w.needs ? `${peopleWords(list.count)} · ${w.needs}` : peopleWords(list.count)}
        </p>
      </section>
      {list.in_batch && (
        <div className="notice" role="status" data-testid="collect-in-batch">
          Some of your list waits to be sent.{" "}
          <Link to={`/payments/batches/${encodeURIComponent(list.in_batch)}`}>Open it</Link> to send it or cancel it.
        </div>
      )}
      <section className="card" aria-labelledby="collect-who-h" data-testid="collect-list">
        <h2 id="collect-who-h">Who to pay</h2>
        {list.count === 0 ? (
          <EmptyState icon="members">
            Nobody on your list. Under a message with an account number, tap Add to the list.
          </EmptyState>
        ) : (
          <>
            <Field label="Name this list (optional)" hint="Such as Suppliers, Friday. It helps you find it next time.">
              <input value={title} maxLength={80} autoComplete="off" onChange={(e) => setTitle(e.target.value)} data-testid="collect-title" />
            </Field>
            <Items label="People to pay">
              {list.rows.map((x, i) => {
                const who = rowWho(x);
                const path = chatPath(x.source);
                const from = sourceWords(x.source, names);
                const problem = rowProblems[i];
                return (
                  <Row
                    key={x.id}
                    letter={who}
                    tone={problem ? "warn" : undefined}
                    title={who}
                    side={x.amount_kobo ? <span className="item-figure">{naira(x.amount_kobo)}</span> : undefined}
                    testid="collect-row"
                    attrs={problem ? { "data-problem": "yes" } : undefined}
                    sub={
                      <span className="sub">
                        <span className="block">{rowAccount(x)}</span>
                        <span className="collect-from" data-testid="collect-from">
                          {path ? <Link to={path}>{from}</Link> : from}
                        </span>
                        {problem && (
                          <span className="block pay-warn warn batch-problem" data-testid="collect-problem">
                            {problem}
                          </span>
                        )}
                        <span className="batch-fields">
                          <Field label="How much (₦)">
                            <input
                              inputMode="decimal"
                              autoComplete="off"
                              value={amountOf(x)}
                              onChange={(e) => setTyped((cur) => ({ ...cur, [x.id]: e.target.value }))}
                              onBlur={() => void saveAmount(x)}
                              data-testid="collect-amount"
                            />
                          </Field>
                          <Field label="What for">
                            <select value={x.purpose} onChange={(e) => setPurpose(x, e.target.value)} data-testid="collect-for">
                              <option value="">Choose</option>
                              {PURPOSES.map(([k, words]) => (
                                <option key={k} value={k}>
                                  {words}
                                </option>
                              ))}
                            </select>
                          </Field>
                        </span>
                      </span>
                    }
                  >
                    <button type="button" className="small" onClick={() => remove(x)} data-testid="collect-remove">
                      Remove<span className="sr-only"> {who}</span>
                    </button>
                  </Row>
                );
              })}
            </Items>
          </>
        )}
      </section>
      <section className="card batch-foot" aria-label="The list's total" data-testid="collect-foot">
        <p className="batch-foot-sum">
          <strong data-testid="collect-foot-total">{w.main}</strong>
          <span className="hint">Nothing is paid until you confirm. Without a choice, what it is for is Other.</span>
        </p>
        {tried && blocked && (
          <p className="error" role="alert" data-testid="collect-blocked">
            {blocked}
          </p>
        )}
        <ErrorBox error={error} />
        <div className="toolbar">
          <button
            type="button"
            className="primary"
            onClick={() => void check()}
            disabled={busy || list.count === 0 || !!list.in_batch}
            data-testid="collect-check"
          >
            {busy ? "Checking with the bank…" : "Check the list"}
          </button>
          {list.count > 0 && (
            <button type="button" className="danger" onClick={clear} disabled={busy} data-testid="collect-clear">
              Clear the list
            </button>
          )}
        </div>
      </section>
    </>
  );
}
