// A step's window (docs/workflow-builder.md): what came in on the left,
// its settings in the middle, what came out on the right. Fields on the
// left drag into the settings; Test this step runs the draft with sample
// data and fills both sides.
import { useRef } from "react";
import type { Step } from "@sdk/wd";
import type { ConnectorInfo, Problem } from "../api";
import { Icon } from "../icons";
import { StepPanel } from "../canvas/StepPanel";
import { FIELD_DRAG } from "../forms/FieldText";
import { isContainer } from "../lib/flow";
import type { Source } from "../lib/plaincanvas";
import { useFocusTrap } from "../ui";
import { ContainerSettings } from "./ContainerSettings";
import type { TryResult } from "./Canvas";
import { lookOf } from "./look";
import { sampleWords, valueAt } from "./sources";

interface Props {
  step: Step;
  connectors: ConnectorInfo[];
  sources: Source[];
  problems: Problem[];
  result?: TryResult;
  testing: boolean;
  kept: boolean;
  readOnly: boolean;
  /** "Step 3 of 6", and the steps either side by name. */
  place: { index: number; count: number; prev?: { id: string; title: string }; next?: { id: string; title: string } };
  onChange: (s: Step) => void;
  onRename: (id: string) => boolean;
  onDelete: () => void;
  onGo: (id: string) => void;
  onTest: () => void;
  onKeep: (keep: boolean) => void;
  onClose: () => void;
}

/** "From “Check the stock”" and the rest, grouped by where the value comes from. */
function groupsOf(sources: Source[]): { from: string; items: { label: string; expr: string }[] }[] {
  const out: { from: string; items: { label: string; expr: string }[] }[] = [];
  for (const s of sources) {
    const m = /^(From “[^”]*”|What was filled in|What started it): (.*)$/.exec(s.label);
    const from = m && m[1]?.startsWith("From ") ? m[1].replace(/^From /, "") : "What started it";
    const label = m ? (m[2] ?? s.label) : s.label;
    const g = out.find((x) => x.from === from);
    if (g) g.items.push({ label, expr: s.expr });
    else out.push({ from, items: [{ label, expr: s.expr }] });
  }
  return out;
}

