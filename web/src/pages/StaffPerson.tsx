import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { get, put } from "../api";
import { ErrorBox, Field, Modal, PageHeader, Skeleton, useAction, useLoad } from "../ui";
import { dateLabel, naira } from "../lib/invoices";
import {
  WEEKDAYS,
  payWords,
  personSub,
  profileForm,
  profileProblem,
  profileRequest,
  rotaWords,
  type PayPer,
  type ProfileForm,
  type StaffPersonView,
} from "../lib/team";

/** One person of the business (phase 3): who they are, their usual hours
 * and their pay for the record. */
export function StaffPerson() {
  const { user = "" } = useParams();
  const view = useLoad(() => get<StaffPersonView>(`/v1/staff/${encodeURIComponent(user)}`), [user]);
  const [editing, setEditing] = useState(false);
  const p = view.data;
  const name = p ? p.name || p.email || "Someone" : "";
  return (
    <>
      <PageHeader
        title={p ? name : "Staff"}
        description="Their job, their usual hours and their pay, for your records. Taskiem does not pay staff."
        actions={
          <>
            <Link className="button" to="/staff">
              All staff
            </Link>
            {p && (
              <button type="button" className="primary" onClick={() => setEditing(true)}>
                Change
              </button>
            )}
          </>
        }
      />
      <ErrorBox error={view.error} />
      {!p && !view.error && <Skeleton rows={4} label="Loading…" />}
      {p && (
        <section className="card" aria-labelledby="person-about">
          <h2 id="person-about" style={{ marginTop: 0 }}>
            {p.profile.title || "About"}
          </h2>
          <dl className="kv" data-testid="person-facts">
            <dt>What they do</dt>
            <dd>{personSub(p)}</dd>
            <dt>On chat</dt>
            <dd>{p.chat ? "Yes, Taskiem reaches them on WhatsApp" : "Not yet"}</dd>
            {p.profile.started_on && (
              <>
                <dt>Started</dt>
                <dd>{dateLabel(p.profile.started_on)}</dd>
              </>
            )}
            <dt>Usual hours</dt>
            <dd>{rotaWords(p.profile.rota)}</dd>
            <dt>Pay</dt>
            <dd>{payWords(p.profile, naira) || "Not set"}</dd>
            {p.profile.note && (
              <>
                <dt>Note</dt>
                <dd>{p.profile.note}</dd>
              </>
            )}
          </dl>
        </section>
      )}
      {editing && p && (
        <EditProfile
          user={user}
          name={name}
          start={profileForm(p.profile)}
          onClose={() => setEditing(false)}
          onDone={() => {
            setEditing(false);
            view.reload();
          }}
        />
      )}
    </>
  );
}

function EditProfile({ user, name, start, onClose, onDone }: { user: string; name: string; start: ProfileForm; onClose: () => void; onDone: () => void }) {
  const [f, setF] = useState(start);
  const [tried, setTried] = useState(false);
  const act = useAction();
  const problem = profileProblem(f);
  return (
    <Modal title={`Change ${name}`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setTried(true);
          if (problem) return;
          void act.run(async () => {
            await put(`/v1/staff/${encodeURIComponent(user)}/profile`, profileRequest(f));
            onDone();
          }, "Saved");
        }}
      >
        <Field label="Job title" hint="Like Head chef or Cashier, up to 60 letters.">
          <input value={f.title} maxLength={60} onChange={(e) => setF({ ...f, title: e.target.value })} />
        </Field>
        <Field label="The day they started">
          <input type="date" value={f.started_on} onChange={(e) => setF({ ...f, started_on: e.target.value })} />
        </Field>
        <fieldset className="field">
          <legend>Pay, for your records</legend>
          <div className="toolbar">
            <label className="grow">
              <span className="sr-only">Amount in naira</span>
              <input inputMode="decimal" placeholder="80000" value={f.pay} onChange={(e) => setF({ ...f, pay: e.target.value })} />
            </label>
            <label>
              <span className="sr-only">Per</span>
              <select value={f.pay_per} onChange={(e) => setF({ ...f, pay_per: e.target.value as PayPer })}>
                <option value="hour">an hour</option>
                <option value="day">a day</option>
                <option value="week">a week</option>
                <option value="month">a month</option>
              </select>
            </label>
          </div>
          <div className="hint">Leave it empty for none. Only people who manage staff see it.</div>
        </fieldset>
        <fieldset className="field">
          <legend>Usual hours</legend>
          <div className="hint">Clocking in is measured against these. An end before the start runs past midnight.</div>
          {WEEKDAYS.map((d) => {
            const x = f.days[d.value];
            const set = (v: Partial<typeof x>) => setF({ ...f, days: { ...f.days, [d.value]: { ...x, ...v } } });
            return (
              <div key={d.value} className="toolbar">
                <label className="grow">
                  <input type="checkbox" checked={x.on} onChange={(e) => set({ on: e.target.checked })} /> {d.label}
                </label>
                {x.on && (
                  <>
                    <label>
                      <span className="sr-only">{d.label} from</span>
                      <input type="time" value={x.start} onChange={(e) => set({ start: e.target.value })} />
                    </label>
                    <label>
                      <span className="sr-only">{d.label} to</span>
                      <input type="time" value={x.end} onChange={(e) => set({ end: e.target.value })} />
                    </label>
                  </>
                )}
              </div>
            );
          })}
        </fieldset>
        <Field label="Note">
          <textarea rows={3} maxLength={500} value={f.note} onChange={(e) => setF({ ...f, note: e.target.value })} />
        </Field>
        {tried && problem && (
          <p className="error-text" role="alert">
            {problem}
          </p>
        )}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy}>
            Save
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
