import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { ApiError, get, post, put } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { EmptyState, ErrorBox, Field, Modal, PageHeader, Skeleton, fmtTime, useAction, useLiveRefresh, useLoad } from "../ui";
import type { Table } from "../lib/tables";
import { DAYS, MAX_OPENINGS, columnsFor, thumb, tidy, type Storefront as SF } from "../lib/storefront";
import { customerLine, orderStatus, type Order } from "../lib/orders";
import {
  AVAILABILITY_DEFAULTS,
  BOOKING_RANGES,
  SERVICE_MAPPING,
  addDays,
  availabilityErrors,
  availabilityFromForm,
  availabilityToForm,
  bookingsPath,
  canChange,
  cleanServiceColumns,
  dayLabel,
  groupByDay,
  guessServiceColumns,
  lagosDay,
  lagosInstant,
  lagosTime,
  moveProblem,
  serviceLine,
  servicesProblem,
  servicesTables,
  timeSpan,
  upcomingOff,
  whenLabel,
  type AvailabilityForm,
  type BookingDay,
  type Notice,
  type ServiceColumns,
  type Services,
} from "../lib/bookings";
import { StorefrontTabs } from "./Storefront";

const header = (
  <PageHeader
    title="Bookings"
    description="Appointments customers book on your storefront, and the services and hours they book from. Times are in Lagos time."
  />
);

/** The storefront's bookings: the upcoming list, and the services table and availability they come from. */
export function StorefrontBookings() {
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
  return (
    <>
      {header}
      <StorefrontTabs />
      <ErrorBox error={sf.error} />
      {sf.data === undefined && !sf.error && <Skeleton rows={5} label="Loading your storefront…" />}
      {sf.data === null && (
        <EmptyState icon="storefront">
          <span className="sf-links">
            You have no storefront yet. <Link to="/storefront">Create your storefront</Link> first, then set up bookings here.
          </span>
        </EmptyState>
      )}
      {sf.data && (
        <>
          <BookingList />
          <BookingSettings initial={sf.data} tables={tables.data?.tables} tablesError={tables.error} />
        </>
      )}
    </>
  );
}

// --- the list ---

function StatusBadge({ status }: { status: string }) {
  const s = orderStatus(status);
  return <span className={`badge sf-status ${s.tone}`}>{s.label}</span>;
}

