import { useRef, useState } from "react";
import { ApiError, del, get, patch, post, put, uploadFile } from "../api";
import {
  EmptyState,
  ErrorBox,
  Field,
  Items,
  Modal,
  Row,
  Skeleton,
  useAction,
  useLoad,
} from "../ui";
import {
  actionForm,
  actionRequest,
  type StorefrontAction,
} from "../lib/orders";
import {
  emptyItem,
  itemForm,
  itemProblem,
  itemRequest,
  type Front,
  type FrontItem,
  type FrontItemsView,
  type ItemForm,
} from "../lib/fronts";
import { formatPrice, imageProblem, thumb, tidy } from "../lib/storefront";
import { refusalOf } from "../lib/trust";

/** A front's items, edited where the front is (docs/spec/fronts.md §7.1):
 * what is on its list, with a photo, a price and whether it can be ordered
 * now, **Add an item**, and a change, a photo or a removal for each. They are
 * the rows of the front's table, so a change here is a change there. */
export function FrontItems({
  front,
  onChanged,
}: {
  front: Front;
  onChanged: () => void;
}) {
  const base = `/v1/storefront/fronts/${front.id}/items`;
  const view = useLoad(() => get<FrontItemsView>(base), [front.id]);
  const act = useAction();
  const [editing, setEditing] = useState<FrontItem | "new" | null>(null);
  const photoFor = useRef<string | null>(null);
  const picker = useRef<HTMLInputElement>(null);
  const v = view.data;

  const after = async () => {
    view.reload();
    onChanged();
  };
  const sendPhoto = async (row: string, file: File) => {
    const bad = imageProblem(file);
    if (bad) throw new Error(bad);
    await tidy(uploadFile(`${base}/${encodeURIComponent(row)}/photo`, file));
  };

  return (
    <section className="card" aria-labelledby="front-items">
      <div className="toolbar">
        <h2 id="front-items" className="grow" style={{ margin: 0 }}>
          Items
        </h2>
        {v && (
          <button
            type="button"
            className="primary"
            onClick={() => setEditing("new")}
          >
            Add an item
          </button>
        )}
      </div>
      <ErrorBox error={view.error ?? act.error} />
      {!v && !view.error && <Skeleton rows={3} label="Loading the items…" />}
      {v && v.items.length === 0 && (
        <EmptyState
          icon="storefront"
          action={
            <button
              type="button"
              className="primary"
              onClick={() => setEditing("new")}
            >
              Add your first item
            </button>
          }
        >
          Nothing on this front yet. Add what you sell, with a price and a
          photo.
        </EmptyState>
      )}
      {v && v.items.length > 0 && (
        <Items label="Items on this front">
          {v.items.map((it) => (
            <Row
              key={it.id}
              icon="storefront"
              image={thumb(it.photo)}
              tone={it.available ? undefined : "muted"}
              title={it.name}
              side={
                it.price_kobo !== undefined ? (
                  <span className="sf-price">{formatPrice(it.price_kobo)}</span>
                ) : undefined
              }
              sub={
                <span className="sub">
                  {[
                    it.category,
                    it.available ? "" : "Hidden from customers",
                    it.description,
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </span>
              }
              testid="front-item"
              attrs={{ "data-available": String(it.available) }}
            >
              <button
                type="button"
                className="small"
                onClick={() => setEditing(it)}
              >
                Change<span className="sr-only"> {it.name}</span>
              </button>
              {v.takes.photo && (
                <button
                  type="button"
                  className="small"
                  disabled={act.busy}
                  onClick={() => {
                    photoFor.current = it.id;
                    picker.current?.click();
                  }}
                >
                  {it.photo && Object.keys(it.photo).length > 0
                    ? "Change photo"
                    : "Add photo"}
                  <span className="sr-only"> for {it.name}</span>
                </button>
              )}
              {v.takes.available && (
                <button
                  type="button"
                  className="small"
                  disabled={act.busy}
                  onClick={() =>
                    void act.run(
                      async () => (
                        await tidy(
                          patch(`${base}/${encodeURIComponent(it.id)}`, {
                            available: !it.available,
                          }),
                        ),
                        await after()
                      ),
                      it.available
                        ? `${it.name} is hidden`
                        : `${it.name} is on sale`,
                    )
                  }
                >
                  {it.available ? "Hide" : "Show"}
                  <span className="sr-only"> {it.name}</span>
                </button>
              )}
              <button
                type="button"
                className="small danger"
                disabled={act.busy}
                onClick={() =>
                  confirm(`Remove ${it.name} from ${front.name}?`) &&
                  void act.run(
                    async () => (
                      await del(`${base}/${encodeURIComponent(it.id)}`),
                      await after()
                    ),
                    `${it.name} removed`,
                  )
                }
              >
                Remove<span className="sr-only"> {it.name}</span>
              </button>
            </Row>
          ))}
        </Items>
      )}
      <input
        ref={picker}
        type="file"
        accept="image/jpeg,image/png,image/webp"
        hidden
        onChange={(e) => {
          const file = e.target.files?.[0];
          e.target.value = "";
          const row = photoFor.current;
          if (!file || !row) return;
          void act.run(
            async () => (await sendPhoto(row, file), await after()),
            "Photo added",
          );
        }}
      />
      {editing && v && (
        <ItemSheet
          front={front}
          view={v}
          item={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
          onSave={async (form, file) => {
            if (editing === "new") {
              const made = await tidy(
                post<{ id: string }>(base, itemRequest(form, v.takes)),
              );
              if (file) await sendPhoto(made.id, file);
            } else {
              const body = itemRequest(form, v.takes, itemForm(editing));
              if (Object.keys(body).length > 0)
                await tidy(
                  patch(`${base}/${encodeURIComponent(editing.id)}`, body),
                );
              if (file) await sendPhoto(editing.id, file);
            }
            setEditing(null);
            await after();
          }}
        />
      )}
    </section>
  );
}

/** The form of one item: new, or changed. A photo chosen goes up with it. */
function ItemSheet({
  front,
  view,
  item,
  onClose,
  onSave,
}: {
  front: Front;
  view: FrontItemsView;
  item: FrontItem | null;
  onClose: () => void;
  onSave: (f: ItemForm, photo: File | null) => Promise<void>;
}) {
  const [f, setF] = useState<ItemForm>(item ? itemForm(item) : emptyItem);
  const [photo, setPhoto] = useState<File | null>(null);
  const act = useAction();
  const problem =
    itemProblem(f, view.takes) || (photo ? (imageProblem(photo) ?? "") : "");
  const set = (k: keyof ItemForm) => (e: { target: { value: string } }) =>
    setF({ ...f, [k]: e.target.value });
  return (
    <Modal
      title={item ? `Change ${item.name}` : `Add an item to ${front.name}`}
      onClose={onClose}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(() => onSave(f, photo), false);
        }}
      >
        <Field label="Name">
          <input
            value={f.name}
            onChange={set("name")}
            maxLength={120}
            autoFocus
            required
          />
        </Field>
        {view.takes.price && (
          <Field
            label="Price (₦)"
            hint="What one costs, in naira. Leave it empty if it has no price."
          >
            <input
              value={f.price}
              onChange={set("price")}
              inputMode="decimal"
              placeholder="1,500"
            />
          </Field>
        )}
        {view.takes.description && (
          <Field
            label="Description"
            hint="Optional: what it is, what it comes with."
          >
            <textarea
              value={f.description}
              onChange={set("description")}
              maxLength={1000}
              rows={3}
            />
          </Field>
        )}
        {view.takes.category && (
          <Field
            label="Category"
            hint="Optional: items with the same category are shown together."
          >
            <input
              value={f.category}
              onChange={set("category")}
              maxLength={60}
            />
          </Field>
        )}
        {view.takes.photo && (
          <Field
            label={item ? "A new photo" : "Photo"}
            hint="Optional: a JPEG, PNG or WebP picture of it."
          >
            <input
              type="file"
              accept="image/jpeg,image/png,image/webp"
              onChange={(e) => setPhoto(e.target.files?.[0] ?? null)}
            />
          </Field>
        )}
        {problem && f.name !== "" && (
          <p className="hint" role="status">
            {problem}
          </p>
        )}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button
            type="submit"
            className="primary"
            disabled={act.busy || problem !== ""}
          >
            {act.busy ? "Saving…" : item ? "Save" : "Add the item"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** Shown when customers cannot order from a front yet: its order form is
 * off or has no workflow behind it, so the page has nothing to press. One
 * button sets it up, switches it on and (as the Forms card does) has the
 * page updated, with the same checks. */
export function OrdersBanner({
  front,
  action,
  published,
  onDone,
}: {
  front: Front;
  action: StorefrontAction;
  published: boolean;
  onDone: () => void;
}) {
  const act = useAction();
  const [waiting, setWaiting] = useState(false);
  const [notice, setNotice] = useState("");
  const turnOn = () =>
    act.run(async () => {
      setNotice("");
      const setUp = async (): Promise<boolean> => {
        const made = await tidy(
          post<{ workflow_id: string }>(
            `/v1/storefront/actions/${action.id ?? ""}/setup`,
          ),
        );
        const p = await tidy(
          post<{ state: string }>(
            `/v1/workflows/${made.workflow_id}/versions/1/publish`,
          ),
        );
        return p.state === "published";
      };
      if (!action.workflow_id && !(await setUp())) {
        setWaiting(true);
        return;
      }
      const enable = () =>
        tidy(
          put(
            `/v1/storefront/actions/${action.id ?? ""}`,
            actionRequest(action.kind, {
              ...actionForm(action),
              enabled: true,
            }),
          ),
        );
      try {
        await enable();
      } catch (e) {
        // No workflow deployed after all: set it up, then once more.
        if (
          e instanceof ApiError &&
          e.status === 409 &&
          !refusalOf(e) &&
          (await setUp())
        )
          await enable();
        else throw e;
      }
      setNotice(
        published
          ? "Customers can order now."
          : "Customers can order once you publish your page.",
      );
      onDone();
    }, false);
  return (
    <section
      className="card sf-orders-banner"
      aria-labelledby={`orders-off-${front.id}`}
      data-testid="orders-banner"
    >
      <h2 id={`orders-off-${front.id}`} style={{ marginTop: 0 }}>
        Customers cannot order from this front yet
      </h2>
      <p>
        Its “{action.label}” form is {action.workflow_id ? "off" : "not set up"}
        , so items on {front.name} have nothing to press. Turn it on and an item
        opens with <strong>Add to order</strong>.
      </p>
      {waiting && (
        <div className="notice" role="status">
          The form's workflow waits for a second person to publish it under
          Approvals. Come back and turn it on once it is published.
        </div>
      )}
      {notice && (
        <div className="notice" role="status">
          {notice}
        </div>
      )}
      <ErrorBox error={act.error} />
      <button
        type="button"
        className="primary"
        disabled={act.busy}
        onClick={() => void turnOn()}
      >
        {act.busy ? "Turning on…" : "Turn on orders"}
      </button>
    </section>
  );
}
