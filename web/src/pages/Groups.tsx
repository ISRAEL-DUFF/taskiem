import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import { ApiError, del, get, post, put, upload } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { KitField, Sheet } from "../kit";
import { listTime } from "../lib/linkchat";
import {
  addedWords,
  GROUP_KEPT_WORDS,
  GROUP_REPORT_CONSENT,
  groupPath,
  inviteGone,
  invitesWithInvite,
  joinWords,
  membersInOrder,
  membersWords,
  newGroupOpensAtOnce,
  type GroupAdded,
  type Group,
  type GroupInvite,
  type GroupLink,
  type GroupLinkPreview,
  type GroupMember,
  type GroupMembers,
  type GroupPending,
  type JoinState,
} from "../lib/groups";
import { canSendTo, lastSeenWords, phoneWords, photoOf, shownName, type PeopleChat, type PeopleChatList, type PersonContact } from "../lib/people";
import { REPORT_REASONS } from "../lib/personchat";
import { usePagedList } from "../lib/usePagedList";
import { ErrorBox, Items, Modal, Row, Skeleton, useAction, useLoad } from "../ui";
import { ChatAvatar } from "./ChatAvatar";
import { useConfirm } from "./ConfirmSheet";
import { Copy } from "./Forms";
import { MoreRows } from "./MoreRows";
import { usePeopleLive } from "./PeopleLive";
import { useSwitch } from "./Switch";

// People groups on You, beside the chat itself (GroupChat.tsx): making a
// group, its info (members and admins, adding and removing, admins, who
// posts and who edits, the name, photo and description, the invite link
// with its QR code and the joins waiting, mute and mentions, leaving,
// blocking and reporting, a closed group's appeal), invites in Requests,
// and a group's link /g/{code}. Docs: docs/spec/people-chatting.md §9 and
// "As built: stage 5b, groups (server)", "…, group safety (server)".

const NAME_MAX = 80;
const DESCRIPTION_MAX = 500;

/** The people the person may pick to add: their contacts and the people
 * they chat with, each once, leaving out who is already in or invited. */
function usePickable(skip: Set<string>) {
  const contacts = useLoad(() => get<{ contacts: PersonContact[] }>("/v1/me/contacts"), []);
  const chats = usePagedList<PeopleChatList, PeopleChat>("/v1/me/people", (r) => r.chats ?? [], (c) => c.id);
  const people = useMemo(() => {
    const out = new Map<string, { id: string; name: string; photo?: string; contact: boolean }>();
    for (const c of contacts.data?.contacts ?? []) if (!c.deleted && !c.suspended) out.set(c.id, { id: c.id, name: shownName(c), photo: photoOf(c), contact: true });
    for (const c of chats.rows ?? []) if (canSendTo(c) && !out.has(c.person_id)) out.set(c.person_id, { id: c.person_id, name: c.name, photo: photoOf(c.person), contact: false });
    return [...out.values()].filter((p) => !skip.has(p.id)).sort((a, b) => Number(b.contact) - Number(a.contact) || a.name.localeCompare(b.name));
  }, [contacts.data, chats.rows, skip]);
  return { people, loading: !contacts.data && !contacts.error, error: contacts.error ?? chats.error, chats };
}

