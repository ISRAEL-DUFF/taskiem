import { useEffect, useState } from "react";

/**
 * A contact's round picture in the chat: the business's logo when it has
 * one, else the first letter of its name (also when the logo cannot be
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
    <span className={cls} aria-hidden="true">
      {name.slice(0, 1).toUpperCase()}
    </span>
  );
}
