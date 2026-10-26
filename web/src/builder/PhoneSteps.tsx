// The builder on a phone (docs/workflow-builder.md): the steps as a list
// instead of a canvas. What starts it first, then each step in order; a
// choice's paths are listed under it with their Yes and No. Tapping a step
// opens its window; "Add a step here" adds one after it.
import type { Step, WorkflowDefinition } from "@sdk/wd";
import { Icon } from "../icons";
import { childLists, type InsertAt, type ListRef } from "../lib/flow";
import type { TryResult } from "./Canvas";
import { lookOf, stepTitle } from "./look";

type ConnectorLike = NonNullable<Parameters<typeof lookOf>[1]>[number];

interface Props {
  def: WorkflowDefinition;
  connectors: ConnectorLike[];
  triggerWords: string;
  result?: TryResult;
  readOnly: boolean;
  testing: boolean;
  onOpen: (id: string) => void;
  onOpenTrigger: () => void;
  onAdd: (at: InsertAt) => void;
  onTest: () => void;
}

/** A list's steps in the order they run: each after the ones it needs. */
function inOrder(steps: Step[]): Step[] {
  const out: Step[] = [];
  const done = new Set<string>();
  const ids = new Set(steps.map((s) => s.id));
  let rest = steps;
  while (rest.length) {
    const ready = rest.filter((s) => (s.needs ?? []).every((n) => done.has(n) || !ids.has(n)));
    const next = ready.length ? ready : rest.slice(0, 1);
    for (const s of next) {
      out.push(s);
      done.add(s.id);
    }
    rest = rest.filter((s) => !done.has(s.id));
  }
  return out;
}

export function PhoneSteps(p: Props) {
  const list = (steps: Step[], ref: ListRef, depth: number): React.ReactNode => {
    const ordered = inOrder(steps);
    return (
      <ol className={`phone-steps${depth ? " nested" : ""}`}>
        {ordered.map((s) => {
          const look = lookOf(s, p.connectors);
          const status = p.result?.steps[s.id];
          return (
            <li key={s.id}>
              <button type="button" className={`phone-step${status === "failed" ? " failed" : ""}`} onClick={() => p.onOpen(s.id)} data-testid={`phone-step-${s.id}`}>
                <span className={`bcard-icon tone-${look.tone}`} aria-hidden="true">
                  <Icon name={look.icon} />
                </span>
                <span className="phone-step-text">
                  <span className="bcard-title">{stepTitle(s, p.connectors)}</span>
                  <span className="bcard-line">{status === "skipped" || (p.result && !status) ? "Did not need to run" : look.line}</span>
                </span>
                {status === "completed" && (
                  <span className="ran-mark" role="img" aria-label="Ran">
                    <Icon name="check" />
                  </span>
                )}
                <Icon name="chevron" className="phone-step-go" />
              </button>
              {childLists(s).map((c) => (
                <div key={c.ref} className="phone-path">
                  <span className={`barrow-label${c.tone ? ` ${c.tone}` : ""}`}>{c.label}</span>
                  {c.steps.length ? list(c.steps, c.ref, depth + 1) : <p className="hint">Nothing yet.</p>}
                  {!p.readOnly && c.steps.length === 0 && (
                    <button type="button" className="link phone-add" onClick={() => p.onAdd({ list: c.ref })}>
                      <Icon name="plus" /> Add a step here
                    </button>
                  )}
                </div>
              ))}
              {!p.readOnly && (
                <button type="button" className="link phone-add" onClick={() => p.onAdd({ list: ref, after: s.id })} aria-label={`Add a step here, after ${stepTitle(s, p.connectors)}`}>
                  <Icon name="plus" /> Add a step here
                </button>
              )}
            </li>
          );
        })}
      </ol>
    );
  };
  return (
    <div className="phone-builder" data-testid="phone-steps">
      <button type="button" className="phone-step trigger" onClick={p.onOpenTrigger}>
        <span className="bcard-icon tone-brand" aria-hidden="true">
          <Icon name="bolt" />
        </span>
        <span className="phone-step-text">
          <span className="bcard-kicker">When it runs</span>
          <span className="bcard-title">{p.triggerWords}</span>
        </span>
        <Icon name="chevron" className="phone-step-go" />
      </button>
      {p.def.steps.length === 0 && !p.readOnly && (
        <button type="button" className="link phone-add" onClick={() => p.onAdd({ list: "" })}>
          <Icon name="plus" />
          Add the first step
        </button>
      )}
      {list(p.def.steps, "", 0)}
      {!p.readOnly && (
        <button type="button" className="pill phone-test" onClick={p.onTest} disabled={p.testing}>
          <Icon name="play" />
          {p.testing ? "Testing…" : "Test workflow"}
        </button>
      )}
    </div>
  );
}
