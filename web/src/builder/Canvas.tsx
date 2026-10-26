// The builder's canvas (docs/workflow-builder.md): what starts the
// workflow, then each step as a card, joined by arrows. A + on every arrow
// and after every last card adds a step there; a choice's paths are drawn
// with their Yes and No; after a test, each card says whether it ran.
import { memo, useEffect, useMemo, useRef, useState } from "react";
import {
  Background,
  BaseEdge,
  EdgeLabelRenderer,
  Handle,
  MiniMap,
  Position,
  ReactFlow,
  getSmoothStepPath,
  useReactFlow,
  useViewport,
  type Edge,
  type EdgeChange,
  type EdgeProps,
  type Node,
  type NodeChange,
  type NodeProps,
  type Connection,
} from "@xyflow/react";
import type { Step, WorkflowDefinition } from "@sdk/wd";
import { Icon } from "../icons";
import { TRIGGER, flowGraph, notesOf, type FlowEdge, type InsertAt, type Note, type XY } from "../lib/flow";
import { lookOf, stepTitle, type Tone } from "./look";

/** What a test with sample data said each step did. */
export interface TryResult {
  status: string;
  trigger?: unknown;
  steps: Record<string, string>;
  inputs: Record<string, unknown>;
  outputs: Record<string, unknown>;
  errors: Record<string, string>;
  run_error?: string;
}

type ConnectorLike = Parameters<typeof lookOf>[1];

type StepData = {
  step: Step;
  title: string;
  line: string;
  icon: string;
  tone: Tone;
  ports: { id: string; label: string; tone?: "yes" | "no" }[];
  last?: InsertAt;
  status?: string;
  invalid?: boolean;
  onAdd: (at: InsertAt, after?: string) => void;
  readOnly: boolean;
};
type TriggerData = { words: string; detail?: string; ran?: boolean };
type PlaceData = { at: InsertAt; label?: string; onAdd: (at: InsertAt) => void; readOnly: boolean; kind: "empty" | "then" };
type NoteData = { note: Note; onChange: (n: Note | null) => void; readOnly: boolean };
type EdgeData = { label?: string; tone?: "yes" | "no"; at: InsertAt; desc: string; onAdd: (at: InsertAt) => void; dim?: boolean; readOnly: boolean };

type StepNodeT = Node<StepData, "step">;
type TriggerNodeT = Node<TriggerData, "trigger">;
type PlaceNodeT = Node<PlaceData, "place">;
type NoteNodeT = Node<NoteData, "note">;
type AnyNode = StepNodeT | TriggerNodeT | PlaceNodeT | NoteNodeT;
type FlowEdgeT = Edge<EdgeData, "flow">;

const Tick = () => (
  <span className="ran-mark" role="img" aria-label="Ran">
    <Icon name="check" />
  </span>
);

const StepCard = memo(function StepCard({ data, selected }: NodeProps<StepNodeT>) {
  const ran = data.status === "completed";
  const notRun = data.status === "skipped" || data.status === "not run";
  return (
    <div
      className={`bcard${selected ? " selected" : ""}${data.invalid ? " invalid" : ""}${notRun ? " not-run" : ""}${data.status === "failed" ? " failed" : ""}`}
      data-testid={`node-${data.step.id}`}
      data-status={data.status}
    >
      <Handle type="target" position={Position.Left} />
      <span className={`bcard-icon tone-${data.tone}`} aria-hidden="true">
        <Icon name={data.icon} />
      </span>
      <span className="bcard-text">
        <span className="bcard-title">{data.title}</span>
        <span className="bcard-line">{data.line}</span>
      </span>
      {ran && <Tick />}
      {data.status === "failed" && (
        <span className="ran-mark failed" role="img" aria-label="Failed">
          <Icon name="close" />
        </span>
      )}
      {data.ports.length > 0 ? (
        data.ports.map((p, i) => (
          <Handle key={p.id} id={p.id} type="source" position={Position.Right} isConnectable={false} style={{ top: `${((i + 1) * 100) / (data.ports.length + 1)}%` }} />
        ))
      ) : (
        <Handle type="source" position={Position.Right} />
      )}
      {notRun && <span className="bcard-note">Did not need to run</span>}
      {data.last && !data.readOnly && (
        <button
          type="button"
          className="bcard-add nodrag"
          aria-label={`Add a step after ${data.title}`}
          onClick={(e) => {
            e.stopPropagation();
            data.onAdd(data.last as InsertAt, data.title);
          }}
        >
          <Icon name="plus" />
        </button>
      )}
    </div>
  );
});

