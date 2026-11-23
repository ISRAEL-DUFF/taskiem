// People chats' safety for moderators (docs/spec/people-chatting.md §8,
// "As built: stage 5a gaps, safety and moderation"; docs/runbooks/people-moderators.md):
// people's reports of people and of businesses, the messages the checks
// held and their verdicts, a person's standing with the moderators'
// actions, groups (5b: reports from inside a group, a group's page, its
// link turned off or the group closed), and appeals. No one at Taskiem reads a people chat except to
// handle a report of it: opening a report, a held message or a file in
// them asks why, and the server records who, what, why and when before it
// shows anything. Every decision asks for the operator's passkey again.

import { useState, type FormEvent, type ReactNode } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { actorLabel } from "../lib/ops";
import {
  ACTION_LABELS,
  anyActionLabel,
  GROUP_ACTION_LABELS,
  groupActionHelp,
  groupActionOperation,
  groupActionRequest,
  actionHelp,
  actionName,
  actionIsGrave,
  actionOperation,
  bytesWords,
  checkReasonLabel,
  heldFileURL,
  isPhoto,
  reportFileURL,
  reportReasonLabel,
  whyOK,
  type GroupAction,
  type ModerationAction,
} from "../lib/peoplesafety";
import { ErrorBox, Field, Items, Item, Row, fmtTime, useAction, useLoad } from "../ui";
import {
  opsGet,
  withStepUp,
  type OpsAppeal,
  type OpsCustomerReport,
  type OpsCustomerReportRow,
  type OpsGroup,
  type OpsHeld,
  type OpsHeldRow,
  type OpsPeopleReport,
  type OpsPeopleReportRow,
  type OpsPerson,
  type OpsReportMessage,
  type OpsVerdict,
  type PeopleOverview,
} from "./client";

/** Tabs filtering a list. */
function Tabs<T extends string>({ value, options, onChange, label }: { value: T; options: [T, string][]; onChange: (v: T) => void; label: string }) {
  return (
    <div className="tabs" role="group" aria-label={label}>
      {options.map(([v, l]) => (
        <button key={v} className={value === v ? "active" : ""} aria-pressed={value === v} onClick={() => onChange(v)}>
          {l}
        </button>
      ))}
    </div>
  );
}

const RECORDED =
  "No one at Taskiem reads a people chat except to handle a report of it, or under a lawful request. Opening a report, a held message or a file asks why, and your name, your reason and the time are written to the platform audit.";

/** /ops/people: what waits, and how much the kept chats take. */
export function PeopleSafety() {
  const { data, error } = useLoad(() => opsGet<PeopleOverview>("/people/overview"), []);
  const total = data?.storage.reduce((n, t) => n + t.bytes, 0) ?? 0;
  return (
    <>
      <h1>People safety</h1>
      <p className="hint">{RECORDED}</p>
      <ErrorBox error={error} />
      {data && (
        <>
          <Items label="What waits for moderators">
            <Item to="/ops/people/reports" icon="inbox" title="Reports of people" side={String(data.open.people_reports)} sub="Open, newest first" testid="ops-people-open-reports" />
            <Item to="/ops/people/reports?kind=business" icon="storefront" title="Reports of businesses" side={String(data.open.customer_reports)} sub="People's reports from business chats" />
            <Item to="/ops/people/held" icon="lock" title="Held messages" side={String(data.open.held)} sub="Messages the checks held, to release or confirm" />
            <Item to="/ops/people/appeals" icon="approvals" title="Appeals" side={String(data.open.appeals)} sub="People asking for a decision to be looked at again" />
            <Item to="/ops/lawful" icon="policies" title="Lawful requests" side={String(data.open.lawful_requests)} sub="Not yet answered or refused" />
          </Items>
          <section className="card" aria-labelledby="people-storage" data-testid="ops-people-storage">
            <h2 id="people-storage">Storage</h2>
            <p>
              <strong className="figure">{bytesWords(total)}</strong> on disk for people chats. {data.storage_words}
            </p>
            <Items label="People chats' tables">
              {data.storage.map((t) => (
                <Row
                  key={t.table}
                  icon="archive"
                  title={<code>{t.table}</code>}
                  side={<span className="item-figure">{bytesWords(t.bytes)}</span>}
                  sub={<span className="sub">{t.rows.toLocaleString("en-NG")} rows</span>}
                />
              ))}
            </Items>
            <p className="hint">
              Groups keep one copy of their messages and files (group_messages, group_files). Prometheus has the same figures (taskiem_people_storage_bytes); over 150
              GB together, or 8 GB more in a day, opens a ticket.
            </p>
          </section>
        </>
      )}
    </>
  );
}

type ReportStatus = "open" | "reviewed" | "all";

