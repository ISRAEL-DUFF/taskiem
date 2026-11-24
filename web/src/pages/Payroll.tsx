// Payroll in Taskiem, on Payrolla (P4a): Payrolla works out, approves and
// pays the payroll; Taskiem shows it here, read from Payrolla. The runs,
// one run's people and whether their pay arrived, and who each staff
// member is in Payrolla. Nothing here pays anyone or approves a payroll.

import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { ApiError, get, put } from "../api";
import { useAuth } from "../auth";
import {
  EmptyState,
  ErrorBox,
  Item,
  Items,
  Modal,
  PageHeader,
  Row,
  Skeleton,
  fmtTime,
  useAction,
  useLoad,
} from "../ui";
import { dateLabel, naira } from "../lib/invoices";
import {
  choosable,
  countsWords,
  employeeSub,
  frequencyWords,
  lineState,
  payrollaLinkBody,
  peopleCount,
  periodLabel,
  runStatus,
  sortLines,
  suggestionWords,
  type PayrollList,
  type PayrollRun,
  type PayrollRunView,
  type PayrollStaffMember,
  type PayrollStaffView,
  type PayrollTone,
  type StaffPayrolla,
} from "../lib/payroll";

function Pill({
  label,
  tone,
  testid,
}: {
  label: string;
  tone: PayrollTone;
  testid?: string;
}) {
  return (
    <span className={`badge sf-status ${tone}`} data-testid={testid}>
      {label}
    </span>
  );
}

/** Not connected: payroll is worked out and paid in Payrolla. One that
 * ended says so, with Reconnect Payrolla for the owner. */
function NotConnected({ ended, owner }: { ended?: boolean; owner?: boolean }) {
  if (ended)
    return (
      <div className="warning" role="note" data-testid="payroll-ended">
        {owner ? (
          <>
            <p style={{ marginTop: 0 }}>Payrolla is no longer connected.</p>
            <Link className="button" to="/payroll/settings" data-testid="payroll-reconnect-link">
              Reconnect Payrolla
            </Link>
          </>
        ) : (
          <p style={{ margin: 0 }}>Payroll isn't connected right now. The owner has been told.</p>
        )}
      </div>
    );
  return (
    <div className="warning" role="note" data-testid="payroll-not-connected">
      <p style={{ marginTop: 0 }}>
        Your payroll is worked out, approved and paid in Payrolla. Connect your
        business's Payrolla here to see each payroll, who was paid and who was
        not, without leaving Taskiem.
      </p>
      <Link className="button" to="/payroll/settings" data-testid="payroll-connect">
        Connect Payrolla
      </Link>
    </div>
  );
}

/** The latest run on pine: the money page's figure. */
function RunHero({
  run,
  label,
  testid,
}: {
  run: PayrollRun;
  label: string;
  testid: string;
}) {
  const st = runStatus(run.status);
  return (
    <section className="hero" aria-label={label} data-testid={testid}>
      <div className="hero-top">
        <span className="hero-label">{label}</span>
        <span className="hero-tag" data-testid="payroll-hero-status">
          {st.label}
        </span>
      </div>
      <div className="hero-amount">{naira(run.total_net_kobo)}</div>
      <p className="hero-sub">
        {periodLabel(run)} · {peopleCount(run.employee_count)}
      </p>
      <dl className="hero-foot">
        <div>
          <dt>Pay day</dt>
          <dd className="hero-foot-words">
            {dateLabel(run.pay_date) || "Not set"}
          </dd>
        </div>
        <div>
          <dt>Before deductions</dt>
          <dd className="hero-foot-words">{naira(run.total_gross_kobo)}</dd>
        </div>
      </dl>
    </section>
  );
}

