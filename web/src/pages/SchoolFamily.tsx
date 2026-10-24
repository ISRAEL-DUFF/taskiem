import { useState } from "react";
import { Link } from "react-router-dom";
import { get, post, put } from "../api";
import { useAuth } from "../auth";
import { ErrorBox, Field, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { naira } from "../lib/invoices";
import type { SchoolSettings } from "../lib/schools";
import {
  claimLine,
  claimStatus,
  instalmentProblem,
  instalmentsLine,
  instalmentsRequest,
  linkDaysProblem,
  planStatus,
  rowsOf,
  sentLine,
  type Claim,
  type FamilySent,
  type InstalmentRow,
  type PlanRequest,
} from "../lib/schoolFamily";

// The family page's staff side (docs/spec/schools.md 10 and 11.3, B6;
// docs/schools.md#the-family-page): a student's claims and plan requests,
// claims beside the unmatched money in To match, a link sent to a family
// from the Families page, and the link life in Settings.

/** A student's "I have paid" claims, each dismissible with a reason by someone who manages the school. */
export function StudentClaimsPanel({ studentID, manage }: { studentID: string; manage: boolean }) {
  const list = useLoad(() => get<{ claims: Claim[] }>(`/v1/schools/claims?status=any&student=${encodeURIComponent(studentID)}`), [studentID]);
  return (
    <section className="card" aria-labelledby="student-claims">
      <h2 id="student-claims">Payments the family says it made</h2>
      <p className="hint">
        What a parent told the school on the family page about money paid some other way. A claim is never a payment: it is found and counted when the money is matched (or posted), or
        dismissed with a reason.
      </p>
      <ErrorBox error={list.error} />
      {!list.data && !list.error && <Skeleton rows={2} />}
      {list.data && list.data.claims.length === 0 && <p className="hint">No claims.</p>}
      {list.data && list.data.claims.length > 0 && <ClaimList claims={list.data.claims} manage={manage} onChange={() => list.reload()} />}
    </section>
  );
}

/** Claims as a list, with Dismiss for open ones. */
export function ClaimList({ claims, manage, onChange, students }: { claims: Claim[]; manage: boolean; onChange: () => void; students?: boolean }) {
  return (
    <ul className="plain-list" data-testid="claims">
      {claims.map((c) => (
        <ClaimRow key={c.id} claim={c} manage={manage} onChange={onChange} student={students} />
      ))}
    </ul>
  );
}

function ClaimRow({ claim: c, manage, onChange, student }: { claim: Claim; manage: boolean; onChange: () => void; student?: boolean }) {
  const [reason, setReason] = useState("");
  const [open, setOpen] = useState(false);
  const act = useAction();
  const st = claimStatus(c.status);
  const who = c.admission_number ?? c.student ?? "";
  return (
    <li data-testid="claim">
      <div className="toolbar">
        <span className="grow">
          {student && (
            <>
              <Link to={`/schools/students/${c.student_id}`}>{who}</Link> {c.name}, {c.class}:{" "}
            </>
          )}
          {claimLine(c)} <span className={`badge sf-status ${st.tone}`}>{st.label}</span>
        </span>
        <span className="hint">{fmtTime(c.created_at)}</span>
      </div>
      {c.note && <p className="hint">"{c.note}"</p>}
      {c.status === "dismissed" && c.resolution_reason && <p className="hint">Dismissed: {c.resolution_reason}</p>}
      {manage && (c.status ?? "open") === "open" && !open && (
        <button type="button" className="secondary" onClick={() => setOpen(true)}>
          Dismiss
        </button>
      )}
      {open && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (!reason.trim()) return;
            void act.run(async () => {
              await post(`/v1/schools/claims/${c.id}/resolve`, { reason: reason.trim() });
              setOpen(false);
              onChange();
            }, "Claim dismissed");
          }}
        >
          <Field label="Why it is dismissed" hint="The family sees that the school did not find the payment; the reason stays with the claim.">
            <input value={reason} onChange={(e) => setReason(e.target.value)} maxLength={300} required />
          </Field>
          <div className="toolbar">
            <button type="submit" disabled={act.busy || !reason.trim()}>
              Dismiss the claim
            </button>
            <button type="button" className="secondary" onClick={() => setOpen(false)}>
              Keep it
            </button>
          </div>
          <ErrorBox error={act.error} />
        </form>
      )}
    </li>
  );
}

