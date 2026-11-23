// Reading a photo with AI (I3): when this device could not read a photo
// (handwriting, mostly) and an owner turned it on in Settings, the photo
// may be sent to Taskiem's AI (POST /v1/photo-text). It is not kept; its
// words come back only to whoever asked, and go on as the device's words
// do (from: "photo"). The setting is read once a page view, when first
// needed.

import { get, upload } from "../api";
import type { BatchLine } from "./batches";
import { tidyLines } from "./photoText";

export interface AIPhotoSetting {
  ai_photo_reading: boolean;
  /** Whether this Taskiem can read photos with AI at all. */
  available: boolean;
}

const OFF: AIPhotoSetting = { ai_photo_reading: false, available: false };

let setting: Promise<AIPhotoSetting> | undefined;

/** The business's setting, read once (off when it cannot be read). */
export function aiPhotoSetting(): Promise<AIPhotoSetting> {
  if (!setting)
    setting = get<AIPhotoSetting>("/v1/settings/ai-photo-reading").then(
      (r) => ({ ai_photo_reading: !!r.ai_photo_reading, available: !!r.available }),
      () => {
        setting = undefined;
        return OFF;
      },
    );
  return setting;
}

/** Settings changed it: every screen reads it so from now on. */
export function setAIPhotoSetting(s: AIPhotoSetting) {
  setting = Promise.resolve(s);
}

/** Forgets it (sign-out, another business). */
export function forgetAIPhotoSetting() {
  setting = undefined;
}

/** Whether "Read it with AI" is offered. */
export const aiOffered = (s: AIPhotoSetting) => s.available && s.ai_photo_reading;

/** Whether an owner is told they can turn it on. */
export const aiHinted = (s: AIPhotoSetting, owner: boolean) => owner && s.available && !s.ai_photo_reading;

/** Under the button: what tapping it does. */
export const aiOfferLine = "Sends this photo to Taskiem's AI. It is not kept.";

/** What the device found, said above the offer: "Type it in" is the
 * other button there. */
export const offerSaid = (said: string) => said.replace(/\s*Type it in\.?\s*$/, "").trim() || "This device could not read that photo.";

/** Where an owner turns it on. */
export const aiSettingsPath = "/settings#ai-photo-reading";

/** A list read from a photo where not one line could be read. */
export const noLineRead = (lines: Pick<BatchLine, "problem">[]) => lines.every((l) => !!l.problem);

/** The long edge a photo is sent at, at most: the most the AI model
 * reads at full detail, as the phone app sends one. */
export const PHOTO_SIDE = 2576;

/** The most the AI model takes in one photo (5 MB). */
export const MAX_PHOTO_BYTES = 5 << 20;

/** Said with the words when the AI's answer was cut off short. */
export const listLong = "The list was long: check the last lines.";

const asIs = /^image\/(jpeg|png|webp|gif)$/;

/** A photo made ready for the AI model: one it reads as it is (a JPEG,
 * PNG, WebP or GIF within 2576 pixels and 5 MB) goes as it is; any other
 * (a phone's large photo, a HEIC Safari can open) is drawn smaller, its
 * long edge at most 2576 pixels, as a JPEG. One the browser cannot open
 * goes as it is, for the server to say what it is. */
export async function shrinkPhoto(photo: Blob): Promise<Blob> {
  let bitmap: ImageBitmap;
  try {
    bitmap = await createImageBitmap(photo);
  } catch {
    return photo;
  }
  try {
    const { width, height } = bitmap;
    const long = Math.max(width, height);
    if (long <= PHOTO_SIDE && photo.size <= MAX_PHOTO_BYTES && asIs.test(photo.type)) return photo;
    const scale = Math.min(1, PHOTO_SIDE / long);
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(width * scale));
    canvas.height = Math.max(1, Math.round(height * scale));
    const g = canvas.getContext("2d");
    if (!g) return photo;
    // A JPEG has no see-through parts: a screenshot's go white, not black.
    g.fillStyle = "#fff";
    g.fillRect(0, 0, canvas.width, canvas.height);
    g.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    const blob = await new Promise<Blob | null>((ok) => canvas.toBlob(ok, "image/jpeg", 0.85));
    if (!blob) return photo;
    const name = (photo instanceof File ? photo.name : "photo").replace(/\.[A-Za-z0-9]+$/, "") + ".jpg";
    return new File([blob], name, { type: "image/jpeg" });
  } finally {
    bitmap.close?.();
  }
}

/** What Taskiem's AI read in a photo: its words, and whether its answer
 * was cut off (the last lines then need checking). */
export interface PhotoAIRead {
  text: string;
  truncated: boolean;
}

/** The photo's words, read by Taskiem's AI (the photo made smaller
 * first); a refusal throws with the server's words. */
export async function readPhotoAI(photo: Blob): Promise<PhotoAIRead> {
  const sent = await shrinkPhoto(photo);
  const form = new FormData();
  form.append("photo", sent, sent instanceof File ? sent.name : "photo");
  const r = await upload<{ text?: string; truncated?: boolean }>("/v1/photo-text", form);
  return { text: tidyLines(r.text ?? ""), truncated: !!r.truncated };
}
