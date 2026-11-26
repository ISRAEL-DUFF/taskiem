// Links in chats (docs/spec/chat-previews-and-editing.md §2): every web
// address in a message's words opens in a new tab; the first link's card
// (the page's picture, title, words and site) sits at the top of the
// bubble; while a link is typed, its card waits above the box with ✕ to
// send without it. The picture always comes from Taskiem's own address.

import { useEffect, useRef, useState } from "react";
import { post } from "../api";
import { Icon } from "../icons";
import { firstLink, linkParts, shownLink } from "../lib/linkify";
import type { LinkPreview } from "../lib/linkchat";

/** A link opened from a chat: a new tab, telling the page nothing of us. */
const linkProps = { target: "_blank", rel: "noopener noreferrer nofollow" } as const;

/** Words with every web address in them tappable (shortened in the middle
 * when long, opened in full). */
export function LinkedWords({ text }: { text: string }) {
  const parts = linkParts(text);
  if (parts.length === 1 && !parts[0]?.link) return <>{text}</>;
  return (
    <>
      {parts.map((p, i) =>
        p.link ? (
          <a key={i} className="chat-link" href={p.link.url} title={p.link.url} data-testid="chat-link" {...linkProps}>
            {shownLink(p.text)}
          </a>
        ) : (
          p.text
        ),
      )}
    </>
  );
}

/** Whether a picture goes across the card (wide) or in a square on the
 * left (small or square): the same rule as the app's. */
export function wideImage(img?: { w: number; h: number }): boolean {
  return !!img && img.w >= 400 && img.w >= img.h * 1.3;
}

/** A link's card at the top of its bubble: the page's picture (from
 * `src`, Taskiem's own copy), title, words and site. A Taskiem link is a
 * plain card ("Taskiem · Order link"). Tapping it opens the link. */
export function LinkCard({ p, src, onRemove }: { p: LinkPreview; src?: string; onRemove?: () => void }) {
  const wide = wideImage(p.image);
  const site = p.taskiem ? "Taskiem" : p.site || p.host;
  return (
    <span className={`link-card${wide ? " wide" : ""}${p.taskiem ? " taskiem" : ""}`} data-testid="chat-preview">
      <a className="link-card-open" href={p.url} {...linkProps}>
        {p.image && src && <img className="link-card-pic" src={src} alt="" loading="lazy" width={p.image.w} height={p.image.h} data-testid="chat-preview-picture" />}
        {p.taskiem && (
          <span className="link-card-pic mark" aria-hidden="true">
            <Icon name="link" />
          </span>
        )}
        <span className="link-card-words">
          <span className="link-card-title">{p.title}</span>
          {p.description && !p.taskiem && <span className="link-card-desc">{p.description}</span>}
          <span className="link-card-site">{site}</span>
        </span>
      </a>
      {onRemove && (
        <button type="button" className="link-card-close" onClick={onRemove} aria-label="Send without the preview" data-testid="composer-preview-close">
          <Icon name="close" />
        </button>
      )}
    </span>
  );
}

/** The composer's card: what POST /v1/link-preview answered. */
export type ComposerPreview = LinkPreview & { id: string; image?: { id: string; w: number; h: number } };

/** What a message is sent with: the card shown, none (closed), or nothing
 * said (the server makes it as the message is sent). */
export type PreviewChoice = { id: string } | false | undefined;

/** The card of the first link typed in a box, half a second after the
 * person pauses, for `chat` (link:…, people:…, group:…, door:…,
 * customer:…); none without `chat`. */
export function useComposerPreview(text: string, chat: string | undefined) {
  const url = chat ? firstLink(text) : "";
  const [card, setCard] = useState<ComposerPreview>();
  const [loading, setLoading] = useState(false);
  const [closed, setClosed] = useState<string>();
  const asked = useRef("");
  useEffect(() => {
    if (!url || !chat) {
      asked.current = "";
      setCard(undefined);
      setLoading(false);
      return;
    }
    if (url === asked.current) return;
    setCard(undefined);
    const t = window.setTimeout(() => {
      asked.current = url;
      setLoading(true);
      post<ComposerPreview | undefined>("/v1/link-preview", { url, chat })
        .then((p) => {
          if (asked.current === url) setCard(p && p.title ? p : undefined);
        })
        .catch(() => undefined)
        .finally(() => {
          if (asked.current === url) setLoading(false);
        });
    }, 500);
    return () => window.clearTimeout(t);
  }, [url, chat]);
  const shown = url && closed !== url ? card : undefined;
  return {
    url,
    card: shown && shown.url === url ? shown : undefined,
    loading: !!url && closed !== url && loading,
    close: () => setClosed(url),
    /** What the message goes with, for the words being sent. */
    choice: (words: string): PreviewChoice => {
      const u = firstLink(words);
      if (!u || !chat) return undefined;
      if (closed === u) return false;
      if (card && card.url === u) return { id: card.id };
      return undefined;
    },
    reset: () => {
      setClosed(undefined);
      setCard(undefined);
      asked.current = "";
    },
  };
}

export type ComposerPreviewState = ReturnType<typeof useComposerPreview>;

/** The card above the box, with ✕, or "Loading preview…". */
export function ComposerLinkCard({ s }: { s: ComposerPreviewState }) {
  if (s.card) {
    return (
      <div className="composer-link" data-testid="composer-preview">
        <LinkCard p={s.card} src={s.card.image ? `/v1/link-preview/${encodeURIComponent(s.card.image.id)}/image` : undefined} onRemove={s.close} />
      </div>
    );
  }
  if (s.loading) {
    return (
      <div className="composer-link loading" role="status" data-testid="composer-preview-loading">
        Loading preview…
      </div>
    );
  }
  return null;
}
