/** Where the run page streams new events from: the run its loaded history
 * is of (not the address, which moves first when another run opens), after
 * the last event loaded. */
export function runStreamPath(doc: { run: { id: string }; events: { seq: number }[] }): string {
  return `/v1/runs/${encodeURIComponent(doc.run.id)}/stream?after=${doc.events.at(-1)?.seq ?? 0}`;
}
