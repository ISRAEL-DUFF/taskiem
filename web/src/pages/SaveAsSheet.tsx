// "Save as…" beside a message's "Pay this account" chip (paying people,
// phase 2; docs/spec/chat-pay-people.md, "Save as a beneficiary … with a
// name to call them by, 'Chidi, rider'"; the build plan's P2, "can be
// saved as @driver on the way"). The account a message holds, kept among
// the business's saved accounts as the Saved page keeps one (POST
// /v1/saved): a name to call them by, made from the message's name; a
// title; the bank read from the message, or picked as the Pay sheet picks
// it, the likely banks first; the number, masked. Where the business has a
// payments account the bank checks the name as it is saved, and its
// answer is said. Nothing is paid here.

import { useCallback, useEffect, useState } from "react";
import { ApiError, get, post } from "../api";
import { KitField, Sheet } from "../kit";
import { ErrorBox } from "../ui";
import {
  maskedAccount,
  saveAsAnswer,
  saveAsBank,
  saveAsBody,
  saveAsProblem,
  saveAsStart,
  type PayReading,
} from "../lib/paymessage";
import { cleanName, type BankList, type SavedThing } from "../lib/saved";
import { BankPicker } from "./PaySheet";

type Saved = { saved: SavedThing; check_problem?: string };

export function SaveAsSheet({
  r,
  rect,
  onClose,
  onSaved,
}: {
  r: PayReading;
  rect?: DOMRect | null;
  onClose: () => void;
  /** Saved, by this @name (without the @). */
  onSaved: (name: string) => void;
}) {
  const [v, setV] = useState(() => ({ ...saveAsStart(r), bank: r.bank_name ?? "" }));
  const [list, setList] = useState<BankList | null>();
  const [tried, setTried] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [done, setDone] = useState<Saved>();
  const read = !!r.bank_name;
  // The bank read from the message: its code is kept with the list it is
  // from, as the Saved page keeps it.
  useEffect(() => {
    if (!read || !r.account_number) return;
    let live = true;
    get<BankList>(`/v1/payments/banks?account=${encodeURIComponent(r.account_number)}`).then(
      (l) => live && setList(l.banks?.length ? l : null),
      () => live && setList(null),
    );
    return () => {
      live = false;
    };
  }, [read, r.account_number]);
  const onList = useCallback((l: BankList | null) => setList(l), []);
  const set = (patch: Partial<typeof v>) => {
    setV((cur) => ({ ...cur, ...patch }));
    if ("name" in patch) setError(undefined);
  };
  const problem = tried ? saveAsProblem(v) : null;
  const taken = error instanceof ApiError && error.body.name_taken ? "That name is taken: choose another." : undefined;

  const save = async () => {
    setTried(true);
    const bank = saveAsBank(v.bank, list, r);
    if (saveAsProblem(v) || !bank || busy) return;
    setBusy(true);
    setError(undefined);
    try {
      const out = await post<Saved>("/v1/saved", saveAsBody(r, v, bank));
      setDone(out);
      onSaved(out.saved?.name || cleanName(v.name));
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  if (done) {
    const a = saveAsAnswer(done, r.name);
    return (
      <Sheet title="Saved" from={rect} onClose={onClose} testid="save-as-sheet">
        <div className="tk-form-body">
          <p className="save-as-done" data-testid="save-as-done">
            Saved as <strong>@{done.saved.name}</strong>, among your saved accounts. Pay goes by its name next time.
          </p>
          {a.bankName && (
            <p className="notice" role="status" data-testid="save-as-bank-name">
              The bank's name for it: <strong>{a.bankName}</strong>
            </p>
          )}
          {a.differs && (
            <div className="pay-warn warn" role="note" data-testid="save-as-name-differs">
              {a.differs}
            </div>
          )}
          {a.problem && (
            <div className="pay-warn warn" role="note" data-testid="save-as-check-problem">
              {a.problem}
            </div>
          )}
        </div>
        <div className="tk-sheet-foot">
          <button type="button" className="tk-cta" onClick={onClose} data-testid="save-as-close">
            Done
          </button>
        </div>
      </Sheet>
    );
  }

  return (
    <Sheet title="Save this account" from={rect} onClose={onClose} testid="save-as-sheet">
      <form
        className="tk-form"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
        noValidate
      >
        <fieldset className="tk-sheet-list tk-form-body">
          <p className="pay-lead">Keep it among your saved accounts, to pay it by its name next time.</p>
          <KitField
            label="Name to call them by"
            hint="Such as @chidi-rider: letters, numbers and “-”."
            error={taken ?? (problem?.at === "name" ? problem.words : undefined)}
          >
            <input
              value={v.name}
              maxLength={33}
              autoCapitalize="none"
              autoComplete="off"
              onChange={(e) => set({ name: e.target.value })}
              data-testid="save-as-name"
            />
          </KitField>
          <KitField label="Title" hint="How it is shown, such as Chidi, rider." error={problem?.at === "title" ? problem.words : undefined}>
            <input value={v.title} maxLength={80} onChange={(e) => set({ title: e.target.value })} data-testid="save-as-title" />
          </KitField>
          {read ? (
            <div className="tk-field">
              <span className="tk-field-label">Bank</span>
              <span className="save-as-value" data-testid="save-as-bank">
                {r.bank_name}
              </span>
            </div>
          ) : (
            <BankPicker label="Bank" value={v.bank} onChange={(b) => set({ bank: b })} account={r.account_number} expectName={r.name} onList={onList} />
          )}
          <div className="tk-field">
            <span className="tk-field-label">Account number</span>
            <span className="save-as-value" data-testid="save-as-number">
              {maskedAccount(r.account_number)}
            </span>
          </div>
          {problem?.at === "bank" && (
            <p className="tk-field-error" role="alert">
              {problem.words}
            </p>
          )}
          {!taken && <ErrorBox error={error} />}
        </fieldset>
        <div className="tk-sheet-foot">
          <button type="submit" className="tk-cta" disabled={busy} data-testid="save-as-save">
            {busy ? "Saving…" : "Save account"}
          </button>
          <p className="pay-foot-hint">Where your business has a payments account, the bank checks the name as it is saved.</p>
        </div>
      </form>
    </Sheet>
  );
}