/** Picking people (making a group, adding to one). */
function PeoplePicker({ picked, setPicked, skip }: { picked: string[]; setPicked: (f: (cur: string[]) => string[]) => void; skip: Set<string> }) {
  const p = usePickable(skip);
  return (
    <>
      {p.loading && <Skeleton rows={2} label="Loading your contacts…" />}
      <ErrorBox error={p.error} />
      {!p.loading && p.people.length === 0 && <p className="hint">Add people to your contacts, or chat with them, to add them to a group.</p>}
      <fieldset className="pc-choice" data-testid="group-pick">
        <legend>People</legend>
        {p.people.map((x) => (
          <label key={x.id} className="switch-line">
            <input
              type="checkbox"
              checked={picked.includes(x.id)}
              onChange={(e) => setPicked((cur) => (e.target.checked ? [...cur, x.id] : cur.filter((y) => y !== x.id)))}
              data-testid="group-pick-person"
            />{" "}
            <span>
              {x.name}
              {!x.contact && <span className="hint"> · not in your contacts: they'll get an invite</span>}
            </span>
          </label>
        ))}
      </fieldset>
      <MoreRows more={p.chats.more} busy={p.chats.loadingMore} onMore={p.chats.loadMore} count={p.chats.rows?.length ?? 0} label="Show more people" />
    </>
  );
}

/** What an add said for each person, by their name. */
function AddedList({ added, names }: { added: GroupAdded[]; names: Map<string, string> }) {
  if (!added.length) return null;
  return (
    <ul className="group-added" data-testid="group-added" aria-label="What happened for each person">
      {added.map((a) => (
        <li key={a.person_id} data-state={a.state} data-testid="group-added-row">
          <strong>{names.get(a.person_id) ?? "Someone"}</strong>: {addedWords(a.state)}
        </li>
      ))}
    </ul>
  );
}

/** New group: a name, a photo and a description if you like, and people. */
export function NewGroupSheet({ onClose }: { onClose: () => void }) {
  const nav = useNavigate();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [photo, setPhoto] = useState<File>();
  const [picked, setPicked] = useState<string[]>([]);
  const [made, setMade] = useState<{ group: Group; added: GroupAdded[] }>();
  const [photoProblem, setPhotoProblem] = useState<unknown>();
  const act = useAction();
  const none = useMemo(() => new Set<string>(), []);
  const p = usePickable(none);
  const names = new Map(p.people.map((x) => [x.id, x.name]));
  const make = (e: FormEvent) => {
    e.preventDefault();
    void act.run(async () => {
      const out = await post<{ group: Group; added: GroupAdded[] }>("/v1/me/groups", { name: name.trim(), description: description.trim() || undefined, person_ids: picked });
      let photoFailed = false;
      if (photo) {
        const form = new FormData();
        form.append("file", photo);
        await upload(`/v1/me/groups/${encodeURIComponent(out.group.id)}/photo`, form, "PUT").catch((err: unknown) => {
          photoFailed = true;
          setPhotoProblem(err);
        });
      }
      if (newGroupOpensAtOnce(out.added, photoFailed)) {
        onClose();
        nav(groupPath(out.group.id));
        return;
      }
      setMade(out);
    }, false);
  };
  return (
    <Sheet title={made ? made.group.name : "New group"} onClose={onClose} testid="group-new">
      {made ? (
        <div className="pc-check">
          <p role="status">Your group is made.</p>
          <AddedList added={made.added} names={names} />
          {photoProblem !== undefined && <p>The photo was not saved. You can add it again in the group’s info.</p>}
          <ErrorBox error={photoProblem} />
          <button type="button" className="primary" onClick={() => (onClose(), nav(groupPath(made.group.id)))} data-testid="group-new-open">
            Open the group
          </button>
        </div>
      ) : (
        <form className="pc-check" onSubmit={make}>
          <KitField label="Group name" hint={`Up to ${NAME_MAX} letters`}>
            <input value={name} maxLength={NAME_MAX} onChange={(e) => setName(e.target.value)} required data-testid="group-new-name" />
          </KitField>
          <KitField label="Description" hint="Optional">
            <textarea rows={2} maxLength={DESCRIPTION_MAX} value={description} onChange={(e) => setDescription(e.target.value)} data-testid="group-new-description" />
          </KitField>
          <KitField label="Photo" hint="Optional">
            <input type="file" accept="image/jpeg,image/png,image/webp" onChange={(e) => setPhoto(e.target.files?.[0])} data-testid="group-new-photo" />
          </KitField>
          <PeoplePicker picked={picked} setPicked={setPicked} skip={none} />
          <p className="hint">People you added to your contacts are added at once when their settings let you; anyone else gets an invite in their Requests.</p>
          <ErrorBox error={act.error} />
          <button type="submit" className="primary" disabled={!name.trim() || act.busy} data-testid="group-new-make">
            {act.busy ? "Making…" : "Make the group"}
          </button>
        </form>
      )}
    </Sheet>
  );
}

/** Add people to a group (admins). */
function AddPeopleSheet({ base, skip, onClose, onDone }: { base: string; skip: Set<string>; onClose: () => void; onDone: () => void }) {
  const [picked, setPicked] = useState<string[]>([]);
  const [added, setAdded] = useState<GroupAdded[]>();
  const act = useAction();
  const p = usePickable(skip);
  const [names, setNames] = useState(new Map<string, string>());
  return (
    <Sheet
      title="Add people"
      onClose={onClose}
      testid="group-add-sheet"
      footer={
        added ? undefined : (
          <button
            type="button"
            className="tk-cta"
            disabled={picked.length === 0 || act.busy}
            data-testid="group-add-send"
            onClick={() =>
              void act.run(async () => {
                setNames(new Map(p.people.map((x) => [x.id, x.name])));
                const r = await post<{ added: GroupAdded[] }>(`${base}/members`, { person_ids: picked });
                setAdded(r.added);
                onDone();
              }, false)
            }
          >
            Add
          </button>
        )
      }
    >
      {added ? <AddedList added={added} names={names} /> : <PeoplePicker picked={picked} setPicked={setPicked} skip={skip} />}
      <ErrorBox error={act.error} />
    </Sheet>
  );
}

/** Report a group, or one of its members, with consent (§8.3); then offer
 * to leave or block. */
function GroupReportSheet({
  base,
  group,
  member,
  onClose,
  onLeave,
  onBlock,
}: {
  base: string;
  group: string;
  member?: GroupMember;
  onClose: () => void;
  onLeave?: () => void;
  onBlock?: () => void;
}) {
  const [reason, setReason] = useState<(typeof REPORT_REASONS)[number]["key"]>();
  const [note, setNote] = useState("");
  const [consent, setConsent] = useState(false);
  const [said, setSaid] = useState<string>();
  const act = useAction();
  const who = member ? member.name : group;
  return (
    <Sheet
      title={said ? "Reported" : `Report ${who}`}
      onClose={onClose}
      testid="group-report-sheet"
      footer={
        said ? undefined : (
          <button
            type="button"
            className="tk-cta"
            disabled={!reason || !consent || act.busy}
            data-testid="group-report-send"
            onClick={() =>
              void act.run(async () => {
                const r = await post<{ said: string }>(`${base}/report`, { reason, note: note.trim(), consent, person_id: member?.person_id });
                setSaid(r.said);
              }, false)
            }
          >
            {act.busy ? "Sending…" : "Send report"}
          </button>
        )
      }
    >
      {said ? (
        <div className="pc-check">
          <p role="status" data-testid="group-report-said">
            {said}
          </p>
          <div className="row-actions">
            {member && onBlock && (
              <button type="button" className="danger" onClick={onBlock} data-testid="group-report-block">
                Block {member.name}
              </button>
            )}
            {onLeave && (
              <button type="button" className="danger" onClick={onLeave} data-testid="group-report-leave">
                Leave the group
              </button>
            )}
            <button type="button" onClick={onClose}>
              Done
            </button>
          </div>
        </div>
      ) : (
        <div className="pc-check">
          <fieldset className="pc-choice">
            <legend>What is wrong?</legend>
            {REPORT_REASONS.map((r) => (
              <label key={r.key} className="switch-line">
                <input type="radio" name="group-report-reason" checked={reason === r.key} onChange={() => setReason(r.key)} /> <span>{r.words}</span>
              </label>
            ))}
          </fieldset>
          <KitField label="Tell us more" hint="Optional">
            <textarea rows={3} maxLength={1000} value={note} onChange={(e) => setNote(e.target.value)} />
          </KitField>
          <label className="switch-line pc-consent">
            <input type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} data-testid="group-report-consent" /> <span>{GROUP_REPORT_CONSENT}</span>
          </label>
          <ErrorBox error={act.error} />
        </div>
      )}
    </Sheet>
  );
}

