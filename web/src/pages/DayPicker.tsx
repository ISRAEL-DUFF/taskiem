import { useEffect, useRef, useState } from "react";
import { Icon } from "../icons";
import {
  addDays,
  addMonths,
  dayKey,
  dayLabel,
  monthGrid,
  monthLabel,
  sameDay,
  sameMonth,
  startOfDay,
  WEEKDAYS,
} from "../lib/calendar";

/**
 * A month calendar, as WhatsApp's: the month with arrows, the week's days,
 * round day buttons, today filled in, days after today dimmed and not to be
 * chosen. Arrow keys move by a day or a week, Page Up and Down by a month,
 * Enter chooses, Esc closes it (and only it).
 */
export function DayPicker({
  onPick,
  onClose,
}: {
  /** A day was chosen: "2026-11-04". */
  onPick: (day: string) => void;
  onClose: () => void;
}) {
  const today = startOfDay(new Date());
  const [focus, setFocus] = useState(today); // the day that is in the tab order
  const box = useRef<HTMLDivElement>(null);
  const moved = useRef(false); // the focus follows a key, not the first draw
  const weeks = monthGrid(focus.getFullYear(), focus.getMonth());
  useEffect(() => {
    if (moved.current)
      box.current?.querySelector<HTMLElement>('button[tabindex="0"]')?.focus();
    moved.current = true;
  }, [focus]);
  // The first draw puts the focus on the day, for the keyboard.
  useEffect(() => {
    box.current?.querySelector<HTMLElement>('button[tabindex="0"]')?.focus();
  }, []);
  const clamp = (d: Date) => (d.getTime() > today.getTime() ? today : d);
  const go = (to: Date) => setFocus(clamp(to));
  const month = monthLabel(focus);
  const canNext = !sameMonth(focus, today) && focus.getTime() < today.getTime();
  return (
    <div
      className="day-picker"
      ref={box}
      role="dialog"
      aria-label="Go to a day"
      data-testid="chat-calendar"
      onKeyDown={(e) => {
        const keys: Record<string, () => void> = {
          ArrowLeft: () => go(addDays(focus, -1)),
          ArrowRight: () => go(addDays(focus, 1)),
          ArrowUp: () => go(addDays(focus, -7)),
          ArrowDown: () => go(addDays(focus, 7)),
          Home: () => go(addDays(focus, -focus.getDay())),
          End: () => go(addDays(focus, 6 - focus.getDay())),
          PageUp: () => go(addMonths(focus, e.shiftKey ? -12 : -1)),
          PageDown: () => go(addMonths(focus, e.shiftKey ? 12 : 1)),
        };
        // Only from a day (the arrows of the month's buttons are theirs).
        if (!(e.target as HTMLElement).closest("tbody")) return;
        const fn = keys[e.key];
        if (fn) {
          e.preventDefault();
          fn();
        }
      }}
    >
      <div className="dp-head">
        <span className="dp-month" aria-live="polite">
          {month}
        </span>
        <button
          type="button"
          className="icon-button"
          aria-label="Previous month"
          onClick={() => go(addMonths(focus, -1))}
        >
          <Icon name="back" />
        </button>
        <button
          type="button"
          className="icon-button"
          aria-label="Next month"
          disabled={!canNext}
          onClick={() => go(addMonths(focus, 1))}
        >
          <Icon name="chevron" />
        </button>
      </div>
      <table className="dp-grid" role="grid" aria-label={month}>
        <thead>
          <tr>
            {WEEKDAYS.map((w) => (
              <th key={w} scope="col" abbr={w}>
                {w.slice(0, 3)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {weeks.map((w) => (
            <tr key={dayKey(w[0] ?? today)}>
              {w.map((d) => {
                const future = d.getTime() > today.getTime();
                const here = sameDay(d, today);
                return (
                  <td key={dayKey(d)}>
                    <button
                      type="button"
                      className={`dp-day${here ? " today" : ""}${sameMonth(d, focus) ? "" : " other"}`}
                      tabIndex={sameDay(d, focus) ? 0 : -1}
                      disabled={future}
                      aria-label={dayLabel(d)}
                      aria-current={here ? "date" : undefined}
                      onClick={() => onPick(dayKey(d))}
                    >
                      {d.getDate()}
                    </button>
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
      <button type="button" className="sr-only" onClick={onClose}>
        Close the calendar
      </button>
    </div>
  );
}
