import { useState, type ReactNode } from "react";
import { Field } from "../ui";
import {
  columnOf,
  columnsFor,
  isRecords,
  moveExpr,
  moveFrom,
  moveInputs,
  showColumn,
  type MoveFrom,
  type PageChoices,
  type PageFilterDef,
  type Part,
} from "../lib/pages";
import type { Table } from "../lib/tables";
import { TitlePick, ToolSourceForm } from "./PageEditorParts";

// The editor's G4 path in plain words: a board (its columns by a choice,
// each column's value and heading, and the tool moving a card opens, each
// of its values from the card, the column it goes to, words or asked) and
// a chart (bars or a line; how many or a column added up; each day, week
// or month, or each of a column's values; or a tool's rows). No technical
// words: "a board", "a column", "moving a card", "a chart".

type Col = { name: string; words: string };

function Pick({ label, value, choices, onChange, none }: { label: string; value: string; choices: Col[]; onChange: (v: string) => void; none?: string }) {
  return (
    <Field label={label}>
      <select value={value} onChange={(e) => onChange(e.target.value)}>
        {none !== undefined && <option value="">{none}</option>}
        {choices.map((c) => (
          <option key={c.name} value={c.name}>
            {c.words}
          </option>
        ))}
      </select>
    </Field>
  );
}

/** The business's own tables: a board and a chart read no records of Taskiem's. */
function OwnTablePick({ part, tables, onChange }: { part: Part; tables: Table[]; onChange: (p: Part) => void }) {
  const own = tables.filter((t) => !isRecords(t.name));
  return (
    <Pick
      label="From which table?"
      value={part.source?.table ?? ""}
      choices={own.map((t) => ({ name: t.name, words: t.title || t.name }))}
      onChange={(table) => onChange({ ...part, source: { table } })}
    />
  );
}

/** One of the move tool's values: from the card, the column it goes to, words, or asked. */
function MoveValue({ label, value, cols, personal, onChange }: { label: string; value: unknown; cols: Col[]; personal: boolean; onChange: (v: string | undefined) => void }) {
  const [v, setV] = useState<MoveFrom>(() => moveFrom(value) ?? { from: "asked" });
  const set = (next: MoveFrom) => {
    setV(next);
    onChange(moveExpr(next));
  };
  const ways: Col[] = [
    { name: "to", words: "The column it goes to" },
    { name: "column", words: "From the card" },
    ...(personal ? [] : [{ name: "words", words: "These words" }]),
    { name: "asked", words: "Asked when moved" },
  ];
  return (
    <div className="toolbar pg-value">
      <Pick
        label={label}
        value={v.from}
        choices={ways}
        onChange={(from) =>
          set(from === "to" ? { from: "to" } : from === "column" ? { from: "column", column: cols[0]?.name ?? "id" } : from === "words" ? { from: "words", words: "" } : { from: "asked" })
        }
      />
      {v.from === "column" && <Pick label={`${label}: which column`} value={v.column} choices={cols} onChange={(column) => set({ from: "column", column })} />}
      {v.from === "words" && (
        <Field label={`${label}: the words`}>
          <input value={v.words} maxLength={200} onChange={(e) => set({ from: "words", words: e.target.value })} />
        </Field>
      )}
    </div>
  );
}

