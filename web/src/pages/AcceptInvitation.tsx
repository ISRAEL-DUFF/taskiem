import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { post } from "../api";
import { useAuth } from "../auth";
import { createFrom, passkeysSupported, type CreationJSON } from "../passkeys";
import { ErrorBox, Field, useAction } from "../ui";

interface LinkOptions {
  tenant_id: string;
  tenant: string;
  roles: string[];
  email: string;
  publicKey: CreationJSON;
}

/** The page an emailed invitation link opens, for someone with no account
 * yet: they create a passkey and are a member, signed in (decision 0030). */
export function AcceptInvitation() {
  const [token] = useState(() => new URLSearchParams(window.location.hash.slice(1)).get("token") ?? "");
  // Keep the token out of the address bar and history from here on.
  useEffect(() => {
    if (window.location.hash) window.history.replaceState(null, "", window.location.pathname);
  }, []);
  const { refresh } = useAuth();
  const nav = useNavigate();
  const [invite, setInvite] = useState<LinkOptions | null>(null);
  const [name, setName] = useState("");
  const load = useAction();
  const accept = useAction();
  useEffect(() => {
    // Once, for this token.
    if (token) void load.run(async () => setInvite(await post<LinkOptions>("/v1/auth/invitation/options", { token })));
  }, [token]); // not load: it is a new object every render
  const join = () =>
    accept.run(async () => {
      // A fresh challenge each time: one is good for one attempt.
      const o = await post<LinkOptions>("/v1/auth/invitation/options", { token });
      const credential = await createFrom(o.publicKey);
      await post("/v1/auth/invitation/accept", { token, credential, name, passkey_name: "Passkey" });
      await refresh();
      nav("/home", { replace: true });
    });
  return (
    <div className="login card">
      <h1>Accept your invitation</h1>
      {!token ? (
        <p className="error">This page needs the link from your invitation email.</p>
      ) : !invite ? (
        load.error ? (
          <>
            <ErrorBox error={load.error} />
            <p>
              <Link to="/login">Sign in</Link>
            </p>
          </>
        ) : (
          <p role="status">Checking the link…</p>
        )
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void join();
          }}
        >
          <p>
            You are invited to <strong>{invite.tenant}</strong> as {invite.roles.join(", ")}.
          </p>
          <p className="hint">You sign in to Taskiem with a passkey: your device's fingerprint, face or screen lock. No password.</p>
          <Field label="Email">
            <input value={invite.email} readOnly />
          </Field>
          <Field label="Your name">
            <input value={name} onChange={(e) => setName(e.target.value)} autoComplete="name" autoFocus />
          </Field>
          {!passkeysSupported() && <p className="error">This browser cannot create passkeys. Open the link in an up-to-date browser.</p>}
          <ErrorBox error={accept.error} />
          <button className="primary" type="submit" disabled={accept.busy || !passkeysSupported()} style={{ width: "100%" }}>
            {accept.busy ? "Creating your passkey…" : "Create a passkey and join"}
          </button>
        </form>
      )}
    </div>
  );
}
