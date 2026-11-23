import { useId, useRef, useState } from "react";
import { get, post } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { useSheetStep } from "../kit";
import { assert } from "../passkeys";
import { EmptyState, ErrorBox, Items, Modal, Row, Skeleton, useLoad } from "../ui";
import {
  revealProblem,
  savedBase,
  savedDates,
  savedDateSub,
  savedIcon,
  savedKindsBase,
  savedPhone,
  savedSub,
  type KindList,
  type SavedKind,
  type SavedList,
  type SavedThing,
} from "../lib/saved";

// A form's Saved button (saved objects S2, §6.4): anywhere a form asks for
// a person, an address, a phone, a bank account or a date, it offers the
// owner's saved things of that kind, and picking one fills the form. Both
// lists are read when it opens: the person's own, and in a business the
// business's. A person with no business has only their own.

/** What a Saved button fills: a person (a customer), a place (an
 * address), a phone (from saved people and places), an email (a saved
 * person's), a bank account (its number shown with a passkey) or a date
 * (saved on people and things). */
export type FillKind = "person" | "place" | "phone" | "email" | "bank_account" | "date";

const FILL: Record<FillKind, { many: string; icon: string; list?: SavedKind; none: string }> = {
  person: { many: "people", icon: "members", list: "person", none: "Nothing saved of this kind yet." },
  place: { many: "places", icon: "place", list: "place", none: "Nothing saved of this kind yet." },
  phone: { many: "phones", icon: "members", none: "No saved person or place has a phone yet." },
  email: { many: "emails", icon: "members", list: "person", none: "No saved person has an email yet." },
  bank_account: { many: "accounts", icon: "money", list: "bank_account", none: "Nothing saved of this kind yet." },
  date: { many: "dates", icon: "calendar", none: "No dates saved on your things yet." },
};

const none: SavedList = { saved: [], limit: 0 };
const noKinds: KindList = { kinds: [], starters: [], limit: 0, to_change: false };

/** One choice in the list: a saved thing, and what it fills (a phone, a
 * day; "" for a person, a place or an account). */
interface Choice {
  key: string;
  thing: SavedThing;
  sub: string;
  value: string;
  /** What its Use button says to a screen reader. */
  use: string;
}

/**
 * The Saved button and what it opens. label names what it fills ("the
 * customer"), for a screen reader. onPick gets the thing, the value it
 * fills (a phone, a YYYY-MM-DD day, an account's number in full once a
 * passkey showed it) and, for an account whose number could not be shown,
 * a few words why. In a chat tool's sheet (inSheet) the list opens under
 * the button as a step of the sheet, Back closing it: a dialog over the
 * sheet would take its Esc. After a pick, focus goes back to the button
 * unless onPick moved it to a field.
 */
