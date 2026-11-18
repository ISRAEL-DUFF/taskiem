import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ApiError, del, get, post, put } from "../api";
import { useAuth } from "../auth";
import { toast } from "../toast";
import { Icon } from "../icons";
import { KitField, Sheet } from "../kit";
import { listTime } from "../lib/linkchat";
import {
  AUDIENCE_WORDS,
  canSendTo,
  CONTACTS_ON_PHONE,
  KEPT_WORDS,
  lastSeenWords,
  linkCode,
  photoOf,
  PRIVACY_ROWS,
  PRIVACY_SWITCHES,
  rowPreview,
  sendProblem,
  shownName,
  startWords,
  type Audience,
  type BlockedPerson,
  type CardAsk,
  type PayoutAccount,
  type PeopleChat,
  type PeopleChatList,
  type PeoplePrivacy,
  type PersonCard,
  type PersonContact,
  type PersonFound,
  type PersonLink,
  type PersonLinkCard,
  type RequestList,
} from "../lib/people";
import { usePagedList } from "../lib/usePagedList";
import { requestsWithChat, requestsWithGone } from "../lib/peoplelive";
import { livePoll, usePeopleLive } from "./PeopleLive";
import { EmptyState, ErrorBox, fmtTime, Items, Modal, PageHeader, Row, Skeleton, useAction, useLoad } from "../ui";
import { ChatAvatar } from "./ChatAvatar";
import { ChatRow } from "./ChatRow";
import { useConfirm } from "./ConfirmSheet";
import { Copy } from "./Forms";
import { MoreRows } from "./MoreRows";
import { AccountFields } from "./PeopleChat";
import { useSwitch } from "./Switch";

// Finding people and the settings of chats between people, on You
// (docs/spec/people-chatting.md §5, §7, §8.1, §11): New chat (by number,
// by a person's link, your own link and QR code), Requests, contacts,
// blocked people, the privacy settings, the bank account for share cards,
// a person's link /p/{code}, and sharing a card into a chat from elsewhere.

/** Opens (or finds) the chat with someone found, and goes to it. */
function useStartChat() {
  const nav = useNavigate();
  const [problem, setProblem] = useState<string>();
  const [busy, setBusy] = useState(false);
  const start = async (body: { person_id: string } | { link_code: string }) => {
    setProblem(undefined);
    setBusy(true);
    try {
      // A chat just opened is in no list until its first message: it is
      // opened from this answer, by its id.
      const c = await post<PeopleChat>("/v1/me/people", body);
      nav(`/you/people/${encodeURIComponent(c.id)}`, { state: { chat: c } });
    } catch (e) {
      setProblem(startWords(e));
    } finally {
      setBusy(false);
    }
  };
  return { start, problem, busy };
}

/** Someone found by number or link: Message, and Add to contacts. */
function FoundPerson({ f, from, linkCode: code }: { f: PersonFound; from: "number" | "link"; linkCode?: string }) {
  const p = f.person as PersonCard;
  const chat = useStartChat();
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [added, setAdded] = useState(p.in_contacts);
  const act = useAction();
  const seen = lastSeenWords(p);
  return (
    <div className="people-found" data-testid="people-found">
      <div className="people-found-who">
        <ChatAvatar name={p.name} logo={photoOf(p)} />
        <div className="grow">
          <strong data-testid="people-found-name">{shownName(p)}</strong>
          {seen && <div className="hint">{seen}</div>}
          {added && <div className="hint">In your contacts</div>}
        </div>
      </div>
      {f.self ? (
        <p className="hint" data-testid="people-found-self">
          That's you.
        </p>
      ) : (
        <>
          <div className="row-actions">
            <button type="button" className="primary" disabled={chat.busy} onClick={() => void chat.start(code ? { link_code: code } : { person_id: p.id })} data-testid="people-found-message">
              <Icon name="chat" /> Message
            </button>
            {!added && f.add_token && !adding && (
              <button type="button" onClick={() => setAdding(true)} data-testid="people-found-add">
                Add to contacts
              </button>
            )}
          </div>
          {adding && (
            <form
              className="pc-check"
              onSubmit={(e) => {
                e.preventDefault();
                void act.run(async () => {
                  await post("/v1/me/contacts", { person_id: p.id, add_token: f.add_token, from, name: name.trim() || undefined });
                  setAdded(true);
                  setAdding(false);
                }, "Added to your contacts");
              }}
            >
              <KitField label="Their name in your contacts" hint="Optional">
                <input value={name} maxLength={120} placeholder={p.name} onChange={(e) => setName(e.target.value)} data-testid="people-found-name-input" />
              </KitField>
              <button type="submit" className="primary" disabled={act.busy} data-testid="people-found-save">
                Add
              </button>
            </form>
          )}
          {chat.problem && (
            <p className="error" role="alert" data-testid="people-start-problem">
              {chat.problem}
            </p>
          )}
          <ErrorBox error={act.error} />
        </>
      )}
    </div>
  );
}

