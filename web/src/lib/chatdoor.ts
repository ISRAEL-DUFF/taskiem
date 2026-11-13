/** The chat an emailed link would link to the signed-in account
 * (POST /v1/me/chat-link/preview). */
export interface PendingChat {
  channel: string;
  address: string;
  username?: string;
}

/** The chat as its owner knows it, whole: the number, or the @username.
 * The owner of the account may see what is about to be attached to it. */
export function chatAddress(c: PendingChat): string {
  if (c.channel === "telegram")
    return c.username
      ? `@${c.username}`
      : `the Telegram account numbered ${c.address}`;
  return c.address;
}
