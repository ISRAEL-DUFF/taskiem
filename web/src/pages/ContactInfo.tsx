import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { get } from "../api";
import { Icon } from "../icons";
import { fmtTime } from "../ui";
import { roleLine, type BusinessLink, type LinkCard } from "../lib/links";
import {
  previewLine,
  type Conversation,
  type LinkMessage,
} from "../lib/linkchat";
import { ChatAvatar } from "./ChatAvatar";

/** A round icon button with its words under it (WhatsApp's row of actions under the name). */
function RoundAction({
  icon,
  label,
  pressed,
  disabled,
  onClick,
}: {
  icon: string;
  label: string;
  pressed?: boolean;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className={`info-round${pressed ? " on" : ""}`}
      aria-pressed={pressed}
      disabled={disabled}
      onClick={onClick}
    >
      <span className="info-round-dot">
        <Icon name={icon} />
      </span>
      <span>{label}</span>
    </button>
  );
}

/** One line of the panel: an icon, the words, and what is on the right (a count, a chevron or a switch). */
function InfoRow({
  icon,
  label,
  sub,
  trailing,
  danger,
  disabled,
  onClick,
  expanded,
  checked,
  testid,
}: {
  icon: string;
  label: string;
  sub?: string;
  trailing?: ReactNode;
  danger?: boolean;
  disabled?: boolean;
  onClick: () => void;
  expanded?: boolean;
  /** Set for a switch: whether it is on. */
  checked?: boolean;
  testid?: string;
}) {
  return (
    <button
      type="button"
      className={`info-row${danger ? " danger" : ""}`}
      disabled={disabled}
      aria-expanded={expanded}
      role={checked === undefined ? undefined : "switch"}
      aria-checked={checked}
      onClick={onClick}
      data-testid={testid}
    >
      <Icon name={icon} />
      <span className="info-row-main">
        <span className="info-row-label">{label}</span>
        {sub && <span className="info-row-sub">{sub}</span>}
      </span>
      {trailing}
    </button>
  );
}

/** A switch (on or off), as a row's right side. */
function Switch({ on }: { on: boolean }) {
  return (
    <span className={`info-switch${on ? " on" : ""}`} aria-hidden="true">
      <span />
    </span>
  );
}

/**
 * The panel of a conversation as WhatsApp's Contact info is: the picture
 * and name, round actions, then rows with an icon each (starred messages,
 * media, mute as a switch, archive), what the business says about itself,
 * and the one thing that cannot be undone last, in red. `media` is the
 * conversation's pictures and files (a row that opens them).
 */