/** A student's payment plan requests; someone with school.approve decides the open one. */
export function StudentPlansPanel({ studentID }: { studentID: string }) {
  const { can } = useAuth();
  const list = useLoad(() => get<{ plans: PlanRequest[] }>(`/v1/schools/plans?status=any&student=${encodeURIComponent(studentID)}`), [studentID]);
  return (
    <section className="card" aria-labelledby="student-plans">
      <h2 id="student-plans">Payment plans</h2>
      <p className="hint">What the family asked for on its page, in its own words. Approving (with the instalments as decided) or declining with a note tells the family at once.</p>
      <ErrorBox error={list.error} />
      {!list.data && !list.error && <Skeleton rows={2} />}
      {list.data && list.data.plans.length === 0 && <p className="hint">No plan requests.</p>}
      {list.data?.plans.map((p) => (
        <PlanCard key={p.id} plan={p} approve={can("school.approve")} onChange={() => list.reload()} />
      ))}
    </section>
  );
}

function PlanCard({ plan: p, approve, onChange }: { plan: PlanRequest; approve: boolean; onChange: () => void }) {
  const st = planStatus(p.status);
  const [deciding, setDeciding] = useState<"approved" | "declined">();
  const [rows, setRows] = useState<InstalmentRow[]>(() => rowsOf(p.instalments));
  const [note, setNote] = useState("");
  const [said, setSaid] = useState("");
  const act = useAction();
  const problem = deciding === "approved" ? instalmentProblem(rows) : null;
  const bad = problem !== null || (deciding === "declined" && !note.trim());
  return (
    <div className="panel" data-testid="plan">
      <div className="toolbar">
        <strong className="grow">
          Asked {fmtTime(p.created_at)}: {instalmentsLine(p.instalments)}
        </strong>
        <span className={`badge sf-status ${st.tone}`}>{st.label}</span>
      </div>
      {p.message && <p>"{p.message}"</p>}
      {p.status === "approved" && <p>Approved as: {instalmentsLine(p.decided_instalments)}</p>}
      {p.decision_note && <p className="hint">Note to the family: {p.decision_note}</p>}
      <div role="status">{said && <div className="notice">{said}</div>}</div>
      {p.status === "requested" && approve && !deciding && (
        <div className="toolbar">
          <button type="button" onClick={() => setDeciding("approved")}>
            Approve…
          </button>
          <button type="button" className="secondary" onClick={() => setDeciding("declined")}>
            Decline…
          </button>
        </div>
      )}
      {p.status === "requested" && !approve && <p className="hint">Someone who may approve for the school decides it.</p>}
      {deciding && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (bad) return;
            void act.run(async () => {
              const body: Record<string, unknown> = { decision: deciding, note: note.trim() };
              if (deciding === "approved") body.instalments = instalmentsRequest(rows);
              const out = await post<{ sent?: FamilySent }>(`/v1/schools/plans/${p.id}`, body);
              setSaid(`${deciding === "approved" ? "Approved" : "Declined"}. ${out.sent ? sentLine(out.sent) : ""}`);
              setDeciding(undefined);
              onChange();
            }, false);
          }}
        >
          {deciding === "approved" && (
            <fieldset style={{ border: 0, padding: 0, margin: 0 }}>
              <legend>Instalments as approved (change them if you like)</legend>
              {rows.map((r, i) => (
                <div className="toolbar" key={i}>
                  <Field label={`Amount ${i + 1} (₦)`}>
                    <input inputMode="decimal" value={r.amount} onChange={(e) => setRows(rows.map((x, j) => (j === i ? { ...x, amount: e.target.value } : x)))} />
                  </Field>
                  <Field label={`By date ${i + 1}`}>
                    <input type="date" value={r.due_on} onChange={(e) => setRows(rows.map((x, j) => (j === i ? { ...x, due_on: e.target.value } : x)))} />
                  </Field>
                </div>
              ))}
              {rows.length < 6 && (
                <button type="button" className="secondary" onClick={() => setRows([...rows, { amount: "", due_on: "" }])}>
                  Add an instalment
                </button>
              )}
            </fieldset>
          )}
          <Field label={deciding === "approved" ? "A note to the family (optional)" : "Why, for the family"}>
            <input value={note} onChange={(e) => setNote(e.target.value)} maxLength={300} required={deciding === "declined"} />
          </Field>
          {problem && (
            <div className="error" role="alert">
              {problem}
            </div>
          )}
          <div className="toolbar">
            <button type="submit" disabled={act.busy || bad}>
              {deciding === "approved" ? "Approve the plan" : "Decline the plan"}
            </button>
            <button type="button" className="secondary" onClick={() => setDeciding(undefined)}>
              Cancel
            </button>
          </div>
          <ErrorBox error={act.error} />
        </form>
      )}
    </div>
  );
}

