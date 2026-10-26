// The settings of the steps that hold steps, in plain words: an If's
// condition ("Go the Yes way when"), a choice's paths, what "do these
// together" waits for, and the list a "for each" goes through.
import type { Step } from "@sdk/wd";
import { Field } from "../ui";
import { RulesBuilder, ValuePicker } from "../canvas/PlainPickers";
import type { Source } from "../lib/plaincanvas";

type Paths = { name: string; when: string; steps: Step[] }[];

export function ContainerSettings({ step, sources, onChange }: { step: Step; sources: Source[]; onChange: (s: Step) => void }) {
  if (step.type === "branch") {
    const c = step.config as { paths: Paths; default?: { steps: Step[] } };
    const setPaths = (paths: Paths) => onChange({ ...step, config: { ...c, paths } } as Step);
    const rule = (when: string) => (when === "=true" ? "" : when);
    const yes = c.paths[0];
    if (c.paths.length === 1 && c.default && yes?.name === "yes") {
      return (
        <Field label="Go the Yes way when" hint="Otherwise it goes the No way.">
          <RulesBuilder sources={sources} value={rule(yes.when)} onChange={(when) => setPaths([{ ...yes, when: when || "=true" }])} />
        </Field>
      );
    }
    return (
      <>
        {c.paths.map((p, i) => (
          <fieldset key={i} className="path-set">
            <legend>Path {i + 1}</legend>
            <Field label="Its name">
              <input value={p.name} onChange={(e) => setPaths(c.paths.map((x, j) => (j === i ? { ...x, name: e.target.value.replace(/[^a-z0-9_]/gi, "_").toLowerCase() || "path" } : x)))} />
            </Field>
            <Field label="Go this way when">
              <RulesBuilder sources={sources} value={rule(p.when)} onChange={(when) => setPaths(c.paths.map((x, j) => (j === i ? { ...x, when: when || "=true" } : x)))} />
            </Field>
          </fieldset>
        ))}
        <p className="toolbar">
          <button type="button" onClick={() => setPaths([...c.paths, { name: `path_${c.paths.length + 1}`, when: "=true", steps: [] }])}>
            Add a path
          </button>
        </p>
        <p className="hint">The first path whose condition holds is taken{c.default ? "; when none does, Otherwise." : "."}</p>
      </>
    );
  }
  if (step.type === "parallel") {
    const c = step.config;
    return (
      <Field label="Carry on when">
        <select value={c.join ?? "all"} onChange={(e) => onChange({ ...step, config: { ...c, join: e.target.value as "all" | "any" } } as Step)}>
          <option value="all">All of them have finished</option>
          <option value="any">The first one finishes (the others stop)</option>
        </select>
      </Field>
    );
  }
  if (step.type === "foreach") {
    const c = step.config;
    return (
      <>
        <Field label="For each item in">
          <ValuePicker label="For each item in" sources={sources} value={c.items} onChange={(items) => onChange({ ...step, config: { ...c, items } } as Step)} />
        </Field>
        <Field label="At most at once" hint="How many items it works on at the same time">
          <input type="number" min={1} max={50} value={c.max_concurrency ?? 1} onChange={(e) => onChange({ ...step, config: { ...c, max_concurrency: Math.max(1, Number(e.target.value) || 1) } } as Step)} />
        </Field>
      </>
    );
  }
  return null;
}
