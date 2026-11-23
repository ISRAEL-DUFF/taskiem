import { useId, useState } from "react";
import type { Answer, Output, Tool, WorkflowDefinition } from "@sdk/wd";
import type { Problem } from "../api";
import { ValuePicker } from "../canvas/PlainPickers";
import type { Source } from "../lib/plaincanvas";
import { careWords } from "../lib/toolanswer";
import {
  amountOutputs,
  commandProblem,
  firstAnswer,
  itemField,
  itemValue,
  listOutputs,
  newTool,
  outputName,
  pickOf,
  pickValue,
  scalarOutputs,
  sentenceParts,
  sentenceText,
  suggestCommand,
  toolProblem,
  type Part,
  type Pick,
} from "../lib/toolbuild";
import { Field } from "../ui";

// "Use it from chat" (made tools §13), in the builder's Settings beside
// Who can start it: a workflow started by hand becomes one of the
// business's tools. Its name, the word that calls it (/expense), what it
// is for, and What should it say back?: a sentence, a card or a list,
// filled by picking from what the workflow gives back. It writes the
// definition's tool block, and takes it away when switched off.

type Shape = "text" | "card" | "list";

const SHAPES: { value: Shape; label: string; hint: string }[] = [
  { value: "text", label: "A sentence", hint: "Words with values in them" },
  { value: "card", label: "A card", hint: "A title, lines and an amount" },
  { value: "list", label: "A list", hint: "A row for each one" },
];

/** A select of outputs: one to show, or none. */
function OutputSelect({
  label,
  outputs,
  value,
  onChange,
  none = "Nothing",
}: {
  label: string;
  outputs: Output[];
  value: string;
  onChange: (name: string) => void;
  none?: string;
}) {
  return (
    <select aria-label={label} value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">{none}</option>
      {outputs.map((o) => (
        <option key={o.name} value={o.name}>
          {o.title}
        </option>
      ))}
    </select>
  );
}

/** A value of a card: words typed, or a value picked. */
function PickField({
  label,
  outputs,
  value,
  onChange,
}: {
  label: string;
  outputs: Output[];
  value: Pick | undefined;
  onChange: (p: Pick | undefined) => void;
}) {
  const picked = value && "output" in value ? value.output : value ? "__words" : "";
  return (
    <span className="toolbar">
      <select
        aria-label={label}
        value={picked}
        onChange={(e) =>
          onChange(
            e.target.value === ""
              ? undefined
              : e.target.value === "__words"
                ? { text: "" }
                : { output: e.target.value },
          )
        }
      >
        <option value="">Nothing</option>
        <option value="__words">Words I type</option>
        {outputs.map((o) => (
          <option key={o.name} value={o.name}>
            {o.title}
          </option>
        ))}
      </select>
      {value && "text" in value && (
        <input
          aria-label={`${label}: the words`}
          value={value.text}
          onChange={(e) => onChange({ text: e.target.value })}
        />
      )}
    </span>
  );
}

/** A sentence made of words and values, in order. */
function SentenceEditor({
  parts,
  outputs,
  onChange,
}: {
  parts: Part[];
  outputs: Output[];
  onChange: (parts: Part[]) => void;
}) {
  const set = (i: number, p: Part) => onChange(parts.map((x, j) => (j === i ? p : x)));
  const drop = (i: number) => onChange(parts.filter((_, j) => j !== i));
  const shown = parts
    .map((p) => ("text" in p ? p.text : `[${outputs.find((o) => o.name === p.output)?.title ?? p.output}]`))
    .join("");
  return (
    <div className="tool-sentence" data-testid="tool-sentence">
      {parts.map((p, i) => (
        <span className="toolbar" key={i}>
          {"text" in p ? (
            <input aria-label={`Words, part ${i + 1}`} value={p.text} onChange={(e) => set(i, { text: e.target.value })} />
          ) : (
            <OutputSelect label={`Value, part ${i + 1}`} outputs={outputs} value={p.output} none="Choose…" onChange={(name) => set(i, { output: name })} />
          )}
          <button type="button" className="link" onClick={() => drop(i)}>
            Remove<span className="sr-only"> part {i + 1}</span>
          </button>
        </span>
      ))}
      <p className="toolbar">
        <button type="button" onClick={() => onChange([...parts, { text: " " }])} disabled={parts.length > 0 && "text" in (parts[parts.length - 1] ?? {})}>
          Add words
        </button>
        <button type="button" onClick={() => onChange([...parts, { output: outputs[0]?.name ?? "" }])} disabled={outputs.length === 0}>
          Add a value
        </button>
      </p>
      {shown.trim() && (
        <p className="hint">
          It says: <q>{shown}</q>
        </p>
      )}
    </div>
  );
}