/** A board's choices. */
export function BoardForm({ part, tables, choices, conditions, onChange }: { part: Part; tables: Table[]; choices: PageChoices; conditions: ReactNode; onChange: (p: Part) => void }) {
  const t = tables.find((x) => x.name === part.source?.table);
  const any = [{ name: "id", words: "Number" }, ...columnsFor(t, "any")];
  const board = part.board ?? { column: "", columns: [] };
  const setBoard = (b: Partial<NonNullable<Part["board"]>>) => onChange({ ...part, board: { ...board, ...b } });
  const move = board.move;
  const tool = choices.tools.find((x) => x.command === move?.tool);
  const detailCols = (part.detail?.lines ?? []).map((l) => columnOf(l.value)).filter(Boolean);
  return (
    <div className="pg-part-form">
      <Field label="Title">
        <input value={part.title ?? ""} maxLength={60} onChange={(e) => onChange({ ...part, title: e.target.value || undefined })} />
      </Field>
      <OwnTablePick part={part} tables={tables} onChange={onChange} />
      <TitlePick part={part} cols={columnsFor(t, "any")} onChange={onChange} />
      <Pick
        label="The line under it"
        value={columnOf(part.item?.sub)}
        choices={columnsFor(t, "any")}
        none="Nothing"
        onChange={(c) => onChange({ ...part, item: { ...(part.item ?? { title: "" }), sub: c ? showColumn(c) : undefined } })}
      />
      <Pick label="In columns by" value={board.column} choices={columnsFor(t, "group")} onChange={(column) => setBoard({ column })} />
      <fieldset className="sf-choices pg-conditions">
        <legend>Its columns, left to right (two to six)</legend>
        {board.columns.map((c, i) => (
          <div className="toolbar" key={i}>
            <Field label={`Column ${i + 1}: the value its cards hold`}>
              <input value={c.value} maxLength={100} onChange={(e) => setBoard({ columns: board.columns.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)) })} />
            </Field>
            <Field label={`Column ${i + 1}: its heading`}>
              <input
                value={c.label ?? ""}
                maxLength={30}
                onChange={(e) => setBoard({ columns: board.columns.map((x, j) => (j === i ? { ...x, label: e.target.value || undefined } : x)) })}
              />
            </Field>
            <button type="button" className="small" disabled={board.columns.length <= 2} onClick={() => setBoard({ columns: board.columns.filter((_, j) => j !== i) })}>
              Remove<span className="sr-only"> column {i + 1}</span>
            </button>
          </div>
        ))}
        {board.columns.length < 6 && (
          <button type="button" className="small" onClick={() => setBoard({ columns: [...board.columns, { value: "" }] })}>
            Add a column
          </button>
        )}
      </fieldset>
      <fieldset className="sf-choices pg-action">
        <legend>Moving a card to another column</legend>
        <Pick
          label="Opens which tool"
          value={move?.tool ?? ""}
          none="Nothing: the cards stay where they are"
          choices={choices.tools.map((x) => ({ name: x.command, words: x.name }))}
          onChange={(command) => {
            const picked = choices.tools.find((x) => x.command === command);
            setBoard({ move: command ? { label: move?.label || "Move", tool: command, inputs: moveInputs(picked, board.column) } : undefined });
          }}
        />
        {move && (
          <>
            <Field label="Its button says">
              <input value={move.label} maxLength={30} onChange={(e) => setBoard({ move: { ...move, label: e.target.value } })} />
            </Field>
            {(tool?.inputs ?? []).map((i) => (
              <MoveValue
                key={`${move.tool}-${i.name}`}
                label={i.title}
                value={move.inputs?.[i.name]}
                cols={any}
                personal={i.personal}
                onChange={(v) => {
                  const inputs = Object.fromEntries(Object.entries(move.inputs ?? {}).filter(([k]) => k !== i.name));
                  if (v !== undefined) inputs[i.name] = v;
                  setBoard({ move: { ...move, inputs: Object.keys(inputs).length ? inputs : undefined } });
                }}
              />
            ))}
            <p className="hint">The tool's form opens filled in, with its own care, for whoever moves a card; those its roles leave out cannot move cards.</p>
          </>
        )}
      </fieldset>
      <fieldset className="sf-choices">
        <legend>Opening a card shows</legend>
        {columnsFor(t, "any").map((c) => (
          <label key={c.name} className="row">
            <input
              type="checkbox"
              checked={detailCols.includes(c.name)}
              disabled={!detailCols.includes(c.name) && detailCols.length >= 12}
              onChange={(e) => {
                const lines = (part.detail?.lines ?? []).filter((l) => columnOf(l.value) !== c.name);
                if (e.target.checked) lines.push({ label: c.words, value: showColumn(c.name) });
                onChange({ ...part, detail: lines.length ? { title: part.item?.title || showColumn(c.name), lines } : undefined });
              }}
            />
            <span>{c.words}</span>
          </label>
        ))}
      </fieldset>
      <Field label="When a column has none, say">
        <input value={part.empty ?? ""} maxLength={120} placeholder="Nothing here" onChange={(e) => onChange({ ...part, empty: e.target.value || undefined })} />
      </Field>
      {conditions}
    </div>
  );
}

