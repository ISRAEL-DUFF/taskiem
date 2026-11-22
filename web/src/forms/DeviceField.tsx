// A device field on the web (docs/spec/device-fields.md §3, §4, D1): the
// browser is asked only when the person presses the field's button, after
// its why. A location is the browser's own prompt; a photo the camera
// where the browser offers it (capture), else a file, marked sent either
// way; a signature a pad; a scan the code typed. A refusal is an answer.
import { useEffect, useId, useRef, useState } from "react";
import {
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

type WithWhy = FieldSchema & { why?: string; allow_typed?: boolean };

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
        <SignaturePad label={label} onChange={onChange} />
      )}
      {kind === "scan" && (
        <input
          aria-label={`${label}: type the code`}
          placeholder="The code under the barcode"
          onChange={(e) =>
            onChange(
              e.target.value.trim()
                ? { text: e.target.value.trim() }
                : undefined,
            )
          }
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

/** A pad to sign on: pointer strokes, sent as a PNG. */
function SignaturePad({
  label,
  onChange,
}: {
  label: string;
  onChange: (v: unknown) => void;
}) {
  const ref = useRef<HTMLCanvasElement>(null);
  const drawing = useRef(false);
  const [signed, setSigned] = useState(false);
  useEffect(() => {
    const c = ref.current;
    if (!c) return;
    c.width = c.clientWidth || 320;
    c.height = c.clientHeight || 160;
  }, []);
  const at = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    return [e.clientX - r.left, e.clientY - r.top] as const;
  };
  const done = () => {
    drawing.current = false;
    ref.current?.toBlob((b) => {
      if (b) onChange(new File([b], "signature.png", { type: "image/png" }));
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
          ctx.strokeStyle = getComputedStyle(e.currentTarget).color;
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
            const c = ref.current;
            c?.getContext("2d")?.clearRect(0, 0, c.width, c.height);
            setSigned(false);
            onChange(undefined);
          }}
        >
          Clear
        </button>
      )}
    </div>
  );
}
