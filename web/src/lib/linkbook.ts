// Book in a business's conversation (docs/spec/business-chat-tools.md §3):
// the other business's services as their storefront shows them, their free
// times, and a booking made through their storefront's booking.

import { useEffect, useState } from "react";
import { get } from "../api";

/** One of their services (GET /v1/link-chats/{link}/services). */
export interface LinkService {
  id: string;
  name: string;
  minutes: number;
  price_kobo?: number;
  description?: string;
  category?: string;
}

export interface LinkServices {
  services: LinkService[];
  /** They take bookings online now; closed says so when not. */
  open: boolean;
  closed?: string;
  /** The number a booking gives them: our storefront's, where it has one. */
  phone: string;
}

/** A service's free times (GET …/services/{id}/times). */
export interface LinkTimes {
  service: LinkService;
  days: string[];
  day?: string;
  times: string[];
}

/** A booking made (POST /v1/link-chats/{link}/bookings). */
export interface LinkBooking {
  reference: string;
  service_id: string;
  service_name: string;
  starts_at: string;
  ends_at: string;
  price_kobo?: number;
}

const cache = new Map<string, Promise<LinkServices>>();

/** Their services, read once a minute at most: the panel's tile and the
 * sheet share the answer. */
export function linkServices(link: string): Promise<LinkServices> {
  let p = cache.get(link);
  if (!p) {
    p = get<LinkServices>(
      `/v1/link-chats/${encodeURIComponent(link)}/services`,
    ).catch((e: unknown) => {
      cache.delete(link);
      throw e;
    });
    cache.set(link, p);
    window.setTimeout(() => cache.delete(link), 60_000);
  }
  return p;
}

/** Their services in a business's conversation; undefined until known (or
 * when there is no such conversation, or they could not be read). */
export function useLinkServices(link?: string): LinkServices | undefined {
  const [got, setGot] = useState<{ link: string; s: LinkServices }>();
  useEffect(() => {
    if (!link) return;
    let on = true;
    linkServices(link).then(
      (s) => on && setGot({ link, s }),
      () => undefined,
    );
    return () => {
      on = false;
    };
  }, [link]);
  return got && got.link === link ? got.s : undefined;
}

/** The Book tile's second line. */
export function bookLine(s: LinkServices): string {
  if (!s.open) return "Not taking bookings online";
  const n = s.services.length;
  return `${n} ${n === 1 ? "service" : "services"} · choose a time`;
}

/** How a booking is paid from the conversation. */
export const bookPayWords = (business: string) =>
  `You pay ${business} at the appointment, or as you agree with them.`;