const TriggerCard = memo(function TriggerCard({ data }: NodeProps<TriggerNodeT>) {
  return (
    <div className="bcard trigger" data-testid="node-trigger" title="Change when it runs">
      <span className="bcard-icon" aria-hidden="true">
        <Icon name="bolt" />
      </span>
      <span className="bcard-text">
        <span className="bcard-kicker">When</span>
        <span className="bcard-title">{data.words}</span>
        {data.detail && <span className="bcard-line">{data.detail}</span>}
      </span>
      {data.ran && <Tick />}
      <Handle type="source" position={Position.Right} isConnectable={false} />
    </div>
  );
});

const PlaceCard = memo(function PlaceCard({ data }: NodeProps<PlaceNodeT>) {
  return (
    <div className={`bplace is-${data.kind}`}>
      <Handle type="target" position={Position.Left} isConnectable={false} />
      {data.readOnly ? (
        <span className="bplace-label">{data.kind === "then" ? "End" : data.label ?? "Nothing yet"}</span>
      ) : (
        <button type="button" className="nodrag" onClick={() => data.onAdd(data.at)} aria-label={data.kind === "then" ? "Add a step after the paths" : data.at.list === "" ? undefined : "Add a step to this path"}>
          <Icon name="plus" />
          {data.kind === "empty" && <span>{data.at.list === "" ? "Add the first step" : "Add a step"}</span>}
        </button>
      )}
    </div>
  );
});

const NoteCard = memo(function NoteCard({ data }: NodeProps<NoteNodeT>) {
  return (
    <div className="bnote">
      <span className="bnote-head">
        Note
        {!data.readOnly && (
          <button type="button" className="nodrag" aria-label="Delete this note" onClick={() => data.onChange(null)}>
            <Icon name="close" />
          </button>
        )}
      </span>
      <textarea
        className="nodrag"
        aria-label="Note"
        rows={3}
        readOnly={data.readOnly}
        defaultValue={data.note.text}
        placeholder="Write a note for whoever works on this next"
        onBlur={(e) => e.target.value !== data.note.text && data.onChange({ ...data.note, text: e.target.value })}
      />
    </div>
  );
});

function FlowArrow({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, data, markerEnd, selected }: EdgeProps<FlowEdgeT>) {
  const [path, lx, ly] = getSmoothStepPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, borderRadius: 14, offset: 18 });
  const d = data as EdgeData;
  // A Yes or No sits near where the arrow leaves the choice.
  const labelX = d.label ? sourceX + 34 : lx;
  const labelY = d.label ? sourceY + (targetY > sourceY ? 14 : targetY < sourceY ? -14 : -12) : ly;
  // On a labelled arrow the + sits by its end, clear of the Yes or No.
  const addX = d.label ? targetX - 24 : lx;
  const addY = d.label ? targetY : ly;
  return (
    <>
      <BaseEdge id={id} path={path} markerEnd={markerEnd} className={`barrow${d.dim ? " dim" : ""}${selected ? " selected" : ""}`} />
      <EdgeLabelRenderer>
        {d.label && (
          <span className={`barrow-label ${d.tone ?? ""}`} style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}>
            {d.label}
          </span>
        )}
        {!d.readOnly && (
          <button
            type="button"
            className="barrow-add nodrag nopan"
            aria-label={`Add a step ${d.desc}`}
            style={{ transform: `translate(-50%, -50%) translate(${addX}px, ${addY}px)` }}
            onClick={() => d.onAdd(d.at)}
          >
            <Icon name="plus" />
          </button>
        )}
      </EdgeLabelRenderer>
    </>
  );
}

const nodeTypes = { step: StepCard, trigger: TriggerCard, place: PlaceCard, note: NoteCard };
const edgeTypes = { flow: FlowArrow };

export interface CanvasProps {
  def: WorkflowDefinition;
  layout: Record<string, unknown>;
  connectors: ConnectorLike;
  triggerWords: string;
  triggerDetail?: string;
  readOnly: boolean;
  invalid: Set<string>;
  result?: TryResult;
  selected: string | null;
  onSelect: (id: string | null) => void;
  onOpen: (id: string) => void;
  onOpenTrigger: () => void;
  onAdd: (at: InsertAt, after?: string) => void;
  onRemove: (id: string) => void;
  onConnect: (source: string, target: string) => void;
  onDisconnect: (source: string, target: string) => void;
  onLayout: (layout: Record<string, unknown>, done: boolean) => void;
  /** Bumped to fit the view (Tidy up, a step added). */
  fitKey: number;
}

/** The arrow's words for its + ("between X and Y", "at the start of Yes"). */
function arrowWords(e: FlowEdge, names: (id: string) => string): string {
  if (e.at.after && e.at.before) return `between ${names(e.at.after)} and ${names(e.at.before)}`;
  if (e.at.before) return `before ${names(e.at.before)}`;
  if (e.at.after) return `after ${names(e.at.after)}`;
  return "here";
}

