// Reading account details from photos (I2; the app's photo_text.dart): a
// form's "From a photo" button, and the words of a photo read on this
// device (src/lib/photoText.ts). A photo chosen stays in the browser, and
// a chat's photo is fetched the way the chat shows it (same origin, with
// the session) and read here. Only the words go on, to Taskiem's readers,
// with from: "photo".
// When this device finds nothing in it (handwriting, mostly) and an owner
// turned on reading with AI (I3, src/lib/photoAI.ts), "Read it with AI" is
// offered: only on that tap is the photo sent, to POST /v1/photo-text, and
// its words go on as the device's would. Off, an owner is told where to
// turn it on.

import { useState } from "react";
import { getBlob } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { aiHinted, aiOffered, aiOfferLine, aiPhotoSetting, aiSettingsPath, listLong, offerSaid, readPhotoAI } from "../lib/photoAI";
import { readOnDevice, readPhotoText } from "../lib/photoText";
import { toast, untoast, type ToastKind } from "../toast";
import { Modal } from "../ui";

/** One photo read at a time. */
let reading = false;

/** Whether a photo is being read just now. */
export const readingPhoto = () => reading;

/** What this device read in a photo: its words, or what to say instead. */
export type DeviceRead = { words: string } | { said: string; kind: ToastKind };

/** The words in a photo, read on this device; null while another is
 * being read. Nothing said. */
export async function devicePhotoWords(photo: Blob): Promise<DeviceRead | null> {
  if (reading) return null;
  reading = true;
  try {
    let words: string;
    try {
      words = (await readPhotoText(photo)).trim();
    } catch {
      return { said: "This browser could not read that photo. Type it in.", kind: "error" };
    }
    if (!words) return { said: "No words in that photo. Type it in.", kind: "info" };
    return { words };
  } finally {
    reading = false;
  }
}

/** Said while a chat's photo is read. */
export const readingWords = "Reading the photo…";

/** A photo in a chat (only on the server, at url), fetched as the chat
 * fetches it to show it, to be read on this device (usePhotoAI's read);
 * null when it could not be (said). Never uploaded. */
export async function chatPhoto(url: string): Promise<Blob | null> {
  if (reading) return null;
  try {
    return await getBlob(url);
  } catch {
    toast("That photo could not be got just now. Try again.", "error");
    return null;
  }
}

/** What a flow does with a photo's words: null when it used them (or said
 * why not), else what to say because nothing was found in them. */
export type UseWords = (words: string, byAI: boolean) => Promise<string | null>;

/** "Read it with AI" waiting to be tapped: the photo, and what to do with
 * the words that come back. */
export interface AIOffer {
  photo: Blob;
  /** What the device found, said above the button. */
  said: string;
  use: UseWords;
}

/** A photo read on this device and, where it finds nothing and reading
 * with AI is on, "Read it with AI" offered (offer, shown by the caller
 * with AIReadOffer). */
export function usePhotoAI() {
  const { me } = useAuth();
  const owner = !!me?.roles.includes("owner");
  const [offer, setOffer] = useState<AIOffer>();

  /** Nothing found in the photo: offered with AI where on (true), else
   * said, with where an owner turns it on. */
  const nothing = async (photo: Blob, said: string, use: UseWords, kind: ToastKind = "info"): Promise<boolean> => {
    const s = await aiPhotoSetting();
    // "Reading the photo…" is over: it would cover what is said now.
    untoast(readingWords);
    if (aiOffered(s)) {
      setOffer({ photo, said, use });
      return true;
    }
    toast(said, kind, aiHinted(s, owner) ? { href: aiSettingsPath, linkText: "Turn on reading with AI" } : {});
    return false;
  };

  /** Reads photo on this device and gives its words to use; what finds
   * nothing goes to nothing(). Errors from use are thrown. */
  const read = async (photo: Blob, use: UseWords): Promise<void> => {
    const d = await devicePhotoWords(photo);
    if (!d) return;
    if ("said" in d) {
      await nothing(photo, d.said, use, d.kind);
      return;
    }
    const said = await use(d.words, false);
    if (said !== null) await nothing(photo, said, use);
  };

  return { read, nothing, offer, clear: () => setOffer(undefined) };
}

/** "Read it with AI": what the device found, the button, and that the
 * photo is sent and not kept. The words that come back go on as the
 * device's do; a refusal is said in the server's words. */
export function AIReadOffer({ offer, onClose, testid = "photo-ai" }: { offer: AIOffer; onClose: () => void; testid?: string }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const go = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const { text, truncated } = await readPhotoAI(offer.photo);
      if (!text) {
        setError("No words could be read in that photo. Type it in.");
        return;
      }
      const said = await offer.use(text, true);
      onClose();
      if (said !== null) toast(said, "info");
      if (truncated) toast(listLong, "info");
    } catch (e) {
      setError(e instanceof Error ? e.message : "The photo could not be read just now. Try again.");
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="photo-ai" data-testid={testid}>
      <p className="photo-ai-said">{offerSaid(offer.said)}</p>
      <div className="photo-ai-row">
        <button type="button" className="primary small" onClick={() => void go()} disabled={busy} data-testid={`${testid}-go`}>
          <Icon name="sparkle" />
          {busy ? "Reading with AI…" : "Read it with AI"}
        </button>
        <button type="button" className="small" onClick={onClose} disabled={busy} data-testid={`${testid}-no`}>
          Type it in
        </button>
      </div>
      <p className="photo-ai-note">{aiOfferLine}</p>
      {error && (
        <p className="photo-ai-error" role="alert" data-testid={`${testid}-error`}>
          {error}
        </p>
      )}
    </div>
  );
}

/** The offer in a dialog of its own: for a chat's photo, whose menu has
 * closed. */
export function AIReadOfferModal({ offer, onClose }: { offer: AIOffer | undefined; onClose: () => void }) {
  if (!offer) return null;
  return (
    <Modal title="Read the photo with AI?" onClose={onClose}>
      <AIReadOffer offer={offer} onClose={onClose} />
    </Modal>
  );
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
