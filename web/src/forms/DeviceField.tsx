// A device field on the web (docs/spec/device-fields.md §3, §4, D1): the
// browser is asked only when the person presses the field's button, after
// its why. A location is the browser's own prompt; a photo the camera
// where the browser offers it (capture), else a file, marked sent either
// way; a signature a pad (drawn with a finger or the mouse, in ink on
// white, with the signer's name where the question asks it: D2); a scan
// the code typed. A refusal is an answer.
import { useEffect, useId, useRef, useState } from "react";
import {
  canScanWithCamera,
  scanFormatOf,
  scanFormatsOf,
  deviceSummary,
  labelOf,
  locationAnswer,
  locationRefusal,
  type DeviceKind,
  type FieldSchema,
} from "../lib/forms";

interface Props {
  name: string;
  kind: DeviceKind;
  schema: FieldSchema;
  required: boolean;
  value: unknown;
  error?: string;
  onChange: (v: unknown) => void;
}

type WithWhy = FieldSchema & {
  why?: string;
  allow_typed?: boolean;
  ask_name?: boolean;
};

export function DeviceField({
  name,
  kind,
  schema,
  required,
  value,
  error,
  onChange,
}: Props) {
  const id = useId();
  const p = schema as WithWhy;
  const label = labelOf(name, schema);
  const errId = `${id}-err`;
  const whyId = `${id}-why`;
  const [reading, setReading] = useState(false);
  const [problem, setProblem] = useState("");
  const [typing, setTyping] = useState(false);
  const summary = deviceSummary(kind, value);
  const mark = required ? (
    <span aria-hidden="true"> *</span>
  ) : (
    <span className="hint"> (optional)</span>
  );

  const readLocation = () => {
    setProblem("");
    if (!("geolocation" in navigator)) {
      onChange(locationRefusal(2));
      setProblem("This browser cannot read where you are.");
      return;
    }
    setReading(true);
    navigator.geolocation.getCurrentPosition(
      (pos) => {
        setReading(false);
        onChange(locationAnswer(pos));
      },
      (err) => {
        setReading(false);
        onChange(locationRefusal(err.code));
        setProblem(
          err.code === 1
            ? "Your browser was not allowed to read where you are, so you said you cannot answer this. Allow it in the browser's settings and press the button again."
            : "Your browser could not read where you are just now. Try again, outside or near a window.",
        );
      },
      { enableHighAccuracy: true, timeout: 20000, maximumAge: 0 },
    );
  };

  return (
    <fieldset
      className="form-field device-field"
      aria-describedby={[whyId, error ? errId : ""].filter(Boolean).join(" ")}
    >
      <legend>
        {label}
        {mark}
      </legend>
      {p.why && (
        <p className="hint" id={whyId}>
          Why: {p.why}
        </p>
      )}
      {kind === "location" && (
        <div className="toolbar">
          <button type="button" onClick={readLocation} disabled={reading}>
            {reading
              ? "Reading your location…"
              : value && !(value as { state?: string }).state
                ? "Read it again"
                : "Use my location"}
          </button>
          {p.allow_typed !== false && (
            <button
              type="button"
              className="link"
              onClick={() => setTyping((t) => !t)}
            >
              Type the address instead
            </button>
          )}
        </div>
      )}
      {kind === "location" && typing && (
        <input
          aria-label={`${label}: the address`}
          placeholder="12 Allen Avenue, Ikeja"
          defaultValue={
            (value as { address?: string } | undefined)?.address ?? ""
          }
          onChange={(e) =>
            onChange(
              e.target.value.trim() ? { address: e.target.value } : undefined,
            )
          }
        />
      )}
      {kind === "photo" && (
        <input
          type="file"
          accept="image/jpeg,image/png"
          capture="environment"
          aria-label={`${label}: take or choose a photo`}
          onChange={(e) => onChange(e.target.files?.[0])}
        />
      )}
      {kind === "signature" && (
        <SignaturePad
          label={label}
          askName={p.ask_name === true}
          onChange={onChange}
        />
      )}
      {kind === "scan" && (
        <CodeScan
          label={label}
          formats={scanFormatsOf(schema)}
          allowTyped={p.allow_typed !== false}
          value={value}
          onChange={onChange}
        />
      )}
      {summary && (
        <p className="hint" role="status">
          {summary}
        </p>
      )}
      {problem && (
        <p className="hint" role="alert">
          {problem}
        </p>
      )}
      {!summary && (
        <button
          type="button"
          className="link"
          onClick={() =>
            onChange({ state: "refused", reason: "I cannot answer this here" })
          }
        >
          I can't answer this
        </button>
      )}
      {error && (
        <p className="field-error" id={errId}>
          {error}
        </p>
      )}
    </fieldset>
  );
}