const PERIODS: Col[] = [
  { name: "day", words: "day" },
  { name: "week", words: "week" },
  { name: "month", words: "month" },
];

/** A chart's choices: over a table, or a tool's rows. */
export function ChartForm({ part, tables, choices, filters, conditions, onChange }: { part: Part; tables: Table[]; choices: PageChoices; filters: PageFilterDef[]; conditions: ReactNode; onChange: (p: Part) => void }) {
  const chart = part.chart ?? { kind: "bars" as const };
  const setChart = (c: Partial<NonNullable<Part["chart"]>>) => onChange({ ...part, chart: { ...chart, ...c } });
  const kind = (
    <Pick
      label="Drawn as"
      value={chart.kind}
      choices={[
        { name: "bars", words: "Bars" },
        ...(part.source?.tool || chart.period ? [{ name: "line", words: "A line" }] : []),
      ]}
      onChange={(k) => setChart({ kind: k as "bars" | "line" })}
    />
  );
  const title = (
    <Field label="Title">
      <input value={part.title ?? ""} maxLength={60} onChange={(e) => onChange({ ...part, title: e.target.value || undefined })} />
    </Field>
  );
  const money = (
    <label className="switch-line">
      <input type="checkbox" checked={!!part.naira} onChange={(e) => onChange({ ...part, naira: e.target.checked || undefined })} /> <span>It is money (shown in naira)</span>
    </label>
  );
  if (part.source?.tool !== undefined) {
    const field = (v: string | undefined) => /^=item\.([a-zA-Z_][a-zA-Z0-9_]*)$/.exec(v ?? "")?.[1];
    return (
      <div className="pg-part-form">
        {title}
        {kind}
        <ToolSourceForm part={part} choices={choices} filters={filters} onChange={onChange} />
        <Field label="Each bar's words are the answer's" hint="The name it has in each of the tool's rows, like week">
          <input value={field(chart.label) ?? ""} maxLength={40} onChange={(e) => setChart({ label: `=item.${e.target.value.replace(/[^a-zA-Z0-9_]/g, "")}` })} />
        </Field>
        <Field label="Each bar's number is the answer's" hint="Like total">
          <input value={field(chart.value) ?? ""} maxLength={40} onChange={(e) => setChart({ value: `=item.${e.target.value.replace(/[^a-zA-Z0-9_]/g, "")}` })} />
        </Field>
        {money}
      </div>
    );
  }
  const t = tables.find((x) => x.name === part.source?.table);
  const dates = columnsFor(t, "date");
  const groups = columnsFor(t, "group");
  const over = chart.period ? `${chart.period}:${chart.by ?? ""}` : `by:${chart.by ?? ""}`;
  return (
    <div className="pg-part-form">
      {title}
      <OwnTablePick part={part} tables={tables} onChange={onChange} />
      <Pick
        label="Shows"
        value={chart.of ?? ""}
        none="How many rows"
        choices={columnsFor(t, "sum").map((c) => ({ name: c.name, words: `${c.words} added up` }))}
        onChange={(of) => setChart({ of: of || undefined })}
      />
      {chart.of && money}
      <Pick
        label="For each"
        value={over}
        choices={[
          ...dates.flatMap((c) => PERIODS.map((p) => ({ name: `${p.name}:${c.name}`, words: `${p.words} by ${c.words.toLowerCase()}` }))),
          ...groups.map((c) => ({ name: `by:${c.name}`, words: `${c.words.toLowerCase()}: a bar for each` })),
        ]}
        onChange={(v) => {
          const [how = "", by = ""] = v.split(":");
          if (how === "by") setChart({ by, period: undefined, last: undefined, kind: "bars" });
          else setChart({ by, period: how as "day" | "week" | "month", last: chart.last ?? 8 });
        }}
      />
      {chart.period && (
        <Field label={`How many ${chart.period}s, up to now`}>
          <input type="number" min={2} max={12} value={chart.last ?? 8} onChange={(e) => setChart({ last: Math.min(12, Math.max(2, Number(e.target.value) || 8)) })} />
        </Field>
      )}
      {kind}
      {conditions}
    </div>
  );
}
