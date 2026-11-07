import { useEffect, useState } from "react";
import { del, get, patch } from "../api";
import { useAuth } from "../auth";
import {
  browserEnv,
  disablePush,
  enablePush,
  myDevice,
  pushSupport,
  serverKey,
  thisSubscription,
  type PushView,
  type PushWhy,
} from "../lib/push";
import { ErrorBox, fmtTime, Items, Row, useAction } from "../ui";

// Chat when Taskiem is closed (docs/spec/chat-push.md §6.6): a notification on
// this phone or computer when a business writes, even with the app closed. It
// shows only where the server has the keys for it, and only to people who write
// in conversations. Each device has its own switch for showing what the
// message says.

const WHY: Record<PushWhy, string> = {
  denied:
    "Notifications are blocked for Taskiem in this browser. Allow them in the browser's settings for this site, then try again.",
  unsupported: "This browser cannot send notifications when Taskiem is closed.",
  off: "Notifications are not set up on this server.",
  failed: "Notifications could not be turned on. Try again in a moment.",
};

export function ChatPush() {
  const { can } = useAuth();
  const [feature, setFeature] = useState<"loading" | "off" | "on">("loading");
  const [view, setView] = useState<PushView>();
  const [subscribed, setSubscribed] = useState(false);
  const [permission, setPermission] = useState<
    NotificationPermission | "unsupported"
  >(
    typeof Notification === "undefined"
      ? "unsupported"
      : Notification.permission,
  );
  const act = useAction();
  const support = pushSupport(browserEnv());

  useEffect(() => {
    let live = true;
    void serverKey().then((k) => live && setFeature(k ? "on" : "off"));
    return () => {
      live = false;
    };
  }, []);
  useEffect(() => {
    if (feature !== "on") return;
    let live = true;
    get<PushView>("/v1/me/push").then(
      (v) => live && setView(v),
      () => undefined,
    );
    void thisSubscription().then((s) => live && setSubscribed(!!s));
    return () => {
      live = false;
    };
  }, [feature]);

  if (feature !== "on" || !can("chat.trade")) return null;
  const mine = view?.devices.find((d) => d.id === myDevice());
  const on = subscribed && !!mine;
  const others = (view?.devices ?? []).filter((d) => d.id !== mine?.id);

  const turnOn = () =>
    void act.run(async () => {
      const r = await enablePush(true);
      setPermission(
        typeof Notification === "undefined"
          ? "unsupported"
          : Notification.permission,
      );
      if (!r.ok) throw new Error(WHY[r.why]);
      setView(r.view);
      setSubscribed(true);
    }, "Notifications are on for this device");
  const turnOff = () =>
    void act.run(async () => {
      const v = await disablePush();
      setView(v ?? (await get<PushView>("/v1/me/push")));
      setSubscribed(false);
    }, "Notifications are off for this device");
  const showWords = (preview: boolean) =>
    void act.run(async () => {
      if (mine)
        setView(await patch<PushView>(`/v1/me/push/${mine.id}`, { preview }));
    }, "Saved");
  const remove = (id: string) =>
    void act.run(
      async () => setView(await del<PushView>(`/v1/me/push/${id}`)),
      "Device removed",
    );

  return (
    <section aria-label="Chat when Taskiem is closed" data-testid="chat-push">
      <h2>Chat when Taskiem is closed</h2>
      <ErrorBox error={act.error} />
      <Items label="Chat when Taskiem is closed">
        <Row
          icon="bell"
          title="Notify me on this device when a business writes"
          sub={
            <span className="sub">
              You get a notification here even when Taskiem is closed. Turn it
              off any time.
            </span>
          }
          testid="chat-push-here"
        >
          {support === "install-first" && (
            <span className="hint" data-testid="chat-push-ios">
              To get notifications on an iPhone, add Taskiem to your Home Screen
              first: tap Share, then Add to Home Screen.
            </span>
          )}
          {support === "no" && <span className="hint">{WHY.unsupported}</span>}
          {support === "yes" && (
            <>
              <label className="switch-line">
                <input
                  type="checkbox"
                  aria-label="Notify me on this device when a business writes"
                  checked={on}
                  disabled={act.busy || permission === "denied"}
                  onChange={(e) => (e.target.checked ? turnOn() : turnOff())}
                  data-testid="chat-push-switch"
                />
                <span>{on ? "On for this device" : "Off for this device"}</span>
              </label>
              {permission === "denied" && (
                <span className="hint">{WHY.denied}</span>
              )}
              {on && mine && (
                <label className="switch-line">
                  <input
                    type="checkbox"
                    aria-label="Show what the message says"
                    checked={mine.preview}
                    disabled={act.busy}
                    onChange={(e) => showWords(e.target.checked)}
                    data-testid="chat-push-preview"
                  />
                  <span>Show what the message says</span>
                </label>
              )}
              {on && mine && !mine.preview && (
                <span className="hint">
                  Off: the notification says only that a business wrote.
                </span>
              )}
              <span className="hint">
                The message is scrambled on its way, so only this device can
                read it. Your browser's maker still sees that a message was
                sent, and when.
              </span>
            </>
          )}
        </Row>
      </Items>
      {others.length > 0 && (
        <>
          <h3>Your other devices</h3>
          <Items label="Your other devices">
            {others.map((d) => (
              <Row
                key={d.id}
                icon="bell"
                title={d.label}
                sub={
                  <span className="sub">
                    {d.last_ok_at
                      ? `Last notified ${fmtTime(d.last_ok_at)}`
                      : "Not notified yet"}
                  </span>
                }
                testid="chat-push-device"
              >
                <button
                  type="button"
                  className="small"
                  disabled={act.busy}
                  onClick={() => remove(d.id)}
                >
                  Remove<span className="sr-only"> {d.label}</span>
                </button>
              </Row>
            ))}
          </Items>
        </>
      )}
    </section>
  );
}
