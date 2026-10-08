// Operator console pages besides the review queue: publishers, reviewers,
// the status page, tenants (read-only) and the platform audit chain.

import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { actorLabel, impacts, isClosed, statusesFor, toRFC3339, updateTarget } from "../lib/ops";
import { Badge, ErrorBox, Field, fmtTime, Json, useAction, useLoad } from "../ui";
import { opsGet, withStepUp, type AuditEntry, type Incident, type OpsMe, type Publisher, type TenantRow } from "./client";

export function Publishers() {
  const { data, error, reload } = useLoad(() => opsGet<{ publishers: Publisher[] }>("/catalogue/publishers"), []);
  const [notes, setNotes] = useState<Record<string, string>>({});
  const act = useAction();
  const run = (slug: string, action: "verify" | "suspend" | "reinstate") =>
    act.run(async () => {
      await withStepUp(`ops.publisher.${action}`, slug, `/catalogue/publishers/${slug}/${action}`, { note: notes[slug] ?? "" });
      reload();
    });
  return (
    <>
      <h1>Publishers</h1>
      <p className="hint">
        Verify a namespace once you know who the publisher is and the contact answers. Suspending stops every version from loading at once; the
        publisher is told your note.
      </p>
      <ErrorBox error={error ?? act.error} />
      {data && data.publishers.length === 0 && <div className="empty">No publisher has asked for a namespace.</div>}
      {data && data.publishers.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Namespace</th>
              <th>Name</th>
              <th>Status</th>
              <th>Requested</th>
              <th>Note</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {data.publishers.map((p) => (
              <tr key={p.slug}>
                <td>
                  <code>{p.slug}</code>
                  <div className="hint">tenant {p.tenant_id}</div>
                </td>
                <td>{p.name}</td>
                <td>
                  <Badge value={p.status} />
                  {p.verified_by && <div className="hint">by {actorLabel(p.verified_by)}</div>}
                </td>
                <td>{fmtTime(p.requested_at)}</td>
                <td>
                  <input
                    aria-label={`Note for ${p.slug}`}
                    placeholder={p.status_note ?? "Note (shown to the publisher)"}
                    value={notes[p.slug] ?? ""}
                    onChange={(e) => setNotes({ ...notes, [p.slug]: e.target.value })}
                  />
                </td>
                <td style={{ whiteSpace: "nowrap" }}>
                  {p.status === "pending" && (
                    <button disabled={act.busy} onClick={() => void run(p.slug, "verify")}>
                      Verify
                    </button>
                  )}
                  {p.status === "verified" && (
                    <button className="danger" disabled={act.busy || !(notes[p.slug] ?? "").trim()} onClick={() => void run(p.slug, "suspend")}>
                      Suspend
                    </button>
                  )}
                  {p.status === "suspended" && (
                    <button disabled={act.busy} onClick={() => void run(p.slug, "reinstate")}>
                      Reinstate
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}

export function Reviewers({ me }: { me: OpsMe }) {
  const { data, error } = useLoad(() => opsGet<{ reviewers: { email: string; added_by: string; added_at: string }[] }>("/catalogue/reviewers"), []);
  return (
    <>
      <h1>Reviewers</h1>
      <p className="hint">
        Operators who may review catalogue submissions. {me.reviewer ? "You are on the list." : "You are not on the list."} The list changes from the
        operator CLI only: <code>taskiem catalogue reviewers add|remove EMAIL</code>.
      </p>
      <ErrorBox error={error} />
      {data && (
        <table>
          <thead>
            <tr>
              <th>Reviewer</th>
              <th>Added by</th>
              <th>Added</th>
            </tr>
          </thead>
          <tbody>
            {data.reviewers.map((r) => (
              <tr key={r.email}>
                <td>{r.email}</td>
                <td>{r.added_by}</td>
                <td>{fmtTime(r.added_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}

interface Component {
  ID: string;
  Name: string;
}

export function Status() {
  const { data, error, reload } = useLoad(() => opsGet<{ incidents: Incident[]; components: Component[] }>("/status/incidents"), []);
  const [kind, setKind] = useState("incident");
  const [title, setTitle] = useState("");
  const [message, setMessage] = useState("");
  const [impact, setImpact] = useState("degraded");
  const [components, setComponents] = useState<string[]>([]);
  const [starts, setStarts] = useState("");
  const [ends, setEnds] = useState("");
  const act = useAction();
  const open = () =>
    act.run(async () => {
      await withStepUp("ops.status.open", kind, "/status/incidents", {
        kind,
        title,
        message,
        components,
        ...(kind === "incident" ? { impact } : { starts_at: toRFC3339(starts), ends_at: toRFC3339(ends) }),
      });
      setTitle("");
      setMessage("");
      reload();
    });
  return (
    <>
      <h1>Status page</h1>
      <p className="hint">
        What customers see at <a href="/status">/status</a>. Operators' names stay here, never on the page.
      </p>
      <ErrorBox error={error} />
      <div className="card">
        <h2>Declare</h2>
        <div className="row">
          <Field label="Kind">
            <select value={kind} onChange={(e) => setKind(e.target.value)}>
              <option value="incident">Incident</option>
              <option value="maintenance">Maintenance</option>
            </select>
          </Field>
          <Field label="Title">
            <input value={title} onChange={(e) => setTitle(e.target.value)} maxLength={200} />
          </Field>
          {kind === "incident" ? (
            <Field label="Impact">
              <select value={impact} onChange={(e) => setImpact(e.target.value)}>
                {impacts.map((i) => (
                  <option key={i} value={i}>
                    {i.replaceAll("_", " ")}
                  </option>
                ))}
              </select>
            </Field>
          ) : (
            <>
              <Field label="Starts">
                <input type="datetime-local" value={starts} onChange={(e) => setStarts(e.target.value)} />
              </Field>
              <Field label="Ends">
                <input type="datetime-local" value={ends} onChange={(e) => setEnds(e.target.value)} />
              </Field>
            </>
          )}
        </div>
        <fieldset style={{ border: "none", padding: 0 }}>
          <legend className="hint">Components</legend>
          {(data?.components ?? []).map((c) => (
            <label key={c.ID} className="inline" style={{ marginRight: 16 }}>
              <input
                type="checkbox"
                checked={components.includes(c.ID)}
                onChange={(e) => setComponents(e.target.checked ? [...components, c.ID] : components.filter((x) => x !== c.ID))}
              />
              {c.Name}
            </label>
          ))}
        </fieldset>
        <Field label="Message">
          <textarea rows={3} value={message} onChange={(e) => setMessage(e.target.value)} />
        </Field>
        <ErrorBox error={act.error} />
        <button className="primary" disabled={act.busy || !title.trim() || !message.trim() || components.length === 0} onClick={() => void open()}>
          {kind === "incident" ? "Open incident" : "Schedule maintenance"}
        </button>
      </div>
      <h2>Last 90 days</h2>
      {data && data.incidents.length === 0 && <div className="empty">No incidents or maintenance.</div>}
      {data?.incidents.map((i) => <IncidentCard key={i.id} incident={i} onChange={reload} />)}
    </>
  );
}

function IncidentCard({ incident: i, onChange }: { incident: Incident; onChange: () => void }) {
  const [status, setStatus] = useState(i.status);
  const [message, setMessage] = useState("");
  const act = useAction();
  const post = () =>
    act.run(async () => {
      await withStepUp("ops.status.update", updateTarget(i.id, status), `/status/incidents/${i.id}/updates`, { status, message });
      setMessage("");
      onChange();
    });
  return (
    <div className="card" aria-label={i.title}>
      <h3 style={{ marginTop: 0 }}>
        {i.title} <Badge value={i.status} /> <span className="hint">{i.kind}</span>
      </h3>
      <p className="hint">
        {i.components.join(", ")} · opened {fmtTime(i.created_at)}
        {i.starts_at && ` · window ${fmtTime(i.starts_at)} to ${fmtTime(i.ends_at)}`}
      </p>
      <ul>
        {i.updates.map((u, n) => (
          <li key={n}>
            <strong>{u.status}</strong> ({u.impact.replaceAll("_", " ")}) {fmtTime(u.at)}: {u.message} <span className="hint">{u.actor && actorLabel(u.actor)}</span>
          </li>
        ))}
      </ul>
      {!i.closed && !isClosed(i.kind, i.status) && (
        <div className="row">
          <Field label="New status">
            <select value={status} onChange={(e) => setStatus(e.target.value)}>
              {statusesFor(i.kind).map((s) => (
                <option key={s} value={s}>
                  {s.replaceAll("_", " ")}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Update">
            <input value={message} onChange={(e) => setMessage(e.target.value)} />
          </Field>
          <div>
            <ErrorBox error={act.error} />
            <button disabled={act.busy || !message.trim()} onClick={() => void post()}>
              Post update
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

export function Tenants() {
  const [q, setQ] = useState("");
  const [query, setQuery] = useState("");
  const { data, error } = useLoad(() => opsGet<{ tenants: TenantRow[] }>(`/tenants?q=${encodeURIComponent(query)}`), [query]);
  return (
    <>
      <h1>Tenants</h1>
      <p className="hint">Read-only: plan, subscription state, limits and worker pool. Changes stay in the CLI (taskiem tenants, billing, pools).</p>
      <form
        className="toolbar"
        onSubmit={(e) => {
          e.preventDefault();
          setQuery(q.trim());
        }}
      >
        <input aria-label="Search tenants" placeholder="Name or id" value={q} onChange={(e) => setQ(e.target.value)} />
        <button type="submit">Search</button>
      </form>
      <ErrorBox error={error} />
      {data && (
        <table>
          <thead>
            <tr>
              <th>Tenant</th>
              <th>Status</th>
              <th>Plan</th>
              <th>Subscription</th>
              <th>Pool</th>
              <th>Created</th>
            </tr>
          </thead>
          <tbody>
            {data.tenants.map((t) => (
              <tr key={t.id}>
                <td>
                  <Link to={`/ops/tenants/${t.id}`}>{t.name}</Link>
                  <div className="hint">
                    {t.id}
                    {t.parent_id ? ` · sub-tenant of ${t.parent_id}` : ""}
                  </div>
                </td>
                <td>
                  <Badge value={t.status} />
                </td>
                <td>{t.plan ?? "—"}</td>
                <td>{t.subscription_status ?? "—"}</td>
                <td>{t.pool}</td>
                <td>{fmtTime(t.created_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}

export function Tenant() {
  const { id = "" } = useParams();
  const { data, error } = useLoad(
    () => opsGet<{ tenant: TenantRow; subscription: Record<string, unknown> | null; limits: { limits: Record<string, unknown>; overrides: Record<string, unknown>; usage: Record<string, unknown> } }>(`/tenants/${id}`),
    [id],
  );
  if (error) return <ErrorBox error={error} />;
  if (!data) return <div className="empty">Loading…</div>;
  const overrides = data.limits.overrides ?? {};
  return (
    <>
      <p className="hint">
        <Link to="/ops/tenants">Tenants</Link>
      </p>
      <h1>{data.tenant.name}</h1>
      <dl className="kv">
        <dt>Id</dt>
        <dd>
          <code>{data.tenant.id}</code>
        </dd>
        <dt>Status</dt>
        <dd>{data.tenant.status}</dd>
        <dt>Worker pool</dt>
        <dd>{data.tenant.pool}</dd>
      </dl>
      <h2>Subscription</h2>
      {data.subscription ? <Json value={data.subscription} /> : <p className="hint">No subscription (billing off, or never set up).</p>}
      <h2>Limits</h2>
      <table>
        <thead>
          <tr>
            <th>Limit</th>
            <th>Value</th>
            <th>From</th>
          </tr>
        </thead>
        <tbody>
          {Object.entries(data.limits.limits).map(([k, v]) => (
            <tr key={k}>
              <td>{k}</td>
              <td>{String(v)}</td>
              <td>{k in overrides ? "tenant" : "default"}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <h2>Usage</h2>
      <Json value={data.limits.usage} />
    </>
  );
}

export function Audit() {
  const { data, error } = useLoad(() => opsGet<{ entries: AuditEntry[] }>("/audit?limit=200"), []);
  const verify = useLoad(() => opsGet<{ intact: boolean; first_broken_seq: number | null }>("/audit/verify"), []);
  return (
    <>
      <h1>Platform audit</h1>
      <p className="hint">
        Every operator action, in the platform's hash chain: anchored and signed outside the database like every tenant's. Download it and check it
        offline with <code>taskiem audit verify FILE</code>.
      </p>
      <div className="toolbar">
        {verify.data && (verify.data.intact ? <Badge value="ok" /> : <Badge value="failed" />)}
        <span className="hint">{verify.data ? (verify.data.intact ? "Chain intact" : `Broken at entry ${verify.data.first_broken_seq}`) : ""}</span>
        <span className="grow" />
        <a className="button" href="/v1/ops/audit/export">
          Download export
        </a>
      </div>
      <ErrorBox error={error ?? verify.error} />
      {data && (
        <table>
          <thead>
            <tr>
              <th>#</th>
              <th>When</th>
              <th>Who</th>
              <th>Action</th>
              <th>Target</th>
            </tr>
          </thead>
          <tbody>
            {data.entries.map((e) => (
              <tr key={e.seq}>
                <td>{e.seq}</td>
                <td>{fmtTime(e.at)}</td>
                <td>{e.actor_id}</td>
                <td>{e.action}</td>
                <td>
                  <code>{e.target}</code>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
