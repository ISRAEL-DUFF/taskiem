import { useEffect, useState } from "react";
import { del, get, post, put } from "../api";
import { useAuth } from "../auth";
import { pickName, sendPhoto, type PhotoPick } from "../lib/photos";
import { thumb, type Catalogue, type Image } from "../lib/storefront";
import { tileLetters, toneOf } from "../lib/tone";
import { ErrorBox, errorText, useLoad } from "../ui";
import { PhotoAdd } from "./PhotoAdd";
import { MAX_GALLERY } from "./PhotoStrip";

// A product's photos in the chat (docs/spec/chat-categories-and-variants.md,
// Part C): the ones it has, each with Make main and Remove, and the three
// ways to add one. Changing photos needs storefront.manage, as on the
// storefront page.

/** Photos sent one after another: those that did not go, each with why. */
export async function sendAll(
  row: string,
  picks: PhotoPick[],
  have: number,
  onSent?: () => void,
): Promise<{ pick: PhotoPick; why: string }[]> {
  const failed: { pick: PhotoPick; why: string }[] = [];
  let n = have;
  for (const pick of picks) {
    try {
      await sendPhoto(row, pick, n > 0);
      n++;
      onSent?.();
    } catch (e) {
      failed.push({ pick, why: errorText(e) });
    }
  }
  return failed;
}

/** What did not go, and Try again. */
export function PhotoFailures({
  failed,
  onRetry,
  busy,
  hideRetry,
}: {
  failed: { pick: PhotoPick; why: string }[];
  onRetry: () => void;
  busy?: boolean;
  /** The form's own button sends them again. */
  hideRetry?: boolean;
}) {
  if (failed.length === 0) return null;
  return (
    <div className="error tk-photo-failed" role="alert">
      <p>
        {failed.length === 1
          ? "A photo did not go:"
          : `${failed.length} photos did not go:`}
      </p>
      <ul>
        {failed.map((f, i) => (
          <li key={i}>
            {pickName(f.pick)}: {f.why}
          </li>
        ))}
      </ul>
      {!hideRetry && (
        <button
          type="button"
          className="tk-btn soft"
          disabled={busy}
          onClick={onRetry}
        >
          Try again
        </button>
      )}
    </div>
  );
}

