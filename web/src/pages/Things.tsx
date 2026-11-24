import { useId, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { ApiError, get, patch, post, sendWith } from "../api";
import { useAuth } from "../auth";
import { dateLabel, naira } from "../lib/invoices";
import { lagosDate } from "../lib/money";
import type { SuppliersView } from "../lib/purchases";
import {
  STATUSES,
  THINGS_PATH,
  byLabel,
  cameOn,
  detailsRequest,
  emptyThing,
  hadOf,
  keyFor,
  priceProblem,
  rowsOf,
  statusRequest,
  statusSaid,
  statusSavable,
  statusTone,
  statusWords,
  thingLine,
  thingProblem,
  thingRequest,
  thingsCount,
  thingsPath,
  type Thing,
  type ThingForm,
  type ThingStatus,
  type ThingsView,
} from "../lib/things";
import { knownLabel, labelChoices, type SpendLabel } from "../lib/treat";
import { feeKobo } from "../lib/orders";
import {
  EmptyState,
  ErrorBox,
  Field,
  Item,
  Items,
  Modal,
  PageHeader,
  Skeleton,
  useAction,
  useLoad,
} from "../ui";
import { MoneyTabs } from "./Money";
import { LabelPick, useLabels } from "./TreatLine";

// Things we have (what a business buys §5.3), a page under Money: what the
// business keeps track of, one row each, grouped by label; a row's page
// with what happened to it (sold, broken, given away, gone: out of the
// totals, kept in the history); and Add something we have, for what was
// bought before Taskiem or outside it.

/** A server's refusal as a sentence. */
const plain = (e: unknown) => {
  if (e instanceof ApiError && (e.status === 400 || e.status === 409))
    e.message = e.message.replace(/^[a-z_]+: /, "");
  throw e;
};

/** The label of that name, found or made (none for ""). */
async function labelIdOf(
  name: string,
  labels: SpendLabel[] | undefined,
): Promise<string | undefined> {
  const n = name.trim();
  if (!n) return undefined;
  return (
    knownLabel(labels, n) ??
    (await post<SpendLabel>("/v1/money/labels", { name: n })).id
  );
}

export function ThingsPage() {
  const { can } = useAuth();
  const [params, setParams] = useSearchParams();
  const all = params.get("all") === "true";
  const line = params.get("line");
  const read = can("table.read");
  const path = thingsPath(all, line);
  // Only for those who may see them; each answer kept with what it is of,
  // so the totals of the last filter are not shown while the next loads.
  const view = useLoad(
    () =>
      read
        ? get<ThingsView>(path).then((v) => ({ path, v }))
        : Promise.resolve(undefined),
    [read, path],
  );
  const [adding, setAdding] = useState(false);
  const v = view.data?.path === path ? view.data.v : undefined;
  const rows = v ? rowsOf(v.things, line) : [];
  // One order line's: its sections and hero of its own rows still had.
  const groups = v ? byLabel(rows, line ? undefined : v.groups) : [];
  const had = line ? hadOf(rows) : v;
  return (
    <>
      <PageHeader
        title="Things we have"
        description="What the business keeps track of, one row each: what it cost, when it came and from whom."
        actions={
          can("table.edit") && (
            <button type="button" className="primary" onClick={() => setAdding(true)}>
              Add something we have
            </button>
          )
        }
      />
      <MoneyTabs />
      {!read ? (
        <p className="notice">Someone who may see your stock can see Things we have.</p>
      ) : (
        <>
          <ErrorBox error={view.error} />
          {!v && !view.error && <Skeleton rows={3} label="Loading Things we have…" />}
          {v && had && (
            <>
              <section className="hero" aria-label="What you have" data-testid="things-hero">
                <div className="hero-top">
                  <span className="hero-label">{line ? "What they cost, from this order line" : "What they cost"}</span>
                  <span className="hero-tag">{thingsCount(had.count)}</span>
                </div>
                <div className="hero-amount">{naira(had.total_kobo)}</div>
                <p className="hero-sub">
                  {line ? "Those from this order line you still have." : "Those you still have."} Sold, broken, given away or gone leave the totals.
                </p>
              </section>
              {line ? (
                <p className="notice" data-testid="things-of-line">
                  The rows from one order line.{" "}
                  <Link to="/money/things">See them all</Link>
                </p>
              ) : (
                <label className="sf-toggle">
                  <input
                    type="checkbox"
                    checked={all}
                    onChange={(e) => setParams(e.target.checked ? { all: "true" } : {}, { replace: true })}
                  />
                  Show those no longer had
                </label>
              )}
              {groups.length === 0 ? (
                <EmptyState icon="tag">
                  Nothing yet. What you buy and keep track of comes here when it arrives, or add something by hand.
                </EmptyState>
              ) : (
                groups.map((g) => (
                  <section key={g.key} aria-label={g.label} data-testid="things-group">
                    <h2 className="things-head">
                      {g.label}
                      <span className="hint">
                        {" "}
                        {thingsCount(g.count)}, {naira(g.total_kobo)}
                      </span>
                    </h2>
                    <Items label={`${g.label}: things`}>
                      {g.rows.map((t) => (
                        <Item
                          key={t.id}
                          to={`/money/things/${t.id}`}
                          icon="tag"
                          tone={t.status === "have" ? undefined : "muted"}
                          title={t.name}
                          side={t.cost_kobo !== undefined ? naira(t.cost_kobo) : undefined}
                          sub={thingLine(t, dateLabel)}
                          sideSub={<span className={`badge ${statusTone(t.status)}`}>{statusWords(t.status)}</span>}
                          testid="thing-row"
                        />
                      ))}
                    </Items>
                  </section>
                ))
              )}
            </>
          )}
        </>
      )}
      {adding && (
        <AddThingModal
          onClose={() => setAdding(false)}
          onAdded={() => {
            setAdding(false);
            view.reload();
          }}
        />
      )}
    </>
  );
}

/** Add something we have: anything bought before Taskiem or outside it. */
function AddThingModal({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const [f, setF] = useState<ThingForm>(emptyThing);
  const [tried, setTried] = useState(false);
  const labels = useLabels();
  const suppliers = useLoad(
    () => get<SuppliersView>("/v1/stock/suppliers").then((v) => v.suppliers, () => []),
    [],
  );
  const act = useAction();
  const today = lagosDate(new Date());
  const problem = thingProblem(f, today);
  const set = (p: Partial<ThingForm>) => setF((x) => ({ ...x, ...p }));
  return (
    <Modal title="Add something we have" onClose={onClose}>
      <form
        data-testid="add-thing"
        onSubmit={(e) => {
          e.preventDefault();
          setTried(true);
          if (problem) return;
          void act.run(async () => {
            const labelId = await labelIdOf(f.label, labels.data);
            await post(THINGS_PATH, thingRequest(f, labelId)).catch(plain);
            onAdded();
          }, `${f.name.trim()} is on Things we have`);
        }}
      >
        <Field label="What is it?">
          <input value={f.name} maxLength={200} autoFocus onChange={(e) => set({ name: e.target.value })} />
        </Field>
        <LabelPick
          item={f.name.trim() || "it"}
          value={f.label}
          picks={labelChoices(labels.data, undefined)}
          onChange={(label) => set({ label })}
        />
        <Field label="From whom" hint="Optional">
          <select value={f.supplierId} onChange={(e) => set({ supplierId: e.target.value })}>
            <option value="">Not said</option>
            {(suppliers.data ?? []).map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="What it cost" hint="In naira, like 250,000">
          <input inputMode="decimal" value={f.cost} onChange={(e) => set({ cost: e.target.value })} />
        </Field>
        <Field label="When it came" hint="Today when left empty">
          <input type="date" value={f.came} max={today} onChange={(e) => set({ came: e.target.value })} />
        </Field>
        <Field label="Serial number" hint="Optional">
          <input value={f.serial} maxLength={100} onChange={(e) => set({ serial: e.target.value })} />
        </Field>
        <Field label="Warranty ends" hint="Optional">
          <input type="date" value={f.warranty} onChange={(e) => set({ warranty: e.target.value })} />
        </Field>
        <Field label="Note" hint="Optional">
          <textarea value={f.note} maxLength={500} rows={2} onChange={(e) => set({ note: e.target.value })} />
        </Field>
        {tried && problem && (
          <div className="error" role="alert">
            {problem}
          </div>
        )}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy}>
            Add it
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** One of Things we have: what happened to it, its details, the order it
 * came on. */
export function ThingPage() {
  const { id = "" } = useParams();
  const { can } = useAuth();
  const read = can("table.read");
  const view = useLoad(
    () =>
      read
        ? get<{ thing: Thing }>(`${THINGS_PATH}/${encodeURIComponent(id)}`)
        : Promise.resolve(undefined),
    [read, id],
  );
  const [editing, setEditing] = useState(false);
  const t = view.data?.thing;
  const edit = can("table.edit");
  if (!read)
    return (
      <>
        <p className="crumbs">
          <Link to="/money/things" aria-label="Back to Things we have">
            Things we have
          </Link>
        </p>
        <PageHeader title="Something we have" />
        <p className="notice">Someone who may see your stock can see Things we have.</p>
      </>
    );
  return (
    <>
      <p className="crumbs">
        <Link to="/money/things" aria-label="Back to Things we have">
          Things we have
        </Link>
      </p>
      <PageHeader title={t?.name ?? "Something we have"} />
      <ErrorBox error={view.error} />
      {!t ? (
        !view.error && <Skeleton rows={3} label="Loading it…" />
      ) : (
        <>
          <section className="hero" aria-label="What it cost" data-testid="thing-hero">
            <div className="hero-top">
              <span className="hero-label">What it cost</span>
              <span className="hero-tag" data-testid="thing-status">
                {statusWords(t.status)}
              </span>
            </div>
            <div className={t.cost_kobo !== undefined ? "hero-amount" : "hero-amount words"}>
              {t.cost_kobo !== undefined ? naira(t.cost_kobo) : "Not said"}
            </div>
            <p className="hero-sub">
              {t.status === "have"
                ? "You have it."
                : `${statusWords(t.status)} on ${dateLabel(lagosDate(new Date(t.status_at)))}. Kept in the history, out of the totals.`}
            </p>
          </section>
          <section className="card" aria-labelledby="thing-about">
            <h2 id="thing-about">About it</h2>
            <dl className="kv" data-testid="thing-facts">
                <dt>Label</dt>
                <dd>{t.label || "Not labelled"}</dd>
                <dt>From</dt>
                <dd>{t.supplier || "Not said"}</dd>
                <dt>Came</dt>
                <dd>{dateLabel(cameOn(t))}</dd>
                <dt>Order</dt>
                <dd>
                  {t.purchase_order_id ? (
                    <Link to={`/purchases/${t.purchase_order_id}`}>{t.order_reference || "The order it came on"}</Link>
                  ) : (
                    "Added by hand"
                  )}
                </dd>
                <dt>Serial number</dt>
                <dd>{t.serial || "None"}</dd>
                <dt>Warranty ends</dt>
                <dd>{t.warranty_until ? dateLabel(t.warranty_until) : "None"}</dd>
                <dt>Note</dt>
                <dd>{t.note || "None"}</dd>
            </dl>
            {edit && (
              <button type="button" onClick={() => setEditing(true)}>
                Change the details
              </button>
            )}
          </section>
          {edit ? (
            <StatusCard key={`${t.status}-${t.sale_entry_id ?? ""}`} thing={t} onSaved={view.reload} />
          ) : (
            <p className="notice">Someone who may buy for the business can change it.</p>
          )}
        </>
      )}
      {editing && t && (
        <DetailsModal
          thing={t}
          onClose={() => setEditing(false)}
          onSaved={() => {
            setEditing(false);
            view.reload();
          }}
        />
      )}
    </>
  );
}

/** What happened to it: Have, Sold (with what it sold for, money in, for
 * those who manage money), Broken, Given away, Gone. */
function StatusCard({ thing: t, onSaved }: { thing: Thing; onSaved: () => void }) {
  const { can } = useAuth();
  const money = can("invoice.manage");
  const [status, setStatus] = useState<ThingStatus>(t.status);
  const [price, setPrice] = useState("");
  const [how, setHow] = useState<"cash" | "transfer">("cash");
  // Kept for the same change tried again; new when the change differs.
  const key = useRef<{ body: string; key: string }>(undefined);
  const act = useAction();
  const navigate = useNavigate();
  const priceId = useId();
  // A sale recorded already is not recorded again.
  const pricing = status === "sold" && !t.sale_entry_id;
  // Sold with its money recorded: it stays sold (the server refuses).
  const soldWithMoney = t.status === "sold" && !!t.sale_entry_id;
  const problem = pricing ? priceProblem(price, money) : "";
  const kobo = pricing && price.trim() ? feeKobo(price) : undefined;
  return (
    <section className="card" aria-labelledby="thing-status-h" data-testid="thing-change">
      <h2 id="thing-status-h">What happened to it</h2>
      <div
        role="radiogroup"
        aria-label="What happened to it"
        aria-describedby={soldWithMoney ? `${priceId}-kept` : undefined}
      >
        {STATUSES.map((s) => (
          <label key={s.value} className="sf-radio">
            <input
              type="radio"
              name="thing-status"
              checked={status === s.value}
              disabled={soldWithMoney && s.value !== "sold"}
              onChange={() => setStatus(s.value)}
            />
            {s.label}
          </label>
        ))}
      </div>
      {pricing && (
        <>
          <label className="field" htmlFor={priceId}>
            <span>What it sold for (optional)</span>
            <input
              id={priceId}
              inputMode="decimal"
              value={price}
              disabled={!money}
              aria-describedby={money ? undefined : `${priceId}-why`}
              onChange={(e) => setPrice(e.target.value)}
            />
          </label>
          {money ? (
            <>
              <p className="hint">With a price it is money in, Sale of things we had.</p>
              {price.trim() !== "" && (
                <Field label="Paid">
                  <select value={how} onChange={(e) => setHow(e.target.value as "cash" | "transfer")}>
                    <option value="cash">In cash</option>
                    <option value="transfer">By transfer</option>
                  </select>
                </Field>
              )}
            </>
          ) : (
            <p className="hint" id={`${priceId}-why`} data-testid="sale-price-why">
              Only someone who manages money can record what it sold for, since it is money in. You can mark it sold without a price.
            </p>
          )}
        </>
      )}
      {soldWithMoney ? (
        <p className="hint" id={`${priceId}-kept`} data-testid="sold-kept">
          What it sold for is recorded, as money in, so it stays sold.
        </p>
      ) : (
        status === "sold" &&
        t.sale_entry_id && <p className="hint">What it sold for is recorded, as money in.</p>
      )}
      {problem && price.trim() !== "" && (
        <div className="error" role="alert">
          {problem}
        </div>
      )}
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button
          type="button"
          className="primary"
          disabled={act.busy || !!problem || !statusSavable(t, status, price, money)}
          onClick={() =>
            void act.run(async () => {
              const body = statusRequest(status, pricing ? price : "", how);
              key.current = keyFor(key.current, body);
              await sendWith("PATCH", `${THINGS_PATH}/${t.id}`, body, {
                "Idempotency-Key": key.current.key,
              }).catch(plain);
              onSaved();
            }, statusSaid(t.name, status, kobo))
          }
        >
          Save
        </button>
        {t.sale_entry_id && (
          <button type="button" onClick={() => navigate("/money/entries")}>
            See money in
          </button>
        )}
      </div>
    </section>
  );
}

/** Its serial, warranty, note and label. */
function DetailsModal({ thing: t, onClose, onSaved }: { thing: Thing; onClose: () => void; onSaved: () => void }) {
  const [f, setF] = useState({
    serial: t.serial ?? "",
    warranty: (t.warranty_until ?? "").slice(0, 10),
    note: t.note ?? "",
    label: t.label ?? "",
  });
  const labels = useLabels();
  const act = useAction();
  const set = (p: Partial<typeof f>) => setF((x) => ({ ...x, ...p }));
  return (
    <Modal title={`Change ${t.name}`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => {
            const body = detailsRequest(t, f);
            if (f.label.trim() !== (t.label ?? "")) {
              const id = await labelIdOf(f.label, labels.data);
              if (id) body.label_id = id;
              else body.clear_label = true;
            }
            if (Object.keys(body).length > 0) await patch(`${THINGS_PATH}/${t.id}`, body).catch(plain);
            onSaved();
          }, "Saved");
        }}
      >
        <LabelPick
          item={t.name}
          value={f.label}
          picks={labelChoices(labels.data, undefined)}
          onChange={(label) => set({ label })}
        />
        <Field label="Serial number">
          <input value={f.serial} maxLength={100} onChange={(e) => set({ serial: e.target.value })} />
        </Field>
        <Field label="Warranty ends">
          <input type="date" value={f.warranty} onChange={(e) => set({ warranty: e.target.value })} />
        </Field>
        <Field label="Note">
          <textarea value={f.note} maxLength={500} rows={2} onChange={(e) => set({ note: e.target.value })} />
        </Field>
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy}>
            Save
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
