import { useState } from "react";
import { del, get, put, type WorkflowSummary } from "../api";
import { useAuth } from "../auth";
import { ErrorBox, Field, fmtTime, useAction, useLoad } from "../ui";

interface NumberView {
  connected: boolean;
  phone_number_id?: string;
  waba_id?: string;
  display_number?: string;
  own_app: boolean;
  status?: string;
  public_menu: boolean;
  webhook_path?: string;
  flows_endpoint_path?: string;
  public_workflows: { workflow_id: string; label: string; name?: string }[];
  flows_enabled: boolean;
  shared_number?: string;
  created_at?: string;
  flows_key_error?: string;
}

/** The organisation's own WhatsApp number (spec 11.4): people hear from it
 * instead of the shared number, and it can carry a public menu. */
export function WhatsAppNumber() {
  const { can } = useAuth();
  const view = useLoad(() => get<NumberView>("/v1/whatsapp/number"), []);
  const [form, setForm] = useState({ phone_number_id: "", waba_id: "", access_token: "", app_secret: "", verify_token: "", own_app: true });
  const [notice, setNotice] = useState("");
  const act = useAction();
  const v = view.data;
  if (!v) return view.error ? <ErrorBox error={view.error} /> : null;
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm({ ...form, [k]: e.target.type === "checkbox" ? e.target.checked : e.target.value });
  return (
    <section className="card" data-testid="whatsapp-own-number">
      <h2 style={{ marginTop: 0 }}>WhatsApp number</h2>
      <p className="hint">
        Messages to your people come from {v.shared_number ? <strong>{v.shared_number}</strong> : "Taskiem's shared number"} with your organisation's name in
        brackets, unless you connect your own WhatsApp Business number. Template messages count against your plan's allowance.
      </p>
      <ErrorBox error={act.error} />
      {notice && <p className="hint">{notice}</p>}
      {v.connected ? (
        <>
          <p>
            Connected: <strong>{v.display_number}</strong> <span className="hint">(phone number id {v.phone_number_id}, WABA {v.waba_id}{v.created_at && `, since ${fmtTime(v.created_at)}`})</span>
          </p>
          <p className="hint">
            In your Meta app, set the webhook callback to <code>{v.webhook_path}</code> and the WhatsApp Flows endpoint to <code>{v.flows_endpoint_path}</code> on
            Taskiem's channel address{v.own_app ? ", with the verify token you entered" : ""}. Publish the <code>taskiem_inputs</code> and <code>taskiem_pin</code> Flows
            and the templates in your WhatsApp Business Account.
          </p>
          <button className="danger" disabled={act.busy} onClick={() => confirm("Disconnect this number? Messages go from the shared number again.") && void act.run(async () => (await del("/v1/whatsapp/number"), view.reload()))}>
            Disconnect
          </button>
          {can("workflow.publish") && <PublicMenu view={v} onSaved={view.reload} />}
        </>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => {
              const out = await put<NumberView>("/v1/whatsapp/number", form);
              setNotice(out.flows_key_error ?? "");
              setForm({ ...form, access_token: "", app_secret: "", verify_token: "" });
              view.reload();
            });
          }}
        >
          <div className="row">
            <Field label="Phone number id">
              <input value={form.phone_number_id} onChange={set("phone_number_id")} inputMode="numeric" required />
            </Field>
            <Field label="WhatsApp Business Account id">
              <input value={form.waba_id} onChange={set("waba_id")} inputMode="numeric" required />
            </Field>
          </div>
          <Field label="System user access token" hint="With whatsapp_business_messaging; kept in your vault">
            <input type="password" value={form.access_token} onChange={set("access_token")} autoComplete="off" required />
          </Field>
          <label className="row">
            <input type="checkbox" checked={form.own_app} onChange={set("own_app")} /> The number is on our own Meta app
          </label>
          {form.own_app && (
            <div className="row">
              <Field label="App secret">
                <input type="password" value={form.app_secret} onChange={set("app_secret")} autoComplete="off" required />
              </Field>
              <Field label="Verify token" hint="Any random string of 16 or more characters">
                <input type="password" value={form.verify_token} onChange={set("verify_token")} autoComplete="off" required />
              </Field>
            </div>
          )}
          <button className="primary" type="submit" disabled={act.busy}>
            Connect
          </button>
        </form>
      )}
    </section>
  );
}

/** The public self-service menu: workflows anyone may start by writing to the number. */
function PublicMenu({ view, onSaved }: { view: NumberView; onSaved: () => void }) {
  const wfs = useLoad(() => get<{ workflows: WorkflowSummary[] }>("/v1/workflows"), []);
  const [enabled, setEnabled] = useState(view.public_menu);
  const [items, setItems] = useState(view.public_workflows.map((p) => ({ workflow_id: p.workflow_id, label: p.label })));
  const act = useAction();
  const chosen = new Set(items.map((i) => i.workflow_id));
  return (
    <div data-testid="whatsapp-public-menu">
      <h3>Public menu</h3>
      <p className="hint">
        Anyone who writes to this number without a linked account sees these workflows and can start them, after filling in a WhatsApp form and confirming. They
        run as <code>whatsapp_public:&lt;number&gt;</code> under your plan limits, with strict rate limits. Offer only what is safe for anyone to start.
      </p>
      <ErrorBox error={act.error ?? wfs.error} />
      <label className="row">
        <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} /> Offer the menu
      </label>
      {items.map((it, i) => (
        <div className="row" key={it.workflow_id}>
          <span className="grow">{wfs.data?.workflows.find((w) => w.id === it.workflow_id)?.name ?? it.workflow_id}</span>
          <input value={it.label} maxLength={24} onChange={(e) => setItems(items.map((x, j) => (j === i ? { ...x, label: e.target.value } : x)))} />
          <button onClick={() => setItems(items.filter((_, j) => j !== i))}>Remove</button>
        </div>
      ))}
      {items.length < 9 && (
        <select
          value=""
          onChange={(e) => {
            const w = wfs.data?.workflows.find((x) => x.id === e.target.value);
            if (w) setItems([...items, { workflow_id: w.id, label: w.name.slice(0, 24) }]);
          }}
        >
          <option value="">Add a workflow…</option>
          {wfs.data?.workflows
            .filter((w) => !chosen.has(w.id))
            .map((w) => (
              <option key={w.id} value={w.id}>
                {w.name}
              </option>
            ))}
        </select>
      )}
      <div className="toolbar" style={{ marginTop: 8 }}>
        <button className="primary" disabled={act.busy} onClick={() => void act.run(async () => (await put("/v1/whatsapp/public-menu", { enabled, workflows: items }), onSaved()))}>
          Save menu
        </button>
      </div>
    </div>
  );
}