/** Not on Taskiem: the words to invite them, sent from this device. */
function InviteThem({ invite }: { invite: NonNullable<PersonFound["invite"]> }) {
  return (
    <div className="people-found" data-testid="people-not-found">
      <strong>Not on Taskiem yet</strong>
      <p className="hint">Invite them: the message goes from your own phone. Taskiem sends nothing.</p>
      <div className="row-actions">
        <a className="button" href={`https://wa.me/?text=${encodeURIComponent(invite.words)}`} target="_blank" rel="noopener noreferrer" data-testid="people-invite-whatsapp">
          Invite on WhatsApp
        </a>
        <a className="button" href={`sms:?&body=${encodeURIComponent(invite.words)}`} data-testid="people-invite-sms">
          Invite by text
        </a>
        <Copy text={invite.words} label="Copy the invitation" />
      </div>
    </div>
  );
}

/** The person's own link and QR code: Copy, Share and Replace. */
export function YourLink() {
  const view = useLoad(() => get<PersonLink>("/v1/me/link"), []);
  const [l, setL] = useState<PersonLink>();
  const [v, setV] = useState(0);
  const act = useAction();
  const sure = useConfirm();
  const link = l ?? view.data;
  const share = async () => {
    if (!link) return;
    const nav = navigator as Navigator & { share?: (d: { text: string; url: string }) => Promise<void> };
    if (nav.share) await nav.share({ text: link.words, url: link.url }).catch(() => undefined);
    else window.open(`https://wa.me/?text=${encodeURIComponent(link.words)}`, "_blank", "noopener");
  };
  return (
    <div className="people-link" data-testid="people-your-link">
      {!link && !view.error && <Skeleton rows={1} label="Loading your link…" />}
      <ErrorBox error={view.error ?? act.error} />
      {link && (
        <>
          <p className="hint">Share it, or let someone scan the code: it opens a chat with you.</p>
          <img className="people-qr" src={`${link.qr}?v=${v}-${encodeURIComponent(link.code)}`} alt={`QR code for your link ${link.url}`} width={168} height={168} data-testid="people-qr" />
          <div className="email-address">
            <code data-testid="people-link-url">{link.url}</code>
          </div>
          <div className="row-actions">
            <Copy text={link.url} label="Copy your link" />
            <button type="button" className="small" onClick={() => void share()}>
              Share
            </button>
            <button
              type="button"
              className="small"
              disabled={act.busy}
              data-testid="people-link-replace"
              onClick={() =>
                void sure
                  .confirm({ title: "Replace your link?", body: "Your old link and its code stop working. People who already chat with you keep chatting.", ok: "Replace link" })
                  .then(
                    (yes) =>
                      yes &&
                      act.run(async () => {
                        setL(await post<PersonLink>("/v1/me/link"));
                        setV((x) => x + 1);
                      }, "New link made"),
                  )
              }
            >
              Replace link
            </button>
          </div>
        </>
      )}
      {sure.node}
    </div>
  );
}

