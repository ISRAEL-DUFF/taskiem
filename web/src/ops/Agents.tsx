// Field agents (docs/agents.md): registering an agent after checking them
// in person, what each has done, and suspending, reactivating or retiring
// one. Every change asks for the operator's passkey.

import { Fragment, useState } from "react";
import { Badge, ErrorBox, Field, fmtTime, useAction, useLoad } from "../ui";
import { opsGet, withStepUp, type OpsAgent, type OpsAgentBusiness } from "./client";

type Action = "suspend" | "reactivate" | "retire";

export function Agents() {
  const { data, error, reload } = useLoad(() => opsGet<{ agents: OpsAgent[] }>("/agents"), []);
  const [adding, setAdding] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  const [notes, setNotes] = useState<Record<string, string>>({});
  const [added, setAdded] = useState("");
  const act = useAction();
  const run = (a: OpsAgent, action: Action) =>
    act.run(async () => {
      if (action === "retire" && !confirm(`Retire ${a.name} (${a.code})? A retired agent stays retired.`)) return;
      await withStepUp(`ops.agent.${action}`, a.id, `/agents/${a.id}/${action}`, { note: (notes[a.id] ?? "").trim() });
      setNotes({ ...notes, [a.id]: "" });
      reload();
    }, "Agent updated");
  return (
    <>
      <div className="toolbar">
        <h1 className="grow" style={{ margin: 0 }}>
          Field agents
        </h1>
        {!adding && (
          <button className="primary" onClick={() => (setAdding(true), setAdded(""))}>
            Register an agent
          </button>
        )}
      </div>
      <p className="hint">
        People Taskiem has checked in person who sign small businesses up. Suspending or retiring an agent ends their open sessions at once; a retired agent
        stays retired.
      </p>
      {added && (
        <div className="notice" role="status">
          {added}
        </div>
      )}
      {adding && (
        <Register
          onClose={() => setAdding(false)}
          onDone={(msg) => {
            setAdding(false);
            setAdded(msg);
            reload();
          }}
        />
      )}
      <ErrorBox error={error ?? act.error} />
      {data && data.agents.length === 0 && <div className="empty">No agent is registered yet.</div>}
      {data && data.agents.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Agent</th>
              <th>Status</th>
              <th>Region</th>
              <th>Signed up</th>
              <th>Published</th>
              <th>Verified</th>
              <th>Open now</th>
              <th>Note</th>
              <th>
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {data.agents.map((a) => {
              const note = (notes[a.id] ?? "").trim();
              return (
                <Fragment key={a.id}>
                  <tr>
                    <td>
                      {a.name} <code>{a.code}</code>
                      <div className="hint">
                        {a.phone} · registered {fmtTime(a.created_at)} by {a.registered_by}
                      </div>
                    </td>
                    <td>
                      <Badge value={a.status} />
                    </td>
                    <td>{a.region || "—"}</td>
                    <td>{a.businesses}</td>
                    <td>{a.published}</td>
                    <td>{a.verified}</td>
                    <td>{a.live_sessions}</td>
                    <td>
                      {a.status !== "retired" && (
                        <input
                          aria-label={`Note for ${a.name}`}
                          placeholder="Why (for the audit)"
                          value={notes[a.id] ?? ""}
                          onChange={(e) => setNotes({ ...notes, [a.id]: e.target.value })}
                        />
                      )}
                    </td>
                    <td style={{ whiteSpace: "nowrap" }}>
                      <button aria-expanded={open === a.id} onClick={() => setOpen(open === a.id ? null : a.id)}>
                        Businesses
                      </button>{" "}
                      {a.status === "active" && (
                        <button className="danger" disabled={act.busy || !note} title={note ? undefined : "Say why in the note first"} onClick={() => void run(a, "suspend")}>
                          Suspend
                        </button>
                      )}
                      {a.status === "suspended" && (
                        <button disabled={act.busy} onClick={() => void run(a, "reactivate")}>
                          Reactivate
                        </button>
                      )}{" "}
                      {a.status !== "retired" && (
                        <button className="danger" disabled={act.busy || !note} title={note ? undefined : "Say why in the note first"} onClick={() => void run(a, "retire")}>
                          Retire
                        </button>
                      )}
                    </td>
                  </tr>
                  {open === a.id && (
                    <tr>
                      <td colSpan={9}>
                        <Businesses agent={a} />
                      </td>
                    </tr>
                  )}
                </Fragment>
              );
            })}
          </tbody>
        </table>
      )}
    </>
  );
}