/** Send a family a fresh link to its page (POST /v1/schools/families/{id}/link); the link is shown to copy by hand. */
export function SendFamilyLink({ familyID }: { familyID: string }) {
  const act = useAction();
  const [sent, setSent] = useState<FamilySent>();
  return (
    <div>
      <button
        type="button"
        className="secondary"
        disabled={act.busy}
        onClick={() =>
          void act.run(async () => {
            setSent(await post<FamilySent>(`/v1/schools/families/${familyID}/link`, {}));
          }, false)
        }
      >
        Send a link
      </button>
      <div role="status">
        {sent && (
          <div className="notice" data-testid="family-link-sent">
            {sentLine(sent)}
            {sent.link && (
              <>
                {" "}
                <a href={sent.link} data-testid="family-link">
                  Open the family page
                </a>
              </>
            )}
          </div>
        )}
      </div>
      <ErrorBox error={act.error} />
    </div>
  );
}

/** To match: a school's open claims beside the unmatched money. */
export function ClaimsToMatch({ claims, manage, onChange }: { claims: Claim[] | null | undefined; manage: boolean; onChange: () => void }) {
  if (!claims || claims.length === 0) return null;
  return (
    <section className="card" aria-labelledby="claims-heading">
      <h2 id="claims-heading">Families say they paid</h2>
      <p className="hint">
        What parents told the school on their family page: money paid some other way, to find among the payments here or to post. A claim counts for nothing until the money is found; one
        the school cannot find is dismissed with a reason.
      </p>
      <ClaimList claims={claims} manage={manage} onChange={onChange} students />
      <p className="hint">{naira(claims.reduce((n, c) => n + c.amount_kobo, 0))} claimed in all.</p>
    </section>
  );
}

/** The owner's link life: how many days a link to a family's page works. */
export function FamilyLinkDays() {
  const settings = useLoad(() => get<SchoolSettings>("/v1/settings/schools"), []);
  const [value, setValue] = useState<string>();
  const act = useAction();
  const shown = value ?? String(settings.data?.family_link_days ?? 3);
  const problem = linkDaysProblem(shown);
  return (
    <section className="card" aria-labelledby="family-link-days">
      <h2 id="family-link-days" style={{ marginTop: 0 }}>
        Family page links
      </h2>
      <p className="hint">
        Every message to a family carries a new link to its fees page, which works for this many days. An expired link tells the family to ask for a new one, and offers to send it to
        the number on file.
      </p>
      <ErrorBox error={settings.error} />
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (problem || !settings.data) return;
          void act.run(async () => {
            await put("/v1/settings/schools", { enabled: settings.data?.enabled ?? true, family_link_days: Number(shown) });
            setValue(undefined);
            settings.reload();
          }, "Link life saved");
        }}
      >
        <Field label="Days a link works" hint="1 to 30; 3 by default">
          <input inputMode="numeric" value={shown} onChange={(e) => setValue(e.target.value)} />
        </Field>
        {problem && value !== undefined && (
          <div className="error" role="alert">
            {problem}
          </div>
        )}
        <button type="submit" disabled={act.busy || problem !== null || !settings.data}>
          Save the link life
        </button>
      </form>
      <ErrorBox error={act.error} />
    </section>
  );
}
