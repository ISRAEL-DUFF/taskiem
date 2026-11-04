// The money pieces Home and Money > Overview share: the day's money card,
// the period's bars of money in and out, and where the money came from.
// Colours: the card is the brand's pine with lime on it (the brand
// guide); the bars' in and out are --chart-in and --chart-out, a pair
// checked for colour-blind readers, and every figure is also in words.
import { useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { naira, channelWords } from "../lib/invoices";
import type { ChannelSummary } from "../lib/money";
import { bucketLong, bucketShort, dayLine, hourWords, linePath, openBucket, type MoneyBucket } from "../lib/moneyChart";
import { useCountUp } from "../ui";

/** Home's card: money in today, the day's line, money out and what waits to be matched. */
export function MoneyCard({ inKobo, payments, outKobo, buckets, toMatch, label = "Money in today", to = "/money" }: {
  inKobo: number;
  payments: number;
  outKobo?: number;
  buckets?: MoneyBucket[];
  toMatch?: number;
  label?: string;
  to?: string;
}) {
  const shown = useCountUp(inKobo);
  const line = buckets ? dayLine(buckets) : null;
  const path = line ? linePath(line.points, 300, 56) : null;
  return (
    <section className="money-card" aria-labelledby="money-card-h">
      <div className="money-card-top">
        <h2 id="money-card-h">{label}</h2>
        <span className="live">Live</span>
      </div>
      <Link className="money-card-amount" to={to}>
        {naira(shown)}
      </Link>
      <p className="money-card-sub">{payments === 1 ? "1 payment" : `${payments} payments`}</p>
      {line && path && (
        <>
          <svg className="money-line" role="img" aria-label={`Money in through the day, ${naira(line.points[line.points.length - 1] ?? 0)} by ${hourWords(line.end + 1)}`} viewBox="0 0 300 56" preserveAspectRatio="none">
            <path d={path.area} className="area" />
            <polyline points={path.line} className="stroke" vectorEffect="non-scaling-stroke" />
          </svg>
          <div className="money-line-ends" aria-hidden="true">
            <span>{hourWords(line.start)}</span>
            <span>Now</span>
          </div>
        </>
      )}
      {(outKobo !== undefined || toMatch !== undefined) && (
        <dl className="money-card-foot">
          {outKobo !== undefined && (
            <div>
              <dt>Money out</dt>
              <dd>{naira(outKobo)}</dd>
            </div>
          )}
          {toMatch !== undefined && (
            <div>
              <dt>To match</dt>
              <dd>
                <Link to="/invoices/match" className={toMatch > 0 ? "lime" : undefined}>
                  {toMatch === 1 ? "1 payment" : `${toMatch} payments`}
                </Link>
              </dd>
            </div>
          )}
        </dl>
      )}
    </section>
  );
}

/** A period's money in and out as bars, one pair per day (or hour); pressing a bar shows its figures. Up to 14 bars can be pressed; more are drawn, with the figures in the table under them. */
export function MoneyBars({ buckets, unit, caption }: { buckets: MoneyBucket[]; unit: string; caption: string }) {
  const [picked, setPicked] = useState(() => openBucket(buckets));
  if (buckets.length === 0) return null;
  const max = Math.max(1, ...buckets.map((b) => Math.max(b.in_settled_kobo, b.out_settled_kobo)));
  const pressable = buckets.length <= 14;
  const at = buckets[picked] ?? buckets[buckets.length - 1];
  const h = (k: number) => `${Math.max(k > 0 ? 3 : 0, Math.round((112 * k) / max))}px`;
  const bars = (b: MoneyBucket): ReactNode => (
    <>
      <span className="bar in" style={{ height: h(b.in_settled_kobo) }} />
      <span className="bar out" style={{ height: h(b.out_settled_kobo) }} />
    </>
  );
  return (
    <figure className="money-bars">
      {pressable && at && (
        <p className="money-bars-picked" aria-live="polite">
          <strong>{bucketLong(at, unit)}</strong>
          <span>
            In {naira(at.in_settled_kobo)} · Out {naira(at.out_settled_kobo)}
          </span>
        </p>
      )}
      <div className={`money-bars-plot${pressable ? "" : " many"}`} style={{ gridTemplateColumns: `repeat(${buckets.length}, minmax(0, 1fr))` }} aria-hidden={pressable ? undefined : true}>
        {buckets.map((b, i) =>
          pressable ? (
            <button key={b.from} type="button" className="money-bar" aria-pressed={i === picked} aria-label={`${bucketLong(b, unit)}: in ${naira(b.in_settled_kobo)}, out ${naira(b.out_settled_kobo)}`} onClick={() => setPicked(i)}>
              {bars(b)}
            </button>
          ) : (
            <span key={b.from} className="money-bar">
              {bars(b)}
            </span>
          ),
        )}
      </div>
      <div className="money-bars-days" aria-hidden="true" style={{ gridTemplateColumns: `repeat(${buckets.length}, minmax(0, 1fr))` }}>
        {buckets.map((b, i) => (
          <span key={b.from} className={i === picked && pressable ? "on" : undefined}>
            {buckets.length > 14 && i % Math.ceil(buckets.length / 7) !== 0 ? "" : bucketShort(b, unit)}
          </span>
        ))}
      </div>
      <details className="money-bars-table">
        <summary>See the figures</summary>
        <table>
          <caption className="sr-only">{caption}</caption>
          <thead>
            <tr>
              <th scope="col">{unit === "hour" ? "Hour" : "Day"}</th>
              <th scope="col">In</th>
              <th scope="col">Out</th>
              <th scope="col">Payments</th>
            </tr>
          </thead>
          <tbody>
            {buckets.map((b) => (
              <tr key={b.from}>
                <th scope="row">{bucketLong(b, unit)}</th>
                <td className="num">{naira(b.in_settled_kobo)}</td>
                <td className="num">{naira(b.out_settled_kobo)}</td>
                <td className="num">{b.payments}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </figure>
  );
}

/** Where a period's money in came from: each account's share of it. */
export function MoneySources({ channels, total }: { channels: ChannelSummary[]; total: number }) {
  const from = channels.filter((c) => c.in_settled_kobo > 0);
  if (from.length === 0 || total <= 0) return null;
  return (
    <section className="money-panel" aria-labelledby="sources-h">
      <h2 id="sources-h">Where it came from</h2>
      <ul className="sources">
        {from.map((c) => {
          const share = Math.round((100 * c.in_settled_kobo) / total);
          return (
            <li key={c.channel}>
              <div className="source-head">
                <span>{channelWords(c.channel)}</span>
                <span className="num">{naira(c.in_settled_kobo)}</span>
              </div>
              <div className="share" role="img" aria-label={`${share}% of the money in`}>
                <span style={{ width: `${Math.max(2, share)}%` }} />
              </div>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
