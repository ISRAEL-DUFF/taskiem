import { useRef, useState } from "react";
import { ApiError, get, post, put, upload } from "../api";
import { Icon } from "../icons";
import { assert } from "../passkeys";
import { ErrorBox, Field, Modal, useAction, useLoad } from "../ui";
import {
  FIELD_TYPES,
  cleanName,
  kindProblem,
  nameProblem,
  savedBase,
  savedKindsBase,
  savedFileURL,
  sizeWords,
  valuesProblem,
  type BankList,
  type FileMeta,
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

/** call, and when the server asks for a passkey first (403 step_up:
 * changing an account, showing what a kind kept hidden), the passkey it
 * names asked for and call again with it. */
export async function withSavedPasskey<T>(mine: boolean, call: (passkey?: unknown) => Promise<T>): Promise<T> {
  try {
    return await call();
  } catch (e) {
    if (!(e instanceof ApiError && e.status === 403 && e.body.step_up === "passkey")) throw e;
    const passkey = await assert(`${savedBase(mine)}/step-up/options`, { operation: e.body.operation, target: e.body.target });
    return call(passkey);
  }
}

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
      // Showing what was kept hidden asks for a passkey (saved.kind).
      const r = kind
        ? await withSavedPasskey(mine, (passkey) => put<{ kind: MadeKind }>(`${base}/${encodeURIComponent(kind.id)}`, passkey ? { ...body, passkey } : body))
        : await post<{ kind: MadeKind }>(base, body);
      onSaved(r.kind);
    }, kind ? "Kind changed" : `${name.trim()} made`);
  };
  // Fields of the kind as it was that are gone now: their values go too.
  const gone = kind && kind.count > 0 ? kind.fields.filter((k) => k.key && !fields.some((f) => f.key === k.key)).map((k) => k.label) : [];
  // Fields once sealed that would be shown now: a passkey is asked.
  const shownNow =
    kind && kind.count > 0
      ? fields.filter((f) => {
          const was = kind.fields.find((k) => k.key && k.key === f.key);
          return was && ((was.sensitive && !(f.sensitive || f.never_share || f.type === "bank_account")) || (was.never_share && !f.never_share) || (was.sensitive && was.type !== f.type));
        })
      : [];
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
                {/* A file is kept apart, never sealed; an account is always sensitive. */}
                {f.type !== "file" && (
                  <label>
                    <input
                      type="checkbox"
                      checked={!!f.sensitive || !!f.never_share || f.type === "bank_account"}
                      disabled={!!f.never_share || f.type === "bank_account"}
                      onChange={(e) => set(i, { sensitive: e.target.checked })}
                    />{" "}
                    Sensitive
                  </label>
                )}
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
        {gone.length > 0 && (
          <p className="hint warn" data-testid="kind-gone">
            Saving deletes what your saved things hold in {gone.join(", ")}.
          </p>
        )}
        {shownNow.length > 0 && (
          <p className="hint" data-testid="kind-shown">
            {shownNow.map((f) => f.label).join(", ")} will no longer be kept sealed: saving asks for your passkey.
          </p>
        )}
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
    // A file field holds its file's id: kept as it is unless another is chosen.
  );
  const [note, setNote] = useState(thing?.details.note ?? "");
  const [problem, setProblem] = useState<string>();
  // Files being checked: Save waits for them.
  const [uploading, setUploading] = useState(0);
  const act = useAction();
  const save = () => {
    // An account half filled (a bank with no number, or a number with no
    // bank) is said, not left out.
    const half = kind.fields.find((f) => f.type === "bank_account" && (v[f.key ?? ""] ?? "") !== "" && !/^\S.* · \d{10}$/.test(v[f.key ?? ""] ?? ""));
    const p =
      nameProblem(name) ??
      (title.trim() ? null : "Give it a title, like My car.") ??
      (half ? `${half.label}: choose the bank and type its 10-digit account number.` : null) ??
      valuesProblem(kind.fields, v, thing ? had : {});
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
        // Changing an account in it asks for a passkey (saved.change).
        if (thing) await withSavedPasskey(mine, (passkey) => put(`${base}/${encodeURIComponent(thing.id)}`, passkey ? { ...body, passkey } : body));
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
              {f.type === "file" ? (
                <FilePick
                  mine={mine}
                  label={f.label}
                  had={thing?.files?.[key]}
                  onPicked={(m) => change(m.file)}
                  onBusy={(b) => setUploading((n) => n + (b ? 1 : -1))}
                  testid={`custom-${key}`}
                />
              ) : f.type === "bank_account" ? (
                <BankField label={f.label} value={v[key] ?? ""} onChange={change} />
              ) : f.type === "choice" ? (
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
          <button type="submit" className="primary" disabled={act.busy || uploading > 0} data-testid="saved-custom-save">
            {act.busy ? "Saving…" : uploading > 0 ? "Checking the file…" : "Save"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** A photo or document chosen, uploaded to save (POST …/saved/uploads):
 * checked as a chat's file is, then kept until it is saved. */
export async function uploadSaved(mine: boolean, file: File): Promise<FileMeta> {
  const form = new FormData();
  form.append("file", file);
  const r = await upload<{ upload: FileMeta }>(`${savedBase(mine)}/uploads`, form);
  return r.upload;
}

/** Choose a photo or document: uploaded at once, its name and size said. */
export function FilePick({
  mine,
  label,
  had,
  onPicked,
  onBusy,
  testid,
}: {
  mine: boolean;
  label: string;
  /** What it holds now: its name and size. */
  had?: FileMeta;
  onPicked: (f: FileMeta) => void;
  /** A file is being checked (true), or done (false). */
  onBusy?: (busy: boolean) => void;
  testid?: string;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [shown, setShown] = useState<FileMeta | undefined>(had);
  const act = useAction();
  return (
    <div className="file-pick" data-testid={testid}>
      <input
        ref={input}
        type="file"
        accept="image/*,application/pdf"
        className="sr-only"
        aria-label={label}
        onChange={(e) => {
          const f = e.target.files?.[0];
          e.target.value = "";
          if (!f) return;
          onBusy?.(true);
          void act
            .run(async () => {
              const m = await uploadSaved(mine, f);
              setShown(m);
              onPicked(m);
            }, false)
            .finally(() => onBusy?.(false));
        }}
      />
      <button type="button" className="small" disabled={act.busy} onClick={() => input.current?.click()}>
        {act.busy ? "Checking the file…" : shown ? "Choose another" : "Choose a photo or document"}
        <span className="sr-only"> for {label}</span>
      </button>
      {shown && (
        <span className="hint">
          {shown.name} · {sizeWords(shown.size)}
        </span>
      )}
      <ErrorBox error={act.error} />
    </div>
  );
}

/** Save a photo or document, or change one. */
export function FileSheet({
  mine,
  thing,
  start,
  startName,
  onClose,
  onSaved,
}: {
  mine: boolean;
  thing?: SavedThing;
  /** A file already uploaded to save (Save as… on a chat's file). */
  start?: FileMeta;
  /** The name typed in the picker (Save "@menu"…). */
  startName?: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const base = savedBase(mine);
  const [name, setName] = useState(thing ? `@${thing.name}` : startName ? `@${startName}` : "");
  const [title, setTitle] = useState(thing?.title ?? (start ? start.name.replace(/\.[a-z0-9]{1,5}$/i, "") : ""));
  const [note, setNote] = useState(thing?.details.note ?? "");
  const [file, setFile] = useState<FileMeta | undefined>(start);
  const [problem, setProblem] = useState<string>();
  // The file being checked: Save waits for it.
  const [uploading, setUploading] = useState(false);
  const act = useAction();
  const had: FileMeta | undefined = thing?.details.file
    ? { file: thing.details.file, name: thing.details.file_name ?? "", type: thing.details.file_type ?? "", size: thing.details.file_size ?? 0 }
    : start;
  const save = () => {
    const p = nameProblem(name) ?? (title.trim() ? null : "Give it a title, like Our menu.") ?? (file || had ? null : "Choose the photo or document.");
    setProblem(p ?? undefined);
    if (p) return;
    void act.run(
      async () => {
        const details: Record<string, string> = { note: note.trim() };
        if (file) details.file = file.file;
        const body = { name: cleanName(name), title: title.trim(), details };
        if (thing) await put(`${base}/${encodeURIComponent(thing.id)}`, body);
        else await post(base, { ...body, kind: "file" });
        onSaved();
      },
      thing ? "Saved" : `Saved as @${cleanName(name)}`,
    );
  };
  const taken = act.error instanceof ApiError && act.error.body.name_taken ? "That name is taken: choose another." : undefined;
  return (
    <Modal title={thing ? `Change @${thing.name}` : "Save a photo or document"} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          save();
        }}
        data-testid="saved-file-form"
      >
        <Field label="The photo or document" hint="A photo, or a PDF up to 25 MB. It is checked before it is kept.">
          <FilePick mine={mine} label="The photo or document" had={had} onPicked={setFile} onBusy={setUploading} testid="saved-file-pick" />
        </Field>
        <Field label="Name to call it up by" hint="Such as @menu or @cac-certificate.">
          <input value={name} maxLength={33} autoCapitalize="none" autoComplete="off" onChange={(e) => setName(e.target.value)} required />
        </Field>
        <Field label="Title" hint="How it is shown.">
          <input value={title} maxLength={80} onChange={(e) => setTitle(e.target.value)} required />
        </Field>
        <Field label="Note" hint="Optional, for your own reference.">
          <input value={note} maxLength={200} onChange={(e) => setNote(e.target.value)} />
        </Field>
        {thing && had && (
          <p className="hint">
            <a href={savedFileURL(thing.owner, thing.id)} target="_blank" rel="noreferrer">
              Open {had.name}
            </a>
          </p>
        )}
        {(problem || taken) && (
          <p className="error" role="alert">
            {problem ?? taken}
          </p>
        )}
        {!taken && <ErrorBox error={act.error} />}
        <div className="row-actions">
          <button type="submit" className="primary" disabled={act.busy || uploading} data-testid="saved-file-save">
            {act.busy ? "Saving…" : uploading ? "Checking the file…" : "Save"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** A bank account field: the bank chosen by name, and its number. */
function BankField({ value, onChange, label }: { value: string; onChange: (v: string) => void; label: string }) {
  const banks = useLoad(() => get<BankList>("/v1/me/banks"), []);
  const m = /^(.*?)\s*·\s*(\d{10})$/.exec(value);
  const [bank, setBank] = useState(m?.[1] ?? "");
  const [number, setNumber] = useState(m?.[2] ?? "");
  const set = (b: string, n: string) => {
    setBank(b);
    setNumber(n);
    const digits = n.replace(/\D/g, "");
    // Half filled is kept half filled, so the form can say what is missing.
    onChange(b || digits ? `${b} · ${digits}` : "");
  };
  return (
    <div className="bank-field">
      <select aria-label={`${label}: the bank`} value={bank} onChange={(e) => set(e.target.value, number)} disabled={!banks.data}>
        <option value="">Choose the bank</option>
        {(banks.data?.banks ?? []).map((b) => (
          <option key={b.name} value={b.name}>
            {b.name}
          </option>
        ))}
      </select>
      <input aria-label={`${label}: the account number`} value={number} inputMode="numeric" autoComplete="off" maxLength={14} placeholder="10 digits" onChange={(e) => set(bank, e.target.value)} />
    </div>
  );
}
