// The canvas's choosers for owners (no-technical-steps N8): a schedule, a
// value from an earlier step, rules for "run only when", and durations.
// The cron and the expressions they make are under Advanced.
import {
  cronOf,
  DEFAULT_SCHEDULE,
  DURATIONS,
  OPS,
  rulesExpr,
  rulesOf,
  scheduleOf,
  scheduleWords,
  sourceOf,
  WEEKDAY_NAMES,
  type Every,
  type Op,
  type Rule,
  type Schedule,
  type Source,
} from "../lib/plaincanvas";

const EVERY: [Every, string][] = [
  ["day", "Every day"],
  ["weekdays", "Every weekday (Monday to Friday)"],
  ["week", "Every week"],
  ["month", "Every month"],
  ["hour", "Every hour"],
];

/** When a scheduled workflow runs, chosen in words. Null onChange never happens: the cron is always one it says. */
export function SchedulePicker({ cron, onChange }: { cron: string; onChange: (cron: string) => void }) {
  const s = scheduleOf(cron);
  if (!s) {
    return <p className="hint">This schedule was set up by hand. Turn on Advanced in Settings to change it, or start again: <button type="button" className="link" onClick={() => onChange(cronOf(DEFAULT_SCHEDULE))}>choose a schedule</button>.</p>;
  }
  const set = (patch: Partial<Schedule>) => onChange(cronOf({ ...s, ...patch }));
  return (
    <div>
      <span className="toolbar">
        <select aria-label="How often" value={s.every} onChange={(e) => set({ every: e.target.value as Every })}>
          {EVERY.map(([v, l]) => (
            <option key={v} value={v}>
              {l}
            </option>
          ))}
        </select>
        {s.every === "week" && (
          <select aria-label="On" value={s.weekday} onChange={(e) => set({ weekday: Number(e.target.value) })}>
            {WEEKDAY_NAMES.map((d, i) => (
              <option key={d} value={i}>
                on {d}
              </option>
            ))}
          </select>
        )}
        {s.every === "month" && (
          <select aria-label="On day" value={s.day} onChange={(e) => set({ day: Number(e.target.value) })}>
            {Array.from({ length: 28 }, (_, i) => i + 1).map((d) => (
              <option key={d} value={d}>
                on day {d}
              </option>
            ))}
          </select>
        )}
        {s.every === "hour" ? (
          <select aria-label="Minutes past" value={s.minute} onChange={(e) => set({ minute: Number(e.target.value) })}>
            {[0, 15, 30, 45].map((m) => (
              <option key={m} value={m}>
                {m === 0 ? "on the hour" : `at ${m} minutes past`}
              </option>
            ))}
          </select>
        ) : (
          <input aria-label="At" type="time" value={s.time} onChange={(e) => e.target.value && set({ time: e.target.value })} />
        )}
      </span>
      <p className="hint">{scheduleWords(s)}, Lagos time.</p>
    </div>
  );
}

/** A value from what came before, chosen by its words. */
export function ValuePicker({ sources, value, onChange, label }: { sources: Source[]; value: string; onChange: (expr: string) => void; label: string }) {
  return (
    <select aria-label={label} value={sourceOf(sources, value)?.expr ?? ""} onChange={(e) => onChange(e.target.value)}>
      <option value="">Choose…</option>
      {sources.map((s) => (
        <option key={s.expr} value={s.expr}>
          {s.label}
        </option>
      ))}
    </select>
  );
}

/** "Run only when", as rules on values from earlier. */
export function RulesBuilder({ sources, value, onChange }: { sources: Source[]; value: string; onChange: (expr: string) => void }) {
  const r = rulesOf(value);
  if (!r) {
    return <p className="hint">This step has a rule set up by hand. Turn on Advanced in Settings to change it.</p>;
  }
  const set = (rules: Rule[], all = r.all) => onChange(rulesExpr({ rules, all }));
  if (sources.length === 0) {
    return <p className="hint">Always. Rules use values from earlier steps; make this step run after one first.</p>;
  }
  return (
    <div>
      {r.rules.length === 0 && <p className="hint">Always.</p>}
      {r.rules.map((rule, i) => {
        const change = (patch: Partial<Rule>) => set(r.rules.map((x, j) => (j === i ? { ...x, ...patch } : x)));
        return (
          <span key={i} className="toolbar" style={{ marginBottom: 4 }}>
            {i > 0 && <span className="hint">{r.all ? "and" : "or"}</span>}
            <ValuePicker label={`Rule ${i + 1}: the value`} sources={sources} value={rule.left} onChange={(left) => change({ left })} />
            <select aria-label={`Rule ${i + 1}: how`} value={rule.op} onChange={(e) => change({ op: e.target.value as Op })}>
              {OPS.map((o) => (
                <option key={o} value={o}>
                  {o}
                </option>
              ))}
            </select>
            <input aria-label={`Rule ${i + 1}: what`} value={rule.right} onChange={(e) => change({ right: e.target.value })} />
            <button type="button" className="link" onClick={() => set(r.rules.filter((_, j) => j !== i))}>
              Remove
            </button>
          </span>
        );
      })}
      <span className="toolbar">
        <button type="button" className="small" onClick={() => set([...r.rules, { left: sources[0]?.expr ?? "", op: "is", right: "" }])}>
          Add a rule
        </button>
        {r.rules.length > 1 && (
          <select aria-label="Which rules" value={r.all ? "all" : "any"} onChange={(e) => set(r.rules, e.target.value === "all")}>
            <option value="all">All of these</option>
            <option value="any">Any one of these</option>
          </select>
        )}
      </span>
    </div>
  );
}

/** A wait or a time limit, picked from a list. */
export function DurationPicker({ value, onChange, label, allowNone = false }: { value: string; onChange: (d: string) => void; label: string; allowNone?: boolean }) {
  const listed = DURATIONS.some(([v]) => v === value);
  return (
    <select aria-label={label} value={value} onChange={(e) => onChange(e.target.value)}>
      {allowNone && <option value="">No limit</option>}
      {!listed && value && <option value={value}>{value}</option>}
      {DURATIONS.map(([v, l]) => (
        <option key={v} value={v}>
          {l}
        </option>
      ))}
    </select>
  );
}
