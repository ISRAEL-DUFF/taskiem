import { describe, expect, it } from "vitest";
import { runStreamPath } from "./runStream";

// The run page's live stream carries on from the history it loaded. While
// the page for run B is still loading, that history is run A's: the
// stream must be A's too, never B's from A's last seq.
describe("the run page's live stream", () => {
  it("follows the run the loaded history is of, from its last event", () => {
    const loadedA = { run: { id: "aaaaaaaa-0000-0000-0000-000000000000" }, events: [{ seq: 1 }, { seq: 7 }] };
    expect(runStreamPath(loadedA)).toBe("/v1/runs/aaaaaaaa-0000-0000-0000-000000000000/stream?after=7");
    expect(runStreamPath({ run: { id: "b" }, events: [] })).toBe("/v1/runs/b/stream?after=0");
  });
});