/** /you/groups/:id/info: the group's info, members, settings and link. */
export function GroupInfoPage() {
  const { id = "" } = useParams();
  const nav = useNavigate();
  const loc = useLocation();
  const base = `/v1/me/groups/${encodeURIComponent(id)}`;
  const group = useLoad(() => get<Group>(base), [base]);
  // A switch moves at once; the group as the server answered it follows.
  const [fresh, setFresh] = useState<Group>();
  useEffect(() => setFresh(undefined), [group.data]);
  const g = fresh ?? group.data;
  const admin = !!g && !g.left && g.role === "admin";
  const members = useLoad(() => (g && !g.left ? get<GroupMembers>(`${base}/members`) : Promise.resolve(undefined)), [base, !!g, g?.left]);
  const act = useAction();
  const sure = useConfirm();
  const [sheet, setSheet] = useState<{ k: "add" | "edit" | "report" } | { k: "report-member"; m: GroupMember } | undefined>(() =>
    new URLSearchParams(loc.search).get("report") ? { k: "report" } : undefined,
  );
  const live = usePeopleLive();
  const reloadGroup = group.reload;
  const reloadMembers = members.reload;
  useEffect(
    () =>
      live.subscribe((e) => {
        if ((e.type === "group" && e.data.id === id) || e.type === "ready") {
          reloadGroup();
          reloadMembers();
        }
      }),
    [live.subscribe, id, reloadGroup, reloadMembers],
  );
  const reload = () => {
    reloadGroup();
    reloadMembers();
  };
  const setGroup = (body: Record<string, unknown>, said: string) => {
    if (g) setFresh({ ...g, ...body });
    return act
      .run(async () => {
        setFresh(await put<Group>(base, body));
        reload();
      }, said)
      .then((ok) => {
        if (!ok) setFresh(undefined);
        return ok;
      });
  };
  const setMine = (body: Record<string, boolean>, said: string) => {
    if (g) setFresh({ ...g, ...body });
    return act
      .run(async () => {
        setFresh(await put<Group>(`${base}/me`, body));
        reloadGroup();
      }, said)
      .then((ok) => {
        if (!ok) setFresh(undefined);
        return ok;
      });
  };
  const leave = async () => {
    if (!g) return;
    const yes = await sure.confirm({
      title: `Leave ${g.name}?`,
      body: "You keep your copy up to now, until you delete it. You won't get new messages or be able to write.",
      ok: "Leave",
      danger: true,
    });
    if (!yes) return;
    await act.run(async () => {
      const r = await post<{ closed: boolean }>(`${base}/leave`);
      if (r.closed) nav("/you", { replace: true });
      else reload();
    }, `You left ${g.name}`);
  };
  const deleteForMe = async () => {
    const yes = await sure.confirm({ title: "Delete this group?", body: "Its messages go from your chats for good.", ok: "Delete", danger: true });
    if (!yes) return;
    await act.run(async () => {
      await del(base);
      nav("/you", { replace: true });
    }, "Deleted");
  };
  const block = async (m: GroupMember) => {
    const yes = await sure.confirm({
      title: `Block ${m.name}?`,
      body: `You still see their messages in this group. ${m.name} can't message you one to one, can't add you to groups, and doesn't see when you were last here or your photo. They are not told.`,
      ok: "Block",
      danger: true,
    });
    if (!yes) return;
    await act.run(async () => {
      await post(`${base}/members/${encodeURIComponent(m.person_id)}/block`);
      setSheet(undefined);
      reloadMembers();
    }, `Blocked ${m.name}`);
  };
  const photoIn = async (f: File | undefined) => {
    if (!f) return;
    const form = new FormData();
    form.append("file", f);
    await act.run(async () => {
      await upload(`${base}/photo`, form, "PUT");
      reloadGroup();
    }, "Photo changed");
  };
  const list = members.data;
  const skip = useMemo(() => new Set([...(list?.members ?? []).map((m) => m.person_id), ...(list?.invited ?? []).map((m) => m.person_id)]), [list]);

  return (
    <>
      <p className="crumbs">
        <Link to={groupPath(id)} aria-label={`Back to ${g?.name ?? "the group"}`}>
          {g?.name ?? "Group"}
        </Link>
      </p>
      {!g && !group.error && <Skeleton rows={3} label="Loading the group…" />}
      <ErrorBox error={group.error ?? act.error} />
      {g && (
        <>
          <section className="card group-info" aria-labelledby="group-info-name" data-testid="group-info-page">
            <div className="people-info">
              <ChatAvatar name={g.name} logo={g.photo?.url} className="chat-link-avatar" />
              <h1 id="group-info-name">{g.name}</h1>
              {g.description && <p data-testid="group-info-description">{g.description}</p>}
              <p className="hint">{g.left ? (g.left_how === "removed" ? "You were removed from this group" : "You left this group") : `Group · ${membersWords(g.members)}`}</p>
              {g.can_edit && (
                <div className="row-actions">
                  <button type="button" className="small" onClick={() => setSheet({ k: "edit" })} data-testid="group-edit">
                    Edit name and description
                  </button>
                  <label className="button small">
                    {g.photo ? "Change photo" : "Add a photo"}
                    <input type="file" accept="image/jpeg,image/png,image/webp" className="sr-only" onChange={(e) => void photoIn(e.target.files?.[0])} data-testid="group-photo-input" />
                  </label>
                  {g.photo && (
                    <button
                      type="button"
                      className="small"
                      onClick={() =>
                        void act.run(async () => {
                          await del(`${base}/photo`);
                          reloadGroup();
                        }, "Photo removed")
                      }
                    >
                      Remove photo
                    </button>
                  )}
                </div>
              )}
              {!g.can_edit && !g.left && !g.closed && <p className="hint" data-testid="group-edit-admins">Only admins can change the group's name, photo and description.</p>}
            </div>
          </section>

          {g.closed && <ClosedCard g={g} base={base} onDone={reloadGroup} />}

          {!g.left && (
            <section className="card" aria-labelledby="group-yours">
              <h2 id="group-yours">Your settings</h2>
              <label className="switch-line you-notify-line">
                <input type="checkbox" role="switch" checked={g.muted} disabled={act.busy} onChange={(e) => void setMine({ muted: e.target.checked }, e.target.checked ? "Muted" : "Notifications on")} data-testid="group-muted" />{" "}
                <span>
                  Mute notifications
                  <span className="hint block">Messages still come; your phone stays quiet.</span>
                </span>
              </label>
              <label className="switch-line you-notify-line">
                <input type="checkbox" role="switch" checked={g.mentions} disabled={act.busy} onChange={(e) => void setMine({ mentions: e.target.checked }, "Saved")} data-testid="group-mentions" />{" "}
                <span>
                  Notify me when I'm mentioned, even when muted
                  <span className="hint block">When someone writes @ and your name.</span>
                </span>
              </label>
              <label className="switch-line you-notify-line">
                <input type="checkbox" role="switch" checked={g.archived} disabled={act.busy} onChange={(e) => void setMine({ archived: e.target.checked }, e.target.checked ? "Archived" : "Back in your chats")} data-testid="group-archived" />{" "}
                <span>Archived</span>
              </label>
            </section>
          )}

          {!g.left && (
            <section className="card" aria-labelledby="group-members-h" data-testid="group-members">
              <div className="toolbar">
                <h2 id="group-members-h" className="grow" style={{ margin: 0 }}>
                  {membersWords(g.members)}
                </h2>
                {admin && !g.closed && (
                  <button type="button" className="small primary" onClick={() => setSheet({ k: "add" })} data-testid="group-add-open">
                    <Icon name="plus" /> Add people
                  </button>
                )}
              </div>
              <ErrorBox error={members.error} />
              {!list && !members.error && <Skeleton rows={2} label="Loading the members…" />}
              {list && (
                <Items label="Members">
                  {membersInOrder(list.members).map((m) => (
                    <MemberRow
                      key={m.person_id}
                      m={m}
                      admin={admin && !g.closed}
                      busy={act.busy}
                      onAdmin={(on) =>
                        void act.run(async () => {
                          await put(`${base}/members/${encodeURIComponent(m.person_id)}`, { admin: on });
                          reloadMembers();
                        }, on ? `${m.name} is an admin` : `${m.name} is no longer an admin`)
                      }
                      onRemove={() =>
                        void sure.confirm({ title: `Remove ${m.name}?`, body: "They keep their copy up to now, and see nothing new.", ok: "Remove", danger: true }).then(
                          (yes) =>
                            yes &&
                            act.run(async () => {
                              await del(`${base}/members/${encodeURIComponent(m.person_id)}`);
                              reload();
                            }, `Removed ${m.name}`),
                        )
                      }
                      onReport={() => setSheet({ k: "report-member", m })}
                      onBlock={() => void block(m)}
                    />
                  ))}
                </Items>
              )}
              {admin && list?.invited && list.invited.length > 0 && (
                <>
                  <h3>Invited</h3>
                  <Items label="Invited">
                    {list.invited.map((p) => (
                      <PendingRow key={p.person_id} p={p} sub="Invited" testid="group-invited">
                        <button
                          type="button"
                          className="small"
                          disabled={act.busy}
                          onClick={() =>
                            void act.run(async () => {
                              await del(`${base}/invites/${encodeURIComponent(p.person_id)}`);
                              reloadMembers();
                            }, "Invite taken back")
                          }
                        >
                          Take back<span className="sr-only"> the invite to {p.name}</span>
                        </button>
                      </PendingRow>
                    ))}
                  </Items>
                </>
              )}
            </section>
          )}

          {admin && <AdminSettings g={g} busy={act.busy} set={setGroup} />}
          {admin && <LinkCard g={g} base={base} onWaiting={reloadMembers} />}

          <section className="card" aria-labelledby="group-kept-h">
            <h2 id="group-kept-h">How groups are kept</h2>
            <p className="hint" data-testid="group-kept">
              {GROUP_KEPT_WORDS}
            </p>
          </section>

          <section className="card" aria-label="Leave or report">
            <div className="row-actions">
              {!g.left && (
                <button type="button" className="danger" onClick={() => void leave()} data-testid="group-info-leave">
                  Leave group
                </button>
              )}
              {g.left && (
                <button type="button" className="danger" onClick={() => void deleteForMe()} data-testid="group-info-delete">
                  Delete group
                </button>
              )}
              <button type="button" className="danger" onClick={() => setSheet({ k: "report" })} data-testid="group-info-report">
                Report group
              </button>
            </div>
          </section>
        </>
      )}
      {g && sheet?.k === "edit" && <EditInfo g={g} base={base} onClose={() => setSheet(undefined)} onDone={reload} />}
      {g && sheet?.k === "add" && <AddPeopleSheet base={base} skip={skip} onClose={() => setSheet(undefined)} onDone={reload} />}
      {g && sheet?.k === "report" && <GroupReportSheet base={base} group={g.name} onClose={() => setSheet(undefined)} onLeave={g.left ? undefined : () => (setSheet(undefined), void leave())} />}
      {g && sheet?.k === "report-member" && (
        <GroupReportSheet base={base} group={g.name} member={sheet.m} onClose={() => setSheet(undefined)} onBlock={() => void block(sheet.m)} onLeave={() => (setSheet(undefined), void leave())} />
      )}
      {sure.node}
    </>
  );
}

/** A member's row: their name and photo as their settings let, admin,
 * and what an admin (or anyone) may do with them. */
function MemberRow({
  m,
  admin,
  busy,
  onAdmin,
  onRemove,
  onReport,
  onBlock,
}: {
  m: GroupMember;
  admin: boolean;
  busy: boolean;
  onAdmin: (on: boolean) => void;
  onRemove: () => void;
  onReport: () => void;
  onBlock: () => void;
}) {
  const sub = [m.role === "admin" ? "Admin" : "", m.person ? lastSeenWords(m.person) : "", m.phone ? phoneWords(m.phone) : ""].filter(Boolean).join(" · ");
  const name = m.you ? "You" : m.name;
  return (
    <Row letter={name} image={m.person?.photo?.url} title={name} sub={sub ? <span className="sub">{sub}</span> : undefined} testid="group-member" attrs={{ "data-role": m.role }}>
      {admin && !m.you && (
        <>
          <button type="button" className="small" disabled={busy} onClick={() => onAdmin(m.role !== "admin")} data-testid="group-member-admin">
            {m.role === "admin" ? "Remove as admin" : "Make admin"}
            <span className="sr-only"> {m.name}</span>
          </button>
          <button type="button" className="small danger" disabled={busy} onClick={onRemove} data-testid="group-member-remove">
            Remove<span className="sr-only"> {m.name}</span>
          </button>
        </>
      )}
      {admin && m.you && m.role === "admin" && (
        <button type="button" className="small" disabled={busy} onClick={() => onAdmin(false)}>
          Stop being an admin
        </button>
      )}
      {!m.you && !m.person?.deleted && (
        <>
          <button type="button" className="small" onClick={onReport} data-testid="group-member-report">
            Report<span className="sr-only"> {m.name}</span>
          </button>
          <button type="button" className="small" disabled={busy} onClick={onBlock} data-testid="group-member-block">
            Block<span className="sr-only"> {m.name}</span>
          </button>
        </>
      )}
    </Row>
  );
}

function PendingRow({ p, sub, testid, children }: { p: GroupPending; sub: string; testid: string; children: React.ReactNode }) {
  return (
    <Row letter={p.name} title={p.name} sub={<span className="sub">{`${sub} ${listTime(p.at)}`}</span>} testid={testid}>
      {children}
    </Row>
  );
}

/** Who may post, who may edit the info (A2), and approving new members (A4). */
function AdminSettings({ g, busy, set }: { g: Group; busy: boolean; set: (body: Record<string, unknown>, said: string) => Promise<boolean> }) {
  return (
    <section className="card" aria-labelledby="group-admin-h" data-testid="group-settings">
      <h2 id="group-admin-h">Group settings</h2>
      <p className="hint">Only admins see and change these.</p>
      <fieldset className="pc-choice people-setting">
        <legend>Who can send messages</legend>
        <label className="switch-line">
          <input type="radio" name="group-post" checked={!g.admins_only_post} disabled={busy || !!g.closed} onChange={() => void set({ admins_only_post: false }, "Everyone can send messages")} /> <span>Everyone</span>
        </label>
        <label className="switch-line">
          <input type="radio" name="group-post" checked={g.admins_only_post} disabled={busy || !!g.closed} onChange={() => void set({ admins_only_post: true }, "Only admins can send messages")} data-testid="group-admins-post" />{" "}
          <span>Only admins</span>
        </label>
      </fieldset>
      <label className="switch-line you-notify-line">
        <input
          type="checkbox"
          role="switch"
          checked={!g.admins_only_edit}
          disabled={busy || !!g.closed}
          onChange={(e) => void set({ admins_only_edit: !e.target.checked }, "Saved")}
          data-testid="group-everyone-edits"
        />{" "}
        <span>
          Everyone can edit the group's info
          <span className="hint block">Off: only admins change the name, photo and description.</span>
        </span>
      </label>
      <label className="switch-line you-notify-line">
        <input type="checkbox" role="switch" checked={g.approve_joins} disabled={busy || !!g.closed} onChange={(e) => void set({ approve_joins: e.target.checked }, "Saved")} data-testid="group-approve" />{" "}
        <span>
          Approve new members
          <span className="hint block">People who join by the link wait for an admin to let them in.</span>
        </span>
      </label>
    </section>
  );
}

/** The group's invite link: on, replaced, off, its QR code, and who waits. */
function LinkCard({ g, base, onWaiting }: { g: Group; base: string; onWaiting: () => void }) {
  const view = useLoad(() => get<GroupLink>(`${base}/link`), [base]);
  const joins = useLoad(() => get<{ requests: GroupPending[] }>(`${base}/joins`), [base, g.join_requests]);
  const [l, setL] = useState<GroupLink>();
  const [v, setV] = useState(0);
  const act = useAction();
  const sure = useConfirm();
  const link = l ?? view.data;
  const share = async () => {
    if (!link?.url) return;
    const n = navigator as Navigator & { share?: (d: { text: string; url: string }) => Promise<void> };
    if (n.share) await n.share({ text: link.words ?? link.url, url: link.url }).catch(() => undefined);
    else window.open(`https://wa.me/?text=${encodeURIComponent(link.words ?? link.url)}`, "_blank", "noopener");
  };
  const make = (said: string) =>
    act.run(async () => {
      setL(await post<GroupLink>(`${base}/link`));
      setV((x) => x + 1);
    }, said);
  const waiting = joins.data?.requests ?? [];
  return (
    <section className="card" aria-labelledby="group-link-h" data-testid="group-link">
      <h2 id="group-link-h">Invite link</h2>
      <ErrorBox error={view.error ?? act.error} />
      {!link && !view.error && <Skeleton rows={1} label="Loading the link…" />}
      {g.link_stopped ? (
        <p className="notice" role="status" data-testid="group-link-stopped">
          Taskiem's moderators turned this group's link off, so no new link can be made for now.
        </p>
      ) : (
        link &&
        (link.on && link.url ? (
          <div className="people-link">
            <p className="hint">Anyone with the link can see the group's name, photo and how many are in it, and join{g.approve_joins ? " once an admin lets them in" : ""}.</p>
            <img className="people-qr" src={`${link.qr}?v=${v}-${encodeURIComponent(link.code ?? "")}`} alt={`QR code for the group's link ${link.url}`} width={168} height={168} data-testid="group-qr" />
            <div className="email-address">
              <code data-testid="group-link-url">{link.url}</code>
            </div>
            <div className="row-actions">
              <Copy text={link.url} label="Copy the link" />
              <button type="button" className="small" onClick={() => void share()}>
                Share
              </button>
              <button
                type="button"
                className="small"
                disabled={act.busy}
                data-testid="group-link-replace"
                onClick={() =>
                  void sure
                    .confirm({ title: "Replace the link?", body: "The old link stops working. People in the group stay.", ok: "Replace link" })
                    .then((yes) => yes && make("New link made"))
                }
              >
                Replace link
              </button>
              <button
                type="button"
                className="small danger"
                disabled={act.busy}
                data-testid="group-link-off"
                onClick={() =>
                  void sure.confirm({ title: "Turn the link off?", body: "The link stops working. People in the group stay.", ok: "Turn off", danger: true }).then(
                    (yes) =>
                      yes &&
                      act.run(async () => {
                        await del(`${base}/link`);
                        setL({ on: false });
                      }, "Link turned off"),
                  )
                }
              >
                Turn off
              </button>
            </div>
          </div>
        ) : (
          <div className="pc-check">
            <p className="hint">Off. Turn it on to invite people with a link or a QR code.</p>
            <button type="button" className="primary" disabled={act.busy || !!g.closed} onClick={() => void make("Link on")} data-testid="group-link-on">
              Turn on the invite link
            </button>
          </div>
        ))
      )}
      {(g.approve_joins || waiting.length > 0) && (
        <>
          <h3>Waiting to join</h3>
          <ErrorBox error={joins.error} />
          {joins.data && waiting.length === 0 && <p className="hint">No one is waiting.</p>}
          {waiting.length > 0 && (
            <Items label="Waiting to join">
              {waiting.map((p) => (
                <PendingRow key={p.person_id} p={p} sub="Asked" testid="group-waiting">
                  <button
                    type="button"
                    className="small primary"
                    disabled={act.busy}
                    data-testid="group-waiting-approve"
                    onClick={() =>
                      void act.run(async () => {
                        await post(`${base}/joins/${encodeURIComponent(p.person_id)}/approve`);
                        joins.reload();
                        onWaiting();
                      }, `${p.name} is in`)
                    }
                  >
                    Let in<span className="sr-only"> {p.name}</span>
                  </button>
                  <button
                    type="button"
                    className="small"
                    disabled={act.busy}
                    data-testid="group-waiting-decline"
                    onClick={() =>
                      void act.run(async () => {
                        await post(`${base}/joins/${encodeURIComponent(p.person_id)}/decline`);
                        joins.reload();
                      }, "Turned away")
                    }
                  >
                    Turn away<span className="sr-only"> {p.name}</span>
                  </button>
                </PendingRow>
              ))}
            </Items>
          )}
        </>
      )}
      {sure.node}
    </section>
  );
}

/** A group Taskiem's moderators closed: what it means, and its admins' appeal. */
function ClosedCard({ g, base, onDone }: { g: Group; base: string; onDone: () => void }) {
  const [text, setText] = useState("");
  const [said, setSaid] = useState<string>();
  const act = useAction();
  const c = g.closed;
  if (!c) return null;
  return (
    <section className="card" aria-labelledby="appeal" data-testid="group-closed-card">
      <h2 id="appeal">Closed by Taskiem's moderators</h2>
      <p>{c.words}</p>
      {c.reason && <p className="hint">Why: {c.reason}</p>}
      {c.appeal && (
        <p className="notice" role="status" data-testid="group-appeal-state">
          Your appeal: {c.appeal.state === "open" ? "a moderator will read it." : c.appeal.state}
          {c.appeal.note ? ` ${c.appeal.note}` : ""}
        </p>
      )}
      {said && (
        <p role="status" data-testid="group-appeal-said">
          {said}
        </p>
      )}
      {g.role === "admin" && c.can_appeal && !c.appeal && !said && (
        <form
          className="pc-check"
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => {
              const r = await post<{ said: string }>(`${base}/appeal`, { text: text.trim() });
              setSaid(r.said);
              onDone();
            }, false);
          }}
        >
          <KitField label="Your appeal" hint="Say why the group should stay. A different moderator reads it.">
            <textarea rows={3} maxLength={2000} value={text} onChange={(e) => setText(e.target.value)} data-testid="group-appeal-text" />
          </KitField>
          <ErrorBox error={act.error} />
          <button type="submit" className="primary" disabled={!text.trim() || act.busy} data-testid="group-appeal-send">
            Appeal
          </button>
        </form>
      )}
    </section>
  );
}

