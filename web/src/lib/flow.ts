// The builder's canvas model (docs/brand/screens.md, the workflow builder):
// a definition's steps at every depth (a choice's paths, steps done
// together, a for-each's steps) as one picture of cards and arrows, the
// edits the canvas makes on it, and where each card goes when nobody has
// placed it. Pure functions over the definition, so they are tested
// without a browser.
import type { Step, WorkflowDefinition } from "@sdk/wd";

/** Where a list of steps lives: "" is the workflow's own steps;
 * "<id>/paths/<i>", "<id>/default", "<id>/branches/<i>" and "<id>/each"
 * are the steps inside a choice, its "otherwise", a "do these together"
 * and a "for each". */
export type ListRef = string;

export interface XY {
  x: number;
  y: number;
}

type Config = Record<string, unknown> & {
  paths?: { name: string; when: string; steps: Step[] }[];
  default?: { steps: Step[] };
  branches?: { name: string; steps: Step[] }[];
  steps?: Step[];
};

const cfg = (s: Step): Config => ((s as { config?: Config }).config ?? {}) as Config;

/** A step that holds other steps: a choice, steps done together, or a for each. */
export const isContainer = (s: Step) => s.type === "branch" || s.type === "parallel" || s.type === "foreach";

/** The lists a step holds, each with its words for the canvas. */
export function childLists(s: Step): { ref: ListRef; label: string; tone?: "yes" | "no"; steps: Step[] }[] {
  const c = cfg(s);
  if (s.type === "branch") {
    const paths = c.paths ?? [];
    // An If: one path called yes and, optionally, the rest (No). An empty
    // No is left out when saved (forSaving), so a missing default is No too.
    const ifElse = paths.length === 1 && (paths[0]?.name === "yes" || !!c.default);
    const out: { ref: ListRef; label: string; tone?: "yes" | "no"; steps: Step[] }[] = paths.map((p, i) => ({
      ref: `${s.id}/paths/${i}`,
      label: ifElse && p.name === "yes" ? "Yes" : words(p.name),
      tone: ifElse && p.name === "yes" ? ("yes" as const) : undefined,
      steps: p.steps ?? [],
    }));
    if (c.default || ifElse) out.push({ ref: `${s.id}/default`, label: ifElse ? "No" : "Otherwise", tone: ifElse ? "no" : undefined, steps: c.default?.steps ?? [] });
    return out;
  }
  if (s.type === "parallel") return (c.branches ?? []).map((b, i) => ({ ref: `${s.id}/branches/${i}`, label: words(b.name), steps: b.steps ?? [] }));
  if (s.type === "foreach") return [{ ref: `${s.id}/each`, label: "For each", steps: c.steps ?? [] }];
  return [];
}

const words = (id: string) => (id.charAt(0).toUpperCase() + id.slice(1)).replaceAll("_", " ");

function setChild(s: Step, kind: string, idx: number, steps: Step[]): Step {
  const c = cfg(s);
  let next: Config;
  if (kind === "paths") next = { ...c, paths: (c.paths ?? []).map((p, i) => (i === idx ? { ...p, steps } : p)) };
  else if (kind === "default") next = { ...c, default: { steps } };
  else if (kind === "branches") next = { ...c, branches: (c.branches ?? []).map((b, i) => (i === idx ? { ...b, steps } : b)) };
  else next = { ...c, steps };
  return { ...s, config: next } as Step;
}

/** Every step at every depth with the list it is in, outer lists first. */
export function placed(steps: Step[], list: ListRef = ""): { step: Step; list: ListRef }[] {
  const out: { step: Step; list: ListRef }[] = [];
  for (const s of steps) {
    out.push({ step: s, list });
    for (const c of childLists(s)) out.push(...placed(c.steps, c.ref));
  }
  return out;
}

/** The step with an id, anywhere, and the list it is in. */
export function locate(steps: Step[], id: string): { step: Step; list: ListRef } | undefined {
  return placed(steps).find((p) => p.step.id === id);
}

/** The steps of a list. */
export function listAt(steps: Step[], ref: ListRef): Step[] {
  if (ref === "") return steps;
  const [owner = "", kind = "", i = "0"] = ref.split("/");
  const s = locate(steps, owner)?.step;
  if (!s) return [];
  const c = cfg(s);
  if (kind === "paths") return c.paths?.[Number(i)]?.steps ?? [];
  if (kind === "default") return c.default?.steps ?? [];
  if (kind === "branches") return c.branches?.[Number(i)]?.steps ?? [];
  return c.steps ?? [];
}

