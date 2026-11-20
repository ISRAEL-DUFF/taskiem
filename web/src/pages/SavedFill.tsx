import { useState } from "react";
import { Link } from "react-router-dom";
import { get } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { useSheetStep } from "../kit";
import { EmptyState, ErrorBox, Items, Modal, Row, Skeleton, useLoad } from "../ui";
import { KIND_WORDS, savedIcon, savedSub, type SavedList, type SavedThing } from "../lib/saved";

// A form's Saved button (saved objects S2, §6.4): it offers the owner's
// saved things of the kind the form asks for, a person or a place, and
// picking one fills the form. Both lists are read when it opens: the
// person's own, and in a business the business's. A person with no
// business has only their own.

const none: SavedList = { saved: [], limit: 0 };

/** The Saved button and what it opens. label names what it fills ("the
 * customer"), for a screen reader. In a chat tool's sheet (inSheet) the
 * list opens under the button as a step of the sheet, Back closing it: a
 * dialog over the sheet would take its Esc. */
export function SavedFill({
  kind,
  onPick,
  label,
  inSheet = false,
}: {
  kind: "person" | "place";
  onPick: (thing: SavedThing) => void;
  label: string;
  inSheet?: boolean;
}) {
  const [open, setOpen] = useState(false);
  useSheetStep(inSheet && open, () => setOpen(false));
  const words = KIND_WORDS[kind];
  const pick = (x: SavedThing) => {
    setOpen(false);
    onPick(x);
  };
  const button = (
    <button
      type="button"
      className={inSheet ? "tk-btn ghost saved-fill-button" : "small saved-fill-button"}
      aria-expanded={inSheet ? open : undefined}
      onClick={() => setOpen((o) => !o)}
      data-testid="saved-fill"
    >
      Saved<span className="sr-only"> {words.many.toLowerCase()} for {label}</span>
    </button>
  );
  if (inSheet)
    return (
      <div className="saved-fill-sheet">
        {button}
        {open && <SavedChoices kind={kind} onPick={pick} inSheet />}
      </div>
    );
  return (
    <>
      {button}
      {open && (
        <Modal title={`Saved ${words.many.toLowerCase()}`} onClose={() => setOpen(false)}>
          <SavedChoices kind={kind} onPick={pick} />
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

function SavedChoices({ kind, onPick, inSheet = false }: { kind: "person" | "place"; onPick: (x: SavedThing) => void; inSheet?: boolean }) {
  const { me } = useAuth();
  const inBusiness = !!me?.tenant_id && !me.personal;
  const mine = useLoad(() => get<SavedList>("/v1/me/saved"), []);
  const business = useLoad(() => (inBusiness ? get<SavedList>("/v1/saved") : Promise.resolve(none)), [inBusiness]);
  const ofKind = (l: SavedList | undefined) => (l?.saved ?? []).filter((x) => x.kind === kind);
  const groups = [
    { key: "mine", title: "Yours", list: ofKind(mine.data) },
    { key: "business", title: "The business's", list: ofKind(business.data) },
  ].filter((g) => g.list.length > 0);
  const done = (mine.data || mine.error) && (business.data || business.error);
  const words = KIND_WORDS[kind];
  return (
    <div className="saved-fill-list" data-testid="saved-fill-list">
      <ErrorBox error={mine.error} />
      <ErrorBox error={business.error} />
      {!done ? (
        <Skeleton rows={2} label={`Loading saved ${words.many.toLowerCase()}…`} />
      ) : groups.length === 0 ? (
        <EmptyState icon={savedIcon(kind)} action={<Link to="/settings/saved">Saved things</Link>}>
          Nothing saved of this kind yet.
        </EmptyState>
      ) : (
        groups.map((g) => (
          <section key={g.key}>
            {groups.length > 1 && <h3 className="saved-fill-group">{g.title}</h3>}
            {inSheet ? (
              <ul aria-label={g.title}>
                {g.list.map((x) => (
                  <li key={x.id}>
                    <button type="button" className="tk-row saved-fill-row" onClick={() => onPick(x)} data-testid="saved-fill-pick">
                      <span className="tk-row-tile" aria-hidden="true">
                        <Icon name={savedIcon(x.kind)} />
                      </span>
                      <span className="tk-row-words">
                        <span className="tk-row-name">{x.title}</span>
                        <span className="tk-row-sub">{savedSub(x)}</span>
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            ) : (
              <Items label={g.title}>
                {g.list.map((x) => (
                  <Row key={x.id} icon={savedIcon(x.kind)} title={x.title} sub={<span className="sub">{savedSub(x)}</span>} testid="saved-fill-row">
                    <button type="button" className="small" onClick={() => onPick(x)}>
                      Use<span className="sr-only"> {x.title}</span>
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
