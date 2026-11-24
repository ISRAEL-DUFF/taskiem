import { useState } from "react";
import { Field } from "../ui";
import {
  PLACE_AREAS,
  columnsFor,
  feedingTools,
  titleCols,
  titleExpr,
  valueExpr,
  valueFrom,
  whenConds,
  whenExpr,
  type ChoiceTool,
  type PageActionDef,
  type PageChoices,
  type PageDoc,
  type PageFilterDef,
  type Part,
  type ValueFrom,
  type WhenCond,
} from "../lib/pages";
import type { Table } from "../lib/tables";

// The editor's G2 path in plain words (pages H5, g-pages-editor): buttons
// (a tool's form, telling a run something, a run's approval; when each
// shows), a part a tool works out, where a run is, a figure as an alert, a
// yes/no column ticked in place, each row's run, and where the page shows.
// No technical words: "a tool", "a run", "where it is", "only when".

type Col = { name: string; words: string };

/** Values with one set (or, given none, left out); none at all is none. */
function withValue(m: Record<string, unknown> | undefined, k: string, v: string | undefined): Record<string, unknown> | undefined {
  const out = Object.fromEntries(Object.entries(m ?? {}).filter(([x]) => x !== k));
  if (v !== undefined) out[k] = v;
  return Object.keys(out).length ? out : undefined;
}

/** Picks one of a few, with words. */
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

/** Where a button's value comes from: a column of the row, a filter at the
 * page's top, words written here, or asked of the person who presses it. */
function ValuePick({ label, value, cols, filters, personal, onChange }: { label: string; value: unknown; cols: Col[]; filters: PageFilterDef[]; personal?: boolean; onChange: (v: string | undefined) => void }) {
  // Kept here as picked: "These words" with none written yet is not "Asked".
  const [v, setV] = useState<ValueFrom>(() => valueFrom(value) ?? { from: "asked" });
  const set = (next: ValueFrom) => {
    setV(next);
    onChange(valueExpr(next));
  };
  const ways: Col[] = [
    { name: "asked", words: "Asked when pressed" },
    ...(cols.length ? [{ name: "column", words: "From the row" }] : []),
    ...(filters.length ? [{ name: "filter", words: "From a filter at the top" }] : []),
    // A personal value is never kept on a page as fixed words.
    ...(personal ? [] : [{ name: "words", words: "These words" }]),
  ];
  return (
    <div className="toolbar pg-value">
      <Pick
        label={label}
        value={v.from}
        choices={ways}
        onChange={(from) =>
          set(
            from === "column"
              ? { from: "column", column: cols[0]?.name ?? "" }
              : from === "filter"
                ? { from: "filter", id: filters[0]?.id ?? "" }
                : from === "words"
                  ? { from: "words", words: "" }
                  : { from: "asked" },
          )
        }
      />
      {v.from === "column" && <Pick label={`${label}: which column`} value={v.column} choices={cols} onChange={(column) => set({ from: "column", column })} />}
      {v.from === "filter" && (
        <Pick label={`${label}: which filter`} value={v.id} choices={filters.map((f) => ({ name: f.id, words: f.label || f.id }))} onChange={(id) => set({ from: "filter", id })} />
      )}
      {v.from === "words" && (
        <Field label={`${label}: the words`}>
          <input value={v.words} maxLength={200} onChange={(e) => set({ from: "words", words: e.target.value })} />
        </Field>
      )}
    </div>
  );
}

