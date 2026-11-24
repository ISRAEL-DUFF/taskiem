// Each run's use of a saved thing (build plan H6, g-saved-wf): the runs
// that read one (GET /v1/saved/{id}/runs) and the things a run read (GET
// /v1/runs/{id}'s saved).

/** A run that read a saved thing at its start. */
export interface SavedUse {
  run_id: string;
  workflow_id: string;
  workflow: string;
  version: number;
  name: string;
  steps: string[];
  read_at: string;
  status?: string;
}

/** A saved thing a run read. */
export interface RunSavedRead {
  id: string;
  name: string;
  /** Left out for a saved person unless the run is revealed. */
  title?: string;
  kind: string;
  steps: string[];
  read_at: string;
}

/** Which steps read it, in words: "Read by step send", "Read by steps
 * note and send". */
export function stepsWords(steps: string[]): string {
  if (steps.length === 0) return "Read when the run started";
  if (steps.length === 1) return `Read by step ${steps[0]}`;
  return `Read by steps ${steps.slice(0, -1).join(", ")} and ${steps[steps.length - 1]}`;
}