export function ProductPhotos({
  row,
  name,
  canEdit,
}: {
  /** The product's catalogue row. */
  row: string;
  name: string;
  canEdit: boolean;
}) {
  const { can } = useAuth();
  const may = canEdit && can("storefront.manage");
  const cat = useLoad(
    () => get<Catalogue>("/v1/storefront/catalogue").catch(() => null),
    [],
  );
  const it = cat.data?.items.find((x) => x.id === row);
  const shots = [it?.photo, ...(it?.gallery ?? [])].filter(
    (p): p is Image => !!p,
  );
  const ids = it?.photo_ids ?? [];
  const [sending, setSending] = useState<{ done: number; of: number }>();
  const [failed, setFailed] = useState<{ pick: PhotoPick; why: string }[]>([]);
  const [error, setError] = useState<unknown>();
  // What the page shows changes with the photos: read again.
  const refresh = async () => {
    await post("/v1/storefront/catalogue/refresh").catch(() => undefined);
    cat.reload();
  };
  const send = async (picks: PhotoPick[]) => {
    setFailed([]);
    setError(undefined);
    setSending({ done: 0, of: picks.length });
    const bad = await sendAll(row, picks, ids.length, () =>
      setSending((s) => s && { ...s, done: s.done + 1 }),
    );
    setSending(undefined);
    setFailed(bad);
    await refresh();
  };
  const act = async (fn: () => Promise<unknown>) => {
    setError(undefined);
    try {
      await fn();
      await refresh();
    } catch (e) {
      setError(e);
    }
  };
  const path = `/v1/storefront/catalogue/rows/${encodeURIComponent(row)}/photo`;
  // A photo removed or made main while one is sending would race it.
  const quiet = !!sending;
  return (
    <div className="tk-product-photos">
      <ul className="tk-photos" aria-label={`Photos of ${name}`}>
        {shots.length === 0 && (
          <li>
            <span
              className="tk-photo-none"
              aria-hidden="true"
              data-tone={toneOf(name)}
            >
              {tileLetters(name)}
            </span>
            <span className="sr-only">No photo yet</span>
          </li>
        )}
        {shots.map((p, i) => {
          const id = ids[i];
          const which = i === 0 ? "the main photo" : `photo ${i + 1}`;
          return (
            <li key={id ?? i} className="tk-photo">
              <img
                src={thumb(p)}
                alt={
                  i === 0
                    ? `${name}, its main photo`
                    : `${name}, photo ${i + 1}`
                }
              />
              {may && id && (
                <span className="tk-photo-acts">
                  {i > 0 && (
                    <button
                      type="button"
                      className="link"
                      disabled={quiet}
                      onClick={() =>
                        void act(() =>
                          put(`${path}/main?media=${encodeURIComponent(id)}`),
                        )
                      }
                    >
                      Make main
                      <span className="sr-only">, photo {i + 1}</span>
                    </button>
                  )}
                  <button
                    type="button"
                    className="link"
                    disabled={quiet}
                    onClick={() =>
                      void act(() =>
                        del(`${path}?media=${encodeURIComponent(id)}`),
                      )
                    }
                  >
                    Remove
                    <span className="sr-only">, {which}</span>
                  </button>
                </span>
              )}
            </li>
          );
        })}
      </ul>
      {may && cat.data !== undefined && (
        <PhotoAdd
          room={MAX_GALLERY + 1 - ids.length}
          busy={!!sending}
          onPick={(picks) => void send(picks)}
        />
      )}
      {sending && (
        <p className="hint" role="status">
          Sending{" "}
          {sending.of === 1
            ? "the photo"
            : `photo ${Math.min(sending.done + 1, sending.of)} of ${sending.of}`}
          …
        </p>
      )}
      <PhotoFailures
        failed={failed}
        busy={!!sending}
        onRetry={() => void send(failed.map((f) => f.pick))}
      />
      <ErrorBox error={error} />
    </div>
  );
}

/** Photos chosen for something not yet made (a new product, a new row):
 * shown, each with Remove, and sent once it exists. A link cannot be shown
 * before Taskiem fetches it, so it is named by its site. */
export function PendingPhotos({
  picks,
  onChange,
  busy,
}: {
  picks: PhotoPick[];
  onChange: (p: PhotoPick[]) => void;
  busy?: boolean;
}) {
  const [urls, setUrls] = useState<Map<File, string>>(new Map());
  useEffect(() => {
    const m = new Map<File, string>();
    for (const p of picks)
      if ("file" in p) m.set(p.file, URL.createObjectURL(p.file));
    setUrls(m);
    return () => m.forEach((u) => URL.revokeObjectURL(u));
  }, [picks]);
  return (
    <div className="tk-product-photos">
      {picks.length > 0 && (
        <ul className="tk-photos" aria-label="Photos to add">
          {picks.map((p, i) => (
            <li key={i} className="tk-photo">
              {"file" in p ? (
                <img src={urls.get(p.file)} alt={`Photo ${i + 1} to add`} />
              ) : (
                <span className="tk-photo-linked">{pickName(p)}</span>
              )}
              <span className="tk-photo-acts">
                <button
                  type="button"
                  className="link"
                  disabled={busy}
                  aria-label={`Remove photo ${i + 1}`}
                  onClick={() => onChange(picks.filter((_, j) => j !== i))}
                >
                  Remove
                </button>
              </span>
            </li>
          ))}
        </ul>
      )}
      <PhotoAdd
        room={MAX_GALLERY + 1 - picks.length}
        busy={busy}
        onPick={(more) => onChange([...picks, ...more])}
      />
    </div>
  );
}
