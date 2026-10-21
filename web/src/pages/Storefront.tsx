import { useEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Link, NavLink } from "react-router-dom";
import { ApiError, currentAgentSession, del, get, getBlob, post, put, uploadFile } from "../api";
import { useAuth } from "../auth";
import { EmptyState, ErrorBox, Field, Modal, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { Icon } from "../icons";
import { StorefrontForms } from "./StorefrontForms";
import { PageStatusBadge, RefusalBox, TrustNotices, TrustPanel } from "./StorefrontTrust";
import { publishNotice, type Trust } from "../lib/trust";
import { StorefrontDomains } from "./StorefrontDomains";
import { StorefrontEmbedSites } from "./StorefrontEmbedSites";
import { StorefrontPreview } from "./StorefrontPreview";
import type { Table } from "../lib/tables";
import {
  DAYS,
  DEFAULT_ACCENTS,
  DEFAULT_SPAN,
  DEFAULT_TEMPLATES,
  LIMITS,
  MAPPING,
  MAX_OPENINGS,
  TEMPLATE_HELP,
  catalogueTables,
  columnsFor,
  formFrom,
  formatPrice,
  guessColumns,
  hoursErrors,
  imageProblem,
  pageURL,
  pastMidnight,
  requestFrom,
  sameAsMonday,
  sameRequest,
  slugError,
  slugify,
  thumb,
  tidy,
  type Catalogue,
  type Columns,
  type Form,
  type HoursForm,
  type Media,
  type Storefront as SF,
  type TextField,
  itemRow,
} from "../lib/storefront";

/** The organisation's storefront (docs/spec/storefronts.md): a one-page
 * public page with the business card and a catalogue from a data table. */
export function Storefront() {
  const { can } = useAuth();
  const sf = useLoad(
    () =>
      get<SF>("/v1/storefront").catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }),
    [],
  );
  const tables = useLoad(() => (can("table.read") ? get<{ tables: Table[] }>("/v1/tables") : Promise.resolve({ tables: [] as Table[] })), []);
  const header = <PageHeader title="Storefront" description="A one-page public page for your business: your card, your opening hours and what you sell, with a QR code to share." />;
  if (sf.error) {
    return (
      <>
        {header}
        <ErrorBox error={sf.error} />
      </>
    );
  }
  if (sf.data === undefined) {
    return (
      <>
        {header}
        <Skeleton rows={5} label="Loading your storefront…" />
      </>
    );
  }
  return <StorefrontEditor initial={sf.data} tables={tables.data?.tables} tablesError={tables.error} />;
}

