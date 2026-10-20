import { useState } from "react";
import { Link } from "react-router-dom";
import { currentAgentSession, post } from "../api";
import { ErrorBox, Field, Skeleton, fmtTime, useAction } from "../ui";
import { tidy } from "../lib/storefront";
import {
  REFUSAL_HELP,
  VERIFICATION_LEVELS,
  findingsSummary,
  pageStatus,
  pendingOf,
  refusalMessage,
  refusalOf,
  reviewKind,
  reviewStatus,
  turnedDown,
  type Tone,
  type Trust,
  type TrustState,
} from "../lib/trust";

/** A page status as a badge, in words. */
export function PageStatusBadge({ status }: { status: string }) {
  const s = pageStatus(status);
  return <span className={`badge sf-status ${s.tone}`}>{s.label}</span>;
}

function ToneBadge({ label, tone }: { label: string; tone: Tone }) {
  return <span className={`badge sf-status ${tone}`}>{label}</span>;
}

/** An error, with what to do when it is a verification, moderation, plan or suspension refusal. */
export function RefusalBox({ error }: { error: unknown }) {
  const kind = refusalOf(error);
  if (!kind || !(error instanceof Error)) return <ErrorBox error={error} />;
  const title = {
    verification: "Verification needed",
    moderation: "This cannot go on a public page",
    plan: "Not on your plan",
    suspended: "Your page is suspended",
  }[kind];
  return (
    <div className="error" role="alert">
      <strong>{title}.</strong> {kind === "moderation" ? error.message.replace(/^this cannot go on a public page:\s*/i, "") : refusalMessage(error.message)}
      <div style={{ marginTop: 6 }}>
        {REFUSAL_HELP[kind]}
        {kind === "plan" && (
          <>
            {" "}
            <Link className="inline-link" to="/settings/billing">Open Billing</Link>
          </>
        )}
      </div>
    </div>
  );
}

/** Why the page is held, turned down, suspended or has orders paused; with an appeal form. */
export function TrustNotices({ state, trust, onChanged }: { state: TrustState; trust: Trust | undefined; onChanged: () => void }) {
  const appealing = pendingOf(trust, "appeal");
  const agent = !!currentAgentSession();
  const down = turnedDown(state);
  return (
    <>
      {state.status === "in_review" && (
        <div className="notice" role="status">
          <strong>With Taskiem for a check.</strong> {pageStatus("in_review").help} You can keep changing your draft meanwhile.
        </div>
      )}
      {state.status === "suspended" && (
        <div className="error" role="status">
          <strong>Your page is suspended.</strong> {state.suspended_reason || "Taskiem took it down."} Customers see “This page is unavailable” and your forms take
          nothing until Taskiem restores it.
        </div>
      )}
      {down && (
        <div className="warning" role="status">
          <strong>Taskiem could not put your page live.</strong> {state.review_note} Change it and publish again, or appeal.
        </div>
      )}
      {state.orders_paused && (
        <div className="warning" role="status">
          <strong>Orders and bookings are paused.</strong> Your page has taken {state.orders_paused}. The forms say you are not taking orders online right now. Change
          plan under <Link className="inline-link" to="/settings/billing">Billing</Link>, or ask Taskiem to verify your business, to take more.
        </div>
      )}
      {(state.status === "suspended" || down) &&
        !agent &&
        (appealing ? (
          <p className="hint">Your appeal of {fmtTime(appealing.created_at)} waits for Taskiem.</p>
        ) : (
          <AppealForm onDone={onChanged} />
        ))}
    </>
  );
}

function AppealForm({ onDone }: { onDone: () => void }) {
  const [message, setMessage] = useState("");
  const [sent, setSent] = useState(false);
  const act = useAction();
  const over = message.length > 1000;
  if (sent) {
    return (
      <div className="notice" role="status">
        Your appeal is with Taskiem. You are told on WhatsApp or Telegram when it is decided.
      </div>
    );
  }
  return (
    <form
      className="sf-appeal"
      onSubmit={(e) => {
        e.preventDefault();
        void act.run(async () => {
          await tidy(post("/v1/storefront/appeal", { message: message.trim() }));
          setSent(true);
          onDone();
        });
      }}
    >
      <Field label="Appeal" hint={<span className={over ? "error-text" : undefined}>{`Say what you changed, or why the page should be live. ${message.length}/1000`}</span>}>
        <textarea rows={3} value={message} onChange={(e) => setMessage(e.target.value)} aria-invalid={over} />
      </Field>
      <ErrorBox error={act.error} />
      <button type="submit" disabled={act.busy || !message.trim() || over}>
        Send appeal
      </button>
    </form>
  );
}

