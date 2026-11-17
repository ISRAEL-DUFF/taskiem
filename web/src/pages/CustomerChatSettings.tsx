import { useRef, useState } from "react";
import { Link } from "react-router-dom";
import { get, post, put } from "../api";
import { useAuth } from "../auth";
import { Sheet } from "../kit";
import { buttonWords, offerCount, ORDERS_OFF_WORDS, takesChatsHint, type ChatLink, type ChatLinkSettings, type CustomerChatSettings as Replies } from "../lib/customerlinks";
import { phoneWords } from "../lib/customerchat";
import { toast } from "../toast";
import { ErrorBox, Field, PageHeader, Skeleton, useAction, useLoad } from "../ui";
import { useConfirm } from "./ConfirmSheet";

// Customers chatting with the business (docs/spec/taskiem-for-everyone.md
// §8.2, §8.6, §12, §15): Your chat link (its QR code, the words to paste,
// Make a new link), whether the business takes chats and where its shop
// offers them, what happens when nobody answers, and an offer to the
// customers who said yes. A page under Settings, and the Customers tile of
// the chat's tools.

/** The page: /settings/customers. */
export function CustomerChatSettings() {
  const { can } = useAuth();
  return (
    <>
      <p className="crumbs">
        <Link to="/settings" aria-label="Back to Settings">
          Settings
        </Link>
      </p>
      <PageHeader title="Customer chats" description="How customers chat with you in the Taskiem app and their browser, and what happens when nobody answers." />
      <YourChatLink />
      {can("storefront.manage") && <TakingChats />}
      <WhenNobodyAnswers editable={can("storefront.manage")} />
      <SendOffer />
    </>
  );
}

/** Your chat link: the link, its QR code to print, the words to paste,
 * and Make a new link. */
export function YourChatLink() {
  const { can } = useAuth();
  const view = useLoad(() => get<ChatLink>("/v1/customers/chat-link"), []);
  const act = useAction();
  const sure = useConfirm();
  // The QR code is read again when the link changes.
  const [n, setN] = useState(0);
  const k = view.data;
  const copy = (text: string, said: string) =>
    void navigator.clipboard?.writeText(text).then(
      () => toast(said),
      () => toast("Could not copy. Select the words and copy them.", "error"),
    );
  return (
    <section className="card" aria-labelledby="your-chat-link" data-testid="your-chat-link">
      <h2 id="your-chat-link">Your chat link</h2>
      <p className="hint">Customers who open it chat with you here, in the Taskiem app or their phone's browser. Share it on your shop, Instagram, a flyer or a receipt.</p>
      {!k && !view.error && <Skeleton rows={2} label="Loading your chat link…" />}
      <ErrorBox error={view.error ?? act.error} />
      {k && (
        <div className="chat-link-box">
          <img className="chat-link-qr" src={`${k.qr}?v=${n}`} alt={`QR code for your chat link, ${k.url}`} width={160} height={160} data-testid="chat-link-qr" />
          <div className="grow">
            <p className="chat-link-url">
              <a href={k.url} target="_blank" rel="noopener noreferrer" data-testid="chat-link-url">
                {k.url}
              </a>
            </p>
            <Field label="Words to paste">
              <textarea readOnly rows={3} value={k.words} data-testid="chat-link-words" />
            </Field>
            <div className="row-actions">
              <button type="button" className="primary" onClick={() => copy(k.words, "Copied. Paste it where your customers will see it.")}>
                Copy the words
              </button>
              <button type="button" onClick={() => copy(k.url, "Link copied")}>
                Copy the link
              </button>
              <a className="button" href={`${k.qr}?format=png`} download="chat-link-qr.png">
                Download the QR code
              </a>
              {can("storefront.manage") && (
                <button
                  type="button"
                  className="danger"
                  disabled={act.busy}
                  data-testid="new-chat-link"
                  onClick={async () => {
                    const yes = await sure.confirm({
                      title: "Make a new link?",
                      body: "Your old link stops working: for 30 days it says the link no longer works. Printed QR codes and shared posts with the old link must be changed.",
                      ok: "Make a new link",
                      danger: true,
                    });
                    if (!yes) return;
                    await act.run(async () => {
                      await post("/v1/customers/chat-link");
                      setN((x) => x + 1);
                      view.reload();
                    }, "Your new link is ready");
                  }}
                >
                  Make a new link
                </button>
              )}
            </div>
          </div>
        </div>
      )}
      {sure.node}
    </section>
  );
}

