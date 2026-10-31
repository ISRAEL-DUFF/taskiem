import { useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, get, patch, post, put } from "../api";
import {
  EmptyState,
  ErrorBox,
  Field,
  Item,
  Items,
  Modal,
  PageHeader,
  Row,
  Skeleton,
  useAction,
  useLoad,
} from "../ui";
import {
  limitInput,
  limitRequest,
  supplierProblem,
  supplierSub,
  type Supplier,
  type SupplierForm,
  type SuppliersView,
} from "../lib/purchases";
import { owedBySupplier, type Bill } from "../lib/bills";
import type { LinksView } from "../lib/links";
import type { StockView } from "../lib/stock";
import { formatPrice } from "../lib/storefront";
import { StorefrontTabs } from "./Storefront";

/** The business's suppliers (docs/stock.md "Suppliers and purchase
 * orders"): who it buys from, where their orders reach them, and what is
 * owed to them; and who may send a large order. */
export function StorefrontSuppliers() {
  const [retired, setRetired] = useState(false);
  const view = useLoad(
    () =>
      get<SuppliersView>(
        `/v1/stock/suppliers${retired ? "?retired=true" : ""}`,
      ),
    [retired],
  );
  const [editing, setEditing] = useState<Supplier | "new" | null>(null);
  const [linking, setLinking] = useState<Supplier | null>(null);
  const act = useAction();
  const v = view.data;
  const off = view.error instanceof ApiError && view.error.status === 503;
  return (
    <>
      <PageHeader
        title="Suppliers"
        description="Who you buy your stock from. Your orders reach them on WhatsApp, and they confirm with one tap."
        actions={
          v && (
            <button
              type="button"
              className="primary"
              onClick={() => setEditing("new")}
            >
              Add a supplier
            </button>
          )
        }
      />
      <StorefrontTabs />
      {off ? (
        <EmptyState icon="members">
          Suppliers need text messages, which are not set up on this Taskiem
          yet.
        </EmptyState>
      ) : (
        <ErrorBox error={view.error ?? act.error} />
      )}
      {!v && !view.error && (
        <Skeleton rows={3} label="Loading your suppliers…" />
      )}
      {v && (
        <>
          <section className="card" aria-labelledby="suppliers-list">
            <div className="toolbar" style={{ marginBottom: 6 }}>
              <h2 id="suppliers-list" className="grow" style={{ margin: 0 }}>
                {retired ? "Retired suppliers" : "Your suppliers"}
              </h2>
              <button
                type="button"
                className="small"
                onClick={() => setRetired(!retired)}
              >
                {retired ? "Show current suppliers" : "Show retired suppliers"}
              </button>
            </div>
            {v.suppliers.length === 0 ? (
              <EmptyState icon="members">
                {retired
                  ? "No retired suppliers."
                  : "No suppliers yet. Add who you buy from, and the items they supply."}
              </EmptyState>
            ) : (
              <Items label={retired ? "Retired suppliers" : "Your suppliers"}>
                {v.suppliers.map((s) => (
                  <Row
                    key={s.id}
                    letter={s.name}
                    title={s.name}
                    side={
                      s.owed_kobo > 0 && (
                        <span className="badge warn">
                          Owed {formatPrice(s.owed_kobo)}
                        </span>
                      )
                    }
                    sub={<span className="sub">{supplierSub(s)}</span>}
                    testid="supplier"
                  >
                    {!retired && (
                      <>
                        <button
                          type="button"
                          className="small"
                          onClick={() => setEditing(s)}
                        >
                          Change<span className="sr-only"> {s.name}</span>
                        </button>
                        {s.link_id && (
                          <Link
                            className="button small"
                            to={`/storefront/suppliers/${s.link_id}/prices`}
                          >
                            Prices
                            <span className="sr-only"> from {s.name}</span>
                          </Link>
                        )}
                        {s.link_id && (
                          <Link
                            className="button small"
                            to={`/links/${s.link_id}/statement`}
                          >
                            Statement
                            <span className="sr-only"> with {s.name}</span>
                          </Link>
                        )}
                        <button
                          type="button"
                          className="small"
                          onClick={() => setLinking(s)}
                        >
                          On Taskiem
                          <span className="sr-only"> for {s.name}</span>
                        </button>
                        {s.reached === "sms" && (
                          <button
                            type="button"
                            className="small"
                            disabled={act.busy}
                            onClick={() =>
                              void act.run(
                                async () => (
                                  await post(
                                    `/v1/stock/suppliers/${s.id}/invite`,
                                  ),
                                  view.reload()
                                ),
                                `Link sent to ${s.name}`,
                              )
                            }
                          >
                            Send the link again
                            <span className="sr-only"> to {s.name}</span>
                          </button>
                        )}
                        <button
                          type="button"
                          className="small danger"
                          disabled={act.busy}
                          onClick={() =>
                            confirm(
                              `Retire ${s.name}? Their orders stay, and you can add them again.`,
                            ) &&
                            void act.run(
                              async () => (
                                await post(
                                  `/v1/stock/suppliers/${s.id}/retire`,
                                ),
                                view.reload()
                              ),
                              `${s.name} retired`,
                            )
                          }
                        >
                          Retire<span className="sr-only"> {s.name}</span>
                        </button>
                      </>
                    )}
                  </Row>
                ))}
              </Items>
            )}
          </section>
          {!retired && (
            <LimitCard initial={limitInput(v.settings)} onSaved={view.reload} />
          )}
        </>
      )}
      {editing && (
        <SupplierModal
          supplier={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            if (retired) setRetired(false);
            else view.reload();
          }}
        />
      )}
      {linking && (
        <LinkSupplierModal
          supplier={linking}
          onClose={() => setLinking(null)}
          onSaved={() => {
            setLinking(null);
            view.reload();
          }}
        />
      )}
      <LinkedSuppliersOwed />
      <DeliverTo />
    </>
  );
}