export function ContactInfo({
  conv,
  logo,
  link,
  card,
  canManage,
  busy,
  media,
  onClose,
  onSetting,
  onEndLink,
  onSearch,
}: {
  conv: Conversation;
  logo?: string;
  link?: BusinessLink;
  card?: LinkCard;
  /** May change this chat's settings and end the link. */
  canManage: boolean;
  busy: boolean;
  media: ReactNode;
  onClose: () => void;
  onSetting: (
    body: { muted?: boolean; archived?: boolean; pinned?: boolean },
    done: string,
  ) => void;
  /** Asks to end the link (the page confirms first). */
  onEndLink?: () => void;
  onSearch: () => void;
}) {
  const [view, setView] = useState<"info" | "starred">("info");
  const [starred, setStarred] = useState<LinkMessage[]>();
  useEffect(() => {
    let live = true;
    get<{ messages: LinkMessage[] }>("/v1/link-chats/starred").then(
      (r) => live && setStarred(r.messages.filter((m) => m.link === conv.link)),
      () => live && setStarred([]),
    );
    return () => {
      live = false;
    };
  }, [conv.link]);
  const hasCard =
    !!card && (card.area || card.tagline || card.items.length > 0);

  if (view === "starred")
    return (
      <aside
        className="chat-info"
        aria-label={`Starred messages with ${conv.name}`}
        data-testid="chat-info"
      >
        <div className="chat-info-head">
          <button
            type="button"
            className="icon-button"
            aria-label="Back to contact info"
            onClick={() => setView("info")}
          >
            <Icon name="back" />
          </button>
          <h3>Starred messages</h3>
        </div>
        <div className="info-list" data-testid="chat-starred">
          {(starred ?? []).length === 0 ? (
            <p className="hint info-empty">
              Nothing starred in this chat. Open a message’s menu and choose
              Star.
            </p>
          ) : (
            <ul>
              {(starred ?? []).map((m) => (
                <li key={m.id}>
                  <Link
                    className="info-starred"
                    to={`/chat?with=${conv.link}&msg=${m.id}`}
                    onClick={onClose}
                  >
                    <span className="info-starred-line">
                      {previewLine(m, conv.name)}
                    </span>
                    <span className="hint">{fmtTime(m.created_at)}</span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </div>
      </aside>
    );

  return (
    <aside
      className="chat-info"
      aria-label={`About ${conv.name}`}
      data-testid="chat-info"
    >
      <div className="chat-info-head">
        <button
          type="button"
          className="icon-button"
          aria-label="Close contact info"
          onClick={onClose}
        >
          <Icon name="close" />
        </button>
        <h3>Contact info</h3>
      </div>
      <div className="info-scroll">
        <div className="info-hero">
          <ChatAvatar name={conv.name} logo={logo} className="big" />
          <p className="chat-info-name">{conv.name}</p>
          {link && <p className="hint">{roleLine(link.role)}</p>}
          {link?.state === "active" && link.answered_at && (
            <p className="hint">Linked {fmtTime(link.answered_at)}</p>
          )}
          {conv.state !== "active" && (
            <p className="hint">The link has ended.</p>
          )}
          <div className="info-round-row">
            <RoundAction icon="search" label="Search" onClick={onSearch} />
            {canManage && (
              <RoundAction
                icon="pin"
                label={conv.pinned ? "Unpin" : "Pin"}
                pressed={conv.pinned}
                disabled={busy}
                onClick={() =>
                  onSetting(
                    { pinned: !conv.pinned },
                    conv.pinned ? "Unpinned" : "Pinned",
                  )
                }
              />
            )}
          </div>
        </div>

        {hasCard && (
          <section className="info-section" aria-label="About the business">
            <h4>About</h4>
            {card?.tagline && <p>{card.tagline}</p>}
            {card?.area && <p className="hint">{card.area}</p>}
            {card && card.items.length > 0 && (
              <ul className="chat-info-items">
                {card.items.slice(0, 5).map((it) => (
                  <li key={it.name}>{it.name}</li>
                ))}
              </ul>
            )}
          </section>
        )}

        <section className="info-section flush">
          <InfoRow
            icon="star"
            label="Starred messages"
            trailing={
              <>
                <span className="info-count">
                  {starred ? starred.length : ""}
                </span>
                <Icon name="chevron" />
              </>
            }
            onClick={() => setView("starred")}
            testid="info-starred"
          />
          {media}
        </section>

        {canManage && (
          <section className="info-section flush">
            <InfoRow
              icon="mute"
              label="Mute notifications"
              disabled={busy}
              checked={conv.muted}
              trailing={<Switch on={conv.muted} />}
              onClick={() =>
                onSetting(
                  { muted: !conv.muted },
                  conv.muted ? "Unmuted" : "Muted",
                )
              }
              testid="info-mute"
            />
            <InfoRow
              icon="archive"
              label={
                conv.archived ? "Unarchive this chat" : "Archive this chat"
              }
              disabled={busy}
              onClick={() =>
                onSetting(
                  { archived: !conv.archived },
                  conv.archived ? "Unarchived" : "Archived",
                )
              }
              testid="info-archive"
            />
          </section>
        )}

        {canManage && link && link.state === "active" && onEndLink && (
          <section className="info-section flush">
            <InfoRow
              icon="unlink"
              label={`End link with ${link.name}`}
              danger
              disabled={busy}
              onClick={onEndLink}
              testid="info-end-link"
            />
          </section>
        )}
      </div>
    </aside>
  );
}