function Businesses({ agent }: { agent: OpsAgent }) {
  const { data, error } = useLoad(() => opsGet<{ businesses: OpsAgentBusiness[] }>(`/agents/${agent.id}/businesses`), [agent.id]);
  if (error) return <ErrorBox error={error} />;
  if (!data) return <span className="hint">Loading…</span>;
  if (data.businesses.length === 0) return <span className="hint">{agent.name} has signed no business up yet.</span>;
  return (
    <table aria-label={`Businesses ${agent.name} signed up`}>
      <thead>
        <tr>
          <th>Business</th>
          <th>Status</th>
          <th>Signed up</th>
          <th>Page published</th>
          <th>Visit recorded</th>
        </tr>
      </thead>
      <tbody>
        {data.businesses.map((b) => (
          <tr key={b.tenant_id}>
            <td>
              {b.name}
              <div className="hint">{b.tenant_id}</div>
            </td>
            <td>
              <Badge value={b.status} />
            </td>
            <td>{fmtTime(b.created_at)}</td>
            <td>{b.published ? "Yes" : "No"}</td>
            <td>{b.verified ? "Yes" : "No"}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function Register({ onClose, onDone }: { onClose: () => void; onDone: (msg: string) => void }) {
  const [f, setF] = useState({ email: "", name: "", phone: "", region: "", id_type: "", id_checked: false, referee_name: "", referee_phone: "" });
  const act = useAction();
  const set = (k: keyof typeof f, v: string | boolean) => setF({ ...f, [k]: v });
  return (
    <section className="card" aria-labelledby="register-agent">
      <h2 id="register-agent">Register an agent</h2>
      <p className="hint">
        Only after you have seen the person's ID and spoken to their referee. Someone new is emailed a link to choose a password; someone with a Taskiem
        account keeps it.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const email = f.email.trim();
          void act.run(async () => {
            const out = await withStepUp<{ agent: { code: string; name: string } }>("ops.agent.add", email.toLowerCase(), "/agents", {
              email,
              name: f.name.trim(),
              phone: f.phone.trim(),
              region: f.region.trim(),
              checks: { id_type: f.id_type.trim(), id_checked: f.id_checked, referee_name: f.referee_name.trim(), referee_phone: f.referee_phone.trim() },
            });
            onDone(`${out.agent.name} is registered as ${out.agent.code}. Print the code on their card.`);
          }, "Agent added");
        }}
      >
        <div className="row">
          <Field label="Email">
            <input type="email" value={f.email} onChange={(e) => set("email", e.target.value)} required maxLength={320} autoComplete="off" />
          </Field>
          <Field label="Name">
            <input value={f.name} onChange={(e) => set("name", e.target.value)} required maxLength={100} autoComplete="off" />
          </Field>
        </div>
        <div className="row">
          <Field label="Phone">
            <input type="tel" value={f.phone} onChange={(e) => set("phone", e.target.value)} required autoComplete="off" />
          </Field>
          <Field label="Region" hint="Optional, such as Lagos Island.">
            <input value={f.region} onChange={(e) => set("region", e.target.value)} maxLength={100} autoComplete="off" />
          </Field>
        </div>
        <div className="row">
          <Field label="ID type" hint="Such as NIN slip, voter's card or passport.">
            <input value={f.id_type} onChange={(e) => set("id_type", e.target.value)} required autoComplete="off" />
          </Field>
          <Field label="Referee's name">
            <input value={f.referee_name} onChange={(e) => set("referee_name", e.target.value)} required autoComplete="off" />
          </Field>
          <Field label="Referee's phone">
            <input type="tel" value={f.referee_phone} onChange={(e) => set("referee_phone", e.target.value)} autoComplete="off" />
          </Field>
        </div>
        <label className="inline" style={{ marginBottom: 14 }}>
          <input type="checkbox" checked={f.id_checked} onChange={(e) => set("id_checked", e.target.checked)} />I have seen this person's ID, in person
        </label>
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy || !f.id_checked}>
            Register with passkey
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </section>
  );
}
