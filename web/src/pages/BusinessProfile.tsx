import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { del, get, patch, put, uploadFile } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import {
  MAX_ABOUT,
  MAX_AREA,
  MAX_CAPTION,
  MAX_NAME,
  MAX_TAGLINE,
  moved,
  photoCount,
  photoSrc,
  profileLetter,
  stepTo,
  type BusinessProfile,
  type GalleryPhoto,
} from "../lib/businessProfile";
import { findableLine, type LinkSettings } from "../lib/links";
import {
  ErrorBox,
  Field,
  PageHeader,
  Skeleton,
  useAction,
  useLoad,
} from "../ui";

// Your business in Explore (docs/spec/business-links.md, "Business profile and
// gallery"): the words, the logo and the photos other businesses see when they
// find you, with or without a storefront. A live card shows how it will look.

/** The card as another business sees it. */
function ProfileCard({
  name,
  tagline,
  area,
  logo,
  photos,
}: {
  name: string;
  tagline: string;
  area: string;
  logo?: string;
  photos: string[];
}) {
  return (
    <div
      className="explore-card profile-preview"
      aria-label="How your card looks"
      role="group"
    >
      {photos.length > 0 && (
        <div className="explore-strip" aria-hidden="true">
          {photos.slice(0, 3).map((src) => (
            <img key={src} src={src} alt="" loading="lazy" />
          ))}
        </div>
      )}
      <div className="explore-head">
        {logo ? (
          <img
            className="explore-logo"
            src={logo}
            alt=""
            width={48}
            height={48}
          />
        ) : (
          <span className="chat-avatar" aria-hidden="true">
            {profileLetter(name)}
          </span>
        )}
        <div className="explore-who">
          <strong>{name || "Your business"}</strong>
          <span className="hint">
            {[tagline, area].filter(Boolean).join(" · ") ||
              "A line about what you do"}
          </span>
        </div>
      </div>
    </div>
  );
}