/** /ops/people/reports: people's reports of people, or (?kind=business) of businesses. */
export function PeopleReports() {
  const [params, setParams] = useSearchParams();
  const kind = params.get("kind") === "business" ? "business" : "people";
  const [status, setStatus] = useState<ReportStatus>("open");
  const people = useLoad(() => (kind === "people" ? opsGet<{ reports: OpsPeopleReportRow[] }>(`/people/reports?status=${status}`) : Promise.resolve(undefined)), [kind, status]);
  const business = useLoad(
    () => (kind === "business" ? opsGet<{ reports: OpsCustomerReportRow[] }>(`/customer-reports?status=${status}`) : Promise.resolve(undefined)),
    [kind, status],
  );
  const empty = status === "open" ? "No open reports." : "No reports.";
  return (
    <>
      <h1>Reports</h1>
      <p className="hint">
        People's reports, with the last 20 messages of the chat (and their photos and files) sent with their consent. The list shows who and why, never the words.
        Reports stay after the reporter deletes their account, and a reviewed report is deleted a year after review.
      </p>
      <Tabs
        label="Reports of"
        value={kind}
        onChange={(k) => setParams(k === "business" ? { kind: "business" } : {})}
        options={[
          ["people", "People"],
          ["business", "Businesses"],
        ]}
      />
      <Tabs
        label="Show reports"
        value={status}
        onChange={setStatus}
        options={[
          ["open", "Open"],
          ["reviewed", "Reviewed"],
          ["all", "All"],
        ]}
      />
      <ErrorBox error={people.error ?? business.error} />
      {kind === "people" && people.data && (
        <>
          {people.data.reports.length === 0 && <div className="empty">{empty}</div>}
          {people.data.reports.length > 0 && (
            <Items label="Reports of people">
              {people.data.reports.map((r) => (
                <Item
                  key={r.id}
                  to={`/ops/people/reports/${r.id}`}
                  icon="inbox"
                  tone={r.status === "open" ? "warn" : "muted"}
                  testid="ops-people-report"
                  title={
                    r.group_id && !r.group_member
                      ? `A group: ${reportReasonLabel(r.reason)}`
                      : `${r.reported_name || "Deleted account"}: ${reportReasonLabel(r.reason)}`
                  }
                  side={fmtTime(r.created_at)}
                  sub={[
                    r.group_id ? (r.group_member ? "A member, from a group" : "The whole group") : r.from_request ? "From a message request" : "From a chat",
                    `${r.files} ${r.files === 1 ? "file" : "files"} kept`,
                    `reported by ${r.reports_against} ${r.reports_against === 1 ? "person" : "people"}`,
                    r.detached ? "the reporter deleted their account" : "",
                    r.reported_suspended ? "suspended" : "",
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                  sideSub={r.status === "open" ? "Open" : `Reviewed by ${actorLabel(r.reviewed_by ?? "")}`}
                />
              ))}
            </Items>
          )}
        </>
      )}
      {kind === "business" && business.data && (
        <>
          {business.data.reports.length === 0 && <div className="empty">{empty}</div>}
          {business.data.reports.length > 0 && (
            <Items label="Reports of businesses">
              {business.data.reports.map((r) => (
                <Item
                  key={r.id}
                  to={`/ops/customer-reports/${r.id}`}
                  icon="storefront"
                  tone={r.status === "open" ? "warn" : "muted"}
                  testid="ops-customer-report"
                  title={`${r.business}: ${reportReasonLabel(r.reason)}`}
                  side={fmtTime(r.created_at)}
                  sub={[`${r.messages} messages`, r.detached ? "the reporter deleted their account" : ""].filter(Boolean).join(" · ")}
                  sideSub={r.status === "open" ? "Open" : `Reviewed by ${actorLabel(r.reviewed_by ?? "")}`}
                />
              ))}
            </Items>
          )}
        </>
      )}
    </>
  );
}

/** Asks why before anything of a person's is opened; the reason goes with
 * the request and is recorded with the operator's name. Keyed by what is
 * opened, so a reason given for one is never sent for another; `again`
 * asks for the reason once more (the reason kept to change), after an
 * opening that failed. */
function WhyGate({ what, children }: { what: string; children: (why: string, again: () => void) => ReactNode }) {
  const [draft, setDraft] = useState("");
  const [why, setWhy] = useState("");
  if (why) return <>{children(why, () => setWhy(""))}</>;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (whyOK(draft)) setWhy(draft.trim());
  };
  return (
    <form className="card" onSubmit={submit} data-testid="ops-why">
      <h2>Why are you opening this {what}?</h2>
      <p className="hint">{RECORDED}</p>
      <Field label="Your reason (recorded with your name)">
        <input value={draft} onChange={(e) => setDraft(e.target.value)} maxLength={500} placeholder="Handling this report from the queue" />
      </Field>
      <button className="primary" type="submit" disabled={!whyOK(draft)}>
        Open {what}
      </button>
    </form>
  );
}

/** An opening that failed: what went wrong, and the reason asked again to retry. */
function OpenFailed({ error, again }: { error: unknown; again: () => void }) {
  return (
    <section className="card" data-testid="ops-open-failed">
      <ErrorBox error={error} />
      <button className="primary" onClick={again} data-testid="ops-open-again">
        Try again
      </button>
    </section>
  );
}

/** Lifting a decision in force: why (recorded, and kept with the decision),
 * confirmed, then with the passkey, as the other decisions are. */
function LiftAction({ a, onDone }: { a: { id: string; action: ModerationAction | GroupAction }; onDone: () => void }) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const lift = useAction();
  const name = actionName(a.action);
  if (!open)
    return (
      <button className="small" onClick={() => setOpen(true)} data-testid="ops-lift-open">
        Lift<span className="sr-only"> the {name}</span>
      </button>
    );
  return (
    <form
      className="ops-lift"
      onSubmit={(e) => {
        e.preventDefault();
        if (!reason.trim() || !confirm(`Lift the ${name.toLowerCase()}? ${a.action.startsWith("group_") ? "The group's admins are" : "They are"} told it is lifted.`)) return;
        void lift.run(async () => {
          await withStepUp("ops.people.lift", a.id, `/people/actions/${a.id}/lift`, { reason: reason.trim() });
          setOpen(false);
          setReason("");
          onDone();
        }, "Lifted: they are told");
      }}
    >
      <Field label={`Why you are lifting the ${name.toLowerCase()} (recorded with your name)`}>
        <textarea value={reason} onChange={(e) => setReason(e.target.value)} maxLength={1000} rows={2} />
      </Field>
      <ErrorBox error={lift.error} />
      <div className="row-actions">
        <button className="primary" type="submit" disabled={lift.busy || !reason.trim()}>
          Lift with passkey<span className="sr-only"> the {name}</span>
        </button>
        <button type="button" onClick={() => setOpen(false)}>
          Cancel
        </button>
      </div>
    </form>
  );
}