/** /payroll: the company in Payrolla and its payrolls, newest first. */
export function PayrollPage() {
  const list = useLoad(() => get<PayrollList>("/v1/payroll"), []);
  const [more, setMore] = useState<{ runs: PayrollRun[]; next?: string }>();
  const load = useAction();
  const d = list.data;
  const runs = [...(d?.runs ?? []), ...(more?.runs ?? [])];
  const next = more ? more.next : d?.next_cursor;
  const showMore = () =>
    void load.run(async () => {
      if (!next) return;
      const r = await get<PayrollList>(
        `/v1/payroll?cursor=${encodeURIComponent(next)}`,
      );
      setMore({
        runs: [...(more?.runs ?? []), ...r.runs],
        next: r.next_cursor,
      });
    }, false);
  const latest = runs[0];
  const freq = frequencyWords(d?.company?.pay_frequency);
  return (
    <>
      <p className="crumbs">
        <Link to="/money" aria-label="Back to Money">
          Money
        </Link>
      </p>
      <PageHeader
        title="Payroll"
        description="Your payroll as Payrolla has it. Payrolla works out, approves and pays it; Taskiem shows you each one and who was paid."
      />
      <ErrorBox error={list.error} />
      {!d && !list.error && <Skeleton rows={4} label="Loading the payroll…" />}
      {d && !d.connected && <NotConnected ended={d.ended} owner={d.is_owner} />}
      {d && d.connected && (
        <>
          {d.auth === "key" && d.is_owner && (
            <div className="notice" data-testid="payroll-key-reconnect">
              <p style={{ marginTop: 0 }}>Connect again in one step so you can run payroll here.</p>
              <Link className="button" to="/payroll/settings">
                Reconnect Payrolla
              </Link>
            </div>
          )}
          <p className="payroll-company" data-testid="payroll-company">
            <strong>{d.company?.name || "Your company"}</strong>
            {freq && <span className="sub"> · {freq}</span>}
            {d.test && (
              <>
                {" "}
                <Pill label="Test" tone="info" testid="payroll-test" />
              </>
            )}
          </p>
          {latest && (
            <RunHero
              run={latest}
              label={
                latest.status === "COMPLETED" ? "Last paid" : "Latest payroll"
              }
              testid="payroll-hero"
            />
          )}
          {latest && (
            <div className="toolbar">
              <Link
                className="button primary"
                to={`/payroll/runs/${encodeURIComponent(latest.run_id)}`}
                data-testid="payroll-latest"
              >
                See who is paid
                <span className="sr-only"> in {periodLabel(latest)}</span>
              </Link>
            </div>
          )}
          {runs.length === 0 && (
            <EmptyState icon="money">
              No payroll in Payrolla yet. When one is worked out there, it shows
              here.
            </EmptyState>
          )}
          {runs.length > 0 && (
            <section className="card" aria-labelledby="payroll-runs-h">
              <h2 id="payroll-runs-h">Each payroll</h2>
              <Items label="Payrolls">
                {runs.map((r) => {
                  const st = runStatus(r.status);
                  return (
                    <Item
                      key={r.run_id}
                      to={`/payroll/runs/${encodeURIComponent(r.run_id)}`}
                      icon="money"
                      tone={st.tone === "ok" ? undefined : st.tone}
                      title={periodLabel(r) || "Payroll"}
                      side={naira(r.total_net_kobo)}
                      sub={`${peopleCount(r.employee_count)}${r.pay_date ? ` · pay day ${dateLabel(r.pay_date)}` : ""}`}
                      sideSub={<Pill label={st.label} tone={st.tone} />}
                      testid="payroll-run"
                    />
                  );
                })}
              </Items>
              {next && (
                <div className="more-rows">
                  <button
                    type="button"
                    className="small"
                    disabled={load.busy}
                    onClick={showMore}
                    data-testid="payroll-more"
                  >
                    {load.busy ? "Loading…" : "Show more"}
                  </button>
                </div>
              )}
              <ErrorBox error={load.error} />
            </section>
          )}
          <Items label="Staff in Payrolla">
            <Item
              to="/payroll/staff"
              icon="members"
              title="Staff in Payrolla"
              sub="Who each of your staff is in Payrolla"
              testid="payroll-staff-link"
            />
            <Item
              to="/payroll/settings"
              icon="settings"
              title="Payroll settings"
              sub="Payrolla's connection, and who can approve"
              testid="payroll-settings-link"
            />
          </Items>
        </>
      )}
    </>
  );
}

