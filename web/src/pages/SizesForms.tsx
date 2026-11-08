import { useState } from "react";
import { get, sendWith } from "../api";
import { KitField, KitLoading } from "../kit";
import type { StockItem, StockView } from "../lib/stock";
import { formatPrice } from "../lib/storefront";
import { nairaKobo } from "../lib/toolforms";
import {
  allCells,
  keyOf,
  label,
  MAX_CHOICES,
  MAX_SIZES,
  MAX_VALUES,
  parseValues,
  PRESETS,
  sizesOf,
  sizesWords,
  type Choice,
  type Variant,
} from "../lib/variants";
import { ErrorBox, useLoad } from "../ui";
import { FormFrame, formKey, type Saver } from "./FormFrame";

// Sizes and colours in the chat tools (docs/spec/chat-categories-and-
// variants.md, Part B): a product's choices and the grid of what it sells,
// each box its count; and goods received by the grid. Every combination
// starts on sale; a box left empty is one not sold. The words are "sizes
// and colours" and "choices", never "variant".

const put = <T,>(path: string, key: string, body: unknown) =>
  sendWith<T>("PUT", path, body, {
    "Idempotency-Key": key,
    "X-Taskiem-Diary": "1",
  });
const postKeyed = <T,>(path: string, key: string, body: unknown) =>
  sendWith<T>("POST", path, body, {
    "Idempotency-Key": key,
    "X-Taskiem-Diary": "1",
  });

/** A grid's boxes in groups: by every choice but the last, the last's
 * values side by side (they wrap on a phone; nothing scrolls sideways). */
export function groups(
  cells: Variant[],
  cs: Choice[],
): { label: string; cells: Variant[] }[] {
  const head = cs.slice(0, -1);
  const out: { label: string; cells: Variant[] }[] = [];
  for (const v of cells) {
    const l = head.length ? label(v, head) : "";
    const g = out.find((x) => x.label === l);
    if (g) g.cells.push(v);
    else out.push({ label: l, cells: [v] });
  }
  return out;
}

/** The product's choices as the editor holds them: a name, and its values
 * typed on a line. */
type Draft = { name: string; text: string };

/** A grid's variant in the product's choices as they were, so a value
 * corrected in place (40 to 43) is the same size, renamed. */
function asWas(v: Variant, now: Choice[], was: Choice[]): Variant {
  const out: Variant = {};
  now.forEach((c, i) => {
    const old = was[i];
    const x = v[c.name] ?? "";
    const at = c.values.indexOf(x);
    out[old?.name ?? c.name] =
      old && old.values.length === c.values.length && at >= 0
        ? (old.values[at] ?? x)
        : x;
  });
  return out;
}

