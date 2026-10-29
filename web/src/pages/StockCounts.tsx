import { useState } from "react";
import { get, postOnce, put } from "../api";
import { ErrorBox, Field, Items, Modal, Row, useAction, useLoad } from "../ui";
import {
  countDiff,
  countFromCSV,
  countProblem,
  countRequest,
  shrinkLine,
  stockLine,
  type CountSheet,
  type Shrink,
  type StockCount,
  type StockItem,
} from "../lib/stock";
import { limitInput, type StockSettings } from "../lib/purchases";
import { feeKobo } from "../lib/orders";
import { formatPrice } from "../lib/storefront";
import { whenLabel } from "../lib/bookings";

/** Counts and shrinkage on the Stock page (docs/stock.md "Counts and
 * shrinkage"): this month's in, sold and lost; Count the shelf; the latest
 * counts; and the loss a count may find before it is a leak. */
export function CountsCard({
  items,
  onChanged,
}: {
  items: StockItem[];
  onChanged: () => void;
}) {
  const shrink = useLoad(() => get<Shrink>("/v1/stock/shrinkage"), []);
  const counts = useLoad(
    () => get<{ counts: StockCount[] }>("/v1/stock/counts"),
    [],
  );
  const [counting, setCounting] = useState(false);
  const reload = () => {
    shrink.reload();
    counts.reload();
    onChanged();
  };
  const s = shrink.data;
  const worst = s?.items.find((i) => i.lost > 0);
  return (
    <section className="card" aria-labelledby="stock-counts">
      <div className="toolbar" style={{ marginBottom: 6 }}>
        <h2 id="stock-counts" className="grow" style={{ margin: 0 }}>
          Counts and losses
        </h2>
        <button
          type="button"
          className="small primary"
          disabled={items.length === 0}
          onClick={() => setCounting(true)}
        >
          Count the shelf
        </button>
      </div>
      <ErrorBox error={shrink.error ?? counts.error} />
      {s && (
        <p className="hint" data-testid="shrink">
          This month: {shrinkLine(s)}.
          {worst && (
            <>
              {" "}
              Lost most: {worst.item} ({worst.lost}
              {worst.lost_kobo ? `, ${formatPrice(worst.lost_kobo)}` : ""}).
            </>
          )}
        </p>
      )}
      {counts.data && counts.data.counts.length > 0 && (
        <Items label="Latest counts">
          {counts.data.counts.slice(0, 5).map((c) => (
            <Row
              key={c.id}
              letter="#"
              title={`${c.reference} · ${c.lines.length === 1 ? "1 item" : `${c.lines.length} items`}${c.full ? ", every item" : ""}`}
              side={
                c.lost_kobo > 0 ? (
                  <span className="badge warn">
                    {formatPrice(c.lost_kobo)} missing
                  </span>
                ) : (
                  <span className="badge muted">
                    {c.changed === 1 ? "1 change" : `${c.changed} changes`}
                  </span>
                )
              }
              sub={
                <span className="sub">
                  {whenLabel(c.counted_at)}
                  {c.reason ? ` · ${c.reason}` : ""}
                </span>
              }
              testid="count"
            />
          ))}
        </Items>
      )}
      <CountLimit />
      {counting && (
        <CountModal
          items={items}
          onClose={() => setCounting(false)}
          onSaved={() => {
            setCounting(false);
            reload();
          }}
        />
      )}
    </section>
  );
}

/** The loss a count may find, at cost, before it is a leak. */
function CountLimit() {
  const view = useLoad(
    () =>
      get<{ settings: StockSettings }>("/v1/stock/settings").catch(() => null),
    [],
  );
  const limit = view.data?.settings.count_limit_kobo;
  const initial =
    limit == null ? "" : limitInput({ purchase_limit_kobo: limit });
  const [v, setV] = useState<string | null>(null);
  const act = useAction();
  if (!view.data) return null;
  const value = v ?? initial;
  const kobo = value.trim() === "" ? null : feeKobo(value);
  const bad = kobo !== null && Number.isNaN(kobo);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void act.run(
          async () => (
            await put("/v1/stock/settings", { count_limit_kobo: kobo }),
            view.reload(),
            setV(null)
          ),
          "Saved",
        );
      }}
    >
      <Field
        label="A count missing more than this is a leak (₦)"
        hint="Valued at cost. Leave it empty for ₦5,000. Items with no cost price are always shown."
      >
        <input
          value={value}
          inputMode="decimal"
          placeholder="5000"
          onChange={(e) => setV(e.target.value)}
        />
      </Field>
      {bad && <p className="error-text">Enter an amount, like 5000.</p>}
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button
          type="submit"
          className="small"
          disabled={act.busy || bad || value === initial}
        >
          Save the limit
        </button>
      </div>
    </form>
  );
}

/** The count sheet: how many of each item are on the shelf, typed or read
 * from a CSV, with why they differ. */
function CountModal({
  items,
  onClose,
  onSaved,
}: {
  items: StockItem[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [sheet, setSheet] = useState<CountSheet>({});
  const [reason, setReason] = useState("");
  const [unknown, setUnknown] = useState<string[]>([]);
  const [key] = useState(() => crypto.randomUUID());
  const act = useAction();
  const problem = countProblem(sheet);
  const filled = Object.values(sheet).filter((v) => v.trim() !== "").length;
  return (
    <Modal title="Count the shelf" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(
            async () => (
              await postOnce(
                "/v1/stock/counts",
                key,
                countRequest(sheet, reason),
              ),
              onSaved()
            ),
            "Count recorded",
          );
        }}
      >
        <p className="hint">
          Enter what is on the shelf. Leave an item empty to skip it. Count
          every item to close the month&apos;s count.
        </p>
        <Field
          label="Or read a CSV"
          hint="Two columns: the item's name and how many."
        >
          <input
            type="file"
            accept=".csv,text/csv,text/plain"
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (!file) return;
              void file.text().then((text) => {
                const out = countFromCSV(text, items);
                setSheet((x) => ({ ...x, ...out.sheet }));
                setUnknown(out.unknown);
              });
            }}
          />
        </Field>
        {unknown.length > 0 && (
          <div className="notice" role="status">
            No item is called {unknown.join(", ")}: those rows were left out.
          </div>
        )}
        {items.map((it) => {
          const v = sheet[it.id] ?? "";
          const diff = countDiff(it, v);
          return (
            <Field
              key={it.id}
              label={it.name}
              hint={diff ?? `Taskiem has ${stockLine(it)}.`}
            >
              <input
                value={v}
                inputMode="numeric"
                onChange={(e) =>
                  setSheet({ ...sheet, [it.id]: e.target.value })
                }
              />
            </Field>
          );
        })}
        <Field label="Why they differ" hint="Spoilt, rats, a miscount.">
          <input
            value={reason}
            maxLength={200}
            onChange={(e) => setReason(e.target.value)}
          />
        </Field>
        {problem && filled > 0 && <p className="error-text">{problem}</p>}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button
            type="submit"
            className="primary"
            disabled={act.busy || !!problem}
          >
            Record the count
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