/** /payroll/runs/:run: one payroll, each person's pay and whether it arrived. */
export function PayrollRunPage() {
  const { run = "" } = useParams();
  const view = useLoad(
    () => get<PayrollRunView>(`/v1/payroll/runs/${encodeURIComponent(run)}`),
    [run],
  );
  const d = view.data;
  const s = d?.run.summary;
  const notConnected =
    view.error instanceof ApiError && view.error.body.code === "not_connected";
  return (
    <>
      <p className="crumbs">
        <Link to="/payroll" aria-label="Back to Payroll">
          Payroll
        </Link>
      </p>
      <PageHeader title={s ? `Payroll for ${periodLabel(s)}` : "Payroll"} />
      {notConnected ? <NotConnected /> : <ErrorBox error={view.error} />}
      {!d && !view.error && <Skeleton rows={4} label="Loading the payroll…" />}
      {d && s && (
        <>
          <RunHero run={s} label="Net pay" testid="payroll-run-hero" />
          {d.approval_url && (
            <section
              className="card"
              aria-labelledby="payroll-approve-h"
              data-testid="payroll-approve"
            >
              <h2 id="payroll-approve-h">Waiting for approval</h2>
              <p className="hint">
                Approving happens in Payrolla for now. Check the figures there,
                approve, and Payrolla pays everyone on pay day.
              </p>
              <div className="toolbar">
                <a
                  className="button primary"
                  href={d.approval_url}
                  target="_blank"
                  rel="noopener noreferrer"
                  data-testid="payroll-approve-open"
                >
                  Review and approve in Payrolla
                  <span className="sr-only"> (opens in a new tab)</span>
                </a>
              </div>
            </section>
          )}
          <section
            className="stats"
            aria-label="Who was paid"
            data-testid="payroll-counts"
          >
            {(
              [
                ["Paid", d.counts.paid],
                ["Waiting", d.counts.waiting],
                ["Not paid", d.counts.failed],
                ["Returned", d.counts.returned],
              ] as const
            ).map(([label, n]) => (
              <div className="stat" key={label}>
                <span className="stat-label">{label}</span>
                <span className="stat-value">{n}</span>
              </div>
            ))}
          </section>
          <section className="card" aria-labelledby="payroll-lines-h">
            <h2 id="payroll-lines-h">Each person</h2>
            {countsWords(d.counts) && (
              <p className="hint">{countsWords(d.counts)}</p>
            )}
            {d.lines.length === 0 && (
              <EmptyState icon="members">
                No one in this payroll yet.
              </EmptyState>
            )}
            {d.lines.length > 0 && (
              <Items label="Each person's pay">
                {sortLines(d.lines).map((l) => {
                  const st = lineState(l.state);
                  return (
                    <Row
                      key={l.payslip_id || l.employee_id}
                      letter={l.name || "?"}
                      tone={st.tone === "ok" ? undefined : st.tone}
                      title={l.name || "Someone"}
                      side={
                        <span className="item-figure">{naira(l.net_kobo)}</span>
                      }
                      testid="payroll-line"
                      attrs={{ "data-state": l.state }}
                      sub={
                        <span className="sub">
                          <Pill label={st.label} tone={st.tone} />
                          {(l.state === "failed" || l.state === "returned") &&
                            l.reason && (
                              <span
                                className="block"
                                data-testid="payroll-line-reason"
                              >
                                {l.reason}
                              </span>
                            )}
                          {l.staff && (
                            <span className="block">
                              <Link
                                to={`/staff/${encodeURIComponent(l.staff.user_id)}`}
                              >
                                Staff: {l.staff.name}
                              </Link>
                            </span>
                          )}
                        </span>
                      }
                    />
                  );
                })}
              </Items>
            )}
          </section>
        </>
      )}
    </>
  );
}

/** /payroll/staff: who each staff member is in Payrolla; an owner
 * confirms a likely match, chooses one, or unlinks. */
