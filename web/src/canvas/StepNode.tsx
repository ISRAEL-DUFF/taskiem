import { Handle, Position, type NodeProps, type Node } from "@xyflow/react";
import type { Step } from "@sdk/wd";
import { Icon } from "../icons";

export type StepNodeData = { step: Step; invalid: boolean; status?: string };
export type StepFlowNode = Node<StepNodeData, "step">;
/** What starts the workflow, drawn before its first steps; not a step. */
export type TriggerFlowNode = Node<{ words: string; type: string }, "trigger">;
export const TRIGGER_NODE = "__trigger";

export function summary(s: Step): string {
  switch (s.type) {
    case "connector":
      return `${words(s.connector.split("@")[0] ?? "")} · ${words(s.action)}`;
    case "http":
      return `${s.config.method} ${s.config.url}`;
    case "code":
      return s.config.language;
    case "container":
      return `${s.config.image.split("@")[0]?.split("/").pop() ?? "container"} · ${s.config.network === "egress" ? "egress" : "no network"}`;
    case "wait":
      return "duration" in s.config ? `wait ${s.config.duration}` : `until ${s.config.until}`;
    case "signal":
      return s.config.event;
    case "approval":
      return [s.config.role && `role ${s.config.role}`, s.config.count && s.config.count > 1 && `${s.config.count} approvers`].filter(Boolean).join(", ") || "approval";
    case "form": {
      const to = s.config.to;
      return `${s.config.title} · ${"role" in to ? `role ${to.role}` : "phone" in to ? to.phone || "phone" : to.email || "email"}`;
    }
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

/** "send_sms" as "Send sms", "paystack" as "Paystack". */
const words = (id: string) => (id.charAt(0).toUpperCase() + id.slice(1)).replaceAll("_", " ");

/** Each kind of step in plain words, and its icon. */
const KINDS: Record<string, [string, string]> = {
  connector: ["Service", "connections"],
  approval: ["Approval", "approvals"],
  form: ["Form", "forms"],
  wait: ["Wait", "runs"],
  signal: ["Wait for a reply", "inbox"],
  transform: ["Prepare", "templates"],
  branch: ["Choice", "workflows"],
  parallel: ["At the same time", "workflows"],
  foreach: ["For each", "tables"],
  http: ["Web request", "catalogue"],
  code: ["Code", "audit"],
  container: ["Container", "catalogue"],
  subflow: ["Workflow", "workflows"],
};

export function StepNode({ data, selected }: NodeProps<StepFlowNode>) {
  const s = data.step;
  const [kind, icon] = KINDS[s.type] ?? [words(s.type), "workflows"];
  return (
    <div className={`step-node${selected ? " selected" : ""}${data.invalid ? " invalid" : ""}${data.status ? ` status-${data.status}` : ""}`} data-testid={`node-${s.id}`} data-status={data.status}>
      <Handle type="target" position={Position.Left} />
      <span className="step-icon" aria-hidden="true">
        <Icon name={icon} />
      </span>
      <div className="step-text">
        <div className="type">
          {kind}
          {data.status && (
            <span className={`badge ${data.status}`} style={{ marginLeft: 6, height: 18, fontSize: 11 }}>
              {data.status}
            </span>
          )}
        </div>
        <div className="title">{s.name || s.id}</div>
        <div className="sub">{summary(s)}</div>
        {s.when && s.type !== "transform" && <div className="sub">when {s.when}</div>}
      </div>
      <Handle type="source" position={Position.Right} />
    </div>
  );
}

export function TriggerNode({ data }: NodeProps<TriggerFlowNode>) {
  return (
    <div className="step-node trigger-node" data-testid="node-trigger" title="Change when it runs">
      <span className="step-icon" aria-hidden="true">
        <Icon name={data.type === "schedule" ? "clock" : "bolt"} />
      </span>
      <div className="step-text">
        <div className="type">When</div>
        <div className="title">{data.words}</div>
      </div>
      <Handle type="source" position={Position.Right} isConnectable={false} />
    </div>
  );
}
