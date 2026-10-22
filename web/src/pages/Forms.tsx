// The organisation's forms (docs/forms.md, In the web app): each form's
// link, embed code, status and answers this week; pausing, closing and
// opening times; one-time links for link forms; and the submissions,
// masked unless revealed.
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { formWorkflow, type AfterAnswer } from "../lib/newform";
import { get, patch, post } from "../api";
import { useAuth } from "../auth";
import { embedSnippet, linksCSV, maskContact, parseRecipients, showAnswer } from "../lib/forms";
import { Badge, EmptyState, ErrorBox, Field, Modal, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";

export interface FormInfo {
  id: string;
  public_id: string;
  workflow_id: string;
  workflow: string;
  environment: string;
  version: number;
  title: string;
  access: "public" | "link" | "members";
  status: "live" | "paused" | "closed" | "suspended";
  opens_at: string | null;
  closes_at: string | null;
  max_submissions: number | null;
  submissions: number;
  submissions_this_week: number;
  closed_message: string | null;
  url: string;
  embed_url: string;
}

export interface FormSubmission {
  id: string;
  reference: string;
  form_id: string | null;
  request_id: string | null;
  answers: Record<string, unknown>;
  respondent: Record<string, unknown>;
  channel: string;
  run_id: string | null;
  status: "received" | "started" | "answered" | "refused";
  message: string | null;
  note: string | null;
  created_at: string;
  files?: { id: string; field: string; name: string; type: string; size: number }[];
}

interface MintedLink {
  id: string;
  url: string;
  to?: string;
  expires_at: string;
}

const ACCESS: Record<FormInfo["access"], string> = { public: "Anyone with the link", link: "One-time links", members: "Members, in the app" };

/** The address to open a form at: its own, or the app's for members forms. */
export const formLink = (f: FormInfo) => f.url || (f.access === "members" ? `${window.location.origin}/forms/${f.id}` : "");

export function Copy({ text, label }: { text: string; label: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      className="small"
      aria-label={label}
      onClick={() => {
        void navigator.clipboard?.writeText(text).then(() => {
          setDone(true);
          setTimeout(() => setDone(false), 1500);
        });
      }}
    >
      {done ? "Copied" : "Copy"}
    </button>
  );
}

/** A new form and the workflow behind it (no-technical-steps N8). */
function NewForm({ onClose }: { onClose: () => void }) {
  const nav = useNavigate();
  const [title, setTitle] = useState("");
  const [after, setAfter] = useState<AfterAnswer>("tell");
  const act = useAction();
  return (
    <Modal title="New form" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => {
            const r = await post<{ id: string }>("/v1/workflows", { name: title.trim(), definition: formWorkflow(title, after) });
            nav(`/workflows/${r.id}?tab=settings`);
          }, false);
        }}
      >
        <Field label="What the form is called" hint="People see it at the top of the form.">
          <input value={title} maxLength={120} onChange={(e) => setTitle(e.target.value)} required autoFocus placeholder="e.g. Join the waiting list" />
        </Field>
        <fieldset>
          <legend>When someone fills it in</legend>
          <label className="choice">
            <input type="radio" name="after" checked={after === "tell"} onChange={() => setAfter("tell")} /> Tell me on WhatsApp (or by email)
          </label>
          <label className="choice">
            <input type="radio" name="after" checked={after === "collect"} onChange={() => setAfter("collect")} /> Just keep the answers here
          </label>
        </fieldset>
        <p className="hint">Next you choose its questions; publish it to get its link.</p>
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy || !title.trim()}>
            Make the form
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

