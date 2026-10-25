import { useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, get, type Approval, type RunSummary, type WorkflowSummary } from "../api";
import { useAuth } from "../auth";
import { useOnboarding } from "../onboarding";
import { Icon } from "../icons";
import { CHECKLIST } from "../lib/onboarding";
import { moneyToday, ordersToday, runWords, triggerWords } from "../lib/home";
import type { Order } from "../lib/orders";
import { formatPrice } from "../lib/storefront";
import { greeting, shortWhen } from "../lib/time";
import { ErrorBox, Skeleton, useCountUp, useLiveRefresh, useLoad } from "../ui";
import type { FormRequest } from "./FormFill";
import { Decide } from "./Approvals";

const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
const none = <T,>(): Promise<T | null> => Promise.resolve(null);
// A 404 is a feature this Taskiem has not turned on (storefront orders).
const orNull = <T,>(p: Promise<T>) => p.catch((e: unknown) => (e instanceof ApiError && e.status === 404 ? null : Promise.reject(e)));

/** The owner's morning view: money and orders today, what needs them,
 * what happened, and what still runs for them. */
export function Home() {
  const { me, can } = useAuth();
  const onboarding = useOnboarding().data;
  const shop = can("storefront.manage");
  const runsOK = can("run.read");
  const orders = useLoad(() => (shop ? orNull(get<{ orders: Order[] }>("/v1/storefront/orders?limit=200")) : none<{ orders: Order[] }>()), [shop]);
  const runs = useLoad(() => (runsOK ? get<{ runs: RunSummary[] }>("/v1/runs?limit=6") : none<{ runs: RunSummary[] }>()), [runsOK]);
  const week = useLoad(
    () => (runsOK ? get<{ runs: { total: number; failed: number } }>(`/v1/dashboard?environment=prod&days=7&tz=${encodeURIComponent(tz)}`) : none<{ runs: { total: number; failed: number } }>()),
    [runsOK],
  );
  const decides = can("approval.decide") || can("workflow.publish");
  const approvals = useLoad(() => (decides ? get<{ approvals: Approval[] }>("/v1/approvals") : none<{ approvals: Approval[] }>()), [decides]);
  const toFill = useLoad(() => get<{ requests: FormRequest[] }>("/v1/form-requests").catch(() => ({ requests: [] })), []);
  const workflows = useLoad(() => (can("workflow.read") ? get<{ workflows: WorkflowSummary[] }>(`/v1/workflows?tz=${encodeURIComponent(tz)}`) : none<{ workflows: WorkflowSummary[] }>()), []);
  useLiveRefresh(runs.reload);
  useLiveRefresh(orders.reload);
  const [deciding, setDeciding] = useState<Approval | null>(null);

  const first = (me?.user?.name || "").split(" ")[0];
  const today = new Date().toLocaleDateString("en-GB", { weekday: "long", day: "numeric", month: "long" });
  const money = orders.data ? moneyToday(orders.data.orders) : null;
  const counts = orders.data ? ordersToday(orders.data.orders) : null;
  const waitingOrders = orders.data?.orders.filter((o) => o.status === "received" || o.status === "price_changed" || o.status === "refund_due") ?? [];
  const needs = (approvals.data?.approvals.length ?? 0) + (toFill.data?.requests.length ?? 0) + waitingOrders.length;
  const setup = onboarding && onboarding.self_serve && !onboarding.complete && !onboarding.dismissed ? onboarding : null;
  const live = workflows.data?.workflows.filter((w) => w.active_version) ?? [];

  return (
    <>
      <div className="greeting">
        <h1>
          {greeting()}
          {first ? `, ${first}` : ""}
        </h1>
        <p>
          {today}
          {me?.tenant_name ? ` · ${me.tenant_name}` : ""}
        </p>
      </div>

      <section className="stats" aria-label="At a glance">
        {money && (
          <Link className="stat" to="/storefront/orders">
            <span className="stat-label">Money in today</span>
            <span className="stat-value">
              <Count value={money.kobo} money />
            </span>
            <span className="stat-hint">{money.count === 1 ? "1 payment" : `${money.count} payments`}</span>
          </Link>
        )}
        {counts && (
          <Link className="stat" to="/storefront/orders">
            <span className="stat-label">Orders today</span>
            <span className="stat-value">
              <Count value={counts.count} />
            </span>
            <span className="stat-hint">{counts.waiting > 0 ? `${counts.waiting} waiting for you` : "None waiting for you"}</span>
          </Link>
        )}
        {week.data && (
          <Link className="stat" to="/runs">
            <span className="stat-label">Workflows ran this week</span>
            <span className="stat-value">
              <Count value={week.data.runs.total} />
            </span>
            <span className="stat-hint">{week.data.runs.failed > 0 ? `${week.data.runs.failed} did not finish` : week.data.runs.total > 0 ? "All went well" : "Nothing yet"}</span>
          </Link>
        )}
        {workflows.data && (
          <Link className="stat" to="/workflows">
            <span className="stat-label">Working for you</span>
            <span className="stat-value">
              <Count value={live.length} />
            </span>
            <span className="stat-hint">{live.length === 1 ? "workflow is live" : "workflows are live"}</span>
          </Link>
        )}
      </section>

      <div className="home-grid">
        <div className="home-col">
          <section className="panel" aria-labelledby="needs">
            <div className="panel-head">
              <h2 id="needs">Needs you</h2>
              {needs > 0 && <span className="pill-count">{needs}</span>}
            </div>
            <ErrorBox error={approvals.error} />
            {needs === 0 && <p className="panel-body hint">Nothing needs you right now.</p>}
            {approvals.data?.approvals.slice(0, 5).map((a) => (
              // The row opens Approvals; Approve decides here, in a confirm sheet.
              <div key={`${a.run_id}/${a.step_id}`} className="list-row has-action">
                <span className="list-icon">
                  <Icon name="approvals" />
                </span>
                <Link className="grow row-link" to="/approvals">
                  <span className="title">{a.step_name}</span>
                  <span className="sub">
                    {a.workflow} · asked {shortWhen(new Date(a.requested_at))}
                  </span>
                </Link>
                <button type="button" className="primary" aria-label={`Approve ${a.step_name}`} onClick={() => setDeciding(a)}>
                  Approve
                </button>
              </div>
            ))}
            {deciding && <Decide a={deciding} decision="approved" showSubject onClose={() => setDeciding(null)} onDone={() => (setDeciding(null), approvals.reload())} />}
            {toFill.data?.requests.slice(0, 3).map((f) => (
              <Link key={f.id} className="list-row" to={`/forms/requests/${f.id}`}>
                <span className="list-icon">
                  <Icon name="forms" />
                </span>
                <span className="grow">
                  <span className="title">{f.title}</span>
                  <span className="sub">To fill in · {shortWhen(new Date(f.created_at))}</span>
                </span>
                <span className="button">Fill in</span>
              </Link>
            ))}
            {waitingOrders.slice(0, 3).map((o) => (
              <Link key={o.id} className="list-row" to={`/storefront/orders/${o.reference}`}>
                <span className="list-icon">
                  <Icon name={o.status === "refund_due" ? "refund" : "orders"} />
                </span>
                <span className="grow">
                  <span className="title">
                    {o.status === "refund_due" ? "Refund" : "Order"} from {o.customer.name || o.customer.phone || "a customer"}
                  </span>
                  <span className="sub">
                    #{o.reference} · {formatPrice(o.total_kobo)} · {shortWhen(new Date(o.created_at))}
                  </span>
                </span>
                <span className="button">Open</span>
              </Link>
            ))}
          </section>

          {runsOK && (
            <section className="panel" aria-labelledby="activity">
              <div className="panel-head">
                <h2 id="activity">Activity</h2>
                <span className="grow" />
                <Link to="/runs">See all</Link>
              </div>
              <ErrorBox error={runs.error} />
              {!runs.data && !runs.error && <Skeleton rows={3} />}
              {runs.data?.runs.length === 0 && <p className="panel-body hint">Nothing has run yet. Publish a workflow and its runs show here.</p>}
              {runs.data?.runs.map((r) => (
                <Link key={r.id} className="list-row" to={`/runs/${r.id}`}>
                  <span className={`dot ${r.status}`} aria-hidden="true" />
                  <span className="grow">
                    <span className="title" style={{ fontWeight: 400 }}>
                      {r.workflow} {runWords(r.status)}
                    </span>
                  </span>
                  <span className="when">{shortWhen(new Date(r.started_at))}</span>
                </Link>
              ))}
            </section>
          )}
        </div>

        <div className="home-col">
          {setup && (
            <section className="panel" aria-labelledby="setup">
              <div className="panel-head">
                <h2 id="setup">Finish setting up</h2>
                <span className="grow" />
                <span className="hint">
                  {setup.done} of {setup.total}
                </span>
              </div>
              <div className="panel-body">
                <div className="progress" role="progressbar" aria-label="Set-up progress" aria-valuemin={0} aria-valuemax={setup.total} aria-valuenow={setup.done}>
                  <span style={{ width: `${(100 * setup.done) / Math.max(1, setup.total)}%` }} />
                </div>
                <div style={{ marginTop: 10 }}>
                  {setup.items
                    .filter((it) => !it.done)
                    .slice(0, 3)
                    .map((it) => (
                      <Link key={it.id} className="setup-item" to={CHECKLIST[it.id].href}>
                        <span className="ring" aria-hidden="true" />
                        {CHECKLIST[it.id].title}
                        <span className="go" aria-hidden="true">
                          →
                        </span>
                      </Link>
                    ))}
                </div>
                <Link to="/start" className="panel-more">
                  See every step
                </Link>
              </div>
            </section>
          )}

          <section className="panel" aria-labelledby="running">
            <div className="panel-head">
              <h2 id="running">Running for you</h2>
              <span className="grow" />
              <Link to="/workflows">All</Link>
            </div>
            {workflows.data && live.length === 0 && (
              <p className="panel-body hint">
                Nothing yet. <Link to="/templates">Pick a template</Link> to get started.
              </p>
            )}
            {live.slice(0, 5).map((w) => {
              const n = w.recent?.reduce((a, b) => a + b, 0) ?? 0;
              return (
                <Link key={w.id} className="list-row" to={`/workflows/${w.id}`}>
                  <span className="grow">
                    <span className="title">{w.name}</span>
                    <span className="sub">
                      {triggerWords(w.trigger)} · {n === 0 ? "no runs in 2 weeks" : n === 1 ? "ran once in 2 weeks" : `ran ${n} times in 2 weeks`}
                    </span>
                  </span>
                </Link>
              );
            })}
          </section>
        </div>
      </div>
    </>
  );
}

/** A figure on Home: counts up when the page opens. */
function Count({ value, money }: { value: number; money?: boolean }) {
  const n = useCountUp(value);
  return <>{money ? formatPrice(n) : n}</>;
}