/** Verification, what each level unlocks, the reviews, and asking Taskiem to verify the business. */
export function TrustPanel({ trust, error, onChanged }: { trust: Trust | undefined; error: unknown; onChanged: () => void }) {
  const [asking, setAsking] = useState(false);
  const agent = !!currentAgentSession();
  const pendingBusiness = pendingOf(trust, "business");
  return (
    <section className="card" aria-labelledby="sf-trust">
      <h2 id="sf-trust">Trust</h2>
      <p className="hint">
        What your business has proved to Taskiem, and what waits for a check. Taskiem checks every page's words and prices: no weapons, drugs, fakes, money-doubling
        schemes, or asking for passwords, PINs or bank details.
      </p>
      <ErrorBox error={error} />
      {!trust && !error && <Skeleton rows={2} label="Loading verification…" />}
      {trust && (
        <>
          <ul className="sf-trust-levels" aria-label="Verification">
            {VERIFICATION_LEVELS.map((l) => {
              const ok = trust.verification[l.key];
              return (
                <li key={l.key}>
                  <ToneBadge label={ok ? "Yes" : "Not yet"} tone={ok ? "ok" : "muted"} /> <strong>{l.label}</strong>
                  <div className="hint">
                    {l.how} Unlocks: {l.unlocks}
                  </div>
                </li>
              );
            })}
          </ul>
          {!trust.verification.phone && (
            <p className="hint">
              To publish, link your WhatsApp or Telegram under <Link className="inline-link" to="/account">Account</Link>.
            </p>
          )}
          {!trust.verification.business &&
            !agent &&
            (pendingBusiness ? (
              <div className="notice" role="status">
                You asked Taskiem to verify your business on {fmtTime(pendingBusiness.created_at)}. It waits for a check.
              </div>
            ) : asking ? (
              <VerifyForm
                onClose={() => setAsking(false)}
                onDone={() => {
                  setAsking(false);
                  onChanged();
                }}
              />
            ) : (
              <button onClick={() => setAsking(true)}>Verify your business</button>
            ))}
          {trust.reviews.length > 0 && (
            <>
              <h3>Checks by Taskiem</h3>
              <table>
                <thead>
                  <tr>
                    <th>What</th>
                    <th>Status</th>
                    <th>Asked</th>
                    <th>Note</th>
                  </tr>
                </thead>
                <tbody>
                  {trust.reviews.map((r) => {
                    const s = reviewStatus(r.status);
                    const found = findingsSummary(r.findings);
                    return (
                      <tr key={r.id}>
                        <td>
                          {reviewKind(r.kind)}
                          {r.findings.length > 0 && <div className="hint">{found}</div>}
                        </td>
                        <td>
                          <ToneBadge label={s.label} tone={s.tone} />
                        </td>
                        <td>{fmtTime(r.created_at)}</td>
                        <td>{r.decision_note ?? (r.decided_at ? "—" : "")}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </>
          )}
        </>
      )}
    </section>
  );
}

function VerifyForm({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [f, setF] = useState({ legal_name: "", address: "", cac_number: "", note: "" });
  const act = useAction();
  const set = (k: keyof typeof f, v: string) => setF({ ...f, [k]: v });
  return (
    <form
      className="sf-verify"
      aria-labelledby="sf-verify"
      onSubmit={(e) => {
        e.preventDefault();
        void act.run(async () => {
          await tidy(
            post("/v1/storefront/verification", {
              legal_name: f.legal_name.trim(),
              address: f.address.trim(),
              cac_number: f.cac_number.trim(),
              note: f.note.trim(),
            }),
          );
          onDone();
        });
      }}
    >
      <h3 id="sf-verify">Verify your business</h3>
      <p className="hint">A person at Taskiem checks these, usually within a working day. You are told on WhatsApp or Telegram.</p>
      <Field label="Legal name" hint="As registered, or your own name for a sole trader.">
        <input value={f.legal_name} onChange={(e) => set("legal_name", e.target.value)} required maxLength={200} autoComplete="organization" />
      </Field>
      <Field label="Address" hint="Where the business is.">
        <input value={f.address} onChange={(e) => set("address", e.target.value)} required maxLength={300} autoComplete="street-address" />
      </Field>
      <Field label="CAC number (optional)" hint="Such as RC 1234567 or BN 1234567.">
        <input value={f.cac_number} onChange={(e) => set("cac_number", e.target.value)} maxLength={20} autoComplete="off" />
      </Field>
      <Field label="Note (optional)" hint={`Anything that helps the check. ${f.note.length}/500`}>
        <textarea rows={2} value={f.note} onChange={(e) => set("note", e.target.value)} maxLength={500} />
      </Field>
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button type="submit" className="primary" disabled={act.busy || !f.legal_name.trim() || !f.address.trim()}>
          Ask Taskiem to verify
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </form>
  );
}
