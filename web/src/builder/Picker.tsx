// "What happens next?": the panel a + opens. Search, three suggestions,
// then everything Taskiem can do by group; picking one adds the step where
// the + was and opens its window.
import { useEffect, useMemo, useRef, useState } from "react";
import { Icon } from "../icons";
import { GROUPS, search, suggested, type Pick } from "./catalog";

export function Picker({ items, where, onPick, onClose, onDescribe }: { items: Pick[]; where: string; onPick: (p: Pick) => void; onClose: () => void; onDescribe?: () => void }) {
  const [q, setQ] = useState("");
  const [group, setGroup] = useState<string | null>(null);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => input.current?.focus(), []);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && (group ? setGroup(null) : onClose());
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [group, onClose]);
  const found = useMemo(() => search(items, q), [items, q]);
  const first = useMemo(() => suggested(items), [items]);
  const groups = GROUPS.filter((g) => items.some((p) => p.group === g.name));
  const row = (p: Pick) => (
    <li key={p.key}>
      <button type="button" className="pick-row" onClick={() => onPick(p)}>
        <span className={`pick-icon tone-${p.tone}`} aria-hidden="true">
          <Icon name={p.icon} />
        </span>
        <span className="pick-text">
          <span className="pick-title">{p.title}</span>
          <span className="pick-sub">{p.sub}</span>
        </span>
      </button>
    </li>
  );
  return (
    <div className="picker" aria-label="What happens next?" role="dialog">
      <div className="picker-head">
        <div className="picker-title">
          <h2>What happens next?</h2>
          <span>{where}</span>
        </div>
        <button type="button" className="icon-round" aria-label="Close" onClick={onClose}>
          <Icon name="close" />
        </button>
      </div>
      <label className="picker-search">
        <Icon name="search" />
        <span className="sr-only">Search</span>
        <input
          ref={input}
          type="search"
          value={q}
          placeholder="Search, e.g. send a WhatsApp, payment link, add a row"
          onChange={(e) => {
            setQ(e.target.value);
            setGroup(null);
          }}
        />
      </label>
      <div className="picker-body">
        {q.trim() ? (
          found.length ? (
            <ul className="pick-list" aria-label="Found">
              {found.slice(0, 40).map(row)}
            </ul>
          ) : (
            <p className="hint picker-empty">Nothing matches. Try other words, or describe it below.</p>
          )
        ) : group ? (
          <>
            <button type="button" className="link picker-back" onClick={() => setGroup(null)}>
              <Icon name="back" /> All groups
            </button>
            <h3 className="picker-group">{group}</h3>
            <ul className="pick-list" aria-label={group}>
              {items.filter((p) => p.group === group).map(row)}
            </ul>
          </>
        ) : (
          <>
            {first.length > 0 && (
              <>
                <h3 className="picker-group">Suggested for this workflow</h3>
                <ul className="pick-list" aria-label="Suggested">
                  {first.map(row)}
                </ul>
              </>
            )}
            <h3 className="picker-group">Everything Taskiem can do</h3>
            <ul className="pick-list" aria-label="Groups">
              {groups.map((g) => (
                <li key={g.name}>
                  <button type="button" className="pick-row" onClick={() => setGroup(g.name)}>
                    <span className={`pick-icon tone-${g.tone}`} aria-hidden="true">
                      <Icon name={g.icon} />
                    </span>
                    <span className="pick-text">
                      <span className="pick-title">{g.name}</span>
                      <span className="pick-sub">{g.what}</span>
                    </span>
                    <Icon name="chevron" className="pick-go" />
                  </button>
                </li>
              ))}
            </ul>
          </>
        )}
      </div>
      {onDescribe && (
        <div className="picker-foot">
          <Icon name="sparkle" />
          <span>
            Not sure?{" "}
            <button type="button" className="link" onClick={onDescribe}>
              Describe it
            </button>{" "}
            and the AI changes the workflow.
          </span>
        </div>
      )}
    </div>
  );
}