function StorefrontEditor({ initial, tables, tablesError }: { initial: SF | null; tables: Table[] | undefined; tablesError: unknown }) {
  const [saved, setSaved] = useState<SF | null>(initial);
  const [form, setForm] = useState<Form>(() => formFrom(initial));
  // A new storefront's address follows its name until the owner types one.
  const [slugTouched, setSlugTouched] = useState(initial !== null);
  // The logo's URL, known only for a logo uploaded on this visit.
  const [logoURL, setLogoURL] = useState<string>();
  const [closing, setClosing] = useState(false);
  const [notice, setNotice] = useState("");
  const [catalogueTick, setCatalogueTick] = useState(0);
  const [trustTick, setTrustTick] = useState(0);
  const trust = useLoad(() => (saved ? get<Trust>("/v1/storefront/trust") : Promise.resolve(undefined)), [saved === null, trustTick]);
  // What the preview lists: the catalogue as last copied.
  const previewItems = useLoad(
    () => (saved?.catalogue_table ? get<Catalogue>("/v1/storefront/catalogue").catch(() => null) : Promise.resolve(null)),
    [saved?.catalogue_table, catalogueTick],
  );
  const reloadTrust = () => setTrustTick((n) => n + 1);
  const act = useAction();
  const base = formFrom(saved);
  const dirty = saved === null || !sameRequest(form, base);
  const templates = saved?.templates ?? DEFAULT_TEMPLATES;
  const accents = saved?.accents ?? DEFAULT_ACCENTS;
  const published = !!saved && saved.published_version > 0;
  // Served to customers: not waiting for Taskiem's check, nor suspended.
  const live = published && (saved.status === "published" || saved.status === "closed");
  const hourProblems = hoursErrors(form.hours);
  const slugBad = slugError(form.slug);

  // Warn before leaving with unsaved changes.
  useEffect(() => {
    if (!dirty || saved === null) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty, saved]);

  const setText = (k: TextField, v: string) =>
    setForm((f) => {
      const next = { ...f, text: { ...f.text, [k]: v } };
      if (k === "name" && !slugTouched) next.slug = slugify(v);
      return next;
    });

  const changed = (sf: SF, msg: string) => {
    setSaved(sf);
    setForm(formFrom(sf));
    setSlugTouched(true);
    setNotice(msg);
  };

  /** Saves the draft; refreshes the catalogue copy when its mapping changed. */
  const save = async (): Promise<SF> => {
    const mappingChanged =
      JSON.stringify([form.table, requestFrom(form).catalogue_columns]) !== JSON.stringify([saved?.catalogue_table ?? null, requestFrom(base).catalogue_columns]);
    const out = await tidy(put<SF>("/v1/storefront", requestFrom(form)));
    changed(out, "Draft saved.");
    if (mappingChanged && out.catalogue_table) {
      await post("/v1/storefront/catalogue/refresh").catch(() => undefined); // the catalogue panel shows its error
    }
    setCatalogueTick((n) => n + 1);
    return out;
  };

  const publish = async () => {
    if (dirty) await save();
    const out = await tidy(post<SF>("/v1/storefront/publish"));
    changed(out, publishNotice(out));
    setCatalogueTick((n) => n + 1);
    reloadTrust();
  };

  /** Makes the catalogue's table and maps it, as chat does: no table to build by hand. */
  const makeCatalogue = () =>
    act.run(async () => {
      if (dirty) await save();
      await tidy(post("/v1/storefront/catalogue/make", { kind: "products" }));
      changed(await get<SF>("/v1/storefront"), "Your catalogue is ready: add what you sell below.");
      setCatalogueTick((n) => n + 1);
    });

  const canSave = !slugBad && hourProblems.length === 0;
  const suspended = saved?.status === "suspended";

  return (
    <>
      <PageHeader
        title="Storefront"
        description="A one-page public page for your business: your card, your opening hours and what you sell, with a QR code to share."
        actions={
          <>
            <button disabled={act.busy || !dirty || !canSave} onClick={() => void act.run(save)}>
              Save draft
            </button>
            <button
              className="primary"
              disabled={act.busy || !canSave || suspended}
              title={suspended ? "A suspended page cannot be published until Taskiem restores it" : undefined}
              onClick={() => void act.run(publish)}
            >
              {dirty && saved ? "Save and publish" : "Publish"}
            </button>
          </>
        }
      />
      <StorefrontTabs />
      {notice && (
        <div className="notice" role="status">
          {notice}
        </div>
      )}
      <RefusalBox error={act.error} />

      <div className="sf-layout">
        <div className="sf-main">
      <section className="card" aria-labelledby="sf-status">
        <h2 id="sf-status">Status</h2>
        {saved === null ? (
          <p className="hint" style={{ margin: 0 }}>
            You have no storefront yet. Fill in your card below, save it as a draft, and publish it when it is ready.
          </p>
        ) : (
          <>
            <dl className="kv">
              <dt>Status</dt>
              <dd>
                <PageStatusBadge status={saved.status} />
                {dirty && <span className="hint"> Unsaved changes</span>}
              </dd>
              <dt>Published version</dt>
              <dd>{published ? saved.published_version : "Not published yet"}</dd>
              <dt>Published</dt>
              <dd>{fmtTime(saved.published_at)}</dd>
              {live && (
                <>
                  <dt>Address</dt>
                  <dd>
                    {pageURL(saved) ? (
                      <a href={pageURL(saved)} target="_blank" rel="noreferrer">
                        {pageURL(saved).replace(/^https:\/\//, "")}
                      </a>
                    ) : (
                      <span className="hint">This Taskiem has no public address for storefronts yet.</span>
                    )}
                  </dd>
                </>
              )}
              {saved.status === "closed" && (
                <>
                  <dt>Message</dt>
                  <dd>{saved.closed_message || <span className="hint">Not taking orders now.</span>}</dd>
                </>
              )}
            </dl>
            <TrustNotices state={saved} trust={trust.data} onChanged={reloadTrust} />
            {published && (
              <div className="toolbar" style={{ marginTop: 12, marginBottom: 0 }}>
                {saved.status === "published" && (
                  <button disabled={act.busy} onClick={() => setClosing(true)}>
                    Close for now
                  </button>
                )}
                {saved.status === "closed" && (
                  <button
                    disabled={act.busy}
                    onClick={() => void act.run(async () => changed(await tidy(post<SF>("/v1/storefront/reopen")), "Your storefront is open again."))}
                  >
                    Reopen
                  </button>
                )}
                {dirty && <span className="hint">Changes show on your page once you publish them.</span>}
              </div>
            )}
          </>
        )}
      </section>

      <section className="card" aria-labelledby="sf-address">
        <h2 id="sf-address">Address</h2>
        <Field label="Page address" hint={(form.slug && slugBad) || (saved?.preview_url ? `Your page: ${saved.preview_url.replace(/^https:\/\/[^.]+/, form.slug || "…")}` : "3 to 30 lower-case letters, digits and hyphens, such as mama-put-kitchen.")}>
          <input
            value={form.slug}
            onChange={(e) => {
              setSlugTouched(true);
              setForm((f) => ({ ...f, slug: e.target.value.trim().toLowerCase() }));
            }}
            placeholder="mama-put-kitchen"
            autoComplete="off"
            spellCheck={false}
            aria-invalid={!!form.slug && !!slugBad}
          />
        </Field>
      </section>

      <section className="card" aria-labelledby="sf-card">
        <h2 id="sf-card">Business card</h2>
        <Field label="Business name">
          <input value={form.text.name} onChange={(e) => setText("name", e.target.value)} autoComplete="organization" />
        </Field>
        <TextInput label="Tagline" hint="One line about what you sell." value={form.text.tagline} max={LIMITS.tagline} onChange={(v) => setText("tagline", v)} />
        <TextInput label="About" value={form.text.about} max={LIMITS.about} multiline onChange={(v) => setText("about", v)} />
        <div className="row">
          <TextInput label="Area" hint="Where you are or deliver to, such as Yaba, Lagos." value={form.text.area} max={LIMITS.area} onChange={(v) => setText("area", v)} />
          <TextInput label="Address" value={form.text.address} max={LIMITS.address} onChange={(v) => setText("address", v)} />
        </div>
        <TextInput
          label="Map search"
          hint="What a map link searches for. Left empty, your name and address are used."
          value={form.text.map_query}
          max={LIMITS.map_query}
          onChange={(v) => setText("map_query", v)}
        />
        <div className="row">
          <Field label="Phone" hint="For the call button, such as 0803 123 4567.">
            <input type="tel" value={form.text.phone} onChange={(e) => setText("phone", e.target.value)} autoComplete="tel" inputMode="tel" />
          </Field>
          <Field label="WhatsApp number" hint="For the chat on WhatsApp button.">
            <input type="tel" value={form.text.whatsapp} onChange={(e) => setText("whatsapp", e.target.value)} inputMode="tel" />
          </Field>
        </div>
      </section>

      <section className="card" aria-labelledby="sf-look">
        <h2 id="sf-look">Look</h2>
        <fieldset className="sf-choices">
          <legend>Template</legend>
          <div className="sf-templates">
            {templates.map((t) => (
              <label key={t} className={`sf-template${form.template === t ? " selected" : ""}`}>
                <input type="radio" name="sf-template" value={t} checked={form.template === t} onChange={() => setForm((f) => ({ ...f, template: t }))} />
                <span>
                  <strong>{TEMPLATE_HELP[t]?.title ?? t}</strong>
                  <span className="hint">{TEMPLATE_HELP[t]?.help ?? ""}</span>
                </span>
              </label>
            ))}
          </div>
        </fieldset>
        <fieldset className="sf-choices">
          <legend>Accent colour</legend>
          <div className="sf-swatches">
            {Object.entries(accents).map(([name, hex]) => (
              <label key={name} className={`sf-swatch${form.accent === name ? " selected" : ""}`}>
                <input type="radio" name="sf-accent" value={name} checked={form.accent === name} onChange={() => setForm((f) => ({ ...f, accent: name }))} />
                <span className="sf-dot" style={{ background: hex }} aria-hidden="true" />
                <span className="capitalize">{name}</span>
              </label>
            ))}
          </div>
        </fieldset>
        <LogoField
          logo={form.text.logo}
          url={logoURL ?? (form.text.logo === saved?.draft.logo ? thumb(saved?.logo_urls) : undefined)}
          onChange={(id, url) => {
            setText("logo", id);
            setLogoURL(url);
          }}
        />
      </section>

      <section className="card" aria-labelledby="sf-hours">
        <h2 id="sf-hours">Opening hours</h2>
        <p className="hint">In Lagos time. Leave every day closed to show no hours at all.</p>
        <HoursEditor value={form.hours} onChange={(hours) => setForm((f) => ({ ...f, hours }))} />
        {hourProblems.map((p) => (
          <div key={`${p.day}-${p.message}`} className="error-text">
            {p.message}
          </div>
        ))}
      </section>

      <section className="card sf-catalogue" aria-labelledby="sf-catalogue">
        <h2 id="sf-catalogue">Catalogue</h2>
        {!saved?.catalogue_table &&
          (saved ? (
            <div className="notice">
              <p style={{ marginTop: 0 }}>Show what you sell, with prices and photos. Taskiem keeps the list for you.</p>
              <button className="primary" disabled={act.busy || !canSave} onClick={() => void makeCatalogue()}>
                Make my catalogue
              </button>
            </div>
          ) : (
            <p className="hint">Save your page first, then add what you sell.</p>
          ))}
        {saved?.catalogue_table && (
          <CatalogueCopy
            key={saved.catalogue_table}
            tick={catalogueTick}
            table={saved.catalogue_table}
            columns={saved.catalogue_columns}
            photoColumn={saved.catalogue_columns.photo}
            stale={dirty && form.table !== null}
          />
        )}
        <details open={!saved?.catalogue_table && !!form.table}>
          <summary>{saved?.catalogue_table ? "Catalogue settings" : "Use a table you already have"}</summary>
          <CatalogueMapping form={form} setForm={setForm} tables={tables} tablesError={tablesError} />
        </details>
      </section>
        </div>
        <StorefrontPreview form={form} accents={accents} logoURL={logoURL} items={previewItems.data?.items ?? []} />
      </div>

      {saved && <TrustPanel trust={trust.data} error={trust.error} onChanged={reloadTrust} />}

      {saved && <StorefrontForms previewURL={pageURL(saved)} published={published} />}

      {saved && live && pageURL(saved) && <Share sf={saved} form={form} />}

      {saved && !currentAgentSession() && (
        <StorefrontDomains
          published={published}
          onChange={() => void get<SF>("/v1/storefront").then(setSaved, () => undefined)}
        />
      )}

      {saved && live && pageURL(saved) && !currentAgentSession() && <StorefrontEmbedSites pageURL={pageURL(saved)} business={saved.draft.name} />}

      {closing && (
        <CloseDialog
          onClose={() => setClosing(false)}
          onClosed={(sf) => {
            setClosing(false);
            changed(sf, "Your storefront is closed for now. Your card still shows.");
          }}
        />
      )}
    </>
  );
}

/** A text input or text area with its limit counted. */
function TextInput({ label, hint, value, max, multiline, onChange }: { label: string; hint?: string; value: string; max: number; multiline?: boolean; onChange: (v: string) => void }) {
  const over = value.length > max;
  const count = `${value.length}/${max}`;
  return (
    <Field label={label} hint={<span className={over ? "error-text" : undefined}>{hint ? `${hint} ` : ""}{count}</span>}>
      {multiline ? (
        <textarea value={value} rows={4} onChange={(e) => onChange(e.target.value)} aria-invalid={over} />
      ) : (
        <input value={value} onChange={(e) => onChange(e.target.value)} aria-invalid={over} />
      )}
    </Field>
  );
}

function LogoField({ logo, url, onChange }: { logo: string; url?: string; onChange: (id: string, url?: string) => void }) {
  const act = useAction();
  const input = useRef<HTMLInputElement>(null);
  return (
    <div className="sf-logo">
      <Field label="Logo or photo" hint="JPEG, PNG or WebP, up to 5 MB. Location and other details in the photo are removed.">
        <input
          ref={input}
          type="file"
          accept="image/jpeg,image/png,image/webp"
          disabled={act.busy}
          onChange={(e) => {
            const file = e.target.files?.[0];
            e.target.value = "";
            if (!file) return;
            void act.run(async () => {
              const bad = imageProblem(file);
              if (bad) throw new Error(bad);
              const m = await tidy(uploadFile<Media>("/v1/storefront/media", file));
              onChange(m.id, thumb(m.urls));
            });
          }}
        />
      </Field>
      {act.busy && <p className="hint" role="status">Uploading…</p>}
      <ErrorBox error={act.error} />
      {logo && (
        <div className="sf-logo-preview">
          {url ? <img src={url} alt="Your logo" width={96} height={96} /> : <span className="hint">A logo is set.</span>}
          <button type="button" onClick={() => onChange("", undefined)}>
            Remove logo
          </button>
        </div>
      )}
    </div>
  );
}

function HoursEditor({ value, onChange }: { value: HoursForm; onChange: (h: HoursForm) => void }) {
  const setDay = (i: number, day: HoursForm["days"][number]) => onChange({ ...value, days: value.days.map((d, j) => (j === i ? day : d)) });
  return (
    <>
      <div className="sf-hours">
        {value.days.map((d, i) => {
          const day = DAYS[i];
          return (
            <fieldset key={day} className="sf-day">
              <legend className="sr-only">{day}</legend>
              <label className="sf-day-name">
                <input type="checkbox" checked={d.open} onChange={(e) => setDay(i, { ...d, open: e.target.checked, spans: d.spans.length ? d.spans : [{ ...DEFAULT_SPAN }] })} />
                {day}
                <span className="sr-only"> open</span>
              </label>
              {d.open ? (
                <div className="sf-spans">
                  {d.spans.map((s, n) => {
                    const which = d.spans.length > 1 ? ` (opening ${n + 1})` : "";
                    const set = (patch: Partial<typeof s>) => setDay(i, { ...d, spans: d.spans.map((x, m) => (m === n ? { ...x, ...patch } : x)) });
                    return (
                      <div key={n} className="sf-span">
                        <input type="time" aria-label={`${day} opens${which}`} value={s.open} onChange={(e) => set({ open: e.target.value })} />
                        <span aria-hidden="true">to</span>
                        <input type="time" aria-label={`${day} closes${which}`} value={s.close} onChange={(e) => set({ close: e.target.value })} />
                        {pastMidnight(s) && <span className="hint">next day</span>}
                        {d.spans.length > 1 && (
                          <button type="button" className="icon-button" aria-label={`Remove ${day} opening ${n + 1}`} onClick={() => setDay(i, { ...d, spans: d.spans.filter((_, m) => m !== n) })}>
                            <Icon name="close" />
                          </button>
                        )}
                      </div>
                    );
                  })}
                </div>
              ) : (
                <span className="hint sf-spans">Closed</span>
              )}
              <div className="sf-day-actions">
                {d.open && d.spans.length < MAX_OPENINGS && (
                  <button type="button" aria-label={`Add an opening on ${day}`} onClick={() => setDay(i, { ...d, spans: [...d.spans, { open: "", close: "" }] })}>
                    <Icon name="plus" />
                    Opening
                  </button>
                )}
                {i >= 1 && i <= 4 && (
                  <button type="button" aria-label={`${day}: same as Monday`} onClick={() => onChange(sameAsMonday(value, i))}>
                    Same as Monday
                  </button>
                )}
              </div>
            </fieldset>
          );
        })}
      </div>
      <TextInput label="Note on the hours" hint="Such as: Closed on public holidays." value={value.note} max={LIMITS.note} onChange={(note) => onChange({ ...value, note })} />
    </>
  );
}

function CatalogueMapping({ form, setForm, tables, tablesError }: { form: Form; setForm: (fn: (f: Form) => Form) => void; tables: Table[] | undefined; tablesError: unknown }) {
  const usable = tables ? catalogueTables(tables) : [];
  const table = tables?.find((t) => t.id === form.table);
  const setColumns = (patch: Columns) => setForm((f) => ({ ...f, columns: { ...f.columns, ...patch } }));
  if (tablesError) {
    return (
      <>
        <p className="hint">Your tables could not be loaded, so the catalogue cannot be changed now.</p>
        <ErrorBox error={tablesError} />
      </>
    );
  }
  if (!tables) return <Skeleton rows={2} label="Loading your tables…" />;
  return (
    <>
      <p className="hint">
        What you sell comes from one of your <Link to="/tables">tables</Link>: each row is an item. Changes to the table show on your page within minutes.
      </p>
      <Field label="Catalogue table">
        <select
          value={form.table ?? ""}
          onChange={(e) => {
            const id = e.target.value || null;
            const t = tables.find((x) => x.id === id);
            setForm((f) => ({ ...f, table: id, columns: t ? guessColumns(t) : { price_unit: "naira" } }));
          }}
        >
          <option value="">No catalogue</option>
          {usable.map((t) => (
            <option key={t.id} value={t.id}>
              {t.title || t.name}
            </option>
          ))}
          {form.table && !table && <option value={form.table}>A table that no longer exists</option>}
        </select>
      </Field>
      {usable.length === 0 && (
        <p className="hint">
          None of your tables can be a catalogue yet: it needs a text column for the item's name that is not personal data. <Link to="/tables">Create a table</Link> with name and price columns.
        </p>
      )}
      {table && (
        <div className="sf-mapping">
          {MAPPING.map((m) => {
            const options = columnsFor(table.columns, m.types);
            return (
              <Field key={m.key} label={m.required ? `${m.label} column` : `${m.label} column (optional)`} hint={`${m.help} (${m.types.join(" or ")}).`}>
                <select value={form.columns[m.key] ?? ""} onChange={(e) => setColumns({ [m.key]: e.target.value })} required={m.required} aria-invalid={m.required && !form.columns[m.key]}>
                  <option value="">{m.required ? "Choose a column" : "None"}</option>
                  {options.map((c) => (
                    <option key={c.name} value={c.name}>
                      {c.title ? `${c.title} (${c.name})` : c.name}
                    </option>
                  ))}
                </select>
              </Field>
            );
          })}
          {form.columns.price && form.columns.price_unit === "kobo" && (
            <Field data-technical label="Prices in the table are in" hint="Naira: 1500 is ₦1,500. Kobo: 150000 is ₦1,500.">
              <select value={form.columns.price_unit || "naira"} onChange={(e) => setColumns({ price_unit: e.target.value as Columns["price_unit"] })}>
                <option value="naira">Naira</option>
                <option value="kobo">Kobo</option>
              </select>
            </Field>
          )}
          <p className="hint sf-mapping-note">Columns holding personal data are never offered: they can never be shown on a public page.</p>
        </div>
      )}
    </>
  );
}

/** The copy of the catalogue pages read, with photos added per item. */
function CatalogueCopy({ tick, table, columns, photoColumn, stale }: { tick: number; table: string; columns: Columns; photoColumn?: string; stale: boolean }) {
  const { can } = useAuth();
  const [name, setName] = useState("");
  const [price, setPrice] = useState("");
  const [problem, setProblem] = useState("");
  const cat = useLoad(() => get<Catalogue>("/v1/storefront/catalogue"), [tick]);
  const act = useAction();
  const [data, setData] = useState<Catalogue>();
  const fileInput = useRef<HTMLInputElement>(null);
  const target = useRef<string>("");
  useEffect(() => setData(cat.data), [cat.data]);
  const refresh = () => act.run(async () => setData(await tidy(post<Catalogue>("/v1/storefront/catalogue/refresh"))));
  const editable = can("table.edit");
  // Adding and removing items here writes the table: nobody opens Tables.
  const addItem = () => {
    const made = itemRow(columns, name, price);
    if ("error" in made) {
      setProblem(made.error);
      return;
    }
    setProblem("");
    void act.run(async () => {
      await tidy(post(`/v1/tables/${table}/rows`, { row: made.row }));
      setName("");
      setPrice("");
      setData(await tidy(post<Catalogue>("/v1/storefront/catalogue/refresh")));
    });
  };
  const removeItem = (id: string, label: string) =>
    confirm(`Remove ${label} from your catalogue?`) &&
    void act.run(async () => {
      await tidy(del(`/v1/tables/${table}/rows/${encodeURIComponent(id)}`));
      setData(await tidy(post<Catalogue>("/v1/storefront/catalogue/refresh")));
    });
  const shown = data ?? cat.data;
  return (
    <div className="sf-copy">
      <div className="toolbar" style={{ marginTop: 18 }}>
        <h3 style={{ margin: 0 }}>On your page</h3>
        <span className="grow" />
        <span className="hint">Copied {fmtTime(shown?.synced_at)}</span>
        <button disabled={act.busy} onClick={() => void refresh()}>
          {act.busy ? "Refreshing…" : "Refresh"}
        </button>
      </div>
      {stale && <p className="hint">Save your draft to use the catalogue settings above.</p>}
      <ErrorBox error={cat.error ?? act.error} />
      {shown?.error && (
        <div className="warning" role="status">
          The catalogue could not be read from its table: {shown.error}
        </div>
      )}
      {!shown && !cat.error && <Skeleton rows={3} label="Loading the catalogue…" />}
      {editable && (
        <form
          className="toolbar sf-add-item"
          onSubmit={(e) => {
            e.preventDefault();
            addItem();
          }}
        >
          <input aria-label="Item" placeholder="What you sell, such as Jollof rice" value={name} onChange={(e) => setName(e.target.value)} maxLength={120} />
          <input aria-label="Price in naira" placeholder="Price (₦)" inputMode="decimal" value={price} onChange={(e) => setPrice(e.target.value)} style={{ width: "9em" }} />
          <button type="submit" className="primary" disabled={act.busy || !name.trim()}>
            Add
          </button>
          {problem && (
            <span className="error-text" role="alert">
              {problem}
            </span>
          )}
        </form>
      )}
      {shown && shown.items.length === 0 && <EmptyState icon="tables">No items yet. Add what you sell above.</EmptyState>}
      {shown && shown.items.length > 0 && (
        <>
          {!photoColumn && <p className="hint">Choose a photo column above and save to add photos to items here.</p>}
          <ul className="sf-items">
            {shown.items.map((item) => {
              const src = thumb(item.photo);
              return (
                <li key={item.id} className={item.available ? undefined : "unavailable"}>
                  <Thumb src={src} />
                  <div className="sf-item-text">
                    <strong>{item.name}</strong>
                    {item.category && <span className="hint"> · {item.category}</span>}
                    {item.description && <div className="hint sf-item-desc">{item.description}</div>}
                  </div>
                  <div className="sf-item-side">
                    {item.price_kobo !== undefined && item.price_kobo !== null && <span className="sf-price">{formatPrice(item.price_kobo)}</span>}
                    {!item.available && <span className="badge">unavailable</span>}
                    {item.available && item.stock !== undefined && item.stock !== null && (
                      <span className={item.stock > 0 ? "hint" : "badge"}>{item.stock > 0 ? `${item.stock} left` : "sold out"}</span>
                    )}
                    {photoColumn && (
                      <button
                        type="button"
                        disabled={act.busy}
                        aria-label={`${src ? "Change photo" : "Add photo"} for ${item.name}`}
                        onClick={() => {
                          target.current = item.id;
                          fileInput.current?.click();
                        }}
                      >
                        {src ? "Change photo" : "Add photo"}
                      </button>
                    )}
                    {editable && (
                      <button type="button" className="link" disabled={act.busy} aria-label={`Remove ${item.name}`} onClick={() => removeItem(item.id, item.name)}>
                        Remove
                      </button>
                    )}
                  </div>
                </li>
              );
            })}
          </ul>
          <input
            ref={fileInput}
            type="file"
            accept="image/jpeg,image/png,image/webp"
            hidden
            onChange={(e) => {
              const file = e.target.files?.[0];
              e.target.value = "";
              const row = target.current;
              if (!file || !row) return;
              void act.run(async () => {
                const bad = imageProblem(file);
                if (bad) throw new Error(bad);
                await tidy(uploadFile<Media>(`/v1/storefront/catalogue/rows/${encodeURIComponent(row)}/photo`, file));
                setData(await tidy(post<Catalogue>("/v1/storefront/catalogue/refresh")));
              });
            }}
          />
          {act.busy && <p className="hint" role="status">Working…</p>}
        </>
      )}
    </div>
  );
}

/** The QR code, its PNG, and a printable A6 card. */
function Share({ sf, form }: { sf: SF; form: Form }) {
  const [qr, setQr] = useState<string>();
  const [error, setError] = useState<unknown>();
  useEffect(() => {
    let live = true;
    // A data: URL, not a blob: one: the console's Content Security Policy allows only data: images besides its own.
    getBlob("/v1/storefront/qr")
      .then(dataURL)
      .then(
        (url) => {
          if (!live) return;
          setQr(url);
          setError(undefined);
        },
        (e: unknown) => live && setError(e),
      );
    return () => {
      live = false;
    };
  }, [sf.slug, sf.published_version, sf.preview_url, sf.url]);
  const print = () => {
    document.body.classList.add("printing-card");
    const done = () => {
      document.body.classList.remove("printing-card");
      window.removeEventListener("afterprint", done);
    };
    window.addEventListener("afterprint", done);
    window.print();
  };
  // The card prints the saved card.
  const card = sf.draft;
  const name = card.name || form.text.name;
  const where = [card.address, card.area].filter(Boolean).join(", ");
  return (
    <section className="card" aria-labelledby="sf-share">
      <h2 id="sf-share">Share</h2>
      <p className="hint">Customers scan the code to open your page. Print the card for your counter or door, or share the picture on WhatsApp.</p>
      <ErrorBox error={error} />
      <div className="sf-share">
        {qr ? <img className="sf-qr" src={qr} alt={`QR code for ${pageURL(sf)}`} width={180} height={180} /> : !error && <Skeleton rows={1} label="Loading the QR code…" />}
        <div className="toolbar">
          {/* A plain link carries no agent session header: an agent prints the card instead. */}
          {!currentAgentSession() && (
            <a className="button" href="/v1/storefront/qr?format=png" download={`${sf.slug}-qr.png`}>
              Download PNG
            </a>
          )}
          <button disabled={!qr} onClick={print}>
            Print card
          </button>
        </div>
      </div>
      {qr &&
        createPortal(
          <PrintCard>
            <div className="print-name">{name}</div>
            {card.tagline && <div className="print-tagline">{card.tagline}</div>}
            <img src={qr} alt="" className="print-qr" />
            <div className="print-scan">Scan to order</div>
            <div className="print-url">{pageURL(sf).replace(/^https:\/\//, "")}</div>
            {where && <div className="print-where">{where}</div>}
            {card.phone && <div className="print-phone">{card.phone}</div>}
          </PrintCard>,
          document.body,
        )}
    </section>
  );
}

/** A blob as a data: URL. */
function dataURL(b: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () => reject(r.error ?? new Error("The QR code could not be read."));
    r.readAsDataURL(b);
  });
}

/** A stored photo's thumbnail, or a blank tile when there is none or it cannot be shown. */
function Thumb({ src }: { src?: string }) {
  const [failed, setFailed] = useState(false);
  useEffect(() => setFailed(false), [src]);
  if (!src || failed) return <span className="sf-noimg" aria-hidden="true" />;
  return <img src={src} alt="" width={48} height={48} loading="lazy" onError={() => setFailed(true)} />;
}

function PrintCard({ children }: { children: ReactNode }) {
  return (
    <div className="print-card" aria-hidden="true">
      {children}
    </div>
  );
}

function CloseDialog({ onClose, onClosed }: { onClose: () => void; onClosed: (sf: SF) => void }) {
  const [message, setMessage] = useState("");
  const act = useAction();
  const over = message.length > LIMITS.closed;
  return (
    <Modal title="Close for now" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => onClosed(await tidy(post<SF>("/v1/storefront/close", { message: message.trim() }))));
        }}
      >
        <p className="hint">Your page keeps showing your card, with this message instead of the catalogue, until you reopen it.</p>
        <Field label="Message for customers" hint={<span className={over ? "error-text" : undefined}>{`No links. ${message.length}/${LIMITS.closed}`}</span>}>
          <textarea value={message} rows={3} onChange={(e) => setMessage(e.target.value)} placeholder="Back on Monday. Call us for orders." autoFocus aria-invalid={over} />
        </Field>
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy || over}>
            Close for now
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** The storefront's pages: the page itself, the orders it brings, and its bookings. */
export function StorefrontTabs() {
  // A field agent never sees customers' orders or bookings (docs/agents.md).
  if (currentAgentSession()) return null;
  return (
    <nav className="tabs" aria-label="Storefront">
      <NavLink to="/storefront" end>
        Page
      </NavLink>
      <NavLink to="/storefront/orders">Orders</NavLink>
      <NavLink to="/storefront/bookings">Bookings</NavLink>
    </nav>
  );
}
