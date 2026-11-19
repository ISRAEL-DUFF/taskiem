import { useRef, useState } from "react";
import { post } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { ErrorBox, Field, PageHeader, useAction } from "../ui";

// Start something (docs/spec/taskiem-for-everyone.md §18.1): a business, or
// a workflows workspace, made by the signed-in person with POST
// /v1/me/start; the session moves into it at once.

/** What a business says it does, in its own words (lib/areas.ts has the
 * parts each switches on). Workflows is its own choice above. */
type BusinessKind = "shop" | "service" | "delivery" | "school";
const BUSINESS_KINDS: { id: BusinessKind; label: string }[] = [
  { id: "shop", label: "I sell things" },
  { id: "service", label: "I bill customers for a service" },
  { id: "delivery", label: "I deliver for businesses" },
  { id: "school", label: "I run a school" },
];

/** Where a new start opens: Workflows for a workspace, Get started for a business. */
export function startLanding(kind: "business" | "workflows"): string {
  return kind === "workflows" ? "/workflows" : "/start";
}

export function StartSomething() {
  const { me } = useAuth();
  // ?what=business (from /signup?start=business): A business chosen.
  const [what, setWhat] = useState<"business" | "workflows" | "">(() => {
    const w = new URLSearchParams(window.location.search).get("what");
    return w === "business" || w === "workflows" ? w : "";
  });
  // Started: the button stays off until the new page has opened, so a
  // second click starts nothing more.
  const [done, setDone] = useState(false);
  // A click before the page has drawn the button off.
  const going = useRef(false);
  const [name, setName] = useState("");
  const [kind, setKind] = useState<BusinessKind | "">("");
  const act = useAction();
  const person = me?.user?.name?.trim();
  const choices: { id: "business" | "workflows"; title: string; text: string; icon: string }[] = [
    { id: "business", title: "A business", text: "Sell, take orders and bookings, send invoices, with your staff.", icon: "store" },
    {
      id: "workflows",
      title: "Workflows",
      text: "Automate work for yourself or a team: forms, reminders, approvals, your other apps.",
      icon: "workflows",
    },
  ];
  return (
    <>
      <PageHeader title="Start something" />
      <form
        className="card"
        onSubmit={(e) => {
          e.preventDefault();
          if (!what || going.current) return;
          going.current = true;
          void act.run(async () => {
            const body =
              what === "business"
                ? { kind: "business", name: name.trim(), kinds: [kind] }
                : { kind: "workflows", ...(name.trim() ? { name: name.trim() } : {}) };
            try {
              await post("/v1/me/start", body);
            } catch (err) {
              going.current = false;
              throw err;
            }
            setDone(true);
            // A fresh page in what was started: this session is in it now.
            window.location.assign(startLanding(what));
          }, false);
        }}
      >
        <fieldset className="start-choice">
          <legend>What do you want to start?</legend>
          {choices.map((c) => (
            <label key={c.id} className={what === c.id ? "start-option on" : "start-option"} data-testid={`start-${c.id}`}>
              <input type="radio" name="what" value={c.id} checked={what === c.id} onChange={() => (setWhat(c.id), setName(""))} required />
              <span className="list-icon" aria-hidden="true">
                <Icon name={c.icon} />
              </span>
              <span className="grow">
                <strong>{c.title}</strong>
                <span className="sub">{c.text}</span>
              </span>
            </label>
          ))}
        </fieldset>
        {what === "business" && (
          <>
            <Field label="Business name">
              <input value={name} onChange={(e) => setName(e.target.value)} required minLength={2} maxLength={80} autoComplete="organization" autoFocus />
            </Field>
            <fieldset className="start-kinds" data-testid="signup-kind">
              <legend>What does your business do?</legend>
              <p className="hint">This sets up what you see first. Workflows, forms and chat are always there, and you can change it any time.</p>
              {BUSINESS_KINDS.map((k) => (
                <label key={k.id} className="row">
                  <input type="radio" name="kind" value={k.id} checked={kind === k.id} onChange={() => setKind(k.id)} required />
                  <span>{k.label}</span>
                </label>
              ))}
            </fieldset>
          </>
        )}
        {what === "workflows" && (
          <Field label="Name (you can leave it empty)" hint={`Left empty, it's called “${person ? `${person}'s workflows` : "My workflows"}”. You can rename it later.`}>
            <input value={name} onChange={(e) => setName(e.target.value)} minLength={2} maxLength={80} autoFocus />
          </Field>
        )}
        <ErrorBox error={act.error} />
        {what && (
          <button className="primary" type="submit" disabled={act.busy || done}>
            {act.busy || done ? "Starting…" : what === "business" ? "Start the business" : "Start workflows"}
          </button>
        )}
      </form>
    </>
  );
}
