import { useRef, useState } from "react";
import { ApiError, post } from "../api";
import { ErrorBox, Field, Items, Modal, Row, useAction } from "../ui";
import { savedIcon } from "../lib/saved";
import {
  IMPORT_COLUMNS,
  importChanged,
  importDoneLine,
  importExampleHref,
  importKindWord,
  importPlanLine,
  importRefusal,
  importSaveWords,
  importSizeProblem,
  importToSave,
  readImportFile,
  type ImportDone,
  type ImportPlan,
  type ImportRefusal,
} from "../lib/savedImport";

// Saved things from a file (saved objects S3; POST /v1/saved/import): the
// business's Saved page, for those who may change saved things. The file
// is checked whole and shown first; a file with any line that cannot be
// taken saves nothing, and says each line and why. The file is read again
// each time it is sent, so one put right and chosen again is the one sent.

/** Import from a file: pick a CSV, see what it saves, then save it. onSaved
 * runs after a save, to read the list again. */
export function ImportSheet({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const [file, setFile] = useState<File | null>(null);
  const [fileKey, setFileKey] = useState(0);
  const [plan, setPlan] = useState<ImportPlan | null>(null);
  const [refusal, setRefusal] = useState<ImportRefusal | null>(null);
  const [done, setDone] = useState<ImportDone | null>(null);
  // Each pick counts: an answer for a file picked before another is dropped.
  const pickRef = useRef(0);
  const act = useAction();
  // Picked again after a check: the input is emptied, so choosing the same
  // file (put right meanwhile) is a new pick.
  const forget = () => {
    pickRef.current++;
    setFile(null);
    setFileKey((k) => k + 1);
  };
  const send = (confirm?: string) =>
    void act.run(async () => {
      if (!file) return;
      const pick = pickRef.current;
      setRefusal(null);
      const big = importSizeProblem(file.size);
      if (big) throw new Error(big);
      let csv: string;
      try {
        csv = await readImportFile(file);
      } catch {
        forget();
        throw new Error("The file could not be read: choose it again.");
      }
      try {
        const r = await post<ImportPlan | ImportDone>("/v1/saved/import", confirm ? { csv, confirm } : { csv });
        if (pick !== pickRef.current) return;
        if (r.saved) {
          setDone(r);
          setPlan(null);
          forget();
          onSaved();
        } else setPlan(r);
      } catch (e) {
        if (pick !== pickRef.current) return;
        // The file is not the one checked: check it again.
        if (e instanceof ApiError && importChanged(e.status, e.body)) setPlan(null);
        const why = e instanceof ApiError ? importRefusal(e.status, e.body, e.message) : null;
        if (!why) throw e;
        setRefusal(why);
        setPlan(null);
        forget();
      }
    }, false);
  const toSave = plan ? importToSave(plan.rows).length : 0;

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
        {done?.unchecked && done.unchecked.length > 0 && (
          <>
            <h3>Not checked yet</h3>
            <Items label="Not checked yet">
              {done.unchecked.map((u) => (
                <Row key={u.line} icon="warn" tone="warn" title={`Line ${u.line}: @${u.name}`} sub={<span className="sub">{u.problem}</span>} testid="import-unchecked" />
              ))}
            </Items>
            <p className="hint">Check names on the Saved page.</p>
          </>
        )}
        {done?.skipped && done.skipped.length > 0 && (
          <>
            <h3>Saved already, skipped</h3>
            <Items label="Saved already, skipped">
              {done.skipped.map((n) => (
                <Row key={n} icon="check" title={`@${n}`} testid="import-skipped" />
              ))}
            </Items>
          </>
        )}
        {done?.left && done.left.length > 0 && (
          <>
            <h3>Not saved yet: send the file again</h3>
            <Items label="Not saved yet">
              {done.left.map((l) => (
                <Row key={l.line} icon="warn" tone="warn" title={`Line ${l.line}: @${l.name}`} testid="import-left" />
              ))}
            </Items>
          </>
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
              if (file) send();
            }}
          >
            <Field
              label="Saved things file (CSV)"
              hint="A spreadsheet saved as CSV, one thing on each line, up to 200. Every line is checked first: a file with a line that cannot be taken saves nothing."
            >
              <input
                key={fileKey}
                type="file"
                accept=".csv,.txt,.tsv,text/csv,text/plain,text/tab-separated-values"
                data-testid="import-file"
                onChange={(e) => {
                  pickRef.current++;
                  setPlan(null);
                  setRefusal(null);
                  setDone(null);
                  act.clear();
                  setFile(e.target.files?.[0] ?? null);
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
                <button type="submit" className="primary" disabled={act.busy || !file}>
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
                      {r.skip && " · saved already: skipped"}
                      {r.hint && <span className="block">{r.hint}</span>}
                      {r.warning && (
                        <span className="block import-warning" data-testid="import-warning">
                          {r.warning}
                        </span>
                      )}
                    </span>
                  }
                  tone={r.skip ? "muted" : undefined}
                  testid="import-row"
                  attrs={r.skip ? { "data-name": r.name, "data-skip": "true" } : { "data-name": r.name }}
                />
              ))}
            </Items>
            <div className="row-actions">
              {toSave > 0 && (
                <button type="button" className="primary" disabled={act.busy || !file} onClick={() => send(plan.hash)} data-testid="import-save">
                  {act.busy ? "Saving…" : importSaveWords(toSave)}
                </button>
              )}
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