export function SizesEditor({
  item,
  onSaved,
  onCancel,
}: {
  item: StockItem;
  onSaved: (line: string | null) => void;
  onCancel: () => void;
}) {
  const all = useLoad(
    () => get<StockView>("/v1/stock").then((r) => r.items),
    [],
  );
  const was: Choice[] = item.choices ?? [];
  const [drafts, setDrafts] = useState<Draft[]>(() =>
    was.length
      ? was.map((c) => ({ name: c.name, text: c.values.join(", ") }))
      : [{ name: "Size", text: "" }],
  );
  // What the boxes hold, by the variant's key now; "" is not sold.
  const [counts, setCounts] = useState<Record<string, string>>({});
  const [prices, setPrices] = useState<Record<string, string>>({});
  // Those with a price of their own, as the product records them (by the
  // key they had): their boxes start with it, the rest follow the product.
  const ownWas = new Set(item.own_price ?? []);
  const [own, setOwn] = useState(ownWas.size > 0);
  const [fill, setFill] = useState("");
  const cs: Choice[] = drafts
    .map((d) => ({ name: d.name.trim(), values: parseValues(d.text) }))
    .filter((c) => c.name && c.values.length);
  const cells = allCells(cs);
  // What it has now, by the key it had: counts, own prices, rows.
  const before = new Map<string, StockItem>();
  for (const v of all.data ? sizesOf(all.data, item.id, true) : [])
    if (v.variant) before.set(keyOf(v.variant), v);
  const old = (v: Variant) => before.get(keyOf(asWas(v, cs, was)));
  const countOf = (v: Variant) => {
    const k = keyOf(v);
    if (k in counts) return counts[k] ?? "";
    const o = old(v);
    if (o && !o.available) return "";
    return o ? String(o.stock ?? 0) : "0";
  };
  const priceOf = (v: Variant) => {
    const k = keyOf(v);
    if (k in prices) return prices[k] ?? "";
    const o = old(v);
    return o?.price_kobo !== undefined && ownWas.has(keyOf(asWas(v, cs, was)))
      ? String(o.price_kobo / 100)
      : "";
  };
  const sold = cells.filter((v) => countOf(v).trim() !== "");
  // The product's own stock, while it has some, moves into its new sizes
  // and colours: what they take, and what is left over or added.
  const shelf = item.stock !== null && item.stock > 0 ? item.stock : 0;
  const taken = sold
    .filter((v) => !old(v))
    .reduce((n, v) => n + (Number(countOf(v)) || 0), 0);
  const problem = drafts.some((d) => parseValues(d.text).length > MAX_VALUES)
    ? `A choice has up to ${MAX_VALUES} values.`
    : sold.length > MAX_SIZES
      ? `Up to ${MAX_SIZES} sizes and colours in all: clear the boxes of those you do not sell.`
      : sold.some((v) => !/^\d+$/.test(countOf(v).trim()))
        ? "A box holds how many there are: a whole number, or empty for one you do not sell."
        : own &&
            sold.some(
              (v) =>
                priceOf(v).trim() !== "" && Number.isNaN(nairaKobo(priceOf(v))),
            )
          ? "A price in naira, like 45,000."
          : "";
  const save: Saver = async () => {
    await put(
      `/v1/stock/items/${encodeURIComponent(item.id)}/variants`,
      formKey(),
      {
        choices: cs,
        cells: sold.map((v) => {
          const p = own ? priceOf(v).trim() : "";
          return {
            id: old(v)?.id,
            variant: v,
            count: Number(countOf(v)),
            ...(p ? { price_kobo: nairaKobo(p) } : {}),
          };
        }),
      },
    );
    return `${item.name}: ${sizesWords(sold.length)}`;
  };
  const setDraft = (i: number, d: Partial<Draft>) =>
    setDrafts(drafts.map((x, j) => (j === i ? { ...x, ...d } : x)));
  if (!all.data)
    return all.error ? (
      <ErrorBox error={all.error} />
    ) : (
      <KitLoading label="Loading its sizes and colours…" />
    );
  return (
    <FormFrame
      save={save}
      onSaved={onSaved}
      onCancel={onCancel}
      ready={cs.length > 0 && sold.length > 0 && !problem}
    >
      <p className="tk-form-now">
        Each size and colour has its own stock. {item.name} itself keeps none:
        its stock is theirs together.
      </p>
      {drafts.map((d, i) => (
        <fieldset key={i} className="tk-choice">
          <legend>Choice {i + 1}</legend>
          <KitField label="Its name" hint="Size, Colour, Material…">
            <input
              value={d.name}
              maxLength={30}
              onChange={(e) => setDraft(i, { name: e.target.value })}
            />
          </KitField>
          <KitField label="Its values" hint="With commas between: 40, 41, 42">
            <input
              value={d.text}
              onChange={(e) => setDraft(i, { text: e.target.value })}
            />
          </KitField>
          <div
            className="tk-chips tk-field-chips"
            role="group"
            aria-label={`Ready-made values for choice ${i + 1}`}
          >
            {PRESETS.map((p) => (
              <button
                key={p.label}
                type="button"
                onClick={() =>
                  setDraft(i, {
                    name: d.name.trim() || p.name,
                    text: p.values.join(", "),
                  })
                }
              >
                {p.label}
              </button>
            ))}
          </div>
          {drafts.length > 1 && (
            <button
              type="button"
              className="link"
              onClick={() => setDrafts(drafts.filter((_, j) => j !== i))}
            >
              Remove this choice
            </button>
          )}
        </fieldset>
      ))}
      {drafts.length < MAX_CHOICES && (
        <button
          type="button"
          className="tk-btn soft"
          onClick={() =>
            setDrafts([
              ...drafts,
              { name: drafts.length === 1 ? "Colour" : "", text: "" },
            ])
          }
        >
          Add a choice
        </button>
      )}
      {cells.length > 0 && (
        <>
          <div className="tk-grid-tools">
            <KitField
              label="Same number for all"
              hint="Then change the ones that differ."
            >
              <input
                inputMode="numeric"
                value={fill}
                onChange={(e) => setFill(e.target.value)}
              />
            </KitField>
            <button
              type="button"
              className="tk-btn soft"
              disabled={!/^\d+$/.test(fill.trim())}
              onClick={() =>
                setCounts(
                  Object.fromEntries(cells.map((v) => [keyOf(v), fill.trim()])),
                )
              }
            >
              Fill
            </button>
          </div>
          <label className="tk-check">
            <input
              type="checkbox"
              checked={own}
              onChange={(e) => setOwn(e.target.checked)}
            />
            Some cost more or less than{" "}
            {item.price_kobo !== undefined
              ? formatPrice(item.price_kobo)
              : "the product"}
          </label>
          <p className="hint">
            How many of each. Empty a box for one you do not sell.
          </p>
          {groups(cells, cs).map((g) => (
            <fieldset key={g.label || "all"} className="tk-grid-group">
              <legend>{g.label || "How many"}</legend>
              <div className="tk-grid-cells">
                {g.cells.map((v) => {
                  const k = keyOf(v);
                  const last = cs[cs.length - 1];
                  const name = last ? (v[last.name] ?? "") : "";
                  return (
                    <div key={k} className="tk-grid-cell">
                      <label>
                        <span>{name}</span>
                        <input
                          inputMode="numeric"
                          aria-label={`${label(v, cs)}: how many`}
                          value={countOf(v)}
                          placeholder="–"
                          onChange={(e) =>
                            setCounts({ ...counts, [k]: e.target.value })
                          }
                        />
                      </label>
                      {own && (
                        <input
                          inputMode="decimal"
                          className="tk-grid-price"
                          aria-label={`${label(v, cs)}: its own price in naira`}
                          placeholder={
                            item.price_kobo !== undefined
                              ? String(item.price_kobo / 100)
                              : "₦"
                          }
                          value={priceOf(v)}
                          onChange={(e) =>
                            setPrices({ ...prices, [k]: e.target.value })
                          }
                        />
                      )}
                    </div>
                  );
                })}
              </div>
            </fieldset>
          ))}
          <p className="tk-form-now" role="status">
            {sizesWords(sold.length)} on sale
          </p>
          {shelf > 0 && (
            <p className="tk-form-now" data-testid="sizes-share">
              {item.name} has {shelf} on the shelf now: they move into its sizes
              and colours, {taken > shelf ? shelf : taken} counted in so far.
              {taken < shelf &&
                ` ${shelf - taken} not counted into one will be recorded as missing.`}
              {taken > shelf &&
                ` ${taken - shelf} more than that will be recorded as new stock.`}
            </p>
          )}
        </>
      )}
      {problem && (
        <p className="tk-field-error" role="alert">
          {problem}
        </p>
      )}
      {was.length > 0 && <StopSizes item={item} onSaved={onSaved} />}
    </FormFrame>
  );
}

