import { useState } from "react";
import { get, put } from "../api";
import { KitLoading, SavedTick, Sheet } from "../kit";
import { availabilityWord, type Availability } from "../lib/tools";
import { ErrorBox, useLoad } from "../ui";

// A rider's business says whether it is free, busy till a time, or off
// (docs/spec/chat-riders.md, phase 3). Every business that uses it as a
// rider sees the state in its riders list and when it offers a delivery,
// and Explore shows it on the card. The words on, off and busy till 3
// from the business's WhatsApp do the same.

export function AvailabilitySheet({
  from,
  onClose,
  onChanged,
}: {
  from?: DOMRect | null;
  onClose: () => void;
  onChanged?: () => void;
}) {
  const [n, setN] = useState(0);
  const now = useLoad(() => get<Availability>("/v1/availability"), [n]);
  const [until, setUntil] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [saved, setSaved] = useState<string>();
  const a = now.data;
  const say = async (state: "on" | "off" | "busy") => {
    setBusy(true);
    setError(undefined);
    try {
      const next = await put<Availability>("/v1/availability", {
        state,
        ...(state === "busy" && until.trim() && { until: until.trim() }),
      });
      setSaved(availabilityWord(next));
      setN((x) => x + 1);
      onChanged?.();
      window.setTimeout(() => setSaved(undefined), 1300);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet
      title="Availability"
      from={from}
      onClose={onClose}
      testid="availability-sheet"
    >
      {saved && <SavedTick words={saved} />}
      {!a ? (
        now.error ? (
          <ErrorBox error={now.error} />
        ) : (
          <KitLoading rows={2} label="Checking your availability…" />
        )
      ) : (
        <div className="tk-sheet-list tk-form-body">
          <p className="tk-avail-now" data-testid="availability-now">
            <strong>{availabilityWord(a)}</strong>
            <span className="hint">
              {a.businesses === 0
                ? "No business has added you as its rider yet. Once one does, what you choose here is what they see."
                : a.businesses === 1
                  ? "The business you deliver for sees this when it offers a delivery."
                  : `The ${a.businesses} businesses you deliver for see this when they offer a delivery.`}
            </span>
          </p>
          <ErrorBox error={error} />
          <div
            className="segmented tk-avail-pick"
            role="group"
            aria-label="Say whether you are free"
          >
            <button
              type="button"
              aria-pressed={a.state === "free"}
              disabled={busy}
              onClick={() => void say("on")}
              data-testid="availability-on"
            >
              Free
            </button>
            <button
              type="button"
              aria-pressed={a.state === "busy"}
              disabled={busy}
              onClick={() => void say("busy")}
              data-testid="availability-busy"
            >
              Busy
            </button>
            <button
              type="button"
              aria-pressed={a.state === "off"}
              disabled={busy}
              onClick={() => void say("off")}
              data-testid="availability-off"
            >
              Off
            </button>
          </div>
          <label className="tk-p-field">
            Busy till (a time today, like 3pm)
            <input
              value={until}
              placeholder="Leave empty for the rest of the day"
              disabled={busy}
              onChange={(e) => setUntil(e.target.value)}
              data-testid="availability-until"
            />
          </label>
          {a.open ? (
            <p className="hint">
              You hold {a.open} {a.open === 1 ? "delivery" : "deliveries"} now,
              so you are busy until they are delivered, whatever you choose.
            </p>
          ) : null}
        </div>
      )}
    </Sheet>
  );
}