/** New chat: find someone by number or by their link, or share your own. */
export function NewChatSheet({ onClose }: { onClose: () => void }) {
  const [phone, setPhone] = useState("");
  const [link, setLink] = useState("");
  const [found, setFound] = useState<{ f: PersonFound; from: "number" | "link"; code?: string }>();
  const [mine, setMine] = useState(false);
  const act = useAction();
  const byNumber = (e: FormEvent) => {
    e.preventDefault();
    setFound(undefined);
    void act.run(async () => setFound({ f: await post<PersonFound>("/v1/people/lookup", { phone }), from: "number" }), false);
  };
  const byLink = (e: FormEvent) => {
    e.preventDefault();
    setFound(undefined);
    const code = linkCode(link);
    void act.run(async () => {
      if (!code) throw new ApiError(400, "That is not a person's link. It looks like https://…/p/ and ten letters and numbers.");
      try {
        setFound({ f: await get<PersonFound>(`/v1/people/links/${encodeURIComponent(code)}`), from: "link", code });
      } catch (err) {
        if (err instanceof ApiError && (err.status === 410 || err.status === 404)) throw new ApiError(err.status, startWords(err));
        throw err;
      }
    }, false);
  };
  return (
    <Sheet title="New chat" onClose={onClose} testid="people-new-chat">
      <div className="pc-check">
        <form className="pc-check" onSubmit={byNumber} aria-labelledby="people-by-number">
          <h3 id="people-by-number">By phone number</h3>
          <KitField label="Their phone number">
            <input type="tel" inputMode="tel" autoComplete="off" value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="0803 123 4567" data-testid="people-phone" />
          </KitField>
          <button type="submit" className="primary" disabled={!phone.trim() || act.busy} data-testid="people-find">
            Find
          </button>
        </form>
        <form className="pc-check" onSubmit={byLink} aria-labelledby="people-by-link">
          <h3 id="people-by-link">By their link</h3>
          <KitField label="Their Taskiem link">
            <input value={link} onChange={(e) => setLink(e.target.value)} placeholder="https://…/p/…" data-testid="people-link-input" />
          </KitField>
          <button type="submit" disabled={!link.trim() || act.busy} data-testid="people-open-link">
            Open
          </button>
        </form>
        <ErrorBox error={act.error} />
        {found?.f.found && found.f.person && <FoundPerson f={found.f} from={found.from} linkCode={found.code} />}
        {found && !found.f.found && found.f.invite && <InviteThem invite={found.f.invite} />}
        <div>
          <h3>Your link</h3>
          {mine ? (
            <YourLink />
          ) : (
            <button type="button" onClick={() => setMine(true)} data-testid="people-show-link">
              Show your link and QR code
            </button>
          )}
        </div>
        <p className="hint" data-testid="people-contacts-phone">
          {CONTACTS_ON_PHONE}
        </p>
        <p className="hint">
          <Link to="/you/people/contacts">Your contacts</Link>
        </p>
      </div>
    </Sheet>
  );
}

/** /you/people/requests: strangers' first messages, waiting. */
export function RequestsPage() {
  // Read again every little while, as the Chats list is, a page at a time
  // (slowly while the live connection tells what changes).
  const live = usePeopleLive();
  const list = usePagedList<RequestList, PeopleChat>("/v1/me/people/requests", (r) => r.requests ?? [], (c) => c.id, livePoll(live.connected, 8000));
  const { patch, reload } = list;
  useEffect(
    () =>
      live.subscribe((e) => {
        if (e.type === "chat") patch((rows) => requestsWithChat(rows, e.data));
        else if (e.type === "gone") patch((rows) => requestsWithGone(rows, e.data));
        else if (e.type === "ready") reload();
      }),
    [live.subscribe, patch, reload],
  );
  const reqs = list.rows ?? [];
  return (
    <>
      <p className="crumbs">
        <Link to="/you" aria-label="Back to Chats">
          Chats
        </Link>
      </p>
      <PageHeader title="Message requests" description="People you haven't chatted with. They are not told you read their message unless you accept." />
      <section className="card person-chats" aria-label="Message requests">
        <ErrorBox error={list.error} />
        {!list.rows && !list.error && <Skeleton rows={2} label="Loading your requests…" />}
        {list.rows && reqs.length === 0 && <EmptyState icon="inbox">No requests waiting.</EmptyState>}
        {reqs.length > 0 && (
          <ul className="chat-rows" aria-label="Requests" data-testid="people-requests">
            {reqs.map((c) => (
              <ChatRow
                key={c.id}
                to={`/you/people/requests/${encodeURIComponent(c.id)}`}
                letter={c.name}
                logo={photoOf(c.person)}
                title={c.name}
                time={listTime(c.last_at)}
                preview={rowPreview(c)}
                unread={c.unread}
                testid="people-request"
              />
            ))}
          </ul>
        )}
        <MoreRows more={list.more} busy={list.loadingMore} onMore={list.loadMore} count={list.rows?.length ?? 0} label="Show more requests" testid="people-requests-more" />
      </section>
    </>
  );
}

