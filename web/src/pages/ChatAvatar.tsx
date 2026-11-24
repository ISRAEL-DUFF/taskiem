import { useEffect, useState } from "react";
import { toneOf } from "../lib/tone";


/** Up to two initials: "Bayo Rice Mills" is "BR". */
export function initialsOf(name: string): string {
  const words = name.split(/\s+/).filter((w) => /^[\p{L}\p{N}]/u.test(w));
  return (
    words
      .slice(0, 2)
      .map((w) => w.charAt(0).toUpperCase())
      .join("") || name.slice(0, 1).toUpperCase()
  );
}

/**
 * A contact's round picture in the chat: the business's logo when it has
 * one, else its initials (also when the logo cannot be
 * loaded). It is decoration: the name is always next to it.
 */
export function ChatAvatar({
  name,
  logo,
  className = "",
}: {
  name: string;
  /** The logo's address, when there is one. */
  logo?: string;
  className?: string;
}) {
  const [bad, setBad] = useState(false);
  useEffect(() => setBad(false), [logo]);
  const cls = `chat-avatar${className ? ` ${className}` : ""}`;
  if (logo && !bad)
    return (
      <img
        className={cls}
        src={logo}
        alt=""
        aria-hidden="true"
        loading="lazy"
        onError={() => setBad(true)}
      />
    );
  return (
    // The initials are drawn, not text: a button showing the avatar is
    // named by its label alone.
    <span
      className={cls}
      aria-hidden="true"
      data-initials={initialsOf(name)}
      data-tone={toneOf(name)}
    />
  );
}
