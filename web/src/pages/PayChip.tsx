// "Pay this account" under a chat message (paying people, phase 2;
// docs/spec/chat-pay-people.md, "Three ways in"): a message from the other
// side holding a 10-digit account number is read (POST /v1/payments/read)
// once it is on the screen, and gets a chip that opens the Pay sheet
// filled in: the saved account by its @name when the business has one with
// that number, else the number and its bank; the amount when the message
// says one; and the name it gives, said in amber when the bank's differs.
// A chip already paid says so, and still pays again. Only for people who
// may pay (money.pay); staff pay saved accounts only, so an account not
// saved asks them to have an owner save it. Nothing is paid by the chip.
// Beside it, for whoever may manage saved things (saved.manage), "Save
// as…" keeps an account not saved yet by a name to call them by
// (SaveAsSheet); saved, the chip says that @name and Pay goes by it.
// Staff who may save see it in place of "Ask an owner to save".
// A photo in the chat has "Pay this account" in its menu (reading account
// details from photos, I2; usePayFromPhoto): the photo read on this
// device, its words read with from: "photo", and the same sheet opened.
// Beside Pay, "Add to the list" (collecting from several messages, P3;
// lib/paycollect.ts) keeps the message's account on the person's own
// list, kept by the server across chats; PayListBar is "3 to pay" at the
// foot of the chat, with Pay these.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Link } from "react-router-dom";
import { ApiError, del, get, post } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import {
  askToSave,
  chipFor,
  chipWords,
  mayHoldAccount,
  messageFill,
  noAccountInPhoto,
  saveAsShown,
  type PayReading,
  type SaidIn,
} from "../lib/paymessage";
import {
  addBody,
  addedWords,
  footWords,
  needsBank,
  onList,
  rememberChat,
  sourceFor,
  type ChatPlace,
  type Collection,
  type CollectedSource,
} from "../lib/paycollect";
import { toast } from "../toast";
import { Field, Modal } from "../ui";
import { AIReadOfferModal, chatPhoto, readingWords, usePhotoAI } from "./PhotoRead";
import { PopItem } from "./BubbleShell";
import { PaySheet } from "./PaySheet";
import { SaveAsSheet } from "./SaveAsSheet";

/** What a message's words were read as, once per words. */
const reads = new Map<string, Promise<PayReading>>();
const readMessage = (text: string): Promise<PayReading> => {
  let p = reads.get(text);
  if (!p) {
    p = post<PayReading>("/v1/payments/read", { text });
    p.catch(() => reads.delete(text));
    reads.set(text, p);
  }
  return p;
};

interface PayFrom {
  owner: boolean;
  /** May keep an account among the business's saved ones (saved.manage). */
  mayKeep: boolean;
  /** Changes when a payment is made, so chips read again. */
  paidAt: number;
  open: (r: PayReading, text: string, from: DOMRect, saidIn?: SaidIn) => void;
  keep: (r: PayReading, text: string, from: DOMRect) => void;
  /** A chat photo's words, read on this device or, offered, with AI. */
  photoAI: ReturnType<typeof usePhotoAI>;
  /** The chat this is, for rows added to the list. */
  chat?: ChatPlace;
  /** The person's list, once read. */
  list?: Collection;
  /** A message's reading added to the list. */
  add: (r: PayReading, source: CollectedSource) => void;
  /** The list cleared. */
  clear: () => Promise<void>;
}

const Ctx = createContext<PayFrom | null>(null);

/** Said when a message or photo is on the list already. */
const alreadyWords = (s: CollectedSource) =>
  s.kind === "photo" ? "That photo is on your list already." : "That message is on your list already.";

/** The person's list (GET /v1/pay-collection): read when the chat opens,
 * and again whenever the window comes back to the front. */
function useCollection(on: boolean) {
  const [list, setList] = useState<Collection>();
  const reload = useCallback(() => {
    if (!on) return;
    get<Collection>("/v1/pay-collection").then(setList, () => undefined);
  }, [on]);
  useEffect(() => {
    reload();
    const again = () => {
      if (document.visibilityState !== "hidden") reload();
    };
    window.addEventListener("focus", again);
    document.addEventListener("visibilitychange", again);
    return () => {
      window.removeEventListener("focus", again);
      document.removeEventListener("visibilitychange", again);
    };
  }, [reload]);
  return { list, setList, reload };
}

/** Around a business's chat: the chips under its messages, and the Pay
 * sheet they open. Without money.pay, no chip shows. */
