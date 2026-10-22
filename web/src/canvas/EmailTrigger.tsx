// The email trigger (decision 0038): who it takes mail from and whether it
// keeps attachments, the workflow's address, a guide to forwarding mail
// there, and what has arrived.
import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { get, post, type EmailEndpoint } from "../api";
import { useAdvanced, useAuth } from "../auth";
import { badSenders, fromWords, messageWords, parseSenders } from "../lib/email";
import { ErrorBox, Field, fmtTime, useAction, useLoad } from "../ui";

export function EmailTriggerSettings({ config, onChange }: { config: Record<string, unknown>; onChange: (patch: Record<string, unknown>) => void }) {
  const senders = Array.isArray(config.senders) ? (config.senders as string[]) : [];
  const [text, setText] = useState(senders.join(", "));
  const bad = badSenders(parseSenders(text));
  return (
    <>
      <Field
        label="Only take mail from"
        hint={
          bad.length > 0
            ? `Not an email address or a domain: ${bad.join(", ")}`
            : "Email addresses or domains (firstbank.ng takes mail from anyone there), separated by commas. Leave it empty to take mail from anyone."
        }
      >
        <textarea
          rows={2}
          value={text}
          placeholder="alerts@firstbank.ng, mysupplier.com"
          aria-invalid={bad.length > 0 || undefined}
          onChange={(e) => {
            setText(e.target.value);
            const list = parseSenders(e.target.value);
            onChange({ senders: list.length ? list : undefined });
          }}
        />
      </Field>
      <label className="row">
        <input type="checkbox" checked={config.attachments === "keep"} onChange={(e) => onChange({ attachments: e.target.checked ? "keep" : undefined })} />
        Keep attachments (pictures and PDFs, up to 10 an email)
      </label>
    </>
  );
}

/** The workflow's address and what arrived, once it is published. */
export function EmailAddressCard({ workflow }: { workflow: string }) {
  const advanced = useAdvanced();
  const { can } = useAuth();
  const nav = useNavigate();
  // Polls while it waits for the first email, so it shows as it arrives.
  const [waiting, setWaiting] = useState(true);
  const data = useLoad(() => get<{ endpoints: EmailEndpoint[]; receiving: boolean }>(`/v1/workflows/${workflow}/email`), [workflow], waiting);
  const arrived = data.data?.endpoints.some((e) => e.messages.length > 0) ?? false;
  useEffect(() => {
    if (arrived) setWaiting(false);
  }, [arrived]);
  const act = useAction();
  const [note, setNote] = useState("");
  const [copied, setCopied] = useState(false);
  if (!data.data) return <ErrorBox error={data.error} />;
  if (!data.data.receiving) {
    return (
      <div className="card email-card">
        <h2>Its email address</h2>
        <p className="hint">This Taskiem cannot receive email yet. Ask whoever runs it to set up incoming mail.</p>
      </div>
    );
  }
  const shown = data.data.endpoints.filter((e) => advanced || e.environment === "prod");
  const live = shown.filter((e) => e.status === "live");
  if (live.length === 0) {
    return (
      <div className="card email-card">
        <h2>Its email address</h2>
        <p className="hint">Publish the workflow to get its email address. It keeps the same address when you change the workflow later.</p>
      </div>
    );
  }
  return (
    <>
      {live.map((ep) => (
        <div className="card email-card" key={ep.id}>
          <h2>Its email address{advanced && ` (${ep.environment})`}</h2>
          <p className="hint">Email sent or forwarded to this address starts the workflow.</p>
          <div className="email-address">
            <code>{ep.address}</code>
            <button
              type="button"
              onClick={() => {
                void navigator.clipboard?.writeText(ep.address);
                setCopied(true);
                setTimeout(() => setCopied(false), 1500);
              }}
            >
              {copied ? "Copied" : "Copy"}
            </button>
          </div>
          {ep.confirmation && <Confirmation c={ep.confirmation} />}
          <ForwardingGuide address={ep.address} />
          <h3>What arrived</h3>
          {ep.messages.length === 0 ? (
            <p className="email-waiting" role="status">
              <span className="dot" aria-hidden="true" /> Waiting for your first email…
            </p>
          ) : (
            <ul className="email-messages">
              {ep.messages.map((m) => (
                <li key={m.id} className={`email-msg ${m.status}`}>
                  <span className="grow">
                    <strong>{messageWords(m)}</strong>
                    <span className="hint">
                      {fromWords(m)} · {fmtTime(m.received_at)}
                    </span>
                  </span>
                  {m.run_id && <Link to={`/runs/${m.run_id}`}>Open the run</Link>}
                </li>
              ))}
            </ul>
          )}
          <ErrorBox error={act.error} />
          {note && <p className="hint">{note}</p>}
          <div className="page-actions">
            {can("workflow.edit") && (
              <button
                type="button"
                disabled={act.busy}
                onClick={() =>
                  void act.run(async () => {
                    const r = await post<{
                      run_id: string | null;
                      note: string;
                    }>(`/v1/workflows/${workflow}/email/${ep.environment}/test`);
                    if (r.run_id) nav(`/runs/${r.run_id}`);
                    else setNote(r.note);
                  }, false)
                }
              >
                Try it with a sample email
              </button>
            )}
            {can("workflow.publish") && (
              <button
                type="button"
                className="link"
                disabled={act.busy}
                onClick={() => {
                  if (!confirm("Give this workflow a new address? Mail sent to the old one will be refused, so update your forwarding.")) return;
                  void act.run(async () => {
                    await post(`/v1/workflows/${workflow}/email/${ep.environment}/address`);
                    data.reload();
                  }, "New address made");
                }}
              >
                Get a new address
              </button>
            )}
          </div>
        </div>
      ))}
    </>
  );
}

