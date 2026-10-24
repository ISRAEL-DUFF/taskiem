import { Link } from "react-router-dom";
import { get, put } from "../api";
import { groupRows, type Choice, type NotificationSetting } from "../lib/notifications";
import { ErrorBox, PageHeader, useAction, useLoad } from "../ui";

/**
 * How a person hears beyond the app (no-technical-steps N12). The bell
 * always shows everything; chat is the WhatsApp or Telegram they linked,
 * and email is used when they linked neither. The money group (people who
 * can see invoices) is the daily money summary on chat and the monthly
 * money email. A group this page does not know is shown with plain labels.
 */
export function NotificationSettings() {
  const view = useLoad(() => get<{ settings: NotificationSetting[] }>("/v1/me/notification-settings"), []);
  const act = useAction();
  const save = (next: NotificationSetting) => void act.run(async () => (await put("/v1/me/notification-settings", { settings: [next] }), view.reload()), "Saved");
  const box = (c: Choice, checked: boolean, change: (on: boolean) => void) => (
    <label style={{ display: "inline-flex", gap: 8, alignItems: "center" }}>
      <input type="checkbox" aria-label={c.label} checked={checked} disabled={act.busy} onChange={(e) => change(e.target.checked)} />
      {c.caption && <span className="hint">{c.caption}</span>}
    </label>
  );
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
            {groupRows(view.data?.settings).map((r) => (
              <tr key={r.setting.group}>
                <td>
                  <strong>{r.title}</strong>
                  {r.hint && <div className="hint">{r.hint}</div>}
                </td>
                <td>{box(r.chat, r.setting.chat, (on) => save({ ...r.setting, chat: on }))}</td>
                <td>{box(r.email, r.setting.email, (on) => save({ ...r.setting, email: on }))}</td>
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
