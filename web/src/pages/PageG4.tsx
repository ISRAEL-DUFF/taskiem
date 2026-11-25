import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { get } from "../api";
import { Icon } from "../icons";
import { ErrorBox, useAction } from "../ui";
import {
  barPath,
  chartLayout,
  filtersParam,
  laneStep,
  moveWords,
  rowsWord,
  toneClass,
  workedWords,
  type DrawnAction,
  type DrawnLane,
  type DrawnPart,
  type DrawnRow,
} from "../lib/pages";

// A board and a chart on a page (pages G4, docs/pages.md). Both are drawn
// from what the server worked out for this person: a board's columns and
// cards, and a chart's points already placed on its scale. Nothing here
// adds up or reads a table.
//
// A card is moved to another column by dragging it, or by the keyboard:
// its Move button picks it up, the arrow keys choose a column, Enter puts
// it there and Escape leaves it, each step said to a screen reader. Either
// way the server reads the card again and opens the board's move tool,
// filled in, with its own care (POST /v1/pages/{name}/act with to).

type Press = (block: string, row: string | undefined, a: DrawnAction) => Promise<void>;
export type Move = (block: string, row: string, to: string) => Promise<void>;

/** A board: its columns side by side on a wide screen, one under another on a phone. */
export function Board({
  part,
  name,
  draft,
  values,
  onOpen,
  onPress,
  onMove,
}: {
  part: DrawnPart;
  name: string;
  draft: boolean;
  values: Record<string, string>;
  onOpen: (r: DrawnRow) => void;
  onPress?: Press;
  onMove?: Move;
}) {
  const [lanes, setLanes] = useState<DrawnLane[]>(part.lanes ?? []);
  const [dragging, setDragging] = useState<{ row: string; from: string } | null>(null);
  const [over, setOver] = useState<string | null>(null);
  // Moving by the keyboard: the card picked up and the column it would go to.
  const [held, setHeld] = useState<{ row: string; title: string; from: string; to: string } | null>(null);
  const [said, setSaid] = useState("");
  const act = useAction();
  const canMove = !!onMove && !!part.move && !part.move.disabled;
  const values_ = lanes.map((l) => l.value);
  const labelOf = (v: string) => lanes.find((l) => l.value === v)?.label ?? v;
  useEffect(() => setLanes(part.lanes ?? []), [part.lanes]);

  const drop = (row: string, from: string, to: string, title: string) => {
    setDragging(null);
    setOver(null);
    if (!onMove || from === to) return;
    setSaid(moveWords("dropped", title, labelOf(to)));
    void onMove(part.id, row, to);
  };
  const key = (e: KeyboardEvent<HTMLButtonElement>, r: DrawnRow, lane: string) => {
    const title = r.title || "This card";
    if (!held || held.row !== r.id) {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        setHeld({ row: r.id, title, from: lane, to: lane });
        setSaid(moveWords("picked", title, labelOf(lane)));
      }
      return;
    }
    switch (e.key) {
      case "ArrowLeft":
      case "ArrowRight":
      case "ArrowUp":
      case "ArrowDown": {
        e.preventDefault();
        const to = laneStep(values_, held.to, e.key === "ArrowLeft" || e.key === "ArrowUp" ? -1 : 1);
        setHeld({ ...held, to });
        setSaid(moveWords("over", title, labelOf(to), labelOf(held.from)));
        return;
      }
      case "Enter":
      case " ":
        e.preventDefault();
        setHeld(null);
        if (held.to === held.from) {
          setSaid(moveWords("left", title, labelOf(held.from)));
          return;
        }
        drop(r.id, held.from, held.to, title);
        return;
      case "Escape":
      case "Tab":
        if (e.key === "Escape") e.preventDefault();
        setHeld(null);
        setSaid(moveWords("left", title, labelOf(held.from)));
    }
  };
  const more = (lane: DrawnLane) =>
    void act.run(async () => {
      const q = [filtersParam(values), draft ? "draft=1" : "", `lane=${encodeURIComponent(lane.value)}`, `cursor=${encodeURIComponent(lane.more ?? "")}`].filter(Boolean).join("&");
      const next = await get<DrawnPart>(`/v1/pages/${name}/blocks/${part.id}?${q}`);
      const got = next.lanes?.find((l) => l.value === lane.value);
      if (!got) return;
      setLanes((ls) => ls.map((l) => (l.value === lane.value ? { ...l, rows: [...l.rows, ...got.rows], more: got.more } : l)));
    }, false);

  return (
    <section className={`card pg-part${part.size === "half" ? " half" : ""}`} data-testid="page-board">
      <div className="pg-part-head">
        {part.title && <h2>{part.title}</h2>}
        {!part.error && !part.working && <span className="hint">{rowsWord(part.count ?? 0)}</span>}
      </div>
      {part.error ? (
        <p className="hint">{part.error}</p>
      ) : part.working ? (
        <p className="hint" role="status">
          {part.working}
        </p>
      ) : (
        <>
          {canMove && (
            <p className="hint" id={`${part.id}-how`}>
              Drag a card to another column, or press its Move button and use the arrow keys. Its tool opens filled in, to say yes to.
            </p>
          )}
          {part.move?.disabled && <p className="hint">{part.move.disabled}</p>}
          <div className="pg-board">
            {lanes.map((lane) => {
              const target = (dragging && over === lane.value && dragging.from !== lane.value) || (held && held.to === lane.value && held.from !== lane.value);
              return (
                <section
                  key={lane.value}
                  className={`pg-lane${target ? " target" : ""}`}
                  aria-label={`${lane.label}, ${rowsWord(lane.count)}`}
                  data-testid="page-lane"
                  data-lane={lane.value}
                  onDragOver={(e) => {
                    if (!dragging) return;
                    e.preventDefault();
                    e.dataTransfer.dropEffect = "move";
                    if (over !== lane.value) setOver(lane.value);
                  }}
                  onDragLeave={() => over === lane.value && setOver(null)}
                  onDrop={(e) => {
                    e.preventDefault();
                    const d = dragging;
                    if (!d) return;
                    const r = lanes.flatMap((l) => l.rows).find((x) => x.id === d.row);
                    drop(d.row, d.from, lane.value, r?.title || "This card");
                  }}
                >
                  <h3>
                    {lane.label} <span className="hint">({lane.count})</span>
                  </h3>
                  {lane.rows.length === 0 ? (
                    <p className="hint pg-lane-empty">{part.empty}</p>
                  ) : (
                    <ul className="pg-cards" aria-label={lane.label}>
                      {lane.rows.map((r) => {
                        const picked = held?.row === r.id;
                        return (
                          <li
                            key={r.id}
                            className={`pg-card${picked ? " picked" : ""}${dragging?.row === r.id ? " dragging" : ""}`}
                            data-testid="page-card"
                            draggable={canMove}
                            onDragStart={(e) => {
                              e.dataTransfer.effectAllowed = "move";
                              e.dataTransfer.setData("text/plain", r.id);
                              setDragging({ row: r.id, from: lane.value });
                            }}
                            onDragEnd={() => (setDragging(null), setOver(null))}
                          >
                            <button type="button" className="pg-card-open" disabled={!r.detail} onClick={() => onOpen(r)}>
                              <span className="title">{r.title || "—"}</span>
                              {r.state && <span className={`badge sf-status ${toneClass(r.state.tone)}`}>{r.state.text}</span>}
                              {r.sub && <span className="sub">{r.sub}</span>}
                              {r.run_status && <span className={`badge sf-status ${toneClass(r.run_status.tone as never)}`}>{r.run_status.words}</span>}
                            </button>
                            {(canMove || ((r.actions ?? []).length > 0 && onPress)) && (
                              <span className="pg-card-actions">
                                {onPress &&
                                  (r.actions ?? []).map((a) => (
                                    <button
                                      key={a.index}
                                      type="button"
                                      className="small"
                                      disabled={!!a.disabled}
                                      title={a.disabled}
                                      data-testid="page-action"
                                      onClick={() => void onPress(part.id, r.id, a)}
                                    >
                                      {a.label}
                                    </button>
                                  ))}
                                {canMove && (
                                  <button
                                    type="button"
                                    className="small pg-card-move"
                                    aria-pressed={picked}
                                    aria-describedby={`${part.id}-how`}
                                    data-testid="page-card-move"
                                    onKeyDown={(e) => key(e, r, lane.value)}
                                    onClick={(e) => {
                                      // A press by pointer or by a screen reader's own key: Enter and Space
                                      // reach onKeyDown first, which prevents this.
                                      if (e.detail === 0) return;
                                      setHeld(held?.row === r.id ? null : { row: r.id, title: r.title || "This card", from: lane.value, to: lane.value });
                                      setSaid(moveWords(held?.row === r.id ? "left" : "picked", r.title || "This card", lane.label));
                                    }}
                                    onBlur={() => held?.row === r.id && setHeld(null)}
                                  >
                                    <Icon name="forward" />
                                    Move
                                    <span className="sr-only"> {r.title}</span>
                                  </button>
                                )}
                                {picked && held && (
                                  <span className="hint" aria-hidden="true">
                                    {held.to === held.from ? "Choose a column with the arrow keys" : `Going to ${labelOf(held.to)}: press Enter`}
                                  </span>
                                )}
                              </span>
                            )}
                          </li>
                        );
                      })}
                    </ul>
                  )}
                  {lane.more && (
                    <button type="button" className="small" disabled={act.busy} onClick={() => more(lane)}>
                      More<span className="sr-only"> in {lane.label}</span>
                    </button>
                  )}
                </section>
              );
            })}
          </div>
          <p className="sr-only" aria-live="assertive" data-testid="page-board-said">
            {said}
          </p>
        </>
      )}
      {part.worked_at && <p className="hint">{workedWords(part.worked_at)}</p>}
      <ErrorBox error={act.error} />
    </section>
  );
}