function Confirmation({ c }: { c: NonNullable<EmailEndpoint["confirmation"]> }) {
  return (
    <div className="notice email-confirm" role="status">
      <strong>
        {c.provider} wants to confirm forwarding{c.for ? ` from ${c.for}` : ""}.
      </strong>{" "}
      {c.code ? (
        <>
          Enter this code in {c.provider}'s forwarding settings: <code className="email-code">{c.code}</code>
        </>
      ) : (
        "Open its link to confirm."
      )}
      {c.link && (
        <>
          {" "}
          <a href={c.link} target="_blank" rel="noreferrer noopener">
            Confirm in {c.provider}
          </a>
        </>
      )}
      <span className="hint"> · {fmtTime(c.received_at)}</span>
    </div>
  );
}

/** Step by step: forwarding the right mail from Gmail or Outlook. */
function ForwardingGuide({ address }: { address: string }) {
  const [which, setWhich] = useState<"gmail" | "outlook">("gmail");
  return (
    <details className="email-guide">
      <summary>Forward mail here from your inbox</summary>
      <div className="tabs" role="tablist">
        <button type="button" role="tab" aria-selected={which === "gmail"} className={which === "gmail" ? "active" : ""} onClick={() => setWhich("gmail")}>
          Gmail
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={which === "outlook"}
          className={which === "outlook" ? "active" : ""}
          onClick={() => setWhich("outlook")}
        >
          Outlook
        </button>
      </div>
      {which === "gmail" ? (
        <ol>
          <li>On a computer, open Gmail, click the gear and then See all settings.</li>
          <li>
            Open Forwarding and POP/IMAP, click Add a forwarding address, and enter <code>{address}</code>.
          </li>
          <li>
            Gmail sends a confirmation code to that address. It shows up on this page (and in your chat) within a minute: enter it in Gmail and click Verify.
          </li>
          <li>
            Leave forwarding off there. Instead, search your mail for the emails you want (from your bank, say), click Show search options, then Create filter.
          </li>
          <li>Tick Forward it to, choose the address, and click Create filter. Only matching mail comes here.</li>
        </ol>
      ) : (
        <ol>
          <li>In Outlook on the web, open Settings, then Mail, then Rules, and click Add new rule.</li>
          <li>Name it, and add a condition, such as From with your bank's address.</li>
          <li>
            Add the action Forward to and enter <code>{address}</code>, then Save.
          </li>
          <li>A work account may not be allowed to forward outside the company: ask whoever manages your email if the rule does nothing.</li>
        </ol>
      )}
    </details>
  );
}
