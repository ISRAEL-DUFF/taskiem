// Forms members fill in in the app: members forms (/forms/:id), and form
// steps waiting for someone with their role (/forms/requests).
import { useState, type ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import { ApiError, get, post } from "../api";
import { useAuth } from "../auth";
import { FormView } from "../forms/FormView";
import type { FormAfter, FormPage, FormSchema } from "../lib/forms";
import { EmptyState, ErrorBox, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import type { FormSubmission } from "./Forms";

interface MemberFormDoc {
  form: { id: string; title: string; status: string };
  title: string;
  description?: string;
  schema: FormSchema;
  pages?: FormPage[] | null;
  after?: FormAfter;
  member: { name: string; email: string; member_id: string };
}

export interface FormRequest {
  id: string;
  run_id: string;
  title: string;
  description: string | null;
  schema: FormSchema;
  role: string;
  status: string;
  expires_at: string;
  created_at: string;
  prefill?: Record<string, unknown>;
}

const FILES_NOTE = "Files cannot be sent from the app yet: send them through the form's link.";

/** The fields filled from the member's account, and their values. */
function memberValues(schema: FormSchema, member: MemberFormDoc["member"]): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, p] of Object.entries(schema.properties ?? {})) {
    const from = p["x-form"]?.from;
    if (from === "member.name") out[k] = member.name;
    if (from === "member.email") out[k] = member.email;
  }
  return out;
}

function Sent({ submission, message, children }: { submission: FormSubmission; message: string; children?: ReactNode }) {
  return (
    <div className="card form-done" role="status">
      <h1>Sent</h1>
      <p>{message}</p>
      <p className="hint">
        Reference <strong>{submission.reference}</strong>
      </p>
      {children}
    </div>
  );
}

/** A members form, filled in by the signed-in member. */
export function MemberForm() {
  const { id = "" } = useParams();
  const { can } = useAuth();
  const { data, error } = useLoad(() => get<MemberFormDoc>(`/v1/forms/${id}/view`), [id]);
  const [sent, setSent] = useState<FormSubmission>();
  const [refused, setRefused] = useState("");
  const act = useAction();
  if (error) {
    const gone = error instanceof ApiError && error.status === 404;
    return gone ? <EmptyState icon="forms">This form is not open to you, or it no longer exists.</EmptyState> : <ErrorBox error={error} />;
  }
  if (!data) return <Skeleton rows={4} label="Loading the form…" />;
  const locked = memberValues(data.schema, data.member);
  if (sent) {
    const after = data.after ?? {};
    return (
      <Sent submission={sent} message={sent.message || after.message || "Thank you. Your answers were sent."}>
        {after.mode === "redirect" && after.url && (
          <p>
            <a href={after.url}>Continue</a>
          </p>
        )}
        <p>
          {sent.run_id && can("run.read") && (
            <>
              <Link to={`/runs/${sent.run_id}`}>See the run</Link> ·{" "}
            </>
          )}
          <button type="button" className="link" onClick={() => setSent(undefined)}>
            Fill it in again
          </button>
        </p>
      </Sent>
    );
  }
  return (
    <div className="form-page">
      <div className="card">
        <FormView
          title={data.title}
          description={data.description}
          schema={data.schema}
          pages={data.pages}
          initial={locked}
          locked={Object.keys(locked)}
          filesNote={FILES_NOTE}
          level={1}
          busy={act.busy}
          onSubmit={(answers) =>
            void act.run(async () => {
              setRefused("");
              try {
                const r = await post<{ submission: FormSubmission }>(`/v1/forms/${id}/submissions`, { answers });
                setSent(r.submission);
              } catch (e) {
                if (e instanceof ApiError && e.status === 422) {
                  setRefused(String(e.body.error || "The answers were not accepted."));
                  return;
                }
                throw e;
              }
            })
          }
          footer={
            <>
              {refused && (
                <div className="error" role="alert">
                  Not sent: {refused}
                </div>
              )}
              <ErrorBox error={act.error} />
            </>
          }
        />
      </div>
      <p className="hint">You are signed in as {data.member.email}; your answers start a run in your name.</p>
    </div>
  );
}

/** Form steps waiting for an answer from someone with my roles. */
export function FormRequests() {
  const { data, error } = useLoad(() => get<{ requests: FormRequest[] }>("/v1/form-requests"), []);
  return (
    <>
      <PageHeader title="To fill in" description="Workflows waiting for answers from someone with your role." />
      <ErrorBox error={error} />
      {!data && !error && <Skeleton rows={3} />}
      {data?.requests.length === 0 && <EmptyState icon="forms">Nothing to fill in.</EmptyState>}
      {data && data.requests.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Form</th>
              <th>For</th>
              <th>Asked</th>
              <th>Until</th>
            </tr>
          </thead>
          <tbody>
            {data.requests.map((r) => (
              <tr key={r.id}>
                <td>
                  <Link to={`/forms/requests/${r.id}`}>{r.title}</Link>
                  {r.description && <div className="hint">{r.description}</div>}
                </td>
                <td>{r.role}</td>
                <td>{fmtTime(r.created_at)}</td>
                <td>{fmtTime(r.expires_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}

/** One form step addressed to my role. */
export function FormRequestPage() {
  const { id = "" } = useParams();
  const { can } = useAuth();
  const { data, error } = useLoad(() => get<FormRequest>(`/v1/form-requests/${id}`), [id]);
  const [sent, setSent] = useState<FormSubmission>();
  const [gone, setGone] = useState(false);
  const act = useAction();
  if (error) {
    return error instanceof ApiError && error.status === 404 ? <EmptyState icon="forms">This request is not for you, or it no longer exists.</EmptyState> : <ErrorBox error={error} />;
  }
  if (!data) return <Skeleton rows={4} />;
  const closed = gone || data.status !== "pending" || new Date(data.expires_at).getTime() < Date.now();
  if (sent) {
    return (
      <Sent submission={sent} message="Thank you. The workflow carries on with your answers.">
        <p>
          <Link to="/forms/requests">Back to To fill in</Link>
          {can("run.read") && (
            <>
              {" "}
              · <Link to={`/runs/${data.run_id}`}>See the run</Link>
            </>
          )}
        </p>
      </Sent>
    );
  }
  if (closed) {
    return (
      <EmptyState icon="forms" action={<Link to="/forms/requests">Back to To fill in</Link>}>
        This request is no longer waiting for an answer.
      </EmptyState>
    );
  }
  return (
    <div className="form-page">
      <div className="card">
        <FormView
          title={data.title}
          description={data.description ?? undefined}
          schema={data.schema}
          initial={data.prefill}
          filesNote={FILES_NOTE}
          level={1}
          busy={act.busy}
          onSubmit={(answers) =>
            void act.run(async () => {
              try {
                const r = await post<{ submission: FormSubmission }>(`/v1/form-requests/${id}/answers`, { answers });
                setSent(r.submission);
              } catch (e) {
                if (e instanceof ApiError && e.status === 409) {
                  setGone(true);
                  return;
                }
                throw e;
              }
            })
          }
          footer={<ErrorBox error={act.error} />}
        />
      </div>
      <p className="hint">
        Asked {fmtTime(data.created_at)} of the {data.role} role; open until {fmtTime(data.expires_at)}.
      </p>
    </div>
  );
}
