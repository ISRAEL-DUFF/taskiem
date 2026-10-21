import { Link } from "react-router-dom";
import type { Problem } from "../api";

/**
 * A problem in plain words (no-technical-steps N5): what the server said
 * for a business owner, with its fix, and the technical words under
 * Details. onStep opens a step in the editor (a fix of #step=<id>).
 */
export function ProblemLine({ p, onStep }: { p: Problem; onStep?: (id: string) => void }) {
  const fix = p.fix;
  const step = fix?.path.startsWith("#step=") ? fix.path.slice("#step=".length) : "";
  return (
    <>
      {p.plain ?? p.message}
      {fix && !step && (
        <>
          {" "}
          <Link to={fix.path}>{fix.label}</Link>
        </>
      )}
      {step && onStep && (
        <>
          {" "}
          <button type="button" className="link" onClick={() => onStep(step)}>
            {fix?.label}
          </button>
        </>
      )}
      {p.plain && (
        <details className="hint">
          <summary>Details</summary>
          <code>{p.path}</code> {p.message}
        </details>
      )}
    </>
  );
}
