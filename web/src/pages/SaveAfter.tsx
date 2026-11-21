import { useState } from "react";
import { ApiError, post } from "../api";
import { ErrorBox, Field, Items, Row } from "../ui";
import { toast } from "../toast";
import { cleanName, nameProblem, placeBody, type PlaceOffer, type SavedThing } from "../lib/saved";

// Saving after an action (saved objects S3, §9.3: after a new customer's
// first order, after a delivery to a new address): one quiet row, offered
// once, saved with one click; suggested, never done for them. A name
// already taken asks for another in a small field under it, filled in to
// change. As the app does it (mobile/lib/chat/save_after.dart).

/** What was offered, saved: "Saved as @ngozi". */
export function SavedDone({ name, what, testid }: { name: string; what: "people" | "places"; testid: string }) {
  return (
    <Items label="Saved">
      <Row
        icon="check"
        tone="ok"
        title={`Saved as @${name}`}
        sub={<span className="sub">Among your saved {what}, for next time</span>}
        testid={`${testid}-done`}
      />
    </Items>
  );
}

/** After a typed address is saved as where something is delivered: "Save
 * this place for next time?", among the business's saved places. */
export function PlaceSaveOffer({ offer }: { offer: PlaceOffer }) {
  return (
    <SaveAfter
      title="Save this place for next time?"
      sub={`As @${offer.name}, among your saved places`}
      icon="place"
      name={offer.name}
      what="places"
      testid="save-place"
      save={async (name) => (await post<{ saved: SavedThing }>("/v1/saved", placeBody(offer, name ?? offer.name))).saved}
    />
  );
}

const taken = (e: unknown) => e instanceof ApiError && e.status === 409 && !!e.body?.name_taken;

export function SaveAfter({
  title,
  sub,
  icon,
  name,
  what,
  save,
  onSaved,
  testid,
}: {
  title: string;
  sub: string;
  icon: string;
  /** The @name it is offered as; "" when the server makes it. */
  name: string;
  /** Where it is kept once saved: "people" or "places". */
  what: "people" | "places";
  /** Saves it with name (undefined: the server's own). */
  save: (name?: string) => Promise<SavedThing>;
  /** Told the name it was saved as, for a page that reads its offer again
   * (and so loses it) to keep saying so. */
  onSaved?: (name: string) => void;
  testid: string;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  // The name was taken: another, typed.
  const [asking, setAsking] = useState(false);
  const [typed, setTyped] = useState("");
  const [problem, setProblem] = useState("");
  const [saved, setSaved] = useState("");
  const [gone, setGone] = useState(false);
  if (gone) return null;
  const done = (x: SavedThing) => {
    setSaved(x.name);
    setAsking(false);
    toast(`Saved as @${x.name}`);
    onSaved?.(x.name);
  };
  const first = async () => {
    setBusy(true);
    setError(undefined);
    try {
      done(await save(name || undefined));
    } catch (e) {
      if (taken(e)) {
        setTyped(name ? `@${name}` : "");
        setProblem("");
        setAsking(true);
      } else setError(e);
    } finally {
      setBusy(false);
    }
  };
  const again = async () => {
    const p = nameProblem(typed);
    if (p) return setProblem(p);
    setBusy(true);
    setProblem("");
    setError(undefined);
    try {
      done(await save(cleanName(typed)));
    } catch (e) {
      if (taken(e)) setProblem("That name is taken too: choose another.");
      else setError(e);
    } finally {
      setBusy(false);
    }
  };
  if (saved) return <SavedDone name={saved} what={what} testid={testid} />;
  return (
    <Items label={title}>
      <Row icon={icon} title={title} sub={<span className="sub">{sub}</span>} testid={testid}>
        {!asking && (
          <button type="button" className="small" disabled={busy} onClick={() => void first()}>
            Save<span className="sr-only">{name ? ` as @${name}` : " for next time"}</span>
          </button>
        )}
      </Row>
      {asking && (
        <li className="save-after-name">
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void again();
            }}
          >
            <p className="hint" role="status">
              {name ? `@${name} is taken` : "That name is taken"}: choose another.
            </p>
            <Field label="Name to call it up by" hint='Letters and numbers, with "-" between words'>
              <input
                value={typed}
                maxLength={33}
                autoComplete="off"
                autoFocus
                aria-invalid={problem ? true : undefined}
                onChange={(e) => setTyped(e.target.value)}
              />
            </Field>
            {problem && (
              <p className="error-text" role="alert">
                {problem}
              </p>
            )}
            <div className="toolbar">
              <button type="submit" className="primary" disabled={busy || !cleanName(typed)}>
                Save
              </button>
              <button type="button" disabled={busy} onClick={() => setGone(true)}>
                Not now
              </button>
            </div>
          </form>
        </li>
      )}
      {error !== undefined && (
        <li className="save-after-name">
          <ErrorBox error={error} />
        </li>
      )}
    </Items>
  );
}
