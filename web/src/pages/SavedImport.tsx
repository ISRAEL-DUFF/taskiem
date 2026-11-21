import { useState } from "react";
import { ApiError, post } from "../api";
import { ErrorBox, Field, Items, Modal, Row, useAction } from "../ui";
import { savedIcon } from "../lib/saved";
import {
  IMPORT_COLUMNS,
  importDoneLine,
  importExampleHref,
  importKindWord,
  importPlanLine,
  importRefusal,
  importSaveWords,
  type ImportDone,
  type ImportPlan,
  type ImportRefusal,
} from "../lib/savedImport";

// Saved things from a file (saved objects S3; POST /v1/saved/import): the
// business's Saved page, for those who may change saved things. The file
// is checked whole and shown first; a file with any line that cannot be
// taken saves nothing, and says each line and why.

/** Import from a file: pick a CSV, see what it saves, then save it. onSaved
 * runs after a save, to read the list again. */
export function ImportSheet({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const [text, setText] = useState<string | null>(null);
  const [fileKey, setFileKey] = useState(0);
  const [plan, setPlan] = useState<ImportPlan | null>(null);
  const [refusal, setRefusal] = useState<ImportRefusal | null>(null);
  const [done, setDone] = useState<ImportDone | null>(null);
  const act = useAction();
  const send = (confirm?: string) =>
    void act.run(async () => {
      setRefusal(null);
      try {
        const r = await post<ImportPlan | ImportDone>("/v1/saved/import", confirm ? { csv: text, confirm } : { csv: text });
        if (r.saved) {
          setDone(r);
          setPlan(null);
          setText(null);
          setFileKey((k) => k + 1);
          onSaved();
        } else setPlan(r);
      } catch (e) {
        const why = e instanceof ApiError ? importRefusal(e.status, e.body, e.message) : null;
        if (!why) throw e;
        setRefusal(why);
        setPlan(null);
      }
    }, false);

  return (
    <Modal title="Import from a file" onClose={onClose}>
      <div data-testid="saved-import">
        <div role="status" data-testid="import-status">
          {done && <div className="notice">{importDoneLine(done)}</div>}
        </div>
        {done && done.failed.length > 0 && (
          <Items label="Lines not saved">
            {done.failed.map((f) => (
              <Row key={f.line} icon="warn" tone="warn" title={`Line ${f.line}: @${f.name}`} sub={<span className="sub">{f.message}</span>} testid="import-failed" />
            ))}
          </Items>
        )}
        {done && (
          <div className="row-actions">
            <button type="button" className="primary" onClick={onClose}>
              Done
            </button>
            <button type="button" onClick={() => setDone(null)}>
              Import another file
            </button>
          </div>
        )}
        {!done && (
          <form
            noValidate
            onSubmit={(e) => {
              e.preventDefault();
              if (text) send();
            }}
          >
            <Field
              label="Saved things file (CSV)"
              hint="A spreadsheet saved as CSV, one thing on each line, up to 200. Every line is checked first: a file with a line that cannot be taken saves nothing."
            >
              <input
                key={fileKey}
                type="file"
                accept=".csv,text/csv"
                data-testid="import-file"
                onChange={(e) => {
                  const f = e.target.files?.[0];
                  setPlan(null);
                  setRefusal(null);
                  setDone(null);
                  act.clear();
                  if (f) void f.text().then(setText);
                  else setText(null);
                }}
              />
            </Field>
            {!plan && (
              <>
                <p className="hint">{IMPORT_COLUMNS}</p>
                <p>
                  <a className="button small" href={importExampleHref()} download="saved-things-example.csv" data-testid="import-example">
                    Download an example
                  </a>
                </p>
              </>
            )}
            {refusal && (
              <div className="error" role="alert" data-testid="import-refusal">
                {refusal.message}
              </div>
            )}
            {refusal && refusal.problems.length > 0 && (
              <Items label="Lines that cannot be saved">
                {refusal.problems.map((p) => (
                  <Row key={`${p.line}/${p.message}`} icon="warn" tone="danger" title={`Line ${p.line}`} sub={<span className="sub">{p.message}</span>} testid="import-problem" />
                ))}
              </Items>
            )}
            {refusal && refusal.total > 0 && refusal.problems.length > 0 && <p className="hint">The file has {refusal.total} lines with things on them.</p>}
            <ErrorBox error={act.error} />
            {!plan && (
              <div className="row-actions">
                <button type="submit" className="primary" disabled={act.busy || !text}>
                  {act.busy ? "Checking…" : "Check the file"}
                </button>
                <button type="button" onClick={onClose}>
                  Cancel
                </button>
              </div>
            )}
          </form>
        )}
        {plan && (
          <section aria-labelledby="import-plan-h" data-testid="import-plan">
            <h3 id="import-plan-h">Check before saving</h3>
            <p>{importPlanLine(plan)}</p>
            <Items label="What the file saves">
              {plan.rows.map((r) => (
                <Row
                  key={r.line}
                  icon={savedIcon(r.kind)}
                  title={r.title}
                  sub={
                    <span className="sub">
                      @{r.name} · {importKindWord(r.kind)}
                      {r.hint && <span className="block">{r.hint}</span>}
                    </span>
                  }
                  testid="import-row"
                  attrs={{ "data-name": r.name }}
                />
              ))}
            </Items>
            <div className="row-actions">
              <button type="button" className="primary" disabled={act.busy} onClick={() => send(plan.hash)} data-testid="import-save">
                {act.busy ? "Saving…" : importSaveWords(plan.rows.length)}
              </button>
              <button type="button" onClick={onClose}>
                Cancel
              </button>
            </div>
          </section>
        )}
      </div>
    </Modal>
  );
}
