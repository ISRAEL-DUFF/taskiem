import { useState, type ReactNode } from "react";
import { ErrorBox } from "../ui";

// The chat tools' forms (ChatToolForms.tsx, SizesForms.tsx): the scrolling
// body and the Save at the foot.

/** Saves a form; answers its diary line, or null for none. */
export type Saver = () => Promise<string | null>;

/** A fresh Idempotency-Key for one change made from chat. */
export const formKey = () => `chat-${crypto.randomUUID()}`;

/** The scrolling body and the Save at the foot, as every form has them. */
export function FormFrame({
  children,
  save,
  saveWords = "Save",
  onSaved,
  ready = true,
  canEdit = true,
  onCancel,
}: {
  /** With it, the foot is Cancel | Save changes (the DeskSeller board). */
  onCancel?: () => void;
  children: ReactNode;
  save: Saver;
  saveWords?: string;
  onSaved: (line: string | null) => void;
  ready?: boolean;
  canEdit?: boolean;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  return (
    <form
      className="tk-form"
      onSubmit={(e) => {
        e.preventDefault();
        if (busy || !ready || !canEdit) return;
        setBusy(true);
        setError(undefined);
        save().then(
          (line) => onSaved(line),
          (err: unknown) => (setError(err), setBusy(false)),
        );
      }}
    >
      <fieldset className="tk-sheet-list tk-form-body" disabled={!canEdit}>
        {children}
        <ErrorBox error={error} />
      </fieldset>
      <div className="tk-sheet-foot">
        {canEdit && onCancel ? (
          <div className="tk-form-foot-two">
            <button type="button" className="tk-btn soft" onClick={onCancel}>
              Cancel
            </button>
            <button type="submit" className="tk-cta" disabled={busy || !ready}>
              {busy ? "Saving…" : "Save changes"}
            </button>
          </div>
        ) : canEdit ? (
          <button type="submit" className="tk-cta" disabled={busy || !ready}>
            {busy ? "Saving…" : saveWords}
          </button>
        ) : (
          <p className="tk-form-readonly">
            You can see this. Changing it needs someone who may edit it.
          </p>
        )}
      </div>
    </form>
  );
}

