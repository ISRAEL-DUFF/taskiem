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

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { post } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import {
  chipFor,
  chipWords,
  mayHoldAccount,
  messageFill,
  type PayReading,
} from "../lib/paymessage";
import { PaySheet } from "./PaySheet";

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
  /** Changes when a payment is made, so chips read again. */
  paidAt: number;
  open: (r: PayReading, text: string, from: DOMRect) => void;
}

const Ctx = createContext<PayFrom | null>(null);

/** Around a business's chat: the chips under its messages, and the Pay
 * sheet they open. Without money.pay, no chip shows. */
export function PayFromMessages({ children }: { children: ReactNode }) {
  const { can, me } = useAuth();
  const owner = !!me?.roles.includes("owner");
  const [paying, setPaying] = useState<{
    r: PayReading;
    text: string;
    from: DOMRect;
  }>();
  const [paidAt, setPaidAt] = useState(0);
  const open = useCallback(
    (r: PayReading, text: string, from: DOMRect) =>
      setPaying({ r, text, from }),
    [],
  );
  if (!can("money.pay")) return <>{children}</>;
  const fill = paying && messageFill(paying.r);
  return (
    <Ctx.Provider value={{ owner, paidAt, open }}>
      {children}
      {paying && fill && (
        <PaySheet
          title="Pay this account"
          actionId={fill.way === "person" ? "pay.person" : "pay.account"}
          fields={fill.fields}
          initial={fill.initial}
          expectName={fill.expectName}
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

/** The chip under a message's words, when they hold an account number. */
export function PayChip({ text }: { text: string | undefined }) {
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
      {kind === "ask" && (
        <span className="pay-chip muted" data-testid="pay-chip-ask">
          Ask an owner to save this account
        </span>
      )}
    </span>
  );
}