/** Every step, at every depth, after fn. */
export function mapSteps(steps: Step[], fn: (s: Step) => Step): Step[] {
  return steps.map((s) => {
    let next = fn(s);
    for (const c of childLists(next)) {
      const [, kind = "", i = "0"] = c.ref.split("/");
      const inner = mapSteps(c.steps, fn);
      if (inner.some((x, j) => x !== c.steps[j])) next = setChild(next, kind, Number(i), inner);
    }
    return next;
  });
}

/** The steps with one list replaced. */
export function withList(steps: Step[], ref: ListRef, next: Step[]): Step[] {
  if (ref === "") return next;
  const [owner = "", kind = "", i = "0"] = ref.split("/");
  return mapSteps(steps, (s) => (s.id === owner ? setChild(s, kind, Number(i), next) : s));
}

/** The definition as saved: an If whose No has nothing in it has no default
 * (wd/v1 lists hold at least one step; a choice without a default carries on). */
export function forSaving<D extends { steps: Step[] }>(def: D): D {
  return {
    ...def,
    steps: mapSteps(def.steps, (s) => {
      const c = cfg(s);
      if (s.type !== "branch" || !c.default || (c.default.steps ?? []).length > 0) return s;
      const rest = Object.fromEntries(Object.entries(c).filter(([k]) => k !== "default"));
      return { ...s, config: rest } as Step;
    }),
  };
}

/** The paths with nothing in them, which wd/v1 does not allow: "Yes of “Is it so?”". */
export function emptyPaths(steps: Step[], title: (s: Step) => string): string[] {
  const out: string[] = [];
  for (const p of placed(steps)) {
    for (const c of childLists(p.step)) {
      if (c.steps.length === 0 && !(c.tone === "no")) out.push(`${c.label} of “${title(p.step)}”`);
    }
  }
  return out;
}

/** Where a new step goes: after a step, before one, both (between), or neither (a list's first). */
export interface InsertAt {
  list: ListRef;
  after?: string;
  before?: string;
}

const needsOf = (s: Step) => s.needs ?? [];
const withNeeds = (s: Step, needs: string[]): Step => {
  const next = { ...s, needs: [...new Set(needs)] };
  if (next.needs.length === 0) delete (next as { needs?: string[] }).needs;
  return next;
};

/** The steps with a new one put where the canvas's + was. */
export function insertStep(steps: Step[], at: InsertAt, step: Step): Step[] {
  const list = listAt(steps, at.list);
  let s = withNeeds(step, at.after ? [at.after] : []);
  let next = list;
  if (at.before) {
    const before = list.find((x) => x.id === at.before);
    if (before && !at.after) s = withNeeds(step, needsOf(before));
    next = list.map((x) => (x.id !== at.before ? x : withNeeds(x, at.after ? needsOf(x).map((n) => (n === at.after ? s.id : n)) : [s.id])));
  }
  // Placed beside its neighbour in the list, so the definition reads in order.
  const anchor = at.after ?? at.before;
  const idx = anchor ? next.findIndex((x) => x.id === anchor) : -1;
  const pos = idx < 0 ? next.length : at.after ? idx + 1 : idx;
  next = [...next.slice(0, pos), s, ...next.slice(pos)];
  return withList(steps, at.list, next);
}

/** The steps without one (and what it held); the steps after it now come
 * after what came before it, so the chain stays joined. */
export function removeAnywhere(steps: Step[], id: string): Step[] {
  const at = locate(steps, id);
  if (!at) return steps;
  const list = listAt(steps, at.list);
  const gone = needsOf(at.step);
  const next = list.filter((s) => s.id !== id).map((s) => (needsOf(s).includes(id) ? withNeeds(s, [...needsOf(s).filter((n) => n !== id), ...gone]) : s));
  return withList(steps, at.list, next);
}

/** One step changed, wherever it is. */
export function updateAnywhere(steps: Step[], id: string, fn: (s: Step) => Step): Step[] {
  return mapSteps(steps, (s) => (s.id === id ? fn(s) : s));
}

/** A step renamed, with the needs in its list that point at it. Expressions are not rewritten. */
export function renameAnywhere(steps: Step[], from: string, to: string): Step[] {
  const at = locate(steps, from);
  if (!at) return steps;
  const list = listAt(steps, at.list).map((s) => {
    const next = s.id === from ? { ...s, id: to } : s;
    return needsOf(next).includes(from) ? withNeeds(next, needsOf(next).map((n) => (n === from ? to : n))) : next;
  });
  return withList(steps, at.list, list);
}

