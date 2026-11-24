import { useEffect, useId, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, get, patch, post, postOnce } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { thingsOfLine } from "../lib/things";
import type { Purchase, PurchaseLine } from "../lib/purchases";
import type { StockItemRow, StockItemsView } from "../lib/stock";
import {
  NEW_STOCK,
  TREATMENTS,
  askUses,
  choiceProblem,
  billLineOf,
  choiceWords,
  fromLine,
  knownLabel,
  labelChoices,
  lineBody,
  lineLabel,
  lineLocked,
  lineMark,
  madeStockItem,
  newStockRequest,
  suggestChoice,
  trackAskOf,
  trackAskWords,
  usesProblem,
  usesQuestion,
  type Choice,
  type LabelsView,
  type LineBody,
  type SpendLabel,
  type TrackAsk,
} from "../lib/treat";
import { useLoad } from "../ui";

// The question on a line bought (what a business buys §6): "<item> from
// <supplier>: How should we treat it?", with Just record it, Add to stock
// (which stock item, or a new one asking Do you sell it?) and Keep track of
// it, and its label. Used by every order sheet: a supplier's price list,
// their shop, an order they drafted for us, and an order by hand.

/** What the question reads once for a sheet: the business's labels, its
 * stock items, and the names it has kept track of before. */
export interface TreatData {
  labels?: SpendLabel[];
  stockItems?: StockItemRow[];
  tracked?: string[];
  ready: boolean;
  reloadLabels: () => void;
}

export function useTreatData(on = true): TreatData {
  // Things we have only for those who may see them (table.read).
  const read = useAuth().can("table.read");
  const labels = useLoad(
    () =>
      on
        ? get<LabelsView>("/v1/money/labels").then(
            (r) => r.labels,
            (): SpendLabel[] => [],
          )
        : Promise.resolve(undefined),
    [on],
  );
  const stock = useLoad(
    () =>
      on
        ? get<StockItemsView>("/v1/stock/stock-items").then(
            (r) => r.items.filter((x) => !x.retired),
            (): StockItemRow[] => [],
          )
        : Promise.resolve(undefined),
    [on],
  );
  const things = useLoad(
    () =>
      !on
        ? Promise.resolve(undefined)
        : read
          ? get<{ things: { name: string }[] }>("/v1/things?all=true").then(
              (r) => r.things.map((t) => t.name),
              (): string[] => [],
            )
          : Promise.resolve([]),
    [on, read],
  );
  return {
    labels: labels.data,
    stockItems: stock.data,
    tracked: things.data,
    ready: !!labels.data && !!stock.data && !!things.data,
    reloadLabels: labels.reload,
  };
}

/** What making the answers has made so far, kept for a sheet's life: a
 * retry makes each label and stock item once. */
export type Made = Map<string, string>;

/** An answer as the API takes it: the label found or made by its name, a
 * new stock item made (Do you sell it? Yes: a product; No: a material). */
export async function resolveChoice(
  c: Choice,
  data: Pick<TreatData, "labels">,
  made: Made,
  unit?: string,
): Promise<LineBody> {
  let labelId = knownLabel(data.labels, c.label);
  const name = c.label.trim();
  if (!labelId && name) {
    const k = `label:${name.toLowerCase()}`;
    labelId = made.get(k);
    if (!labelId) {
      // Making a label needs invoice.manage or table.edit, more than
      // drafting or confirming may (H2 review fix 5): refused or failed,
      // the line goes without it rather than failing the whole order.
      try {
        labelId = (await post<SpendLabel>("/v1/money/labels", { name })).id;
        made.set(k, labelId);
      } catch (e) {
        if (!(e instanceof ApiError)) throw e;
        labelId = undefined;
      }
    }
  }
  let stockItemId = c.stockItemId;
  if (c.treatment === "stock" && stockItemId === NEW_STOCK) {
    const k = `stock:${(c.newName ?? "").trim().toLowerCase()}`;
    stockItemId = made.get(k);
    if (!stockItemId) {
      const once = made.get(`key:${k}`) ?? crypto.randomUUID();
      made.set(`key:${k}`, once);
      const out = await postOnce<{
        item: { id: string; material?: boolean; stock_item_id?: string };
      }>("/v1/stock/items", once, newStockRequest(c, unit));
      stockItemId = madeStockItem(out.item);
      made.set(k, stockItemId);
    }
  }
  return lineBody(c, { stockItemId, labelId });
}

