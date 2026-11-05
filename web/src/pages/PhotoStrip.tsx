import { thumb, type Image } from "../lib/storefront";

/** The photos an item has after its first, each named, with Make main and
 * Remove, and how many of the most it has. `ids` are every photo's media
 * id in order (the first is the item's main photo). */
export function PhotoStrip({
  name,
  gallery,
  ids,
  busy,
  onRemove,
  onMain,
}: {
  name: string;
  gallery?: Image[];
  ids?: string[];
  busy: boolean;
  onRemove: (id: string) => void;
  onMain: (id: string) => void;
}) {
  if (!gallery || gallery.length === 0 || !ids) return null;
  return (
    <span className="sf-gallery">
      <span className="hint sf-gallery-count">
        {ids.length} of {MAX_GALLERY + 1} photos
      </span>
      {gallery.map((g, i) => {
        const id = ids[i + 1];
        const src = thumb(g);
        const which = `photo ${i + 2} of ${name}`;
        if (!id) return null;
        return (
          <span key={id} className="sf-gallery-pic">
            {src && (
              <img
                src={src}
                alt={which.charAt(0).toUpperCase() + which.slice(1)}
                width={40}
                height={40}
              />
            )}
            <button
              type="button"
              className="link"
              disabled={busy}
              aria-label={`Make ${which} the main photo`}
              onClick={() => onMain(id)}
            >
              Make main
            </button>
            <button
              type="button"
              className="link"
              disabled={busy}
              aria-label={`Remove ${which}`}
              onClick={() => onRemove(id)}
            >
              Remove
            </button>
          </span>
        );
      })}
    </span>
  );
}

/** The most photos an item has after its first (engine/storefront MaxGallery). */
export const MAX_GALLERY = 5;