/** A note and a decision taken with the passkey. */
function Decide({
  label,
  name,
  buttons,
  noteLabel,
  required,
}: {
  label: string;
  /** Its landmark's name where several are on one page: unique there. */
  name?: string;
  noteLabel: string;
  required?: boolean;
  buttons: { text: string; danger?: boolean; run: (note: string) => Promise<unknown>; done: string }[];
}) {
  const [note, setNote] = useState("");
  const act = useAction();
  return (
    <section className="card" aria-label={name ?? label}>
      <h2>{label}</h2>
      <Field label={noteLabel}>
        <textarea value={note} onChange={(e) => setNote(e.target.value)} maxLength={1000} rows={3} />
      </Field>
      <ErrorBox error={act.error} />
      <div className="row-actions">
        {buttons.map((b) => (
          <button key={b.text} className={b.danger ? "danger" : "primary"} disabled={act.busy || (required && !note.trim())} onClick={() => void act.run(() => b.run(note.trim()), b.done)}>
            {b.text}
          </button>
        ))}
      </div>
    </section>
  );
}

/** One message of a report, with its photo or file behind a button (each opening recorded). */
function ReportMessage({ m, file }: { m: OpsReportMessage; file?: { url: string; type: string; size: number } }) {
  const [shown, setShown] = useState(false);
  return (
    <li className="act-row" data-testid="ops-report-message">
      <span className="list-icon" aria-hidden="true">
        {m.from_reporter ? "R" : "S"}
      </span>
      <div className="grow">
        <div className="item-top">
          <span className="title">{m.from_reporter ? "The reporter" : (m.sender ?? "The person reported")}</span>
          <span className="hint">{fmtTime(m.at)}</span>
        </div>
        {m.deleted ? (
          <p className="hint">Deleted for everyone before the report.</p>
        ) : (
          <>
            {m.body && <p style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{m.body}</p>}
            {m.card && <p className="hint">A card: {String(m.card.title ?? m.card.kind ?? "shared card")}</p>}
            {m.location && <p className="hint">A place: {m.location.label || `${m.location.lat}, ${m.location.lng}`}</p>}
            {m.attachment && (
              <p className="hint">
                {m.attachment.name ? `${m.attachment.name}, ` : ""}
                {m.attachment.type}
                {m.file_left_out ? " (not kept: the report's files were full)" : ""}
              </p>
            )}
            {file &&
              (isPhoto(file.type) ? (
                shown ? (
                  <img src={file.url} alt={`Photo sent ${fmtTime(m.at)}`} style={{ maxWidth: "100%", maxHeight: 360, borderRadius: 8 }} />
                ) : (
                  <button className="small" onClick={() => setShown(true)}>
                    Show the photo<span className="sr-only"> sent {fmtTime(m.at)}</span>
                  </button>
                )
              ) : (
                <a className="button small" href={file.url} download>
                  Download the file ({bytesWords(file.size)})
                </a>
              ))}
          </>
        )}
      </div>
    </li>
  );
}

/** /ops/people/reports/:id: a report opened, saying why. */
export function PeopleReport() {
  const { id = "" } = useParams();
  return (
    <>
      <p className="crumbs">
        <Link to="/ops/people/reports">Reports</Link>
      </p>
      <h1>Report</h1>
      <WhyGate key={id} what="report">{(why, again) => <PeopleReportOpened id={id} why={why} again={again} />}</WhyGate>
    </>
  );
}