/** The name and description, by whoever may edit them (A2). */
function EditInfo({ g, base, onClose, onDone }: { g: Group; base: string; onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState(g.name);
  const [description, setDescription] = useState(g.description);
  const act = useAction();
  return (
    <Modal title="Edit the group's info" onClose={onClose}>
      <form
        className="pc-check"
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => {
            await put(base, { name: name.trim(), description: description.trim() });
            onClose();
            onDone();
          }, "Saved");
        }}
      >
        <KitField label="Group name">
          <input value={name} maxLength={NAME_MAX} onChange={(e) => setName(e.target.value)} required data-testid="group-edit-name" autoFocus />
        </KitField>
        <KitField label="Description" hint="Optional">
          <textarea rows={3} maxLength={DESCRIPTION_MAX} value={description} onChange={(e) => setDescription(e.target.value)} data-testid="group-edit-description" />
        </KitField>
        <ErrorBox error={act.error} />
        <div className="row-actions">
          <button type="button" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="primary" disabled={!name.trim() || act.busy} data-testid="group-edit-save">
            Save
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** Invites to groups, in Requests: Accept or Decline (the inviter is not told a decline). */
export function GroupInvites() {
  const nav = useNavigate();
  const list = useLoad(() => get<{ invites: GroupInvite[] }>("/v1/me/groups/invites"), []);
  const [rows, setRows] = useState<GroupInvite[]>();
  const act = useAction();
  const live = usePeopleLive();
  useEffect(() => setRows(list.data?.invites), [list.data]);
  const reload = list.reload;
  useEffect(
    () =>
      live.subscribe((e) => {
        if (e.type === "group_invite") setRows((cur) => invitesWithInvite(cur, e.data));
        else if (e.type === "ready") reload();
      }),
    [live.subscribe, reload],
  );
  const invites = rows ?? [];
  if (list.data && invites.length === 0) return null;
  return (
    <section className="card" aria-labelledby="group-invites-h" data-testid="group-invites">
      <h2 id="group-invites-h">Invites to groups</h2>
      <ErrorBox error={list.error ?? act.error} />
      {!list.data && !list.error && <Skeleton rows={1} label="Loading your invites…" />}
      <Items label="Invites to groups">
        {invites.map((i) => (
          <Row key={i.group_id} letter={i.name} image={i.photo?.url} title={i.name} sub={<span className="sub">{`${i.by} invited you · ${listTime(i.at)}`}</span>} testid="group-invite">
            <button
              type="button"
              className="small primary"
              disabled={act.busy}
              data-testid="group-invite-accept"
              onClick={() =>
                void act.run(async () => {
                  const g = await post<Group>(`/v1/me/groups/invites/${encodeURIComponent(i.group_id)}/accept`).catch((err: unknown) => {
                    if (inviteGone(err)) reload();
                    throw err;
                  });
                  nav(groupPath(g.id));
                }, `You joined ${i.name}`)
              }
            >
              Accept<span className="sr-only"> the invite to {i.name}</span>
            </button>
            <button
              type="button"
              className="small"
              disabled={act.busy}
              data-testid="group-invite-decline"
              onClick={() =>
                void act.run(async () => {
                  await post(`/v1/me/groups/invites/${encodeURIComponent(i.group_id)}/decline`).catch((err: unknown) => {
                    if (inviteGone(err)) reload();
                    throw err;
                  });
                  setRows((cur) => cur?.filter((x) => x.group_id !== i.group_id));
                }, "Invite declined")
              }
            >
              Decline<span className="sr-only"> the invite to {i.name}</span>
            </button>
          </Row>
        ))}
      </Items>
    </section>
  );
}

