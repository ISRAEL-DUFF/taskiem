import { thumb, type Image } from "../lib/storefront";

/** The photos an item has after its first, each with a Remove. `ids` are
 * every photo's media id in order (the first is the item's photo). */
export function PhotoStrip({
  name,
  gallery,
  ids,
  busy,
  onRemove,
}: {
  name: string;
  gallery?: Image[];
  ids?: string[];
  busy: boolean;
  onRemove: (id: string) => void;
}) {
  if (!gallery || gallery.length === 0 || !ids) return null;
  return (
    <span className="sf-gallery">
      {gallery.map((g, i) => {
        const id = ids[i + 1];
        const src = thumb(g);
        if (!id) return null;
        return (
          <span key={id} className="sf-gallery-pic">
            {src && <img src={src} alt="" width={40} height={40} />}
            <button
              type="button"
              className="link"
              disabled={busy}
              aria-label={`Remove photo ${i + 2} of ${name}`}
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
