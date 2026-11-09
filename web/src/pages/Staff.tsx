import { useState } from "react";
import { Link } from "react-router-dom";
import { del, get, post } from "../api";
import { EmptyState, ErrorBox, Field, Items, Modal, PageHeader, Row, Skeleton, useAction, useLoad } from "../ui";
import { toast } from "../toast";
import {
  STAFF_ROLES,
  emptyStaff,
  inviteSub,
  personSub,
  roleWords,
  shiftWords,
  staffProblem,
  type StaffDay,
  type StaffForm,
  type StaffView,
} from "../lib/team";

/** The people of the business (phase 3): add them by phone, see who has
 * joined and who is still to tap Join, and remove someone who has left. */
export function Staff() {
  const view = useLoad(() => get<StaffView>("/v1/staff"), []);
  const day = useLoad(() => get<StaffDay>("/v1/staff/clock"), []);
  const roles = useLoad(() => get<{ roles: { name: string }[] }>("/v1/roles").catch(() => ({ roles: [] })), []);
  const [adding, setAdding] = useState(false);
  const act = useAction();
  const v = view.data;
  // The business's own roles, after the ones staff usually have.
  const own = (roles.data?.roles ?? [])
    .map((r) => r.name)
    .filter((n) => n !== "owner" && !STAFF_ROLES.some((s) => s.value === n));
  return (
    <>
      <PageHeader
        title="Staff"
        description="The people who work with you. Add someone by their phone number: they get a WhatsApp message from Taskiem and join with one tap, no email needed."
        actions={
          <button type="button" className="primary" onClick={() => setAdding(true)}>
            Add staff
          </button>
        }
      />
      <ErrorBox error={view.error ?? act.error} />
      {!v && !view.error && <Skeleton rows={4} label="Loading your staff…" />}
      {day.data && (day.data.in.length > 0 || day.data.late.length > 0 || day.data.shifts.length > 0) && (
        <section className="card" aria-labelledby="staff-today">
          <h2 id="staff-today" style={{ marginTop: 0 }}>
            Today
          </h2>
          <p className="hint">Staff say in and out to Taskiem on WhatsApp when they start and finish.</p>
          <Items label="Today">
            {day.data.late.map((l) => (
              <Row key={`late-${l.user_id}`} letter={l.name} title={l.name} side={<span className="badge warn">Not in yet</span>} sub={<span className="sub">Due at {l.due}</span>} testid="staff-late" />
            ))}
            {day.data.shifts.map((x) => (
              <Row
                key={x.id}
                letter={x.name || "?"}
                title={x.name || "Someone"}
                side={!x.out_at && <span className="badge ok">In now</span>}
                sub={
                  <span className="sub">
                    {shiftWords(x)}
                    {x.in_where && (
                      <>
                        {" · "}
                        <a href={x.in_where} target="_blank" rel="noopener noreferrer">
                          where<span className="sr-only"> {x.name} clocked in (opens a map)</span>
                        </a>
                      </>
                    )}
                  </span>
                }
                testid="staff-shift"
              />
            ))}
          </Items>
        </section>
      )}
      {v && v.invites.length > 0 && (
        <section className="card" aria-labelledby="staff-waiting">
          <h2 id="staff-waiting" style={{ marginTop: 0 }}>
            Waiting to join
          </h2>
          <p className="hint">They tap Join in the WhatsApp message Taskiem sent them. An invitation lasts seven days.</p>
          <Items label="Waiting to join">
            {v.invites.map((x) => (
              <Row key={x.id} letter={x.name || "?"} title={x.name || x.phone} sub={<span className="sub">{inviteSub(x)}</span>} testid="staff-invite">
                <button
                  type="button"
                  className="small"
                  disabled={act.busy}
                  onClick={() =>
                    void act.run(async () => {
                      const r = await post<{ sent: boolean }>(`/v1/staff/invites/${x.id}/send`);
                      view.reload();
                      toast(r.sent ? `Sent again to ${x.name || x.phone}` : "The WhatsApp message did not go. Try again in a little while.", r.sent ? "success" : "error");
                    }, false)
                  }
                >
                  Send again<span className="sr-only"> to {x.name || x.phone}</span>
                </button>
                <button
                  type="button"
                  className="small danger"
                  disabled={act.busy}
                  onClick={() =>
                    confirm(`Cancel the invitation to ${x.name || x.phone}? Join will no longer work.`) &&
                    void act.run(async () => (await del(`/v1/staff/invites/${x.id}`), view.reload()), "Invitation cancelled")
                  }
                >
                  Cancel<span className="sr-only"> the invitation to {x.name || x.phone}</span>
                </button>
              </Row>
            ))}
          </Items>
        </section>
      )}
      {v && (
        <section className="card" aria-labelledby="staff-list">
          <h2 id="staff-list" style={{ marginTop: 0 }}>
            Your staff
          </h2>
          {v.people.length === 0 ? (
            <EmptyState icon="members">No one yet. Add the people who work with you.</EmptyState>
          ) : (
            <Items label="Your staff">
              {v.people.map((p) => (
                <Row
                  key={p.user_id}
                  letter={p.name || p.email || "?"}
                  title={
                    <>
                      <Link to={`/staff/${p.user_id}`}>{p.name || p.email}</Link>
                      {p.you ? " (you)" : ""}
                    </>
                  }
                  side={<span className={`badge ${p.chat ? "ok" : "muted"}`}>{p.chat ? "On chat" : "Web only"}</span>}
                  sub={<span className="sub">{personSub(p)}</span>}
                  testid="staff-person"
                >
                  {!p.you && (
                    <button
                      type="button"
                      className="small danger"
                      disabled={act.busy}
                      onClick={() =>
                        confirm(`Remove ${p.name || p.email} from the business? They lose ${p.roles.map(roleWords).join(", ")}; what they did stays.`) &&
                        void act.run(async () => (await post(`/v1/staff/${p.user_id}/remove`), view.reload()), `${p.name || p.email} removed`)
                      }
                    >
                      Remove<span className="sr-only"> {p.name || p.email}</span>
                    </button>
                  )}
                </Row>
              ))}
            </Items>
          )}
        </section>
      )}
      {adding && (
        <AddStaff
          own={own}
          onClose={() => setAdding(false)}
          onDone={(sent, name) => {
            setAdding(false);
            view.reload();
            toast(
              sent ? `Invitation sent to ${name}` : `${name} is added, but the WhatsApp message did not go. Try Send again in a little while.`,
              sent ? "success" : "error",
            );
          }}
        />
      )}
    </>
  );
}

