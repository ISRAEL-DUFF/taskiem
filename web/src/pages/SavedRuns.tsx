import { get } from "../api";
import { useAuth } from "../auth";
import { stepsWords, type SavedUse } from "../lib/savedUses";
import { fmtTime, Items, Item, useLoad } from "../ui";

/** The runs that read one of the business's saved things when they
 * started (build plan H6, GET /v1/saved/{id}/runs), newest first: for
 * those who may read runs. A person's own saved things are never read by
 * runs, so they have none. */
export function SavedRuns({ id }: { id: string }) {
  const { can } = useAuth();
  const may = can("run.read");
  const runs = useLoad(() => (may ? get<{ runs: SavedUse[] }>(`/v1/saved/${encodeURIComponent(id)}/runs`) : Promise.resolve({ runs: [] })), [id, may]);
  if (!may || !runs.data) return null;
  return (
    <section className="saved-runs" aria-labelledby={`saved-runs-${id}`} data-testid="saved-runs">
      <h3 id={`saved-runs-${id}`}>Runs that read it</h3>
      {runs.data.runs.length === 0 ? (
        <p className="hint">No workflow has read it yet. A workflow reads it when a run starts, so a change is used from the next run.</p>
      ) : (
        <Items label="Runs that read it">
          {runs.data.runs.map((u) => (
            <Item
              key={u.run_id}
              to={`/runs/${encodeURIComponent(u.run_id)}`}
              icon="play"
              tone={u.status === "failed" ? "danger" : u.status === "completed" ? "ok" : undefined}
              title={u.workflow || "A workflow"}
              side={fmtTime(u.read_at)}
              sub={stepsWords(u.steps)}
            />
          ))}
        </Items>
      )}
    </section>
  );
}