function BookingList() {
  const [params, setParams] = useSearchParams();
  const range = params.get("range") === "week" ? "week" : "month";
  const list = useLoad(
    () =>
      get<{ bookings: Order[] }>(bookingsPath(range)).catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }),
    [range],
  );
  useLiveRefresh(list.reload);
  const [notice, setNotice] = useState("");
  const groups = list.data ? groupByDay(list.data.bookings) : undefined;
  return (
    <section className="card" aria-labelledby="sf-upcoming">
      <h2 id="sf-upcoming">Upcoming bookings</h2>
      <p className="hint sf-links">
        You confirm each booking in the approval request Taskiem sends you in WhatsApp or Telegram, or on the <Link to="/approvals">Approvals</Link> page. Open a
        booking to see the customer's details.
      </p>
      <div className="filters" role="search" aria-label="Filter bookings">
        <label>
          Show{" "}
          <select value={range} onChange={(e) => setParams(e.target.value === "month" ? {} : { range: e.target.value }, { replace: true })}>
            {BOOKING_RANGES.map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
          </select>
        </label>
        <button type="button" onClick={list.reload}>
          Refresh
        </button>
      </div>
      {notice && (
        <div className="notice" role="status">
          {notice}
        </div>
      )}
      <ErrorBox error={list.error} />
      {list.data === undefined && !list.error && <Skeleton rows={4} label="Loading bookings…" />}
      {list.data === null && <EmptyState icon="storefront">This Taskiem does not take storefront bookings yet.</EmptyState>}
      {groups && groups.length === 0 && <EmptyState icon="storefront">No bookings {range === "week" ? "this week" : "in the next 30 days"}.</EmptyState>}
      {groups?.map((g) => (
        <div key={g.day} className="sf-booking-day">
          <h3 id={`sf-day-${g.day}`}>{g.label}</h3>
          <table className="cards" aria-labelledby={`sf-day-${g.day}`}>
            <thead>
              <tr>
                <th scope="col">Time</th>
                <th scope="col">Service</th>
                <th scope="col">Customer</th>
                <th scope="col">Status</th>
                <th scope="col">Reference</th>
                <th scope="col">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {g.bookings.map((b) => (
                <tr key={b.id} data-testid="booking">
                  <td className="primary" data-label="Time">
                    {timeSpan(b)}
                  </td>
                  <td data-label="Service">{b.service_name || <span className="hint">—</span>}</td>
                  <td data-label="Customer">{customerLine(b.customer)}</td>
                  <td data-label="Status">
                    <StatusBadge status={b.status} />
                  </td>
                  <td data-label="Reference">
                    <Link to={`/storefront/orders/${encodeURIComponent(b.reference)}`}>{b.reference}</Link>
                  </td>
                  <td data-label="Actions">
                    <BookingActions
                      booking={b}
                      onChanged={(msg) => {
                        setNotice(msg);
                        list.reload();
                      }}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ))}
    </section>
  );
}

/** Cancel and Move for a booking that still holds its time
 * (POST /v1/storefront/bookings/{reference}/cancel and /move). The run
 * moves on and the customer is told. */
export function BookingActions({ booking, onChanged }: { booking: Order; onChanged: (message: string, b: Order) => void }) {
  const [open, setOpen] = useState<"cancel" | "move" | null>(null);
  if (!canChange(booking)) return null;
  const what = `${booking.service_name ?? "booking"} ${booking.reference}`;
  return (
    <div className="sf-row-actions">
      <button type="button" onClick={() => setOpen("move")}>
        Move<span className="sr-only"> {what}</span>
      </button>
      <button type="button" className="danger" onClick={() => setOpen("cancel")}>
        Cancel<span className="sr-only"> {what}</span>
      </button>
      {open === "cancel" && (
        <CancelDialog
          booking={booking}
          onClose={() => setOpen(null)}
          onDone={(b) => {
            setOpen(null);
            onChanged(b.status === "refund_due" ? `${b.reference} is cancelled. They had paid: refund them in Paystack.` : `${b.reference} is cancelled; the customer is told.`, b);
          }}
        />
      )}
      {open === "move" && (
        <MoveDialog
          booking={booking}
          onClose={() => setOpen(null)}
          onDone={(b) => {
            setOpen(null);
            onChanged(`${b.reference} is moved to ${b.starts_at ? whenLabel(b.starts_at) : "its new time"}; the customer is told.`, b);
          }}
        />
      )}
    </div>
  );
}

function CancelDialog({ booking, onClose, onDone }: { booking: Order; onClose: () => void; onDone: (b: Order) => void }) {
  const act = useAction();
  return (
    <Modal title={`Cancel booking ${booking.reference}?`} onClose={onClose}>
      <p>
        {booking.service_name}
        {booking.starts_at && `, ${whenLabel(booking.starts_at)}`}. The time is freed and the customer is texted that you cancelled.
        {booking.paid_at && " They have paid, so the booking shows as Refund due until you refund them in Paystack."}
      </p>
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button
          type="button"
          className="danger"
          disabled={act.busy}
          onClick={() => void act.run(async () => onDone((await tidy(post<{ booking: Order }>(`/v1/storefront/bookings/${encodeURIComponent(booking.reference)}/cancel`))).booking))}
        >
          {act.busy ? "Cancelling…" : "Cancel the booking"}
        </button>
        <button type="button" onClick={onClose} autoFocus>
          Keep it
        </button>
      </div>
    </Modal>
  );
}

function MoveDialog({ booking, onClose, onDone }: { booking: Order; onClose: () => void; onDone: (b: Order) => void }) {
  const [day, setDay] = useState(booking.starts_at ? lagosDay(booking.starts_at) : lagosDay(Date.now()));
  const [time, setTime] = useState(booking.starts_at ? lagosTime(booking.starts_at) : "");
  const act = useAction();
  const problem = moveProblem(day, time);
  const same = !!booking.starts_at && lagosInstant(day, time) === lagosInstant(lagosDay(booking.starts_at), lagosTime(booking.starts_at));
  return (
    <Modal title={`Move booking ${booking.reference}`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (problem || same) return;
          void act.run(async () =>
            onDone(
              (await tidy(post<{ booking: Order }>(`/v1/storefront/bookings/${encodeURIComponent(booking.reference)}/move`, { starts_at: lagosInstant(day, time) })))
                .booking,
            ),
          );
        }}
      >
        <p className="hint">
          {booking.service_name}
          {booking.starts_at && `, now ${whenLabel(booking.starts_at)}`}. Choose a free time in Lagos time; the customer is texted the new one.
        </p>
        <div className="row">
          <Field label="New date">
            <input type="date" value={day} min={lagosDay(Date.now())} onChange={(e) => setDay(e.target.value)} required />
          </Field>
          <Field label="New time">
            <input type="time" value={time} step={300} onChange={(e) => setTime(e.target.value)} required />
          </Field>
        </div>
        {day && /^\d{4}-\d{2}-\d{2}$/.test(day) && <p className="hint">{dayLabel(day)}</p>}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy || !!problem || same}>
            {act.busy ? "Moving…" : "Move"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

// --- the settings ---

interface SettingsForm {
  table: string | null;
  columns: ServiceColumns;
  availability: AvailabilityForm;
}

function settingsFrom(sf: SF): SettingsForm {
  return {
    table: sf.services_table ?? null,
    columns: { price_unit: "naira", ...(sf.services_columns ?? {}) },
    availability: availabilityToForm(sf.availability, sf.draft.hours),
  };
}

function requestOf(f: SettingsForm) {
  return { services_table: f.table, services_columns: f.table ? cleanServiceColumns(f.columns) : {}, availability: availabilityFromForm(f.availability) };
}

function BookingSettings({ initial, tables, tablesError }: { initial: SF; tables: Table[] | undefined; tablesError: unknown }) {
  const [saved, setSaved] = useState(initial);
  const [form, setForm] = useState<SettingsForm>(() => settingsFrom(initial));
  const [notice, setNotice] = useState("");
  const [tick, setTick] = useState(0);
  const act = useAction();
  const dirty = JSON.stringify(requestOf(form)) !== JSON.stringify(requestOf(settingsFrom(saved)));
  const avProblems = availabilityErrors(form.availability);
  const mapProblem = servicesProblem(form.table, form.columns);
  const published = saved.published_version > 0;

  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const setAv = (patch: Partial<AvailabilityForm>) => {
    setNotice("");
    setForm((f) => ({ ...f, availability: { ...f.availability, ...patch } }));
  };

  const save = () =>
    act.run(async () => {
      setNotice("");
      const out = await tidy(put<SF>("/v1/storefront/bookings", requestOf(form)));
      setSaved(out);
      setForm(settingsFrom(out));
      setTick((n) => n + 1);
      setNotice(published ? "Saved; your page is updated." : "Saved. Bookings show on your page once you publish it and switch on the booking form.");
    });

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <section className="card sf-catalogue" aria-labelledby="sf-services">
        <h2 id="sf-services">Services</h2>
        <ServicesMapping form={form} setForm={setForm} tables={tables} tablesError={tablesError} />
        {mapProblem && <p className="error-text">{mapProblem}</p>}
        {saved.services_table && <ServicesPreview tick={tick} stale={dirty && form.table !== saved.services_table} />}
      </section>

      <section className="card" aria-labelledby="sf-booking-hours">
        <h2 id="sf-booking-hours">Booking hours</h2>
        <AvailabilityEditor value={form.availability} onChange={setAv} />
      </section>

      <section className="card" aria-labelledby="sf-booking-rules">
        <h2 id="sf-booking-rules">Booking rules</h2>
        <WindowEditor value={form.availability} onChange={setAv} />
        {avProblems.map((p) => (
          <div key={`${p.day}-${p.message}`} className="error-text">
            {p.message}
          </div>
        ))}
        {notice && (
          <div className="notice" role="status">
            {notice}
          </div>
        )}
        <ErrorBox error={act.error} />
        <div className="toolbar" style={{ marginBottom: 0 }}>
          <button type="submit" className="primary" disabled={act.busy || !dirty || avProblems.length > 0 || !!mapProblem}>
            {act.busy ? "Saving…" : "Save booking settings"}
          </button>
          {dirty && <span className="hint">Unsaved changes</span>}
        </div>
        <p className="hint sf-links">
          Customers book through the booking form: set it up and switch it on under <Link to="/storefront">Page</Link>, Forms.
        </p>
      </section>
    </form>
  );
}

function ServicesMapping({
  form,
  setForm,
  tables,
  tablesError,
}: {
  form: SettingsForm;
  setForm: (fn: (f: SettingsForm) => SettingsForm) => void;
  tables: Table[] | undefined;
  tablesError: unknown;
}) {
  if (tablesError) {
    return (
      <>
        <p className="hint">Your tables could not be loaded, so the services cannot be changed now.</p>
        <ErrorBox error={tablesError} />
      </>
    );
  }
  if (!tables) return <Skeleton rows={2} label="Loading your tables…" />;
  const usable = servicesTables(tables);
  const table = tables.find((t) => t.id === form.table);
  const setColumns = (patch: ServiceColumns) => setForm((f) => ({ ...f, columns: { ...f.columns, ...patch } }));
  return (
    <>
      <p className="hint">
        What customers book comes from one of your <Link to="/tables">tables</Link>: each row is a service, with how long it takes in minutes. A service with no price
        shows as priced on the day.
      </p>
      <Field label="Services table">
        <select
          value={form.table ?? ""}
          onChange={(e) => {
            const id = e.target.value || null;
            const t = tables.find((x) => x.id === id);
            setForm((f) => ({ ...f, table: id, columns: t ? guessServiceColumns(t) : { price_unit: "naira" } }));
          }}
        >
          <option value="">No bookings</option>
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
          None of your tables can hold services yet: it needs a text column for the name and a number column for the duration in minutes, neither personal data.{" "}
          <Link to="/tables">Create a table</Link> with name, duration and price columns.
        </p>
      )}
      {table && (
        <div className="sf-mapping">
          {SERVICE_MAPPING.map((m) => {
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
          {form.columns.price && (
            <Field label="Prices in the table are in" hint="Naira: 1500 is ₦1,500. Kobo: 150000 is ₦1,500.">
              <select value={form.columns.price_unit || "naira"} onChange={(e) => setColumns({ price_unit: e.target.value as ServiceColumns["price_unit"] })}>
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

/** The services copy the booking form offers (GET /v1/storefront/services). */
function ServicesPreview({ tick, stale }: { tick: number; stale: boolean }) {
  const svc = useLoad(() => get<Services>("/v1/storefront/services"), [tick]);
  const act = useAction();
  const refresh = () =>
    act.run(async () => {
      await tidy(post("/v1/storefront/catalogue/refresh"));
      svc.reload();
    });
  const shown = svc.data;
  return (
    <div className="sf-copy">
      <div className="toolbar" style={{ marginTop: 18 }}>
        <h3 style={{ margin: 0 }}>On your page</h3>
        <span className="grow" />
        <span className="hint">Copied {fmtTime(shown?.synced_at)}</span>
        <button type="button" disabled={act.busy} onClick={() => void refresh()}>
          {act.busy ? "Refreshing…" : "Refresh"}
          <span className="sr-only"> services</span>
        </button>
      </div>
      {stale && <p className="hint">Save your booking settings to use the services table above.</p>}
      <ErrorBox error={svc.error ?? act.error} />
      {shown?.error && (
        <div className="warning" role="status">
          The services could not be read from their table: {shown.error}
        </div>
      )}
      {!shown && !svc.error && <Skeleton rows={3} label="Loading the services…" />}
      {shown && shown.items.length === 0 && <EmptyState icon="tables">No services yet. Add rows to the table, then refresh.</EmptyState>}
      {shown && shown.items.length > 0 && (
        <ul className="sf-items" aria-label="Services">
          {shown.items.map((it) => {
            const src = thumb(it.photo);
            return (
              <li key={it.id} className={it.available ? undefined : "unavailable"}>
                {src ? <img src={src} alt="" width={48} height={48} loading="lazy" /> : <span className="sf-noimg" aria-hidden="true" />}
                <div className="sf-item-text">
                  <strong>{it.name}</strong>
                  {it.category && <span className="hint"> · {it.category}</span>}
                  <div className="hint">{serviceLine(it)}</div>
                </div>
                <div className="sf-item-side">
                  {!it.available && <span className="badge">unavailable</span>}
                  {it.available && !it.duration_minutes && <span className="badge">no duration</span>}
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

function AvailabilityEditor({ value, onChange }: { value: AvailabilityForm; onChange: (p: Partial<AvailabilityForm>) => void }) {
  const [newOff, setNewOff] = useState("");
  const today = lagosDay(Date.now());
  const setDay = (i: number, day: BookingDay) => onChange({ days: value.days.map((d, j) => (j === i ? day : d)) });
  const { upcoming, past } = upcomingOff(value.off, today);
  const noHours = value.sameAsHours && !value.days.some((d) => d.open);
  return (
    <>
      <p className="hint">In Lagos time. Times are offered in steps of each service's duration plus its buffer.</p>
      <label className="sf-toggle">
        <input type="checkbox" role="switch" checked={value.sameAsHours} onChange={(e) => onChange({ sameAsHours: e.target.checked })} />
        <span>
          Same as opening hours
          <span className="hint"> Take bookings whenever your page shows you open (set on the Page tab).</span>
        </span>
      </label>
      {noHours && (
        <div className="warning" role="status">
          Your page has no opening hours, so no times can be booked. Set booking hours here, or opening hours on the <Link to="/storefront">Page</Link> tab.
        </div>
      )}
      <div className="sf-hours" aria-disabled={value.sameAsHours}>
        {value.days.map((d, i) => {
          const day = DAYS[i];
          if (value.sameAsHours) {
            return (
              <div key={day} className="sf-day sf-day-readonly">
                <span className="sf-day-name">{day}</span>
                <span className="hint sf-spans">{d.open ? d.spans.map((s) => `${s.open} to ${s.close}`).join(", ") : "Closed"}</span>
              </div>
            );
          }
          return (
            <fieldset key={day} className="sf-day">
              <legend className="sr-only">{day} bookings</legend>
              <label className="sf-day-name">
                <input type="checkbox" checked={d.open} onChange={(e) => setDay(i, { ...d, open: e.target.checked, spans: d.spans.length ? d.spans : [{ open: "09:00", close: "17:00" }] })} />
                {day}
                <span className="sr-only"> takes bookings</span>
              </label>
              {d.open ? (
                <div className="sf-spans">
                  {d.spans.map((s, n) => {
                    const which = d.spans.length > 1 ? ` (period ${n + 1})` : "";
                    const set = (patch: Partial<typeof s>) => setDay(i, { ...d, spans: d.spans.map((x, m) => (m === n ? { ...x, ...patch } : x)) });
                    return (
                      <div key={n} className="sf-span">
                        <input type="time" aria-label={`${day} bookings from${which}`} value={s.open} onChange={(e) => set({ open: e.target.value })} />
                        <span aria-hidden="true">to</span>
                        <input type="time" aria-label={`${day} bookings until${which}`} value={s.close} onChange={(e) => set({ close: e.target.value })} />
                        {d.spans.length > 1 && (
                          <button type="button" className="icon-button" aria-label={`Remove ${day} period ${n + 1}`} onClick={() => setDay(i, { ...d, spans: d.spans.filter((_, m) => m !== n) })}>
                            <Icon name="close" />
                          </button>
                        )}
                      </div>
                    );
                  })}
                </div>
              ) : (
                <span className="hint sf-spans">No bookings</span>
              )}
              <div className="sf-day-actions">
                {d.open && d.spans.length < MAX_OPENINGS && (
                  <button type="button" aria-label={`Add a booking period on ${day}`} onClick={() => setDay(i, { ...d, spans: [...d.spans, { open: "", close: "" }] })}>
                    <Icon name="plus" />
                    Period
                  </button>
                )}
              </div>
            </fieldset>
          );
        })}
      </div>
      {!value.sameAsHours && <p className="hint">Up to {MAX_OPENINGS} periods a day, such as 09:00 to 13:00 and 14:00 to 18:00 for a lunch break. Each ends the same day.</p>}

      <h3>Days off</h3>
      <p className="hint">Dates with no bookings, such as public holidays.</p>
      {upcoming.length > 0 ? (
        <ul className="sf-days-off">
          {upcoming.map((d) => (
            <li key={d}>
              <span>{dayLabel(d)}</span> <span className="hint">{d.slice(0, 4)}</span>
              <button type="button" className="icon-button" aria-label={`Remove day off ${dayLabel(d)} ${d.slice(0, 4)}`} onClick={() => onChange({ off: value.off.filter((x) => x !== d) })}>
                <Icon name="close" />
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <p className="hint">No days off coming up.</p>
      )}
      {past > 0 && (
        <p className="hint">
          {past === 1 ? "1 past day off is" : `${past} past days off are`} kept.{" "}
          <button type="button" className="link" onClick={() => onChange({ off: value.off.filter((d) => d >= today) })}>
            Remove past days off
          </button>
        </p>
      )}
      <div className="toolbar">
        <Field label="Add a day off">
          <input type="date" value={newOff} min={today} max={addDays(today, 730)} onChange={(e) => setNewOff(e.target.value)} />
        </Field>
        <button
          type="button"
          disabled={!/^\d{4}-\d{2}-\d{2}$/.test(newOff) || value.off.includes(newOff)}
          onClick={() => {
            onChange({ off: [...value.off, newOff].sort() });
            setNewOff("");
          }}
        >
          Add day off
        </button>
      </div>
    </>
  );
}

function NoticeInput({ label, hint, value, defaultMinutes, onChange }: { label: string; hint: string; value: Notice; defaultMinutes: number; onChange: (n: Notice) => void }) {
  const placeholder = value.unit === "hours" ? String(defaultMinutes / 60) : String(defaultMinutes);
  return (
    <fieldset className="sf-choices">
      <legend>{label}</legend>
      <div className="sf-span">
        <input
          type="number"
          min={0}
          step={value.unit === "hours" ? 0.5 : 5}
          inputMode="decimal"
          aria-label={`${label}, amount`}
          value={value.value}
          placeholder={placeholder}
          onChange={(e) => onChange({ ...value, value: e.target.value })}
          style={{ width: "7em" }}
        />
        <select aria-label={`${label}, unit`} value={value.unit} onChange={(e) => onChange({ ...value, unit: e.target.value as Notice["unit"] })} style={{ width: "auto" }}>
          <option value="hours">hours</option>
          <option value="minutes">minutes</option>
        </select>
      </div>
      <p className="hint" style={{ margin: "4px 0 0" }}>
        {hint}
      </p>
    </fieldset>
  );
}

function WindowEditor({ value, onChange }: { value: AvailabilityForm; onChange: (p: Partial<AvailabilityForm>) => void }) {
  return (
    <>
      <div className="row">
        <Field label="How far ahead (days)" hint={`How many days ahead customers may book, up to 180. Empty: ${AVAILABILITY_DEFAULTS.ahead_days}.`}>
          <input type="number" min={1} max={180} step={1} inputMode="numeric" value={value.ahead} placeholder={String(AVAILABILITY_DEFAULTS.ahead_days)} onChange={(e) => onChange({ ahead: e.target.value })} />
        </Field>
      </div>
      <div className="row">
        <NoticeInput
          label="How soon"
          hint="The earliest a booking may start, from now. Empty: 2 hours."
          value={value.soonest}
          defaultMinutes={AVAILABILITY_DEFAULTS.soonest_minutes}
          onChange={(soonest) => onChange({ soonest })}
        />
        <NoticeInput
          label="Change cut-off"
          hint="Customers may move or cancel up to this long before the start. Empty: 2 hours."
          value={value.cutoff}
          defaultMinutes={AVAILABILITY_DEFAULTS.cutoff_minutes}
          onChange={(cutoff) => onChange({ cutoff })}
        />
      </div>
      <label className="sf-toggle">
        <input type="checkbox" role="switch" checked={value.autoConfirm} onChange={(e) => onChange({ autoConfirm: e.target.checked })} />
        <span>
          Confirm bookings automatically
          <span className="hint"> Otherwise each booking waits for you to accept it in WhatsApp or Telegram.</span>
        </span>
      </label>
    </>
  );
}
