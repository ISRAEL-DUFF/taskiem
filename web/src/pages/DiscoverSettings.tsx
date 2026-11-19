import { useState } from "react";
import { Link } from "react-router-dom";
import { get, put } from "../api";
import { Icon } from "../icons";
import {
  chatOffWords,
  hiddenWords,
  listedWords,
  mapLink,
  missingWords,
  photoAt,
  roundPlace,
  standing,
  placeProblem,
  previewResult,
  type DiscoverCategory,
  type DiscoverLocation,
  type DiscoverSettings as Settings,
} from "../lib/discover";
import { CATEGORY_NAMES } from "../lib/explore";
import { hoursLines, type ChatLinkCard } from "../lib/personchat";
import { hoursErrors, hoursFromForm, hoursToForm, type HoursForm } from "../lib/storefront";
import { ErrorBox, Field, Items, PageHeader, Skeleton, useAction, useLoad } from "../ui";
import { ChatAvatar } from "./ChatAvatar";
import { DiscoverRow } from "./Discover";
import { HoursEditor } from "./Storefront";

// Settings > Discover (docs/spec/taskiem-for-everyone.md §10): whether the
// business is found by the public in the Taskiem app (on by default with a
// published shop page), what its listing lacks, why it is hidden or has
// no Chat button, its own category, area, hours and place on the map, and
// the listing as customers see it. Apart from Explore, where businesses
// find businesses (Your business in Explore).

/** The page: /settings/discover. */
export function DiscoverSettings() {
  const view = useLoad(() => get<Settings>("/v1/settings/discover"), []);
  const [st, setSt] = useState<Settings>();
  const act = useAction();
  const s = st ?? view.data;
  // The switch shows what was chosen at once; the saved settings once they
  // are back, or what was there before when it was refused.
  const save = async (body: Record<string, unknown>, said: string) => {
    const was = s;
    if (s && typeof body.listed === "boolean") setSt({ ...s, listed: body.listed });
    const ok = await act.run(async () => {
      setSt(await put<Settings>("/v1/settings/discover", body));
    }, said);
    if (!ok) setSt(was);
    return ok;
  };
  return (
    <>
      <p className="crumbs">
        <Link to="/settings" aria-label="Back to Settings">
          Settings
        </Link>
      </p>
      <PageHeader title="Discover" description="Customers using the Taskiem app find businesses in Discover, then chat, order and book. This is apart from Explore, where businesses find each other." />
      {!s && !view.error && <Skeleton rows={4} label="Loading your listing…" />}
      <ErrorBox error={view.error ?? act.error} />
      {s && (
        <>
          <Standing s={s} busy={act.busy} save={save} />
          <Listing s={s} busy={act.busy} save={save} />
          <OwnHours s={s} busy={act.busy} save={save} />
          <Place s={s} busy={act.busy} save={save} />
          <Preview s={s} />
        </>
      )}
    </>
  );
}

type Save = (body: Record<string, unknown>, said: string) => Promise<boolean>;

/** The switch, where the listing stands, and why. */
function Standing({ s, busy, save }: { s: Settings; busy: boolean; save: Save }) {
  const now = standing(s);
  const missing = s.listed ? missingWords(s) : "";
  return (
    <section className="card" aria-labelledby="discover-switch" data-testid="discover-standing">
      <h2 id="discover-switch">Your listing</h2>
      <p className="discover-state">
        <span className={`badge sf-status ${s.listed && s.shown && !s.hidden ? "ok" : s.listed ? "warn" : "muted"}`} data-testid="discover-tag">
          {now.tag}
        </span>{" "}
        {now.line}
      </p>
      <label className="switch-line">
        <input
          type="checkbox"
          role="switch"
          checked={s.listed}
          disabled={busy || !!s.workspace}
          onChange={(e) => void save({ listed: e.target.checked }, e.target.checked ? "You're listed in Discover" : "Taken out of Discover")}
          data-testid="discover-listed"
        />{" "}
        <span>Found by the public in the Taskiem app</span>
      </label>
      <p className="hint" data-testid="discover-default">
        {listedWords(s)}
      </p>
      {s.chosen !== null && !s.workspace && (
        <button type="button" className="small" disabled={busy} onClick={() => void save({ default: true }, "Back to the default")} data-testid="discover-default-back">
          Go back to the default
        </button>
      )}
      {missing && (
        <div className="warning" role="note" data-testid="discover-missing">
          {missing}
        </div>
      )}
      {s.hidden && (
        <div className="warning" role="note" data-testid="discover-hidden">
          {hiddenWords(s.hidden, s.removed_note)}
        </div>
      )}
      {s.chat_off && (
        <div className="notice" role="note" data-testid="discover-chat-off">
          {chatOffWords(s.chat_off)}{" "}
          {s.chat_off === "orders_off" ? <Link to="/settings/areas">Your areas</Link> : <Link to="/settings/customers">Customer chats</Link>}
        </div>
      )}
    </section>
  );
}

