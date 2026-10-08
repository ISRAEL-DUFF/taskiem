// Converts between a workflow definition and the canvas graph. Only
// top-level steps are nodes; steps nested in branch, parallel, foreach, or on_error
// are edited inside their parent step's panel.
import type { Step, WorkflowDefinition } from "@sdk/wd";

export interface XY {
  x: number;
  y: number;
}
export type Layout = Record<string, XY>;

export interface GraphNode {
  id: string;
  position: XY;
  step: Step;
}
export interface GraphEdge {
  id: string;
  source: string;
  target: string;
}

export const COLUMN = 280;
export const ROW = 130;

/** Depth of each step: 0 for roots, 1 + the deepest need otherwise. */
export function depths(steps: Step[]): Map<string, number> {
  const byId = new Map(steps.map((s) => [s.id, s]));
  const out = new Map<string, number>();
  const visit = (id: string, seen: Set<string>): number => {
    const known = out.get(id);
    if (known !== undefined) return known;
    if (seen.has(id)) return 0; // a cycle: the validator reports it
    seen.add(id);
    const s = byId.get(id);
    let d = 0;
    for (const n of s?.needs ?? []) if (byId.has(n)) d = Math.max(d, visit(n, seen) + 1);
    out.set(id, d);
    return d;
  };
  for (const s of steps) visit(s.id, new Set());
  return out;
}

/** Positions for steps without saved ones: one column per depth. */
export function autoLayout(steps: Step[], saved: Layout = {}): Layout {
  const d = depths(steps);
  const rows = new Map<number, number>();
  const out: Layout = {};
  for (const s of steps) {
    const col = d.get(s.id) ?? 0;
    const row = rows.get(col) ?? 0;
    rows.set(col, row + 1);
    out[s.id] = saved[s.id] ?? { x: col * COLUMN, y: row * ROW };
  }
  return out;
}

export function toGraph(def: Pick<WorkflowDefinition, "steps">, saved: Layout = {}): { nodes: GraphNode[]; edges: GraphEdge[] } {
  const pos = autoLayout(def.steps, saved);
  const ids = new Set(def.steps.map((s) => s.id));
  const nodes = def.steps.map((s) => ({ id: s.id, position: pos[s.id] ?? { x: 0, y: 0 }, step: s }));
  const edges: GraphEdge[] = [];
  for (const s of def.steps) {
    for (const n of s.needs ?? []) {
      if (ids.has(n)) edges.push({ id: `${n}->${s.id}`, source: n, target: s.id });
    }
  }
  return { nodes, edges };
}

/** Adds "target needs source"; refuses self-edges and edges that would make a cycle. */
export function connect(steps: Step[], source: string, target: string): Step[] | null {
  if (source === target) return null;
  // A cycle would appear if source already (transitively) needs target.
  const byId = new Map(steps.map((s) => [s.id, s]));
  const stack = [source];
  const seen = new Set<string>();
  while (stack.length) {
    const id = stack.pop() as string;
    if (id === target) return null;
    if (seen.has(id)) continue;
    seen.add(id);
    for (const n of byId.get(id)?.needs ?? []) stack.push(n);
  }
  return steps.map((s) => (s.id === target && !(s.needs ?? []).includes(source) ? { ...s, needs: [...(s.needs ?? []), source] } : s));
}

export function disconnect(steps: Step[], source: string, target: string): Step[] {
  return steps.map((s) => {
    if (s.id !== target) return s;
    const needs = (s.needs ?? []).filter((n) => n !== source);
    const next = { ...s, needs };
    if (needs.length === 0) delete (next as { needs?: string[] }).needs;
    return next;
  });
}

/** Removes a step and every reference to it in needs. */
export function removeStep(steps: Step[], id: string): Step[] {
  return steps.filter((s) => s.id !== id).map((s) => (s.needs?.includes(id) ? disconnect([s], id, s.id)[0] as Step : s));
}

/** Renames a step and the needs that point at it. Expressions are not rewritten. */
export function renameStep(steps: Step[], from: string, to: string): Step[] {
  return steps.map((s) => {
    const next = s.id === from ? { ...s, id: to } : s;
    return next.needs?.includes(from) ? { ...next, needs: next.needs.map((n) => (n === from ? to : n)) } : next;
  });
}

/** A step id not yet used: base, base_2, base_3 ... */
export function freshId(steps: Step[], base: string): string {
  const used = new Set<string>();
  const walk = (list: Step[]) => {
    for (const s of list) {
      used.add(s.id);
      const c = (s as { config?: { steps?: Step[]; paths?: { steps: Step[] }[]; branches?: { steps: Step[] }[]; default?: { steps: Step[] } } }).config;
      if (c?.steps) walk(c.steps);
      for (const p of c?.paths ?? []) walk(p.steps);
      for (const b of c?.branches ?? []) walk(b.steps);
      if (c?.default) walk(c.default.steps);
      if (s.on_error) walk(s.on_error.steps);
    }
  };
  walk(steps);
  if (!used.has(base)) return base;
  for (let i = 2; ; i++) if (!used.has(`${base}_${i}`)) return `${base}_${i}`;
}

/** A new step of a type, with the smallest valid configuration. */
export function newStep(type: Step["type"], id: string): Step {
  switch (type) {
    case "connector":
      return { id, type, connector: "paystack@1", action: "check_balance" };
    case "http":
      return { id, type, config: { method: "GET", url: "https://example.com" } };
    case "code":
      return { id, type, config: { language: "typescript", source: "export default (input: Record<string, unknown>) => {\n  return { ok: true };\n};\n" } };
    case "transform":
      return { id, type, config: { output: { value: "=trigger.body" } } };
    case "wait":
      return { id, type, config: { duration: "1h" } };
    case "signal":
      return { id, type, config: { event: "paystack@1:transfer_event", correlation: "=trigger.body.reference", timeout: "24h" } };
    case "approval":
      return { id, type, config: { role: "approver", timeout: "24h", on_timeout: "reject" } };
    case "branch":
      return { id, type, config: { paths: [{ name: "yes", when: "=true", steps: [] }] } };
    case "foreach":
      return { id, type, config: { items: "=trigger.body.items", max_concurrency: 5, steps: [] } };
    case "parallel":
      return { id, type, config: { join: "all", branches: [{ name: "a", steps: [] }, { name: "b", steps: [] }] } };
    default:
      return { id, type: "transform", config: { output: null } };
  }
}
