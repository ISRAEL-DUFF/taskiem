import { useCallback, useEffect, useRef, useState } from "react";
import { Icon } from "../icons";

// Voice notes, as WhatsApp records them (business links L3, §5.5): hold
// the microphone to record, release to send, slide left to cancel; a tap
// (or the keyboard) records until Done or Cancel instead.

const MAX_VOICE_SECONDS = 600;
/** A press shorter than this is a tap, not a hold. */
const HOLD_MS = 400;
/** Sliding this far left while holding cancels. */
const SLIDE_CANCEL_PX = 80;

export const minsSecs = (s: number) =>
  `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;

export type VoiceNote = ReturnType<typeof useVoiceNote>;

/** Records a voice note with the browser's microphone: secs counts up
 * while recording; stop(true) hands the note to onDone (send: it was a
 * hold, released, and goes at once), stop(false) throws it away. holding
 * is a hold under way. Unavailable where the browser cannot record. */
export function useVoiceNote(
  onDone: (f: File, send: boolean) => void,
  onError: (e: unknown) => void,
) {
  const [secs, setSecs] = useState<number>();
  const [holding, setHolding] = useState(false);
  const rec = useRef<MediaRecorder | null>(null);
  const keep = useRef(false);
  const sendNow = useRef(false);
  const cancelled = useRef(false); // stopped before the microphone opened
  const pressedAt = useRef(0);
  const done = useRef(onDone);
  done.current = onDone;
  const can =
    typeof window !== "undefined" &&
    typeof window.MediaRecorder !== "undefined" &&
    !!navigator.mediaDevices?.getUserMedia;
  const stop = useCallback((keepIt: boolean, send = false) => {
    keep.current = keepIt;
    sendNow.current = send;
    setHolding(false);
    if (rec.current && rec.current.state !== "inactive") rec.current.stop();
    else cancelled.current = true;
  }, []);
  useEffect(() => () => stop(false), [stop]);
  useEffect(() => {
    if (secs === undefined) return;
    if (secs >= MAX_VOICE_SECONDS) {
      stop(true);
      return;
    }
    const t = setTimeout(() => setSecs((s) => (s ?? 0) + 1), 1000);
    return () => clearTimeout(t);
  }, [secs, stop]);
  const start = async () => {
    cancelled.current = false;
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      if (cancelled.current) {
        stream.getTracks().forEach((t) => t.stop());
        return;
      }
      const type = [
        "audio/ogg;codecs=opus",
        "audio/webm;codecs=opus",
        "audio/mp4",
      ].find((t) => MediaRecorder.isTypeSupported(t));
      const r = new MediaRecorder(
        stream,
        type ? { mimeType: type } : undefined,
      );
      const chunks: Blob[] = [];
      r.ondataavailable = (e) => {
        if (e.data.size > 0) chunks.push(e.data);
      };
      r.onstop = () => {
        stream.getTracks().forEach((t) => t.stop());
        setSecs(undefined);
        rec.current = null;
        if (!keep.current || chunks.length === 0) return;
        const mime = (r.mimeType || "audio/webm").split(";")[0] ?? "audio/webm";
        const ext = mime.includes("ogg")
          ? "ogg"
          : mime.includes("mp4")
            ? "m4a"
            : "webm";
        done.current(
          new File(chunks, `voice-note.${ext}`, { type: mime }),
          sendNow.current,
        );
      };
      keep.current = false;
      rec.current = r;
      r.start();
      setSecs(0);
    } catch {
      setHolding(false);
      onError(
        new Error(
          "The microphone could not be used. Allow it for this page and try again.",
        ),
      );
    }
  };
  /** A press on the microphone: recording starts; released after a hold
   * it is sent, slid left it is cancelled, released at once it is a tap
   * (Done or Cancel then). */
  const press = (x: number) => {
    pressedAt.current = Date.now();
    setHolding(true);
    void start();
    const move = (e: PointerEvent) => {
      if (x - e.clientX > SLIDE_CANCEL_PX) {
        off();
        stop(false);
      }
    };
    const up = () => {
      off();
      if (Date.now() - pressedAt.current < HOLD_MS) setHolding(false);
      else stop(true, true);
    };
    const off = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", up);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", up);
  };
  /** A click that was not part of a press (the keyboard): a tap. */
  const click = () => {
    if (Date.now() - pressedAt.current < 1000) return;
    void start();
  };
  return { can, secs, holding, start, stop, press, click };
}

/** The microphone: hold to record, or tap. */
export function MicButton({
  voice,
  onStart,
}: {
  voice: VoiceNote;
  onStart?: () => void;
}) {
  return (
    <button
      type="button"
      className="icon-button primary"
      aria-label="Record a voice note"
      title="Hold to record, release to send. Tap to record and send when you are done."
      data-testid="chat-mic"
      onPointerDown={(e) => {
        if (e.button !== 0) return;
        e.preventDefault();
        onStart?.();
        voice.press(e.clientX);
      }}
      onClick={() => {
        onStart?.();
        voice.click();
      }}
    >
      <Icon name="mic" />
    </button>
  );
}

/** The composer while a voice note records: the time, and Cancel and
 * Done, or while held, how to send or cancel it. */
export function RecordingBar({ voice }: { voice: VoiceNote }) {
  const secs = voice.secs ?? 0;
  return (
    <div className="composer" data-testid="chat-recording">
      <span className="recording" role="status">
        <span className="dot" aria-hidden="true" />
        Recording {minsSecs(secs)}
        {voice.holding && (
          <span className="hint"> · release to send, slide left to cancel</span>
        )}
      </span>
      {!voice.holding && (
        <>
          <button
            type="button"
            className="small"
            onClick={() => voice.stop(false)}
          >
            Cancel<span className="sr-only"> the voice note</span>
          </button>
          <button
            type="button"
            className="primary"
            onClick={() => voice.stop(true)}
          >
            Done
          </button>
        </>
      )}
    </div>
  );
}