function PeopleReportOpened({ id, why, again }: { id: string; why: string; again: () => void }) {
  const { data, error, reload } = useLoad(() => opsGet<OpsPeopleReport>(`/people/reports/${id}?why=${encodeURIComponent(why)}`), [id, why]);
  if (error) return <OpenFailed error={error} again={again} />;
  if (!data) return <div className="empty">Opening…</div>;
  const r = data.report;
  const files = new Map(data.files.map((f) => [f.message_id, f]));
  // Oldest first, as the chat read.
  const msgs = [...data.messages].reverse();
  return (
    <>
      <section className="card" data-testid="ops-people-report-head">
        <h2>
          {r.group_id && !r.group_member ? "A group" : r.reported_name || "Deleted account"}: {reportReasonLabel(r.reason)}
        </h2>
        {data.group && (
          <p data-testid="ops-report-group">
            In the group <strong>{data.group.name || "with no name"}</strong> ({data.group.members} {data.group.members === 1 ? "member" : "members"} when reported
            {data.group.left ? "; the reporter had left it" : ""}).
          </p>
        )}
        <p>{r.note ? <>The reporter wrote: “{r.note}”</> : "No note from the reporter."}</p>
        <p className="hint">
          Reported {fmtTime(r.created_at)}
          {r.from_request ? ", from a message request" : ""}
          {r.reporter_id ? "" : ". The reporter has since deleted their account"}. {data.sealed ? "Kept sealed under its own key." : ""}
        </p>
        <div className="row-actions">
          {r.reported_id && (
            <Link className="button" to={`/ops/people/persons/${r.reported_id}?report=${r.id}`}>
              Warn, limit, suspend or close<span className="sr-only"> {r.reported_name}</span>
            </Link>
          )}
          {r.group_id && (
            <Link className="button" to={`/ops/people/groups/${r.group_id}?report=${r.id}`} data-testid="ops-report-open-group">
              The group: its members and actions
            </Link>
          )}
        </div>
      </section>
      <section aria-labelledby="report-messages">
        <h2 id="report-messages">The last {msgs.length} messages</h2>
        <ul className="items" aria-label="The report's messages">
          {msgs.map((m, i) => {
            const f = m.id ? files.get(m.id) : undefined;
            return <ReportMessage key={m.id ?? i} m={m} file={f && m.id ? { url: reportFileURL(r.id, m.id, why), type: f.type, size: f.size } : undefined} />;
          })}
        </ul>
      </section>
      {r.status === "open" ? (
        <Decide
          label="Mark reviewed"
          noteLabel="What you did (kept with the report)"
          buttons={[
            {
              text: "Mark reviewed with passkey",
              done: "Report reviewed",
              run: async (note) => {
                await withStepUp("ops.people.report.review", r.id, `/people/reports/${r.id}/review`, { note });
                reload();
              },
            },
          ]}
        />
      ) : (
        <p className="notice" data-testid="ops-report-reviewed">
          Reviewed by {actorLabel(r.reviewed_by ?? "")} {fmtTime(r.reviewed_at)}
          {r.review_note ? `: ${r.review_note}` : "."}
        </p>
      )}
    </>
  );
}

/** /ops/customer-reports/:id: a person's report of a business, opened saying why. */
export function CustomerReport() {
  const { id = "" } = useParams();
  return (
    <>
      <p className="crumbs">
        <Link to="/ops/people/reports?kind=business">Reports of businesses</Link>
      </p>
      <h1>Report of a business</h1>
      <WhyGate key={id} what="report">{(why, again) => <CustomerReportOpened id={id} why={why} again={again} />}</WhyGate>
    </>
  );
}

function CustomerReportOpened({ id, why, again }: { id: string; why: string; again: () => void }) {
  const { data, error, reload } = useLoad(() => opsGet<OpsCustomerReport>(`/customer-reports/${id}?why=${encodeURIComponent(why)}`), [id, why]);
  if (error) return <OpenFailed error={error} again={again} />;
  if (!data) return <div className="empty">Opening…</div>;
  const r = data.report;
  const msgs = [...(r.messages ?? [])].reverse();
  return (
    <>
      <section className="card" data-testid="ops-customer-report-head">
        <h2>
          {r.business}: {reportReasonLabel(r.reason)}
        </h2>
        <p>{r.note ? <>The reporter wrote: “{r.note}”</> : "No note from the reporter."}</p>
        <p className="hint">
          Reported {fmtTime(r.created_at)}
          {r.reporter_id ? "" : ". The reporter has since deleted their account"}. To act on the business, open it under{" "}
          <Link to={`/ops/tenants/${r.tenant_id}`}>Tenants</Link>.
        </p>
      </section>
      <ul className="items" aria-label="The report's messages">
        {msgs.map((m, i) => (
          <ReportMessage key={i} m={{ from_reporter: m.from_customer, kind: m.kind, body: m.body, at: m.at }} />
        ))}
      </ul>
      {r.status === "open" ? (
        <Decide
          label="Mark reviewed"
          noteLabel="What you did (kept with the report)"
          buttons={[
            {
              text: "Mark reviewed with passkey",
              done: "Report reviewed",
              run: async (note) => {
                await withStepUp("ops.customer_report.review", r.id, `/customer-reports/${r.id}/review`, { note });
                reload();
              },
            },
          ]}
        />
      ) : (
        <p className="notice" data-testid="ops-report-reviewed">
          Reviewed by {actorLabel(r.reviewed_by ?? "")} {fmtTime(r.reviewed_at)}
          {r.review_note ? `: ${r.review_note}` : "."}
        </p>
      )}
    </>
  );
}

type HeldState = "held" | "released" | "confirmed" | "all";

