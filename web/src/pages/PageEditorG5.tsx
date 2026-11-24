import type { Part } from "../lib/pages";
import { comingFields, type ChoiceKind } from "../lib/pages_g5";
import { Field } from "../ui";

// The editor's choices for a part of saved things (G5): which kind, the
// date they come in the order of, and how near. A private field is never
// offered: the server refuses one, as its order would show it.

export function SavedSourceForm({ part, kinds, onChange }: { part: Part; kinds: ChoiceKind[]; onChange: (p: Part) => void }) {
  const src = part.source ?? {};
  const kind = kinds.find((k) => k.name.toLowerCase() === (src.saved ?? "").toLowerCase());
  const dates = comingFields(kind);
  const set = (p: Partial<Part>) => onChange({ ...part, ...p });
  const setSource = (s: Partial<NonNullable<Part["source"]>>) => {
    const next = { ...src, ...s };
    if (!next.coming) {
      delete next.coming;
      delete next.within;
    }
    if (!next.within) delete next.within;
    onChange({ ...part, source: next });
  };
  return (
    <div className="pg-part-form">
      {part.type !== "figure" ? (
        <Field label="Title">
          <input value={part.title ?? ""} maxLength={60} onChange={(e) => set({ title: e.target.value || undefined })} />
        </Field>
      ) : (
        <Field label="Label">
          <input value={part.label ?? ""} maxLength={40} onChange={(e) => set({ label: e.target.value })} />
        </Field>
      )}
      <Field label="Which saved things" hint="Your business's own, as everyone in it sees them: what is kept private stays hidden">
        <select value={kind?.name ?? src.saved ?? ""} onChange={(e) => setSource({ saved: e.target.value, coming: comingFields(kinds.find((k) => k.name === e.target.value))[0]?.key })}>
          {kinds.map((k) => (
            <option key={k.name} value={k.name}>
              {k.name}
            </option>
          ))}
        </select>
      </Field>
      <Field label="In the order a date comes" hint="Soonest first, the late ones at the top, each saying when">
        <select value={src.coming ?? ""} onChange={(e) => setSource({ coming: e.target.value || undefined })}>
          <option value="">By name</option>
          {dates.map((f) => (
            <option key={f.key} value={f.key}>
              {f.label}
            </option>
          ))}
        </select>
      </Field>
      {src.coming && (
        <Field label="Only those due within (days)" hint="Leave empty for all of them">
          <input
            type="number"
            min={1}
            max={366}
            value={src.within ?? ""}
            onChange={(e) => setSource({ within: e.target.value ? Math.min(366, Math.max(1, Number(e.target.value))) : undefined })}
          />
        </Field>
      )}
      {part.type !== "figure" && (
        <Field label="When there are none, say">
          <input value={part.empty ?? ""} maxLength={120} placeholder="Nothing to show" onChange={(e) => set({ empty: e.target.value || undefined })} />
        </Field>
      )}
    </div>
  );
}