export function Canvas(p: CanvasProps) {
  const names = (id: string) => {
    const s = p.def.steps.length ? findStep(p.def.steps, id) : undefined;
    return s ? stepTitle(s, p.connectors) : id;
  };
  const graph = useMemo(() => flowGraph(p.def, p.layout), [p.def, p.layout]);
  const tested = !!p.result;
  const status = (id: string) => (tested ? (p.result?.steps[id] ?? "not run") : undefined);

  // The sizes React Flow measured: the nodes are made afresh on each draw,
  // and the map and fitting the view need them.
  const [sizes, setSizes] = useState<Record<string, { width: number; height: number }>>({});
  const leaving = new Set(graph.edges.map((e) => e.source));
  const nodes: AnyNode[] = [];
  for (const n of graph.nodes) {
    if (n.kind === "trigger") {
      nodes.push({ id: TRIGGER, type: "trigger", position: n.position, draggable: !p.readOnly, deletable: false, data: { words: p.triggerWords, detail: p.triggerDetail, ran: tested } });
    } else if (n.kind === "step") {
      const look = lookOf(n.step, p.connectors);
      nodes.push({
        id: n.id,
        type: "step",
        position: n.position,
        selected: n.id === p.selected,
        draggable: !p.readOnly,
        deletable: !p.readOnly,
        data: {
          step: n.step,
          title: n.step.name || look.title,
          line: look.line,
          icon: look.icon,
          tone: look.tone,
          ports: n.ports,
          // An arrow already leaves a path's last card (to after the paths): its + is there.
          last: n.last && !leaving.has(n.id) ? { list: n.list, after: n.id } : undefined,
          status: status(n.id),
          invalid: p.invalid.has(n.id),
          onAdd: p.onAdd,
          readOnly: p.readOnly,
        },
      });
    } else {
      nodes.push({ id: n.id, type: "place", position: n.position, draggable: false, selectable: false, deletable: false, data: { at: n.at, label: n.kind === "empty" ? n.label : undefined, onAdd: p.onAdd, readOnly: p.readOnly, kind: n.kind } });
    }
  }
  for (const note of notesOf(p.layout)) {
    nodes.push({
      id: `note:${note.id}`,
      type: "note",
      position: { x: note.x, y: note.y },
      draggable: !p.readOnly,
      deletable: false,
      data: {
        note,
        readOnly: p.readOnly,
        onChange: (next) => {
          const rest = notesOf(p.layout).filter((x) => x.id !== note.id);
          p.onLayout({ ...p.layout, __notes: next ? [...rest, next] : rest }, true);
        },
      },
    });
  }
  const edges: FlowEdgeT[] = graph.edges.map((e) => ({
    id: e.id,
    type: "flow",
    source: e.source,
    target: e.target,
    sourceHandle: e.sourceHandle,
    deletable: !p.readOnly && !!e.need,
    data: { label: e.label, tone: e.tone, at: e.at, desc: arrowWords(e, names), onAdd: p.onAdd, readOnly: p.readOnly, dim: tested && (status(e.target) === "not run" || status(e.target) === "skipped") },
  }));

  const onNodesChange = (changes: NodeChange<AnyNode>[]) => {
    let layout = p.layout;
    let moved = false;
    let done = false;
    for (const c of changes) {
      if (c.type === "position" && c.position) {
        moved = true;
        done = done || !c.dragging;
        if (c.id.startsWith("note:")) {
          const nid = c.id.slice(5);
          const at = c.position;
          layout = { ...layout, __notes: notesOf(layout).map((n) => (n.id === nid ? { ...n, x: at.x, y: at.y } : n)) };
        } else layout = { ...layout, [c.id]: c.position as XY };
      } else if (c.type === "select") {
        if (c.selected && !c.id.startsWith("note:") && c.id !== TRIGGER) p.onSelect(c.id);
      } else if (c.type === "remove" && !c.id.startsWith("note:") && c.id !== TRIGGER) {
        p.onRemove(c.id);
      }
    }
    if (moved) p.onLayout(layout, done);
    const measured = changes.flatMap((c) => (c.type === "dimensions" && c.dimensions ? [[c.id, c.dimensions] as const] : []));
    if (measured.length) setSizes((was) => ({ ...was, ...Object.fromEntries(measured) }));
  };
  const onEdgesChange = (changes: EdgeChange<FlowEdgeT>[]) => {
    for (const c of changes) {
      if (c.type !== "remove") continue;
      const e = graph.edges.find((x) => x.id === c.id);
      if (e?.need) p.onDisconnect(e.need.source, e.need.target);
    }
  };
  return (
    <ReactFlow
      nodes={nodes.map((n) => (sizes[n.id] ? { ...n, measured: sizes[n.id] } : n))}
      edges={edges}
      nodeTypes={nodeTypes}
      edgeTypes={edgeTypes}
      onNodesChange={onNodesChange}
      onEdgesChange={onEdgesChange}
      onConnect={(c: Connection) => c.source && c.target && p.onConnect(c.source, c.target)}
      onNodeClick={(_, n) => (n.id === TRIGGER ? p.onOpenTrigger() : n.type === "step" ? p.onOpen(n.id) : undefined)}
      onPaneClick={() => p.onSelect(null)}
      deleteKeyCode={p.readOnly ? null : ["Delete", "Backspace"]}
      fitView
      fitViewOptions={{ maxZoom: 1, padding: 0.2 }}
      minZoom={0.25}
      proOptions={{ hideAttribution: true }}
      nodesConnectable={!p.readOnly}
    >
      <Background gap={22} size={1.4} color="var(--border-strong)" />
      <Controls readOnly={p.readOnly} onTidy={() => p.onLayout(tidyLayout(p.layout), true)} onNote={(xy) => p.onLayout({ ...p.layout, __notes: [...notesOf(p.layout), { id: noteId(), x: xy.x, y: xy.y, text: "" }] }, true)} />
      <MiniMap className="bminimap" pannable zoomable nodeColor={(n) => (n.type === "trigger" ? "var(--brand)" : n.type === "note" ? "var(--tint-warn)" : "var(--border-strong)")} ariaLabel="Map of the workflow" />
      <FitOn fitKey={p.fitKey} />
    </ReactFlow>
  );
}