/** "target needs source", in one list; null when it would loop or the two are in different lists. */
export function connectIn(steps: Step[], source: string, target: string): Step[] | null {
  const a = locate(steps, source);
  const b = locate(steps, target);
  if (!a || !b || a.list !== b.list || source === target) return null;
  const list = listAt(steps, a.list);
  const byId = new Map(list.map((s) => [s.id, s]));
  const stack = [source];
  const seen = new Set<string>();
  while (stack.length) {
    const id = stack.pop() as string;
    if (id === target) return null;
    if (seen.has(id)) continue;
    seen.add(id);
    stack.push(...needsOf(byId.get(id) ?? ({} as Step)));
  }
  return withList(steps, a.list, list.map((s) => (s.id === target ? withNeeds(s, [...needsOf(s), source]) : s)));
}

/** "target no longer needs source". */
export function disconnectIn(steps: Step[], source: string, target: string): Step[] {
  return updateAnywhere(steps, target, (s) => withNeeds(s, needsOf(s).filter((n) => n !== source)));
}

/** A new choice with a yes path and a no path, both empty. */
export function newChoice(id: string): Step {
  return { id, type: "branch", name: "Is it so?", config: { paths: [{ name: "yes", when: "=true", steps: [] }], default: { steps: [] } } } as Step;
}

// --- The picture ---

export const TRIGGER = "__trigger";
export const COLUMN = 300;
export const ROW = 118;

export type FlowNode =
  | { id: string; kind: "trigger"; position: XY }
  | { id: string; kind: "step"; position: XY; step: Step; list: ListRef; ports: { id: string; label: string; tone?: "yes" | "no" }[]; last: boolean }
  /** An empty path's place, where its first step is added. */
  | { id: string; kind: "empty"; position: XY; at: InsertAt; label?: string }
  /** After a choice, a for each or steps done together that nothing follows: where the next step goes. */
  | { id: string; kind: "then"; position: XY; at: InsertAt };

export interface FlowEdge {
  id: string;
  source: string;
  target: string;
  sourceHandle?: string;
  label?: string;
  tone?: "yes" | "no";
  /** Where the + on the arrow puts a new step. */
  at: InsertAt;
  /** The "needs" the arrow is, when deleting it means something. */
  need?: { source: string; target: string };
}

/** The canvas's cards and arrows for a definition, placed. */
export function flowGraph(def: Pick<WorkflowDefinition, "steps">, saved: Record<string, unknown> = {}): { nodes: FlowNode[]; edges: FlowEdge[] } {
  const nodes: FlowNode[] = [{ id: TRIGGER, kind: "trigger", position: { x: 0, y: 0 } }];
  const edges: FlowEdge[] = [];
  const edge = (e: Omit<FlowEdge, "id">) => edges.push({ ...e, id: `${e.source}:${e.sourceHandle ?? ""}->${e.target}` });

  // Walks a list from where it is entered; returns the cards it ends on.
  const walk = (list: ListRef, steps: Step[], entry: { node: string; handle?: string; label?: string; tone?: "yes" | "no" }, emptyLabel?: string): string[] => {
    if (steps.length === 0) {
      const id = `empty:${list}`;
      nodes.push({ id, kind: "empty", position: { x: 0, y: 0 }, at: { list }, label: emptyLabel });
      edge({ source: entry.node, sourceHandle: entry.handle, target: id, label: entry.label, tone: entry.tone, at: { list } });
      return [id];
    }
    const ids = new Set(steps.map((s) => s.id));
    const followed = new Set(steps.flatMap((s) => needsOf(s).filter((n) => ids.has(n))));
    const exits = new Map<string, string[]>();
    for (const s of steps) {
      const kids = childLists(s);
      nodes.push({ id: s.id, kind: "step", position: { x: 0, y: 0 }, step: s, list, ports: kids.map((k, i) => ({ id: `p${i}`, label: k.label, tone: k.tone })), last: !followed.has(s.id) && kids.length === 0 });
      if (kids.length === 0) exits.set(s.id, [s.id]);
      else exits.set(s.id, kids.flatMap((k, i) => walk(k.ref, k.steps, { node: s.id, handle: `p${i}`, label: k.label, tone: k.tone }, k.ref.endsWith("/default") ? "Nothing yet" : undefined)));
    }
    for (const s of steps) {
      const needs = needsOf(s).filter((n) => ids.has(n));
      if (needs.length === 0) edge({ source: entry.node, sourceHandle: entry.handle, target: s.id, label: entry.label, tone: entry.tone, at: { list, before: s.id } });
      for (const n of needs) for (const from of exits.get(n) ?? [n]) edge({ source: from, target: s.id, at: { list, after: n, before: s.id }, need: { source: n, target: s.id } });
    }
    // A container nothing follows gets a place for what comes after it.
    const tails: string[] = [];
    for (const s of steps) {
      if (followed.has(s.id)) continue;
      if (childLists(s).length === 0) {
        tails.push(s.id);
        continue;
      }
      const id = `then:${s.id}`;
      nodes.push({ id, kind: "then", position: { x: 0, y: 0 }, at: { list, after: s.id } });
      for (const from of exits.get(s.id) ?? []) edge({ source: from, target: id, at: { list, after: s.id } });
      tails.push(id);
    }
    return tails;
  };
  walk("", def.steps, { node: TRIGGER });
  placeNodes(nodes, edges, saved);
  return { nodes, edges };
}

