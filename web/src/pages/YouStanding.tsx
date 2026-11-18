// The person's standing with Taskiem's moderators (docs/spec/people-chatting.md
// §8.3, "As built: stage 5a gaps, safety and moderation"): what they
// decided, in plain words, and an appeal of each decision. The notice on
// You says when there is something new; opening this page marks it seen.

import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { get, post } from "../api";
import { actionName, appealWords, standingTitle, type Standing, type StandingAction } from "../lib/peoplesafety";
import { ErrorBox, Field, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";

/** /you/settings/standing. */
export function YouStandingPage() {
  const view = useLoad(() => get<Standing>("/v1/me/standing"), []);
  const s = view.data;
  useEffect(() => {
    if (s && s.unseen > 0) void post("/v1/me/standing/seen").catch(() => undefined);
  }, [s]);
  return (
    <>
      <p className="crumbs">
        <Link to="/you/settings" aria-label="Back to Settings">
          Settings
        </Link>
      </p>
      <PageHeader title="Your standing" description="What Taskiem's moderators decided about how you message people, and how to appeal." />
      {!s && !view.error && <Skeleton rows={2} label="Loading your standing…" />}
      <ErrorBox error={view.error} />
      {s && (
        <>
          <section className="card" aria-labelledby="standing-now" data-testid="standing-now">
            <h2 id="standing-now">{standingTitle(s.state)}</h2>
            <p>{s.words}</p>
          </section>
          {s.actions.length > 0 && (
            <section aria-labelledby="standing-list">
              <h2 id="standing-list">Decisions</h2>
              <p className="hint">{s.appeal_words}</p>
              {s.actions.map((a) => (
                <StandingCard key={a.id} a={a} onDone={view.reload} />
              ))}
            </section>
          )}
          <p className="hint">
            Taskiem also publishes how many <Link to="/transparency">requests from the authorities</Link> it has received.
          </p>
        </>
      )}
    </>
  );
}

function StandingCard({ a, onDone }: { a: StandingAction; onDone: () => void }) {
  const [text, setText] = useState("");
  const [open, setOpen] = useState(false);
  const act = useAction();
  return (
    <article className="card" data-testid="standing-action" aria-label={`${actionName(a.action)}, ${fmtTime(a.at)}`}>
      <h3>
        {actionName(a.action)} <span className="hint">{fmtTime(a.at)}</span>
      </h3>
      <p>Why: {a.reason}</p>
      <p>{a.in_force ? a.words : a.lifted_at ? `Lifted ${fmtTime(a.lifted_at)}.` : "No longer in force."}</p>
      {a.appeal && <p data-testid="standing-appeal">{appealWords(a.appeal)}</p>}
      {a.can_appeal &&
        (open ? (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void act.run(async () => {
                await post("/v1/me/standing/appeals", { action_id: a.id, text: text.trim() });
                onDone();
              }, "Appeal sent");
            }}
          >
            <Field label="Why you think this decision is wrong">
              <textarea value={text} onChange={(e) => setText(e.target.value)} maxLength={2000} rows={4} required />
            </Field>
            <ErrorBox error={act.error} />
            <button className="primary" type="submit" disabled={act.busy || !text.trim()}>
              Send the appeal
            </button>
          </form>
        ) : (
          <button onClick={() => setOpen(true)} data-testid="standing-appeal-open">
            Appeal<span className="sr-only"> the {actionName(a.action).toLowerCase()}</span>
          </button>
        ))}
    </article>
  );
}

/** The notice on You when moderators decided something the person has not seen. */
export function StandingNotice() {
  const view = useLoad(() => get<Standing>("/v1/me/standing").catch(() => undefined), []);
  const s = view.data;
  if (!s || s.unseen === 0) return null;
  return (
    <div className="warning" role="status" data-testid="standing-notice">
      {s.state === "ok" ? "Taskiem's moderators changed a decision about your account" : standingTitle(s.state)}. <Link to="/you/settings/standing">Read what Taskiem's moderators decided</Link>.
    </div>
  );
}
