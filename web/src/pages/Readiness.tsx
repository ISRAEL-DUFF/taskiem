import { useState } from "react";
import { Link } from "react-router-dom";
import { get, post, put, type ConnectorInfo } from "../api";
import { useAdvanced, useAuth } from "../auth";
import { fixFor, needsToShow, stepsText, variableValue, words, type ReadyNeed, type TableSpec } from "../lib/readiness";
import { ErrorBox, useAction, useLoad } from "../ui";
import { NewConnection, startLink } from "./Connections";

/**
 * What a workflow needs before it can run, each with its fix in place
 * (no-technical-steps N3): connect a service, fill in a setting, give a
 * role, make a table. onChanged runs after a fix, to check again.
 * tablesHere: the caller makes tables itself (the AI review).
 */
export function NeedsList({ needs, onChanged, tablesHere = false }: { needs: ReadyNeed[]; onChanged: () => void; tablesHere?: boolean }) {
  const advanced = useAdvanced();
  const shown = needsToShow(needs, advanced).filter((n) => !(tablesHere && n.kind === "table"));
  if (shown.length === 0) return null;
  const blocking = shown.some((n) => n.blocking);
  return (
    <section className={blocking ? "notice needs" : "needs"} aria-label="Before it can run">
      <h3>{blocking ? "Before it can run" : "Worth doing"}</h3>
      <ul className="needs-list">
        {shown.map((n) => (
          <li key={`${n.kind}/${n.name}/${n.connector ?? ""}/${n.environment}`}>
            <NeedItem need={n} onChanged={onChanged} />
          </li>
        ))}
      </ul>
    </section>
  );
}

function NeedItem({ need, onChanged }: { need: ReadyNeed; onChanged: () => void }) {
  const { can } = useAuth();
  const fix = fixFor(need);
  const steps = stepsText(need);
  return (
    <div>
      <div>
        {need.message}
        {steps && <span className="hint"> ({steps})</span>}
      </div>
      {fix.kind === "connect" &&
        (can("connection.manage") ? <ConnectFix connector={fix.connector} environment={fix.environment} name={fix.name} service={fix.service} onDone={onChanged} /> : <AskOwner />)}
      {fix.kind === "choose_connection" && (
        <p className="hint">
          Choose one on the <Link to="/connections">Connections</Link> page, or remove the ones you no longer use.
        </p>
      )}
      {fix.kind === "set_variable" &&
        (can("secret.manage") ? (
          <ValueFix label={`Your ${words(fix.name)}`} onSave={(v) => put(`/v1/variables/${fix.environment}/${fix.name}`, { value: variableValue(v) })} onDone={onChanged} />
        ) : (
          <AskOwner />
        ))}
      {fix.kind === "set_secret" &&
        (can("secret.manage") ? (
          <ValueFix label={`Your ${words(fix.name)}`} secret onSave={(v) => put(`/v1/secrets/${fix.environment}/${fix.name}`, { value: v })} onDone={onChanged} />
        ) : (
          <AskOwner />
        ))}
      {fix.kind === "make_table" &&
        (fix.table && can("table.edit") ? (
          <TableFix table={fix.table} onDone={onChanged} />
        ) : (
          <p className="hint">
            Make it on the <Link to="/tables">Tables</Link> page.
          </p>
        ))}
      {fix.kind === "give_role" && (can("member.manage") ? <RoleFix role={fix.role} onDone={onChanged} /> : <AskOwner />)}
    </div>
  );
}

/** Makes a missing table with the columns the workflow uses. */
function TableFix({ table, onDone }: { table: TableSpec; onDone: () => void }) {
  const act = useAction();
  return (
    <div>
      <p className="hint">Its columns: {table.columns.map((c) => c.title).join(", ")}.</p>
      <button type="button" className="small" disabled={act.busy} onClick={() => void act.run(async () => (await post("/v1/tables", table), onDone()), "Table made")}>
        {act.busy ? "Making it…" : "Make it"}
      </button>
      <ErrorBox error={act.error} />
    </div>
  );
}

function AskOwner() {
  return <p className="hint">Ask the business's owner to do this.</p>;
}

function ConnectFix({ connector, environment, name, service, onDone }: { connector: string; environment: string; name: string; service: string; onDone: () => void }) {
  const [open, setOpen] = useState(false);
  const connectors = useLoad(() => (open ? get<{ connectors: ConnectorInfo[] }>("/v1/connectors") : Promise.resolve(undefined)), [open]);
  return (
    <>
      <button type="button" className="primary small" onClick={() => setOpen(true)}>
        Connect {service}
      </button>
      <ErrorBox error={connectors.error} />
      {open && connectors.data && (
        <NewConnection
          connectors={connectors.data.connectors}
          preset={{ connector, environment, name }}
          onClose={() => setOpen(false)}
          onDone={() => (setOpen(false), onDone())}
          onLink={startLink}
        />
      )}
    </>
  );
}

function ValueFix({ label, secret = false, onSave, onDone }: { label: string; secret?: boolean; onSave: (v: string) => Promise<unknown>; onDone: () => void }) {
  const [value, setValue] = useState("");
  const act = useAction();
  return (
    <form
      className="toolbar"
      onSubmit={(e) => {
        e.preventDefault();
        void act.run(async () => (await onSave(value), setValue(""), onDone()), "Saved");
      }}
    >
      <input aria-label={label} placeholder={label} type={secret ? "password" : "text"} autoComplete="off" value={value} onChange={(e) => setValue(e.target.value)} required />
      <button type="submit" className="small" disabled={act.busy || !value.trim()}>
        Save
      </button>
      <ErrorBox error={act.error} />
    </form>
  );
}

interface Member {
  user_id: string;
  email: string;
  name: string;
  roles: string[];
}

function RoleFix({ role, onDone }: { role: string; onDone: () => void }) {
  const members = useLoad(() => get<{ members: Member[] }>("/v1/members"), []);
  const [who, setWho] = useState("");
  const act = useAction();
  return (
    <form
      className="toolbar"
      onSubmit={(e) => {
        e.preventDefault();
        // Adding a role to someone already in the business keeps theirs.
        void act.run(async () => (await post("/v1/members", { email: who, roles: [role] }), onDone()), "Role given");
      }}
    >
      <select aria-label={`Who is ${words(role)}`} value={who} onChange={(e) => setWho(e.target.value)} required>
        <option value="">Give {words(role)} to…</option>
        {members.data?.members.map((m) => (
          <option key={m.user_id} value={m.email}>
            {m.name || m.email}
          </option>
        ))}
      </select>
      <button type="submit" className="small" disabled={act.busy || !who}>
        Give
      </button>
      <ErrorBox error={act.error ?? members.error} />
    </form>
  );
}
