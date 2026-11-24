import { Fragment, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { get, put } from "../api";
import { useAuth } from "../auth";
import { useStepUp, type StepUpProof } from "../stepup";
import { EmptyState, ErrorBox, Field, Items, Modal, PageHeader, Row, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { dateLabel, naira } from "../lib/invoices";
import { STAFF_PAY_FIELDS } from "../lib/paymessage";
import type { SavedList, SavedThing } from "../lib/saved";
import {
  WEEKDAYS,
  figureRows,
  payAccountRequest,
  payAccountWords,
  payWords,
  personSub,
  profileForm,
  profileProblem,
  profileRequest,
  rotaWords,
  staffPayFill,
  type PayPer,
  type ProfileForm,
  type StaffFigures,
  type StaffPayAccount,
  type StaffPersonView,
} from "../lib/team";
import { PaySheet } from "./PaySheet";
import { InPayrollaCard } from "./Payroll";

/** One person of the business (phase 3): who they are, their usual hours
 * and their pay, for the record, where payments to them go (outside
 * payroll), and how they are doing. */
export function StaffPerson() {
  const { user = "" } = useParams();
  const view = useLoad(() => get<StaffPersonView>(`/v1/staff/${encodeURIComponent(user)}`), [user]);
  const [editing, setEditing] = useState(false);
  const [paying, setPaying] = useState<DOMRect | null>();
  const { can } = useAuth();
  const p = view.data;
  const name = p ? p.name || p.email || "Someone" : "";
  const acct = p?.pay_account ?? null;
  // Pay them: to the account an owner linked, for anyone who may pay.
  const mayPay = can("money.pay") && !!acct;
  return (
    <>
      <PageHeader
        title={p ? name : "Staff"}
        description="Their job, their usual hours and their pay for your records, and the account other payments to them go to. Salaries are paid through Payrolla."
        actions={
          <>
            <Link className="button" to="/staff">
              All staff
            </Link>
            {p && (
              <button type="button" className={mayPay ? undefined : "primary"} onClick={() => setEditing(true)}>
                Change
              </button>
            )}
            {p && mayPay && (
              <button type="button" className="primary" onClick={(e) => setPaying(e.currentTarget.getBoundingClientRect())} data-testid="staff-pay">
                Pay<span className="sr-only"> {name}</span>
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
      {p && <WherePaid user={user} name={name} acct={acct} onChanged={view.reload} />}
      {p && can("payroll.read") && <InPayrollaCard payrolla={p.payrolla} />}
      {p && <PersonFigures user={user} name={name} />}
      {paying !== undefined && acct && (
        <PaySheet
          title={`Pay ${name}`}
          actionId="pay.person"
          fields={STAFF_PAY_FIELDS}
          initial={staffPayFill(user)}
          from="tools"
          rect={paying}
          lead={
            <p className="pay-lead" data-testid="pay-staff-lead">
              To the account linked to {name}: {payAccountWords(acct)}
            </p>
          }
          onClose={() => setPaying(undefined)}
        />
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

/** Their figures for today, this week or this month (phase 3, step 5). */
function PersonFigures({ user, name }: { user: string; name: string }) {
  const [period, setPeriod] = useState<StaffFigures["period"]>("week");
  const figs = useLoad(() => get<StaffFigures>(`/v1/staff/${encodeURIComponent(user)}/figures?period=${period}`), [user, period]);
  const f = figs.data;
  return (
    <section className="card" aria-labelledby="person-figures">
      <h2 id="person-figures" style={{ marginTop: 0 }}>
        How {name.split(" ")[0]} is doing
      </h2>
      <div className="segmented" role="group" aria-label="Period">
        {(
          [
            ["day", "Today"],
            ["week", "This week"],
            ["month", "This month"],
          ] as const
        ).map(([v, label]) => (
          <button key={v} type="button" aria-pressed={period === v} onClick={() => setPeriod(v)}>
            {label}
          </button>
        ))}
      </div>
      <ErrorBox error={figs.error} />
      {!f && !figs.error && <Skeleton rows={3} label="Loading their figures…" />}
      {f && f.period === period && (
        <dl className="kv" data-testid="person-figures">
          {figureRows(f, naira).map(([k, v]) => (
            <Fragment key={k}>
              <dt>{k}</dt>
              <dd>{v}</dd>
            </Fragment>
          ))}
        </dl>
      )}
    </section>
  );
}

/** Where we pay them: the business's saved bank account linked to them,
 * masked, for payments outside payroll; an owner links, changes or
 * unlinks it with a passkey for this person. */
function WherePaid({ user, name, acct, onChanged }: { user: string; name: string; acct: StaffPayAccount | null; onChanged: () => void }) {
  const { me } = useAuth();
  const owner = !!me?.roles.includes("owner");
  const [choosing, setChoosing] = useState(false);
  const step = useStepUp();
  const act = useAction();
  const send = (savedId: string | null) =>
    step.request(`where ${name} is paid`, (proof: StepUpProof) =>
      put<{ pay_account: StaffPayAccount | null }>(`/v1/staff/${encodeURIComponent(user)}/pay-account`, { ...payAccountRequest(savedId), ...proof }),
    );
  return (
    <section className="card" aria-labelledby="person-pay" data-testid="person-pay">
      <h2 id="person-pay" style={{ marginTop: 0 }}>
        Where we pay them
      </h2>
      {acct ? (
        <Items label="Where we pay them">
          <Row
            icon="money"
            title={acct.title || `@${acct.name}`}
            sub={
              <span className="sub">
                <span className="block" data-testid="person-pay-account">
                  {payAccountWords(acct)}
                </span>
                {acct.linked_at && <span className="block">Linked {fmtTime(acct.linked_at)}</span>}
              </span>
            }
            testid="person-pay-row"
          >
            {owner && (
              <>
                <button type="button" className="small" disabled={act.busy} onClick={() => setChoosing(true)}>
                  Change<span className="sr-only"> where {name} is paid</span>
                </button>
                <button
                  type="button"
                  className="small danger"
                  disabled={act.busy}
                  onClick={() =>
                    confirm(`Unlink this account from ${name}? Pay on their page stops until an account is linked again.`) &&
                    void act.run(async () => (await send(null), onChanged()), "Account unlinked")
                  }
                >
                  Unlink<span className="sr-only"> the account from {name}</span>
                </button>
              </>
            )}
          </Row>
        </Items>
      ) : (
        <p data-testid="person-pay-none">No account linked</p>
      )}
      {!acct && owner && (
        <div className="toolbar">
          <button type="button" onClick={() => setChoosing(true)} data-testid="person-pay-link">
            Link an account
          </button>
        </div>
      )}
      {!acct && !owner && <p className="hint">An owner links one of your saved accounts here.</p>}
      <p className="hint">Salaries are paid through Payrolla. This account is for other payments to them, such as an advance or expenses paid back.</p>
      <ErrorBox error={act.error} />
      {choosing && (
        <ChooseAccount
          name={name}
          current={acct?.saved_id}
          onClose={() => setChoosing(false)}
          onPick={(id) => send(id).then(() => (setChoosing(false), onChanged()))}
        />
      )}
      {step.modal}
    </section>
  );
}

/** One of the business's saved bank accounts, to link to them. */
function ChooseAccount({ name, current, onClose, onPick }: { name: string; current?: string; onClose: () => void; onPick: (id: string) => Promise<unknown> }) {
  const list = useLoad(() => get<SavedList>("/v1/saved?kind=bank_account"), []);
  const items = list.data?.saved.filter((x: SavedThing) => x.kind === "bank_account" && x.owner === "business");
  const [picked, setPicked] = useState(current ?? "");
  const act = useAction();
  return (
    <Modal title={`Where we pay ${name}`} onClose={onClose}>
      <p className="hint">Choose one of the business's saved bank accounts. You confirm with your passkey: it changes where their money goes.</p>
      <ErrorBox error={list.error ?? act.error} />
      {!items && !list.error && <Skeleton rows={3} label="Loading the saved accounts…" />}
      {items && items.length === 0 && (
        <EmptyState icon="money">
          No bank accounts saved yet. <Link to="/saved">Save theirs under Saved</Link>, then link it here.
        </EmptyState>
      )}
      {items && items.length > 0 && (
        <fieldset className="field">
          <legend>Their account</legend>
          <ul className="batch-picks" aria-label="Saved bank accounts">
            {items.map((x) => (
              <li key={x.id}>
                <label className="batch-pick">
                  <input type="radio" name="staff-account" value={x.id} checked={picked === x.id} onChange={() => setPicked(x.id)} data-testid="pick-account" />{" "}
                  <span>
                    {x.check?.account_name || x.title || `@${x.name}`}
                    <span className="block sub">{x.hint ? `@${x.name} · ${x.hint}` : `@${x.name}`}</span>
                  </span>
                </label>
              </li>
            ))}
          </ul>
        </fieldset>
      )}
      <div className="toolbar">
        <button
          type="button"
          className="primary"
          disabled={act.busy || !picked || picked === current}
          onClick={() => void act.run(() => onPick(picked), "Account linked")}
          data-testid="person-pay-save"
        >
          Link this account
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}
