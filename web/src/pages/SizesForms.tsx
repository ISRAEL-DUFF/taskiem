import { useState } from "react";
import { del, get, sendWith } from "../api";
import { KitField, KitLoading } from "../kit";
import type { StockItem, StockView } from "../lib/stock";
import {
  choicePhotoPath,
  sendChoicePhoto,
  type PhotoPick,
} from "../lib/photos";
import { formatPrice, thumb } from "../lib/storefront";
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
  proposeGroup,
  sizesOf,
  sizesWords,
  type Choice,
  type Variant,
} from "../lib/variants";
import { ErrorBox, useLoad } from "../ui";
import { FormFrame, formKey, type Saver } from "./FormFrame";
import { PhotoAdd } from "./PhotoAdd";

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
      {was.length > 0 && <ColourPhotos item={item} />}
      {was.length > 0 && <StopSizes item={item} onSaved={onSaved} />}
    </FormFrame>
  );
}

/** A photo for each colour, which the page shows when a customer picks it
 * (the product's photos show for the rest). Set at once, not with Save.
 * Only a colour some size has is offered; the photos are read from the
 * stock, so a product not on the page shows them too. */
function ColourPhotos({ item }: { item: StockItem }) {
  const colour = (item.choices ?? []).find((c) => /^colou?rs?$/i.test(c.name));
  const sizes = useLoad(
    () =>
      colour
        ? get<StockView>("/v1/stock").then((r) =>
            sizesOf(r.items, item.id, true),
          )
        : Promise.resolve<StockItem[]>([]),
    [item.id],
  );
  const [busy, setBusy] = useState<string>();
  const [error, setError] = useState<unknown>();
  const [said, setSaid] = useState("");
  if (!colour) return null;
  const of = (value: string) =>
    (sizes.data ?? []).filter((z) => z.variant?.[colour.name] === value);
  const values = colour.values.filter((v) => of(v).length > 0);
  const photoOf = (value: string) =>
    thumb(of(value).find((z) => z.photo)?.photo);
  const run = async (
    value: string,
    f: () => Promise<unknown>,
    line: string,
  ) => {
    setBusy(value);
    setError(undefined);
    try {
      await f();
      setSaid(line);
      sizes.reload();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(undefined);
    }
  };
  return (
    <fieldset className="tk-grid-group" aria-describedby="colour-photos-hint">
      <legend>A photo for each {colour.name.toLowerCase()}</legend>
      <p className="hint" id="colour-photos-hint">
        Your page shows it when a customer picks that{" "}
        {colour.name.toLowerCase()}. Saved at once.
      </p>
      {values.map((value) => {
        const pic = photoOf(value);
        return (
          <div key={value} className="tk-colour-photo">
            {pic ? (
              <img
                src={pic}
                alt={`${value} ${item.name}`}
                width={48}
                height={48}
              />
            ) : (
              <span className="tk-colour-photo-none" aria-hidden="true" />
            )}
            <span className="tk-colour-photo-name">{value}</span>
            <PhotoAdd
              room={1}
              busy={busy === value}
              onPick={(picks: PhotoPick[]) => {
                const first = picks[0];
                if (first)
                  void run(
                    value,
                    () => sendChoicePhoto(item.id, colour.name, value, first),
                    `Photo for ${value} saved`,
                  );
              }}
            />
            {pic && (
              <button
                type="button"
                className="link"
                disabled={busy === value}
                onClick={() =>
                  void run(
                    value,
                    () => del(choicePhotoPath(item.id, colour.name, value)),
                    `Photo for ${value} taken out`,
                  )
                }
              >
                Remove<span className="sr-only"> the photo for {value}</span>
              </button>
            )}
          </div>
        );
      })}
      <ErrorBox error={error} />
      {said && (
        <p className="tk-form-now" role="status">
          {said}
        </p>
      )}
    </fieldset>
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

/** Make one product with sizes and colours (Part B, grouping): the items
 * chosen in the Products sheet become one product's sizes and colours.
 * Taskiem proposes the product's name, its choices and each item's values
 * from their names; the owner changes any of it. Each item keeps its own
 * stock, history, price and links. */
export function GroupForm({
  items,
  onSaved,
  onCancel,
}: {
  items: StockItem[];
  onSaved: (line: string | null) => void;
  onCancel: () => void;
}) {
  const [first] = useState(() => proposeGroup(items));
  const [name, setName] = useState(first.name);
  const [choices, setChoices] = useState<string[]>(
    first.choices.length ? first.choices : ["Size"],
  );
  const [values, setValues] = useState<Record<string, Variant>>(first.values);
  // One key for this grouping: a retry makes nothing twice.
  const [saveKey] = useState(formKey);
  const named = choices.map((c) => c.trim());
  const valueOf = (id: string, i: number) =>
    values[id]?.[choices[i] ?? ""] ?? values[id]?.[named[i] ?? ""] ?? "";
  const filled = items.map((it) =>
    Object.fromEntries(named.map((c, i) => [c, valueOf(it.id, i).trim()])),
  );
  const keys = filled.map((v) => keyOf(v));
  const problem = !name.trim()
    ? "Give the product a name."
    : named.some((c) => !c)
      ? "Give each choice a name: Size, Colour…"
      : new Set(named.map((c) => c.toLowerCase())).size !== named.length
        ? "Each choice has its own name."
        : filled.some((v) => Object.values(v).some((x) => !x))
          ? "Say each item's size and colour."
          : new Set(keys).size !== keys.length
            ? "Two items have the same size and colour: change one."
            : "";
  const setChoice = (i: number, next: string) => {
    const was = choices[i] ?? "";
    setChoices(choices.map((c, j) => (j === i ? next : c)));
    // Each item's value stays with the renamed choice.
    setValues(
      Object.fromEntries(
        Object.entries(values).map(([id, v]) => {
          const { [was]: x, ...rest } = v;
          return [id, { ...rest, [next]: x ?? "" }];
        }),
      ),
    );
  };
  const setValue = (id: string, i: number, x: string) =>
    setValues({ ...values, [id]: { ...values[id], [choices[i] ?? ""]: x } });
  const save: Saver = async () => {
    await postKeyed("/v1/stock/group", saveKey, {
      name: name.trim(),
      choices: named,
      items: items.map((it, i) => ({ item_id: it.id, variant: filled[i] })),
    });
    return `${name.trim()}: ${sizesWords(items.length)}`;
  };
  return (
    <FormFrame
      save={save}
      onSaved={onSaved}
      onCancel={onCancel}
      saveWords="Make one product"
      ready={!problem}
    >
      <p className="tk-form-now">
        These {items.length} become one product&apos;s sizes and colours. Each
        keeps its own stock, history and price.
      </p>
      <KitField label="The product's name">
        <input
          value={name}
          maxLength={120}
          onChange={(e) => setName(e.target.value)}
        />
      </KitField>
      <fieldset className="tk-choice">
        <legend>Its choices</legend>
        {choices.map((c, i) => (
          <KitField
            key={i}
            label={`Choice ${i + 1}`}
            hint="Size, Colour, Style…"
          >
            <input
              value={c}
              maxLength={30}
              onChange={(e) => setChoice(i, e.target.value)}
            />
          </KitField>
        ))}
        {choices.length < MAX_CHOICES && (
          <button
            type="button"
            className="tk-btn soft"
            onClick={() => setChoices([...choices, ""])}
          >
            Add a choice
          </button>
        )}
        {choices.length > 1 && (
          <button
            type="button"
            className="tk-btn soft"
            onClick={() => setChoices(choices.slice(0, -1))}
          >
            Take away {choices[choices.length - 1] || "the last choice"}
          </button>
        )}
      </fieldset>
      {items.map((it) => (
        <fieldset key={it.id} className="tk-choice" data-testid="group-item">
          <legend>{it.name}</legend>
          {choices.map((c, i) => (
            <KitField key={i} label={c.trim() || `Choice ${i + 1}`}>
              <input
                value={valueOf(it.id, i)}
                maxLength={30}
                onChange={(e) => setValue(it.id, i, e.target.value)}
              />
            </KitField>
          ))}
        </fieldset>
      ))}
      {problem && (
        <p className="tk-form-now" role="status">
          {problem}
        </p>
      )}
    </FormFrame>
  );
}