/** Where linked suppliers deliver the business's orders: carried to each
 * order to them, where their rider takes it. */
function DeliverTo() {
  const cur = useLoad(
    () => get<{ address: string; phone: string }>("/v1/stock/deliver-to"),
    [],
  );
  if (!cur.data) return null;
  return <DeliverToForm initial={cur.data} onSaved={cur.reload} />;
}

function DeliverToForm({
  initial,
  onSaved,
}: {
  initial: { address: string; phone: string };
  onSaved: () => void;
}) {
  const [address, setAddress] = useState(initial.address);
  const [phone, setPhone] = useState(initial.phone);
  const act = useAction();
  return (
    <section
      className="card"
      aria-labelledby="suppliers-deliver"
      data-testid="deliver-to"
    >
      <h2 id="suppliers-deliver">Where linked suppliers deliver</h2>
      <p className="hint">
        Orders to suppliers on Taskiem carry it, and their rider brings them
        here. Leave it empty and you pick orders up.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(
            async () => (
              await put("/v1/stock/deliver-to", {
                address: address.trim(),
                phone: phone.trim(),
              }),
              onSaved()
            ),
            "Saved",
          );
        }}
      >
        <Field label="Address">
          <input
            value={address}
            maxLength={300}
            placeholder="12 Admiralty Way, Lekki"
            onChange={(e) => setAddress(e.target.value)}
            autoComplete="street-address"
          />
        </Field>
        <Field label="Number the rider calls at the door">
          <input
            value={phone}
            inputMode="tel"
            placeholder="0803 123 4567"
            onChange={(e) => setPhone(e.target.value)}
            autoComplete="tel"
          />
        </Field>
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button
            type="submit"
            className="primary"
            disabled={
              act.busy ||
              (address === initial.address && phone === initial.phone)
            }
          >
            Save
          </button>
        </div>
      </form>
    </section>
  );
}

/** Says a supplier is a business we are linked with on Taskiem: its
 * orders then go straight into its business, and come back with its
 * answers (business links L2). */
function LinkSupplierModal({
  supplier,
  onClose,
  onSaved,
}: {
  supplier: Supplier;
  onClose: () => void;
  onSaved: () => void;
}) {
  const list = useLoad(() => get<LinksView>("/v1/links"), []);
  const [link, setLink] = useState(supplier.link_id ?? "");
  const act = useAction();
  const linked = (list.data?.links ?? []).filter(
    (l) => l.state === "active" && (l.role === "supplier" || l.role === "both"),
  );
  return (
    <Modal title={`${supplier.name} on Taskiem`} onClose={onClose}>
      <p className="hint">
        When {supplier.name} is a business you are linked with, your orders go
        straight into their business on Taskiem, and their answers, delivery and
        invoice come back to you, instead of messages.
      </p>
      <ErrorBox error={list.error ?? act.error} />
      {list.data && linked.length === 0 ? (
        <p className="hint">
          You are not linked with a business that supplies you yet.{" "}
          <Link to="/chat">Find them</Link>
        </p>
      ) : (
        <Field label="Which business is it?">
          <select value={link} onChange={(e) => setLink(e.target.value)}>
            <option value="">Not on Taskiem (orders by message)</option>
            {linked.map((l) => (
              <option key={l.id} value={l.id}>
                {l.name}
              </option>
            ))}
          </select>
        </Field>
      )}
      <div className="toolbar">
        <button
          type="button"
          className="primary"
          disabled={act.busy || link === (supplier.link_id ?? "")}
          onClick={() =>
            void act.run(
              async () => (
                await put(`/v1/stock/suppliers/${supplier.id}/link`, {
                  link_id: link || null,
                }),
                onSaved()
              ),
              "Saved",
            )
          }
        >
          Save
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}

/** Who may send a large order: above the limit, not the person who drafted it. */
function LimitCard({
  initial,
  onSaved,
}: {
  initial: string;
  onSaved: () => void;
}) {
  const [limit, setLimit] = useState(initial);
  const act = useAction();
  const body = limitRequest(limit);
  const bad =
    body.purchase_limit_kobo !== null && Number.isNaN(body.purchase_limit_kobo);
  return (
    <section className="card" aria-labelledby="suppliers-limit">
      <h2 id="suppliers-limit" style={{ marginTop: 0 }}>
        Approving orders
      </h2>
      <p className="hint">
        An order above this amount is sent by another owner or admin, not the
        person who drafted it. Leave it empty and whoever drafts an order sends
        it.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(
            async () => (await put("/v1/stock/settings", body), onSaved()),
            "Saved",
          );
        }}
      >
        <Field label="A second person sends orders above (₦)">
          <input
            value={limit}
            inputMode="decimal"
            placeholder="500000"
            onChange={(e) => setLimit(e.target.value)}
          />
        </Field>
        {bad && <p className="error-text">Enter an amount, like 500000.</p>}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button
            type="submit"
            className="primary"
            disabled={act.busy || bad || limit === initial}
          >
            Save
          </button>
        </div>
      </form>
    </section>
  );
}