/** The listing's own category and area (else the profile's or the page's). */
function Listing({ s, busy, save }: { s: Settings; busy: boolean; save: Save }) {
  const cats = useLoad(
    () =>
      get<{ categories: DiscoverCategory[] }>("/v1/discover/categories").then(
        (r) => r.categories,
        () => Object.entries(CATEGORY_NAMES).map(([key, name]) => ({ key, name })),
      ),
    [],
  );
  const [category, setCategory] = useState(s.category ?? "");
  const [area, setArea] = useState(s.area ?? "");
  const changed = category !== (s.category ?? "") || area.trim() !== (s.area ?? "");
  const shownCat = s.listing.category ? (cats.data?.find((c) => c.key === s.listing.category)?.name ?? CATEGORY_NAMES[s.listing.category]) : undefined;
  return (
    <section className="card" aria-labelledby="discover-what">
      <h2 id="discover-what">What customers search by</h2>
      <p className="hint">Left empty, these come from Your business in Explore and your shop's page.</p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void save({ category, area: area.trim() }, "Saved");
        }}
      >
        <Field label="Category" hint={!s.category && shownCat ? `Now: ${shownCat}, from your profile.` : undefined}>
          <select value={category} onChange={(e) => setCategory(e.target.value)} data-testid="discover-category">
            <option value="">From my profile</option>
            {(cats.data ?? []).map((c) => (
              <option key={c.key} value={c.key}>
                {c.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Area" hint={!s.area && s.listing.area ? `Now: ${s.listing.area}, from your profile or page.` : "Such as Yaba, or Wuse 2."}>
          <input value={area} maxLength={80} onChange={(e) => setArea(e.target.value)} data-testid="discover-area-field" />
        </Field>
        <button type="submit" className="primary" disabled={busy || !changed}>
          Save
        </button>
      </form>
    </section>
  );
}

/** Hours for Discover, for a business whose page gives none. */
function OwnHours({ s, busy, save }: { s: Settings; busy: boolean; save: Save }) {
  const [form, setForm] = useState<HoursForm>();
  const pageHours = hoursLines(s.listing.hours as ChatLinkCard["hours"]);
  const problems = form ? hoursErrors(form) : [];
  return (
    <section className="card" aria-labelledby="discover-hours-title">
      <h2 id="discover-hours-title">Opening hours</h2>
      {s.hours ? (
        <p className="hint">Customers see the hours you set here.</p>
      ) : pageHours.length > 0 ? (
        <p className="hint">Customers see your shop page's hours.</p>
      ) : (
        <p className="hint">Your shop's page has no hours. Set them here so customers can find you.</p>
      )}
      {!form && pageHours.length > 0 && (
        <ul className="discover-hours" data-testid="discover-settings-hours">
          {pageHours.map((h) => (
            <li key={h}>{h}</li>
          ))}
        </ul>
      )}
      {form ? (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (problems.length > 0) return;
            void save({ hours: hoursFromForm(form) }, "Hours saved").then((ok) => ok && setForm(undefined));
          }}
        >
          <HoursEditor value={form} onChange={setForm} />
          {problems.map((p) => (
            <p key={`${p.day}-${p.message}`} className="error" role="alert">
              {p.message}
            </p>
          ))}
          <div className="row-actions">
            <button type="submit" className="primary" disabled={busy || problems.length > 0}>
              Save hours
            </button>
            <button type="button" onClick={() => setForm(undefined)}>
              Cancel
            </button>
          </div>
        </form>
      ) : (
        <div className="row-actions">
          <button type="button" onClick={() => setForm(hoursToForm(s.hours ?? (s.listing.hours as Settings["hours"])))} data-testid="discover-hours-edit">
            {s.hours ? "Change the hours" : "Set hours for Discover"}
          </button>
          {s.hours && (
            <button type="button" disabled={busy} onClick={() => void save({ clear_hours: true }, "Your page's hours are used again")}>
              Use my page's hours
            </button>
          )}
        </div>
      )}
    </section>
  );
}

