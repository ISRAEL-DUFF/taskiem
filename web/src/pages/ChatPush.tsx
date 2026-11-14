import { useEffect, useState } from "react";
import { del, get, patch } from "../api";
import { useAuth } from "../auth";
import {
  browserEnv,
  disablePush,
  enablePush,
  findMyDevice,
  KIND_WORDS,
  myDevice,
  pushSupport,
  serverKey,
  thisSubscription,
  type PushDevice,
  type PushKind,
  type PushView,
  type PushWhy,
} from "../lib/push";
import { ErrorBox, fmtTime, Items, Row, useAction } from "../ui";

// Notifications when Taskiem is closed (docs/spec/chat-push.md §6.6,
// docs/spec/mobile-app-m4.md N3): on this phone or computer, even with the app
// closed, whatever the bell tells and every chat message. It shows only where
// the server has the keys for it. Each device has its own switches: which kinds
// it takes, and whether it shows what the message says.

const ALL_KINDS: PushKind[] = [
  "business",
  "customers",
  "orders",
  "approvals",
  "alerts",
];

/** What a device of the list is, in a few words. */
export function deviceKind(d: PushDevice): string {
  if (d.kind !== "fcm") return "Browser";
  return d.platform === "ios"
    ? "Taskiem app on an iPhone"
    : "Taskiem app on Android";
}

const WHY: Record<PushWhy, string> = {
  denied:
    "Notifications are blocked for Taskiem in this browser. Allow them in the browser's settings for this site, then try again.",
  unsupported: "This browser cannot send notifications when Taskiem is closed.",
  off: "Notifications are not set up on this server.",
  failed: "Notifications could not be turned on. Try again in a moment.",
};

export function ChatPush() {
  const { can, me } = useAuth();
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
    void Promise.all([
      get<PushView>("/v1/me/push").catch(() => undefined),
      thisSubscription(),
    ]).then(([v, s]) => {
      if (!live) return;
      if (v) setView(v);
      setSubscribed(!!s);
      // The worker renewed the subscription, or this browser does not know
      // which device it is here: the server finds it by its address.
      const id = myDevice();
      if (s && (!id || (v && !v.devices.some((d) => d.id === id))))
        void findMyDevice().then((f) => live && f && setView(f));
    });
    return () => {
      live = false;
    };
  }, [feature, me?.tenant_id]);

  if (feature !== "on") return null;
  const mine = view?.devices.find((d) => d.id === myDevice());
  // The kinds this person can get at all: business chats for those who
  // write in them, customers for those who answer customers.
  const offered = (view?.kinds ?? ALL_KINDS).filter(
    (k) =>
      (k !== "business" || can("chat.trade")) &&
      (k !== "customers" || can("storefront.manage") || can("order.handle")),
  );
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
  const take = (
    kind: PushKind,
    on: boolean,
    d: PushDevice | undefined = mine,
  ) =>
    void act.run(async () => {
      if (!d) return;
      const now = d.kinds ?? ALL_KINDS;
      const kinds = on ? [...now, kind] : now.filter((k) => k !== kind);
      setView(await patch<PushView>(`/v1/me/push/${d.id}`, { kinds }));
    }, "Saved");
  const remove = (id: string) =>
    void act.run(
      async () => setView(await del<PushView>(`/v1/me/push/${id}`)),
      "Device removed",
    );

  return (
    <section
      aria-label="Notifications when Taskiem is closed"
      data-testid="chat-push"
    >
      <h2>Notifications when Taskiem is closed</h2>
      <ErrorBox error={act.error} />
      <Items label="Notifications when Taskiem is closed">
        <Row
          icon="bell"
          title="Notify me on this device"
          sub={
            <span className="sub">
              Messages, orders and what needs you, here even when Taskiem is
              closed. Turn it off any time.
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
                  role="switch"
                  aria-label="Notify me on this device"
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
                  Off: the notification says only that something came, not what
                  it says.
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
      {on && mine && offered.length > 1 && (
        <>
          <h3>What this device tells you</h3>
          <Items label="What this device tells you">
            {offered.map((k) => (
              <Row
                key={k}
                icon="bell"
                title={KIND_WORDS[k].title}
                sub={<span className="sub">{KIND_WORDS[k].sub}</span>}
                testid={`chat-push-kind-${k}`}
              >
                <label className="switch-line">
                  <input
                    type="checkbox"
                    role="switch"
                    aria-label={KIND_WORDS[k].title}
                    checked={(mine.kinds ?? ALL_KINDS).includes(k)}
                    disabled={act.busy}
                    onChange={(e) => take(k, e.target.checked)}
                  />
                </label>
              </Row>
            ))}
          </Items>
        </>
      )}
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
                    {deviceKind(d)} ·{" "}
                    {d.last_ok_at
                      ? `last notified ${fmtTime(d.last_ok_at)}`
                      : "not notified yet"}
                  </span>
                }
                testid="chat-push-device"
              >
                {offered.length > 1 && (
                  <details data-testid="chat-push-device-kinds">
                    <summary>
                      What it tells you
                      <span className="sr-only"> on {d.label}</span>
                    </summary>
                    {offered.map((k) => (
                      <label className="switch-line" key={k}>
                        <input
                          type="checkbox"
                          role="switch"
                          aria-label={`${KIND_WORDS[k].title} on ${d.label}`}
                          checked={(d.kinds ?? ALL_KINDS).includes(k)}
                          disabled={act.busy}
                          onChange={(e) => take(k, e.target.checked, d)}
                        />
                        <span>{KIND_WORDS[k].title}</span>
                      </label>
                    ))}
                  </details>
                )}
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