/** "Show it only when": up to three conditions on the row, every one held. */
function WhenForm({ when, cols, label, onChange }: { when: string | undefined; cols: Col[]; label: string; onChange: (w: string | undefined) => void }) {
  // Kept here as picked: "is" with no words yet is not "is empty".
  const [conds, setConds] = useState(() => whenConds(when));
  if (conds === null) return <p className="hint">{label} shows only when a condition written in its file holds.</p>;
  const set = (next: WhenCond[]) => {
    setConds(next);
    onChange(whenExpr(next.filter((c) => c.how === "is empty" || c.how === "is not empty" || (c.value ?? "") !== "")));
  };
  const hows: Col[] = [
    { name: "is", words: "is" },
    { name: "is not", words: "is not" },
    { name: "is empty", words: "is empty" },
    { name: "is not empty", words: "is not empty" },
  ];
  return (
    <fieldset className="sf-choices pg-conditions">
      <legend>Show {label} only when</legend>
      {conds.length === 0 && <p className="hint">Always.</p>}
      {conds.map((c, i) => (
        <div className="toolbar" key={i}>
          <Pick label={`When ${i + 1}: column`} value={c.column} choices={cols} onChange={(column) => set(conds.map((x, j) => (j === i ? { ...x, column } : x)))} />
          <Pick label={`When ${i + 1}: how`} value={c.how} choices={hows} onChange={(how) => set(conds.map((x, j) => (j === i ? { ...x, how: how as WhenCond["how"] } : x)))} />
          {(c.how === "is" || c.how === "is not") && (
            <Field label={`When ${i + 1}: words`}>
              <input value={c.value ?? ""} maxLength={100} onChange={(e) => set(conds.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))} />
            </Field>
          )}
          <button type="button" className="small" onClick={() => set(conds.filter((_, j) => j !== i))}>
            Remove<span className="sr-only"> when {i + 1}</span>
          </button>
        </div>
      ))}
      {conds.length < 3 && cols.length > 0 && (
        <button type="button" className="small" onClick={() => set([...conds, { column: cols[0]?.name ?? "", how: "is", value: "" }])}>
          Add a condition<span className="sr-only"> for {label}</span>
        </button>
      )}
    </fieldset>
  );
}

type Kind = "tool" | "signal" | "approve";
const kindOf = (a: PageActionDef): Kind => (a.signal ? "signal" : a.approve ? "approve" : "tool");