/** The business's place on the map, so customers near it find it first. */
function Place({ s, busy, save }: { s: Settings; busy: boolean; save: Save }) {
  const [lat, setLat] = useState(s.location ? String(s.location.lat) : "");
  const [lng, setLng] = useState(s.location ? String(s.location.lng) : "");
  const [locating, setLocating] = useState(false);
  const [problem, setProblem] = useState<string>();
  const typed = (): DiscoverLocation | null => {
    const a = Number(lat);
    const b = Number(lng);
    if (!lat.trim() || !lng.trim() || !Number.isFinite(a) || !Number.isFinite(b) || Math.abs(a) > 90 || Math.abs(b) > 180) return null;
    return roundPlace(a, b);
  };
  const here = () => {
    setProblem(undefined);
    if (!("geolocation" in navigator)) return setProblem(placeProblem(2));
    setLocating(true);
    navigator.geolocation.getCurrentPosition(
      (p) => {
        setLocating(false);
        const at = roundPlace(p.coords.latitude, p.coords.longitude);
        setLat(String(at.lat));
        setLng(String(at.lng));
        void save({ location: at }, "Your place is saved");
      },
      (e) => {
        setLocating(false);
        setProblem(placeProblem(e.code));
      },
      { enableHighAccuracy: true, timeout: 20000 },
    );
  };
  return (
    <section className="card" aria-labelledby="discover-place">
      <h2 id="discover-place">Your place on the map</h2>
      <p className="hint">Optional. With it, customers near you see you first. Kept to about 100 metres.</p>
      {s.location ? (
        <p data-testid="discover-pin">
          <Icon name="pin" />{" "}
          <a href={mapLink(s.location)} target="_blank" rel="noopener noreferrer">
            See your place on the map
          </a>
        </p>
      ) : (
        <p className="hint">No place set.</p>
      )}
      <div className="row-actions">
        <button type="button" disabled={busy || locating} onClick={here}>
          {locating ? "Finding you…" : "Use where I am now"}
        </button>
        {s.location && (
          <button type="button" disabled={busy} onClick={() => void save({ clear_location: true }, "Your place is removed").then((ok) => ok && (setLat(""), setLng("")))}>
            Remove the place
          </button>
        )}
      </div>
      {problem && (
        <p className="hint" role="status">
          {problem}
        </p>
      )}
      <details className="discover-type-place">
        <summary>Type the place</summary>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const at = typed();
            if (at) void save({ location: at }, "Your place is saved");
          }}
        >
          <Field label="Latitude" hint="Such as 6.5095">
            <input inputMode="decimal" value={lat} onChange={(e) => setLat(e.target.value)} />
          </Field>
          <Field label="Longitude" hint="Such as 3.3711">
            <input inputMode="decimal" value={lng} onChange={(e) => setLng(e.target.value)} />
          </Field>
          <button type="submit" disabled={busy || !typed()}>
            Save the place
          </button>
        </form>
      </details>
    </section>
  );
}

/** The listing as customers see it in Discover. */
function Preview({ s }: { s: Settings }) {
  const l = s.listing;
  const cat = l.category ? CATEGORY_NAMES[l.category] : "";
  return (
    <section className="card" aria-labelledby="discover-preview" data-testid="discover-preview">
      <h2 id="discover-preview">How customers see you</h2>
      <p className="hint">In a search, as a row:</p>
      <Items label="Your listing in a search">
        <DiscoverRow r={previewResult(l, cat ?? "")} preview />
      </Items>
      {(l.tagline || l.has_shop || l.has_book) && (
        <p className="hint" data-testid="discover-preview-profile">
          On your profile: {[l.tagline && `“${l.tagline}”`, l.has_shop && "Their shop", l.has_book && "Book"].filter(Boolean).join(" · ")}
        </p>
      )}
      {!s.shown && <p className="hint">Customers don't see this yet.</p>}
      <p className="discover-preview-who hint">
        <ChatAvatar name={l.name} logo={photoAt(l.photo?.urls, 160)} /> Your name and photo come from <Link to="/settings/links/profile">Your business in Explore</Link> and your shop's page.
      </p>
    </section>
  );
}