/** A chart: bars or a line drawn as plain SVG from the server's points,
 * a value on hover and on focus, and the figures as a table under it. */
export function Chart({ part }: { part: DrawnPart }) {
  const wrap = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(320);
  const [at, setAt] = useState<number | null>(null);
  useEffect(() => {
    const el = wrap.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver((es) => {
      const w = es[0]?.contentRect.width;
      if (w) setWidth(Math.round(w));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const points = part.points ?? [];
  const ticks = part.ticks ?? [];
  const left = 44;
  const right = 8;
  const top = 10;
  const plotH = 150;
  const bottom = 26;
  const plotW = Math.max(60, width - left - right);
  const marks = chartLayout(points, part.zero ?? 0, plotW, plotH);
  const empty = points.length === 0 || points.every((p) => p.value === 0);
  const y = (share: number) => top + plotH - share * plotH;
  const line = marks.map((m) => `${left + m.cx},${top + m.y}`).join(" ");
  const tip = at !== null ? points[at] : undefined;
  const tipMark = at !== null ? marks[at] : undefined;
  return (
    <section className={`card pg-part${part.size === "half" ? " half" : ""}`} data-testid="page-chart">
      <div className="pg-part-head">{part.title && <h2>{part.title}</h2>}</div>
      {part.error ? (
        <p className="hint">{part.error}</p>
      ) : part.working ? (
        <p className="hint" role="status">
          {part.working}
        </p>
      ) : (
        <>
          <div className="pg-chart" ref={wrap}>
            <svg
              className="pg-chart-svg"
              width={width}
              height={top + plotH + bottom}
              role="img"
              aria-label={`${part.title ? `${part.title}. ` : ""}${part.summary ?? ""}`}
              data-testid="page-chart-svg"
              onPointerLeave={() => setAt(null)}
            >
              {ticks.map((t) => (
                <g key={t.value} aria-hidden="true">
                  <line className="pg-chart-grid" x1={left} x2={left + plotW} y1={y(t.share)} y2={y(t.share)} />
                  <text className="pg-chart-tick" x={left - 6} y={y(t.share)} dy="0.32em" textAnchor="end">
                    {t.shown}
                  </text>
                </g>
              ))}
              <line className="pg-chart-base" x1={left} x2={left + plotW} y1={y(part.zero ?? 0)} y2={y(part.zero ?? 0)} aria-hidden="true" />
              {part.kind === "line" ? (
                <g aria-hidden="true">
                  <polyline className="pg-chart-line" points={line} />
                  {marks.map((m, i) => (
                    <circle key={i} className={`pg-chart-dot${at === i ? " on" : ""}`} cx={left + m.cx} cy={top + m.y} r={4} />
                  ))}
                </g>
              ) : (
                <g aria-hidden="true">
                  {marks.map((m, i) => (
                    <path key={i} className={`pg-chart-bar${at === i ? " on" : ""}`} d={barPath(left + m.x, top + m.top, m.w, m.h, m.up)} />
                  ))}
                </g>
              )}
              {marks.map((m, i) =>
                points[i]?.tick ? (
                  <text key={`l${i}`} className="pg-chart-label" x={left + m.cx} y={top + plotH + 16} textAnchor="middle" aria-hidden="true">
                    {points[i]?.label}
                  </text>
                ) : null,
              )}
              {/* The hit areas: a whole band each, bigger than its mark. */}
              {marks.map((m, i) => (
                <rect
                  key={`h${i}`}
                  className="pg-chart-hit"
                  x={left + m.cx - m.band / 2}
                  y={top}
                  width={m.band}
                  height={plotH}
                  onPointerEnter={() => setAt(i)}
                  onPointerMove={() => at !== i && setAt(i)}
                  aria-hidden="true"
                />
              ))}
            </svg>
            {tip && tipMark && (
              <div
                className="pg-chart-tip"
                role="presentation"
                style={{ left: Math.min(Math.max(left + tipMark.cx, 60), width - 60), top: Math.max(0, top + Math.min(tipMark.y, y(part.zero ?? 0)) - 8) }}
              >
                <strong>{tip.shown}</strong>
                <span>{tip.long}</span>
              </div>
            )}
          </div>
          {empty && <p className="hint">{part.empty}</p>}
          <p className="hint">{part.summary}</p>
          {part.over && <p className="hint">{part.over}</p>}
          <details className="pg-chart-table">
            <summary>The figures</summary>
            <table className="pg-chart-figures">
              <caption className="sr-only">{part.title}</caption>
              <thead>
                <tr>
                  <th scope="col">{part.label_head}</th>
                  <th scope="col">{part.value_head}</th>
                </tr>
              </thead>
              <tbody>
                {points.map((p, i) => (
                  <tr key={i}>
                    <th scope="row" data-label={part.label_head}>
                      {p.long}
                    </th>
                    <td data-label={part.value_head}>{p.shown}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </details>
          {part.worked_at && <p className="hint">{workedWords(part.worked_at)}</p>}
        </>
      )}
    </section>
  );
}

/** Whether a part is a board or a chart, drawn here. */
export const isG4 = (p: DrawnPart) => p.type === "board" || p.type === "chart";