export function StepWindow(p: Props) {
  const box = useRef<HTMLElement>(null);
  useFocusTrap(box, true, p.onClose);
  const look = lookOf(p.step, p.connectors);
  const title = p.step.name || look.title;
  const status = p.result ? (p.result.steps[p.step.id] ?? "not run") : undefined;
  const output = p.result?.outputs[p.step.id];
  const err = p.result?.errors[p.step.id];
  const { prev, next } = p.place;
  return (
    <div className="modal-backdrop stepwin-backdrop" onMouseDown={(e) => e.target === e.currentTarget && p.onClose()}>
      <section ref={box} className="stepwin" role="dialog" aria-modal="true" aria-label={title} data-testid="step-window">
        <header className="stepwin-head">
          <span className={`bcard-icon tone-${look.tone}`} aria-hidden="true">
            <Icon name={look.icon} />
          </span>
          <div className="stepwin-name">
            <h2>{title}</h2>
            <span>{look.line}</span>
          </div>
          {!p.readOnly && (
            <button type="button" className="pill" onClick={p.onTest} disabled={p.testing}>
              <Icon name="play" />
              {p.testing ? "Testing…" : "Test this step"}
            </button>
          )}
          <button type="button" className="icon-round" aria-label="Close" onClick={p.onClose}>
            <Icon name="close" />
          </button>
        </header>

        <div className="stepwin-body">
          <section className="stepwin-in" aria-label="What came in">
            <h3>What came in</h3>
            <p className="hint">{p.result ? "From the test with sample data. Drag a field into a setting." : "Test it to see sample values here. You can drag a field into a setting."}</p>
            {p.sources.length === 0 && <p className="hint">Nothing yet: this is the first step.</p>}
            {groupsOf(p.sources).map((g) => (
              <div key={g.from} className="in-group">
                <span className="in-from">{g.from}</span>
                {g.items.map((f) => (
                  <div
                    key={f.expr}
                    className="in-field"
                    draggable={!p.readOnly}
                    onDragStart={(e) => {
                      e.dataTransfer.setData(FIELD_DRAG, f.expr);
                      e.dataTransfer.setData("text/plain", f.label);
                      e.dataTransfer.effectAllowed = "copy";
                    }}
                    title={p.readOnly ? undefined : "Drag into a setting"}
                  >
                    <span className="in-label">{f.label}</span>
                    <span className="in-value">{sampleWords(valueAt(p.result, f.expr))}</span>
                  </div>
                ))}
              </div>
            ))}
          </section>

          <section className="stepwin-settings" aria-label="Settings">
            <fieldset disabled={p.readOnly} className="bare-set">
              {isContainer(p.step) && (
                <>
                  <label className="field">
                    <span>Name</span>
                    <input value={p.step.name ?? ""} placeholder={look.title} onChange={(e) => p.onChange({ ...p.step, name: e.target.value || undefined } as Step)} />
                  </label>
                  <ContainerSettings step={p.step} sources={p.sources} onChange={p.onChange} />
                </>
              )}
              {!isContainer(p.step) && (
                <StepPanel
                  key={p.step.id}
                  bare
                  step={p.step}
                  others={[]}
                  connectors={p.connectors}
                  sources={p.sources}
                  problems={p.problems}
                  onChange={p.onChange}
                  onRename={p.onRename}
                  onDelete={p.onDelete}
                />
              )}
            </fieldset>
          </section>

          <section className="stepwin-out" aria-label="What came out">
            <h3>What came out</h3>
            {!p.result && <p className="hint">Press Test this step to see what it gives, with sample data: nothing is sent.</p>}
            {status === "completed" && (
              <div className="out-status ok">
                <Icon name="check" /> It ran
              </div>
            )}
            {status === "failed" && <div className="out-status bad">It stopped: {err ?? "see the run's problem"}</div>}
            {(status === "not run" || status === "skipped") && <div className="out-status muted">It did not need to run in this test.</div>}
            {status && !["completed", "failed", "not run", "skipped"].includes(status) && <div className="out-status muted">It is {status} in this test.</div>}
            {output !== undefined && output !== null && typeof output === "object" && (
              <div className="in-group">
                {Object.entries(output as Record<string, unknown>).map(([k, v]) => (
                  <div key={k} className="in-field">
                    <span className="in-label">{(k.charAt(0).toUpperCase() + k.slice(1)).replaceAll("_", " ")}</span>
                    <span className="in-value">{sampleWords(v)}</span>
                  </div>
                ))}
              </div>
            )}
            {status === "completed" && !p.readOnly && p.step.type === "connector" && (
              <label className="inline keep">
                <input type="checkbox" checked={p.kept} onChange={(e) => p.onKeep(e.target.checked)} /> Keep this result for testing the next steps
              </label>
            )}
            <p className="hint out-foot">Tests use sample data: no message is sent and no money moves.</p>
          </section>
        </div>

        <footer className="stepwin-foot">
          {prev ? (
            <button type="button" className="quiet" onClick={() => p.onGo(prev.id)}>
              <Icon name="back" /> {prev.title}
            </button>
          ) : (
            <span />
          )}
          <span className="hint">
            Step {p.place.index} of {p.place.count}
          </span>
          {next && (
            <button type="button" className="quiet" onClick={() => p.onGo(next.id)}>
              {next.title} <Icon name="chevron" />
            </button>
          )}
          <span className="grow" />
          {!p.readOnly && (
            <button type="button" className="danger quiet" onClick={p.onDelete}>
              Delete this step
            </button>
          )}
          <button type="button" className="primary" onClick={p.onClose}>
            Done
          </button>
        </footer>
      </section>
    </div>
  );
}
