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

/** The photo's words, read by Taskiem's AI; a refusal throws with the
 * server's words. */
export async function readPhotoAI(photo: Blob): Promise<string> {
  const form = new FormData();
  form.append("photo", photo, photo instanceof File ? photo.name : "photo");
  const r = await upload<{ text?: string }>("/v1/photo-text", form);
  return tidyLines(r.text ?? "");
}