/** One line's question. Remembered (memory) and not changed for this
 * order, it shows its treatment in a line with Change for this order;
 * locked once something on it came. Not remembered, it opens on Taskiem's
 * suggestion (onChange is called with it once it is known). */
export function TreatQuestion({
  item,
  supplier,
  category,
  data,
  memory,
  value,
  onChange,
  locked = false,
  rememberable = true,
  suggested,
  unit,
}: {
  item: string;
  supplier: string;
  /** The unit the line is bought in ("bag"), for how many of a stock item
   * counted in another unit one of it is. */
  unit?: string;
  category?: string;
  data: TreatData;
  /** What the business remembers of the item, or what the order line
   * holds already. */
  memory?: Choice;
  /** This order's answer; undefined: as remembered (or not yet known). */
  value?: Choice;
  /** seeded: the answer is Taskiem's suggestion, not the person's. */
  onChange: (c: Choice | undefined, seeded?: boolean) => void;
  locked?: boolean;
  /** Offer "Remember for next time" when a remembered item changes. */
  rememberable?: boolean;
  /** The order line's own suggestion (the server's), in place of the
   * one worked out here. */
  suggested?: Choice;
}) {
  const id = useId();
  // How many of the stock item one is, as typed (Choice.uses holds it
  // once it is a number).
  const [usesText, setUsesText] = useState(value?.uses ? String(value.uses) : "");
  const open = !locked && (!memory || !!value);
  const sugg = useLoad(
    () =>
      open
        ? get<LabelsView>(
            `/v1/money/labels?item=${encodeURIComponent(item)}${category ? `&category=${encodeURIComponent(category)}` : ""}`,
          ).then(
            (r) => r.suggestions,
            () => [],
          )
        : Promise.resolve(undefined),
    [open, item, category],
  );
  // Not remembered: Taskiem's suggestion, once, as the starting answer.
  const seeded = useRef(false);
  // The answer Taskiem seeded, while the person has not changed it.
  const seed = useRef<Choice>(undefined);
  useEffect(() => {
    if (memory || value || seeded.current || !data.ready || !sugg.data) return;
    seeded.current = true;
    seed.current = suggested
      ? { ...suggested, label: suggested.label || (sugg.data[0]?.name ?? "") }
      : suggestChoice(item, data.stockItems, data.tracked, sugg.data);
    onChange(seed.current, true);
  }, [memory, value, suggested, data.ready, data.stockItems, data.tracked, sugg.data, item, onChange]);
  // Whether the answer asks how many of its stock item one is (askUses):
  // kept on the answer, so a sheet's Save waits for it (choiceProblem)
  // and only an answer asked is sent (lineBody).
  const unitOf = (v: Choice | undefined) =>
    v?.treatment === "stock" && v.stockItemId && v.stockItemId !== NEW_STOCK
      ? data.stockItems?.find((s) => s.id === v.stockItemId)?.unit
      : undefined;
  const su = open ? unitOf(value) : undefined;
  const usesAsk = su !== undefined && askUses(unit, su) ? usesQuestion(su, unit, item) : undefined;
  useEffect(() => {
    if (!value || value.usesAsk === usesAsk) return;
    const wasSeed = value === seed.current;
    const next = { ...value, usesAsk };
    if (wasSeed) seed.current = next;
    onChange(next, wasSeed);
  }, [value, usesAsk, onChange]);
  const stockName = (sid: string) =>
    data.stockItems?.find((s) => s.id === sid)?.name;
  const head = (
    <>
      <b>{item}</b> from <b>{supplier}</b>
    </>
  );
  if (!open) {
    const c = value ?? memory;
    return (
      <div className="treat treat-compact" data-testid="treat">
        <p>
          {head}
          <span className="treat-now">
            {c ? choiceWords(c, stockName) : "Just record it · Not labelled"}
          </span>
          {locked && (
            <span className="hint">
              <Icon name="lock" /> It came, so it stays as it is.
            </span>
          )}
        </p>
        {!locked && memory && (
          <button
            type="button"
            className="small"
            onClick={() => onChange({ ...memory, remember: false })}
          >
            Change for this order
            <span className="sr-only"> of {item}</span>
          </button>
        )}
      </div>
    );
  }
  if (!value)
    return (
      <div className="treat" data-testid="treat">
        <p className="hint">{head}: working out a suggestion…</p>
      </div>
    );
  const c = value;
  // Another stock item drops how many of the last one a line was (H2
  // review fix 3): it is asked again of the new one, if at all.
  const set = (patch: Partial<Choice>) => {
    if (patch.stockItemId !== undefined && patch.stockItemId !== c.stockItemId) {
      patch = { ...patch, uses: undefined, usesBad: false, usesKept: false };
      setUsesText("");
    }
    onChange({ ...c, ...patch });
  };
  // The chosen stock item's unit (none for a new one, made in the line's).
  const stockUnit = su;
  const problem = choiceProblem(c);
  const picks = labelChoices(data.labels, sugg.data);
  return (
    <fieldset className="treat" data-testid="treat">
      <legend>
        {head}
        <span className="treat-ask">How should we treat it?</span>
      </legend>
      <div role="radiogroup" aria-label={`How to treat ${item}`}>
        {TREATMENTS.map((t) => (
          <label key={t.value} className="sf-radio">
            <input
              type="radio"
              name={`${id}-treat`}
              checked={c.treatment === t.value}
              onChange={() =>
                set({
                  treatment: t.value,
                  stockItemId:
                    t.value === "stock"
                      ? (c.stockItemId ??
                        data.stockItems?.find(
                          (s) => s.name.toLowerCase() === item.toLowerCase(),
                        )?.id ??
                        NEW_STOCK)
                      : c.stockItemId,
                  newName: c.newName ?? item,
                })
              }
            />
            <span>
              {t.label}
              <span className="block hint">{t.hint}</span>
            </span>
          </label>
        ))}
      </div>
      {c.treatment === "stock" && (
        <div className="treat-more">
          <label className="field">
            <span>Which stock item?</span>
            <select
              value={c.stockItemId ?? NEW_STOCK}
              onChange={(e) => set({ stockItemId: e.target.value })}
            >
              {(data.stockItems ?? []).map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
              <option value={NEW_STOCK}>A new stock item…</option>
            </select>
          </label>
          {stockUnit !== undefined && usesAsk && (
            <label className="field" data-testid="treat-uses">
              <span>{usesAsk}</span>
              <input
                inputMode="numeric"
                value={usesText}
                placeholder="Like 1000"
                aria-describedby={`${id}-uses-hint`}
                aria-invalid={!!usesProblem(usesText)}
                onChange={(e) => {
                  setUsesText(e.target.value);
                  const bad = !!usesProblem(e.target.value);
                  const t = e.target.value.trim().replace(/,/g, "");
                  set({ uses: !bad && t !== "" ? Number(t) : undefined, usesBad: bad });
                }}
              />
              <small className="hint" id={`${id}-uses-hint`}>
                {usesProblem(usesText) ||
                  `${stockName(c.stockItemId ?? "") ?? "It"} is counted in ${stockUnit}. Said once, it is remembered for this item.`}
              </small>
            </label>
          )}
          {c.stockItemId === NEW_STOCK && (
            <>
              <label className="field">
                <span>Its name</span>
                <input
                  value={c.newName ?? ""}
                  onChange={(e) => set({ newName: e.target.value })}
                />
              </label>
              <fieldset className="sf-choices">
                <legend>Do you sell it?</legend>
                <label className="sf-radio">
                  <input
                    type="radio"
                    name={`${id}-sells`}
                    checked={c.sells === true}
                    onChange={() => set({ sells: true })}
                  />
                  Yes, customers buy it
                </label>
                <label className="sf-radio">
                  <input
                    type="radio"
                    name={`${id}-sells`}
                    checked={c.sells === false}
                    onChange={() => set({ sells: false })}
                  />
                  No, we use it to make or pack things
                </label>
              </fieldset>
              {c.sells && (
                <label className="field">
                  <span>Selling price (₦, optional)</span>
                  <input
                    inputMode="decimal"
                    value={c.price ?? ""}
                    onChange={(e) => set({ price: e.target.value })}
                  />
                </label>
              )}
            </>
          )}
        </div>
      )}
      <LabelPick
        item={item}
        value={c.label}
        picks={picks}
        onChange={(label) => set({ label })}
      />
      {memory && rememberable && (
        <label className="sf-toggle">
          <input
            type="checkbox"
            checked={!!c.remember}
            onChange={(e) => set({ remember: e.target.checked })}
          />
          Remember for next time
        </label>
      )}
      {memory && (
        <button type="button" className="small" onClick={() => onChange(undefined)}>
          As remembered<span className="sr-only"> for {item}</span>
        </button>
      )}
      {problem && <p className="hint treat-problem">{problem}</p>}
    </fieldset>
  );
}

/** A label typed, or picked from chips (the business's own, by use, then
 * suggestions): the question's, and Add something we have's. */
export function LabelPick({
  item,
  value,
  picks,
  onChange,
}: {
  item: string;
  value: string;
  picks: string[];
  onChange: (label: string) => void;
}) {
  return (
    <div className="treat-more">
      <label className="field">
        <span>Label</span>
        <input
          value={value}
          maxLength={60}
          autoComplete="off"
          placeholder="Type one, or pick below"
          onChange={(e) => onChange(e.target.value)}
        />
      </label>
      {picks.length > 0 && (
        <div className="treat-chips" role="group" aria-label={`Labels for ${item}`}>
          {picks.map((n) => (
            <button
              key={n}
              type="button"
              aria-pressed={value.trim().toLowerCase() === n.toLowerCase()}
              onClick={() => onChange(n)}
            >
              {n}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

/** A drafting that asks first: "Put 500 nylon bags on Things we have, one
 * row each?", with Just record them (and Yes when it can be done). */
export function TrackAskBox({
  ask,
  busy,
  onYes,
  onRecord,
}: {
  ask: TrackAsk;
  busy?: boolean;
  onYes: () => void;
  onRecord: () => void;
}) {
  return (
    <div className="notice treat-ask-box" role="alert" data-testid="track-ask">
      <p>{trackAskWords(ask)}</p>
      <div className="row-actions">
        {!ask.tooMany && (
          <button type="button" className="primary" disabled={busy} onClick={onYes}>
            Yes, one row each
          </button>
        )}
        <button type="button" disabled={busy} onClick={onRecord}>
          Just record them
        </button>
      </div>
    </div>
  );
}

/** A line's label and where it went, on an order or bill card (the
 * buyer's side only): "Machines · On Things we have". */
export function LineTreatMark({
  line,
  labels,
}: {
  line: Pick<PurchaseLine, "treatment" | "label_id"> &
    Partial<Pick<PurchaseLine, "id" | "received" | "label">>;
  labels: SpendLabel[] | undefined;
}) {
  const mark = lineMark(line.treatment);
  // The server names the label kept, so a label merged since shows as
  // the one it was merged into.
  const name = lineLabel(line, labels) || "Not labelled";
  const { can } = useAuth();
  // Kept track of and come: its rows on Things we have, for those who may
  // see them (table.read); words only for the rest.
  const to = can("table.read") ? thingsOfLine(line) : "";
  return (
    <span className="treat-mark" data-testid="line-mark">
      {name}
      {mark &&
        (to ? (
          <Link className="treat-went" to={to}>
            <Icon name={mark.icon} /> {mark.word}
          </Link>
        ) : (
          <span className="treat-went">
            <Icon name={mark.icon} /> {mark.word}
          </span>
        ))}
    </span>
  );
}

/** The business's labels, for marks on cards. */
export function useLabels(on = true) {
  return useLoad(
    () =>
      on
        ? get<LabelsView>("/v1/money/labels").then(
            (r) => r.labels,
            (): SpendLabel[] => [],
          )
        : Promise.resolve(undefined),
    [on],
  );
}

export type Choices = Record<string, Choice | undefined>;

/** A sheet's answers, by a key of its own for each line (the supplier's
 * item, or the line's place), with what drafting needs: whether every
 * line is answered, each line's body for the API, and a refusal that asks
 * first (track_ask, track_too_many) answered and tried again. */
export function useSheetTreat(on = true) {
  const data = useTreatData(on);
  const [choices, setChoices] = useState<Choices>({});
  const setters = useRef(new Map<string, (c: Choice | undefined) => void>());
  const setter = (k: string) => {
    let f = setters.current.get(k);
    if (!f) {
      f = (c) => setChoices((cur) => ({ ...cur, [k]: c }));
      setters.current.set(k, f);
    }
    return f;
  };
  const made = useRef<Made>(new Map());
  const [ask, setAsk] = useState<{ ask: TrackAsk; key: string } | null>(null);
  /** Every line answered: asked ones chosen and complete, remembered ones
   * as remembered or changed completely. */
  const ready = (lines: { key: string; memory?: Choice }[]) =>
    data.ready &&
    lines.every(({ key, memory }) => {
      const c = choices[key];
      return c ? !choiceProblem(c) : !!memory;
    });
  /** The line's treatment for the API; {} when it is as remembered. */
  const body = async (
    key: string,
    unit?: string,
    cs: Choices = choices,
  ): Promise<LineBody | Record<string, never>> => {
    const c = cs[key];
    return c ? resolveChoice(c, data, made.current, unit) : {};
  };
  /** A refusal that asks first, for the line at its place among keys:
   * true when it is one (shown with TrackAskBox). */
  const refused = (e: unknown, keys: string[]) => {
    const a = trackAskOf(e);
    if (!a) return false;
    setAsk({ ask: a, key: keys[a.line] ?? keys[0] ?? "" });
    return true;
  };
  /** Yes, one row each (track_many), or Just record them: the answers to
   * try again with. */
  const answer = (yes: boolean, memory?: Choice): Choices => {
    if (!ask) return choices;
    const base = choices[ask.key] ?? memory ?? { treatment: "track", label: "" };
    const next: Choices = {
      ...choices,
      [ask.key]: yes
        ? { ...base, treatment: "track", trackMany: true }
        : { ...base, treatment: "record", trackMany: false },
    };
    setChoices(next);
    setAsk(null);
    return next;
  };
  /** After drafting: a remembered item changed with Remember for next
   * time keeps the change (the line confirmed with remember). */
  const remember = async (
    purchaseId: string,
    lines: { key: string; position: number; unit?: string }[],
    cs: Choices = choices,
  ) => {
    for (const l of lines) {
      const c = cs[l.key];
      if (!c?.remember) continue;
      const b = await resolveChoice(c, data, made.current, l.unit);
      await patch(
        `/v1/stock/purchases/${encodeURIComponent(purchaseId)}/lines/${l.position}`,
        { ...b, remember: true },
      );
    }
  };
  return { data, choices, setter, ready, body, refused, ask, answer, setAsk, remember };
}

export type SheetTreat = ReturnType<typeof useSheetTreat>;

/** An order's lines as the question asks them, keyed by their place: a
 * line not yet confirmed asked from the order's own suggestion; the others
 * shown in a line, to change for this order; those received locked. */
export function OrderLinesTreat({
  purchase,
  treat,
  rememberable = true,
  only,
  onAsked,
}: {
  purchase: Pick<Purchase, "lines" | "supplier" | "status">;
  treat: SheetTreat;
  rememberable?: boolean;
  /** Only the lines not yet confirmed. */
  only?: "unconfirmed";
  /** A many-to-track question answered: try again with these answers. */
  onAsked: (cs: Choices) => void | Promise<void>;
}) {
  const shut = purchase.status === "cancelled";
  return (
    <>
      {purchase.lines
        .filter((l) => only !== "unconfirmed" || l.unconfirmed)
        .map((l) => {
          const k = String(l.position ?? purchase.lines.indexOf(l));
          const own = fromLine(l, treat.data.labels);
          return (
            <TreatQuestion
              key={k}
              item={l.item}
              unit={l.unit}
              supplier={purchase.supplier}
              data={treat.data}
              memory={l.unconfirmed ? undefined : own}
              suggested={l.unconfirmed ? own : undefined}
              value={treat.choices[k]}
              onChange={treat.setter(k)}
              locked={shut || lineLocked(l)}
              rememberable={rememberable}
            />
          );
        })}
      {treat.ask && (
        <TrackAskBox
          ask={treat.ask.ask}
          onYes={() => void onAsked(treat.answer(true, memoryAt(treat.ask?.key)))}
          onRecord={() => void onAsked(treat.answer(false, memoryAt(treat.ask?.key)))}
        />
      )}
    </>
  );
  function memoryAt(k: string | undefined): Choice | undefined {
    const l = purchase.lines.find((x, i) => String(x.position ?? i) === k);
    return l ? fromLine(l, treat.data.labels) : undefined;
  }
}

/** The lines' keys and what is asked of them, for useSheetTreat.ready:
 * a line not yet confirmed must be answered. */
export const orderLineKeys = (p: Pick<Purchase, "lines">) =>
  p.lines.map((l, i) => ({
    key: String(l.position ?? i),
    memory: l.unconfirmed ? undefined : ({ treatment: "record", label: "" } as Choice),
  }));

/** Each line's answer for confirm-proposed's lines: null as it is. */
export async function orderBodies(
  p: Pick<Purchase, "lines">,
  treat: SheetTreat,
  cs?: Choices,
): Promise<(LineBody | null)[]> {
  const out: (LineBody | null)[] = [];
  for (const [i, l] of p.lines.entries()) {
    const b = await treat.body(String(l.position ?? i), l.unit, cs);
    out.push("treatment" in b ? (b as LineBody) : null);
  }
  return out;
}

/** PATCH each line answered (a change, or one not yet confirmed), the
 * memory kept for a change with Remember for next time. */
export async function saveOrderLines(
  p: Pick<Purchase, "id" | "lines">,
  treat: SheetTreat,
  cs?: Choices,
): Promise<boolean> {
  const keys = p.lines.map((l, i) => String(l.position ?? i));
  for (const [i, l] of p.lines.entries()) {
    const k = keys[i] ?? String(i);
    const c = (cs ?? treat.choices)[k];
    if (!c) continue;
    const b = await treat.body(k, l.unit, cs);
    try {
      await patch(
        `/v1/stock/purchases/${encodeURIComponent(p.id)}/lines/${l.position ?? i}`,
        { ...b, ...(c.remember ? { remember: true } : {}) },
      );
    } catch (e) {
      const a = trackAskOf(e);
      if (a) {
        treat.setAsk({ ask: { ...a, line: l.position ?? i }, key: k });
        return false;
      }
      throw e;
    }
  }
  return true;
}

/** A bill's lines, each with its order line's label and where it went
 * (the buyer's side): the orders its lines name, read once. Undefined
 * when no line names an order. */
export function useBillMarks(
  lines: { seller_item_id?: string; purchase_id?: string }[] | undefined,
) {
  const ids = [
    ...new Set((lines ?? []).map((l) => l.purchase_id).filter((x): x is string => !!x)),
  ];
  const orders = useLoad(
    () =>
      Promise.all(
        ids.map((id) =>
          get<{ purchase: Purchase }>(
            `/v1/stock/purchases/${encodeURIComponent(id)}`,
          ).then(
            (r) => r.purchase,
            () => undefined,
          ),
        ),
      ).then((ps) => ps.filter((p): p is Purchase => !!p)),
    [ids.join()],
  );
  const labels = useLabels(ids.length > 0);
  if (ids.length === 0 || !orders.data) return undefined;
  return (l: { seller_item_id?: string; purchase_id?: string }) => (
    <LineTreatMark
      line={billLineOf(l, orders.data) ?? { treatment: undefined, label_id: undefined }}
      labels={labels.data}
    />
  );
}