export function Forms() {
  const { can } = useAuth();
  const [making, setMaking] = useState(false);
  const { data, error, reload } = useLoad(() => get<{ forms: FormInfo[] }>("/v1/forms"), []);
  return (
    <>
      <PageHeader
        title="Forms"
        description="Forms in front of your workflows: share the link, embed it on your website, and see what comes in."
        actions={
          can("workflow.edit") && (
            <button className="primary" onClick={() => setMaking(true)}>
              New form
            </button>
          )
        }
      />
      {making && <NewForm onClose={() => setMaking(false)} />}
      <ErrorBox error={error} />
      {!data && !error && <Skeleton rows={3} label="Loading your forms…" />}
      {data?.forms.length === 0 && (
        <EmptyState icon="forms" action={can("workflow.edit") ? <button onClick={() => setMaking(true)}>Make your first form</button> : undefined}>
          No forms yet. A form collects answers from your customers or team and starts a workflow with them.
        </EmptyState>
      )}
      <div className="form-cards">
        {data?.forms.map((f) => (
          <FormCard key={f.id} form={f} onChange={reload} />
        ))}
      </div>
    </>
  );
}

function FormCard({ form: f, onChange }: { form: FormInfo; onChange: () => void }) {
  const { can } = useAuth();
  const act = useAction();
  const [settings, setSettings] = useState(false);
  const [links, setLinks] = useState(false);
  const link = formLink(f);
  const headingId = `form-${f.id}`;
  const setStatus = (status: string) => act.run(async () => (await patch(`/v1/forms/${f.id}`, { status }), onChange()), "Saved");
  const publish = can("workflow.publish") && f.status !== "suspended";
  return (
    <article className="card form-card" aria-labelledby={headingId}>
      <div className="toolbar">
        <h2 id={headingId} className="grow" style={{ margin: 0 }}>
          {f.title || f.workflow}
        </h2>
        <Badge value={f.status} />
      </div>
      <dl className="kv">
        <dt>Workflow</dt>
        <dd>
          <Link to={`/workflows/${f.workflow_id}?v=${f.version}`}>{f.workflow}</Link> <span className="hint">v{f.version}</span>
        </dd>
        <dt>Environment</dt>
        <dd>{f.environment}</dd>
        <dt>Who can fill it in</dt>
        <dd>{ACCESS[f.access]}</dd>
        <dt>Answers</dt>
        <dd>
          {f.submissions_this_week} this week, {f.submissions} in all{f.max_submissions ? ` (of at most ${f.max_submissions})` : ""} ·{" "}
          {can("run.read") && <Link to={`/forms/${f.id}/submissions`}>See the answers</Link>}
        </dd>
        {(f.opens_at || f.closes_at) && (
          <>
            <dt>Open</dt>
            <dd>
              {f.opens_at ? `from ${fmtTime(f.opens_at)}` : ""} {f.closes_at ? `until ${fmtTime(f.closes_at)}` : ""}
            </dd>
          </>
        )}
        {link && (
          <>
            <dt>Link</dt>
            <dd>
              {f.access === "members" ? <Link to={`/forms/${f.id}`}>{link}</Link> : <code>{link}</code>} <Copy text={link} label={`Copy the link to ${f.title}`} />
              {f.access === "link" && <div className="hint">This page is reached only through one-time links.</div>}
            </dd>
          </>
        )}
        {f.embed_url && f.access !== "members" && (
          <>
            <dt>On your website</dt>
            <dd>
              <code className="embed-code">{embedSnippet(f.embed_url, f.title)}</code> <Copy text={embedSnippet(f.embed_url, f.title)} label={`Copy the embed code of ${f.title}`} />
              <div className="hint">Works on your verified websites only.</div>
            </dd>
          </>
        )}
      </dl>
      {f.status === "suspended" && <div className="error">Taskiem's operators suspended this form. Contact support to have it looked at.</div>}
      <ErrorBox error={act.error} />
      <div className="toolbar">
        {publish && f.status !== "live" && (
          <button disabled={act.busy} onClick={() => void setStatus("live")}>
            {f.status === "closed" ? "Reopen" : "Resume"}
          </button>
        )}
        {publish && f.status === "live" && (
          <button disabled={act.busy} onClick={() => void setStatus("paused")}>
            Pause
          </button>
        )}
        {publish && f.status !== "closed" && (
          <button disabled={act.busy} onClick={() => confirm(`Close "${f.title}"? People see its closed message until it is reopened.`) && void setStatus("closed")}>
            Close
          </button>
        )}
        {publish && <button onClick={() => setSettings(true)}>Opening times and limit</button>}
        {can("form.links") && f.access === "link" && f.status !== "suspended" && (
          <button className="primary" onClick={() => setLinks(true)}>
            Make links
          </button>
        )}
      </div>
      {settings && (
        <FormSettings
          form={f}
          onClose={() => setSettings(false)}
          onSaved={() => {
            setSettings(false);
            onChange();
          }}
        />
      )}
      {links && <MakeLinks form={f} onClose={() => setLinks(false)} />}
    </article>
  );
}

/** datetime-local value of an RFC 3339 time, in the browser's zone. */
function toLocal(s: string | null): string {
  if (!s) return "";
  const d = new Date(s);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function FormSettings({ form: f, onClose, onSaved }: { form: FormInfo; onClose: () => void; onSaved: () => void }) {
  const [opens, setOpens] = useState(toLocal(f.opens_at));
  const [closes, setCloses] = useState(toLocal(f.closes_at));
  const [max, setMax] = useState(f.max_submissions ? String(f.max_submissions) : "");
  const [message, setMessage] = useState(f.closed_message ?? "");
  const act = useAction();
  const save = () =>
    act.run(async () => {
      const body: Record<string, unknown> = {};
      const clear: string[] = [];
      if (opens) body.opens_at = new Date(opens).toISOString();
      else if (f.opens_at) clear.push("opens_at");
      if (closes) body.closes_at = new Date(closes).toISOString();
      else if (f.closes_at) clear.push("closes_at");
      if (max) {
        const n = Number(max);
        if (!Number.isInteger(n) || n < 1) throw new Error("The limit is a whole number, at least 1.");
        body.max_submissions = n;
      } else if (f.max_submissions) clear.push("max_submissions");
      if (message.trim()) body.closed_message = message.trim();
      else if (f.closed_message) clear.push("closed_message");
      if (clear.length) body.clear = clear;
      await patch(`/v1/forms/${f.id}`, body);
      onSaved();
    }, "Saved");
  return (
    <Modal title={`Opening times: ${f.title}`} onClose={onClose}>
      <div className="row">
        <Field label="Opens at" hint="Empty: open now">
          <input type="datetime-local" value={opens} onChange={(e) => setOpens(e.target.value)} />
        </Field>
        <Field label="Closes at" hint="Empty: no end">
          <input type="datetime-local" value={closes} onChange={(e) => setCloses(e.target.value)} />
        </Field>
      </div>
      <Field label="At most this many answers" hint="Empty: no limit">
        <input type="number" min={1} value={max} onChange={(e) => setMax(e.target.value)} />
      </Field>
      <Field label="Closed message" hint="Shown when the form is closed or paused; up to 300 characters, no links">
        <textarea rows={2} maxLength={300} value={message} onChange={(e) => setMessage(e.target.value)} />
      </Field>
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button className="primary" disabled={act.busy} onClick={() => void save()}>
          Save
        </button>
        <button onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  );
}

function MakeLinks({ form: f, onClose }: { form: FormInfo; onClose: () => void }) {
  const [mode, setMode] = useState<"count" | "list">("count");
  const [count, setCount] = useState("10");
  const [list, setList] = useState("");
  const [days, setDays] = useState("7");
  const [made, setMade] = useState<MintedLink[]>();
  const act = useAction();
  const parsed = parseRecipients(list);
  const make = () =>
    act.run(async () => {
      const expires_in_days = Number(days);
      if (!Number.isInteger(expires_in_days) || expires_in_days < 1 || expires_in_days > 30) throw new Error("Links last 1 to 30 days.");
      let body: Record<string, unknown>;
      if (mode === "count") {
        const n = Number(count);
        if (!Number.isInteger(n) || n < 1 || n > 500) throw new Error("Make 1 to 500 links at a time.");
        body = { count: n, expires_in_days };
      } else {
        if (parsed.bad.length) throw new Error(`These are not phone numbers or email addresses: ${parsed.bad.join(", ")}`);
        if (parsed.to.length === 0) throw new Error("Paste at least one phone number or email address.");
        if (parsed.to.length > 500) throw new Error("Up to 500 people at a time.");
        body = { to: parsed.to, expires_in_days };
      }
      const r = await post<{ links: MintedLink[] }>(`/v1/forms/${f.id}/links`, body);
      setMade(r.links);
    }, false);
  const csv = made ? URL.createObjectURL(new Blob([linksCSV(made)], { type: "text/csv" })) : "";
  return (
    <Modal title={`One-time links: ${f.title}`} onClose={onClose}>
      {made ? (
        <div role="status">
          <p>
            {made.length} link{made.length === 1 ? "" : "s"}, each used once, until {fmtTime(made[0]?.expires_at)}. Copy them now: they are not shown again.
          </p>
          <div className="toolbar">
            <Copy text={made.map((l) => l.url).join("\n")} label="Copy every link" />
            <a className="button" href={csv} download={`${f.public_id}-links.csv`}>
              Download CSV
            </a>
          </div>
          <table>
            <thead>
              <tr>
                <th>Link</th>
                <th>For</th>
              </tr>
            </thead>
            <tbody>
              {made.map((l) => (
                <tr key={l.id}>
                  <td>
                    <code className="secret-once">{l.url}</code>
                  </td>
                  <td>{l.to ?? "Anyone holding it"}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <div className="toolbar">
            <button onClick={onClose}>Done</button>
          </div>
        </div>
      ) : (
        <>
          <fieldset>
            <legend>Links</legend>
            <label className="choice">
              <input type="radio" name="links-mode" checked={mode === "count"} onChange={() => setMode("count")} /> A number of open links, to hand out
            </label>
            <label className="choice">
              <input type="radio" name="links-mode" checked={mode === "list"} onChange={() => setMode("list")} /> One for each person: the link also asks for a code sent to them
            </label>
          </fieldset>
          {mode === "count" ? (
            <Field label="How many">
              <input type="number" min={1} max={500} value={count} onChange={(e) => setCount(e.target.value)} />
            </Field>
          ) : (
            <Field label="Phone numbers or email addresses" hint={`One per line. ${parsed.to.length} found${parsed.bad.length ? `; not readable: ${parsed.bad.map(maskContact).join(", ")}` : ""}.`}>
              <textarea rows={6} value={list} onChange={(e) => setList(e.target.value)} placeholder={"0803 123 4567\nada@example.com"} />
            </Field>
          )}
          <Field label="Days until they expire" hint="1 to 30">
            <input type="number" min={1} max={30} value={days} onChange={(e) => setDays(e.target.value)} />
          </Field>
          <ErrorBox error={act.error} />
          <div className="toolbar">
            <button className="primary" disabled={act.busy} onClick={() => void make()}>
              Make links
            </button>
            <button onClick={onClose}>Cancel</button>
          </div>
        </>
      )}
    </Modal>
  );
}

/** A form's submissions, newest first. */
export function FormSubmissions() {
  const { id = "" } = useParams();
  const forms = useLoad(() => get<{ forms: FormInfo[] }>("/v1/forms"), []);
  const subs = useLoad(() => get<{ submissions: FormSubmission[] }>(`/v1/form-submissions?form=${encodeURIComponent(id)}&limit=100`), [id]);
  const [open, setOpen] = useState<string | null>(null);
  const form = forms.data?.forms.find((f) => f.id === id);
  return (
    <>
      <PageHeader
        title={form ? `Answers: ${form.title}` : "Answers"}
        description={
          <>
            Personal answers are masked. <Link to="/forms">All forms</Link>
          </>
        }
      />
      <ErrorBox error={subs.error ?? forms.error} />
      {!subs.data && !subs.error && <Skeleton rows={4} label="Loading the answers…" />}
      {subs.data?.submissions.length === 0 && <EmptyState icon="forms">No answers yet.</EmptyState>}
      {subs.data && subs.data.submissions.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Reference</th>
              <th>Sent</th>
              <th>Status</th>
              <th>From</th>
              <th>Run</th>
            </tr>
          </thead>
          <tbody>
            {subs.data.submissions.map((s) => (
              <tr key={s.id}>
                <td>
                  <button type="button" className="link" aria-expanded={open === s.id} onClick={() => setOpen(open === s.id ? null : s.id)}>
                    {s.reference}
                  </button>
                </td>
                <td>{fmtTime(s.created_at)}</td>
                <td>
                  <Badge value={s.status} />
                  {s.note && <div className="hint">{s.note}</div>}
                </td>
                <td>{respondentText(s.respondent, s.channel)}</td>
                <td>{s.run_id ? <Link to={`/runs/${s.run_id}`}>Open the run</Link> : "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {open && <SubmissionDetail id={open} onClose={() => setOpen(null)} />}
    </>
  );
}

const KIND: Record<string, string> = { anonymous: "Anonymous", verified: "Verified by a code", link: "A one-time link", request: "A form step's link", member: "A member" };

/** Who sent it, without personal data. */
export function respondentText(r: Record<string, unknown>, channel?: string): string {
  const kind = typeof r.kind === "string" ? (KIND[r.kind] ?? r.kind) : "—";
  const bits = [kind];
  for (const k of ["phone", "email"]) {
    const v = r[k];
    if (typeof v === "string") bits.push(maskContact(v));
    else if (v) bits.push(showAnswer(v));
  }
  if (channel && channel !== "direct") bits.push(channel === "embed" ? "on a website" : channel === "app" ? "in the app" : channel);
  return bits.join(" · ");
}

function SubmissionDetail({ id, onClose }: { id: string; onClose: () => void }) {
  const { can } = useAuth();
  const [reveal, setReveal] = useState(false);
  const { data, error } = useLoad(() => get<{ submission: FormSubmission; revealed: boolean }>(`/v1/form-submissions/${id}${reveal ? "?reveal=true" : ""}`), [id, reveal]);
  const act = useAction();
  const s = data?.submission;
  const download = (fileId: string) =>
    act.run(async () => {
      const r = await get<{ url: string; name: string }>(`/v1/form-files/${fileId}`);
      window.open(r.url, "_blank", "noopener");
    }, false);
  return (
    <Modal title={s ? `Answers ${s.reference}` : "Answers"} onClose={onClose}>
      <ErrorBox error={error ?? act.error} />
      {!s && !error && <Skeleton rows={3} />}
      {s && (
        <>
          <dl className="kv">
            <dt>Sent</dt>
            <dd>{fmtTime(s.created_at)}</dd>
            <dt>From</dt>
            <dd>{respondentText(s.respondent, s.channel)}</dd>
            <dt>Status</dt>
            <dd>
              <Badge value={s.status} /> {s.note}
            </dd>
            {s.message && (
              <>
                <dt>Message to them</dt>
                <dd>{s.message}</dd>
              </>
            )}
            {s.run_id && (
              <>
                <dt>Run</dt>
                <dd>
                  <Link to={`/runs/${s.run_id}`}>Open the run</Link>
                </dd>
              </>
            )}
          </dl>
          {can("pii.reveal") && (
            <label className="inline" title="Decrypts personal answers for this view; the access is audited">
              <input type="checkbox" checked={reveal} onChange={(e) => setReveal(e.target.checked)} /> Reveal personal data
            </label>
          )}
          <table>
            <thead>
              <tr>
                <th>Question</th>
                <th>Answer</th>
              </tr>
            </thead>
            <tbody>
              {Object.entries(s.answers).map(([k, v]) => (
                <tr key={k}>
                  <td>
                    <code>{k}</code>
                  </td>
                  <td>{showAnswer(v)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {(s.files?.length ?? 0) > 0 && (
            <>
              <h3>Files</h3>
              <ul>
                {s.files?.map((f) => (
                  <li key={f.id}>
                    {f.name} <span className="hint">({f.field}, {Math.max(1, Math.round(f.size / 1024))} KB)</span>{" "}
                    {can("pii.reveal") ? (
                      <button className="small" disabled={act.busy} onClick={() => void download(f.id)}>
                        Download
                      </button>
                    ) : (
                      <span className="hint">Downloading needs permission to reveal personal data.</span>
                    )}
                  </li>
                ))}
              </ul>
            </>
          )}
        </>
      )}
      <div className="toolbar">
        <button onClick={onClose}>Close</button>
      </div>
    </Modal>
  );
}
