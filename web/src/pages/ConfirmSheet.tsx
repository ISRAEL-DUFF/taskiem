import { useCallback, useState, type ReactNode } from "react";
import { Modal } from "../ui";

/** What a confirmation says: a title, the words under it, the button that agrees (red for something that cannot be undone). */
export interface Ask {
  title: string;
  body?: string;
  ok: string;
  danger?: boolean;
}

/**
 * The app's own confirmation, in place of the browser's grey box:
 * `const { confirm, node } = useConfirm()`, show `node` somewhere in the
 * page, and `if (await confirm({ title, ok })) …`. Cancel comes first and
 * has the focus, so Enter never agrees to something that cannot be undone.
 */
export function useConfirm(): {
  confirm: (a: Ask) => Promise<boolean>;
  node: ReactNode;
} {
  const [ask, setAsk] = useState<
    (Ask & { done: (yes: boolean) => void }) | null
  >(null);
  const confirm = useCallback(
    (a: Ask) => new Promise<boolean>((done) => setAsk({ ...a, done })),
    [],
  );
  const close = (yes: boolean) => {
    ask?.done(yes);
    setAsk(null);
  };
  const node = ask ? (
    <Modal title={ask.title} onClose={() => close(false)}>
      {ask.body && <p>{ask.body}</p>}
      <div className="row-actions">
        <button type="button" autoFocus onClick={() => close(false)}>
          Cancel
        </button>
        <button
          type="button"
          className={ask.danger ? "danger" : "primary"}
          onClick={() => close(true)}
          data-testid="confirm-ok"
        >
          {ask.ok}
        </button>
      </div>
    </Modal>
  ) : null;
  return { confirm, node };
}
