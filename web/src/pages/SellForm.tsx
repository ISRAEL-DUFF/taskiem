// Forms that sell (docs/spec/forms-that-sell.md §5): the sheet that turns a
// form into one that sells a product in packages, from the Forms page (Sell
// something with this form) or a front (Add an order form), and the code
// that shows one order form on the business's own website.
import { useState } from "react";
import { get, post, put } from "../api";
import { ErrorBox, Field, Modal, useAction, useLoad } from "../ui";
import { Copy } from "./Forms";
import type { FrontsView } from "../lib/fronts";
import { formatPrice } from "../lib/storefront";
import {
  MAX_PACKAGES,
  areaName,
  areaOffOf,
  dearerEach,
  readPackages,
  startingRows,
  type ActionEmbed,
  type PackageRow,
  type Sellable,
} from "../lib/sell";

/** What a sell request answers with that the page shows. */
export interface Sold {
  warnings: string[];
  url?: string;
}

/** The sheet: which product, its packages, sizes and colours, the line
 * above the button. From a form it goes to POST /v1/forms/{id}/sell (on a
 * front the owner may pick); on a front to the front's actions. */
export function SellSheet({
  title,
  formId,
  frontId,
  defaultLabel,
  onClose,
  onDone,
}: {
  title: string;
  formId?: string;
  frontId?: string;
  defaultLabel: string;
  onClose: () => void;
  onDone: (s: Sold) => void;
}) {
  const fronts = useLoad(
    () =>
      frontId
        ? Promise.resolve(null)
        : get<FrontsView>("/v1/storefront/fronts"),
    [frontId],
  );
  const [front, setFront] = useState(frontId ?? "");
  const shownFront =
    front || fronts.data?.fronts.find((f) => f.slug === "")?.id || "";
  const items = useLoad(
    () =>
      shownFront
        ? get<{ items: Sellable[] }>(
            `/v1/storefront/fronts/${shownFront}/sellable`,
          )
        : Promise.resolve({ items: [] as Sellable[] }),
    [shownFront],
  );
  const [item, setItem] = useState("");
  const product =
    items.data?.items.find((i) => i.id === item) ?? items.data?.items[0];
  const [rows, setRows] = useState<PackageRow[] | null>(null);
  const shownRows = rows ?? startingRows(product?.price_kobo ?? null);
  const [each, setEach] = useState(false);
  const [label, setLabel] = useState(defaultLabel);
  const [notice, setNotice] = useState("");
  const act = useAction();
  const turnOn = useAction();
  const read = readPackages(shownRows);
  // The Storefront (or Orders) area off: said as the sheet opens, or when
  // the server refuses, with the offer to turn it on.
  const off =
    areaOffOf(act.error) || areaOffOf(fronts.error) || areaOffOf(items.error);
  const setRow = (i: number, p: Partial<PackageRow>) =>
    setRows(shownRows.map((r, j) => (j === i ? { ...r, ...p } : r)));
  const send = () =>
    act.run(async () => {
      if (!product) return;
      const sells = {
        item: product.id,
        packages: read.packages,
        ...(product.sizes > 0 ? { choose: each ? "each" : "all" } : {}),
      };
      if (formId) {
        const out = await post<{ warnings: string[]; url: string }>(
          `/v1/forms/${formId}/sell`,
          {
            ...(front ? { front_id: front } : {}),
            label: label.trim(),
            sells,
            notice: notice.trim(),
          },
        );
        onDone({ warnings: out.warnings, url: out.url });
      } else {
        const out = await post<{ warnings: string[] }>(
          `/v1/storefront/fronts/${shownFront}/actions`,
          {
            kind: "order",
            label: label.trim(),
            sells,
            notice: notice.trim(),
          },
        );
        onDone({ warnings: out.warnings });
      }
    }, false);
  return (
    <Modal title={title} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void send();
        }}
      >
        <p className="hint">
          Buyers choose a package, say where it goes and how they pay. Each
          order comes to you like any order from your page.
        </p>
        {!frontId && fronts.data && fronts.data.fronts.length > 1 && (
          <Field label="On which page">
            <select
              value={shownFront}
              onChange={(e) => (
                setFront(e.target.value),
                setItem(""),
                setRows(null)
              )}
            >
              {fronts.data.fronts.map((f) => (
                <option key={f.id} value={f.id}>
                  {f.slug === "" ? "Your home page" : f.name}
                </option>
              ))}
            </select>
          </Field>
        )}
        {!off && <ErrorBox error={fronts.error ?? items.error} />}
        {!off && items.data && items.data.items.length === 0 && (
          <p className="notice">
            Add a product with a price to your list first: the form sells one of
            them.
          </p>
        )}
        {items.data && items.data.items.length > 0 && (
          <>
            <Field label="The product">
              <select
                value={product?.id ?? ""}
                onChange={(e) => (setItem(e.target.value), setRows(null))}
              >
                {items.data.items.map((i) => (
                  <option key={i.id} value={i.id}>
                    {i.name}
                    {i.price_kobo !== null
                      ? `, ${formatPrice(i.price_kobo)}`
                      : ""}
                  </option>
                ))}
              </select>
            </Field>
            <fieldset className="sell-packages">
              <legend>Packages</legend>
              <p className="hint">
                Up to {MAX_PACKAGES}: how many, and the price for all of them.
                Your words are optional: “1 item + free delivery”.
              </p>
              {shownRows.map((r, i) => {
                const p = read.packages.find((x) => x.qty === Number(r.qty));
                const warn =
                  p && product
                    ? dearerEach(p, product.price_kobo, product.name)
                    : "";
                return (
                  <div key={i} className="sell-row">
                    <Field label={`Items in package ${i + 1}`}>
                      <input
                        inputMode="numeric"
                        value={r.qty}
                        onChange={(e) => setRow(i, { qty: e.target.value })}
                      />
                    </Field>
                    <Field label={`Price of package ${i + 1} (₦)`}>
                      <input
                        inputMode="decimal"
                        value={r.price}
                        onChange={(e) => setRow(i, { price: e.target.value })}
                      />
                    </Field>
                    <Field label={`Words for package ${i + 1}`}>
                      <input
                        value={r.label}
                        maxLength={60}
                        onChange={(e) => setRow(i, { label: e.target.value })}
                      />
                    </Field>
                    {shownRows.length > 1 && (
                      <button
                        type="button"
                        className="small"
                        onClick={() =>
                          setRows(shownRows.filter((_, j) => j !== i))
                        }
                      >
                        Remove<span className="sr-only"> package {i + 1}</span>
                      </button>
                    )}
                    {warn && <p className="hint sell-warn">{warn}</p>}
                  </div>
                );
              })}
              {shownRows.length < MAX_PACKAGES && (
                <button
                  type="button"
                  className="small"
                  onClick={() =>
                    setRows([
                      ...shownRows,
                      {
                        qty: String(shownRows.length + 1),
                        price: "",
                        label: "",
                      },
                    ])
                  }
                >
                  Add a package
                </button>
              )}
            </fieldset>
            {product && product.sizes > 0 && (
              <fieldset className="choices">
                <legend>Sizes and colours</legend>
                <label className="choice">
                  <input
                    type="radio"
                    name="sell-each"
                    checked={!each}
                    onChange={() => setEach(false)}
                  />{" "}
                  One choice for the whole package
                </label>
                <label className="choice">
                  <input
                    type="radio"
                    name="sell-each"
                    checked={each}
                    onChange={() => setEach(true)}
                  />{" "}
                  A choice for each item in it
                </label>
              </fieldset>
            )}
            <Field label="Button" hint="What the order button says.">
              <input
                value={label}
                maxLength={40}
                onChange={(e) => setLabel(e.target.value)}
              />
            </Field>
            <Field
              label="Line above the button"
              hint="Optional: “Do not order if you are not ready to receive it in 2 to 4 days.” Left empty, it says “Please order only when you are ready to receive it.”"
            >
              <input
                value={notice}
                maxLength={160}
                onChange={(e) => setNotice(e.target.value)}
              />
            </Field>
            <p className="hint">
              Delivery areas, their fees and paying at the door are your order
              form's: change them under Storefront › Fronts. To offer free
              delivery, set the areas' fees to nothing.
            </p>
          </>
        )}
        {read.problem && items.data && items.data.items.length > 0 && (
          <p className="error-text">{read.problem}</p>
        )}
        {off ? (
          <div className="notice" role="alert">
            <p style={{ marginTop: 0 }}>
              {areaName(off)} is turned off for this business. Selling needs it:
              orders, your products and your page live there.
            </p>
            <ErrorBox error={turnOn.error} />
            <button
              type="button"
              className="primary"
              disabled={turnOn.busy}
              onClick={() =>
                void turnOn.run(
                  async () => {
                    await put("/v1/settings/areas", {
                      areas: { [off]: true },
                      confirm: true,
                    });
                    act.clear();
                    fronts.reload();
                    items.reload();
                  },
                  `${areaName(off)} is on`,
                )
              }
            >
              Turn on {areaName(off)}
            </button>
          </div>
        ) : (
          <ErrorBox error={act.error} />
        )}
        <div className="toolbar">
          <button
            type="submit"
            className="primary"
            disabled={act.busy || !product || !!read.problem}
          >
            {act.busy
              ? "Saving…"
              : formId
                ? "Sell with this form"
                : "Add the order form"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** One order form's code for a website: the frame that grows with it, or
 * a plain one. Asked for when shown, as it needs the published page. */
export function ActionEmbedCode({
  actionId,
  label,
}: {
  actionId: string;
  label: string;
}) {
  const [open, setOpen] = useState(false);
  const code = useLoad(
    () =>
      open
        ? get<ActionEmbed>(`/v1/storefront/embed?action=${actionId}`)
        : Promise.resolve(null),
    [open, actionId],
  );
  if (!open)
    return (
      <button type="button" className="small" onClick={() => setOpen(true)}>
        Code for your website<span className="sr-only"> for {label}</span>
      </button>
    );
  const c = code.data;
  const both = c ? `${c.frame}\n${c.script}` : "";
  return (
    <div className="sell-embed">
      <ErrorBox error={code.error} />
      {c && (
        <>
          <p className="hint">
            Put this where the form goes on your website. It works on the
            websites you proved are yours (On your website, under Page), and
            grows with the form.
          </p>
          <pre className="embed-code">{both}</pre>
          <Copy text={both} label={`Copy the code for ${label}`} />
          <p className="hint">
            A website builder that takes no script: this plain frame, of a fixed
            height.
          </p>
          <pre className="embed-code">{c.plain}</pre>
          <Copy text={c.plain} label={`Copy the plain frame for ${label}`} />
        </>
      )}
    </div>
  );
}