/** /you/people/contacts: the people the person added. */
export function ContactsPage() {
  const list = useLoad(() => get<{ contacts: PersonContact[] }>("/v1/me/contacts"), []);
  const [finding, setFinding] = useState(false);
  const [naming, setNaming] = useState<PersonContact>();
  const [name, setName] = useState("");
  const act = useAction();
  const sure = useConfirm();
  const chat = useStartChat();
  const people = list.data?.contacts ?? [];
  return (
    <>
      <p className="crumbs">
        <Link to="/you" aria-label="Back to Chats">
          Chats
        </Link>
      </p>
      <PageHeader
        title="Your contacts"
        description="People you added. Only you see this list."
        actions={
          <button type="button" className="primary" onClick={() => setFinding(true)} data-testid="contacts-add">
            <Icon name="plus" /> Add someone
          </button>
        }
      />
      <section className="card" aria-label="Your contacts">
        <ErrorBox error={list.error ?? act.error} />
        {chat.problem && (
          <p className="error" role="alert">
            {chat.problem}
          </p>
        )}
        {!list.data && !list.error && <Skeleton rows={2} label="Loading your contacts…" />}
        {list.data && people.length === 0 && <EmptyState icon="members">No contacts yet. Find someone by their number or their link.</EmptyState>}
        {people.length > 0 && (
          <Items label="Contacts">
            {people.map((p) => (
              <Row
                key={p.id}
                letter={shownName(p)}
                image={photoOf(p)}
                title={shownName(p)}
                sub={<span className="sub">{[p.contact_name && !p.deleted ? p.name : "", lastSeenWords(p)].filter(Boolean).join(" · ") || `Added ${fmtTime(p.added_at)}`}</span>}
                testid="people-contact"
              >
                {!p.deleted && (
                  <button type="button" className="small primary" disabled={chat.busy} onClick={() => void chat.start({ person_id: p.id })}>
                    Message<span className="sr-only"> {shownName(p)}</span>
                  </button>
                )}
                <button
                  type="button"
                  className="small"
                  onClick={() => {
                    setName(p.contact_name ?? "");
                    setNaming(p);
                  }}
                >
                  Rename<span className="sr-only"> {shownName(p)}</span>
                </button>
                <button
                  type="button"
                  className="small danger"
                  data-testid="people-contact-remove"
                  onClick={() =>
                    void sure.confirm({ title: `Remove ${shownName(p)}?`, body: "They are not told. Your chat with them stays.", ok: "Remove", danger: true }).then(
                      (yes) =>
                        yes &&
                        act.run(async () => {
                          await del(`/v1/me/contacts/${encodeURIComponent(p.id)}`);
                          list.reload();
                        }, "Removed"),
                    )
                  }
                >
                  Remove<span className="sr-only"> {shownName(p)}</span>
                </button>
              </Row>
            ))}
          </Items>
        )}
        <p className="hint">{CONTACTS_ON_PHONE}</p>
      </section>
      {naming && (
        <Modal title={`Rename ${shownName(naming)}`} onClose={() => setNaming(undefined)}>
          <form
            className="pc-check"
            onSubmit={(e) => {
              e.preventDefault();
              void act.run(async () => {
                await put(`/v1/me/contacts/${encodeURIComponent(naming.id)}`, { name: name.trim() || null });
                setNaming(undefined);
                list.reload();
              }, "Name saved");
            }}
          >
            <KitField label="Their name in your contacts" hint={`Empty: their own name, ${naming.name}`}>
              <input value={name} maxLength={120} onChange={(e) => setName(e.target.value)} data-testid="people-contact-name" autoFocus />
            </KitField>
            <div className="row-actions">
              <button type="button" onClick={() => setNaming(undefined)}>
                Cancel
              </button>
              <button type="submit" className="primary" disabled={act.busy}>
                Save
              </button>
            </div>
          </form>
        </Modal>
      )}
      {finding && (
        <NewChatSheet
          onClose={() => {
            setFinding(false);
            list.reload();
          }}
        />
      )}
      {sure.node}
    </>
  );
}