/** Add a supplier with the items they supply, or rename one. */
function SupplierModal({
  supplier,
  onClose,
  onSaved,
}: {
  supplier: Supplier | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const adding = supplier === null;
  const stock = useLoad(
    () => (adding ? get<StockView>("/v1/stock") : Promise.resolve(null)),
    [adding],
  );
  const [f, setF] = useState<SupplierForm>({
    name: supplier?.name ?? "",
    phone: "",
    items: [],
  });
  const act = useAction();
  const set = (p: Partial<SupplierForm>) => setF((x) => ({ ...x, ...p }));
  const problem = supplierProblem(f, adding);
  const items = stock.data?.keeps_stock ? stock.data.items : [];
  return (
    <Modal
      title={adding ? "Add a supplier" : `Change ${supplier.name}`}
      onClose={onClose}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(
            async () => {
              if (adding)
                await post("/v1/stock/suppliers", {
                  name: f.name.trim(),
                  phone: f.phone.trim(),
                  items: f.items,
                });
              else
                await patch(`/v1/stock/suppliers/${supplier.id}`, {
                  name: f.name.trim(),
                });
              onSaved();
            },
            adding
              ? `${f.name.trim()} added; the link is on its way by SMS`
              : "Saved",
          );
        }}
      >
        <Field label="Name">
          <input
            value={f.name}
            maxLength={80}
            placeholder="Dangote Depot"
            onChange={(e) => set({ name: e.target.value })}
            autoFocus
            autoComplete="off"
          />
        </Field>
        {adding && (
          <Field
            label="Mobile number"
            hint="They get a link by SMS. One tap, and your orders reach them on WhatsApp."
          >
            <input
              value={f.phone}
              inputMode="tel"
              placeholder="0803 123 4567"
              onChange={(e) => set({ phone: e.target.value })}
              autoComplete="off"
            />
          </Field>
        )}
        {adding && items.length > 0 && (
          <fieldset className="sf-choices">
            <legend>What they supply</legend>
            {items.map((it) => (
              <label key={it.id} className="sf-radio">
                <input
                  type="checkbox"
                  checked={f.items.includes(it.id)}
                  onChange={(e) =>
                    set({
                      items: e.target.checked
                        ? [...f.items, it.id]
                        : f.items.filter((x) => x !== it.id),
                    })
                  }
                />
                {it.name}
                {it.supplier ? ` (now ${it.supplier})` : ""}
              </label>
            ))}
          </fieldset>
        )}
        {!adding && (
          <p className="hint">
            The items naming {supplier.name} take the new name too.
          </p>
        )}
        {problem && f.name !== "" && <p className="error-text">{problem}</p>}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button
            type="submit"
            className="primary"
            disabled={act.busy || !!problem}
          >
            {adding ? "Add the supplier" : "Save"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** What is owed to linked suppliers on Taskiem (business links): the sum
 * of their open bills, which is what their own records say. */
function LinkedSuppliersOwed() {
  const bills = useLoad(() => get<{ bills: Bill[] }>("/v1/bills"), []);
  const owed = owedBySupplier(bills.data?.bills);
  if (owed.length === 0) return null;
  return (
    <section
      className="card"
      aria-labelledby="suppliers-linked"
      data-testid="suppliers-linked"
    >
      <h2 id="suppliers-linked">Linked suppliers on Taskiem</h2>
      <p className="hint">
        What you owe them is their open bills, as their own records say.{" "}
        <Link to="/bills">All bills</Link>
      </p>
      <Items label="Owed to linked suppliers">
        {owed.map((o) => (
          <Item
            key={o.supplier}
            to="/bills"
            icon="invoices"
            tone="warn"
            title={o.name}
            side={formatPrice(o.dueKobo)}
            sub={`${o.bills} ${o.bills === 1 ? "bill" : "bills"} to pay`}
            testid="supplier-linked"
          />
        ))}
      </Items>
    </section>
  );
}