export function SavedFill({
  kind,
  onPick,
  label,
  inSheet = false,
}: {
  kind: FillKind;
  onPick: (thing: SavedThing, value: string, problem?: string) => void;
  label: string;
  inSheet?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const listId = useId();
  const btn = useRef<HTMLButtonElement>(null);
  useSheetStep(inSheet && open, () => setOpen(false));
  const words = FILL[kind];
  const refocus = () =>
    requestAnimationFrame(() => {
      const at = document.activeElement;
      if (!at || at === document.body) btn.current?.focus();
    });
  const pick = async (c: Choice) => {
    setOpen(false);
    if (kind !== "bank_account") {
      onPick(c.thing, c.value);
      refocus();
      return;
    }
    // An account's number is never listed in full: it is shown for this
    // pick only, with the person's passkey.
    setBusy(true);
    try {
      const base = savedBase(c.thing.owner === "mine");
      const passkey = await assert(`${base}/step-up/options`, { operation: "saved.reveal", target: c.thing.id });
      const r = await post<{ account_number?: string }>(`${base}/${encodeURIComponent(c.thing.id)}/reveal`, { passkey });
      onPick(c.thing, r.account_number ?? "");
    } catch (e) {
      onPick(c.thing, "", revealProblem(e));
    } finally {
      setBusy(false);
      refocus();
    }
  };
  const button = (
    <button
      ref={btn}
      type="button"
      className={inSheet ? "tk-btn ghost saved-fill-button" : "small saved-fill-button"}
      aria-expanded={inSheet ? open : undefined}
      aria-controls={inSheet && open ? listId : undefined}
      aria-busy={busy || undefined}
      disabled={busy}
      onClick={() => setOpen((o) => !o)}
      data-testid="saved-fill"
    >
      Saved
      <span className="sr-only">
        {" "}
        {words.many} for {label}
      </span>
    </button>
  );
  if (inSheet)
    return (
      <div className="saved-fill-sheet">
        {button}
        {open && <SavedChoices id={listId} kind={kind} onPick={(c) => void pick(c)} inSheet />}
      </div>
    );
  return (
    <>
      {button}
      {open && (
        <Modal title={`Saved ${words.many}`} onClose={() => setOpen(false)}>
          <SavedChoices id={listId} kind={kind} onPick={(c) => void pick(c)} />
          <div className="toolbar">
            <button type="button" onClick={() => setOpen(false)}>
              Close
            </button>
          </div>
        </Modal>
      )}
    </>
  );
}

/** A saved thing's choices for kind: one per thing, or one per date. */
function choicesOf(kind: FillKind, things: SavedThing[], kinds: KindList["kinds"]): Choice[] {
  if (kind === "date")
    return savedDates(things, kinds).map((d) => ({ key: d.key, thing: d.thing, sub: savedDateSub(d), value: d.date, use: `${d.thing.title}: ${d.label}` }));
  if (kind === "phone")
    return things.flatMap((x) => {
      const p = savedPhone(x);
      return p ? [{ key: x.id, thing: x, sub: savedSub(x), value: p, use: x.title }] : [];
    });
  if (kind === "email")
    return things.flatMap((x) => {
      const e = x.kind === "person" ? (x.details.email ?? "").trim() : "";
      return e && !e.includes("•") ? [{ key: x.id, thing: x, sub: savedSub(x), value: e, use: x.title }] : [];
    });
  return things.filter((x) => x.kind === kind).map((x) => ({ key: x.id, thing: x, sub: savedSub(x), value: "", use: x.title }));
}

function SavedChoices({ id, kind, onPick, inSheet = false }: { id: string; kind: FillKind; onPick: (c: Choice) => void; inSheet?: boolean }) {
  const { me } = useAuth();
  const inBusiness = !!me?.tenant_id && !me.personal;
  const words = FILL[kind];
  // One kind is asked for by the lists; a phone (people and places) and a
  // date (people and things of the owner's kinds) read every kind.
  const q = words.list ? `?kind=${words.list}` : "";
  const mine = useLoad(() => get<SavedList>(`${savedBase(true)}${q}`), [q]);
  const business = useLoad(() => (inBusiness ? get<SavedList>(`${savedBase(false)}${q}`) : Promise.resolve(none)), [inBusiness, q]);
  // A date field's type is in its kind: the owner's kinds, read for dates.
  const dates = kind === "date";
  const myKinds = useLoad(() => (dates ? get<KindList>(savedKindsBase(true)) : Promise.resolve(noKinds)), [dates]);
  const bizKinds = useLoad(() => (dates && inBusiness ? get<KindList>(savedKindsBase(false)) : Promise.resolve(noKinds)), [dates, inBusiness]);
  const groups = [
    { key: "mine", title: "Yours", list: choicesOf(kind, mine.data?.saved ?? [], myKinds.data?.kinds ?? []) },
    { key: "business", title: "The business's", list: choicesOf(kind, business.data?.saved ?? [], bizKinds.data?.kinds ?? []) },
  ].filter((g) => g.list.length > 0);
  const loaded = (x: { data?: unknown; error?: unknown }) => !!(x.data || x.error);
  const done = loaded(mine) && loaded(business) && loaded(myKinds) && loaded(bizKinds);
  // One error, however many lists failed; and no "nothing saved" over it.
  const error = mine.error ?? business.error;
  return (
    <div className="saved-fill-list" id={id} data-testid="saved-fill-list">
      <ErrorBox error={error} />
      {!done ? (
        <Skeleton rows={2} label={`Loading saved ${words.many}…`} />
      ) : groups.length === 0 ? (
        !error && (
          <EmptyState
            icon={words.icon}
            action={
              <a href={inBusiness ? "/settings/saved" : "/you/settings"} target="_blank" rel="noreferrer">
                Saved things<span className="sr-only"> (opens in a new tab)</span>
              </a>
            }
          >
            {words.none}
          </EmptyState>
        )
      ) : (
        groups.map((g) => (
          <section key={g.key}>
            {groups.length > 1 && <h3 className="saved-fill-group">{g.title}</h3>}
            {inSheet ? (
              <ul aria-label={g.title}>
                {g.list.map((c) => (
                  <li key={c.key}>
                    <button type="button" className="tk-row saved-fill-row" onClick={() => onPick(c)} data-testid="saved-fill-pick">
                      <span className="tk-row-tile" aria-hidden="true">
                        <Icon name={savedIcon(c.thing.kind)} />
                      </span>
                      <span className="tk-row-words">
                        <span className="tk-row-name">{c.thing.title}</span>
                        <span className="tk-row-sub">{c.sub}</span>
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            ) : (
              <Items label={g.title}>
                {g.list.map((c) => (
                  <Row key={c.key} icon={savedIcon(c.thing.kind)} title={c.thing.title} sub={<span className="sub">{c.sub}</span>} testid="saved-fill-row">
                    <button type="button" className="small saved-fill-use" onClick={() => onPick(c)}>
                      Use<span className="sr-only"> {c.use}</span>
                    </button>
                  </Row>
                ))}
              </Items>
            )}
          </section>
        ))
      )}
    </div>
  );
}