/** One button: what it says, what it does, and (on a row) when it shows. */
function ActionForm({ a, n, cols, filters, choices, onChange, onRemove }: { a: PageActionDef; n: number; cols: Col[]; filters: PageFilterDef[]; choices: PageChoices; onChange: (a: PageActionDef) => void; onRemove: () => void }) {
  const kind = kindOf(a);
  const name = a.label || `Button ${n}`;
  const telling = choices.workflows.filter((w) => w.signals.length > 0);
  const approving = choices.workflows.filter((w) => w.approvals.length > 0);
  const tool = choices.tools.find((t) => t.command === a.tool);
  // The signal and the approval as they stand (empty until picked).
  const sig = a.signal ?? { workflow: "", name: "" };
  const ap = a.approve ?? { workflow: "", run: "", step: "" };
  const kinds: Col[] = [
    { name: "tool", words: "Opens a tool's form, filled in" },
    ...(telling.length ? [{ name: "signal", words: "Tells a run something, after a Yes" }] : []),
    ...(approving.length ? [{ name: "approve", words: "Opens a run's approval" }] : []),
  ];
  const becomes = (k: string) => {
    const base = { label: a.label, when: a.when };
    if (k === "signal") {
      const w = telling[0];
      return onChange({ ...base, signal: { workflow: w?.name ?? "", name: w?.signals[0] ?? "", ...(cols.length ? { to: `=row.${cols[0]?.name ?? "id"}` } : { to: "" }) } });
    }
    if (k === "approve") {
      const w = approving[0];
      return onChange({ ...base, approve: { workflow: w?.name ?? "", step: w?.approvals[0]?.step ?? "", run: cols.length ? `=row.${cols[0]?.name ?? "id"}` : "" } });
    }
    return onChange({ ...base, tool: choices.tools[0]?.command ?? "" });
  };
  return (
    <fieldset className="sf-choices pg-action" data-testid="editor-button">
      <legend>{name}</legend>
      <Field label="It says">
        <input value={a.label} maxLength={30} onChange={(e) => onChange({ ...a, label: e.target.value })} />
      </Field>
      <Pick label="Pressing it" value={kind} choices={kinds} onChange={becomes} />
      {kind === "tool" && (
        <>
          <Pick
            label="Which tool"
            value={a.tool ?? ""}
            choices={choices.tools.map((t) => ({ name: t.command, words: t.name }))}
            onChange={(tool) => onChange({ ...a, tool, inputs: undefined })}
          />
          {(tool?.inputs ?? []).map((i) => (
            <ValuePick
              key={i.name}
              label={i.title}
              value={a.inputs?.[i.name]}
              cols={cols}
              filters={filters}
              personal={i.personal}
              onChange={(v) => {
                const inputs = withValue(a.inputs, i.name, v);
                onChange({ ...a, inputs });
              }}
            />
          ))}
          <label className="switch-line">
            <input type="checkbox" checked={!!a.once} onChange={(e) => onChange({ ...a, once: e.target.checked || undefined })} />{" "}
            <span>{cols.length ? "Once for each row: the first person to use it takes it" : "Once: the first person to use it takes it"}</span>
          </label>
        </>
      )}
      {kind === "signal" && a.signal && (
        <>
          <Pick
            label="Which workflow"
            value={a.signal.workflow}
            choices={telling.map((w) => ({ name: w.name, words: w.name }))}
            onChange={(workflow) => onChange({ ...a, signal: { ...sig, workflow, name: telling.find((w) => w.name === workflow)?.signals[0] ?? "" } })}
          />
          <Pick
            label="Tells it"
            value={a.signal.name}
            choices={(telling.find((w) => w.name === a.signal?.workflow)?.signals ?? []).map((s) => ({ name: s, words: s }))}
            onChange={(name) => onChange({ ...a, signal: { ...sig, name } })}
          />
          {cols.length > 0 ? (
            <>
              <Pick
                label="Which run"
                value={a.signal.run !== undefined ? "run" : "to"}
                choices={[
                  { name: "to", words: "The one waiting for this row" },
                  { name: "run", words: "The one whose number is in a column" },
                ]}
                onChange={(how) => {
                  const col = valueFrom(a.signal?.run ?? a.signal?.to);
                  const c = col?.from === "column" ? col.column : cols[0]?.name ?? "id";
                  const rest = { workflow: sig.workflow, name: sig.name, ...(sig.input ? { input: sig.input } : {}) };
                  onChange({ ...a, signal: how === "run" ? { ...rest, run: `=row.${c}` } : { ...rest, to: `=row.${c}` } });
                }}
              />
              <Pick
                label={a.signal.run !== undefined ? "The run's number is in" : "The row is known by"}
                value={(() => {
                  const v = valueFrom(a.signal?.run ?? a.signal?.to);
                  return v?.from === "column" ? v.column : "";
                })()}
                choices={cols}
                onChange={(c) => onChange({ ...a, signal: sig.run !== undefined ? { ...sig, run: `=row.${c}` } : { ...sig, to: `=row.${c}` } })}
              />
            </>
          ) : (
            <ValuePick label="What the run waits for" value={a.signal.to} cols={[]} filters={filters} onChange={(v) => onChange({ ...a, signal: { ...sig, run: undefined, to: v ?? "" } })} />
          )}
        </>
      )}
      {kind === "approve" && a.approve && (
        <>
          <Pick
            label="Which workflow"
            value={a.approve.workflow}
            choices={approving.map((w) => ({ name: w.name, words: w.name }))}
            onChange={(workflow) => onChange({ ...a, approve: { ...ap, workflow, step: approving.find((w) => w.name === workflow)?.approvals[0]?.step ?? "" } })}
          />
          <Pick
            label="At which approval"
            value={a.approve.step}
            choices={(approving.find((w) => w.name === a.approve?.workflow)?.approvals ?? []).map((s) => ({ name: s.step, words: s.name }))}
            onChange={(step) => onChange({ ...a, approve: { ...ap, step } })}
          />
          {cols.length > 0 ? (
            <Pick
              label="The run's number is in"
              value={(() => {
                const v = valueFrom(a.approve?.run);
                return v?.from === "column" ? v.column : "";
              })()}
              choices={cols}
              onChange={(c) => onChange({ ...a, approve: { ...ap, run: `=row.${c}` } })}
            />
          ) : (
            <ValuePick label="The run's number" value={a.approve.run} cols={[]} filters={filters} onChange={(v) => onChange({ ...a, approve: { ...ap, run: v ?? "" } })} />
          )}
        </>
      )}
      {cols.length > 0 && <WhenForm when={a.when} cols={cols} label={name} onChange={(when) => onChange({ ...a, when })} />}
      <button type="button" className="small danger" onClick={onRemove}>
        Remove<span className="sr-only"> {name}</span>
      </button>
    </fieldset>
  );
}