/** The browser's own barcode reader (no outside library). */
interface Detector {
  detect(v: HTMLVideoElement): Promise<{ rawValue: string; format: string }[]>;
}
type DetectorClass = {
  new (o: { formats: string[] }): Detector;
  getSupportedFormats(): Promise<string[]>;
};

/** A code: typed, or (build-plan-gaps H3, g-scan-web) read by the camera
 * where the browser has a barcode reader, asked only at the tap. A code
 * read keeps its format (the server marks it read by the camera); typed
 * over, it is typed. */
function CodeScan({
  label,
  formats,
  allowTyped,
  value,
  onChange,
}: {
  label: string;
  formats: string[];
  allowTyped: boolean;
  value: unknown;
  onChange: (v: unknown) => void;
}) {
  const video = useRef<HTMLVideoElement>(null);
  const stream = useRef<MediaStream | null>(null);
  const timer = useRef<number>(0);
  const [on, setOn] = useState(false);
  const [problem, setProblem] = useState("");
  const [usable, setUsable] = useState<string[]>([]);
  const text = (value as { text?: string } | undefined)?.text ?? "";

  useEffect(() => {
    if (!canScanWithCamera()) return;
    const D = (window as unknown as { BarcodeDetector: DetectorClass }).BarcodeDetector;
    D.getSupportedFormats()
      .then((have) => setUsable(have.filter((f) => formats.includes(scanFormatOf(f) ?? ""))))
      .catch(() => setUsable([]));
  }, [formats]);

  const stop = () => {
    window.clearTimeout(timer.current);
    stream.current?.getTracks().forEach((t) => t.stop());
    stream.current = null;
    setOn(false);
  };
  useEffect(() => stop, []);

  const start = async () => {
    setProblem("");
    const D = (window as unknown as { BarcodeDetector: DetectorClass }).BarcodeDetector;
    try {
      const s = await navigator.mediaDevices.getUserMedia({ video: { facingMode: "environment" }, audio: false });
      stream.current = s;
      setOn(true);
      const v = video.current;
      if (!v) return stop();
      v.srcObject = s;
      await v.play();
      const detector = new D({ formats: usable });
      const look = async () => {
        if (!stream.current) return;
        try {
          const found = await detector.detect(v);
          const hit = found.find((c) => formats.includes(scanFormatOf(c.format) ?? ""));
          if (hit) {
            onChange({ text: hit.rawValue, format: scanFormatOf(hit.format) });
            stop();
            return;
          }
        } catch {
          // A frame not ready yet: the next one.
        }
        timer.current = window.setTimeout(() => void look(), 300);
      };
      void look();
    } catch (e) {
      stop();
      setProblem(
        (e as { name?: string }).name === "NotAllowedError"
          ? "The camera was not allowed. Type the code instead."
          : "The camera could not start. Type the code instead.",
      );
    }
  };

  return (
    <div className="code-scan">
      {usable.length > 0 && (
        <div className="toolbar">
          <button type="button" onClick={() => (on ? stop() : void start())}>
            {on ? "Stop the camera" : "Scan with the camera"}
          </button>
        </div>
      )}
      {usable.length > 0 && (
        <video
          ref={video}
          hidden={!on}
          className="code-scan-video"
          playsInline
          muted
          aria-label={`${label}: the camera's view; hold the code in front of it`}
        />
      )}
      {(allowTyped || text) && (
        <input
          aria-label={`${label}: type the code`}
          placeholder="The code under the barcode"
          value={text}
          readOnly={!allowTyped}
          onChange={(e) =>
            onChange(e.target.value.trim() ? { text: e.target.value.trim() } : undefined)
          }
        />
      )}
      {problem && (
        <p className="hint" role="alert">
          {problem}
        </p>
      )}
    </div>
  );
}