/** A group's link, /g/{code} (A4): its name, photo and how many are in it;
 * Join (at once, or waiting for an admin, with Take back); signed out, sign
 * in first and come back; in a business, move to You first. */
export function GroupLinkPage() {
  const { code = "" } = useParams();
  const { me } = useAuth();
  const nav = useNavigate();
  const personal = !!me && (!!me.personal || (me.tenant_id === null && !me.gate_only));
  const here = `/g/${encodeURIComponent(code)}`;
  const sw = useSwitch(here);
  const card = useLoad(() => get<GroupLinkPreview>(`/v1/group-links/${encodeURIComponent(code)}`), [code]);
  const [state, setState] = useState<{ s: JoinState; group?: Group }>();
  const act = useAction();
  const err = card.error;
  const gone = err instanceof ApiError && err.status === 410;
  const unknown = err instanceof ApiError && err.status === 404;
  if (gone || unknown)
    return (
      <div className="login card way-in chat-link-page" data-testid="group-link-gone">
        <h1>{gone ? "This link no longer works" : "Link not found"}</h1>
        <p className="way-in-line">{gone ? "The group's admins replaced it or turned it off. Ask them for the new one." : "Check the link, or ask for it again."}</p>
        <p className="hint way-in-other">
          <Link to={me ? "/" : "/login"}>{me ? "Go to Taskiem" : "Sign in to Taskiem"}</Link>
        </p>
      </div>
    );
  const c = card.data;
  const join = () =>
    act.run(async () => {
      const r = await post<{ state: JoinState; group?: Group }>(`/v1/me/group-links/${encodeURIComponent(code)}/join`);
      setState({ s: r.state, group: r.group });
      if (r.state !== "waiting" && r.group) nav(groupPath(r.group.id));
    }, false);
  const withdraw = () =>
    act.run(async () => {
      await del(`/v1/me/group-links/${encodeURIComponent(code)}/join`);
      setState(undefined);
    }, "Request taken back");
  return (
    <div className="login card way-in chat-link-page" data-testid="group-link-page">
      {!c ? (
        <>
          <h1 className="sr-only">A group's link</h1>
          {err ? <ErrorBox error={err} /> : <Skeleton rows={3} label="Opening the link…" />}
        </>
      ) : (
        <>
          <div className="chat-link-who">
            <ChatAvatar name={c.name} logo={c.photo?.url} className="chat-link-avatar" />
            <div>
              <h1 data-testid="group-link-name">{c.name}</h1>
              <p className="way-in-line" data-testid="group-link-count">
                Group on Taskiem · {membersWords(c.members)}
              </p>
            </div>
          </div>
          {state?.s === "waiting" ? (
            <>
              <p role="status" data-testid="group-link-waiting">
                {joinWords("waiting", c.name)}
              </p>
              <button type="button" disabled={act.busy} onClick={() => void withdraw()} data-testid="group-link-withdraw">
                Take back my request
              </button>
            </>
          ) : c.full ? (
            <p role="status">This group is full.</p>
          ) : personal ? (
            <button type="button" className="primary" disabled={act.busy} onClick={() => void join()} data-testid="group-link-join">
              Join the group
            </button>
          ) : (
            <button
              type="button"
              className="primary"
              disabled={sw.busy}
              data-testid="group-link-signin"
              onClick={() => (me ? sw.go("you") : nav(`/login?next=${encodeURIComponent(here)}`))}
            >
              Join on Taskiem
            </button>
          )}
          <ErrorBox error={act.error ?? sw.error} />
        </>
      )}
    </div>
  );
}

/** The one Chats list's group row: its photo, "Group", the mention mark. */
export function GroupRowMark({ mentioned }: { mentioned: boolean }) {
  return (
    <span className="chat-kind" data-testid="chat-kind-group" aria-hidden="true">
      {mentioned && <span className="group-row-at">@ </span>}
      Group
    </span>
  );
}