export function PayrollStaffPage() {
  const view = useLoad(() => get<PayrollStaffView>("/v1/payroll/staff"), []);
  const { me } = useAuth();
  const owner = !!me?.roles.includes("owner");
  const act = useAction();
  const [choosing, setChoosing] = useState<PayrollStaffMember>();
  const d = view.data;
  const notConnected =
    view.error instanceof ApiError && view.error.body.code === "not_connected";
  const link = (m: PayrollStaffMember, employee: string | null, done: string) =>
    act.run(async () => {
      await put<{ payrolla: StaffPayrolla | null }>(
        `/v1/staff/${encodeURIComponent(m.user_id)}/payrolla`,
        payrollaLinkBody(employee),
      );
      setChoosing(undefined);
      view.reload();
    }, done);
  return (
    <>
      <p className="crumbs">
        <Link to="/payroll" aria-label="Back to Payroll">
          Payroll
        </Link>
      </p>
      <PageHeader
        title="Staff in Payrolla"
        description="Say who each of your staff is in Payrolla. Their payroll then shows their name here, and Payrolla's messages about their pay reach them on chat."
      />
      {notConnected ? <NotConnected /> : <ErrorBox error={view.error} />}
      {!d && !view.error && <Skeleton rows={4} label="Loading your staff…" />}
      {d && !owner && (
        <p className="hint">Only an owner says who someone is in Payrolla.</p>
      )}
      <ErrorBox error={act.error} />
      {d && d.staff.length === 0 && (
        <EmptyState icon="members">
          No staff yet. Add them under Staff, then link them here.
        </EmptyState>
      )}
      {d && d.staff.length > 0 && (
        <Items label="Staff">
          {d.staff.map((m) => (
            <Row
              key={m.user_id}
              letter={m.name || "?"}
              tone={m.payrolla ? undefined : m.suggested ? "warn" : "muted"}
              title={m.name || "Someone"}
              testid="payroll-staff"
              sub={
                <span className="sub">
                  {m.payrolla ? (
                    <>
                      <span
                        className="block"
                        data-testid="payroll-staff-linked"
                      >
                        In Payrolla: {m.payrolla.name || "linked"}
                      </span>
                      {m.payrolla.linked_at && (
                        <span className="block">
                          Linked {fmtTime(m.payrolla.linked_at)}
                          {m.payrolla.linked_by_name
                            ? ` by ${m.payrolla.linked_by_name}`
                            : ""}
                        </span>
                      )}
                    </>
                  ) : m.suggested ? (
                    <span
                      className="block"
                      data-testid="payroll-staff-suggested"
                    >
                      {suggestionWords(m.suggested)}
                    </span>
                  ) : (
                    <span className="block">
                      Not linked to anyone in Payrolla
                    </span>
                  )}
                </span>
              }
            >
              {owner &&
                !m.payrolla &&
                m.suggested &&
                ((s) => (
                  <button
                    type="button"
                    className="small primary"
                    disabled={act.busy}
                    onClick={() => void link(m, s.employee_id, "Linked")}
                    data-testid="payroll-staff-confirm"
                  >
                    Confirm
                    <span className="sr-only">
                      {" "}
                      {m.name} is {s.name}
                    </span>
                  </button>
                ))(m.suggested)}
              {owner && (
                <button
                  type="button"
                  className="small"
                  disabled={act.busy}
                  onClick={() => setChoosing(m)}
                  data-testid="payroll-staff-choose"
                >
                  {m.payrolla ? "Change" : "Choose"}
                  <span className="sr-only"> who {m.name} is in Payrolla</span>
                </button>
              )}
              {owner && m.payrolla && (
                <button
                  type="button"
                  className="small danger"
                  disabled={act.busy}
                  onClick={() =>
                    confirm(
                      `Unlink ${m.name} from ${m.payrolla?.name || "Payrolla"}?`,
                    ) && void link(m, null, "Unlinked")
                  }
                  data-testid="payroll-staff-unlink"
                >
                  Unlink<span className="sr-only"> {m.name}</span>
                </button>
              )}
            </Row>
          ))}
        </Items>
      )}
      {choosing && d && (
        <ChooseEmployee
          member={choosing}
          view={d}
          busy={act.busy}
          onClose={() => setChoosing(undefined)}
          onPick={(id) => void link(choosing, id, "Linked")}
        />
      )}
    </>
  );
}

function ChooseEmployee({
  member,
  view,
  busy,
  onClose,
  onPick,
}: {
  member: PayrollStaffMember;
  view: PayrollStaffView;
  busy: boolean;
  onClose: () => void;
  onPick: (id: string) => void;
}) {
  const options = choosable(view.employees, member.user_id);
  const current = member.payrolla?.employee_id ?? "";
  const [picked, setPicked] = useState(
    current || member.suggested?.employee_id || "",
  );
  return (
    <Modal title={`Who ${member.name} is in Payrolla`} onClose={onClose}>
      <p className="hint">
        Choose them from your Payrolla employees. Those already linked to
        someone else are not shown.
      </p>
      {options.length === 0 && (
        <EmptyState icon="members">
          Everyone in Payrolla is linked to someone already.
        </EmptyState>
      )}
      {options.length > 0 && (
        <fieldset className="field">
          <legend>Payrolla employees</legend>
          <ul className="batch-picks" aria-label="Payrolla employees">
            {options.map((e) => (
              <li key={e.id}>
                <label className="batch-pick">
                  <input
                    type="radio"
                    name="payrolla-employee"
                    value={e.id}
                    checked={picked === e.id}
                    onChange={() => setPicked(e.id)}
                    data-testid="pick-employee"
                  />{" "}
                  <span>
                    {e.name}
                    {employeeSub(e) && (
                      <span className="block sub">{employeeSub(e)}</span>
                    )}
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
          disabled={busy || !picked || picked === current}
          onClick={() => onPick(picked)}
          data-testid="payroll-staff-save"
        >
          Link {member.name}
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}

/** The In Payrolla card on a staff member's page. */
export function InPayrollaCard({
  payrolla,
}: {
  payrolla: StaffPayrolla | null | undefined;
}) {
  return (
    <section
      className="card"
      aria-labelledby="person-payrolla"
      data-testid="person-payrolla"
    >
      <h2 id="person-payrolla" style={{ marginTop: 0 }}>
        In Payrolla
      </h2>
      <p data-testid="person-payrolla-name">
        {payrolla
          ? payrolla.name || "Linked"
          : "Not linked to anyone in Payrolla yet"}
      </p>
      <p>
        <Link to="/payroll/staff">
          {payrolla ? "Staff in Payrolla" : "Link them in Staff in Payrolla"}
        </Link>
      </p>
    </section>
  );
}