const noteId = () => Math.random().toString(36).slice(2, 10);
const tidyLayout = (layout: Record<string, unknown>) => ("__notes" in layout ? { __notes: layout.__notes } : {});

function findStep(steps: Step[], id: string): Step | undefined {
  for (const s of steps) {
    if (s.id === id) return s;
    const c = (s as { config?: { paths?: { steps: Step[] }[]; default?: { steps: Step[] }; branches?: { steps: Step[] }[]; steps?: Step[] } }).config;
    for (const list of [...(c?.paths ?? []).map((x) => x.steps), c?.default?.steps ?? [], ...(c?.branches ?? []).map((x) => x.steps), c?.steps ?? []]) {
      const f = findStep(list ?? [], id);
      if (f) return f;
    }
  }
  return undefined;
}

/** Zoom, fit, tidy up and add a note, at the canvas's bottom left. */
function Controls({ readOnly, onTidy, onNote }: { readOnly: boolean; onTidy: () => void; onNote: (xy: XY) => void }) {
  const rf = useReactFlow();
  const { zoom } = useViewport();
  return (
    <div className="bcontrols" role="toolbar" aria-label="Canvas">
      <button type="button" aria-label="Zoom out" onClick={() => void rf.zoomOut({ duration: 150 })}>
        <Icon name="minus" />
      </button>
      <span className="bzoom">{Math.round(zoom * 100)}%</span>
      <button type="button" aria-label="Zoom in" onClick={() => void rf.zoomIn({ duration: 150 })}>
        <Icon name="plus" />
      </button>
      <button type="button" aria-label="Fit to screen" onClick={() => void rf.fitView({ maxZoom: 1, padding: 0.2, duration: 200 })}>
        <Icon name="fit" />
      </button>
      {!readOnly && (
        <>
          <span className="bcontrols-sep" aria-hidden="true" />
          <button
            type="button"
            className="wide"
            onClick={() => {
              onTidy();
              setTimeout(() => void rf.fitView({ maxZoom: 1, padding: 0.2, duration: 200 }), 30);
            }}
          >
            <Icon name="tidy" />
            Tidy up
          </button>
          <button
            type="button"
            className="wide"
            onClick={() => {
              const box = document.querySelector(".react-flow")?.getBoundingClientRect();
              const xy = rf.screenToFlowPosition({ x: (box?.left ?? 0) + (box?.width ?? 600) / 2 - 110, y: (box?.top ?? 0) + 80 });
              onNote(xy);
            }}
          >
            <Icon name="note" />
            Add a note
          </button>
        </>
      )}
    </div>
  );
}

/** Fits the view when fitKey changes (not on the first draw: fitView does that). */
function FitOn({ fitKey }: { fitKey: number }) {
  const rf = useReactFlow();
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    const t = setTimeout(() => void rf.fitView({ maxZoom: 1, padding: 0.2, duration: 200 }), 40);
    return () => clearTimeout(t);
  }, [fitKey, rf]);
  return null;
}
