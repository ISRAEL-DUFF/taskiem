import { del, get, post } from "../api";
import { useAuth } from "../auth";
import { ErrorBox, fmtTime, useAction, useLoad } from "../ui";
import { Copy } from "./Forms";

interface ReachChannel {
  channel: "telegram" | "whatsapp";
  linked_at: string;
  last_sent_at?: string;
  stopped_at?: string;
}

interface ReachContact {
  id: string;
  phone: string;
  channels: ReachChannel[];
  created_at: string;
}

interface ReachView {
  telegram: boolean;
  whatsapp: boolean;
  page_url?: string;
  contacts: ReachContact[];
}

const APP = { telegram: "Telegram", whatsapp: "WhatsApp" } as const;

/** What the standing link says when shared, e.g. in the staff WhatsApp group. */
export const shareText = (url: string) => `Get our updates on WhatsApp or Telegram: tap ${url} and choose the app you use.`;

/**
 * People outside the business who get its workflows' messages on WhatsApp
 * or Telegram (no-technical-steps N11): the standing link to share with
 * them, and who asked, with their phones masked.
 */
export function PeopleYouMessage() {
  const { can } = useAuth();
  const view = useLoad(() => get<ReachView>("/v1/reach"), []);
  const act = useAction();
  const v = view.data;
  if (!v) return view.error ? <ErrorBox error={view.error} /> : null;
  const on = v.telegram || v.whatsapp;
  const apps = [v.whatsapp && "WhatsApp", v.telegram && "Telegram"].filter(Boolean).join(" or ");
  return (
    <section className="card" data-testid="people-you-message">
      <h2 style={{ marginTop: 0 }}>People you message</h2>
      {!on ? (
        <p className="hint">Your workflows text people outside the business. Updates on WhatsApp and Telegram are not set up on this Taskiem yet.</p>
      ) : (
        <>
          <p className="hint">
            Staff who don't use Taskiem, and your customers, can get your workflows' messages on {apps} instead of by text. They tap a link once and choose
            their app; forms that send people updates offer it on their thank-you page. Nobody needs a chat ID.
          </p>
          {v.page_url && (
            <>
              <h3>Your link</h3>
              <p className="hint">Share it once, for example in your staff WhatsApp group.</p>
              <div className="email-address">
                <code>{v.page_url}</code>
                <Copy text={v.page_url} label="Copy the link" />
                <a className="button small" href={`https://wa.me/?text=${encodeURIComponent(shareText(v.page_url))}`} target="_blank" rel="noopener noreferrer">
                  Share on WhatsApp
                </a>
              </div>
              {can("workflow.publish") && (
                <button
                  type="button"
                  className="small"
                  disabled={act.busy}
                  onClick={() =>
                    confirm("Make a new link? The old one stops working; people who already get your updates keep getting them.") &&
                    void act.run(async () => (await post("/v1/reach/page"), view.reload()))
                  }
                >
                  New link
                </button>
              )}
            </>
          )}
        </>
      )}
      <ErrorBox error={act.error} />
      {v.contacts.length > 0 && (
        <table className="table">
          <thead>
            <tr>
              <th>Phone</th>
              <th>Gets updates on</th>
              <th>Since</th>
              {can("workflow.publish") && (
                <th>
                  <span className="sr-only">Remove</span>
                </th>
              )}
            </tr>
          </thead>
          <tbody>
            {v.contacts.map((c) => (
              <tr key={c.id}>
                <td>{c.phone}</td>
                <td>
                  {c.channels.map((ch) => (
                    <div key={ch.channel}>
                      {APP[ch.channel]}
                      {ch.stopped_at && <span className="hint"> (stopped {fmtTime(ch.stopped_at)})</span>}
                    </div>
                  ))}
                </td>
                <td>{fmtTime(c.created_at)}</td>
                {can("workflow.publish") && (
                  <td>
                    <button
                      type="button"
                      className="small danger"
                      disabled={act.busy}
                      onClick={() =>
                        confirm(`Stop messaging ${c.phone} on chat apps? Your workflows text them instead.`) &&
                        void act.run(async () => (await del(`/v1/reach/contacts/${c.id}`), view.reload()))
                      }
                    >
                      Remove
                    </button>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {on && v.contacts.length === 0 && <p className="hint">Nobody has asked for your updates on a chat app yet.</p>}
    </section>
  );
}