/** /ops/people/held: the messages the checks held, and the latest verdicts. */
export function HeldQueue() {
  const [state, setState] = useState<HeldState>("held");
  const held = useLoad(() => opsGet<{ held: OpsHeldRow[] }>(`/people/held?state=${state}`), [state]);
  const verdicts = useLoad(() => opsGet<{ verdicts: OpsVerdict[] }>("/people/verdicts"), []);
  return (
    <>
      <h1>Held messages</h1>
      <p className="hint">
        Messages to strangers the automated checks held: the recipient never got them. Release one held by mistake (it is delivered then, as the sender wrote it), or
        confirm the hold. Kept 90 days after they were held or decided.
      </p>
      <Tabs
        label="Show held messages"
        value={state}
        onChange={setState}
        options={[
          ["held", "Waiting"],
          ["released", "Released"],
          ["confirmed", "Confirmed"],
          ["all", "All"],
        ]}
      />
      <ErrorBox error={held.error} />
      {held.data && held.data.held.length === 0 && <div className="empty">{state === "held" ? "No held message waits." : "None."}</div>}
      {held.data && held.data.held.length > 0 && (
        <Items label="Held messages">
          {held.data.held.map((h) => (
            <Item
              key={h.id}
              to={`/ops/people/held/${h.id}`}
              icon="lock"
              tone={h.state === "held" ? "warn" : "muted"}
              testid="ops-held"
              title={`${h.sender_name || "Deleted account"} to ${h.group_id ? "a group" : h.recipient_name || "Deleted account"}`}
              side={fmtTime(h.created_at)}
              sub={h.reasons.map(checkReasonLabel).join(", ") || "Held by the checks"}
              sideSub={h.state === "held" ? "Waiting" : `${h.state === "released" ? "Released" : "Confirmed"} by ${actorLabel(h.decided_by ?? "")}`}
            />
          ))}
        </Items>
      )}
      <h2>Latest verdicts</h2>
      <p className="hint">What the checks decided about strangers' messages (only the verdict is kept, never the words).</p>
      <ErrorBox error={verdicts.error} />
      {verdicts.data && verdicts.data.verdicts.length === 0 && <div className="empty">No verdicts yet.</div>}
      {verdicts.data && verdicts.data.verdicts.length > 0 && (
        <Items label="Latest verdicts">
          {verdicts.data.verdicts.slice(0, 50).map((v, i) => (
            <Row
              key={i}
              icon={v.verdict === "hold" ? "lock" : "warn"}
              tone={v.verdict === "hold" ? "danger" : "warn"}
              title={
                <Link to={`/ops/people/persons/${v.from_person}`}>
                  {v.from_name || "Deleted account"}
                  <span className="sr-only"> (a {v.verdict === "hold" ? "held" : "warned"} message)</span>
                </Link>
              }
              side={<span className="hint">{fmtTime(v.at)}</span>}
              sub={
                <span className="sub">
                  {v.verdict === "hold" ? "Held" : "Warned"} · to {v.group_id ? "a group" : v.to_name || "Deleted account"} · {v.reasons.map(checkReasonLabel).join(", ")}
                </span>
              }
            />
          ))}
        </Items>
      )}
    </>
  );
}

/** /ops/people/held/:id: a held message opened, saying why. */
export function HeldMessage() {
  const { id = "" } = useParams();
  return (
    <>
      <p className="crumbs">
        <Link to="/ops/people/held">Held messages</Link>
      </p>
      <h1>Held message</h1>
      <WhyGate key={id} what="message">{(why, again) => <HeldOpened id={id} why={why} again={again} />}</WhyGate>
    </>
  );
}

