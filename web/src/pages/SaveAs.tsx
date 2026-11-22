import { useState, type ReactNode } from "react";
import { get, getBlob, post } from "../api";
import { useAuth } from "../auth";
import { Items, Modal, Row } from "../ui";
import { toast } from "../toast";
import { KIND_WORDS, SAVED_KINDS, kindChoice, savedKindsBase, type FileMeta, type KindList, type MadeKind, type SavedCard, type SavedDetails, type SavedKind } from "../lib/saved";
import { PopItem } from "./BubbleShell";
import { AccountSheet, ThingSheet } from "./Saved";
import { CustomSheet, FileSheet, uploadSaved } from "./SavedKinds";
import { readChatPhoto } from "./PhotoRead";

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

/** onSaved: something was saved (the chat's picker reads its list again). */
export function useSaveAs({ business = false, onSaved }: { business?: boolean; onSaved?: () => void } = {}) {
  const { can } = useAuth();
  const mine = !(business && can("saved.manage"));
  const [start, setStart] = useState<SavedCard>();
  const [naming, setNaming] = useState<string>();
  const [said, setSaid] = useState<string>();
  // The owner's own kinds, read when Save "@mum"… asks what kind.
  const [made, setMade] = useState<MadeKind[]>([]);
  const [custom, setCustom] = useState<{ kind: MadeKind; name: string }>();
  const [fileStart, setFileStart] = useState<FileMeta>();
  // Save "@menu"… as a photo or document: its sheet, with the name.
  const [fileNamed, setFileNamed] = useState<string>();
  // A chat's file being fetched and checked to save: one at a time.
  const [fileBusy, setFileBusy] = useState(false);

  /** Save as… on a photo or document in a chat: kept as a file thing,
   * checked again as it is uploaded; what is happening shown, and why it
   * could not be. */
  const fromFile = (url: string, name: string, type: string) => {
    if (fileBusy) return;
    setFileBusy(true);
    toast(`Getting ${name} ready to save…`, "info");
    void getBlob(url)
      .then((b) => uploadSaved(mine, new File([b], name, { type: type || b.type })))
      .then(setFileStart, (e: unknown) => {
        const words = e instanceof Error ? e.message : "That file could not be saved.";
        setSaid(words);
        toast(words, "error");
      })
      .finally(() => setFileBusy(false));
  };

  /** Save as… on a message: its words read, the sheet opened. */
  const fromWords = (text: string) =>
    void post<SavedReading>("/v1/me/saved/read", { text }).then(
      (r) => setStart(readingCard(r)),
      () => setStart(readingCard({ kind: "text", title: "", details: { body: text } })),
    );

  /** Save the account on a photo in a chat (reading account details from
   * photos, I2): the photo fetched as the chat shows it and read on this
   * device, its words read by Taskiem (from: "photo"), and the sheet for
   * an account opened with them. No account in it: said. */
  const fromPhoto = (url: string) =>
    void (async () => {
      const words = await readChatPhoto(url);
      if (!words) return;
      let r: SavedReading;
      try {
        r = await post<SavedReading>("/v1/me/saved/read", { text: words, from: "photo" });
      } catch (e) {
        toast(e instanceof Error ? e.message : "That photo could not be read just now. Try again.", "error");
        return;
      }
      if (r.kind !== "bank_account" || !String(r.details?.account_number ?? "")) {
        toast("No account number in that photo.", "info");
        return;
      }
      setStart(readingCard(r));
    })();

  /** Save "mum"… in the picker: what kind, then its sheet. */
  const named = (name: string) => {
    setNaming(name);
    void get<KindList>(savedKindsBase(mine)).then(
      (r) => setMade(r.kinds),
      () => setMade([]),
    );
  };

  const done = (words: string) => {
    setStart(undefined);
    setSaid(words);
    onSaved?.();
  };

  const node: ReactNode = (
    <>
      {naming !== undefined && (
        <Modal title={`Save @${naming}`} onClose={() => setNaming(undefined)}>
          <Items label="What to save">
            {made.map((k) => (
              <Row key={k.id} icon="box" title={`A ${k.name.toLowerCase()}`} sub="One of your own kinds" testid="save-as-kind">
                <button
                  type="button"
                  className="small"
                  onClick={() => {
                    setCustom({ kind: k, name: naming });
                    setNaming(undefined);
                  }}
                >
                  Choose<span className="sr-only"> a {k.name.toLowerCase()}</span>
                </button>
              </Row>
            ))}
            {SAVED_KINDS.map((k) => (
              <Row key={k} icon={KIND_WORDS[k].icon} title={kindChoice(k)} testid="save-as-kind">
                <button
                  type="button"
                  className="small"
                  onClick={() => {
                    if (k === "file") setFileNamed(naming);
                    else setStart({ kind: k, title: "", name: naming, lines: [], details: {}, sensitive: false });
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
          <ThingSheet mine={mine} kind={start.kind === "custom" ? "text" : start.kind} start={start} onClose={() => setStart(undefined)} onSaved={() => done("Saved")} />
        ))}
      {custom && (
        <CustomSheet
          mine={mine}
          kind={custom.kind}
          startName={custom.name}
          onClose={() => setCustom(undefined)}
          onSaved={() => {
            setCustom(undefined);
            setSaid("Saved");
            onSaved?.();
          }}
        />
      )}
      {fileStart && (
        <FileSheet
          mine={mine}
          start={fileStart}
          onClose={() => setFileStart(undefined)}
          onSaved={() => {
            setFileStart(undefined);
            setSaid("Saved");
            onSaved?.();
          }}
        />
      )}
      {fileNamed !== undefined && (
        <FileSheet
          mine={mine}
          startName={fileNamed}
          onClose={() => setFileNamed(undefined)}
          onSaved={() => {
            setFileNamed(undefined);
            setSaid("Saved");
            onSaved?.();
          }}
        />
      )}
      {said && (
        <p className="sr-only" role="status" data-testid="save-as-done">
          {said}
        </p>
      )}
    </>
  );
  return { fromWords, fromFile, fromPhoto, fileBusy, named, node };
}

/** The message menu's Save as…: its words to save. */
export function SaveAsItem({ text, onSave, close, label = "Save as…" }: { text: string; onSave: (text: string) => void; close: () => void; label?: string }) {
  return (
    <PopItem icon="note" onClick={() => (close(), onSave(text))}>
      {label}
    </PopItem>
  );
}