export function PayFromMessages({
  children,
  chat,
}: {
  children: ReactNode;
  /** The chat it is around, so Add to the list says where a row is from. */
  chat?: ChatPlace;
}) {
  const { can, me } = useAuth();
  const owner = !!me?.roles.includes("owner");
  const [paying, setPaying] = useState<{
    r: PayReading;
    text: string;
    from: DOMRect;
    saidIn?: SaidIn;
  }>();
  const [keeping, setKeeping] = useState<{
    r: PayReading;
    text: string;
    from: DOMRect;
  }>();
  const [paidAt, setPaidAt] = useState(0);
  const open = useCallback(
    (r: PayReading, text: string, from: DOMRect, saidIn?: SaidIn) =>
      setPaying({ r, text, from, saidIn }),
    [],
  );
  const keep = useCallback(
    (r: PayReading, text: string, from: DOMRect) =>
      setKeeping({ r, text, from }),
    [],
  );
  const photoAI = usePhotoAI();
  const mayPay = can("money.pay");
  const { list, setList, reload } = useCollection(mayPay);
  const [choosing, setChoosing] = useState<{ r: PayReading; source: CollectedSource }>();
  useEffect(() => {
    if (chat) rememberChat(chat.kind, chat.id, chat.name);
  }, [chat?.kind, chat?.id, chat?.name]);
  const send = useCallback(
    async (r: PayReading, source: CollectedSource, bank?: string) => {
      try {
        const got = await post<Collection>("/v1/pay-collection", addBody(r, source, bank));
        setList(got);
        toast(addedWords(got), "success");
        return true;
      } catch (e) {
        if (e instanceof ApiError && e.body.code === "already") {
          toast(alreadyWords(source), "info");
          reload();
          return true;
        }
        toast(e instanceof Error ? e.message : "It could not be added just now. Try again.", "error");
        return false;
      }
    },
    [reload],
  );
  const add = useCallback(
    (r: PayReading, source: CollectedSource) => {
      if (onList(list, source)) toast(alreadyWords(source), "info");
      else if (needsBank(r)) setChoosing({ r, source });
      else void send(r, source);
    },
    [list, send],
  );
  const clear = useCallback(async () => {
    try {
      setList(await del<Collection>("/v1/pay-collection"));
      toast("The list is cleared", "success");
    } catch (e) {
      toast(e instanceof Error ? e.message : "The list could not be cleared just now. Try again.", "error");
    }
  }, []);
  if (!mayPay) return <>{children}</>;
  const fill = paying && messageFill(paying.r);
  const mayKeep = can("saved.manage");
  return (
    <Ctx.Provider value={{ owner, mayKeep, paidAt, open, keep, photoAI, chat, list, add, clear }}>
      {children}
      <AIReadOfferModal offer={photoAI.offer} onClose={photoAI.clear} />
      {choosing && (
        <ChooseBank
          r={choosing.r}
          onClose={() => setChoosing(undefined)}
          onBank={async (bank) => {
            if (await send(choosing.r, choosing.source, bank)) setChoosing(undefined);
          }}
        />
      )}
      {keeping && (
        <SaveAsSheet
          r={keeping.r}
          rect={keeping.from}
          onClose={() => setKeeping(undefined)}
          onSaved={(name) => {
            // Saved: the message reads as this @name from now on, so the
            // chip says it and Pay goes by it (for staff too).
            reads.set(
              keeping.text,
              Promise.resolve({ ...keeping.r, payee: name }),
            );
            setPaidAt(Date.now());
          }}
        />
      )}
      {paying && fill && (
        <PaySheet
          title="Pay this account"
          actionId={fill.way === "person" ? "pay.person" : "pay.account"}
          fields={fill.fields}
          initial={fill.initial}
          expectName={fill.expectName}
          saidIn={paying.saidIn}
          from="chat"
          rect={paying.from}
          onClose={() => setPaying(undefined)}
          onPaid={() => {
            reads.delete(paying.text);
            setPaidAt(Date.now());
          }}
        />
      )}
    </Ctx.Provider>
  );
}

/** A chat photo read as the chip reads a message: fetched as the chat
 * shows it, read on this device (never sent to be read), its words read
 * by Taskiem (from: "photo"), and the reading given to use when it holds
 * an account the person may pay. null outside PayFromMessages. */
function usePhotoReading() {
  const ctx = useContext(Ctx);
  if (!ctx) return null;
  const read = (url: string, use: (r: PayReading, words: string) => void) =>
    void (async () => {
      const photo = await chatPhoto(url);
      if (!photo) return;
      toast(readingWords, "info");
      try {
        // Nothing found on this device: "Read it with AI" where it is on.
        await ctx.photoAI.read(photo, async (words) => {
          const r = await post<PayReading>("/v1/payments/read", {
            text: words,
            from: "photo",
          });
          const kind = chipFor(r, ctx.owner);
          if (!kind) return noAccountInPhoto;
          if (kind === "ask") toast(askToSave, "info");
          else use(r, words);
          return null;
        });
      } catch (e) {
        toast(
          e instanceof Error ? e.message : "That photo could not be read just now. Try again.",
          "error",
        );
      }
    })();
  return { read, ctx };
}

