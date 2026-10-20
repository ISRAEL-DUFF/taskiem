import { useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, get, post, put } from "../api";
import { useAuth } from "../auth";
import { ErrorBox, Field, Skeleton, useAction, useLoad } from "../ui";
import { tidy } from "../lib/storefront";
import {
  ACTION_LIMITS,
  FORM_INFO,
  actionForm,
  actionProblem,
  actionRequest,
  formURL,
  paymentChoices,
  setupRequest,
  type ActionForm,
  type FormKind,
  type StorefrontAction,
} from "../lib/orders";

/** The storefront's forms (order, question, call-back, booking): each needs
 * a workflow in prod before it can be switched on; a booking also needs the
 * services table (under Bookings). */
export function StorefrontForms({ previewURL, published }: { previewURL: string; published: boolean }) {
  const actions = useLoad(() => get<{ actions: StorefrontAction[] }>("/v1/storefront/actions"), []);
  return (
    <section className="card sf-links" aria-labelledby="sf-forms">
      <h2 id="sf-forms">Forms</h2>
      <p className="hint">
        Buttons on your page for customers to order, book, ask a question or ask for a call back. Each form runs a workflow, which asks you to accept in
        WhatsApp or Telegram. See what comes in under <Link to="/storefront/orders">Orders</Link>; set your services and booking hours under{" "}
        <Link to="/storefront/bookings">Bookings</Link>.
      </p>
      <ErrorBox error={actions.error} />
      {!actions.data && !actions.error && <Skeleton rows={3} label="Loading your forms…" />}
      <div className="sf-forms">
        {actions.data?.actions.map((a) => (
          <FormCard key={`${a.kind}/${a.workflow_id ?? ""}`} action={a} previewURL={previewURL} published={published} onReload={actions.reload} />
        ))}
      </div>
    </section>
  );
}

function FormCard({ action, previewURL, published, onReload }: { action: StorefrontAction; previewURL: string; published: boolean; onReload: () => void }) {
  const { can } = useAuth();
  const kind = action.kind;
  const info = FORM_INFO[kind];
  const [saved, setSaved] = useState(action);
  const [form, setForm] = useState<ActionForm>(() => actionForm(action));
  const [notice, setNotice] = useState("");
  // A 409 on saving: no workflow is deployed in prod after all.
  const [noWorkflow, setNoWorkflow] = useState(false);
  const act = useAction();
  const setup = useAction();
  const dirty = JSON.stringify(actionRequest(kind, form)) !== JSON.stringify(actionRequest(kind, actionForm(saved)));
  const problem = actionProblem(kind, form);
  const id = `sf-form-${kind}`;
  const canSetup = can("workflow.edit") && can("workflow.publish");
  const set = (patch: Partial<ActionForm>) => {
    setNotice("");
    setForm((f) => ({ ...f, ...patch }));
  };

  const save = () =>
    act.run(async () => {
      setNotice("");
      try {
        const out = await tidy(put<StorefrontAction>(`/v1/storefront/actions/${kind}`, actionRequest(kind, form)));
        setSaved(out);
        setForm(actionForm(out));
        setNoWorkflow(false);
        setNotice(published ? "Saved; your page is updated." : "Saved. It shows on your page once you publish it.");
      } catch (e) {
        if (e instanceof ApiError && e.status === 409) setNoWorkflow(true);
        throw e;
      }
    });

  const setUp = () =>
    setup.run(async () => {
      setNotice("");
      const req = setupRequest(kind);
      const wf = await tidy(post<{ id: string }>(req.path, req.body));
      const p = await tidy(post<{ state: string }>(`/v1/workflows/${wf.id}/versions/1/publish`));
      if (p.state !== "published") {
        setNotice("The workflow waits for a second person to publish it under Approvals. Switch the form on once it is published.");
        return;
      }
      onReload();
    });

  const showSetup = !saved.workflow_id || noWorkflow;
  return (
    <article className="sf-form" aria-labelledby={id}>
      <div className="toolbar" style={{ marginBottom: 6 }}>
        <h3 id={id} className="grow" style={{ margin: 0 }}>
          {info.title}
        </h3>
        <span className={`badge ${saved.enabled ? "ok" : "muted"}`}>{saved.enabled ? "On" : "Off"}</span>
      </div>
      <p className="hint" style={{ marginTop: 0 }}>
        {info.help} Button: “{saved.label}”.{" "}
        {saved.workflow_id ? (
          <>
            <Link to={`/workflows/${saved.workflow_id}`}>
              Open its workflow<span className="sr-only"> for {info.title.toLowerCase()}</span>
            </Link>
            .
          </>
        ) : (
          "No workflow set up yet."
        )}
        {published && saved.enabled && previewURL && (
          <>
            {" "}
            <a href={formURL(previewURL, kind)} target="_blank" rel="noreferrer">
              Open the form<span className="sr-only"> for {info.title.toLowerCase()}</span>
            </a>
          </>
        )}
      </p>
      {notice && (
        <div className="notice" role="status">
          {notice}
        </div>
      )}
      {showSetup && (
        <div className="sf-setup">
          <p style={{ margin: "0 0 8px" }}>
            {kind === "booking"
              ? "Adds a workflow that asks you in WhatsApp or Telegram to confirm each booking, texts and reminds the customer, and asks you afterwards whether they came."
              : "Adds a workflow that asks you in WhatsApp or Telegram to accept each order and texts the customer."}
          </p>
          <ErrorBox error={setup.error} />
          {canSetup ? (
            <button className="primary" disabled={setup.busy} onClick={() => void setUp()}>
              {setup.busy ? "Setting up…" : "Set up"}
              <span className="sr-only"> {info.title.toLowerCase()}</span>
            </button>
          ) : (
            <p className="hint">Setting up a form needs someone who can edit and publish workflows.</p>
          )}
        </div>
      )}
      {saved.workflow_id && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <label className="sf-toggle">
            <input type="checkbox" role="switch" checked={form.enabled} onChange={(e) => set({ enabled: e.target.checked })} />
            Show this form on your page
          </label>
          <Field label="Button label" hint={<span className={form.label.length > ACTION_LIMITS.label ? "error-text" : undefined}>{`${form.label.length}/${ACTION_LIMITS.label}`}</span>}>
            <input value={form.label} onChange={(e) => set({ label: e.target.value })} aria-invalid={form.label.length > ACTION_LIMITS.label} />
          </Field>
          {(kind === "order" || kind === "booking") && <PaymentOptions kind={kind} form={form} set={set} />}
          {kind === "order" && <OrderOptions form={form} set={set} />}
          {kind === "booking" && (
            <p className="hint sf-links">
              Bookings need your services table and booking hours, set under <Link to="/storefront/bookings">Bookings</Link>.
            </p>
          )}
          {problem && <p className="error-text">{problem}</p>}
          <ErrorBox error={act.error} />
          <div className="toolbar" style={{ marginBottom: 0 }}>
            <button type="submit" className="primary" disabled={act.busy || !dirty || !!problem}>
              Save<span className="sr-only"> {info.title.toLowerCase()}</span>
            </button>
          </div>
        </form>
      )}
    </article>
  );
}

