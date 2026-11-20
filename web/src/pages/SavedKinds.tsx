import { useState } from "react";
import { ApiError, post, put } from "../api";
import { Icon } from "../icons";
import { ErrorBox, Field, Modal, useAction } from "../ui";
import {
  FIELD_TYPES,
  cleanName,
  kindProblem,
  nameProblem,
  savedBase,
  savedKindsBase,
  valuesProblem,
  type FieldType,
  type KindField,
  type KindList,
  type MadeKind,
  type SavedThing,
} from "../lib/saved";

// Kinds the owner makes (saved objects S1, part 5; saved-objects §4.2):
// the sheet that makes or changes one (its name and fields, or a starter
// to copy), and the sheet that saves a thing of one (a field for each,
// sensitive ones masked and kept unless typed again).

/** Make a kind, or change one: its name and fields. */
export function KindSheet({
  mine,
  kind,
  starters,
  onClose,
  onSaved,
}: {
  mine: boolean;
  kind?: MadeKind;
  starters: KindList["starters"];
  onClose: () => void;
  onSaved: (k: MadeKind) => void;
}) {
  const [name, setName] = useState(kind?.name ?? "");
  const [fields, setFields] = useState<KindField[]>(() => kind?.fields.map((f) => ({ ...f })) ?? [{ label: "", type: "text" }]);
  const [problem, setProblem] = useState<string>();
  const act = useAction();
  const set = (i: number, patch: Partial<KindField>) => setFields((cur) => cur.map((f, j) => (j === i ? { ...f, ...patch } : f)));
  const move = (i: number, by: number) =>
    setFields((cur) => {
      const next = [...cur];
      const [f] = next.splice(i, 1);
      if (f) next.splice(i + by, 0, f);
      return next;
    });
  const save = () => {
    const clean = fields.map((f) => ({ ...f, label: f.label.trim(), options: f.type === "choice" ? (f.options ?? []).map((o) => o.trim()).filter(Boolean) : undefined }));
    const p = kindProblem(name, clean);
    setProblem(p ?? undefined);
    if (p) return;
    void act.run(async () => {
      const base = savedKindsBase(mine);
      const body = { name: name.trim(), fields: clean };
      const r = kind ? await put<{ kind: MadeKind }>(`${base}/${encodeURIComponent(kind.id)}`, body) : await post<{ kind: MadeKind }>(base, body);
      onSaved(r.kind);
    }, kind ? "Kind changed" : `${name.trim()} made`);
  };
  const taken = act.error instanceof ApiError && act.error.body.name_taken ? "You have a kind by that name: choose another." : undefined;
  return (
    <Modal title={kind ? `Change ${kind.name}` : "Make a kind"} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          save();
        }}
        data-testid="kind-form"
      >
        {!kind && starters.length > 0 && (
          <div className="field">
            <span className="label" id="kind-starters">
              Start from
            </span>
            <div className="row-actions" role="group" aria-labelledby="kind-starters">
              {starters.map((st) => (
                <button
                  key={st.name}
                  type="button"
                  className="small"
                  data-testid="kind-starter"
                  onClick={() => {
                    setName(st.name);
                    setFields(st.fields.map((f) => ({ ...f, key: undefined })));
                  }}
                >
                  {st.name}
                </button>
              ))}
            </div>
          </div>
        )}
        <Field label="Name of the kind" hint="Such as Car, Generator or Tenant.">
          <input value={name} maxLength={40} onChange={(e) => setName(e.target.value)} data-testid="kind-name" />
        </Field>
        <ol className="kind-fields" aria-label="Its fields">
          {fields.map((f, i) => (
            <li key={i} className="kind-field" data-testid="kind-field">
              <div className="kind-field-top">
                <Field label={`Field ${i + 1}`}>
                  <input value={f.label} maxLength={60} placeholder="Such as Plate number" onChange={(e) => set(i, { label: e.target.value })} />
                </Field>
                <Field label="What it is">
                  <select value={f.type} onChange={(e) => set(i, { type: e.target.value as FieldType })}>
                    {FIELD_TYPES.map((t) => (
                      <option key={t.value} value={t.value}>
                        {t.label}
                      </option>
                    ))}
                  </select>
                </Field>
              </div>
              {f.type === "choice" && (
                <Field label="Its choices" hint="One on each line.">
                  <textarea rows={3} value={(f.options ?? []).join("\n")} onChange={(e) => set(i, { options: e.target.value.split("\n") })} />
                </Field>
              )}
              <div className="kind-field-flags">
                <label>
                  <input type="checkbox" checked={!!f.required} onChange={(e) => set(i, { required: e.target.checked })} /> Required
                </label>
                <label>
                  <input type="checkbox" checked={!!f.sensitive || !!f.never_share} disabled={!!f.never_share} onChange={(e) => set(i, { sensitive: e.target.checked })} /> Sensitive
                </label>
                <label>
                  <input type="checkbox" checked={!!f.never_share} onChange={(e) => set(i, { never_share: e.target.checked })} /> Never share
                </label>
              </div>
              <div className="row-actions">
                {i > 0 && (
                  <button type="button" className="small" onClick={() => move(i, -1)}>
                    Up<span className="sr-only"> {f.label || `field ${i + 1}`}</span>
                  </button>
                )}
                {fields.length > 1 && (
                  <button type="button" className="small danger" onClick={() => setFields((cur) => cur.filter((_, j) => j !== i))}>
                    Remove<span className="sr-only"> {f.label || `field ${i + 1}`}</span>
                  </button>
                )}
              </div>
            </li>
          ))}
        </ol>
        {fields.length < 30 && (
          <button type="button" className="small" onClick={() => setFields((cur) => [...cur, { label: "", type: "text" }])} data-testid="kind-add-field">
            <Icon name="plus" /> Add a field
          </button>
        )}
        <p className="hint">Sensitive fields are kept sealed and shown in full only with your passkey. Never share keeps a field out of anything you share.</p>
        {(problem || taken) && (
          <p className="error" role="alert">
            {problem ?? taken}
          </p>
        )}
        {!taken && <ErrorBox error={act.error} />}
        <div className="row-actions">
          <button type="submit" className="primary" disabled={act.busy} data-testid="kind-save">
            {act.busy ? "Saving…" : kind ? "Save" : "Make it"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** Save a thing of a made-up kind, or change one. */
export function CustomSheet({
  mine,
  kind,
  thing,
  startName,
  onClose,
  onSaved,
}: {
  mine: boolean;
  kind: MadeKind;
  thing?: SavedThing;
  /** A name to begin with (Save "@car"… in the picker). */
  startName?: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const base = savedBase(mine);
  const had = thing?.values ?? {};
  const [name, setName] = useState(thing ? `@${thing.name}` : startName ? `@${startName}` : "");
  const [title, setTitle] = useState(thing?.title ?? "");
  // Sensitive values start empty: typed again only to change them.
  const [v, setV] = useState<Record<string, string>>(() =>
    Object.fromEntries(kind.fields.map((f) => [f.key ?? "", f.sensitive ? "" : (had[f.key ?? ""] ?? "")])),
  );
  const [note, setNote] = useState(thing?.details.note ?? "");
  const [problem, setProblem] = useState<string>();
  const act = useAction();
  const save = () => {
    const p = nameProblem(name) ?? (title.trim() ? null : "Give it a title, like My car.") ?? valuesProblem(kind.fields, v, thing ? had : {});
    setProblem(p ?? undefined);
    if (p) return;
    const values: Record<string, string | null> = {};
    for (const f of kind.fields) {
      const key = f.key ?? "";
      const val = (v[key] ?? "").trim();
      if (val) values[key] = val;
      else if (thing && !f.sensitive && had[key]) values[key] = null;
    }
    void act.run(
      async () => {
        const body = { name: cleanName(name), title: title.trim(), values, note: note.trim() };
        if (thing) await put(`${base}/${encodeURIComponent(thing.id)}`, body);
        else await post(base, { ...body, kind: "custom", kind_id: kind.id });
        onSaved();
      },
      thing ? "Saved" : `Saved as @${cleanName(name)}`,
    );
  };
  const taken = act.error instanceof ApiError && act.error.body.name_taken ? "That name is taken: choose another." : undefined;
  return (
    <Modal title={thing ? `Change @${thing.name}` : `Save a ${kind.name.toLowerCase()}`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          save();
        }}
        data-testid="saved-custom-form"
      >
        <Field label="Name to call it up by" hint="Such as @car or @generator.">
          <input value={name} maxLength={33} autoCapitalize="none" autoComplete="off" onChange={(e) => setName(e.target.value)} required />
        </Field>
        <Field label="Title" hint="How it is shown.">
          <input value={title} maxLength={80} onChange={(e) => setTitle(e.target.value)} required />
        </Field>
        {kind.fields.map((f) => {
          const key = f.key ?? "";
          const kept = f.sensitive && had[key];
          const hint = [f.required ? "" : "Optional.", kept ? `Kept as ${had[key]}: type it only to change it.` : f.sensitive ? "Kept sealed." : ""].filter(Boolean).join(" ");
          const change = (val: string) => setV((cur) => ({ ...cur, [key]: val }));
          return (
            <Field key={key} label={f.label} hint={hint || undefined}>
              {f.type === "choice" ? (
                <select value={v[key] ?? ""} onChange={(e) => change(e.target.value)}>
                  <option value="">Choose</option>
                  {(f.options ?? []).map((o) => (
                    <option key={o} value={o}>
                      {o}
                    </option>
                  ))}
                </select>
              ) : (
                <input
                  value={v[key] ?? ""}
                  type={f.type === "date" ? "date" : f.type === "email" ? "email" : f.type === "phone" ? "tel" : "text"}
                  inputMode={f.type === "number" || f.type === "money" ? "decimal" : f.type === "phone" ? "tel" : undefined}
                  autoComplete="off"
                  maxLength={200}
                  onChange={(e) => change(e.target.value)}
                  data-testid={`custom-${key}`}
                />
              )}
            </Field>
          );
        })}
        <Field label="Note" hint="Optional, for your own reference.">
          <input value={note} maxLength={200} onChange={(e) => setNote(e.target.value)} />
        </Field>
        {(problem || taken) && (
          <p className="error" role="alert">
            {problem ?? taken}
          </p>
        )}
        {!taken && <ErrorBox error={act.error} />}
        <div className="row-actions">
          <button type="submit" className="primary" disabled={act.busy} data-testid="saved-custom-save">
            {act.busy ? "Saving…" : "Save"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
