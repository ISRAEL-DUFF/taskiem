// Reading account details from photos (I2; the app's photo_text.dart): a
// form's "From a photo" button, and the words of a photo read on this
// device (src/lib/photoText.ts). The photo is never sent to be read: a
// photo chosen stays in the browser, and a chat's photo is fetched the way
// the chat shows it (same origin, with the session) and read here. Only
// the words go on, to Taskiem's readers, with from: "photo".

import { getBlob } from "../api";
import { Icon } from "../icons";
import { readOnDevice, readPhotoText } from "../lib/photoText";
import { toast } from "../toast";

/** One photo read at a time. */
let reading = false;

/** Whether a photo is being read just now. */
export const readingPhoto = () => reading;

/** The words in a photo, read on this device; null when none could be
 * read (said in a toast). */
export async function readPhotoWords(photo: Blob): Promise<string | null> {
  if (reading) return null;
  reading = true;
  try {
    let words: string;
    try {
      words = (await readPhotoText(photo)).trim();
    } catch {
      toast("This browser could not read that photo. Type it in.", "error");
      return null;
    }
    if (!words) {
      toast("No words in that photo. Type it in.", "info");
      return null;
    }
    return words;
  } finally {
    reading = false;
  }
}

/** A photo in a chat (only on the server, at url): fetched as the chat
 * fetches it to show it, then read on this device. Never uploaded. */
export async function readChatPhoto(url: string): Promise<string | null> {
  if (reading) return null;
  let photo: Blob;
  try {
    photo = await getBlob(url);
  } catch {
    toast("That photo could not be got just now. Try again.", "error");
    return null;
  }
  toast("Reading the photo…", "info");
  return readPhotoWords(photo);
}

/** A form's "From a photo" button: the file picker (a phone offers its
 * camera too; nothing is forced), and beside it that the photo is read
 * on this device and not sent. While busy it says the photo is being read
 * (the first time, the reader is fetched once, about 7 MB). */
export function PhotoReadButton({
  label,
  describe,
  busy,
  disabled,
  onPhoto,
  testid = "photo-read",
}: {
  label: string;
  /** What the button does, for those who hear the page. */
  describe: string;
  busy: boolean;
  disabled?: boolean;
  onPhoto: (photo: File) => void;
  testid?: string;
}) {
  return (
    <div className="photo-read" data-testid={testid}>
      <label className={`button small photo-read-pick${busy || disabled ? " off" : ""}`}>
        <input
          type="file"
          accept="image/*"
          className="sr-only"
          disabled={busy || disabled}
          aria-label={describe}
          onChange={(e) => {
            const f = e.target.files?.[0];
            e.target.value = "";
            if (f) onPhoto(f);
          }}
          data-testid={`${testid}-file`}
        />
        <Icon name="camera" />
        <span aria-hidden="true">{busy ? "Reading the photo…" : label}</span>
      </label>
      <span className="photo-read-note" aria-live="polite" data-testid={`${testid}-note`}>
        {busy ? "Reading the photo on this device. The first time takes longer: the reader is fetched once." : readOnDevice}
      </span>
    </div>
  );
}