/** "Pay this account" on a photo in a chat, for those the chip shows to
 * (inside PayFromMessages, with money.pay); null for anyone else. Given
 * where the chat fetches the photo: read as usePhotoReading reads it, and
 * the Pay sheet opened filled in as the chip opens it. */
export function usePayFromPhoto(): ((url: string, from: DOMRect) => void) | null {
  const p = usePhotoReading();
  if (!p) return null;
  return (url, from) => p.read(url, (r, words) => p.ctx.open(r, words, from, "photo"));
}

/** Which bank an account read with none is with, before it goes on the
 * list: the banks its number could be with first, or typed. */
function ChooseBank({
  r,
  onClose,
  onBank,
}: {
  r: PayReading;
  onClose: () => void;
  onBank: (bank: string) => Promise<void>;
}) {
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const go = async (bank: string) => {
    if (!bank.trim() || busy) return;
    setBusy(true);
    try {
      await onBank(bank.trim());
    } finally {
      setBusy(false);
    }
  };
  const likely = (r.likely ?? []).filter((b) => b.likely !== false).slice(0, 4);
  return (
    <Modal title="Which bank is it with?" onClose={onClose}>
      <div data-testid="collect-bank">
        <p className="hint">
          The message gives the account number ••{(r.account_number ?? "").slice(-4)} but not its bank. The bank
          checks the name before anyone is paid.
        </p>
        {likely.length > 0 && (
          <div className="toolbar" role="group" aria-label="Banks this number could be with">
            {likely.map((b) => (
              <button key={b.code} type="button" disabled={busy} onClick={() => void go(b.name)} data-testid="collect-bank-likely">
                {b.name}
              </button>
            ))}
          </div>
        )}
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void go(typed);
          }}
        >
          <Field label={likely.length ? "Or type the bank" : "The bank"}>
            <input value={typed} autoComplete="off" onChange={(e) => setTyped(e.target.value)} data-testid="collect-bank-typed" />
          </Field>
          <div className="row-actions">
            <button type="submit" className="primary" disabled={busy || !typed.trim()} data-testid="collect-bank-add">
              {busy ? "Adding…" : "Add to the list"}
            </button>
            <button type="button" onClick={onClose}>
              Cancel
            </button>
          </div>
        </form>
      </div>
    </Modal>
  );
}

/** A photo's menu in a chat (the app's saveAsItems): Pay this account and
 * Add to the list, for those the chip shows to, and Save the account
 * (saveAccount, Save as…'s sheet for an account). url is where the chat
 * fetches the photo; message is the photo's message. */
export function PhotoAccountItems({
  url,
  message,
  close,
  saveAccount,
  payable = true,
}: {
  url: string;
  /** The photo's message, so it can be added to the list. */
  message?: string;
  close: () => void;
  saveAccount: (url: string) => void;
  /** Whether Pay this account is offered at all (a chat whose chip shows
   * only under the other side's messages passes false for our own). */
  payable?: boolean;
}) {
  const payFrom = usePayFromPhoto();
  const reading = usePhotoReading();
  const pay = payable ? payFrom : null;
  const chat = reading?.ctx.chat;
  return (
    <>
      {pay && (
        <PopItem
          icon="money"
          onClick={(e) => {
            const rect = e.currentTarget.getBoundingClientRect();
            close();
            pay(url, rect);
          }}
        >
          Pay this account
        </PopItem>
      )}
      {pay && reading && chat && message && (
        <PopItem
          icon="plus"
          onClick={() => {
            close();
            const source = sourceFor(chat, message, true);
            if (onList(reading.ctx.list, source)) toast(alreadyWords(source), "info");
            else reading.read(url, (r) => reading.ctx.add(r, source));
          }}
        >
          Add to the list
        </PopItem>
      )}
      <PopItem icon="card" onClick={() => (close(), saveAccount(url))}>
        Save the account
      </PopItem>
    </>
  );
}

/** The chip under a message's words, when they hold an account number.
 * message is the message's id, for Add to the list. */
