// A One product front (docs/spec/forms-that-sell.md §5.4): one product on
// a page of its own. Here are the parts of the Fronts screens it has: the
// product and its packages when it is added, and its page's settings (the
// promise line, the feature chips, the stock line) on its own screen.
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { get, patch } from "../api";
import { ErrorBox, Field, useAction, useLoad } from "../ui";
import type { Front } from "../lib/fronts";
import { formatPrice } from "../lib/storefront";
import {
  MAX_PACKAGES,
  dearerEach,
  readPackages,
  startingRows,
  type Package,
  type PackageRow,
  type Sellable,
} from "../lib/sell";
import {
  CHIP_ICONS,
  MAX_CHIPS,
  MAX_CHIP_TEXT,
  MAX_PROMISE,
  readSettings,
  sellsLine,
  settingsRows,
  type ChipRow,
} from "../lib/oneproduct";

/** What the owner chose for a new One product front. */
export interface OneProductPick {
  item: string;
  name: string;
  packages: Package[];
  problem: string;
}

/** The product a new One product front sells, from the catalogue, and its
 * packages. */
export function OneProductFields({
  homeId,
  onChange,
}: {
  homeId: string;
  onChange: (p: OneProductPick) => void;
}) {
  const items = useLoad(
    () =>
      homeId
        ? get<{ items: Sellable[] }>(`/v1/storefront/fronts/${homeId}/sellable`)
        : Promise.resolve({ items: [] as Sellable[] }),
    [homeId],
  );
  const [item, setItem] = useState("");
  const [rows, setRows] = useState<PackageRow[] | null>(null);
  const list = items.data?.items ?? [];
  const product = list.find((i) => i.id === item) ?? list[0];
  const shown = rows ?? startingRows(product?.price_kobo ?? null);
  const read = readPackages(shown);
  const tell = (p: Sellable | undefined, r: PackageRow[]) => {
    const got = readPackages(r);
    onChange({
      item: p?.id ?? "",
      name: p?.name ?? "",
      packages: got.packages,
      problem: p ? got.problem : "Choose the product the page sells.",
    });
  };
  // The first product and its starting packages, once the list is read.
  useEffect(() => {
    if (product && rows === null) tell(product, shown);
  }, [product?.id]);
  const setRow = (i: number, x: Partial<PackageRow>) => {
    const next = shown.map((r, j) => (j === i ? { ...r, ...x } : r));
    setRows(next);
    tell(product, next);
  };
  if (items.error) return <ErrorBox error={items.error} />;
  if (!items.data) return <p className="hint">Loading your products…</p>;
  if (list.length === 0)
    return (
      <p className="notice">
        Add a product with a price to your list first, under{" "}
        <Link to="/storefront">Page</Link>: a One product page sells one of
        them.
      </p>
    );
  return (
    <>
      <Field label="The product">
        <select
          value={product?.id ?? ""}
          onChange={(e) => {
            const p = list.find((i) => i.id === e.target.value);
            setItem(e.target.value);
            setRows(null);
            tell(p, startingRows(p?.price_kobo ?? null));
          }}
        >
          {list.map((i) => (
            <option key={i.id} value={i.id}>
              {i.name}
              {i.price_kobo !== null ? `, ${formatPrice(i.price_kobo)}` : ""}
            </option>
          ))}
        </select>
      </Field>
      <fieldset className="sell-packages">
        <legend>Packages</legend>
        <p className="hint">
          Up to {MAX_PACKAGES}: how many, and the price for all of them.
        </p>
        {shown.map((r, i) => {
          const p = read.packages.find((x) => x.qty === Number(r.qty));
          const warn =
            p && product ? dearerEach(p, product.price_kobo, product.name) : "";
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
              {shown.length > 1 && (
                <button
                  type="button"
                  className="small"
                  onClick={() => {
                    const next = shown.filter((_, j) => j !== i);
                    setRows(next);
                    tell(product, next);
                  }}
                >
                  Remove<span className="sr-only"> package {i + 1}</span>
                </button>
              )}
              {warn && <p className="hint sell-warn">{warn}</p>}
            </div>
          );
        })}
      </fieldset>
      {read.problem && <p className="error-text">{read.problem}</p>}
      <p className="hint">
        The page shows the product's photos and words, delivered and paid at
        the door to your order form's delivery areas.
      </p>
    </>
  );
}

/** A One product front's page: what it sells, its promise line, its chips
 * and its stock line, saved together. */
export function OneProductSettingsCard({
  front,
  onSaved,
}: {
  front: Front;
  onSaved: () => void;
}) {
  const [rows, setRows] = useState(() => settingsRows(front.settings));
  const act = useAction();
  const read = readSettings(rows);
  const setChip = (i: number, c: Partial<ChipRow>) =>
    setRows({
      ...rows,
      chips: rows.chips.map((x, j) => (j === i ? { ...x, ...c } : x)),
    });
  return (
    <section className="card" aria-labelledby="op-page">
      <h2 id="op-page" style={{ marginTop: 0 }}>
        The page
      </h2>
      <p>
        <strong>Sells:</strong> {sellsLine(front.sells)}
      </p>
      <p className="hint">
        Its photos and price are the product's: change them under{" "}
        <Link to="/storefront">Page</Link>. Its headline and reason to buy are
        the name and line above.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(
            async () => (
              await patch(`/v1/storefront/fronts/${front.id}`, {
                settings: read.settings,
              }),
              onSaved()
            ),
            "Saved",
          );
        }}
      >
        <Field
          label="Promise line"
          hint="At the top, in your colour: “Pay on delivery nationwide”. Optional."
        >
          <input
            value={rows.promise}
            maxLength={MAX_PROMISE}
            onChange={(e) => setRows({ ...rows, promise: e.target.value })}
          />
        </Field>
        <label className="choice">
          <input
            type="checkbox"
            checked={rows.stock}
            onChange={(e) => setRows({ ...rows, stock: e.target.checked })}
          />{" "}
          Say how many are left (“Only 14 left”), from your stock
        </label>
        <fieldset className="sell-packages">
          <legend>Feature chips</legend>
          <p className="hint">
            Up to {MAX_CHIPS} short lines, each with an icon.
          </p>
          {rows.chips.map((c, i) => (
            <div key={i} className="sell-row">
              <Field label={`Icon of chip ${i + 1}`}>
                <select
                  value={c.icon}
                  onChange={(e) => setChip(i, { icon: e.target.value })}
                >
                  {CHIP_ICONS.map((x) => (
                    <option key={x.name} value={x.name}>
                      {x.words}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label={`Words of chip ${i + 1}`}>
                <input
                  value={c.text}
                  maxLength={MAX_CHIP_TEXT}
                  onChange={(e) => setChip(i, { text: e.target.value })}
                />
              </Field>
              <button
                type="button"
                className="small"
                onClick={() =>
                  setRows({ ...rows, chips: rows.chips.filter((_, j) => j !== i) })
                }
              >
                Remove<span className="sr-only"> chip {i + 1}</span>
              </button>
            </div>
          ))}
          {rows.chips.length < MAX_CHIPS && (
            <button
              type="button"
              className="small"
              onClick={() =>
                setRows({
                  ...rows,
                  chips: [...rows.chips, { icon: "check", text: "" }],
                })
              }
            >
              Add a chip
            </button>
          )}
        </fieldset>
        {read.problem && <p className="error-text">{read.problem}</p>}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button
            type="submit"
            className="primary"
            disabled={act.busy || read.problem !== ""}
          >
            Save the page
          </button>
        </div>
      </form>
    </section>
  );
}
