// What a business sells (docs/spec/mobile-app-explore-and-offline.md §2.1):
// one or two of a fixed list, which Explore's chips find it by. Chosen in
// Your business in Explore, and asked of a business as it makes itself
// findable (E-S6) when it has none.

import { useEffect, useState } from "react";
import { get, put } from "../api";
import type { BusinessProfile } from "../lib/businessProfile";
import { CATEGORY_NAMES, MAX_CATEGORIES } from "../lib/explore";
import { ErrorBox, Modal, useAction } from "../ui";

/** The categories as buttons that stay pressed; two at most. */
export function CategoryPicker({
  chosen,
  onChange,
  disabled = false,
}: {
  chosen: string[];
  onChange: (next: string[]) => void;
  disabled?: boolean;
}) {
  return (
    <div className="tk-p-cat-list">
      {Object.entries(CATEGORY_NAMES).map(([key, name]) => {
        const on = chosen.includes(key);
        const full = !on && chosen.length >= MAX_CATEGORIES;
        return (
          <button
            key={key}
            type="button"
            className="tk-p-cat"
            aria-pressed={on}
            disabled={disabled || full}
            onClick={() => onChange(on ? chosen.filter((k) => k !== key) : [...chosen, key])}
          >
            {name}
          </button>
        );
      })}
    </div>
  );
}

/** Asked once a business is findable with no category; nothing when it has
 * some, or its profile cannot be read. */
export function AskCategories({ onClose }: { onClose: () => void }) {
  const [profile, setProfile] = useState<BusinessProfile>();
  const [cats, setCats] = useState<string[]>([]);
  const act = useAction();
  useEffect(() => {
    let live = true;
    get<BusinessProfile>("/v1/business-profile").then(
      (p) => {
        if (!live) return;
        if ((p.categories ?? []).length > 0) onClose();
        else setProfile(p);
      },
      () => live && onClose(),
    );
    return () => {
      live = false;
    };
  }, [onClose]);
  if (!profile) return null;
  const save = () =>
    void act.run(async () => {
      // The words as they are, with the categories.
      await put("/v1/business-profile", {
        name: profile.name,
        tagline: profile.tagline,
        area: profile.area,
        about: profile.about,
        delivers: profile.delivers,
        vehicle: profile.vehicle,
        delivery_areas: profile.delivery_areas,
        categories: cats,
      });
      onClose();
    }, "Saved");
  return (
    <Modal title="What do you sell?" onClose={onClose}>
      <p className="hint">Pick one or two, so businesses looking for them find you in Explore.</p>
      <CategoryPicker chosen={cats} onChange={setCats} disabled={act.busy} />
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button type="button" onClick={onClose}>
          Not now
        </button>
        <button type="button" className="primary" disabled={act.busy || cats.length === 0} onClick={save} data-testid="ask-categories-save">
          Save
        </button>
      </div>
    </Modal>
  );
}
