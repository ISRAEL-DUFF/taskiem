import { Handle, Position, type NodeProps, type Node } from "@xyflow/react";
import type { Step } from "@sdk/wd";

export type StepNodeData = { step: Step; invalid: boolean; status?: string };
export type StepFlowNode = Node<StepNodeData, "step">;

export function summary(s: Step): string {
  switch (s.type) {
    case "connector":
      return `${s.connector} · ${s.action}`;
    case "http":
      return `${s.config.method} ${s.config.url}`;
    case "code":
      return s.config.language;
    case "wait":
      return "duration" in s.config ? `wait ${s.config.duration}` : `until ${s.config.until}`;
    case "signal":
      return s.config.event;
    case "approval":
      return [s.config.role && `role ${s.config.role}`, s.config.count && s.config.count > 1 && `${s.config.count} approvers`].filter(Boolean).join(", ") || "approval";
    case "foreach":
      return `each of ${s.config.items} · ${s.config.steps.length} steps`;
    case "branch":
      return `${s.config.paths.length} paths${s.config.default ? " + default" : ""}`;
    case "parallel":
      return `${s.config.branches.length} branches · join ${s.config.join ?? "all"}`;
    default:
      return s.when ? `when ${s.when}` : "";
  }
}

export function StepNode({ data, selected }: NodeProps<StepFlowNode>) {
  const s = data.step;
  return (
    <div className={`step-node${selected ? " selected" : ""}${data.invalid ? " invalid" : ""}${data.status ? ` status-${data.status}` : ""}`} data-testid={`node-${s.id}`} data-status={data.status}>
      <Handle type="target" position={Position.Left} />
      <div className="type">
        {s.type}
        {data.status && <span className={`badge ${data.status}`} style={{ marginLeft: 6 }}>{data.status}</span>}
      </div>
      <div className="title">{s.name || s.id}</div>
      <div className="sub">{summary(s)}</div>
      {s.when && s.type !== "transform" && <div className="sub">when {s.when}</div>}
      <Handle type="source" position={Position.Right} />
    </div>
  );
}