/** The pad is paper in both themes (styles.css `.signature-pad`'s
 * --paper and --paper-ink): ink on white, so what is kept reads the same
 * wherever it is shown. */
function tone(c: HTMLCanvasElement, name: string, fallback: string) {
  return getComputedStyle(c).getPropertyValue(name).trim() || fallback;
}

function paper(c: HTMLCanvasElement | null) {
  const ctx = c?.getContext("2d");
  if (!c || !ctx) return;
  ctx.fillStyle = tone(c, "--paper", "white");
  ctx.fillRect(0, 0, c.width, c.height);
}

/** A pad to sign on: pointer strokes, sent as a PNG; the signer's name
 * beside it where the question asks. */
function SignaturePad({
  label,
  askName,
  onChange,
}: {
  label: string;
  askName: boolean;
  onChange: (v: unknown) => void;
}) {
  const ref = useRef<HTMLCanvasElement>(null);
  const drawing = useRef(false);
  const [signed, setSigned] = useState(false);
  const file = useRef<File | undefined>(undefined);
  const name = useRef("");
  const nameId = useId();
  const tell = () =>
    onChange(
      file.current && askName
        ? { file: file.current, signed_by: name.current }
        : file.current,
    );
  useEffect(() => {
    const c = ref.current;
    if (!c) return;
    c.width = c.clientWidth || 320;
    c.height = c.clientHeight || 160;
    paper(c);
  }, []);
  const at = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    return [e.clientX - r.left, e.clientY - r.top] as const;
  };
  const done = () => {
    drawing.current = false;
    ref.current?.toBlob((b) => {
      if (!b) return;
      file.current = new File([b], "signature.png", { type: "image/png" });
      tell();
    }, "image/png");
  };
  return (
    <div>
      <canvas
        ref={ref}
        className="signature-pad"
        role="img"
        aria-label={`${label}: sign here with a finger or the mouse`}
        onPointerDown={(e) => {
          const ctx = e.currentTarget.getContext("2d");
          if (!ctx) return;
          drawing.current = true;
          e.currentTarget.setPointerCapture(e.pointerId);
          ctx.lineWidth = 2;
          ctx.lineCap = "round";
          ctx.strokeStyle = tone(e.currentTarget, "--paper-ink", "black");
          ctx.beginPath();
          ctx.moveTo(...at(e));
        }}
        onPointerMove={(e) => {
          if (!drawing.current) return;
          const ctx = e.currentTarget.getContext("2d");
          ctx?.lineTo(...at(e));
          ctx?.stroke();
          setSigned(true);
        }}
        onPointerUp={done}
        onPointerLeave={() => drawing.current && done()}
      />
      {signed && (
        <button
          type="button"
          className="link"
          onClick={() => {
            paper(ref.current);
            setSigned(false);
            file.current = undefined;
            onChange(undefined);
          }}
        >
          Clear
        </button>
      )}
      {askName && (
        <div className="form-field">
          <label htmlFor={nameId}>Name of who signed</label>
          <input
            id={nameId}
            maxLength={120}
            autoComplete="off"
            onChange={(e) => {
              name.current = e.target.value;
              if (file.current) tell();
            }}
          />
        </div>
      )}
    </div>
  );
}
