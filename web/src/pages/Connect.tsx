import { useEffect, useState } from "react";
import { useLocation, useParams } from "react-router-dom";
import { get, post, type AuthField, type ConnectorInfo } from "../api";
import { keyProblem, modesOf, splitFields } from "../lib/connections";
import { ErrorBox, Field, Skeleton, useAction } from "../ui";

interface LinkView {
  business: string;
  connector: ConnectorInfo;
  channel: string;
  expires_at: string;
  back?: string;
  mini_app?: boolean;
}

/** The Telegram Mini App's signed data, which Telegram puts in the page's
 * fragment when it opens a web_app button: the server checks it. */
export function telegramInitData(hash: string): string {
  return new URLSearchParams(hash.replace(/^#/, "")).get("tgWebAppData") ?? "";
}

/**
 * A connect link sent from chat (docs/spec/chat-way-in.md 7): one service,
 * one business, no sign-in. A key is typed here, never in the chat, and
 * checked with the service before it is saved; a service that signs in
 * starts its consent. Afterwards the chat carries on.
 */
export function ConnectPage() {
  const { token = "" } = useParams();
  const loc = useLocation();
  const [initData] = useState(() => telegramInitData(window.location.hash));
  const [view, setView] = useState<LinkView>();
  const [gone, setGone] = useState("");
  const [creds, setCreds] = useState<Record<string, string>>({});
  const [mode, setMode] = useState("");
  const [done, setDone] = useState<{ connected: string; back?: string }>();
  const act = useAction();
  useEffect(() => {
    // The fragment holds the Mini App's data: kept in memory, dropped
    // from the address bar.
    if (window.location.hash) window.history.replaceState(null, "", loc.pathname);
    get<LinkView>(`/v1/connect/${token}`).then(setView, (e: Error) => setGone(e.message));
  }, [token]);
  if (gone)
    return (
      <div className="login card">
        <h1>This link has ended</h1>
        <p className="error">{gone}</p>
      </div>
    );
  if (!view) return <Skeleton rows={3} />;
  const c = view.connector;
  if (done) return <Connected service={done.connected} business={view.business} back={done.back} />;
  const modes = modesOf(c);
  const chosen = mode || modes.find((m) => m.available)?.mode || "";
  const { main, more } = splitFields(c);
  const signIn = !!c.auth.oauth2 && !!c.oauth_linkable;
  const problems = Object.fromEntries((c.auth.fields ?? []).map((f) => [f.key, keyProblem(f, creds[f.key] ?? "", chosen)]));
  const blocked = Object.values(problems).some(Boolean);
  const field = (f: AuthField) => (
    <Field
      key={f.key}
      label={f.label + (f.required === false ? " (optional)" : "")}
      hint={
        <>
          {f.help}
          {f.where && (
            <>
              {f.help ? " " : ""}Find it in <strong>{f.where}</strong>
              {f.link && (
                <>
                  {" "}
                  (
                  <a href={f.link} target="_blank" rel="noreferrer">
                    open it
                  </a>
                  )
                </>
              )}
              .
            </>
          )}
          {problems[f.key] && (
            <span className="error-text" role="alert">
              {" "}
              {problems[f.key]?.charAt(0).toUpperCase()}
              {problems[f.key]?.slice(1)}.
            </span>
          )}
        </>
      }
    >
      {f.multiline ? (
        <textarea autoComplete="off" spellCheck={false} rows={5} value={creds[f.key] ?? ""} required={f.required !== false} onChange={(e) => setCreds({ ...creds, [f.key]: e.target.value.trim() })} />
      ) : (
        <input
          type={f.secret ? "password" : "text"}
          autoComplete="off"
          spellCheck={false}
          value={creds[f.key] ?? ""}
          required={f.required !== false}
          aria-invalid={!!problems[f.key]}
          onChange={(e) => setCreds({ ...creds, [f.key]: e.target.value.trim() })}
        />
      )}
    </Field>
  );
  return (
    <div className="login card" data-testid="connect-page">
      <h1>
        Connect {c.name} to {view.business}
      </h1>
      {signIn ? (
        <>
          <p>You sign in to {c.name} and approve; nothing is copied. Taskiem never sees your password.</p>
          <ErrorBox error={act.error} />
          <button
            className="primary"
            style={{ width: "100%" }}
            disabled={act.busy}
            onClick={() =>
              void act.run(async () => {
                const { url } = await post<{ url: string }>(`/v1/connect/${token}/oauth`, { mode: chosen || undefined });
                window.location.assign(url);
              }, false)
            }
          >
            Continue to {c.name}
          </button>
          <p className="hint">If {c.name} says it cannot sign you in here, open this page in your phone's browser (Chrome or Safari) and try again.</p>
        </>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => {
              const out = await post<{ connected: string; back?: string }>(`/v1/connect/${token}`, {
                credentials: creds,
                mode: chosen || undefined,
                init_data: initData || undefined,
              });
              setDone(out);
            }, false);
          }}
        >
          <p className="hint">Copy each value from {c.name} and paste it here. Taskiem checks it with {c.name} before saving it, and it never appears in your chat.</p>
          {modes.length > 1 && (
            <fieldset className="choice">
              <legend>Which kind?</legend>
              {modes.map((m) => (
                <label key={m.mode} className="inline">
                  <input type="radio" name="mode" checked={chosen === m.mode} disabled={!m.available} onChange={() => setMode(m.mode)} />
                  {m.label}
                </label>
              ))}
            </fieldset>
          )}
          {main.map(field)}
          {more.length > 0 && (
            <details>
              <summary>More options</summary>
              {more.map(field)}
            </details>
          )}
          <ErrorBox error={act.error} />
          <button className="primary" type="submit" disabled={act.busy || blocked} style={{ width: "100%" }}>
            {act.busy ? "Checking…" : `Connect ${c.name}`}
          </button>
        </form>
      )}
      <p className="hint">This link works once, until {new Date(view.expires_at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}.</p>
    </div>
  );
}

function Connected({ service, business, back }: { service: string; business: string; back?: string }) {
  return (
    <div className="login card" role="status" data-testid="connect-done">
      <h1>{service} is connected</h1>
      <p>
        {service} is connected to {business}. Taskiem has told your chat, and carries on there.
      </p>
      {back && (
        <p>
          <a className="button primary" href={back}>
            Back to the chat
          </a>
        </p>
      )}
    </div>
  );
}

/** Where a consent started from a connect link ends: connected, or what
 * went wrong (the link works again until its time is up). */
export function ConnectDone() {
  const q = new URLSearchParams(useLocation().search);
  const service = q.get("service") ?? "The service";
  const back = q.get("back") ?? undefined;
  const error = q.get("error");
  if (!error) return <Connected service={service} business="your business" back={back?.startsWith("https://") ? back : undefined} />;
  return (
    <div className="login card" role="alert">
      <h1>{service} was not connected</h1>
      <p className="error">{error}</p>
      <p>Open the link in your chat again to try once more, while it lasts.</p>
    </div>
  );
}
