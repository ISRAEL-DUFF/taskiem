import { get } from "../api";
import { naira } from "../lib/invoices";
import { periodWords, type Period } from "../lib/money";
import {
  profitWords,
  spendingLine,
  spendingName,
  spendingPath,
  type Spending,
} from "../lib/things";
import { ErrorBox, Items, Row, Skeleton, useLoad } from "../ui";

/** Spending by label for the period (GET /v1/money/spending; what a
 * business buys §5.4): Bought to sell or make with and Bought to keep apart
 * from running costs, each label with its share, and profit, which leaves
 * Bought to keep out. */
export function SpendingCard({ period }: { period: Period }) {
  const path = spendingPath(period);
  const sp = useLoad(() => get<Spending>(path), [path]);
  const s = sp.data;
  return (
    <section className="card" aria-labelledby="spending-h" data-testid="spending">
      <h2 id="spending-h">Spending by label</h2>
      <ErrorBox error={sp.error} />
      {!s && !sp.error && <Skeleton rows={2} label="Loading the spending…" />}
      {s && (
        <>
          <dl className="kv spending-sum">
            <dt>Running costs</dt>
            <dd data-testid="spending-running">{naira(s.running_costs_kobo)}</dd>
            <dt>Bought to sell or make with</dt>
            <dd data-testid="spending-sell">{naira(s.bought_to_sell_kobo)}</dd>
            <dt>Bought to keep</dt>
            <dd data-testid="spending-keep">
              {naira(s.bought_to_keep_kobo)}
              <span className="hint"> Something you have, not a cost: left out of profit.</span>
            </dd>
            <dt>All spending</dt>
            <dd>{naira(s.total_kobo)}</dd>
          </dl>
          {s.labels.length === 0 ? (
            <p className="hint">Nothing spent in {periodWords(period)}.</p>
          ) : (
            <Items label={`Spending by label, ${periodWords(period)}`}>
              {s.labels.map((l) => (
                <Row
                  key={l.label_id ?? ""}
                  letter={spendingName(l)}
                  title={spendingName(l)}
                  side={<span className="item-figure">{naira(l.kobo)}</span>}
                  sub={<span className="sub">{spendingLine(l)}</span>}
                  testid="spending-label"
                />
              ))}
            </Items>
          )}
          <p className="spending-profit" data-testid="spending-profit">
            <b>{profitWords(s.profit.profit_kobo)}</b>{" "}
            <span className="hint">
              Money in {naira(s.profit.in_kobo)}, less fees {naira(s.profit.fees_kobo)} and costs{" "}
              {naira(s.profit.costs_kobo)}. Bought to keep is left out.
            </span>
          </p>
        </>
      )}
    </section>
  );
}