export function BusinessProfilePage() {
  const { can } = useAuth();
  const manage = can("link.manage");
  const [data, setData] = useState<BusinessProfile>();
  const [loadError, setLoadError] = useState<unknown>();
  const settings = useLoad(() => get<LinkSettings>("/v1/link-settings"), []);
  const act = useAction();
  const capAct = useAction();
  const [form, setForm] = useState({
    name: "",
    tagline: "",
    area: "",
    about: "",
  });
  const [loaded, setLoaded] = useState(false);
  const [uploading, setUploading] = useState(0);
  const logoInput = useRef<HTMLInputElement>(null);
  const photoInput = useRef<HTMLInputElement>(null);
  useEffect(() => {
    let live = true;
    get<BusinessProfile>("/v1/business-profile").then(
      (d) => live && setData(d),
      (e: unknown) => live && setLoadError(e),
    );
    return () => {
      live = false;
    };
  }, []);

  useEffect(() => {
    if (data && !loaded) {
      setForm({
        name: data.name,
        tagline: data.tagline,
        area: data.area,
        about: data.about,
      });
      setLoaded(true);
    }
  }, [data, loaded]);

  const gallery = data?.gallery ?? [];
  const max = data?.max_gallery ?? 12;
  const dirty =
    !!data &&
    (form.name !== data.name ||
      form.tagline !== data.tagline ||
      form.area !== data.area ||
      form.about !== data.about);

  const apply = (next: BusinessProfile) => setData(next);
  const save = () =>
    void act.run(
      async () =>
        apply(await put<BusinessProfile>("/v1/business-profile", form)),
      "Saved",
    );
  const setLogo = (file: File | undefined) => {
    if (!file) return;
    void act.run(
      async () =>
        apply(
          await uploadFile<BusinessProfile>("/v1/business-profile/logo", file),
        ),
      "Logo saved",
    );
  };
  const dropLogo = () =>
    void act.run(
      async () =>
        apply(await del<BusinessProfile>("/v1/business-profile/logo")),
      "Logo removed",
    );
  const addPhotos = async (files: FileList | null) => {
    if (!files || files.length === 0) return;
    const list = Array.from(files).slice(0, Math.max(0, max - gallery.length));
    setUploading(list.length);
    for (const f of list) {
      const ok = await act.run(
        async () =>
          apply(
            await uploadFile<BusinessProfile>(
              "/v1/business-profile/gallery",
              f,
            ),
          ),
        false,
      );
      setUploading((n) => n - 1);
      if (!ok) break;
    }
    setUploading(0);
    if (photoInput.current) photoInput.current.value = "";
  };
  const reorder = (g: GalleryPhoto, by: -1 | 1) => {
    const ids = moved(
      gallery.map((x) => x.id),
      gallery.findIndex((x) => x.id === g.id),
      by,
    );
    void act.run(
      async () =>
        apply(
          await put<BusinessProfile>("/v1/business-profile/gallery/order", {
            ids,
          }),
        ),
      false,
    );
  };
  const remove = (g: GalleryPhoto) =>
    void act.run(
      async () =>
        apply(
          await del<BusinessProfile>(`/v1/business-profile/gallery/${g.id}`),
        ),
      "Photo removed",
    );
  const caption = (g: GalleryPhoto, text: string) => {
    if (text === g.caption) return;
    // Its own action, so saving a caption never switches the other buttons
    // off under a click that is on its way, and only this photo's words change
    // here, so a late answer cannot bring back a photo that was removed.
    void capAct.run(async () => {
      await patch<BusinessProfile>(`/v1/business-profile/gallery/${g.id}`, {
        caption: text,
      });
      setData(
        (d) =>
          d && {
            ...d,
            gallery: d.gallery.map((x) =>
              x.id === g.id ? { ...x, caption: text } : x,
            ),
          },
      );
    }, false);
  };

  return (
    <>
      <p className="crumbs">
        <Link to="/settings/links" aria-label="Back to Linked businesses">
          Linked businesses
        </Link>
      </p>
      <PageHeader
        title="Your business in Explore"
        description="What other businesses see when they look for a business to trade with: your name, a line about what you do, your area, a logo and photos."
      />
      <ErrorBox
        error={loadError ?? settings.error ?? act.error ?? capAct.error}
      />
      {!data ? (
        <Skeleton rows={3} label="Loading your profile…" />
      ) : (
        <>
          <section
            className="card"
            aria-label="Being found"
            data-testid="profile-found"
          >
            {settings.data ? (
              <label className="switch-line">
                <input
                  type="checkbox"
                  aria-label="Let other businesses find us"
                  checked={settings.data.findable_now}
                  disabled={!manage || act.busy}
                  onChange={(e) =>
                    void act.run(
                      async () => (
                        await put("/v1/link-settings", {
                          findable: e.target.checked,
                        }),
                        settings.reload()
                      ),
                      "Saved",
                    )
                  }
                  data-testid="profile-findable"
                />
                <span>{findableLine(settings.data)}</span>
              </label>
            ) : (
              <p className="hint">Checking…</p>
            )}
            <p className="hint">
              A business is listed only while this is on. What is on this page
              is shown to every business that finds you.
            </p>
            <ProfileCard
              name={form.name}
              tagline={form.tagline}
              area={form.area}
              logo={photoSrc(data.logo)}
              photos={gallery
                .map((g) => photoSrc(g.photo) ?? "")
                .filter(Boolean)}
            />
          </section>

          <section
            className="card"
            aria-labelledby="profile-words"
            data-testid="profile-words"
          >
            <h2 id="profile-words">About your business</h2>
            <div className="profile-logo">
              {data.logo ? (
                <img
                  className="explore-logo"
                  src={photoSrc(data.logo)}
                  alt="Your logo"
                  width={64}
                  height={64}
                />
              ) : (
                <span className="chat-avatar big" aria-hidden="true">
                  {profileLetter(form.name)}
                </span>
              )}
              {manage && (
                <span className="row-actions">
                  <input
                    ref={logoInput}
                    type="file"
                    accept="image/jpeg,image/png,image/webp"
                    className="sr-only"
                    aria-label="Choose a logo"
                    data-testid="profile-logo-input"
                    onChange={(e) => {
                      setLogo(e.target.files?.[0]);
                      e.target.value = "";
                    }}
                  />
                  <button
                    type="button"
                    disabled={act.busy}
                    onClick={() => logoInput.current?.click()}
                  >
                    {data.logo ? "Change logo" : "Add a logo"}
                  </button>
                  {data.logo && (
                    <button
                      type="button"
                      disabled={act.busy}
                      onClick={dropLogo}
                    >
                      Remove logo
                    </button>
                  )}
                </span>
              )}
            </div>
            <Field
              label="Business name"
              hint="Leave it empty to use the name on your storefront, or your business's name."
            >
              <input
                value={form.name}
                maxLength={MAX_NAME}
                disabled={!manage}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                data-testid="profile-name"
              />
            </Field>
            <Field
              label="What you do, in one line"
              hint={`${form.tagline.length} of ${MAX_TAGLINE}`}
            >
              <input
                value={form.tagline}
                maxLength={MAX_TAGLINE}
                disabled={!manage}
                placeholder="Cement and sand, delivered"
                onChange={(e) => setForm({ ...form, tagline: e.target.value })}
                data-testid="profile-tagline"
              />
            </Field>
            <Field
              label="Area"
              hint="Where you are, as people say it, such as Ikeja or Wuse 2."
            >
              <input
                value={form.area}
                maxLength={MAX_AREA}
                disabled={!manage}
                onChange={(e) => setForm({ ...form, area: e.target.value })}
                data-testid="profile-area"
              />
            </Field>
            <Field
              label="About us"
              hint={`${form.about.length} of ${MAX_ABOUT}`}
            >
              <textarea
                value={form.about}
                maxLength={MAX_ABOUT}
                disabled={!manage}
                rows={4}
                onChange={(e) => setForm({ ...form, about: e.target.value })}
                data-testid="profile-about"
              />
            </Field>
            {manage && (
              <button
                type="button"
                className="primary"
                disabled={act.busy || !dirty}
                onClick={save}
                data-testid="profile-save"
              >
                Save
              </button>
            )}
          </section>

          <section
            className="card"
            aria-labelledby="profile-photos"
            data-testid="profile-gallery"
          >
            <h2 id="profile-photos">Photos</h2>
            <p className="hint" aria-live="polite" data-testid="profile-count">
              {photoCount(gallery.length, max)}. Show your shop, your products
              and your team.
            </p>
            {gallery.length === 0 && (
              <p className="hint">
                No photos yet. Other businesses will see a letter instead.
              </p>
            )}
            <ul className="gallery-grid" aria-label="Your photos">
              {gallery.map((g, i) => (
                <li
                  key={g.id}
                  className="gallery-tile"
                  data-testid="profile-photo"
                >
                  {g.photo.moderation === "rejected" && (
                    <span className="hint gallery-rejected" role="status">
                      Not accepted for a public page. Remove it.
                    </span>
                  )}
                  <img
                    src={photoSrc(g.photo)}
                    alt={g.caption || `Photo ${i + 1}`}
                    loading="lazy"
                  />
                  {manage && (
                    <>
                      <input
                        className="gallery-caption"
                        defaultValue={g.caption}
                        key={g.id + g.caption}
                        maxLength={MAX_CAPTION}
                        placeholder="Add a caption"
                        aria-label={`Caption for photo ${i + 1}`}
                        onBlur={(e) => caption(g, e.target.value.trim())}
                      />
                      <span className="gallery-tools">
                        <button
                          type="button"
                          className="icon-button"
                          aria-label={`Move photo ${i + 1} earlier`}
                          title="Earlier"
                          disabled={
                            act.busy || stepTo(i, -1, gallery.length) < 0
                          }
                          onClick={() => reorder(g, -1)}
                        >
                          <Icon name="back" />
                        </button>
                        <button
                          type="button"
                          className="icon-button"
                          aria-label={`Move photo ${i + 1} later`}
                          title="Later"
                          disabled={
                            act.busy || stepTo(i, 1, gallery.length) < 0
                          }
                          onClick={() => reorder(g, 1)}
                        >
                          <Icon name="chevron" />
                        </button>
                        <button
                          type="button"
                          className="icon-button danger"
                          aria-label={`Remove photo ${i + 1}`}
                          title="Remove"
                          disabled={act.busy}
                          onClick={() => remove(g)}
                        >
                          <Icon name="trash" />
                        </button>
                      </span>
                    </>
                  )}
                </li>
              ))}
              {uploading > 0 && (
                <li className="gallery-tile gallery-wait" role="status">
                  Adding {uploading} photo{uploading === 1 ? "" : "s"}…
                </li>
              )}
            </ul>
            {manage && (
              <>
                <input
                  ref={photoInput}
                  type="file"
                  multiple
                  accept="image/jpeg,image/png,image/webp"
                  className="sr-only"
                  aria-label="Choose photos"
                  data-testid="profile-photo-input"
                  onChange={(e) => void addPhotos(e.target.files)}
                />
                <button
                  type="button"
                  disabled={act.busy || uploading > 0 || gallery.length >= max}
                  onClick={() => photoInput.current?.click()}
                >
                  <Icon name="plus" /> Add photos
                </button>
                {gallery.length >= max && (
                  <span className="hint"> That is the most photos.</span>
                )}
              </>
            )}
          </section>
        </>
      )}
    </>
  );
}