/** A part's buttons: the page's own (three), or each row's (two). */
export function ActionsForm({ actions, row, filters, choices, onChange }: { actions: PageActionDef[]; row: Table | undefined; filters: PageFilterDef[]; choices: PageChoices; onChange: (a: PageActionDef[]) => void }) {
  const cols = row ? columnsFor(row, "any").concat(row.columns.some((c) => c.name === "id") ? [] : [{ name: "id", words: "Its number" }]) : [];
  const max = row ? 2 : 3;
  return (
    <fieldset className="sf-choices pg-actions">
      <legend>{row ? "Buttons on each row (up to two)" : "The buttons (up to three)"}</legend>
      {actions.length === 0 && <p className="hint">None yet.</p>}
      {actions.map((a, i) => (
        <ActionForm
          key={i}
          a={a}
          n={i + 1}
          cols={cols}
          filters={filters}
          choices={choices}
          onChange={(x) => onChange(actions.map((y, j) => (j === i ? x : y)))}
          onRemove={() => onChange(actions.filter((_, j) => j !== i))}
        />
      ))}
      {actions.length < max && (
        <button type="button" className="small" onClick={() => onChange([...actions, { label: "", tool: choices.tools[0]?.command ?? "" }])}>
          Add a button
        </button>
      )}
    </fieldset>
  );
}

/** A part a tool works out: which tool, which of its answers, the values it is given. */
export function ToolSourceForm({ part, choices, filters, onChange }: { part: Part; choices: PageChoices; filters: PageFilterDef[]; onChange: (p: Part) => void }) {
  const kind = part.type === "list" ? "list" : "figure";
  const tools = feedingTools(choices, kind);
  const tool: ChoiceTool | undefined = choices.tools.find((t) => t.command === part.source?.tool);
  const outputs = (tool?.outputs ?? []).filter((o) => (kind === "list" ? o.type === "list" : o.type !== "list" && o.type !== "object"));
  const src = part.source ?? {};
  return (
    <>
      {tools.length === 0 && <p className="hint">None of your tools only looks things up yet: a tool that changes nothing can work out a part.</p>}
      <Pick
        label="Worked out by which tool"
        value={src.tool ?? ""}
        choices={tools.map((t) => ({ name: t.command, words: t.name }))}
        onChange={(command) => {
          const t = choices.tools.find((x) => x.command === command);
          const out = t?.outputs.find((o) => (kind === "list" ? o.type === "list" : o.type !== "list" && o.type !== "object"));
          const own = kind === "list" && t?.list && t.list.from === out?.name ? t.list : undefined;
          onChange({
            ...part,
            source: { tool: command, output: out?.name ?? "" },
            ...(kind === "list" ? { item: { title: own?.title || part.item?.title || "=item.name", ...(own?.sub ? { sub: own.sub } : {}) } } : { label: part.label || out?.title }),
          });
        }}
      />
      <Pick label="Shows its answer" value={src.output ?? ""} choices={outputs.map((o) => ({ name: o.name, words: o.title }))} onChange={(output) => onChange({ ...part, source: { ...src, output } })} />
      {(tool?.inputs ?? []).map((i) => (
        <ValuePick
          key={i.name}
          label={i.title}
          value={src.inputs?.[i.name]}
          cols={[]}
          filters={filters}
          personal={i.personal}
          onChange={(v) => onChange({ ...part, source: { ...src, inputs: withValue(src.inputs, i.name, v) } })}
        />
      ))}
      <p className="hint">It runs for each person who opens the page, kept for a minute; Refresh works it out again.</p>
    </>
  );
}

