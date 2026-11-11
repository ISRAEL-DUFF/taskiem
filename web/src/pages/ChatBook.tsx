import { useRef, useState } from "react";
import { ApiError, get, postOnce } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { KitEmpty, KitField, KitLoading, SavedTick, Sheet } from "../kit";
import {
  dayLabel,
  formatDuration,
  lagosTime,
  servicePrice,
  whenLabel,
} from "../lib/bookings";
import {
  bookPayWords,
  linkServices,
  type LinkBooking,
  type LinkService,
  type LinkTimes,
} from "../lib/linkbook";
import { ErrorBox, useLoad } from "../ui";
import { initialsOf } from "./ChatAvatar";

// Book in a business's conversation (docs/spec/business-chat-tools.md §3):
// their services, a day and a time from their availability, and a booking
// through their storefront's booking. Pick a service, then a time, then
// book.

const serviceLine = (s: LinkService) =>
  [formatDuration(s.minutes), servicePrice(s)].filter(Boolean).join(" · ");

export function BookSheet({
  link,
  business,
  from,
  onClose,
  onBooked,
}: {
  link: string;
  business: string;
  from?: DOMRect | null;
  onClose: () => void;
  onBooked?: (words: string) => void;
}) {
  const { can } = useAuth();
  const list = useLoad(() => linkServices(link), [link]);
  const [service, setService] = useState<LinkService>();
  const [day, setDay] = useState<string>();
  const [start, setStart] = useState<string>();
  const [step, setStep] = useState<"service" | "time" | "confirm">("service");
  const times = useLoad(
    () =>
      service
        ? get<LinkTimes>(
            `/v1/link-chats/${encodeURIComponent(link)}/services/${encodeURIComponent(service.id)}/times${day ? `?day=${day}` : ""}`,
          )
        : Promise.resolve(undefined),
    [link, service?.id, day],
  );
  const [note, setNote] = useState("");
  const [phone, setPhone] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [taken, setTaken] = useState(false);
  const [done, setDone] = useState<string>();
  const [shut, setShut] = useState(false);
  // One booking per service and time for the sheet's life: a retry after
  // a dropped connection is the same booking.
  const keys = useRef<Record<string, string>>({});
  const data = list.data;
  const phoneNow = phone ?? data?.phone ?? "";
  const mayBook = can("chat.trade");

  const pickService = (s: LinkService) => {
    setService(s);
    setDay(undefined);
    setStart(undefined);
    setTaken(false);
    setStep("time");
  };
  const book = async () => {
    if (!service || !start) return;
    setBusy(true);
    setError(undefined);
    setTaken(false);
    const what = `${service.id} ${start}`;
    keys.current[what] ??= `book-${crypto.randomUUID()}`;
    try {
      const out = await postOnce<{ booking: LinkBooking }>(
        `/v1/link-chats/${encodeURIComponent(link)}/bookings`,
        keys.current[what],
        {
          service_id: service.id,
          start,
          note: note.trim(),
          phone: phoneNow.trim(),
        },
      );
      const words = `Booked with ${business}: ${out.booking.service_name}, ${whenLabel(out.booking.starts_at)}`;
      setDone(words);
      onBooked?.(words);
      window.setTimeout(() => setShut(true), 1300);
    } catch (e) {
      setBusy(false);
      if (e instanceof ApiError && e.body.code === "slot_taken") {
        // Taken since: fresh times for the day, to choose another.
        setTaken(true);
        setStart(undefined);
        setStep("time");
        times.reload();
        return;
      }
      setError(e);
    }
  };

  const title =
    step === "confirm"
      ? "Check your booking"
      : step === "time" && service
        ? service.name
        : `Book with ${business}`;
  return (
    <Sheet
      title={title}
      from={from}
      onClose={onClose}
      onBack={
        step === "confirm"
          ? () => setStep("time")
          : step === "time"
            ? () => setStep("service")
            : undefined
      }
      crumb={step === "service" ? undefined : `Book with ${business}`}
      shut={shut}
      testid="book-sheet"
      lead={
        step === "service" ? (
          <span
            className="chat-avatar tk-sheet-lead"
            aria-hidden="true"
            data-initials={initialsOf(business)}
            data-tone="pine"
          />
        ) : undefined
      }
      sub={
        step === "service"
          ? "Their services, as their storefront shows them"
          : step === "time" && service
            ? serviceLine(service)
            : undefined
      }
      footer={
        step === "time" && start ? (
          <button
            type="button"
            className="tk-cta"
            onClick={() => setStep("confirm")}
          >
            {dayLabel(times.data?.day ?? "")} at {lagosTime(start)}{" "}
            <Icon name="arrow-right" />
          </button>
        ) : step === "confirm" && mayBook ? (
          <button
            type="button"
            className="tk-cta"
            disabled={busy || phoneNow.trim() === ""}
            onClick={() => void book()}
          >
            {busy ? "Booking…" : "Book"}
          </button>
        ) : undefined
      }
    >
      {done && <SavedTick words={done} />}
      {!data ? (
        list.error ? (
          <ErrorBox error={list.error} />
        ) : (
          <KitLoading label="Loading their services…" />
        )
      ) : data.services.length === 0 ? (
        <KitEmpty icon="calendar" title={`${business} has no services to book`}>
          Ask them in the chat.
        </KitEmpty>
      ) : step === "service" ? (
        <div className="tk-sheet-list">
          {!data.open && (
            <p className="tk-form-now tk-book-closed" data-testid="book-closed">
              {data.closed}
            </p>
          )}
          <ul className="tk-stagger">
            {data.services.map((s) => {
              const words = (
                <>
                  <span className="tk-row-tile" aria-hidden="true">
                    <Icon name="calendar" />
                  </span>
                  <span className="tk-row-words">
                    <span className="tk-row-name">{s.name}</span>
                    <span className="tk-row-sub">{serviceLine(s)}</span>
                    {s.description && (
                      <span className="tk-row-sub">{s.description}</span>
                    )}
                  </span>
                </>
              );
              return (
                <li key={s.id} data-testid="book-service">
                  {data.open ? (
                    <button
                      type="button"
                      className="tk-row"
                      onClick={() => pickService(s)}
                    >
                      {words}
                      <Icon name="chevron" />
                    </button>
                  ) : (
                    <div className="tk-row">{words}</div>
                  )}
                </li>
              );
            })}
          </ul>
        </div>
      ) : step === "time" ? (
        <div className="tk-sheet-list tk-book-times">
          {taken && (
            <p className="tk-row-warn" role="alert">
              That time was just taken. Please choose another.
            </p>
          )}
          {!times.data ? (
            times.error ? (
              <ErrorBox error={times.error} />
            ) : (
              <KitLoading label="Loading free times…" />
            )
          ) : times.data.days.length === 0 ? (
            <KitEmpty icon="calendar" title="No free times">
              {business} has no free time for this in the days they take
              bookings. Ask them in the chat.
            </KitEmpty>
          ) : (
            <>
              <h3 className="tk-book-head" id="book-days">
                Day
              </h3>
              <div
                className="tk-chips"
                role="group"
                aria-labelledby="book-days"
              >
                {times.data.days.map((d) => (
                  <button
                    key={d}
                    type="button"
                    aria-pressed={d === times.data?.day}
                    onClick={() => {
                      setDay(d);
                      setStart(undefined);
                    }}
                  >
                    {dayLabel(d)}
                  </button>
                ))}
              </div>
              <h3 className="tk-book-head" id="book-times">
                Time
              </h3>
              {times.data.times.length === 0 ? (
                <p className="tk-form-now">
                  No free time that day. Please choose another.
                </p>
              ) : (
                <div
                  className="tk-chips tk-times"
                  role="group"
                  aria-labelledby="book-times"
                >
                  {times.data.times.map((t) => (
                    <button
                      key={t}
                      type="button"
                      aria-pressed={t === start}
                      onClick={() => setStart(t)}
                    >
                      {lagosTime(t)}
                    </button>
                  ))}
                </div>
              )}
            </>
          )}
        </div>
      ) : (
        service &&
        start && (
          <div className="tk-sheet-list tk-form-body">
            <ul className="tk-lines">
              <li>
                <span>{service.name}</span>
                <b>{servicePrice(service)}</b>
              </li>
              <li>
                <span>When</span>
                <b>{whenLabel(start)}</b>
              </li>
              <li>
                <span>How long</span>
                <b>{formatDuration(service.minutes)}</b>
              </li>
            </ul>
            <p className="tk-form-now">{bookPayWords(business)}</p>
            {mayBook ? (
              <>
                <KitField
                  label="Phone number they can reach you on"
                  hint={`Given to ${business} with the booking.`}
                >
                  <input
                    type="tel"
                    inputMode="tel"
                    autoComplete="tel"
                    value={phoneNow}
                    onChange={(e) => setPhone(e.target.value)}
                    placeholder="0803 123 4567"
                  />
                </KitField>
                <KitField label="Note for them" hint="Optional">
                  <textarea
                    value={note}
                    maxLength={500}
                    rows={2}
                    onChange={(e) => setNote(e.target.value)}
                  />
                </KitField>
              </>
            ) : (
              <p className="tk-form-now">
                Your role cannot book with other businesses. Ask the owner.
              </p>
            )}
            <ErrorBox error={error} />
          </div>
        )
      )}
    </Sheet>
  );
}
