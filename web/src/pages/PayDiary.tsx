// A payment in the chat it was paid from (docs/spec/chat-pay-people.md,
// "After"; build-plan-gaps H7): its diary line, "Paid Chidi Okeke ₦45,000
// (delivery) · GTBank ••6789", shown as the payment stands now ("Received
// by the bank", or "Failed: refunded to your balance" with Try again); a
// batch's one line with how its rows stand; a receipt sent into the chat;
// and the card a staff member sees when the business paid them.

import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { get, post } from "../api";
import { naira } from "../lib/invoices";
import { progressWords, type PaymentBatch } from "../lib/batches";
import { diaryState, type Payment } from "../lib/payments";
import { toast } from "../toast";

/** How a payment (or a batch) a diary line names stands now, after its
 * words; Try again on one that did not reach them. */
export function PayDiaryState({ payment, batch }: { payment?: string; batch?: string }) {
  const [x, setX] = useState<Payment | null>();
  const [b, setB] = useState<PaymentBatch | null>();
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();
  useEffect(() => {
    let live = true;
    let timer = 0;
    const load = () => {
      if (payment)
        get<{ payment: Payment }>(`/v1/payments/${encodeURIComponent(payment)}`).then(
          (r) => {
            if (!live) return;
            setX(r.payment);
            // Still on its way: asked again in a while.
            if (["sending", "unknown", "waiting_ok"].includes(r.payment.status)) timer = window.setTimeout(load, 15_000);
          },
          () => live && setX(null),
        );
      else if (batch)
        get<{ batch: PaymentBatch }>(`/v1/payment-batches/${encodeURIComponent(batch)}`).then(
          (r) => live && setB(r.batch),
          () => live && setB(null),
        );
    };
    load();
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [payment, batch]);
  if (b)
    return (
      <span className="pay-diary-state info" data-testid="pay-diary-state">
        {" "}
        · {progressWords(b)}
      </span>
    );
  if (!x) return null;
  const st = diaryState(x);
  const again = async () => {
    setBusy(true);
    try {
      const r = await post<{ payment: Payment }>(`/v1/payments/${encodeURIComponent(x.id)}/again`);
      navigate(`/payments/${encodeURIComponent(r.payment.id)}`);
    } catch (e) {
      toast(e instanceof Error ? e.message : "It could not be prepared again just now.", "error");
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <span className={`pay-diary-state ${st.tone}`} data-testid="pay-diary-state">
        {" "}
        · {st.words}
      </span>
      {st.again && (
        <button type="button" className="tk-btn soft pay-diary-again" onClick={() => void again()} disabled={busy} data-testid="pay-try-again">
          Try again
        </button>
      )}
    </>
  );
}

/** A receipt sent into the chat, or a payment to this person ("paid
 * you"): a small card with its words, and the receipt's PDF. */
export function PayChatCard({ kind, text, payment }: { kind: "receipt" | "paid_you"; text: string; payment?: string }) {
  return (
    <div className="pay-chat-card" data-testid={kind === "receipt" ? "pay-receipt-card" : "pay-paid-you"}>
      <span className="pay-chat-card-label">{kind === "receipt" ? "Receipt" : "Paid to you"}</span>
      <strong>{text}</strong>
      {kind === "receipt" && payment && (
        <a href={`/v1/payments/${encodeURIComponent(payment)}/receipt/pdf`} target="_blank" rel="noreferrer">
          Open the receipt (PDF)
        </a>
      )}
    </div>
  );
}

/** A receipt's words: "Paid Chidi Okeke ₦45,000 for a delivery". */
export const receiptLine = (x: Pick<Payment, "payee" | "account_name" | "amount_kobo">) =>
  `Paid ${x.account_name || x.payee} ${naira(x.amount_kobo)}`;
