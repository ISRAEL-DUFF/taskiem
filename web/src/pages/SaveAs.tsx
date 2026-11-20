import { useState, type ReactNode } from "react";
import { post } from "../api";
import { useAuth } from "../auth";
import { Items, Modal, Row } from "../ui";
import { KIND_WORDS, SAVED_KINDS, kindChoice, type SavedCard, type SavedDetails, type SavedKind } from "../lib/saved";
import { PopItem } from "./BubbleShell";
import { AccountSheet, ThingSheet } from "./Saved";

// Saving from a message, and from the picker (saved objects S1, part 4;
// saved-objects §9): Save as… on a message reads its words (POST
// /v1/me/saved/read) and opens the sheet for what they make, filled in to
// change before saving; Save "mum"… in the picker, when nothing has that
// name, asks what kind it is and opens its sheet with the name. In a
// business's chat, for whoever may manage its saved things, they go in the
// business's; else in one's own.

/** What the server read in a message's words. */
export interface SavedReading {
  kind: SavedKind;
  title: string;
  details: SavedDetails;
}

/** A reading as the sheets start from it: no name yet. */
export function readingCard(r: SavedReading, name = ""): SavedCard {
  return { kind: r.kind, title: r.title, name, lines: [], details: r.details, sensitive: false };
}

export function useSaveAs({ business = false }: { business?: boolean } = {}) {
  const { can } = useAuth();
  const mine = !(business && can("saved.manage"));
  const [start, setStart] = useState<SavedCard>();
  const [naming, setNaming] = useState<string>();
  const [said, setSaid] = useState<string>();

  /** Save as… on a message: its words read, the sheet opened. */
  const fromWords = (text: string) =>
    void post<SavedReading>("/v1/me/saved/read", { text }).then(
      (r) => setStart(readingCard(r)),
      () => setStart(readingCard({ kind: "text", title: "", details: { body: text } })),
    );

  /** Save "mum"… in the picker: what kind, then its sheet. */
  const named = (name: string) => setNaming(name);

  const done = (words: string) => {
    setStart(undefined);
    setSaid(words);
  };

  const node: ReactNode = (
    <>
      {naming !== undefined && (
        <Modal title={`Save @${naming}`} onClose={() => setNaming(undefined)}>
          <Items label="What to save">
            {SAVED_KINDS.map((k) => (
              <Row key={k} icon={KIND_WORDS[k].icon} title={kindChoice(k)} testid="save-as-kind">
                <button
                  type="button"
                  className="small"
                  onClick={() => {
                    setStart({ kind: k, title: "", name: naming, lines: [], details: {}, sensitive: false });
                    setNaming(undefined);
                  }}
                >
                  Choose<span className="sr-only"> {KIND_WORDS[k].one}</span>
                </button>
              </Row>
            ))}
          </Items>
        </Modal>
      )}
      {start &&
        (start.kind === "bank_account" ? (
          <AccountSheet mine={mine} checks={false} start={start} onClose={() => setStart(undefined)} onSaved={(problem) => done(problem ? `Saved. ${problem}` : "Saved")} />
        ) : (
          <ThingSheet mine={mine} kind={start.kind} start={start} onClose={() => setStart(undefined)} onSaved={() => done("Saved")} />
        ))}
      {said && (
        <p className="sr-only" role="status" data-testid="save-as-done">
          {said}
        </p>
      )}
    </>
  );
  return { fromWords, named, node };
}

/** The message menu's Save as…: its words to save. */
export function SaveAsItem({ text, onSave, close }: { text: string; onSave: (text: string) => void; close: () => void }) {
  return (
    <PopItem icon="note" onClick={() => (close(), onSave(text))}>
      Save as…
    </PopItem>
  );
}
