// Product photos from the chat (docs/spec/chat-categories-and-variants.md,
// Part C): a photo is made small on the device before it is sent, so a
// 12 MB camera photo is a few hundred KB on a slow connection; a link is
// checked before it is sent for Taskiem to fetch.

import { post, uploadFile } from "../api";
import { imageProblem } from "./storefront";

/** A photo chosen: a file (the camera's or the device's), or a link. */
export type PhotoPick = { file: File } | { url: string };

/** A photo sent to a catalogue row: added to its photos (add), else as its
 * main photo. A file goes made small; a link for Taskiem to fetch. */
export async function sendPhoto(
  row: string,
  p: PhotoPick,
  add: boolean,
): Promise<unknown> {
  const path = `/v1/storefront/catalogue/rows/${encodeURIComponent(row)}/photo${add ? "?add=1" : ""}`;
  if ("url" in p) return post(path, { url: p.url.trim() });
  return uploadFile(path, await shrinkPhoto(p.file));
}

/** A chosen photo, as a list of them names it: its file's name, or the
 * link's host. */
export function pickName(p: PhotoPick): string {
  if ("file" in p) return p.file.name || "a photo";
  try {
    return new URL(p.url).hostname.replace(/^www\./, "");
  } catch {
    return p.url;
  }
}

/** The longest side a photo is sent at. */
export const SEND_SIDE = 1600;
/** A photo already this small (and no wider than SEND_SIDE) goes as it is. */
export const SEND_AS_IS_BYTES = 1_500_000;

/** A picture's size to send: within max on its longest side, its shape kept. */
export function fitWithin(
  w: number,
  h: number,
  max = SEND_SIDE,
): { w: number; h: number } {
  if (w <= max && h <= max) return { w, h };
  const k = max / Math.max(w, h);
  return {
    w: Math.max(1, Math.round(w * k)),
    h: Math.max(1, Math.round(h * k)),
  };
}

/** What is wrong with a pasted link, in a sentence; null when it can be sent. */
export function linkProblem(raw: string): string | null {
  const v = raw.trim();
  if (v === "") return "Paste the link of a picture.";
  let u: URL;
  try {
    u = new URL(v);
  } catch {
    return "That is not a link: it starts with https://";
  }
  if (u.protocol !== "https:" && u.protocol !== "http:")
    return "That is not a link: it starts with https://";
  return null;
}

/** Whether the device is a phone or tablet (a finger, not a mouse). */
export const isTouch = () =>
  typeof window !== "undefined" &&
  !!window.matchMedia?.("(pointer: coarse)").matches;

/**
 * A photo made ready to send: checked (JPEG, PNG or WebP, at most the
 * upload limit), then, when it is large, drawn smaller as a JPEG. A photo
 * the browser cannot draw is sent as it is, for the server to check.
 */
export async function shrinkPhoto(file: File): Promise<File> {
  const bad = imageProblem(file);
  if (bad) throw new Error(bad);
  if (typeof createImageBitmap !== "function") return file;
  let bmp: ImageBitmap;
  try {
    bmp = await createImageBitmap(file);
  } catch {
    return file;
  }
  const to = fitWithin(bmp.width, bmp.height);
  if (to.w === bmp.width && file.size <= SEND_AS_IS_BYTES) {
    bmp.close();
    return file;
  }
  const c = document.createElement("canvas");
  c.width = to.w;
  c.height = to.h;
  c.getContext("2d")?.drawImage(bmp, 0, 0, to.w, to.h);
  bmp.close();
  const blob = await new Promise<Blob | null>((r) =>
    c.toBlob(r, "image/jpeg", 0.85),
  );
  if (!blob || blob.size >= file.size) return file;
  const name = file.name.replace(/\.[^.]*$/, "") || "photo";
  return new File([blob], `${name}.jpg`, { type: "image/jpeg" });
}
