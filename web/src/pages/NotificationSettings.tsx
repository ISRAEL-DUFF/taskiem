import { Link } from "react-router-dom";
import { get, put } from "../api";
import { groupRows, type Choice, type NotificationSetting } from "../lib/notifications";
import { ErrorBox, Items, PageHeader, Row, useAction, useLoad } from "../ui";

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
  const box = (c: Choice, fallback: string, checked: boolean, change: (on: boolean) => void) => (
    <label className="switch-line">
      <input type="checkbox" aria-label={c.label} checked={checked} disabled={act.busy} onChange={(e) => change(e.target.checked)} />
      <span>{c.caption ?? fallback}</span>
    </label>
  );
  return (
    <>
      <PageHeader title="Notifications" description="What you hear beyond the bell. The bell always shows everything." />
      <ErrorBox error={view.error ?? act.error} />
      <section>
        <Items label="Notifications">
          {groupRows(view.data?.settings).map((r) => (
            <Row key={r.setting.group} icon="bell" title={r.title} sub={r.hint ? <span className="sub">{r.hint}</span> : undefined} testid="notification-setting">
              {box(r.chat, "On WhatsApp or Telegram", r.setting.chat, (on) => save({ ...r.setting, chat: on }))}
              {box(r.email, "By email", r.setting.email, (on) => save({ ...r.setting, email: on }))}
            </Row>
          ))}
        </Items>
        <p className="hint">
          Messages go to the WhatsApp or Telegram you linked under <Link to="/account">Account</Link>, at most once an hour for the same workflow. Email is used for
          what did not go there.
        </p>
      </section>
    </>
  );
}
