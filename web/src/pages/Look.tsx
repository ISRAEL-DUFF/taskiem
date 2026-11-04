import { useEffect, useRef, useState } from "react";
import { ApiError, get, post, put, uploadFile } from "../api";
import { useAuth } from "../auth";
import { ErrorBox, Field, PageHeader, Skeleton, useAction, useLoad } from "../ui";
import { PREVIEW_PAGES, SWATCHES, THEMES, normalizeHex, requestOf, sameLook, type Brand, type BrandRequest, type Theme } from "../lib/brand";
import { imageProblem, thumb, tidy, type Media } from "../lib/storefront";
import { StorefrontTabs } from "./Storefront";

/** How the business's public pages look (N13): a theme, a colour, a logo
 * and a cover photo, for the storefront, its forms and hosted forms, with
 * the real pages previewed as you choose. */
export function Look() {
  const brand = useLoad(() => get<Brand>("/v1/settings/brand"), []);
  return (
    <>
      <PageHeader title="Storefront" description="How your storefront, order and booking pages and your forms look to customers." />
      <StorefrontTabs />
      <ErrorBox error={brand.error} />
      {brand.data ? <LookEditor initial={brand.data} /> : !brand.error && <Skeleton rows={4} label="Loading your look…" />}
    </>
  );
}

/** A look's images: ids to send, and thumbnails to show. */
interface Images {
  logo?: string;
  logoURL?: string;
  cover?: string;
  coverURL?: string;
}

function LookEditor({ initial }: { initial: Brand }) {
  const { can } = useAuth();
  const [saved, setSaved] = useState(initial);
  const [theme, setTheme] = useState<Theme>(initial.theme);
  const [color, setColor] = useState(initial.color);
  const [typed, setTyped] = useState(initial.color);
  const [images, setImages] = useState<Images>({ logo: initial.logo, logoURL: thumb(initial.logo_urls), cover: initial.cover, coverURL: thumb(initial.cover_urls) });
  const act = useAction();
  const editable = can("storefront.manage");
  const look: BrandRequest = { theme, color, logo: images.logo ?? "", cover: images.cover ?? "" };
  const dirty = !sameLook(look, requestOf(saved));
  const typedBad = normalizeHex(typed) === null;

  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const pick = (hex: string) => {
    setColor(hex);
    setTyped(hex);
  };

  return (
    <div className="look">
      <div className="look-edit">
        <section className="card" aria-labelledby="look-theme">
          <h2 id="look-theme">Theme</h2>
          <p className="hint">Each theme lays your pages out its own way. Your words, prices and photos stay the same.</p>
          <div className="look-themes" role="radiogroup" aria-labelledby="look-theme">
            {THEMES.map((t) => (
              <label key={t.id} className={`look-theme${theme === t.id ? " selected" : ""}`}>
                <input type="radio" name="look-theme" value={t.id} checked={theme === t.id} disabled={!editable} onChange={() => setTheme(t.id)} />
                <span className={`look-mini look-mini-${t.id}`} aria-hidden="true">
                  <span className="look-mini-top" style={{ background: t.id === "editorial" ? "#e9e5df" : color }} />
                  <span className="look-mini-line" />
                  <span className="look-mini-line short" />
                  <span className="look-mini-btn" style={{ background: color }} />
                </span>
                <strong>{t.title}</strong>
                <span className="hint">{t.help}</span>
              </label>
            ))}
          </div>
        </section>

        <section className="card" aria-labelledby="look-colour">
          <h2 id="look-colour">Colour</h2>
          <p className="hint">Buttons, highlights and, in Bold, the top of each page. Any colour works: text on it stays readable.</p>
          <div className="look-colour">
            <input type="color" aria-label="Pick a colour" value={color} disabled={!editable} onChange={(e) => pick(e.target.value)} />
            <Field label="Colour code" hint={typedBad ? "Type a colour code like #0b6e4f." : undefined}>
              <input
                value={typed}
                disabled={!editable}
                spellCheck={false}
                onChange={(e) => {
                  setTyped(e.target.value);
                  const hex = normalizeHex(e.target.value);
                  if (hex) setColor(hex);
                }}
              />
            </Field>
          </div>
          <div className="sf-swatches" role="group" aria-label="Suggested colours">
            {SWATCHES.map((hex) => (
              <button key={hex} type="button" className={`look-swatch${color === hex ? " selected" : ""}`} disabled={!editable} onClick={() => pick(hex)} aria-label={hex} aria-pressed={color === hex}>
                <span className="sf-dot" style={{ background: hex }} aria-hidden="true" />
              </button>
            ))}
          </div>
        </section>

        <section className="card" aria-labelledby="look-images">
          <h2 id="look-images">Logo and cover photo</h2>
          <ImageField
            label="Logo"
            hint="Square works best. JPEG, PNG or WebP. Without one, your initials are shown."
            url={images.logoURL}
            disabled={!editable}
            onChange={(id, url) => setImages((i) => ({ ...i, logo: id, logoURL: url }))}
          />
          <ImageField
            label="Cover photo"
            hint="A wide photo of your shop, your work or your products, shown across the top of your storefront."
            url={images.coverURL}
            wide
            disabled={!editable}
            onChange={(id, url) => setImages((i) => ({ ...i, cover: id, coverURL: url }))}
          />
        </section>

        {editable && (
          <div className="look-save">
            <button
              type="button"
              className="primary"
              disabled={!dirty || act.busy}
              onClick={() =>
                void act.run(async () => {
                  const b = await tidy(put<Brand>("/v1/settings/brand", look));
                  setSaved(b);
                }, "Saved. Your pages show the new look within a minute.")
              }
            >
              Save the look
            </button>
            {dirty && <span className="hint">You have changes that are not saved.</span>}
            <ErrorBox error={act.error} />
          </div>
        )}
      </div>
      <LivePreview look={look} />
    </div>
  );
}

