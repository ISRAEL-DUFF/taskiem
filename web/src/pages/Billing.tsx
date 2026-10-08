import { useState } from "react";
import { ApiError, get, post } from "../api";
import { ErrorBox, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import {
  blockerLines,
  featureNames,
  isUpgrade,
  meters,
  naira,
  statusText,
  tiers,
  type Banner,
  type BillingOverview,
  type Plan,
} from "../lib/billing";

interface Checkout {
  checkout_url?: string;
  paid: boolean;
}

interface Change {
  effective: "now" | "on_payment" | "period_end";
  checkout_url?: string;
}

const day = (s?: string) => (s ? new Date(s).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" }) : "—");

/** Settings > Billing: plan, usage, invoices and payment (docs/billing.md). */
export function Billing() {
  const { data, error, reload } = useLoad(() => get<BillingOverview>("/v1/billing"), []);
  const [interval, setInterval] = useState<"monthly" | "annual">("monthly");
  const [blockers, setBlockers] = useState<string[]>([]);
  const [note, setNote] = useState("");
  const act = useAction();
  if (!data) return error ? <ErrorBox error={error} /> : <Skeleton />;
  const sub = data.subscription;

  const go = (url?: string) => {
    if (url) window.location.assign(url);
  };
  const choose = (p: Plan) =>
    act.run(async () => {
      setBlockers([]);
      setNote("");
      try {
        if (sub && (sub.status === "trial" || sub.status === "cancelled")) {
          // From a trial or after cancelling, choosing a plan is paying for it.
          const r = await post<Checkout>("/v1/billing/checkout", { plan: p.id, interval });
          if (r.paid) reload();
          else go(r.checkout_url);
          return;
        }
        const r = await post<Change>("/v1/billing/plan", { plan: p.id, interval });
        if (r.effective === "on_payment") go(r.checkout_url);
        else {
          setNote(r.effective === "period_end" ? `The ${p.name} plan starts at the end of this period (${day(sub?.period_end)}).` : `You are now on ${p.name}.`);
          reload();
        }
      } catch (e) {
        if (e instanceof ApiError && e.body.code === "downgrade_blocked") {
          setBlockers(blockerLines(e.body));
          return;
        }
        throw e;
      }
    });
  const pay = () =>
    act.run(async () => {
      const r = await post<Checkout>("/v1/billing/checkout", data.open_invoice ? { invoice: data.open_invoice.id } : {});
      if (r.paid) reload();
      else go(r.checkout_url);
    });

  return (
    <>
      <PageHeader title="Billing" description="Your plan, what you have used this period, and your invoices." />
      {data.banner && <BillingBannerView banner={data.banner} />}
      <ErrorBox error={act.error} />
      {note && <p className="hint">{note}</p>}
      {blockers.length > 0 && (
        <div className="error" role="alert" data-testid="blockers">
          This plan is smaller than what you use. First:
          <ul>
            {blockers.map((b) => (
              <li key={b}>{b}</li>
            ))}
          </ul>
        </div>
      )}

      <section className="card">
        <h2 style={{ marginTop: 0 }}>Your plan: {data.plan.name}</h2>
        {!data.enabled ? (
          <p className="hint">Billing is off on this deployment: every limit is your operator&apos;s and every feature is on.</p>
        ) : data.inherited ? (
          <p className="hint">Your organisation runs on its partner&apos;s plan.</p>
        ) : sub ? (
          <dl className="kv">
            <dt>Status</dt>
            <dd data-testid="billing-status">{statusText[sub.status]}</dd>
            {sub.status === "trial" && (
              <>
                <dt>Trial ends</dt>
                <dd>{day(sub.trial_end)}</dd>
              </>
            )}
            {sub.status !== "trial" && (
              <>
                <dt>Period</dt>
                <dd>
                  {day(sub.period_start)} to {day(sub.period_end)} ({sub.interval})
                </dd>
              </>
            )}
            {data.pending_plan && (
              <>
                <dt>From {day(sub.period_end)}</dt>
                <dd>{data.pending_plan.name}</dd>
              </>
            )}
            {sub.cancel_at_period_end && (
              <>
                <dt>Ends</dt>
                <dd>
                  {day(sub.period_end)}{" "}
                  <button disabled={act.busy} onClick={() => void act.run(async () => (await post("/v1/billing/cancel", { resume: true }), reload()))}>
                    Keep my subscription
                  </button>
                </dd>
              </>
            )}
            {sub.credit_kobo > 0 && (
              <>
                <dt>Credit</dt>
                <dd>{naira(sub.credit_kobo)}</dd>
              </>
            )}
            <dt>Payment method</dt>
            <dd>{data.payment_method ? `${data.payment_method.brand} ending ${data.payment_method.last4} (expires ${data.payment_method.exp})` : "none saved: you pay each invoice"}</dd>
          </dl>
        ) : null}
        {data.open_invoice && (
          <p>
            Invoice {data.open_invoice.number} for {naira(data.open_invoice.total_kobo)} is due {day(data.open_invoice.due_at)}.{" "}
            <button className="primary" disabled={act.busy} onClick={() => void pay()}>
              Pay now
            </button>
          </p>
        )}
      </section>

      <section className="card">
        <h2 style={{ marginTop: 0 }}>Usage</h2>
        <table>
          <tbody>
            {meters(data.limits, data.usage).map((m) => (
              <tr key={m.key}>
                <td>{m.label}</td>
                <td style={{ width: "40%" }}>
                  {m.fraction !== null && (
                    <div className="meter" role="meter" aria-label={m.label} aria-valuenow={m.used} aria-valuemin={0} aria-valuemax={m.limit}>
                      <div className={`meter-fill ${m.level}`} style={{ width: `${Math.round(m.fraction * 100)}%` }} />
                    </div>
                  )}
                </td>
                <td className="mono">
                  {m.used.toLocaleString()} {m.limit > 0 ? `of ${m.limit.toLocaleString()}` : "(no limit)"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {(data.limits.max_retention_days ?? 0) > 0 && <p className="hint">Run history is kept for {data.limits.max_retention_days} days after a run ends.</p>}
        {data.history.length > 0 && <History rows={data.history} title="Last days" />}
        {data.subtenant_usage && data.subtenant_usage.length > 0 && <History rows={data.subtenant_usage} title="Your customers (sub-tenants), summed" />}
      </section>

      {data.enabled && !data.inherited && sub && sub.status !== "comped" && (
        <section className="card">
          <div className="toolbar">
            <h2 className="grow" style={{ margin: 0 }}>
              Plans
            </h2>
            <label>
              Billed{" "}
              <select aria-label="Billing interval" value={interval} onChange={(e) => setInterval(e.target.value as "monthly" | "annual")}>
                <option value="monthly">monthly</option>
                <option value="annual">annually</option>
              </select>
            </label>
          </div>
          {data.placeholder_prices && <p className="hint">Prices shown are placeholders while pricing is finalised.</p>}
          <div className="plan-cards">
            {data.plans.map((p) => {
              const current = p.id === sub.plan && interval === sub.interval;
              const up = isUpgrade(tiers, data.plan, sub.interval, p, interval);
              return (
                <div key={p.id} className={`card plan-card${current ? " current" : ""}`} data-testid={`plan-${p.id}`}>
                  <h3 style={{ marginTop: 0 }}>{p.name}</h3>
                  <p className="price">
                    {naira(interval === "annual" ? p.annual_kobo : p.monthly_kobo)}
                    <span className="hint"> / {interval === "annual" ? "year" : "month"} + VAT {data.vat_percent}%</span>
                  </p>
                  <ul className="hint">
                    {p.limits.max_running_runs !== undefined && <li>{p.limits.max_running_runs || "Unlimited"} runs at once</li>}
                    {p.limits.max_workflows !== undefined && <li>{p.limits.max_workflows || "Unlimited"} workflows</li>}
                    {p.limits.max_retention_days !== undefined && <li>{p.limits.max_retention_days} days of history</li>}
                    {p.limits.whatsapp_templates_monthly !== undefined && <li>{p.limits.whatsapp_templates_monthly.toLocaleString()} WhatsApp templates a month</li>}
                    {Object.entries(p.features)
                      .filter(([, on]) => on)
                      .map(([f]) => (
                        <li key={f}>{featureNames[f] ?? f}</li>
                      ))}
                  </ul>
                  {current ? (
                    <strong>Current plan</strong>
                  ) : (
                    <button className={up ? "primary" : ""} disabled={act.busy} onClick={() => void choose(p)}>
                      {sub.status === "trial" || sub.status === "cancelled" ? `Choose ${p.name}` : up ? `Upgrade to ${p.name}` : `Move to ${p.name} at period end`}
                    </button>
                  )}
                </div>
              );
            })}
          </div>
          {(sub.status === "active" || sub.status === "trial") && !sub.cancel_at_period_end && (
            <p>
              <button
                disabled={act.busy}
                onClick={() => {
                  if (window.confirm(`Cancel at the end of the period (${day(sub.status === "trial" ? sub.trial_end : sub.period_end)})? New runs stop then; your workflows and history are kept.`))
                    void act.run(async () => (await post("/v1/billing/cancel", {}), reload()));
                }}
              >
                Cancel subscription
              </button>
            </p>
          )}
        </section>
      )}

      {data.invoices.length > 0 && (
        <section className="card">
          <h2 style={{ marginTop: 0 }}>Invoices</h2>
          <table>
            <thead>
              <tr>
                <th>Number</th>
                <th>Issued</th>
                <th>Period</th>
                <th>Total (VAT incl.)</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody>
              {data.invoices.map((i) => (
                <tr key={i.id}>
                  <td>
                    <a href={`/v1/billing/invoices/${i.id}?format=html`} target="_blank" rel="noreferrer">
                      {i.number}
                    </a>
                  </td>
                  <td>{fmtTime(i.issued_at)}</td>
                  <td>
                    {day(i.period_start)} to {day(i.period_end)}
                  </td>
                  <td>{naira(i.total_kobo)}</td>
                  <td>
                    <span className={`badge ${i.status === "paid" ? "ok" : i.status === "open" ? "open" : "skipped"}`}>{i.status}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}
    </>
  );
}

function History({ rows, title }: { rows: BillingOverview["history"]; title: string }) {
  return (
    <>
      <h3>{title}</h3>
      <table>
        <thead>
          <tr>
            <th>Day</th>
            <th>Runs</th>
            <th>Steps</th>
            <th>Running</th>
            <th>WhatsApp templates (month)</th>
            <th>AI tokens (month)</th>
          </tr>
        </thead>
        <tbody>
          {rows.slice(-14).map((r) => (
            <tr key={r.day}>
              <td>{r.day}</td>
              <td>{r.runs_started.toLocaleString()}</td>
              <td>{r.steps.toLocaleString()}</td>
              <td>{r.running_runs}</td>
              <td>
                {r.whatsapp_templates.toLocaleString()}
                {r.whatsapp_overage > 0 ? ` (${r.whatsapp_overage.toLocaleString()} beyond the allowance)` : ""}
              </td>
              <td>{r.ai_tokens.toLocaleString()}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}

export function BillingBannerView({ banner, link = false }: { banner: Banner; link?: boolean }) {
  return (
    <div className={banner.level === "error" ? "error" : "warning"} role="status" data-testid="billing-banner">
      {banner.message} {link && <a href="/settings/billing">Billing</a>}
    </div>
  );
}

interface BillingStatus {
  enabled: boolean;
  banner?: Banner;
}

/** The past-due banner for every member, at the top of every page. */
export function BillingBanner() {
  const { data } = useLoad(() => get<BillingStatus>("/v1/billing/status").catch(() => ({ enabled: false }) as BillingStatus), []);
  if (!data?.banner) return null;
  return <BillingBannerView banner={data.banner} link />;
}
