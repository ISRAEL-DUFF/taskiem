// Lawful requests for people chats (decision 0069 point 11;
// docs/spec/people-chatting.md §8.5; docs/runbooks/lawful-requests.md):
// one operator records a request as it came, a second approves it, and
// only then is exactly the scope asked exported, once per passkey. Whether
// and when the person was told is recorded, or why the law forbids it.
// Every step is in the platform audit; only the counts are published
// (/transparency).

import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { actorLabel } from "../lib/ops";
import { LAWFUL_STATES, lagosDay } from "../lib/peoplesafety";
import { ErrorBox, Field, Items, Item, fmtTime, useAction, useLoad } from "../ui";
import { opsGet, withStepUp, type OpsLawfulRequest, type OpsMe } from "./client";

/** /ops/lawful: the requests, newest first, and recording a new one. */
export function LawfulRequests() {
  const { data, error, reload } = useLoad(() => opsGet<{ requests: OpsLawfulRequest[] }>("/lawful-requests"), []);
  const [adding, setAdding] = useState(false);
  return (
    <>
      <h1>Lawful requests</h1>
      <p className="hint">
        Only proper legal process is answered, with the least that is asked. Record a request exactly as it came; a second operator approves it before anything
        is exported. Follow the <a href="https://github.com/israel-duff/taskiem/blob/main/docs/runbooks/lawful-requests.md">lawful requests runbook</a>.
      </p>
      <p>
        <button className="primary" aria-expanded={adding} onClick={() => setAdding(!adding)}>
          Record a request
        </button>
      </p>
      {adding && <RecordLawful onDone={() => (setAdding(false), reload())} />}
      <ErrorBox error={error} />
      {data && data.requests.length === 0 && <div className="empty">No lawful requests recorded.</div>}
      {data && data.requests.length > 0 && (
        <Items label="Lawful requests">
          {data.requests.map((l) => (
            <Item
              key={l.id}
              to={`/ops/lawful/${l.id}`}
              icon="policies"
              tone={l.state === "recorded" || l.state === "approved" ? "warn" : "muted"}
              testid="ops-lawful"
              title={`${l.authority}, ${l.reference}`}
              side={fmtTime(l.received_at)}
              sub={`About ${l.person_name || "a deleted account"} · ${LAWFUL_STATES[l.state]}`}
            />
          ))}
        </Items>
      )}
    </>
  );
}

function RecordLawful({ onDone }: { onDone: () => void }) {
  const [f, setF] = useState({ authority: "", reference: "", legal_basis: "", person_id: "", with_person_id: "", from: "", to: "", received: "", files: false, scope_note: "" });
  const set = (k: keyof typeof f) => (e: { target: { value: string } }) => setF({ ...f, [k]: e.target.value });
  const act = useAction();
  const ready = f.authority.trim() && f.reference.trim() && f.legal_basis.trim() && f.person_id.trim() && lagosDay(f.from) && lagosDay(f.to) && lagosDay(f.received);
  return (
    <form
      className="card"
      data-testid="ops-lawful-record"
      onSubmit={(e) => {
        e.preventDefault();
        void act.run(async () => {
          const body: Record<string, unknown> = {
            authority: f.authority.trim(),
            reference: f.reference.trim(),
            legal_basis: f.legal_basis.trim(),
            person_id: f.person_id.trim(),
            from: lagosDay(f.from),
            to: lagosDay(f.to, true),
            received_at: lagosDay(f.received),
            files: f.files,
            scope_note: f.scope_note.trim(),
          };
          if (f.with_person_id.trim()) body.with_person_id = f.with_person_id.trim();
          await withStepUp("ops.lawful.record", f.person_id.trim(), "/lawful-requests", body);
          onDone();
        }, "Request recorded: a second operator approves it");
      }}
    >
      <h2>Record a request</h2>
      <Field label="Who asked (the authority)">
        <input value={f.authority} onChange={set("authority")} maxLength={200} required />
      </Field>
      <Field label="Their reference">
        <input value={f.reference} onChange={set("reference")} maxLength={200} required />
      </Field>
      <Field label="The law and the order it was made under">
        <input value={f.legal_basis} onChange={set("legal_basis")} maxLength={500} required />
      </Field>
      <Field label="The person's account id" hint="From Reports or People safety; the request must name one account.">
        <input value={f.person_id} onChange={set("person_id")} required />
      </Field>
      <Field label="Only their chats with (an account id, if the request names one)">
        <input value={f.with_person_id} onChange={set("with_person_id")} />
      </Field>
      <Field label="Messages from (day)">
        <input type="date" value={f.from} onChange={set("from")} required />
      </Field>
      <Field label="Messages to (day, included)">
        <input type="date" value={f.to} onChange={set("to")} required />
      </Field>
      <Field label="Received on">
        <input type="date" value={f.received} onChange={set("received")} required />
      </Field>
      <label className="inline">
        <input type="checkbox" checked={f.files} onChange={(e) => setF({ ...f, files: e.target.checked })} />
        The request asks for photos and files too
      </label>
      <Field label="Anything else about the scope">
        <textarea value={f.scope_note} onChange={set("scope_note")} maxLength={1000} rows={2} />
      </Field>
      <ErrorBox error={act.error} />
      <button className="primary" type="submit" disabled={act.busy || !ready}>
        Record with passkey
      </button>
    </form>
  );
}

