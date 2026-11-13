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
  MAX_VEHICLE,
  moved,
  photoSrc,
  profileLetter,
  sameAreas,
  splitAreas,
  stepTo,
  type BusinessProfile,
  type GalleryPhoto,
  type ProfileWords,
} from "../lib/businessProfile";
import { CATEGORY_NAMES, MAX_CATEGORIES } from "../lib/explore";
import type { LinkSettings } from "../lib/links";
import { ErrorBox, Skeleton, useAction, useLoad } from "../ui";

// Your business in Explore (docs/spec/business-links.md, "Business profile and
// gallery"): the words, the logo and the photos other businesses see when they
// find you, with or without a storefront. A live card shows how it will look.

/** The card as another business sees it (design 11): the logo, the name
 * and line, Connect as they would see it, and the photos as a mosaic. */
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
  photos: { src: string; caption: string }[];
}) {
  const [first, second, third] = photos;
  const more = Math.max(0, photos.length - 2);
  return (
    <section
      className="tk-p-card tk-p-u2"
      aria-label="How your card looks"
      role="group"
    >
      <div className="tk-kicker">How other businesses see you</div>
      <div className="tk-p-who">
        {logo ? (
          <img className="tk-p-logo" src={logo} alt="" width={48} height={48} />
        ) : (
          <span className="tk-p-logo tk-p-letter" aria-hidden="true">
            {profileLetter(name)}
          </span>
        )}
        <div className="tk-p-words">
          <strong>{name || "Your business"}</strong>
          <span>
            {[tagline, area].filter(Boolean).join(" · ") ||
              "A line about what you do"}
          </span>
        </div>
        <span className="tk-p-connect" aria-hidden="true">
          Connect
        </span>
      </div>
      <div className="tk-p-mosaic" aria-hidden="true">
        <div className="tk-p-tile big">
          {first && <img src={first.src} alt="" loading="lazy" />}
          <span className="tk-x-cap">
            {first
              ? first.caption || "Your first photo"
              : "Add your first photo"}
          </span>
        </div>
        <div className="tk-p-stack">
          <div className="tk-p-tile">
            {second && <img src={second.src} alt="" loading="lazy" />}
          </div>
          <div className="tk-p-tile">
            {third && <img src={third.src} alt="" loading="lazy" />}
            {more > 0 && <span className="tk-p-more">+{more}</span>}
          </div>
        </div>
      </div>
    </section>
  );
}

/** What being found means, in a line, for the switch (design 11). */
function foundLine(s: LinkSettings): string {
  if (s.findable === null)
    return s.findable_now
      ? "On while your storefront is published. Businesses near you can find you and connect."
      : "Hidden until you publish your storefront or turn this on.";
  return s.findable
    ? "Businesses near you can find you and connect."
    : "Hidden. Only businesses you are linked with see this.";
}