function AddStaff({ own, onClose, onDone }: { own: string[]; onClose: () => void; onDone: (sent: boolean, name: string) => void }) {
  const [f, setF] = useState<StaffForm>(emptyStaff);
  const [tried, setTried] = useState(false);
  const act = useAction();
  const problem = staffProblem(f);
  const help = STAFF_ROLES.find((r) => r.value === f.role)?.help;
  return (
    <Modal title="Add staff" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setTried(true);
          if (problem) return;
          void act.run(async () => {
            const r = await post<{ id: string; sent: boolean }>("/v1/staff/invites", { name: f.name.trim(), phone: f.phone.trim(), roles: [f.role] });
            onDone(r.sent, f.name.trim());
          }, false);
        }}
      >
        <Field label="Their name">
          <input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} autoComplete="off" maxLength={80} />
        </Field>
        <Field label="Their mobile number" hint="Taskiem sends them a WhatsApp message to join.">
          <input type="tel" value={f.phone} onChange={(e) => setF({ ...f, phone: e.target.value })} placeholder="0803 123 4567" />
        </Field>
        <Field label="What they do" hint={help}>
          <select value={f.role} onChange={(e) => setF({ ...f, role: e.target.value })}>
            {STAFF_ROLES.map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
            {own.map((r) => (
              <option key={r} value={r}>
                {roleWords(r)}
              </option>
            ))}
          </select>
        </Field>
        {tried && problem && (
          <p className="error-text" role="alert">
            {problem}
          </p>
        )}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy}>
            Send the invitation
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
