import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { get, put } from "../api";
import {
  groupRows,
  type Choice,
  type NotificationSetting,
} from "../lib/notifications";
import { ErrorBox, Items, PageHeader, Row, useAction, useLoad } from "../ui";
import { onPrefs, readPrefs, writePrefs, playPing } from "../lib/chatNotify";

/**
 * How a person hears beyond the app (no-technical-steps N12). The bell
 * always shows everything; chat is the WhatsApp or Telegram they linked,
 * and email is used when they linked neither. The money group (people who
 * can see invoices) is the daily money summary on chat and the monthly
 * money email. A group this page does not know is shown with plain labels.
 */
/**
 * Chat on this computer: a notification, and a short sound, when a business
 * writes while the tab is in the background. Each is a choice of this
 * browser, off until chosen; the browser has its own say on notifications.
 */
function ChatOnThisComputer() {
  const [prefs, setPrefs] = useState(readPrefs);
  useEffect(() => onPrefs(() => setPrefs(readPrefs())), []);
  const supported = typeof Notification !== "undefined";
  const [permission, setPermission] = useState<
    NotificationPermission | "unsupported"
  >(supported ? Notification.permission : "unsupported");
  const setBrowser = async (on: boolean) => {
    if (on && supported && Notification.permission === "default")
      setPermission(await Notification.requestPermission());
    const allowed = !supported ? false : Notification.permission === "granted";
    writePrefs({ ...readPrefs(), browser: on && allowed, asked: true });
  };
  return (
    <section aria-label="Chat on this computer">
      <h2>Chat on this computer</h2>
      <Items label="Chat on this computer">
        <Row
          icon="chat"
          title="When a business writes to you"
          sub={
            <span className="sub">
              For this browser only. Nothing is shown for a chat you muted or
              the one you are looking at, and a document says only what it is.
            </span>
          }
          testid="chat-notify-setting"
        >
          <label className="switch-line">
            <input
              type="checkbox"
              aria-label="Show a notification while this tab is in the background"
              checked={prefs.browser && permission === "granted"}
              disabled={!supported || permission === "denied"}
              onChange={(e) => void setBrowser(e.target.checked)}
            />
            <span>Show a notification while this tab is in the background</span>
          </label>
          {permission === "denied" && (
            <span className="hint">
              Your browser has blocked notifications from Taskiem. Allow them in
              the browser's settings for this site, then come back.
            </span>
          )}
          {!supported && (
            <span className="hint">
              This browser cannot show notifications.
            </span>
          )}
          <label className="switch-line">
            <input
              type="checkbox"
              aria-label="Play a short sound for a new message"
              checked={prefs.sound}
              onChange={(e) => {
                writePrefs({ ...readPrefs(), sound: e.target.checked });
                if (e.target.checked) playPing();
              }}
            />
            <span>Play a short sound for a new message</span>
          </label>
        </Row>
      </Items>
    </section>
  );
}

export function NotificationSettings() {
  const view = useLoad(
    () =>
      get<{ settings: NotificationSetting[] }>("/v1/me/notification-settings"),
    [],
  );
  const act = useAction();
  const save = (next: NotificationSetting) =>
    void act.run(
      async () => (
        await put("/v1/me/notification-settings", { settings: [next] }),
        view.reload()
      ),
      "Saved",
    );
  const box = (
    c: Choice,
    fallback: string,
    checked: boolean,
    change: (on: boolean) => void,
  ) => (
    <label className="switch-line">
      <input
        type="checkbox"
        aria-label={c.label}
        checked={checked}
        disabled={act.busy}
        onChange={(e) => change(e.target.checked)}
      />
      <span>{c.caption ?? fallback}</span>
    </label>
  );
  return (
    <>
      <PageHeader
        title="Notifications"
        description="What you hear beyond the bell. The bell always shows everything."
      />
      <ErrorBox error={view.error ?? act.error} />
      <section>
        <Items label="Notifications">
          {groupRows(view.data?.settings).map((r) => (
            <Row
              key={r.setting.group}
              icon="bell"
              title={r.title}
              sub={r.hint ? <span className="sub">{r.hint}</span> : undefined}
              testid="notification-setting"
            >
              {box(r.chat, "On WhatsApp or Telegram", r.setting.chat, (on) =>
                save({ ...r.setting, chat: on }),
              )}
              {box(r.email, "By email", r.setting.email, (on) =>
                save({ ...r.setting, email: on }),
              )}
            </Row>
          ))}
        </Items>
        <p className="hint">
          Messages go to the WhatsApp or Telegram you linked under{" "}
          <Link to="/account">Account</Link>, at most once an hour for the same
          workflow. Email is used for what did not go there.
        </p>
      </section>
      <ChatOnThisComputer />
    </>
  );
}