export function BusinessProfilePage() {
  const { can, me } = useAuth();
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
    delivers: false,
    vehicle: "",
    areasLine: "",
    categories: [] as string[],
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
        delivers: data.delivers,
        vehicle: data.vehicle,
        areasLine: data.delivery_areas.join(", "),
        categories: data.categories ?? [],
      });
      setLoaded(true);
    }
  }, [data, loaded]);

  const gallery = data?.gallery ?? [];
  const max = data?.max_gallery ?? 12;
  const words: ProfileWords = {
    name: form.name,
    tagline: form.tagline,
    area: form.area,
    about: form.about,
    delivers: form.delivers,
    vehicle: form.delivers ? form.vehicle : "",
    delivery_areas: form.delivers ? splitAreas(form.areasLine) : [],
    categories: form.categories,
  };
  const dirty =
    !!data &&
    (words.name !== data.name ||
      words.tagline !== data.tagline ||
      words.area !== data.area ||
      words.about !== data.about ||
      words.delivers !== data.delivers ||
      words.vehicle !== data.vehicle ||
      !sameAreas(words.delivery_areas, data.delivery_areas) ||
      words.categories.join(",") !== (data.categories ?? []).join(","));

  const apply = (next: BusinessProfile) => setData(next);
  const save = () =>
    void act.run(
      async () =>
        apply(await put<BusinessProfile>("/v1/business-profile", words)),
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

  const on = settings.data?.findable_now ?? false;
  return (
    <div className="tk-p">
      <header className="tk-p-head">
        <Link
          to="/settings/links"
          className="tk-x-back"
          aria-label="Back to Linked businesses"
        >
          <Icon name="back" />
        </Link>
        <h1>Your business in Explore</h1>
      </header>
      <ErrorBox
        error={loadError ?? settings.error ?? act.error ?? capAct.error}
      />
      {!data ? (
        <Skeleton rows={3} label="Loading your profile…" />
      ) : (
        <>
          <section
            className={`tk-p-found tk-p-u1${on ? " on" : ""}`}
            aria-label="Being found"
            data-testid="profile-found"
          >
            <label className="tk-p-found-words" htmlFor="profile-findable">
              <span className="tk-p-found-title">Be found in Explore</span>
              <span className="tk-p-found-line">
                {settings.data ? foundLine(settings.data) : "Checking…"}
              </span>
            </label>
            <input
              id="profile-findable"
              type="checkbox"
              role="switch"
              className="tk-switch"
              checked={on}
              disabled={!manage || act.busy || !settings.data}
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
          </section>

          <ProfileCard
            // Empty, the name others see is the storefront's or the business's.
            name={form.name || me?.tenant_name || ""}
            tagline={form.tagline}
            area={form.area}
            logo={photoSrc(data.logo)}
            photos={gallery
              .map((g) => ({
                src: photoSrc(g.photo) ?? "",
                caption: g.caption,
              }))
              .filter((g) => g.src)}
          />

          <section
            className="tk-p-card tk-p-form tk-p-u3"
            aria-labelledby="profile-words"
            data-testid="profile-words"
          >
            <h2 id="profile-words" className="sr-only">
              Your details
            </h2>
            <div className="tk-p-logo-row">
              {manage ? (
                <button
                  type="button"
                  className="tk-p-logo-pick"
                  disabled={act.busy}
                  onClick={() => logoInput.current?.click()}
                  aria-label={data.logo ? "Change logo" : "Add a logo"}
                >
                  {data.logo ? (
                    <img
                      src={photoSrc(data.logo)}
                      alt=""
                      width={64}
                      height={64}
                    />
                  ) : (
                    <span aria-hidden="true">{profileLetter(form.name)}</span>
                  )}
                  <span className="tk-p-plus" aria-hidden="true">
                    <Icon name={data.logo ? "pencil" : "plus"} />
                  </span>
                </button>
              ) : data.logo ? (
                <img
                  className="tk-p-logo-pick"
                  src={photoSrc(data.logo)}
                  alt="Your logo"
                  width={64}
                  height={64}
                />
              ) : (
                <span className="tk-p-logo-pick" aria-hidden="true">
                  {profileLetter(form.name)}
                </span>
              )}
              <div>
                <div className="tk-p-logo-title">Logo</div>
                <div className="tk-p-logo-line">
                  {data.logo
                    ? "Shown on your card and in chats"
                    : "A letter shows until you add one"}
                </div>
                {manage && data.logo && (
                  <button
                    type="button"
                    className="tk-x-withdraw"
                    disabled={act.busy}
                    onClick={dropLogo}
                  >
                    Remove logo
                  </button>
                )}
              </div>
              {manage && (
                <input
                  ref={logoInput}
                  type="file"
                  accept="image/jpeg,image/png,image/webp"
                  className="sr-only"
                  aria-label="Choose a logo"
                  data-testid="profile-logo-input"
                  tabIndex={-1}
                  onChange={(e) => {
                    setLogo(e.target.files?.[0]);
                    e.target.value = "";
                  }}
                />
              )}
            </div>
            <label className="tk-p-field">
              Name
              <input
                value={form.name}
                maxLength={MAX_NAME}
                disabled={!manage}
                placeholder="Your storefront's name, or your business's"
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                data-testid="profile-name"
              />
            </label>
            <label className="tk-p-field">
              <span className="tk-p-field-top">
                One line about you
                <span>
                  {form.tagline.length} / {MAX_TAGLINE}
                </span>
              </span>
              <input
                value={form.tagline}
                maxLength={MAX_TAGLINE}
                disabled={!manage}
                placeholder="Cement and sand, delivered"
                onChange={(e) => setForm({ ...form, tagline: e.target.value })}
                data-testid="profile-tagline"
              />
            </label>
            <label className="tk-p-field">
              Area
              <input
                value={form.area}
                maxLength={MAX_AREA}
                disabled={!manage}
                placeholder="Where you are, such as Ikeja or Wuse 2"
                onChange={(e) => setForm({ ...form, area: e.target.value })}
                data-testid="profile-area"
              />
            </label>
            <label className="tk-p-field">
              <span className="tk-p-field-top">
                About
                <span>
                  {form.about.length} / {MAX_ABOUT}
                </span>
              </span>
              <textarea
                value={form.about}
                maxLength={MAX_ABOUT}
                disabled={!manage}
                rows={3}
                onChange={(e) => setForm({ ...form, about: e.target.value })}
                data-testid="profile-about"
              />
            </label>
            {/* What it sells: one or two categories, which Explore's chips
                find it by. A business already findable with none is asked. */}
            <fieldset className="tk-p-field tk-p-cats" data-testid="profile-categories">
              <legend>What you sell</legend>
              <span className={form.categories.length === 0 && settings.data?.findable_now ? "tk-p-cats-ask" : "hint"}>
                {form.categories.length === 0 && settings.data?.findable_now
                  ? "Pick one or two, so businesses looking for them find you."
                  : "One or two. Businesses find you by them in Explore."}
              </span>
              <div className="tk-p-cat-list">
                {Object.entries(CATEGORY_NAMES).map(([key, name]) => {
                  const on = form.categories.includes(key);
                  const full = !on && form.categories.length >= MAX_CATEGORIES;
                  return (
                    <button
                      key={key}
                      type="button"
                      className="tk-p-cat"
                      aria-pressed={on}
                      disabled={!manage || full}
                      onClick={() =>
                        setForm({
                          ...form,
                          categories: on ? form.categories.filter((k) => k !== key) : [...form.categories, key],
                        })
                      }
                    >
                      {name}
                    </button>
                  );
                })}
              </div>
            </fieldset>
            {/* A rider: a business whose service is delivery. Businesses
                find it under Delivery in Explore. */}
            <label className="tk-p-field tk-p-check">
              <input
                type="checkbox"
                checked={form.delivers}
                disabled={!manage}
                onChange={(e) =>
                  setForm({ ...form, delivers: e.target.checked })
                }
                data-testid="profile-delivers"
              />
              I deliver for other businesses
            </label>
            {form.delivers && (
              <>
                <label className="tk-p-field">
                  What you deliver with
                  <input
                    value={form.vehicle}
                    maxLength={MAX_VEHICLE}
                    disabled={!manage}
                    placeholder="Bike, van, on foot"
                    onChange={(e) =>
                      setForm({ ...form, vehicle: e.target.value })
                    }
                    data-testid="profile-vehicle"
                  />
                </label>
                <label className="tk-p-field">
                  Areas you deliver to
                  <input
                    value={form.areasLine}
                    disabled={!manage}
                    placeholder="Lekki, Ajah, Victoria Island"
                    onChange={(e) =>
                      setForm({ ...form, areasLine: e.target.value })
                    }
                    data-testid="profile-delivery-areas"
                  />
                </label>
              </>
            )}
            {manage && (
              <button
                type="button"
                className="tk-btn primary tk-p-save"
                disabled={act.busy || !dirty}
                onClick={save}
                data-testid="profile-save"
              >
                Save
              </button>
            )}
          </section>

          <section
            className="tk-p-card tk-p-u4"
            aria-labelledby="profile-photos"
            data-testid="profile-gallery"
          >
            <div className="tk-p-photos-top">
              <h2 id="profile-photos">Photos</h2>
              <span aria-live="polite" data-testid="profile-count">
                {gallery.length} of {max}
                <span className="sr-only"> photo{max === 1 ? "" : "s"}</span>
              </span>
            </div>
            <ul className="tk-p-grid" aria-label="Your photos">
              {gallery.map((g, i) => (
                <li
                  key={g.id}
                  className="tk-p-photo"
                  data-testid="profile-photo"
                >
                  <div className="tk-p-pic">
                    <img
                      src={photoSrc(g.photo)}
                      alt={g.caption || `Photo ${i + 1}`}
                      loading="lazy"
                    />
                    {i === 0 && (
                      <span className="tk-pill lime tk-p-first">First</span>
                    )}
                    {g.photo.moderation === "rejected" && (
                      <span className="tk-p-refused" role="status">
                        Not accepted for a public page. Remove it.
                      </span>
                    )}
                    {manage && (
                      <>
                        <button
                          type="button"
                          className="tk-p-x"
                          aria-label={`Remove photo ${i + 1}`}
                          disabled={act.busy}
                          onClick={() => remove(g)}
                        >
                          <Icon name="close" />
                        </button>
                        <span className="tk-p-move">
                          <button
                            type="button"
                            aria-label={`Move photo ${i + 1} earlier`}
                            disabled={
                              act.busy || stepTo(i, -1, gallery.length) < 0
                            }
                            onClick={() => reorder(g, -1)}
                          >
                            <Icon name="back" />
                          </button>
                          <button
                            type="button"
                            aria-label={`Move photo ${i + 1} later`}
                            disabled={
                              act.busy || stepTo(i, 1, gallery.length) < 0
                            }
                            onClick={() => reorder(g, 1)}
                          >
                            <Icon name="chevron" />
                          </button>
                        </span>
                      </>
                    )}
                  </div>
                  {manage ? (
                    <input
                      className="tk-p-caption"
                      defaultValue={g.caption}
                      key={g.id + g.caption}
                      maxLength={MAX_CAPTION}
                      placeholder="Add a caption"
                      aria-label={`Caption for photo ${i + 1}`}
                      onBlur={(e) => caption(g, e.target.value.trim())}
                    />
                  ) : (
                    g.caption && (
                      <span className="tk-p-caption">{g.caption}</span>
                    )
                  )}
                </li>
              ))}
              {uploading > 0 && (
                <li className="tk-p-photo" role="status">
                  <div className="tk-p-pic tk-p-wait">
                    Adding {uploading} photo{uploading === 1 ? "" : "s"}…
                  </div>
                </li>
              )}
              {manage && (
                <li className="tk-p-photo">
                  <button
                    type="button"
                    className="tk-p-add"
                    disabled={
                      act.busy || uploading > 0 || gallery.length >= max
                    }
                    onClick={() => photoInput.current?.click()}
                  >
                    <Icon name="plus" />
                    Add a photo
                  </button>
                </li>
              )}
            </ul>
            {gallery.length === 0 && uploading === 0 && (
              <p className="tk-p-note">
                Show your shop, your products and your team. Other businesses
                see a letter until you add one.
              </p>
            )}
            {manage && gallery.length >= max && (
              <p className="tk-p-note">That is the most photos.</p>
            )}
            {manage && (
              <input
                ref={photoInput}
                type="file"
                multiple
                accept="image/jpeg,image/png,image/webp"
                className="sr-only"
                aria-label="Choose photos"
                tabIndex={-1}
                data-testid="profile-photo-input"
                onChange={(e) => void addPhotos(e.target.files)}
              />
            )}
          </section>
        </>
      )}
    </div>
  );
}