export function PayChip({ text, message }: { text: string | undefined; message?: string }) {
  const ctx = useContext(Ctx);
  const words = text ?? "";
  const maybe = !!ctx && mayHoldAccount(words);
  const ref = useRef<HTMLSpanElement>(null);
  const [r, setR] = useState<PayReading>();
  const paidAt = ctx?.paidAt ?? 0;
  useEffect(() => {
    if (!maybe) return;
    let live = true;
    const go = () =>
      readMessage(words).then(
        (x) => live && setR(x),
        () => undefined,
      );
    const el = ref.current;
    if (!el || typeof IntersectionObserver === "undefined") {
      void go();
      return () => {
        live = false;
      };
    }
    // Read when it is on the screen, not for every message in the thread.
    const io = new IntersectionObserver((es) => {
      if (es.some((e) => e.isIntersecting)) {
        io.disconnect();
        void go();
      }
    });
    io.observe(el);
    return () => {
      live = false;
      io.disconnect();
    };
  }, [maybe, words, paidAt]);
  if (!maybe || !ctx) return null;
  const kind = chipFor(r, ctx.owner);
  const source = ctx.chat && message ? sourceFor(ctx.chat, message) : undefined;
  const listed = source ? onList(ctx.list, source) : false;
  return (
    <span ref={ref} className="pay-chip-line" data-testid="pay-chip-line">
      {kind === "pay" && r && (
        <button
          type="button"
          className={`pay-chip${r.paid ? " paid" : ""}`}
          onClick={(e) =>
            ctx.open(r, words, e.currentTarget.getBoundingClientRect())
          }
          data-testid="pay-chip"
        >
          <Icon name={r.paid ? "check" : "send"} />
          <span>{chipWords(r)}</span>
        </button>
      )}
      {kind === "pay" && r && source && !listed && (
        <button
          type="button"
          className="pay-chip save"
          onClick={() => ctx.add(r, source)}
          data-testid="pay-chip-add"
        >
          <Icon name="plus" />
          <span>Add to the list</span>
        </button>
      )}
      {kind === "pay" && r && source && listed && (
        <span className="pay-chip muted" data-testid="pay-chip-listed">
          <Icon name="check" />
          <span>On your list</span>
        </span>
      )}
      {kind === "ask" && !ctx.mayKeep && (
        <span className="pay-chip muted" data-testid="pay-chip-ask">
          Ask an owner to save this account
        </span>
      )}
      {r && kind && saveAsShown(r, ctx.mayKeep) && (
        <button
          type="button"
          className="pay-chip save"
          onClick={(e) =>
            ctx.keep(r, words, e.currentTarget.getBoundingClientRect())
          }
          data-testid="pay-chip-save"
        >
          <Icon name="plus" />
          <span>Save as…</span>
        </button>
      )}
    </span>
  );
}

/** "3 to pay · ₦95,000" at the foot of a chat, above where you write,
 * while the person's list has anyone on it: Pay these, and Clear behind
 * More. A list waiting in a batch says so, opening it. */
export function PayListBar() {
  const ctx = useContext(Ctx);
  const [menu, setMenu] = useState(false);
  const list = ctx?.list;
  if (!ctx || !list || (list.count === 0 && !list.in_batch)) return null;
  const w = footWords(list);
  return (
    <section className="pay-list-bar" aria-label="Your list to pay" data-testid="pay-list-bar">
      <p className="pay-list-sum">
        <strong data-testid="pay-list-count">{list.count > 0 ? w.main : "Your list"}</strong>
        {list.count > 0 && w.needs && (
          <span className="pay-list-needs" data-testid="pay-list-needs">
            {w.needs}
          </span>
        )}
        {list.in_batch && (
          <Link
            to={`/payments/batches/${encodeURIComponent(list.in_batch)}`}
            className="pay-list-waiting"
            data-testid="pay-list-waiting"
          >
            Waiting to be sent
          </Link>
        )}
      </p>
      {list.count > 0 && (
        <div className="pay-list-acts">
          <Link className="button small primary" to="/payments/several/list" data-testid="pay-list-go">
            Pay these
          </Link>
          <span className="pay-list-more">
            <button
              type="button"
              className="small"
              aria-label="More about the list"
              aria-expanded={menu}
              onClick={() => setMenu((x) => !x)}
              data-testid="pay-list-more"
            >
              <Icon name="more" />
            </button>
            {menu && (
              <span className="pop-card pay-list-menu">
                <PopItem
                  icon="trash"
                  danger
                  onClick={() => {
                    setMenu(false);
                    if (
                      window.confirm(
                        `Clear the list? ${list.count === 1 ? "1 person comes" : `${list.count} people come`} off it, and nobody is paid.`,
                      )
                    )
                      void ctx.clear();
                  }}
                >
                  Clear the list
                </PopItem>
              </span>
            )}
          </span>
        </div>
      )}
    </section>
  );
}
