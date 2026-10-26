// A new workflow, for everyone (docs/workflow-builder.md): name it, choose
// what starts it, and the builder opens with nothing else in it yet. Or
// start from a template, or describe it and the AI drafts it.
import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { post } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { workflowID } from "../lib/aitables";
import { ErrorBox, Field, useAction } from "../ui";
import { AIBuilder } from "./AIBuild";
import { TRIGGER_CHOICES, blankWorkflow } from "../builder/triggers";

export function NewWorkflow() {
  const nav = useNavigate();
  const { can } = useAuth();
  const [name, setName] = useState("");
  const [ai, setAI] = useState(false);
  const act = useAction();
  if (!can("workflow.edit")) return <p className="hint">You can see workflows, but not make them. Ask an owner or a builder for the access.</p>;
  const start = (i: number) =>
    void act.run(async () => {
      const choice = TRIGGER_CHOICES[i];
      if (!choice) return;
      const title = name.trim() || choice.title;
      const r = await post<{ id: string }>("/v1/workflows", { name: title, definition: blankWorkflow(workflowID(title), title, choice) });
      nav(`/workflows/${r.id}`);
    }, false);
  return (
    <div className="new-workflow">
      <Link to="/workflows" className="back-link">
        <Icon name="back" /> Workflows
      </Link>
      <h1>When should it run?</h1>
      <p className="hint">Choose what starts it. Then add what it does, one step at a time.</p>
      <Field label="Its name" hint="You can change it later.">
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Thank customers who pay" maxLength={120} />
      </Field>
      <ErrorBox error={act.error} />
      <ul className="trigger-cards" aria-label="What starts it">
        {TRIGGER_CHOICES.map((t, i) => (
          <li key={t.type}>
            <button type="button" className="trigger-card" onClick={() => start(i)} disabled={act.busy} data-testid={`start-${t.type}`}>
              <span className="pick-icon tone-brand" aria-hidden="true">
                <Icon name={t.icon} />
              </span>
              <span className="pick-text">
                <span className="pick-title">{t.title}</span>
                <span className="pick-sub">{t.sub}</span>
              </span>
            </button>
          </li>
        ))}
      </ul>
      <h2>Or start another way</h2>
      <div className="other-starts">
        <Link to="/templates" className="trigger-card">
          <span className="pick-icon tone-info" aria-hidden="true">
            <Icon name="templates" />
          </span>
          <span className="pick-text">
            <span className="pick-title">From a template</span>
            <span className="pick-sub">Ready-made workflows for Nigerian businesses</span>
          </span>
        </Link>
        <button type="button" className="trigger-card" onClick={() => setAI(true)}>
          <span className="pick-icon tone-ok" aria-hidden="true">
            <Icon name="sparkle" />
          </span>
          <span className="pick-text">
            <span className="pick-title">Describe it</span>
            <span className="pick-sub">Say what it should do and the AI drafts it for you</span>
          </span>
        </button>
      </div>
      {ai && <AIBuilder onClose={() => setAI(false)} onSaved={(id, v) => nav(`/workflows/${id}?v=${v}`)} />}
    </div>
  );
}
