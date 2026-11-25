import { useParams, Link } from "react-router-dom";
import { get, patch } from "../api";
import { ErrorBox, Field, Items, PageHeader, Row, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { WAITS, choiceWords, consentWords, contactWords, followupWords, rowStatus, waitWords, whatsappWords, type FormLeft, type FollowupSettings } from "../lib/left";
import { StorefrontTabs } from "./Storefront";

/** Left without ordering (forms that sell §6.6): an order form's
 * follow-ups, this week's counts, and who left it, each masked. */
export function StorefrontLeft() {
  const { id = "" } = useParams();
  const left = useLoad(() => get<FormLeft>(`/v1/storefront/actions/${encodeURIComponent(id)}/left`), [id]);
  const act = useAction();
  const set = (body: { followup_on?: boolean; followup_wait?: number; release_hold?: boolean; followup_whatsapp?: boolean }, done: string) =>
    void act.run(() => patch<{ followups: FollowupSettings }>(`/v1/storefront/actions/${encodeURIComponent(id)}`, body), done).then((ok) => ok && left.reload());
  const d = left.data;
  return (
    <>
      <PageHeader
        title="Left without ordering"
        description="People who typed their phone or email into your order form and left without sending it. Taskiem keeps only how to reach them and what they chose, for 7 days, and sends them one message, by text, email or WhatsApp, with a link to finish."
      />
      <StorefrontTabs />
      <ErrorBox error={left.error} />
      {!d && !left.error && <Skeleton rows={4} label="Loading who left…" />}
      {d && (
        <>
          <section className="panel" aria-labelledby="fu-h" data-testid="followups">
            <h2 id="fu-h">{d.label}</h2>
            {d.followups.held_at && (
              <div className="notice" role="status" data-testid="followups-held">
                Many people left this form at once ({fmtTime(d.followups.held_at)}), so no more messages go out until you release them. Release them only if
                these look like real buyers.{" "}
                <button disabled={act.busy} onClick={() => set({ release_hold: true }, "Follow-ups released")}>
                  Release follow-ups
                </button>
              </div>
            )}
            <label className="sf-toggle">
              <input
                type="checkbox"
                role="switch"
                checked={d.followups.on}
                disabled={act.busy}
                onChange={(e) => set({ followup_on: e.target.checked }, e.target.checked ? "Follow-ups are on" : "Follow-ups are off")}
              />{" "}
              Follow up people who leave without ordering
            </label>
            <p className="hint">
              {d.followups.on
                ? followupWords(d.followups)
                : "Off: your form keeps nothing of people who leave."}
            </p>
            {d.followups.on && (
              <>
                <Field label="Wait before the message">
                  <select value={d.followups.wait_minutes} disabled={act.busy} onChange={(e) => set({ followup_wait: Number(e.target.value) }, "Saved")}>
                    {WAITS.map((m) => (
                      <option key={m} value={m}>
                        {waitWords(m)}
                      </option>
                    ))}
                  </select>
                </Field>
                {d.followups.whatsapp_ready && (
                  <label className="sf-toggle" data-testid="followups-whatsapp">
                    <input
                      type="checkbox"
                      role="switch"
                      checked={!!d.followups.whatsapp_on}
                      disabled={act.busy}
                      onChange={(e) =>
                        set({ followup_whatsapp: e.target.checked }, e.target.checked ? "Follow-ups may go on WhatsApp" : "Follow-ups go by text or email only")
                      }
                    />{" "}
                    Also follow up on WhatsApp from my number
                  </label>
                )}
                <p className="hint">{whatsappWords(d.followups)}</p>
                <p className="hint">Your form says, under their phone and email: {consentWords(d.followups)}</p>
              </>
            )}
            <ErrorBox error={act.error} />
          </section>
          <section className="stats" aria-label="This week" data-testid="left-week">
            <div className="stat">
              <span className="stat-label">Left without ordering this week</span>
              <span className="stat-value">{d.week.left}</span>
            </div>
            <div className="stat">
              <span className="stat-label">Followed up</span>
              <span className="stat-value">{d.week.followed}</span>
            </div>
            <div className="stat">
              <span className="stat-label">Came back</span>
              <span className="stat-value">{d.week.came_back}</span>
            </div>
          </section>
          {d.left.length === 0 ? (
            <p className="hint">Nobody has left this form without ordering in the last 7 days.</p>
          ) : (
            <Items label="People who left">
              {d.left.map((r) => {
                const st = rowStatus(r, !!d.followups.held_at);
                return (
                  <Row
                    key={r.id}
                    icon={r.contact_kind === "email" ? "mail" : "chat"}
                    tone={st.tone === "warn" ? "warn" : st.tone === "ok" ? "ok" : st.tone === "info" ? "info" : "muted"}
                    title={choiceWords(r)}
                    side={<span className="item-figure">{fmtTime(r.at)}</span>}
                    sub={
                      <span className="sub">
                        {contactWords(r)} · {st.words}
                      </span>
                    }
                    testid="left-row"
                  />
                );
              })}
            </Items>
          )}
          <p className="hint">
            <Link to="/storefront/fronts">Your fronts and their order forms</Link>
          </p>
        </>
      )}
    </>
  );
}
