import { useState } from "react";
import type { MergeConflict } from "../api";
import { Modal } from "../ui";

/** Lets an editor choose, for each conflict, their edit or the newer version's. */
export function MergeDialog({
  conflicts,
  latest,
  busy,
  ours = "Yours",
  onClose,
  onResolve,
}: {
  conflicts: MergeConflict[];
  latest: number;
  busy: boolean;
  /** What the edit being saved is called (an AI proposal is not "yours"). */
  ours?: string;
  onClose: () => void;
  onResolve: (res: Record<string, "ours" | "theirs">) => void;
}) {
  const [choice, setChoice] = useState<Record<string, "ours" | "theirs">>({});
  const show = (v: unknown) => (v === undefined ? "(removed)" : JSON.stringify(v, null, 2));
  const label = (c: MergeConflict) => (c.path.startsWith("steps/") ? `Step ${c.path.slice(6)}` : `Workflow ${c.path}`);
  return (
    <Modal title="Resolve conflicting edits" onClose={onClose}>
      <p className="hint">
        Version {latest} changed these parts too. Everything else is merged. Choose which version of each to keep, then save.
      </p>
      {conflicts.map((c) => (
        <fieldset key={c.path} className="card" data-testid={`conflict-${c.path}`}>
          <legend>
            {label(c)} {c.kind === "changed_and_deleted" && "(changed on one side, removed on the other)"}
          </legend>
          <div className="row">
            {(["ours", "theirs"] as const).map((side) => (
              <label key={side} className="grow">
                <input type="radio" name={c.path} checked={choice[c.path] === side} onChange={() => setChoice({ ...choice, [c.path]: side })} />{" "}
                {side === "ours" ? ours : `Version ${latest}`}
                <pre className="mono">{show(c[side])}</pre>
              </label>
            ))}
          </div>
        </fieldset>
      ))}
      <div className="row">
        <button className="primary" disabled={busy || conflicts.some((c) => !choice[c.path])} onClick={() => onResolve(choice)}>
          Save merged version
        </button>
        <button onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  );
}
