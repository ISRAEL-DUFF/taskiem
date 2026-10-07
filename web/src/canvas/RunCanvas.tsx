import { Background, Controls, ReactFlow } from "@xyflow/react";
import type { WorkflowDefinition } from "@sdk/wd";
import { useMemo } from "react";
import { get } from "../api";
import { toGraph, type Layout } from "../lib/graph";
import { topLevelStatus, type StepRow } from "../lib/timeline";
import { ErrorBox, useLoad } from "../ui";
import { StepNode, type StepFlowNode } from "./StepNode";

const nodeTypes = { step: StepNode };

/** The workflow's canvas with each step lit by its state in a run. */
export function RunCanvas({
  workflow,
  version,
  rows,
  ended,
  onSelect,
  load = (wf, v) => get(`/v1/workflows/${wf}/versions/${v}`),
}: {
  workflow: string;
  version: number;
  rows: StepRow[];
  ended: boolean;
  onSelect?: (step: string) => void;
  /** Loads the version (the embedded builder reads it through the embed API). */
  load?: (workflow: string, version: number) => Promise<{ definition: WorkflowDefinition; layout?: Layout }>;
}) {
  const doc = useLoad(() => load(workflow, version), [workflow, version]);
  const graph = useMemo(() => (doc.data ? toGraph(doc.data.definition, doc.data.layout ?? {}) : { nodes: [], edges: [] }), [doc.data]);
  const status = topLevelStatus(
    rows,
    graph.nodes.map((n) => n.id),
  );
  // Once the run has ended, a step still open never finished.
  if (ended) for (const [id, st] of Object.entries(status)) if (["scheduled", "running", "retrying", "waiting"].includes(st)) status[id] = "cancelled";
  const nodes: StepFlowNode[] = graph.nodes.map((n) => ({ id: n.id, type: "step", position: n.position, data: { step: n.step, invalid: false, status: status[n.id] }, draggable: false }));
  const edges = graph.edges.map((e) => ({ ...e, animated: status[e.source] === "completed" && ["running", "retrying", "waiting", "scheduled"].includes(status[e.target] ?? "") }));
  if (doc.error) return <ErrorBox error={doc.error} />;
  return (
    <div className="canvas run-canvas" aria-label="Run canvas">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        nodesConnectable={false}
        nodesDraggable={false}
        elementsSelectable={!!onSelect}
        onNodeClick={(_, n) => onSelect?.(n.id)}
        fitView
        fitViewOptions={{ maxZoom: 1 }}
        proOptions={{ hideAttribution: true }}
      >
        <Background />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  );
}