/** Columns by the longest way in from what starts it; within a column,
 * each card near the cards it comes from (a choice's paths fanned out in
 * order), never closer than a row. Saved places win. */
function placeNodes(nodes: FlowNode[], edges: FlowEdge[], saved: Record<string, unknown>) {
  const ins = new Map<string, FlowEdge[]>();
  for (const e of edges) ins.set(e.target, [...(ins.get(e.target) ?? []), e]);
  const layer = new Map<string, number>([[TRIGGER, 0]]);
  const order = nodes.map((n) => n.id);
  // Longest path: relax in rounds (the graph is small and has no loops).
  for (let round = 0; round < nodes.length; round++) {
    let changed = false;
    for (const e of edges) {
      const l = (layer.get(e.source) ?? 0) + 1;
      if (l > (layer.get(e.target) ?? -1)) {
        layer.set(e.target, l);
        changed = true;
      }
    }
    if (!changed) break;
  }
  const byLayer = new Map<number, string[]>();
  for (const id of order) {
    const l = layer.get(id) ?? 0;
    byLayer.set(l, [...(byLayer.get(l) ?? []), id]);
  }
  const portsOf = new Map<string, number>();
  for (const n of nodes) if (n.kind === "step") portsOf.set(n.id, n.ports.length);
  const y = new Map<string, number>([[TRIGGER, 0]]);
  for (const l of [...byLayer.keys()].sort((a, b) => a - b)) {
    if (l === 0) continue;
    const want = (byLayer.get(l) ?? []).map((id, i) => {
      const from = ins.get(id) ?? [];
      const ys = from.map((e) => {
        const base = y.get(e.source) ?? 0;
        const ports = portsOf.get(e.source) ?? 0;
        if (!e.sourceHandle || ports < 2) return base;
        const p = Number(e.sourceHandle.slice(1));
        return base + (p - (ports - 1) / 2) * ROW;
      });
      return { id, i, y: ys.length ? ys.reduce((a, b) => a + b, 0) / ys.length : 0 };
    });
    want.sort((a, b) => a.y - b.y || a.i - b.i);
    // Spread apart, then keep the group centred on where it wanted to be.
    const placed: number[] = [];
    for (const w of want) placed.push(placed.length ? Math.max(w.y, (placed[placed.length - 1] as number) + ROW) : w.y);
    const shift = (want.reduce((a, w) => a + w.y, 0) - placed.reduce((a, p) => a + p, 0)) / want.length;
    want.forEach((w, i) => y.set(w.id, (placed[i] as number) + shift));
  }
  for (const n of nodes) {
    const s = saved[n.id];
    n.position = isXY(s) ? s : { x: (layer.get(n.id) ?? 0) * COLUMN, y: Math.round(y.get(n.id) ?? 0) };
  }
}

const isXY = (v: unknown): v is XY => !!v && typeof v === "object" && typeof (v as XY).x === "number" && typeof (v as XY).y === "number";

/** The canvas's notes, kept in the layout beside the cards' places. */
export interface Note {
  id: string;
  x: number;
  y: number;
  text: string;
}

export const NOTES_KEY = "__notes";

export function notesOf(layout: Record<string, unknown>): Note[] {
  const v = layout[NOTES_KEY];
  return Array.isArray(v) ? v.filter((n): n is Note => isXY(n) && typeof (n as Note).id === "string" && typeof (n as Note).text === "string") : [];
}

export function withNotes(layout: Record<string, unknown>, notes: Note[]): Record<string, unknown> {
  const rest = Object.fromEntries(Object.entries(layout).filter(([k]) => k !== NOTES_KEY));
  return notes.length ? { ...rest, [NOTES_KEY]: notes } : rest;
}

/** The places of cards nobody moved, cleared: "Tidy up". */
export function tidy(layout: Record<string, unknown>): Record<string, unknown> {
  return NOTES_KEY in layout ? { [NOTES_KEY]: layout[NOTES_KEY] } : {};
}
