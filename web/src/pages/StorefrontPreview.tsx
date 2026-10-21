import { DEFAULT_ACCENTS, formatPrice, openNow, type Form, type Item } from "../lib/storefront";

/** The page as customers will see it, drawn from the editor's unsaved
 * form: a rough likeness for while you type, not the published page. */
export function StorefrontPreview({ form, accents, logoURL, items }: { form: Form; accents: Record<string, string>; logoURL?: string; items: Item[] }) {
  const accent = accents[form.accent] ?? DEFAULT_ACCENTS[form.accent] ?? "#0b6e4f";
  const t = form.text;
  const now = openNow(form.hours);
  const shown = items.filter((i) => i.available).slice(0, 4);
  const name = t.name.trim() || "Your business";
  return (
    <aside className="sf-preview" aria-label="Preview of your page">
      <span className="sf-preview-label">Preview</span>
      <div className="sf-phone">
        <div className="sf-screen">
          <div className="sf-cover" style={{ background: accent }}>
            <span className="sf-pv-logo" style={{ color: accent }}>
              {logoURL ? <img src={logoURL} alt="" /> : name.charAt(0).toUpperCase()}
            </span>
          </div>
          <div className="sf-body">
            <strong className="sf-name">{name}</strong>
            {t.tagline.trim() && <span className="sf-tagline">{t.tagline}</span>}
            <span className={`sf-open${now.open ? " on" : ""}`}>
              <span className="dot" aria-hidden="true" />
              {now.open ? `Open now · until ${now.until}` : now.opens ? `Closed · opens at ${now.opens}` : "Closed today"}
            </span>
            {shown.length > 0 && (
              <ul className="sf-preview-items">
                {shown.map((i) => (
                  <li key={i.id}>
                    {i.photo?.["320"] ? <img src={i.photo["320"]} alt="" /> : <span className="sf-noimg" aria-hidden="true" />}
                    <span className="grow">{i.name}</span>
                    {i.price_kobo !== undefined && <span className="num">{formatPrice(i.price_kobo)}</span>}
                  </li>
                ))}
              </ul>
            )}
            {(t.area.trim() || t.address.trim()) && <span className="sf-where">{[t.address.trim(), t.area.trim()].filter(Boolean).join(", ")}</span>}
            <span className="sf-cta" style={{ background: accent }}>
              {shown.length > 0 ? "Order now" : t.whatsapp.trim() ? "Chat on WhatsApp" : "Call us"}
            </span>
          </div>
        </div>
      </div>
    </aside>
  );
}