/** Whether the business takes chats, its shop's button, and the link at
 * the end of updates on Taskiem's shared WhatsApp number. */
function TakingChats() {
  const view = useLoad(() => get<ChatLink>("/v1/customers/chat-link").then((k) => k.settings), []);
  const [st, setSt] = useState<ChatLinkSettings>();
  const act = useAction();
  const s = st ?? view.data;
  // What was chosen shows at once; the saved settings once they are back.
  const save = (body: Partial<ChatLinkSettings>) => {
    const was = s;
    if (s) setSt({ ...s, ...body, ...(body.link_in_updates !== undefined ? { updates_link: !!body.link_in_updates } : {}) });
    void act
      .run(async () => {
        setSt(await put<ChatLinkSettings>("/v1/customers/chat-settings", body));
      }, "Saved")
      .then((ok) => ok || setSt(was));
  };
  return (
    <section className="card" aria-labelledby="taking-chats" data-testid="taking-chats">
      <h2 id="taking-chats">Taking chats</h2>
      {!s && !view.error && <Skeleton rows={2} label="Loading…" />}
      <ErrorBox error={view.error ?? act.error} />
      {s && (
        <>
          {s.orders_off && (
            <div className="warning" role="note" data-testid="orders-off">
              {ORDERS_OFF_WORDS} <Link to="/settings/areas">Your areas</Link>
            </div>
          )}
          <label className="switch-line">
            <input type="checkbox" checked={s.takes_chats} disabled={act.busy || !!s.orders_off} onChange={(e) => save({ takes_chats: e.target.checked })} data-testid="takes-chats" />{" "}
            <span>Customers can start a chat with you</span>
          </label>
          <p className="hint" data-testid="takes-chats-hint">{takesChatsHint(s)}</p>
          <fieldset className="pc-choice" disabled={act.busy}>
            <legend>On your shop's page</legend>
            {(["both", "taskiem", "whatsapp"] as const).map((b) => (
              <label key={b} className="switch-line">
                <input type="radio" name="storefront-button" checked={s.storefront_button === b} onChange={() => save({ storefront_button: b })} /> <span>{buttonWords(b)}</span>
              </label>
            ))}
          </fieldset>
          <label className="switch-line">
            <input
              type="checkbox"
              checked={s.updates_link}
              disabled={act.busy}
              onChange={(e) => save({ link_in_updates: e.target.checked })}
              data-testid="updates-link"
            />{" "}
            <span>End updates to customers on Taskiem's WhatsApp number with your chat link</span>
          </label>
          <p className="hint">
            "Reply in the Taskiem app", then your link, so customers can answer you.
            {s.link_in_updates === null ? " On by default when you have no WhatsApp number of your own." : ""}
          </p>
          {s.own_number && (
            <>
              <label className="switch-line">
                <input
                  type="checkbox"
                  checked={s.link_whatsapp ?? true}
                  disabled={act.busy}
                  onChange={(e) => save({ link_whatsapp: e.target.checked })}
                  data-testid="link-whatsapp"
                />{" "}
                <span>Offer WhatsApp on your chat link</span>
              </label>
              <p className="hint">
                Your link offers Chat on WhatsApp, on your own number {phoneWords(s.own_number)}, beside Chat on Taskiem. A customer on both is one chat here.
              </p>
            </>
          )}
        </>
      )}
    </section>
  );
}