function HeldOpened({ id, why, again }: { id: string; why: string; again: () => void }) {
  const { data, error, reload } = useLoad(() => opsGet<OpsHeld>(`/people/held/${id}?why=${encodeURIComponent(why)}`), [id, why]);
  const [shown, setShown] = useState(false);
  if (error) return <OpenFailed error={error} again={again} />;
  if (!data) return <div className="empty">Opening…</div>;
  const h = data.held;
  const m = data.message;
  const url = heldFileURL(h.id, why);
  return (
    <>
      <section className="card" data-testid="ops-held-message">
        <h2>
          {h.sender_name || "Deleted account"} to {h.group_id ? "a group" : h.recipient_name || "Deleted account"}
        </h2>
        {h.group_id && <p className="hint">A group's message is one copy: no member got it. Released, it is sent to the group then.</p>}
        <p className="hint">
          Held {fmtTime(h.created_at)}: {h.reasons.map(checkReasonLabel).join(", ") || "by the checks"}.
        </p>
        {m.body && <p style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{m.body}</p>}
        {m.card && <p className="hint">A card: {String(m.card.what ?? m.card.title ?? "shared card")}</p>}
        {m.location && <p className="hint">A place: {m.location.label || `${m.location.lat}, ${m.location.lng}`}</p>}
        {h.has_file && h.file_type && (
          <>
            <p className="hint">
              {m.name ? `${m.name}, ` : ""}
              {h.file_type}, {bytesWords(h.file_size ?? 0)}
            </p>
            {isPhoto(h.file_type) ? (
              shown ? (
                <img src={url} alt="The held photo" style={{ maxWidth: "100%", maxHeight: 360, borderRadius: 8 }} />
              ) : (
                <button className="small" onClick={() => setShown(true)}>
                  Show the photo
                </button>
              )
            ) : (
              <a className="button small" href={url} download>
                Download the file
              </a>
            )}
          </>
        )}
        {h.sender_id && (
          <p>
            <Link to={`/ops/people/persons/${h.sender_id}`}>The sender's standing</Link>
          </p>
        )}
      </section>
      {h.state === "held" ? (
        <Decide
          label="Decide"
          noteLabel="Note (kept with the decision)"
          buttons={[
            {
              text: "Release with passkey",
              done: "Released: the message is delivered",
              run: async (note) => {
                await withStepUp("ops.people.held.release", h.id, `/people/held/${h.id}/release`, { note });
                reload();
              },
            },
            {
              text: "Confirm the hold with passkey",
              danger: true,
              done: "Hold confirmed",
              run: async (note) => {
                await withStepUp("ops.people.held.confirm", h.id, `/people/held/${h.id}/confirm`, { note });
                reload();
              },
            },
          ]}
        />
      ) : (
        <p className="notice" data-testid="ops-held-decided">
          {h.state === "released" ? "Released, and delivered," : "Hold confirmed"} by {actorLabel(h.decided_by ?? "")} {fmtTime(h.decided_at)}
          {h.decision_note ? `: ${h.decision_note}` : "."}
        </p>
      )}
    </>
  );
}

/** /ops/people/persons/:id: a person's standing, signals and the moderators' actions; never their chats. */
export function PersonStanding() {
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  const report = params.get("report") ?? undefined;
  const { data, error, reload } = useLoad(() => opsGet<OpsPerson>(`/people/persons/${id}`), [id]);
  const [action, setAction] = useState<ModerationAction>("warn");
  const [reason, setReason] = useState("");
  const [days, setDays] = useState("");
  const act = useAction();
  if (error) return <ErrorBox error={error} />;
  if (!data) return <div className="empty">Loading…</div>;
  const p = data.person;
  const take = () => {
    if (actionIsGrave(action) && !confirm(`${ACTION_LABELS[action]}: ${p.name}? They are told, with your reason, and can appeal.`)) return;
    void act.run(async () => {
      const body: Record<string, unknown> = { action, reason: reason.trim() };
      if (days.trim()) body.days = Number(days);
      if (report) body.report_id = report;
      await withStepUp(actionOperation(action), p.id, `/people/persons/${p.id}/actions`, body);
      setReason("");
      setDays("");
      reload();
    }, "Done: they are told");
  };
  const state = p.deleted ? "Deleted" : p.suspended ? "Suspended" : p.active ? "Active" : "Not active";
  return (
    <>
      <p className="crumbs">
        <Link to="/ops/people">People safety</Link>
      </p>
      <h1>{p.name || "No name"}</h1>
      <section className="card" data-testid="ops-person-signals">
        <h2>Standing and signals</h2>
        <p>
          <strong>{state}</strong>. Account made {fmtTime(p.created_at)}.
        </p>
        <ul>
          <li>Reported by {p.reports_against} people</li>
          <li>{p.held} messages held, {p.verdicts_week} held or warned in the last week</li>
          <li>{p.device_accounts} accounts on the devices they signed in on</li>
          <li>
            Requests: {p.requests_waiting} waiting, {p.requests_accepted} accepted, {p.requests_refused} deleted or blocked
          </li>
        </ul>
      </section>
      {!p.deleted && (
        <section className="card" aria-labelledby="take-action">
          <h2 id="take-action">Take an action</h2>
          {report && <p className="hint">For the report you came from.</p>}
          <Field label="Action">
            <select value={action} onChange={(e) => setAction(e.target.value as ModerationAction)}>
              {(Object.keys(ACTION_LABELS) as ModerationAction[]).map((a) => (
                <option key={a} value={a}>
                  {ACTION_LABELS[a]}
                </option>
              ))}
            </select>
          </Field>
          <p className="hint">{actionHelp(action)}</p>
          <Field label="Reason (they are told it, in these words)">
            <textarea value={reason} onChange={(e) => setReason(e.target.value)} maxLength={1000} rows={3} />
          </Field>
          {(action === "limit" || action === "suspend") && (
            <Field label="For how many days">
              <input type="number" inputMode="numeric" min={0} max={365} value={days} onChange={(e) => setDays(e.target.value)} />
            </Field>
          )}
          <ErrorBox error={act.error} />
          <button className={actionIsGrave(action) ? "danger" : "primary"} disabled={act.busy || !reason.trim()} onClick={take}>
            {ACTION_LABELS[action]} with passkey
          </button>
        </section>
      )}
      <h2>What moderators decided</h2>
      {data.actions.length === 0 && <div className="empty">Nothing yet.</div>}
      {data.actions.length > 0 && (
        <Items label="What moderators decided">
          {data.actions.map((a) => {
            const live = !a.lifted_at && !a.closed_at && a.action !== "warn" && (!a.until || a.action === "close" || new Date(a.until) > new Date());
            return (
              <Row
                key={a.id}
                icon={a.action === "warn" ? "warn" : "lock"}
                tone={live ? "danger" : "muted"}
                testid="ops-person-action"
                title={ACTION_LABELS[a.action]}
                side={<span className="hint">{fmtTime(a.created_at)}</span>}
                sub={
                  <span className="sub">
                    {a.reason} · by {actorLabel(a.operator)}
                    {a.until ? ` · until ${fmtTime(a.until)}` : ""}
                    {a.lifted_at ? ` · lifted by ${actorLabel(a.lifted_by ?? "")} ${fmtTime(a.lifted_at)}` : ""}
                    {a.closed_at ? ` · ${a.close_note ?? "closed"} ${fmtTime(a.closed_at)}` : ""}
                    {a.appeal_state ? ` · appeal ${a.appeal_state}` : ""}
                  </span>
                }
              >
                {live && <LiftAction a={a} onDone={reload} />}
              </Row>
            );
          })}
        </Items>
      )}
    </>
  );
}

type AppealState = "open" | "upheld" | "overturned" | "all";

/** /ops/people/appeals: appeals oldest first; uphold or overturn with a note the person is told. */
export function Appeals() {
  const [state, setState] = useState<AppealState>("open");
  const { data, error, reload } = useLoad(() => opsGet<{ appeals: OpsAppeal[] }>(`/people/appeals?state=${state}`), [state]);
  return (
    <>
      <h1>Appeals</h1>
      <p className="hint">
        People asking for a decision to be looked at again, oldest first. A different moderator from the one who decided should answer. They are told your note
        either way; overturning lifts the decision.
      </p>
      <Tabs
        label="Show appeals"
        value={state}
        onChange={setState}
        options={[
          ["open", "Open"],
          ["upheld", "Upheld"],
          ["overturned", "Overturned"],
          ["all", "All"],
        ]}
      />
      <ErrorBox error={error} />
      {data && data.appeals.length === 0 && <div className="empty">{state === "open" ? "No open appeals." : "None."}</div>}
      {data?.appeals.map((a, i) => (
        // Numbered: two appeals may come from people of one name.
        <section className="card" key={a.id} data-testid="ops-appeal" aria-label={`Appeal ${i + 1}, from ${a.name || "no name"}`}>
          <h2>
            <Link to={`/ops/people/persons/${a.person_id}`}>{a.name || "No name"}</Link>: {anyActionLabel(a.action)}
          </h2>
          {a.group_id && (
            <p>
              For a group they run: <Link to={`/ops/people/groups/${a.group_id}`}>the group's page</Link>
            </p>
          )}
          <p className="hint">
            Decided by {actorLabel(a.operator)}: {a.action_reason}
            {a.until ? ` (until ${fmtTime(a.until)})` : ""}. Appealed {fmtTime(a.created_at)}.
          </p>
          <p style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>“{a.text}”</p>
          {a.state === "open" ? (
            <Decide
              label="Answer the appeal"
              name={`Answer appeal ${i + 1}`}
              noteLabel="Your answer (they are told it)"
              required
              buttons={[
                {
                  text: "Overturn with passkey",
                  done: "Overturned: the decision is lifted",
                  run: async (note) => {
                    await withStepUp("ops.people.appeal.overturn", a.id, `/people/appeals/${a.id}/overturn`, { note });
                    reload();
                  },
                },
                {
                  text: "Uphold with passkey",
                  danger: true,
                  done: "Upheld",
                  run: async (note) => {
                    await withStepUp("ops.people.appeal.uphold", a.id, `/people/appeals/${a.id}/uphold`, { note });
                    reload();
                  },
                },
              ]}
            />
          ) : (
            <p className="notice">
              {a.state === "upheld" ? "Upheld" : "Overturned"} by {actorLabel(a.decided_by ?? "")} {fmtTime(a.decided_at)}
              {a.decision_note ? `: ${a.decision_note}` : "."}
            </p>
          )}
        </section>
      ))}
    </>
  );
}

/** /ops/people/groups/:id: a group for moderators, opened saying why (who is in it
 * is a person's data); never its name or messages, which a report shows. Turn its
 * invite link off or close it, each with the passkey; act on its members as people. */
export function GroupSafety() {
  const { id = "" } = useParams();
  return (
    <>
      <p className="crumbs">
        <Link to="/ops/people/reports">Reports</Link>
      </p>
      <h1>A group</h1>
      <WhyGate key={id} what="group">{(why, again) => <GroupOpened id={id} why={why} again={again} />}</WhyGate>
    </>
  );
}

function GroupOpened({ id, why, again }: { id: string; why: string; again: () => void }) {
  const [params] = useSearchParams();
  const report = params.get("report") ?? undefined;
  const { data, error, reload } = useLoad(() => opsGet<OpsGroup>(`/people/groups/${id}?why=${encodeURIComponent(why)}`), [id, why]);
  const [action, setAction] = useState<GroupAction>("group_link_off");
  const [reason, setReason] = useState("");
  const act = useAction();
  if (error) return <OpenFailed error={error} again={again} />;
  if (!data) return <div className="empty">Opening…</div>;
  const g = data.group;
  const take = () => {
    if (action === "group_close" && !confirm("Close the group? Its members are told, its admins can appeal, and it is deleted in 30 days.")) return;
    void act.run(async () => {
      const body: Record<string, unknown> = { action: groupActionRequest(action), reason: reason.trim() };
      if (report) body.report_id = report;
      await withStepUp(groupActionOperation(action), id, `/people/groups/${id}/actions`, body);
      setReason("");
      reload();
    }, "Done: the members are told");
  };
  return (
    <>
      <section className="card" data-testid="ops-group">
        <h2>Standing</h2>
        {g ? (
          <>
            <p>
              <strong>{g.closed_at ? `Closed: deleted ${fmtTime(g.close_until)} unless an appeal holds it` : "Open"}</strong>
              {g.link_stopped_at ? ". Its invite link is turned off." : g.link_on ? ". Its invite link is on." : ". No invite link."}
            </p>
            <ul>
              <li>
                Made {fmtTime(g.created_at)} by{" "}
                {g.created_by ? <Link to={`/ops/people/persons/${g.created_by}`}>{g.creator_name || "No name"}</Link> : "a deleted account"}
              </li>
              <li>
                {g.members} {g.members === 1 ? "member" : "members"}, {g.admins} {g.admins === 1 ? "admin" : "admins"}
              </li>
              <li>
                {g.reports} {g.reports === 1 ? "report" : "reports"}, {g.held} held {g.held === 1 ? "message" : "messages"}
              </li>
              <li>Last message {fmtTime(g.last_message_at)}</li>
            </ul>
          </>
        ) : (
          <p data-testid="ops-group-gone">This group was deleted. What moderators decided is kept below.</p>
        )}
      </section>
      {g && (
        <section className="card" aria-labelledby="group-action">
          <h2 id="group-action">Take an action on the group</h2>
          {report && <p className="hint">For the report you came from.</p>}
          <Field label="Action">
            <select value={action} onChange={(e) => setAction(e.target.value as GroupAction)} data-testid="ops-group-action">
              {(Object.keys(GROUP_ACTION_LABELS) as GroupAction[]).map((a) => (
                <option key={a} value={a}>
                  {GROUP_ACTION_LABELS[a]}
                </option>
              ))}
            </select>
          </Field>
          <p className="hint">{groupActionHelp(action)}</p>
          <Field label="Reason (the group's admins are told it, in these words)">
            <textarea value={reason} onChange={(e) => setReason(e.target.value)} maxLength={1000} rows={3} />
          </Field>
          <ErrorBox error={act.error} />
          <button className={action === "group_close" ? "danger" : "primary"} disabled={act.busy || !reason.trim()} onClick={take}>
            {GROUP_ACTION_LABELS[action]} with passkey
          </button>
        </section>
      )}
      <h2>Members</h2>
      <p className="hint">Act on a member as on anyone: warn, limit, suspend or close their account from their page.</p>
      {data.members.length === 0 && <div className="empty">No one.</div>}
      {data.members.length > 0 && (
        <Items label="The group's members">
          {data.members.map((m) => (
            <Item
              key={m.person_id}
              to={`/ops/people/persons/${m.person_id}${report ? `?report=${report}` : ""}`}
              icon="members"
              tone={m.left_at ? "muted" : m.suspended ? "danger" : undefined}
              testid="ops-group-member"
              title={m.deleted ? "Deleted account" : m.name || "No name"}
              side={m.role === "admin" ? "Admin" : "Member"}
              sub={[`joined ${fmtTime(m.joined_at)}`, m.left_at ? `${m.left_how === "removed" ? "removed" : "left"} ${fmtTime(m.left_at)}` : "", m.suspended ? "suspended" : ""]
                .filter(Boolean)
                .join(" · ")}
            />
          ))}
        </Items>
      )}
      <h2>Reports</h2>
      {data.reports.length === 0 && <div className="empty">None.</div>}
      {data.reports.length > 0 && (
        <Items label="Reports of the group and its members">
          {data.reports.map((r) => (
            <Item
              key={r.id}
              to={`/ops/people/reports/${r.id}`}
              icon="inbox"
              tone={r.status === "open" ? "warn" : "muted"}
              title={`${r.group_member ? r.reported_name || "Deleted account" : "The group"}: ${reportReasonLabel(r.reason)}`}
              side={fmtTime(r.created_at)}
              sideSub={r.status === "open" ? "Open" : "Reviewed"}
            />
          ))}
        </Items>
      )}
      <h2>What moderators decided</h2>
      {data.actions.length === 0 && <div className="empty">Nothing yet.</div>}
      {data.actions.length > 0 && (
        <Items label="What moderators decided about the group">
          {data.actions.map((a) => {
            const live = !a.lifted_at && !a.closed_at;
            return (
              <Row
                key={a.id}
                icon="lock"
                tone={live ? "danger" : "muted"}
                testid="ops-group-decision"
                title={GROUP_ACTION_LABELS[a.action]}
                side={<span className="hint">{fmtTime(a.created_at)}</span>}
                sub={
                  <span className="sub">
                    {a.reason} · by {actorLabel(a.operator)}
                    {a.until ? ` · deleted ${fmtTime(a.until)} unless appealed` : ""}
                    {a.lifted_at ? ` · lifted by ${actorLabel(a.lifted_by ?? "")} ${fmtTime(a.lifted_at)}` : ""}
                    {a.closed_at ? ` · ${a.close_note ?? "deleted"} ${fmtTime(a.closed_at)}` : ""}
                    {a.appeal_state ? ` · the admins' appeal ${a.appeal_state}` : ""}
                  </span>
                }
              >
                {live && g && <LiftAction a={a} onDone={reload} />}
              </Row>
            );
          })}
        </Items>
      )}
    </>
  );
}