/** /ops/lawful/:id: one request and its steps. */
export function LawfulRequest({ me }: { me: OpsMe }) {
  const { id = "" } = useParams();
  const { data, error, reload } = useLoad(() => opsGet<{ request: OpsLawfulRequest }>(`/lawful-requests/${id}`), [id]);
  const [refusal, setRefusal] = useState("");
  const [toldDay, setToldDay] = useState("");
  const [notTold, setNotTold] = useState("");
  const [exported, setExported] = useState("");
  const act = useAction();
  if (error) return <ErrorBox error={error} />;
  if (!data) return <div className="empty">Loading…</div>;
  const l = data.request;
  const recordedByMe = actorLabel(l.recorded_by) === me.operator.email;
  const step = (s: "approve" | "refuse" | "told", body: Record<string, unknown>, done: string) =>
    act.run(async () => {
      await withStepUp(`ops.lawful.${s}`, l.id, `/lawful-requests/${l.id}/${s}`, body);
      reload();
    }, done);
  const exportNow = () =>
    act.run(async () => {
      const out = await withStepUp<{ messages_given: number }>("ops.lawful.export", l.id, `/lawful-requests/${l.id}/export`, {});
      const blob = new Blob([JSON.stringify(out, null, 2)], { type: "application/json" });
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = `lawful-request-${l.id}.json`;
      a.click();
      URL.revokeObjectURL(a.href);
      setExported(`Exported ${out.messages_given} messages. Send the file only to ${l.authority}, as the runbook says.`);
      reload();
    }, false);
  return (
    <>
      <p className="crumbs">
        <Link to="/ops/lawful">Lawful requests</Link>
      </p>
      <h1>
        {l.authority}, {l.reference}
      </h1>
      <section className="card" data-testid="ops-lawful-detail">
        <p>
          <strong>{LAWFUL_STATES[l.state]}</strong>
        </p>
        <ul>
          <li>Under: {l.legal_basis}</li>
          <li>About: {l.person_name || "a deleted account"}</li>
          <li>
            Messages from {fmtTime(l.from)} to {fmtTime(l.to)}
            {l.with_person_id ? ", only with one other person" : ", in every chat"}
            {l.files ? ", with photos and files" : ", without photos and files"}
          </li>
          {l.scope_note && <li>Scope: {l.scope_note}</li>}
          <li>Received {fmtTime(l.received_at)}</li>
          <li>
            Recorded by {actorLabel(l.recorded_by)} {fmtTime(l.recorded_at)}
          </li>
          {l.approved_by && (
            <li>
              Approved by {actorLabel(l.approved_by)} {fmtTime(l.approved_at)}
            </li>
          )}
          {l.answered_at && (
            <li>
              {l.state === "refused" ? `Refused by ${actorLabel(l.answered_by ?? "")}: ${l.refused_reason}` : `Answered by ${actorLabel(l.answered_by ?? "")}, ${l.messages_given ?? 0} messages`}{" "}
              {fmtTime(l.answered_at)}
            </li>
          )}
          <li data-testid="ops-lawful-told">
            {l.told_at ? `The person was told ${fmtTime(l.told_at)}.` : l.not_told_reason ? `Not told: ${l.not_told_reason}` : "Not yet recorded whether the person was told."}
          </li>
        </ul>
      </section>
      {exported && (
        <div className="notice" role="status">
          {exported}
        </div>
      )}
      <ErrorBox error={act.error} />
      {l.state === "recorded" && (
        <section className="card" aria-labelledby="lawful-approve">
          <h2 id="lawful-approve">Approve</h2>
          <p className="hint">
            {recordedByMe ? "You recorded this request: a second operator approves it." : "Check the order, its reference and that the scope is the least that answers it."}
          </p>
          <button className="primary" disabled={act.busy || recordedByMe} onClick={() => void step("approve", {}, "Approved")}>
            Approve with passkey
          </button>
        </section>
      )}
      {(l.state === "approved" || l.state === "answered") && (
        <section className="card" aria-labelledby="lawful-export">
          <h2 id="lawful-export">Export</h2>
          <p className="hint">Exactly the scope above, opened, as a file. The export is written to the platform audit with the number of messages given.</p>
          <button className="primary" disabled={act.busy} onClick={() => void exportNow()}>
            Export with passkey
          </button>
        </section>
      )}
      {(l.state === "recorded" || l.state === "approved") && (
        <section className="card" aria-labelledby="lawful-refuse">
          <h2 id="lawful-refuse">Refuse</h2>
          <Field label="Why it is refused">
            <textarea value={refusal} onChange={(e) => setRefusal(e.target.value)} maxLength={1000} rows={2} />
          </Field>
          <button className="danger" disabled={act.busy || !refusal.trim()} onClick={() => void step("refuse", { reason: refusal.trim() }, "Refused")}>
            Refuse with passkey
          </button>
        </section>
      )}
      <section className="card" aria-labelledby="lawful-told">
        <h2 id="lawful-told">Telling the person</h2>
        <p className="hint">Tell them where the law allows, then record the day; or record why the law forbids telling them.</p>
        <Field label="Told on">
          <input type="date" value={toldDay} onChange={(e) => setToldDay(e.target.value)} />
        </Field>
        <button disabled={act.busy || !lagosDay(toldDay)} onClick={() => void step("told", { told_at: lagosDay(toldDay) }, "Recorded")}>
          Record that they were told, with passkey
        </button>
        <Field label="Or: why they are not told">
          <textarea value={notTold} onChange={(e) => setNotTold(e.target.value)} maxLength={1000} rows={2} />
        </Field>
        <button disabled={act.busy || !notTold.trim()} onClick={() => void step("told", { reason: notTold.trim() }, "Recorded")}>
          Record why not, with passkey
        </button>
      </section>
    </>
  );
}