/** No more sizes and colours: all off sale (kept, for their history). */
function StopSizes({
  item,
  onSaved,
}: {
  item: StockItem;
  onSaved: (line: string | null) => void;
}) {
  const [sure, setSure] = useState(false);
  const [error, setError] = useState<unknown>();
  return sure ? (
    <div className="tk-stop-sizes">
      <p>
        Take all its sizes and colours off sale? Their sales stay in your
        records.
      </p>
      <button
        type="button"
        className="tk-btn soft"
        onClick={() =>
          put(
            `/v1/stock/items/${encodeURIComponent(item.id)}/variants`,
            formKey(),
            { choices: [], cells: [] },
          ).then(() => onSaved(`${item.name}: no sizes and colours`), setError)
        }
      >
        Yes, take them off sale
      </button>
      <ErrorBox error={error} />
    </div>
  ) : (
    <button type="button" className="link" onClick={() => setSure(true)}>
      Stop selling it in sizes and colours
    </button>
  );
}

/** Goods received by the grid: a box for each size and colour on sale,
 * the low ones marked. */
export function GridStockIn({
  item,
  onSaved,
  canEdit = true,
}: {
  item: StockItem;
  onSaved: (line: string | null) => void;
  canEdit?: boolean;
}) {
  const all = useLoad(
    () => get<StockView>("/v1/stock").then((r) => r.items),
    [],
  );
  const [came, setCame] = useState<Record<string, string>>({});
  const cs = item.choices ?? [];
  if (!all.data)
    return all.error ? (
      <ErrorBox error={all.error} />
    ) : (
      <KitLoading label="Loading its sizes and colours…" />
    );
  const sizes = sizesOf(all.data, item.id).filter((v) => v.variant);
  const byKey = new Map(sizes.map((v) => [keyOf(v.variant ?? {}), v]));
  const cells = sizes.map((v) => v.variant ?? {});
  const lines = sizes
    .map((v) => ({ item_id: v.id, change: Number((came[v.id] ?? "").trim()) }))
    .filter((l) => Number.isInteger(l.change) && l.change > 0);
  const bad = Object.values(came).some(
    (x) => x.trim() !== "" && !/^\d+$/.test(x.trim()),
  );
  const save: Saver = async () => {
    await postKeyed("/v1/stock/movements/grid", formKey(), {
      reason: "received",
      lines,
    });
    const n = lines.reduce((a, l) => a + l.change, 0);
    return `Stock in: ${item.name}, +${n}`;
  };
  return (
    <FormFrame
      save={save}
      onSaved={onSaved}
      saveWords="Record stock in"
      ready={lines.length > 0 && !bad}
      canEdit={canEdit}
    >
      <p className="tk-form-now">
        How many of each came in. The low ones are marked.
      </p>
      {groups(cells, cs).map((g) => (
        <fieldset key={g.label || "all"} className="tk-grid-group">
          <legend>{g.label || "Came in"}</legend>
          <div className="tk-grid-cells">
            {g.cells.map((v) => {
              const it = byKey.get(keyOf(v));
              if (!it) return null;
              const last = cs[cs.length - 1];
              return (
                <div
                  key={it.id}
                  className={`tk-grid-cell${it.low ? " low" : ""}`}
                >
                  <label>
                    <span>
                      {last ? v[last.name] : label(v, cs)}
                      <small>
                        {it.stock === null ? "no count" : `${it.stock} now`}
                        {it.low ? " · low" : ""}
                      </small>
                    </span>
                    <input
                      inputMode="numeric"
                      aria-label={`${label(v, cs)}: how many came in${it.low ? " (low)" : ""}`}
                      placeholder="0"
                      value={came[it.id] ?? ""}
                      onChange={(e) =>
                        setCame({ ...came, [it.id]: e.target.value })
                      }
                    />
                  </label>
                </div>
              );
            })}
          </div>
        </fieldset>
      ))}
      {bad && (
        <p className="tk-field-error" role="alert">
          A box holds a whole number.
        </p>
      )}
    </FormFrame>
  );
}