/** Adds an output from a step's value: what the tool can then show. */
function AddOutput({ sources, taken, onAdd }: { sources: Source[]; taken: string[]; onAdd: (o: Output) => void }) {
  const [title, setTitle] = useState("");
  const [value, setValue] = useState("");
  const src = sources.find((s) => s.expr === value);
  return (
    <div className="row" data-testid="tool-add-output">
      <Field label="What it is called">
        <input value={title} placeholder="Total spent" onChange={(e) => setTitle(e.target.value)} />
      </Field>
      <Field label="Where it comes from">
        <ValuePicker label="Where it comes from" sources={sources} value={value} onChange={setValue} />
      </Field>
      <button
        type="button"
        disabled={!title.trim() || !value}
        onClick={() => {
          onAdd({
            name: outputName(title, taken),
            type: src?.number ? "number" : "string",
            title: title.trim(),
            description: title.trim(),
            value: value as `=${string}`,
          });
          setTitle("");
          setValue("");
        }}
      >
        Add it
      </button>
    </div>
  );
}

/** What the tool says back, by its shape, from what was picked. */
function AnswerEditor({ answer, outputs, name, onChange }: { answer: Answer | undefined; outputs: Output[]; name: string; onChange: (a: Answer | undefined) => void }) {
  const scalars = scalarOutputs(outputs);
  const lists = listOutputs(outputs);
  const shape = answer?.type;
  const choose = (s: Shape) => onChange(firstAnswer(s, outputs, name));
  const radio = useId();
  let body = null;
  if (answer?.type === "text") {
    const parts = sentenceParts(answer.text);
    body = parts ? (
      <SentenceEditor parts={parts} outputs={scalars} onChange={(p) => onChange({ type: "text", text: sentenceText(p) })} />
    ) : (
      <p className="hint">Its sentence was set up by hand. Choose a shape again to pick it here.</p>
    );
  } else if (answer?.type === "card") {
    const a = answer;
    const title = pickOf(a.title);
    const lines = a.lines ?? [];
    const linePicks = lines.map((l) => pickOf(l.value));
    const byHand = !title || (a.ref && !pickOf(a.ref)) || (a.state && !pickOf(a.state.text)) || (a.amount && !pickOf(a.amount)) || linePicks.some((p) => !p);
    const outName = (v?: string) => {
      const p = pickOf(v);
      return p && "output" in p ? p.output : "";
    };
    body = byHand ? (
      <p className="hint">Its card was set up by hand. Choose a shape again to pick it here.</p>
    ) : (
      <div data-testid="tool-card">
        <Field label="Its title">
          <PickField label="Its title" outputs={scalars} value={title} onChange={(p) => onChange({ ...a, title: pickValue(p) || name })} />
        </Field>
        <div className="row">
          <Field label="Its reference">
            <OutputSelect label="Its reference" outputs={scalars} value={outName(a.ref)} onChange={(n) => onChange({ ...a, ref: n ? pickValue({ output: n }) : undefined })} />
          </Field>
          <Field label="Its status">
            <OutputSelect label="Its status" outputs={scalars} value={outName(a.state?.text)} onChange={(n) => onChange({ ...a, state: n ? { text: pickValue({ output: n }), tone: "plain" } : undefined })} />
          </Field>
        </div>
        <Field label="An amount" hint="Shown in naira, from a whole amount the workflow gives back.">
          <OutputSelect label="An amount" outputs={amountOutputs(outputs)} value={outName(a.amount)} onChange={(n) => onChange({ ...a, amount: n ? pickValue({ output: n }) : undefined })} />
        </Field>
        <fieldset className="sf-choices">
          <legend>Its lines</legend>
          {lines.map((l, i) => (
            <span className="toolbar" key={i}>
              <input aria-label={`Line ${i + 1}: its label`} value={l.label} onChange={(e) => onChange({ ...a, lines: lines.map((x, j) => (j === i ? { ...x, label: e.target.value } : x)) })} />
              <OutputSelect label={`Line ${i + 1}: its value`} outputs={scalars} value={outName(l.value)} none="Choose…" onChange={(n) => onChange({ ...a, lines: lines.map((x, j) => (j === i ? { ...x, value: pickValue({ output: n }) } : x)) })} />
              <button type="button" className="link" onClick={() => onChange({ ...a, lines: lines.filter((_, j) => j !== i) })}>
                Remove<span className="sr-only"> line {i + 1}</span>
              </button>
            </span>
          ))}
          {lines.length < 6 && scalars.length > 0 && (
            <p>
              <button type="button" onClick={() => onChange({ ...a, lines: [...lines, { label: scalars[0]?.title ?? "", value: pickValue({ output: scalars[0]?.name ?? "" }) }] })}>
                Add a line
              </button>
            </p>
          )}
        </fieldset>
      </div>
    );
  } else if (answer?.type === "list") {
    const a = answer;
    const titleField = itemField(a.item.title);
    const subField = itemField(a.item.sub);
    body =
      titleField === undefined || subField === undefined ? (
        <p className="hint">Its list was set up by hand. Choose a shape again to pick it here.</p>
      ) : (
        <div data-testid="tool-list">
          <Field label="The list">
            <OutputSelect label="The list" outputs={lists} value={a.from} none="Choose…" onChange={(n) => onChange({ ...a, from: n })} />
          </Field>
          <Field label="Its heading">
            <input value={a.title ?? ""} onChange={(e) => onChange({ ...a, title: e.target.value || undefined })} />
          </Field>
          <div className="row">
            <Field label="Each row shows" hint="The name of the part of each row, as the list has it (name, amount).">
              <input value={titleField} placeholder="name" onChange={(e) => onChange({ ...a, item: { ...a.item, title: itemValue(e.target.value) } })} />
            </Field>
            <Field label="Under it">
              <input value={subField} placeholder="Nothing" onChange={(e) => onChange({ ...a, item: { ...a.item, sub: itemValue(e.target.value) || undefined } })} />
            </Field>
          </div>
          <Field label="When there are none">
            <input value={a.empty ?? ""} placeholder="Nothing to show." onChange={(e) => onChange({ ...a, empty: e.target.value || undefined })} />
          </Field>
        </div>
      );
  }
  return (
    <>
      <fieldset className="sf-choices" data-testid="tool-says-back">
        <legend>What should it say back?</legend>
        {SHAPES.map((s) => (
          <label key={s.value} className="row">
            <input
              type="radio"
              name={radio}
              checked={shape === s.value}
              disabled={s.value === "list" ? lists.length === 0 : s.value === "text" ? scalars.length === 0 : false}
              onChange={() => choose(s.value)}
            />
            <span>
              {s.label} <span className="hint">{s.value === "list" && lists.length === 0 ? "It gives back no list" : s.hint}</span>
            </span>
          </label>
        ))}
        <label className="row">
          <input type="radio" name={radio} checked={!shape} onChange={() => onChange(undefined)} />
          <span>
            Just that it is done <span className="hint">How the run ended</span>
          </span>
        </label>
      </fieldset>
      {body}
    </>
  );
}