/** Where a run is: which workflow's. */
export function StatusForm({ part, choices, onChange }: { part: Part; choices: PageChoices; onChange: (p: Part) => void }) {
  return (
    <div className="pg-part-form">
      <Field label="Title">
        <input value={part.title ?? ""} maxLength={60} onChange={(e) => onChange({ ...part, title: e.target.value || undefined })} />
      </Field>
      <Pick label="Where a run of which workflow is" value={part.workflow ?? ""} choices={choices.workflows.map((w) => ({ name: w.name, words: w.name }))} onChange={(workflow) => onChange({ ...part, workflow })} />
      <p className="hint">It shows the latest run, or the one the page was opened for.</p>
    </div>
  );
}

/** Each row's own run: which workflow's, and how the row names it. */
export function RowStatusForm({ part, table, choices, onChange }: { part: Part; table: Table | undefined; choices: PageChoices; onChange: (p: Part) => void }) {
  const cols = columnsFor(table, "any").concat(table?.columns.some((c) => c.name === "id") ? [] : [{ name: "id", words: "Its number" }]);
  const s = part.status;
  const col = valueFrom(s?.run ?? s?.to);
  const column = col?.from === "column" ? col.column : "id";
  return (
    <fieldset className="sf-choices">
      <legend>Where each row's run is</legend>
      <Pick
        label="A run of"
        value={s?.workflow ?? ""}
        none="Not shown"
        choices={choices.workflows.map((w) => ({ name: w.name, words: w.name }))}
        onChange={(workflow) => onChange({ ...part, status: workflow ? { workflow, to: `=row.${column}` } : undefined })}
      />
      {s && (
        <>
          <Pick
            label="Which run"
            value={s.run !== undefined ? "run" : "to"}
            choices={[
              { name: "to", words: "The one waiting for this row, or that last did" },
              { name: "run", words: "The one whose number is in a column" },
            ]}
            onChange={(how) => onChange({ ...part, status: how === "run" ? { workflow: s.workflow, run: `=row.${column}` } : { workflow: s.workflow, to: `=row.${column}` } })}
          />
          <Pick
            label={s.run !== undefined ? "The run's number is in" : "The row is known by"}
            value={column}
            choices={cols}
            onChange={(c) => onChange({ ...part, status: s.run !== undefined ? { workflow: s.workflow, run: `=row.${c}` } : { workflow: s.workflow, to: `=row.${c}` } })}
          />
        </>
      )}
    </fieldset>
  );
}

/** A list's row title: one column, or two with a dot between. */
export function TitlePick({ part, cols, onChange }: { part: Part; cols: Col[]; onChange: (p: Part) => void }) {
  const item = part.item ?? { title: "" };
  const [a = "", b = ""] = titleCols(item.title);
  const set = (x: string, y: string) => {
    const title = titleExpr(x, y || undefined);
    onChange({ ...part, item: { ...item, title }, detail: part.detail ? { ...part.detail, title } : undefined });
  };
  return (
    <>
      <Pick label="Each row's title" value={a} choices={cols} onChange={(x) => set(x, b)} />
      <Pick label="Then, after a dot" value={b} choices={cols} none="Nothing" onChange={(y) => set(a, y)} />
    </>
  );
}

/** Where the page shows besides Pages: a tab under one of Taskiem's areas. */
export function PlaceForm({ doc, onChange }: { doc: PageDoc; onChange: (d: PageDoc) => void }) {
  return (
    <Pick
      label="Also a tab under"
      value={doc.place?.area ?? ""}
      none="Nowhere else: on Pages only"
      choices={PLACE_AREAS.map((p) => ({ name: p.area, words: p.words }))}
      onChange={(area) => onChange({ ...doc, place: area ? { area } : undefined })}
    />
  );
}
