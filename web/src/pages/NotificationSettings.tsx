import { Link } from "react-router-dom";
import { get, put } from "../api";
import { ErrorBox, PageHeader, useAction, useLoad } from "../ui";

interface Setting {
  group: "failures" | "setup" | "finished";
  chat: boolean;
  email: boolean;
}

const GROUPS: Record<Setting["group"], { title: string; hint: string }> = {
  failures: { title: "Failed runs", hint: "A run failed, or needs a person to check something. Owners, whoever started it and whoever published it." },
  setup: { title: "Setup problems", hint: "Something Taskiem needs you to fix: a domain, a connection, your data tables. Owners." },
  finished: { title: "Your runs that finish", hint: "A run you started yourself finished." },
};

/**
 * How a person hears beyond the app (no-technical-steps N12). The bell
 * always shows everything; chat is the WhatsApp or Telegram they linked,
 * and email is used when they linked neither.
 */
export function NotificationSettings() {
  const view = useLoad(() => get<{ settings: Setting[] }>("/v1/me/notification-settings"), []);
  const act = useAction();
  const save = (next: Setting) => void act.run(async () => (await put("/v1/me/notification-settings", { settings: [next] }), view.reload()), "Saved");
  return (
    <>
      <PageHeader title="Notifications" description="What you hear beyond the bell. The bell always shows everything." />
      <ErrorBox error={view.error ?? act.error} />
      <section className="card">
        <table className="table">
          <thead>
            <tr>
              <th>When</th>
              <th>On WhatsApp or Telegram</th>
              <th>By email</th>
            </tr>
          </thead>
          <tbody>
            {(view.data?.settings ?? []).map((s) => (
              <tr key={s.group}>
                <td>
                  <strong>{GROUPS[s.group].title}</strong>
                  <div className="hint">{GROUPS[s.group].hint}</div>
                </td>
                <td>
                  <input type="checkbox" aria-label={`${GROUPS[s.group].title} on WhatsApp or Telegram`} checked={s.chat} disabled={act.busy} onChange={(e) => save({ ...s, chat: e.target.checked })} />
                </td>
                <td>
                  <input type="checkbox" aria-label={`${GROUPS[s.group].title} by email`} checked={s.email} disabled={act.busy} onChange={(e) => save({ ...s, email: e.target.checked })} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <p className="hint">
          Messages go to the WhatsApp or Telegram you linked under <Link to="/account">Account</Link>, at most once an hour for the same workflow. Email is used for
          what did not go there.
        </p>
      </section>
    </>
  );
}