/** The tool block of a definition, as the builder edits it. */
export function UseFromChat({
  def,
  onChange,
  problems,
  sources,
  care,
}: {
  def: WorkflowDefinition;
  onChange: (d: WorkflowDefinition) => void;
  /** The validator's and the last publish's problems. */
  problems: Problem[];
  /** Values of the workflow's steps, to give back. */
  sources: Source[];
  /** How careful the published version is (tool_care). */
  care?: string;
}) {
  const tool = def.tool;
  const manual = def.trigger.type === "manual";
  const outputs = def.outputs ?? [];
  const set = (t: Tool | undefined) => {
    const next = { ...def } as WorkflowDefinition;
    if (t) next.tool = t;
    else delete next.tool;
    onChange(next);
  };
  const patch = (p: Partial<Tool>) => tool && set({ ...tool, ...p });
  const mine = problems.filter((p) => p.path.startsWith("/tool") || (tool && (p.path.startsWith("/outputs") || p.path === "/inputs")));
  const bad = tool ? commandProblem(tool.command) : undefined;
  const addOutput = (o: Output) => {
    const next = { ...def, outputs: [...outputs, o] } as WorkflowDefinition;
    // What gives back is called with inputs, even none.
    if (!next.inputs?.schema) next.inputs = { schema: { type: "object", properties: {} } };
    if (next.tool && !next.tool.answer) next.tool = { ...next.tool, answer: firstAnswer("text", next.outputs ?? [], next.tool.name) };
    onChange(next);
  };
  return (
    <section className="use-from-chat" aria-labelledby="use-from-chat-h" data-testid="use-from-chat">
      <h2 id="use-from-chat-h">Use it from chat</h2>
      {!manual && !tool ? (
        <p className="hint">Only a workflow you start by hand can be used from chat.</p>
      ) : (
        <label className="switch-line">
          <input type="checkbox" role="switch" checked={!!tool} onChange={(e) => set(e.target.checked ? newTool(def.name, outputs, def.description) : undefined)} />
          <span>Use it from chat</span>
        </label>
      )}
      <p className="hint">Your people call it from the chat by a word, like /expense, and it answers there.</p>
      {tool && !manual && <p className="error" role="alert">Only a workflow you start by hand can be used from chat. Change when it runs, or switch this off.</p>}
      {tool && (
        <>
          <Field label="Its name" hint="What its tile is called.">
            <input
              value={tool.name}
              maxLength={40}
              onChange={(e) => {
                const name = e.target.value;
                // The command follows the name until it is changed by hand.
                patch(tool.command === suggestCommand(tool.name) ? { name, command: suggestCommand(name) } : { name });
              }}
            />
          </Field>
          <Field label="The word to call it by" hint={bad ?? `Typed as /${tool.command}. Small letters, numbers and dashes.`}>
            <input value={tool.command} maxLength={24} aria-invalid={!!bad} onChange={(e) => patch({ command: e.target.value.toLowerCase().replace(/\s+/g, "-") })} />
          </Field>
          <Field label="What it is for" hint="One line under its tile.">
            <input value={tool.about} maxLength={120} placeholder="Record money spent, with what it was for" onChange={(e) => patch({ about: e.target.value })} />
          </Field>
          {outputs.length === 0 ? (
            <div className="notice" role="note" data-testid="tool-no-outputs">
              <p>
                <strong>Say what it gives back first.</strong> Until then it answers with how its run ended. Add a value from its steps here
                {sources.length === 0 ? " once it has steps that give one" : ""}; all it gives back is under Advanced too.
              </p>
              {sources.length > 0 && <AddOutput sources={sources} taken={[]} onAdd={addOutput} />}
            </div>
          ) : (
            <>
              <AnswerEditor answer={tool.answer} outputs={outputs} name={tool.name} onChange={(answer) => patch({ answer })} />
              {sources.length > 0 && (
                <details>
                  <summary>Give back another value</summary>
                  <AddOutput sources={sources} taken={outputs.map((o) => o.name)} onAdd={addOutput} />
                </details>
              )}
            </>
          )}
          {mine.length > 0 && (
            <ul className="error" role="alert" data-testid="tool-problems">
              {mine.map((p, i) => (
                <li key={i}>{toolProblem(p)}</li>
              ))}
            </ul>
          )}
          {care && (
            <p className="hint" data-testid="tool-care">
              As published: <strong>{careWords(care)}</strong>.{" "}
              {care === "answers"
                ? "Its steps only read, so it answers without asking. An owner can make it always ask on its page."
                : care === "step_up"
                  ? "It moves money, so it asks yes and a passkey."
                  : "Its steps change things, so it shows what will run and asks yes."}
            </p>
          )}
        </>
      )}
    </section>
  );
}