/** The closed reply and its words, Needs you, and telling the owner. */
function WhenNobodyAnswers({ editable }: { editable: boolean }) {
  const view = useLoad(() => get<Replies>("/v1/settings/customer-chats"), []);
  const [st, setSt] = useState<Replies>();
  const [words, setWords] = useState<string>();
  const act = useAction();
  const s = st ?? view.data;
  // What was chosen shows at once; the saved settings once they are back.
  const save = (body: Partial<Replies>) => {
    const was = s;
    if (s) setSt({ ...s, ...body });
    void act
      .run(async () => {
        setSt(await put<Replies>("/v1/settings/customer-chats", body));
        // Words typed and not saved yet stay in the box when a switch is
        // turned; only saving the words themselves ends their edit.
        if (body.closed_words !== undefined) setWords(undefined);
      }, "Saved")
      .then((ok) => ok || setSt(was));
  };
  return (
    <section className="card" aria-labelledby="nobody-answers" data-testid="nobody-answers">
      <h2 id="nobody-answers">When nobody answers</h2>
      {!s && !view.error && <Skeleton rows={2} label="Loading…" />}
      <ErrorBox error={view.error ?? act.error} />
      {s && (
        <>
          <label className="switch-line">
            <input type="checkbox" checked={s.closed_reply} disabled={!editable || act.busy} onChange={(e) => save({ closed_reply: e.target.checked })} data-testid="closed-reply" />{" "}
            <span>Answer when you are closed</span>
          </label>
          <p className="hint">Once a day, in your name, to a customer who writes while your shop's hours say you are closed.</p>
          {s.closed_reply && (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                save({ closed_words: (words ?? s.closed_words).trim() });
              }}
            >
              <Field label="Your words" hint="Leave it empty for ours: We're closed now. We open at 9:00 and will answer then.">
                <textarea rows={2} maxLength={500} value={words ?? s.closed_words} disabled={!editable} onChange={(e) => setWords(e.target.value)} data-testid="closed-words" />
              </Field>
              {editable && (
                <button type="submit" disabled={act.busy || words === undefined}>
                  Save the words
                </button>
              )}
            </form>
          )}
          <label className="switch-line">
            <input type="checkbox" checked={s.needs_you} disabled={!editable || act.busy} onChange={(e) => save({ needs_you: e.target.checked })} data-testid="needs-you" />{" "}
            <span>Put a customer waiting 30 minutes on Needs you</span>
          </label>
          <label className="switch-line">
            <input type="checkbox" checked={s.tell_owner} disabled={!editable || act.busy} onChange={(e) => save({ tell_owner: e.target.checked })} data-testid="tell-owner" />{" "}
            <span>Tell the owner when a customer waits 2 hours</span>
          </label>
        </>
      )}
    </section>
  );
}

/** An offer to every customer who said yes to offers, at most two a week each. */
export function SendOffer({ onSent }: { onSent?: () => void }) {
  const [text, setText] = useState("");
  const [said, setSaid] = useState<string>();
  const act = useAction();
  const sure = useConfirm();
  // One offer, one client_id: sent again after a failure (a dropped
  // connection that may have got through) it is not sent twice. New words
  // are a new offer.
  const client = useRef<{ words: string; id: string } | undefined>(undefined);
  return (
    <section className="card" aria-labelledby="send-offer" data-testid="send-offer">
      <h2 id="send-offer">Send an offer</h2>
      <p className="hint">Goes to the customers in the Taskiem app who said yes to your offers, marked Offer. Each gets at most two a week; those who had two this week are skipped.</p>
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          const words = text.trim();
          if (!words) return;
          const yes = await sure.confirm({ title: "Send this offer?", body: words, ok: "Send" });
          if (!yes) return;
          if (client.current?.words !== words) client.current = { words, id: crypto.randomUUID() };
          const id = client.current.id;
          await act.run(async () => {
            const r = await post<{ sent: number; skipped: number }>("/v1/customer-chats/offers", { text: words, client_id: id });
            client.current = undefined;
            setSaid(offerCount(r.sent, r.skipped));
            setText("");
            onSent?.();
          }, false);
        }}
      >
        <Field label="Your offer">
          <textarea rows={3} maxLength={1000} value={text} onChange={(e) => setText(e.target.value)} data-testid="offer-text" />
        </Field>
        <ErrorBox error={act.error} />
        {said && (
          <p className="notice" role="status" data-testid="offer-sent">
            {said}
          </p>
        )}
        <button type="submit" className="primary" disabled={act.busy || !text.trim()}>
          {act.busy ? "Sending…" : "Send to customers"}
        </button>
      </form>
      {sure.node}
    </section>
  );
}

/** Tools > Customers in the chat: Your chat link, and an offer. */
export function CustomersSheet({ from, onClose }: { from?: DOMRect | null; onClose: () => void }) {
  return (
    <Sheet title="Customers" from={from} onClose={onClose} testid="customers-sheet" headExtra={<Link to="/settings/customers">Open settings</Link>}>
      <YourChatLink />
      <SendOffer />
    </Sheet>
  );
}