function ImageField({ label, hint, url, wide, disabled, onChange }: { label: string; hint: string; url?: string; wide?: boolean; disabled: boolean; onChange: (id: string | undefined, url?: string) => void }) {
  const act = useAction();
  const input = useRef<HTMLInputElement>(null);
  return (
    <div className="look-image">
      <Field label={label} hint={hint}>
        <input
          ref={input}
          type="file"
          accept="image/jpeg,image/png,image/webp"
          disabled={disabled || act.busy}
          onChange={(e) => {
            const file = e.target.files?.[0];
            e.target.value = "";
            if (!file) return;
            void act.run(async () => {
              const bad = imageProblem(file);
              if (bad) throw new Error(bad);
              const m = await tidy(uploadFile<Media>("/v1/storefront/media", file));
              onChange(m.id, thumb(m.urls));
            }, "Uploaded. Save the look to use it.");
          }}
        />
      </Field>
      {act.busy && <p className="hint" role="status">Uploading…</p>}
      <ErrorBox error={act.error} />
      {url && (
        <div className="look-image-preview">
          <img src={url} alt={`Your ${label.toLowerCase()}`} className={wide ? "wide" : undefined} />
          {!disabled && (
            <button type="button" onClick={() => onChange(undefined, undefined)}>
              Remove
            </button>
          )}
        </div>
      )}
    </div>
  );
}

/** The real pages, drawn by the public host in the look being chosen,
 * with sample content: they change as you choose. */
function LivePreview({ look }: { look: BrandRequest }) {
  const [page, setPage] = useState("storefront");
  const [pages, setPages] = useState<Record<string, string>>();
  const [off, setOff] = useState(false);
  const [error, setError] = useState<unknown>();
  const key = JSON.stringify(look);
  useEffect(() => {
    // Wait for the choosing to settle, then ask for the new pages.
    const t = window.setTimeout(() => {
      post<{ pages: Record<string, string> }>("/v1/settings/brand/preview", JSON.parse(key) as BrandRequest).then(
        (r) => {
          setPages(r.pages);
          setError(undefined);
        },
        (e: unknown) => {
          if (e instanceof ApiError && e.status === 409) setOff(true);
          else setError(e);
        },
      );
    }, 350);
    return () => window.clearTimeout(t);
  }, [key]);
  if (off) {
    return (
      <aside className="look-preview" aria-label="Preview">
        <p className="hint">The preview shows once this Taskiem's public pages are set up.</p>
      </aside>
    );
  }
  return (
    <aside className="look-preview" aria-label="Preview of your pages">
      <nav className="tabs look-preview-tabs" aria-label="Page to preview">
        {PREVIEW_PAGES.map((p) => (
          <button key={p.id} type="button" className={page === p.id ? "active" : undefined} aria-pressed={page === p.id} onClick={() => setPage(p.id)}>
            {p.label}
          </button>
        ))}
      </nav>
      <div className="sf-phone look-phone">
        {pages?.[page] ? (
          <iframe key={pages[page]} src={pages[page]} title={`Preview: ${PREVIEW_PAGES.find((p) => p.id === page)?.label ?? page}`} sandbox="allow-scripts" loading="lazy" />
        ) : (
          <Skeleton rows={6} label="Drawing the preview…" />
        )}
      </div>
      <p className="hint">Sample products and times, in your look. Your own appear on your pages.</p>
      <ErrorBox error={error} />
    </aside>
  );
}
