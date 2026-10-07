import { useState } from "react";
import { del, get, post, put } from "../api";
import { byokRequest, healthLine, missingFields, providerFields, providerNames, versionState, type FieldSpec, type KeysView, type Provider } from "../lib/keys";
import { useStepUp, type StepUpProof } from "../stepup";
import { Badge, ErrorBox, Field, fmtTime, useAction, useLoad } from "../ui";

/** Sends a key change, asking for step-up when the server needs it. */
type KeyRequest = <T>(title: string, send: (p: StepUpProof) => Promise<T>) => Promise<T>;

/** Settings > Encryption keys (docs/byok.md): the tenant key, rotation, and
 * bring your own key. Credentials go in once and are never shown again.
 * Every change is confirmed with a passkey or authenticator code. */
export function Keys() {
  const view = useLoad(() => get<KeysView>("/v1/keys"), []);
  const act = useAction();
  const step = useStepUp();
  const [notice, setNotice] = useState("");
  const v = view.data;
  if (!v) return view.error ? <ErrorBox error={view.error} /> : <div className="empty">Loading…</div>;
  const st = v.status;
  const run = (fn: () => Promise<string>) => void act.run(async () => (setNotice(await fn()), view.reload()));
  return (
    <div>
      <h1>Encryption keys</h1>
      {step.modal}
      <p className="hint">
        Every secret, connection credential and piece of personal data is encrypted with its own data key, under your organisation's tenant key. The tenant
        key is wrapped by Taskiem's key{st.mode === "customer" ? " and by your own key" : ""}.
      </p>
      <ErrorBox error={act.error} />
      {notice && (
        <p className="hint" role="status">
          {notice}
        </p>
      )}

      <section className="card" data-testid="keys-summary">
        <h2 style={{ marginTop: 0 }}>Tenant key</h2>
        <p>
          Version <strong>{st.current_version || "—"}</strong>, wrapped by {st.mode === "customer" ? "your key and Taskiem's" : "Taskiem's key"}.
        </p>
        {st.rewrap &&
          (st.rewrap.phase === "destroy" ? (
            <p className="hint">Older versions are retired; their material is destroyed after {fmtTime(st.rewrap.not_before)}.</p>
          ) : (
            <p className="hint" data-testid="keys-rewrap">
              Re-wrapping in the background: {st.rewrap.secrets} data keys and {st.rewrap.subject_keys} personal-data keys left.
              {st.rewrap.last_error && <> Last error: {st.rewrap.last_error}</>}
            </p>
          ))}
        {st.parked_steps > 0 && (
          <p className="error" role="alert">
            {st.parked_steps} step(s) are paused waiting for the key.
          </p>
        )}
        <div className="toolbar">
          <button
            disabled={act.busy}
            onClick={() =>
              confirm("Rotate the tenant key? Data keys are re-wrapped under a new version in the background; nothing is re-encrypted.") &&
              run(
                async () =>
                  `Version ${(await step.request("rotate the tenant key", (p) => post<{ version: number }>("/v1/keys/rotate", p))).version} is current; re-wrapping runs in the background.`,
              )
            }
          >
            Rotate tenant key
          </button>
        </div>
        <table>
          <thead>
            <tr>
              <th>Version</th>
              <th>Wrapped by</th>
              <th>State</th>
              <th>Data keys</th>
              <th>Personal-data keys</th>
              <th>Created</th>
            </tr>
          </thead>
          <tbody>
            {st.versions.map((k) => (
              <tr key={k.version}>
                <td>{k.version}</td>
                <td>{k.wrapped_by === "customer" ? "your key + Taskiem" : "Taskiem"}</td>
                <td>{versionState(k, st.current_version)}</td>
                <td>{k.secrets}</td>
                <td>{k.subject_keys}</td>
                <td>{fmtTime(k.created_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section className="card" data-testid="keys-byok">
        <h2 style={{ marginTop: 0 }}>Bring your own key</h2>
        {st.byok ? (
          <>
            <p>
              <strong>{st.byok.description}</strong> <Badge value={st.byok.status} />
            </p>
            <p className="hint">{healthLine(st.byok)}</p>
            {st.byok.last_error && st.byok.status !== "active" && <p className="error">{st.byok.last_error}</p>}
            <p className="hint">
              Credentials {st.byok.credentials_digest} (fingerprint), added by {st.byok.created_by} on {fmtTime(st.byok.created_at)}. Unwrapped tenant keys are kept
              in memory for at most {Math.round(st.cache_ttl_seconds / 60)} minutes: revoking or disabling your key stops Taskiem within that time, and every
              step that needs a secret pauses until you allow it again.
            </p>
            <div className="toolbar">
              <button
                disabled={act.busy}
                onClick={() =>
                  run(async () => {
                    const out = await post<{ health: { ok: boolean; error?: string }; resumed_steps?: number }>("/v1/keys/byok/check");
                    return out.health.ok ? `Your key works${out.resumed_steps ? `; resumed ${out.resumed_steps} paused steps` : ""}.` : `Your key does not work: ${out.health.error}`;
                  })
                }
              >
                Check now
              </button>
              <button
                className="danger"
                disabled={act.busy}
                onClick={() =>
                  confirm("Return to Taskiem's key? Your data is re-wrapped under a new tenant key that only Taskiem's key wraps. Your key must stay available until that finishes.") &&
                  run(
                    async () =>
                      `Version ${(await step.request("return to Taskiem's key", (p) => del<{ version: number }>("/v1/keys/byok", p))).version} is current; your key is no longer used once re-wrapping finishes.`,
                  )
                }
              >
                Return to Taskiem's key
              </button>
            </div>
            <Credentials provider={st.byok.provider} request={step.request} onSaved={(msg) => run(async () => msg)} />
          </>
        ) : v.plan_allows_byok ? (
          <Onboard providers={v.providers} request={step.request} onDone={(msg) => run(async () => msg)} />
        ) : (
          <p className="hint">Your plan does not include bringing your own key. It is part of the Enterprise plan (Settings &gt; Billing).</p>
        )}
        {(st.retired_byok ?? []).length > 0 && (
          <p className="hint">Earlier keys: {(st.retired_byok ?? []).map((k) => `${k.description} (retired ${fmtTime(k.retired_at)})`).join("; ")}</p>
        )}
      </section>
    </div>
  );
}

function Inputs({ fields, values, set }: { fields: FieldSpec[]; values: Record<string, string>; set: (k: string, v: string) => void }) {
  return (
    <>
      {fields.map((f) => (
        <Field key={f.key} label={f.label + (f.optional ? " (optional)" : "")} hint={f.hint}>
          {f.multiline ? (
            <textarea rows={4} value={values[f.key] ?? ""} placeholder={f.placeholder} onChange={(e) => set(f.key, e.target.value)} autoComplete="off" spellCheck={false} />
          ) : (
            <input type={f.secret ? "password" : "text"} value={values[f.key] ?? ""} placeholder={f.placeholder} onChange={(e) => set(f.key, e.target.value)} autoComplete="off" />
          )}
        </Field>
      ))}
    </>
  );
}

/** Onboarding: Taskiem wraps and unwraps a test value with the key before
 * anything is saved. */
function Onboard({ providers, request, onDone }: { providers: Provider[]; request: KeyRequest; onDone: (msg: string) => void }) {
  const [provider, setProvider] = useState<Provider>(providers[0] ?? "vault_transit");
  const [auth, setAuth] = useState<"token" | "approle">("token");
  const [values, setValues] = useState<Record<string, string>>({});
  const act = useAction();
  const f = providerFields(provider, auth);
  const missing = missingFields(provider, values, auth);
  const set = (k: string, v: string) => setValues({ ...values, [k]: v });
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void act.run(async () => {
          const out = await request("use your own key", (p) => put<{ version: number }>("/v1/keys/byok", { ...byokRequest(provider, values, auth), ...p }));
          setValues({});
          onDone(`Your key is in use: tenant key version ${out.version} is wrapped by it. Existing data keys are re-wrapped in the background.`);
        });
      }}
    >
      <p className="hint">
        Your tenant key will be wrapped by a key in your own KMS. Taskiem keeps the key's address and the credentials it uses (sealed by Taskiem's own key,
        never shown again, never available to workflows). If you revoke or disable the key, Taskiem can no longer read your secrets or personal data: runs
        pause rather than fail, and continue when you allow it again. Give Taskiem a credential that can only encrypt and decrypt with this one key.
      </p>
      <ErrorBox error={act.error} />
      <Field label="Key service">
        <select value={provider} onChange={(e) => (setProvider(e.target.value as Provider), setValues({}))}>
          {providers.map((p) => (
            <option key={p} value={p}>
              {providerNames[p]}
            </option>
          ))}
        </select>
      </Field>
      {provider === "vault_transit" && (
        <Field label="Sign in with">
          <select value={auth} onChange={(e) => setAuth(e.target.value as "token" | "approle")}>
            <option value="token">A token</option>
            <option value="approle">AppRole</option>
          </select>
        </Field>
      )}
      <Inputs fields={[...f.config, ...f.credentials]} values={values} set={set} />
      <button className="primary" type="submit" disabled={act.busy || missing.length > 0} title={missing.length ? `Missing: ${missing.join(", ")}` : undefined}>
        {act.busy ? "Checking the key…" : "Verify and use this key"}
      </button>
    </form>
  );
}

/** New credentials for the key in use; they must reach the same key. */
function Credentials({ provider, request, onSaved }: { provider: Provider; request: KeyRequest; onSaved: (msg: string) => void }) {
  const [open, setOpen] = useState(false);
  const [auth, setAuth] = useState<"token" | "approle">("token");
  const [values, setValues] = useState<Record<string, string>>({});
  const act = useAction();
  if (!open) return <button onClick={() => setOpen(true)}>Replace credentials</button>;
  const fields = providerFields(provider, auth).credentials;
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void act.run(async () => {
          const body = byokRequest(provider, values, auth).credentials;
          const out = await request("replace the key's credentials", (p) => put<{ health?: { ok: boolean } }>("/v1/keys/byok/credentials", { credentials: body, ...p }));
          setValues({});
          setOpen(false);
          onSaved(out.health?.ok ? "New credentials saved; your key works." : "New credentials saved.");
        });
      }}
    >
      <h3>Replace credentials</h3>
      <ErrorBox error={act.error} />
      {provider === "vault_transit" && (
        <Field label="Sign in with">
          <select value={auth} onChange={(e) => setAuth(e.target.value as "token" | "approle")}>
            <option value="token">A token</option>
            <option value="approle">AppRole</option>
          </select>
        </Field>
      )}
      <Inputs fields={fields} values={values} set={(k, v) => setValues({ ...values, [k]: v })} />
      <div className="toolbar">
        <button className="primary" type="submit" disabled={act.busy}>
          Verify and save
        </button>
        <button type="button" onClick={() => setOpen(false)}>
          Cancel
        </button>
      </div>
    </form>
  );
}