/** How the customer pays: for an order or a booking, with the Paystack connection, and a booking's deposit. */
function PaymentOptions({ kind, form, set }: { kind: FormKind; form: ActionForm; set: (p: Partial<ActionForm>) => void }) {
  const depositBad = kind === "booking" && form.deposit.trim() !== "" && !/^(100|[1-9][0-9]?)$/.test(form.deposit.trim());
  return (
    <>
      <fieldset className="sf-choices">
        <legend>Payment</legend>
        {paymentChoices(kind).map((c) => (
          <label key={c.value} className="sf-radio">
            <input type="radio" name={`sf-payment-${kind}`} value={c.value} checked={form.payment === c.value} onChange={() => set({ payment: c.value })} />
            <span>
              {c.label}
              <span className="hint"> {c.help}</span>
            </span>
          </label>
        ))}
      </fieldset>
      {form.payment !== "none" && (
        <Field
          label="Paystack connection"
          hint={
            <>
              The name of your Paystack connection; left empty, “main”. Add one under <Link to="/connections">Connections</Link>.
            </>
          }
        >
          <input value={form.connection} placeholder="main" autoComplete="off" spellCheck={false} onChange={(e) => set({ connection: e.target.value })} />
        </Field>
      )}
      {kind === "booking" && form.payment !== "none" && (
        <Field label="Deposit (per cent)" hint={<span className={depositBad ? "error-text" : undefined}>Paid online when booking; the rest at the appointment. Leave it empty for the full price.</span>}>
          <input
            type="number"
            min={1}
            max={100}
            step={1}
            inputMode="numeric"
            value={form.deposit}
            placeholder="100"
            onChange={(e) => set({ deposit: e.target.value })}
            aria-invalid={depositBad}
          />
        </Field>
      )}
      {kind === "booking" && form.payment !== "none" && (
        <label className="sf-radio">
          <input type="checkbox" checked={form.keepDeposit} onChange={(e) => set({ keepDeposit: e.target.checked })} />
          Keep the deposit when a customer cancels a paid booking (otherwise they are refunded in full)
        </label>
      )}
    </>
  );
}

function OrderOptions({ form, set }: { form: ActionForm; set: (p: Partial<ActionForm>) => void }) {
  return (
    <>
      <fieldset className="sf-choices">
        <legend>How customers get their order</legend>
        <label className="sf-radio">
          <input type="checkbox" checked={form.pickup} onChange={(e) => set({ pickup: e.target.checked })} />
          Pickup
        </label>
        <label className="sf-radio">
          <input type="checkbox" checked={form.delivery} onChange={(e) => set({ delivery: e.target.checked })} />
          Delivery
        </label>
      </fieldset>
      {form.delivery && (
        <Field
          label="Delivery note"
          hint={
            <span className={form.deliveryNote.length > ACTION_LIMITS.delivery_note ? "error-text" : undefined}>
              Where you deliver and what it costs, such as Yaba and Surulere only. {`${form.deliveryNote.length}/${ACTION_LIMITS.delivery_note}`}
            </span>
          }
        >
          <input value={form.deliveryNote} onChange={(e) => set({ deliveryNote: e.target.value })} aria-invalid={form.deliveryNote.length > ACTION_LIMITS.delivery_note} />
        </Field>
      )}
    </>
  );
}