/** /you/settings/blocked: the people the person blocked. */
export function BlockedPage() {
  const list = useLoad(() => get<{ people: BlockedPerson[] }>("/v1/me/people/blocked"), []);
  const act = useAction();
  const people = list.data?.people ?? [];
  return (
    <>
      <p className="crumbs">
        <Link to="/you/settings" aria-label="Back to Settings">
          Settings
        </Link>
      </p>
      <PageHeader title="Blocked people" description="They can't message you or see when you were last here or your photo. They are not told." />
      <section className="card" aria-label="Blocked people">
        <ErrorBox error={list.error ?? act.error} />
        {!list.data && !list.error && <Skeleton rows={2} label="Loading…" />}
        {list.data && people.length === 0 && <EmptyState icon="lock">You haven't blocked anyone.</EmptyState>}
        {people.length > 0 && (
          <Items label="Blocked people">
            {people.map((p) => (
              <Row key={p.person_id} letter={p.name} title={p.name} sub={<span className="sub">Blocked {fmtTime(p.at)}</span>} testid="people-blocked">
                <button
                  type="button"
                  className="small"
                  disabled={act.busy}
                  onClick={() =>
                    void act.run(async () => {
                      await del(`/v1/me/people/${encodeURIComponent(p.chat_id)}/block`);
                      list.reload();
                    }, `${p.name} can write to you again`)
                  }
                >
                  Unblock<span className="sr-only"> {p.name}</span>
                </button>
              </Row>
            ))}
          </Items>
        )}
      </section>
    </>
  );
}

/** /you/settings/people: who can find me, message me, see me (§5.4). */
export function PeoplePrivacyPage() {
  const view = useLoad(() => get<PeoplePrivacy>("/v1/me/privacy/people"), []);
  const [now, setNow] = useState<PeoplePrivacy>();
  const act = useAction();
  const p = now ?? view.data;
  const set = (body: Partial<PeoplePrivacy>) => {
    if (!p) return;
    const was = p;
    setNow({ ...p, ...body });
    void act.run(async () => setNow(await put<PeoplePrivacy>("/v1/me/privacy/people", body)), "Saved").then((ok) => ok || setNow(was));
  };
  return (
    <>
      <p className="crumbs">
        <Link to="/you/settings" aria-label="Back to Settings">
          Settings
        </Link>
      </p>
      <PageHeader title="Privacy with people" description="Who can find you, message you, and see you, in chats with people." />
      {!p && !view.error && <Skeleton rows={3} label="Loading your settings…" />}
      <ErrorBox error={view.error ?? act.error} />
      {p && (
        <section className="card" aria-label="Your settings" data-testid="people-privacy">
          {PRIVACY_ROWS.map((r) => (
            <fieldset key={r.key} className="pc-choice people-setting" data-testid={`privacy-${r.key}`}>
              <legend>{r.label}</legend>
              <span className="hint">{r.hint}</span>
              {r.choices.map((ch) => (
                <label key={ch.value} className="switch-line">
                  <input type="radio" name={`privacy-${r.key}`} checked={p[r.key] === ch.value} disabled={act.busy} onChange={() => set({ [r.key]: ch.value as Audience })} /> <span>{ch.words}</span>
                </label>
              ))}
            </fieldset>
          ))}
          {PRIVACY_SWITCHES.map((s) => (
            <label key={s.key} className="switch-line you-notify-line">
              <input type="checkbox" role="switch" checked={p[s.key]} disabled={act.busy} onChange={(e) => set({ [s.key]: e.target.checked })} data-testid={`privacy-${s.key}`} />{" "}
              <span>
                {s.label}
                <span className="hint block">{s.hint}</span>
              </span>
            </label>
          ))}
          <p className="hint">Who can message you now: {AUDIENCE_WORDS[p.message]}.</p>
        </section>
      )}
      <section className="card" aria-labelledby="people-kept-h">
        <h2 id="people-kept-h">How your chats are kept</h2>
        <p data-testid="people-privacy-kept">{KEPT_WORDS}</p>
        <p className="hint">
          <Link to="/transparency" data-testid="people-privacy-transparency">
            How many requests from the authorities Taskiem received
          </Link>
        </p>
        <p className="hint">
          <Link to="/you/settings/standing" data-testid="people-privacy-standing">
            Your standing: what Taskiem's moderators decided about you, and how to appeal
          </Link>
        </p>
      </section>
    </>
  );
}

/** /you/settings/payout: the bank account share cards ask to be paid into. */
export function PayoutPage() {
  const view = useLoad(() => get<{ account: PayoutAccount | null; words: string }>("/v1/me/payout-account"), []);
  const [acct, setAcct] = useState<PayoutAccount>({ bank: "", account_number: "", account_name: "" });
  const act = useAction();
  const sure = useConfirm();
  useEffect(() => {
    if (view.data?.account) setAcct(view.data.account);
  }, [view.data?.account]);
  return (
    <>
      <p className="crumbs">
        <Link to="/you/settings" aria-label="Back to Settings">
          Settings
        </Link>
      </p>
      <PageHeader title="Your bank account" description="Shown on the 'Your share' cards you send, so friends can pay you back." />
      <section className="card" aria-label="Your bank account" data-testid="people-payout">
        {!view.data && !view.error && <Skeleton rows={2} label="Loading…" />}
        <ErrorBox error={view.error ?? act.error} />
        {view.data && (
          <form
            className="pc-check"
            onSubmit={(e) => {
              e.preventDefault();
              void act.run(async () => {
                await put("/v1/me/payout-account", { ...acct, account_number: acct.account_number.replace(/[\s-]/g, "") });
                view.reload();
              }, "Saved");
            }}
          >
            <p className="hint">{view.data.words || "Taskiem does not check this account and moves no money."}</p>
            <AccountFields value={acct} onChange={setAcct} />
            <div className="row-actions">
              <button type="submit" className="primary" disabled={act.busy} data-testid="payout-save">
                Save
              </button>
              {view.data.account && (
                <button
                  type="button"
                  className="danger"
                  disabled={act.busy}
                  onClick={() =>
                    void sure.confirm({ title: "Remove your bank account?", body: "Cards you already sent keep the account they show.", ok: "Remove", danger: true }).then(
                      (yes) =>
                        yes &&
                        act.run(async () => {
                          await del("/v1/me/payout-account");
                          setAcct({ bank: "", account_number: "", account_name: "" });
                          view.reload();
                        }, "Removed"),
                    )
                  }
                >
                  Remove
                </button>
              )}
            </div>
          </form>
        )}
      </section>
      {sure.node}
    </>
  );
}

/** You's settings: the rows for chats with people. */
export function YouPeopleSettings() {
  const [link, setLink] = useState(false);
  const rows: { to: string; title: string; sub: string; testid: string }[] = [
    { to: "/you/settings/people", title: "Privacy with people", sub: "Who can find you, message you, and see when you were last here.", testid: "you-people-privacy" },
    { to: "/you/people/contacts", title: "Your contacts", sub: "People you added.", testid: "you-people-contacts" },
    { to: "/you/settings/blocked", title: "Blocked people", sub: "People who can't message you.", testid: "you-people-blocked" },
    { to: "/you/settings/payout", title: "Your bank account", sub: "For 'Your share' cards. Taskiem moves no money.", testid: "you-people-payout" },
    { to: "/you/people/starred", title: "Starred messages", sub: "Messages you starred in your chats with people.", testid: "you-people-starred" },
    { to: "/you/settings/standing", title: "Your standing", sub: "What Taskiem's moderators decided, and appeals.", testid: "you-people-standing" },
  ];
  return (
    <section className="card" aria-labelledby="you-people">
      <h2 id="you-people">Chats with people</h2>
      {rows.map((r) => (
        <div key={r.to} className="setting-line">
          <div className="grow">
            <strong>{r.title}</strong>
            <div className="sub">{r.sub}</div>
          </div>
          <Link className="button" to={r.to} data-testid={r.testid}>
            Open<span className="sr-only"> {r.title}</span>
          </Link>
        </div>
      ))}
      <div className="setting-line">
        <div className="grow">
          <strong>Your link and QR code</strong>
          <div className="sub">Anyone with it can start a chat with you.</div>
        </div>
        <button type="button" className="button" aria-expanded={link} onClick={() => setLink(!link)} data-testid="you-people-link">
          {link ? "Hide" : "Show"}
          <span className="sr-only"> your link and QR code</span>
        </button>
      </div>
      {link && <YourLink />}
    </section>
  );
}

/** Share a card (a business, a product, an order) into a chat with a
 * person, from elsewhere on You: pick what (when there is a choice, as an
 * order or its delivery) and the chat, and it is sent. A refusal (held by
 * the checks, not taken) is said here, in the server's words. */
export function ShareToPerson({
  card,
  what,
  options,
  onClose,
}: {
  card: CardAsk;
  what: string;
  /** Another choice of what to send (an order, or its delivery). */
  options?: { label: string; card: CardAsk }[];
  onClose: () => void;
}) {
  const list = usePagedList<PeopleChatList, PeopleChat>("/v1/me/people", (r) => r.chats ?? [], (c) => c.id);
  const nav = useNavigate();
  const act = useAction();
  const [pick, setPick] = useState(0);
  const [problem, setProblem] = useState<string>();
  const choices = options && options.length > 1 ? options : undefined;
  const sending = choices ? (choices[pick]?.card ?? card) : card;
  const chats = (list.rows ?? []).filter(canSendTo);
  return (
    <Sheet title={`Share ${what}`} onClose={onClose} testid="share-to-person">
      {!list.rows && !list.error && <Skeleton rows={2} label="Loading your chats…" />}
      <ErrorBox error={list.error ?? act.error} />
      {problem && (
        <p className="error" role="alert" data-testid="share-to-problem">
          {problem}
        </p>
      )}
      {choices && (
        <fieldset className="pc-choice" data-testid="share-to-what">
          <legend>What to share</legend>
          {choices.map((o, i) => (
            <label key={o.label} className="switch-line">
              <input type="radio" name="share-to-what" checked={pick === i} onChange={() => setPick(i)} /> <span>{o.label}</span>
            </label>
          ))}
        </fieldset>
      )}
      {list.rows && chats.length === 0 && !list.more && <EmptyState icon="chat">Start a chat with someone first: New chat on your Chats.</EmptyState>}
      <Items label="Your chats with people">
        {chats.map((c) => (
          <Row key={c.id} letter={c.name} image={photoOf(c.person)} title={c.name} testid="share-to-row">
            <button
              type="button"
              className="small primary"
              disabled={act.busy}
              onClick={() => {
                setProblem(undefined);
                let refused = false;
                void act
                  .run(async () => {
                    try {
                      await post(`/v1/me/people/${encodeURIComponent(c.id)}/messages`, { card: sending, client_id: `share-${Date.now()}` });
                    } catch (e) {
                      const said = sendProblem(e);
                      if (!said) throw e;
                      refused = true;
                      setProblem(said);
                    }
                  }, false)
                  .then((ok) => {
                    if (!ok || refused) return;
                    toast(`Shared with ${c.name}`);
                    nav(`/you/people/${encodeURIComponent(c.id)}`);
                  });
              }}
            >
              Send<span className="sr-only"> to {c.name}</span>
            </button>
          </Row>
        ))}
      </Items>
      <MoreRows more={list.more} busy={list.loadingMore} onMore={list.loadMore} count={list.rows?.length ?? 0} label="Show more chats" />
    </Sheet>
  );
}

/** A person's link, /p/{code} (§5.2): their name and photo (when it is
 * everyone's), Chat on Taskiem; signed in on You, Message and Add to
 * contacts. A business's session moves to You first. */
export function PersonLinkPage() {
  const { code = "" } = useParams();
  const { me } = useAuth();
  const nav = useNavigate();
  const personal = !!me && (!!me.personal || (me.tenant_id === null && !me.gate_only));
  const here = `/p/${encodeURIComponent(code)}`;
  const sw = useSwitch(here);
  const card = useLoad(() => (personal ? Promise.resolve(undefined) : get<PersonLinkCard>(`/v1/person-links/${encodeURIComponent(code)}`)), [code, personal]);
  const found = useLoad(() => (personal ? get<PersonFound>(`/v1/people/links/${encodeURIComponent(code)}`) : Promise.resolve(undefined)), [code, personal]);
  const err = card.error ?? found.error;
  const gone = err instanceof ApiError && err.status === 410;
  const unknown = err instanceof ApiError && err.status === 404;
  if (gone || unknown)
    return (
      <div className="login card way-in chat-link-page" data-testid="person-link-gone">
        <h1>{gone ? "This link no longer works" : "Link not found"}</h1>
        <p className="way-in-line">{gone ? "They made a new link. Ask them for it." : "Check the link, or ask them for it."}</p>
        <p className="hint way-in-other">
          <Link to={me ? "/" : "/login"}>{me ? "Go to Taskiem" : "Sign in to Taskiem"}</Link>
        </p>
      </div>
    );
  if (personal) {
    const f = found.data;
    return (
      <section className="card chat-link-page" data-testid="person-link-page">
        <h1 className="sr-only">A person's link</h1>
        {!f && (err ? <ErrorBox error={err} /> : <Skeleton rows={2} label="Opening the link…" />)}
        {f?.person && <FoundPerson f={f} from="link" linkCode={code} />}
      </section>
    );
  }
  const c = card.data;
  if (!c)
    return (
      <div className="login card way-in chat-link-page">
        <h1 className="sr-only">A person's link</h1>
        {err ? <ErrorBox error={err} /> : <Skeleton rows={3} label="Opening the link…" />}
      </div>
    );
  return (
    <div className="login card way-in chat-link-page" data-testid="person-link-page">
      <div className="chat-link-who">
        <ChatAvatar name={c.name} logo={c.photo?.url} className="chat-link-avatar" />
        <div>
          <h1>{c.name}</h1>
          <p className="way-in-line">is on Taskiem</p>
        </div>
      </div>
      <button
        type="button"
        className="primary"
        disabled={sw.busy}
        data-testid="person-link-chat"
        onClick={() => (me ? sw.go("you") : nav(`/login?next=${encodeURIComponent(here)}`))}
      >
        Chat on Taskiem
      </button>
      <ErrorBox error={sw.error} />
      {c.app && (
        <a className="button" href={c.app} target="_blank" rel="noopener noreferrer">
          Get the Taskiem app
        </a>
      )}
    </div>
  );
}

/** /you/people/* opened in a business's session (a tapped notification):
 * the chat is the person's own, on You. */
export function PeopleOnYou() {
  const { "*": rest = "" } = useParams();
  const sw = useSwitch(`/you/people/${rest}`);
  return (
    <section className="card" data-testid="people-on-you">
      <h1>A chat of your own</h1>
      <p>This chat is between you and another person, on your own side of Taskiem (You), not in this business.</p>
      <button type="button" className="primary" disabled={sw.busy} onClick={() => sw.go("you")} data-testid="people-open-on-you">
        Open it on You
      </button>
      <ErrorBox error={sw.error} />
    </section>
  );
}
