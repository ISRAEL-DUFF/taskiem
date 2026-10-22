// Storefront trust and safety for operators (docs/moderation.md): the
// review queue (first publishes, flagged changes, reports, appeals and
// business checks), visitors' reports, and suspending or restoring a page.
// Every decision asks for the operator's passkey; the owner is told.

import { Fragment, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { formatPrice, type Card } from "../lib/storefront";
import {
  DECISION_HELP,
  REPORT_STATUS,
  appealMessage,
  businessDetail,
  categoryLabel,
  fieldLabel,
  findingsSummary,
  pageStatus,
  reportReason,
  reviewKind,
  reviewStatus,
  smallest,
  type ReviewKind,
} from "../lib/trust";
import { actorLabel } from "../lib/ops";
import { ErrorBox, Field, fmtTime, useAction, useLoad } from "../ui";
import { opsGet, withStepUp, type OpsReport, type OpsStorefrontReview, type OpsStorefrontReviewDetail } from "./client";

function Tone({ label, tone }: { label: string; tone: string }) {
  return <span className={`badge sf-status ${tone}`}>{label}</span>;
}

function PageBadge({ status }: { status: string }) {
  const s = pageStatus(status);
  return <Tone label={s.label} tone={s.tone} />;
}

/** Tabs filtering a list by status. */
function StatusTabs<T extends string>({ value, options, onChange, label }: { value: T; options: [T, string][]; onChange: (v: T) => void; label: string }) {
  return (
    <div className="tabs" role="group" aria-label={label}>
      {options.map(([v, l]) => (
        <button key={v} className={value === v ? "active" : ""} aria-pressed={value === v} onClick={() => onChange(v)}>
          {l}
        </button>
      ))}
    </div>
  );
}

type ReviewFilter = "pending" | "approved" | "rejected" | "all";

export function StorefrontReviews() {
  const [status, setStatus] = useState<ReviewFilter>("pending");
  const { data, error } = useLoad(() => opsGet<{ reviews: OpsStorefrontReview[] }>(`/storefront-reviews?status=${status}`), [status]);
  return (
    <>
      <h1>Storefront reviews</h1>
      <p className="hint">
        Pages waiting for a person, oldest first: first publishes, flagged changes and catalogue items, pages reported by several people, appeals and business
        checks. Aim to decide within 4 working hours.
      </p>
      <StatusTabs
        label="Show reviews"
        value={status}
        onChange={setStatus}
        options={[
          ["pending", "Waiting"],
          ["approved", "Approved"],
          ["rejected", "Rejected"],
          ["all", "All"],
        ]}
      />
      <ErrorBox error={error} />
      {data && data.reviews.length === 0 && <div className="empty">{status === "pending" ? "Nothing waits for review." : "No reviews."}</div>}
      {data && data.reviews.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Kind</th>
              <th>Business</th>
              <th>Page</th>
              <th>Verified</th>
              <th>Agent</th>
              <th>Open reports</th>
              <th>Findings</th>
              <th>Asked</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {data.reviews.map((r) => {
              const s = reviewStatus(r.status);
              return (
                <tr key={r.id}>
                  <td>
                    <Link to={`/ops/storefront-reviews/${r.id}`}>{reviewKind(r.kind)}</Link>
                  </td>
                  <td>
                    {r.business}
                    <div className="hint">{r.tenant_id}</div>
                  </td>
                  <td>
                    <code>{r.slug}</code> <PageBadge status={r.page_status} />
                  </td>
                  <td>{r.verified ? "Yes" : "No"}</td>
                  <td>{r.agent_code ? <code>{r.agent_code}</code> : "—"}</td>
                  <td>{r.open_reports}</td>
                  <td>{findingsSummary(r.findings)}</td>
                  <td>{fmtTime(r.created_at)}</td>
                  <td>
                    <Tone label={s.label} tone={s.tone} />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </>
  );
}

const CARD_FIELDS: [keyof Card, string][] = [
  ["name", "Name"],
  ["tagline", "Tagline"],
  ["about", "About"],
  ["area", "Area"],
  ["address", "Address"],
  ["map_query", "Map search"],
  ["phone", "Phone"],
  ["whatsapp", "WhatsApp"],
];

export function StorefrontReview() {
  const { id = "" } = useParams();
  const { data, error, reload } = useLoad(() => opsGet<OpsStorefrontReviewDetail>(`/storefront-reviews/${id}`), [id]);
  const [note, setNote] = useState("");
  const [rejectPhotos, setRejectPhotos] = useState<Record<string, boolean>>({});
  const [done, setDone] = useState("");
  const act = useAction();
  if (error) return <ErrorBox error={error} />;
  if (!data) return <div className="empty">Loading…</div>;
  const r = data.review;
  const kind = r.kind as ReviewKind;
  const help = DECISION_HELP[kind];
  const photos = Object.entries(rejectPhotos)
    .filter(([, v]) => v)
    .map(([k]) => k);
  const decide = (decision: "approve" | "reject") =>
    act.run(async () => {
      const out = await withStepUp<{ status: string }>("ops.storefront.review", r.id, `/storefront-reviews/${r.id}/${decision}`, {
        note: note.trim(),
        reject_photos: photos,
      });
      setDone(out.status === "approved" ? "Approved. The owner is told." : "Rejected. The owner is told, with your note.");
      setNote("");
      setRejectPhotos({});
      reload();
    }, "Decision recorded");
  const card: Partial<Card> = data.published?.card ?? data.storefront.draft;
  const business = businessDetail(r.detail);
  const appeal = kind === "appeal" ? appealMessage(r.detail) : null;
  const findings = r.findings ?? [];
  const s = reviewStatus(r.status);
  return (
    <>
      <p className="hint">
        <Link to="/ops/storefront-reviews">Storefront reviews</Link>
      </p>
      <h1>
        {reviewKind(r.kind)}: {r.business}
      </h1>
      <div className="toolbar">
        <Tone label={s.label} tone={s.tone} />
        <span className="hint">
          Page <code>{r.slug}</code> <PageBadge status={r.page_status} /> · asked {fmtTime(r.created_at)} by <code>{actorLabel(r.created_by)}</code> · tenant{" "}
          <code>{r.tenant_id}</code>
        </span>
      </div>
      {done && (
        <div className="notice" role="status">
          {done}
        </div>
      )}

      {business && (
        <div className="card">
          <h2>Business to verify</h2>
          <dl className="kv">
            <dt>Legal name</dt>
            <dd>{business.legal_name}</dd>
            <dt>Address</dt>
            <dd>{business.address}</dd>
            <dt>CAC number</dt>
            <dd>{business.cac_number || "None given"}</dd>
            <dt>Note</dt>
            <dd>{business.note || "—"}</dd>
          </dl>
        </div>
      )}
      {appeal !== null && (
        <div className="card">
          <h2>The owner's appeal</h2>
          <p style={{ whiteSpace: "pre-wrap" }}>{appeal}</p>
          {data.storefront.suspended_reason && <p className="hint">Suspended because: {data.storefront.suspended_reason}</p>}
          {data.storefront.review_note && <p className="hint">Turned down because: {data.storefront.review_note}</p>}
        </div>
      )}

      {r.status === "pending" ? (
        <div className="card">
          <h2>Decision</h2>
          {help && (
            <p className="hint">
              Approve: {help[0]} Reject: {help[1]}
            </p>
          )}
          <Field label="Note to the owner" hint="Needed to reject: the owner is told it on WhatsApp or Telegram.">
            <textarea rows={3} value={note} onChange={(e) => setNote(e.target.value)} maxLength={1000} />
          </Field>
          {photos.length > 0 && <p className="hint">{photos.length === 1 ? "1 photo" : `${photos.length} photos`} will be rejected, whatever you decide.</p>}
          <ErrorBox error={act.error} />
          <div className="toolbar">
            <button className="primary" disabled={act.busy} onClick={() => void decide("approve")}>
              Approve with passkey
            </button>
            <button className="danger" disabled={act.busy || !note.trim()} title={note.trim() ? undefined : "Write a note first"} onClick={() => void decide("reject")}>
              Reject with passkey
            </button>
          </div>
        </div>
      ) : (
        <div className="card">
          <h2>Decision</h2>
          <p>
            {s.label} by <strong>{r.decided_by ? actorLabel(r.decided_by) : "—"}</strong> {fmtTime(r.decided_at)}
            {r.decision_note ? `: ${r.decision_note}` : "."}
          </p>
        </div>
      )}

      <div className="card">
        <h2>What the checks found</h2>
        {r.kind === "first_publish" && r.detail && (
          <p className="hint">
            {r.detail.verified ? "The business is verified." : "The business is not verified, so a first publish waits for a person."}
            {r.detail.model_skipped ? " The model could not be asked: only the word list and prices were checked." : ""}
          </p>
        )}
        {findings.length === 0 ? (
          <p className="hint">Nothing was flagged.</p>
        ) : (
          <table aria-label="Findings">
            <thead>
              <tr>
                <th>Where</th>
                <th>Text</th>
                <th>Verdict</th>
                <th>Category</th>
                <th>Why</th>
                <th>Checker</th>
              </tr>
            </thead>
            <tbody>
              {findings.map((f, i) => (
                <tr key={i}>
                  <td>{fieldLabel(f.field)}</td>
                  <td>{f.text}</td>
                  <td>
                    <Tone label={f.verdict} tone={f.verdict === "blocked" ? "danger" : f.verdict === "flagged" ? "warn" : "ok"} />
                  </td>
                  <td>{categoryLabel(f.category)}</td>
                  <td>{f.reason}</td>
                  <td>{f.checker}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card">
        <h2>{data.published ? `The page as published (version ${data.published.version})` : "The page's draft"}</h2>
        <dl className="kv">
          {CARD_FIELDS.map(([k, label]) => {
            const v = card[k];
            return typeof v === "string" && v ? (
              <Fragment key={k}>
                <dt>{label}</dt>
                <dd style={{ whiteSpace: "pre-wrap" }}>{v}</dd>
              </Fragment>
            ) : null;
          })}
          {card.hours?.note && (
            <>
              <dt>Note on the hours</dt>
              <dd>{card.hours.note}</dd>
            </>
          )}
        </dl>
      </div>

      <div className="card">
        <h2>Catalogue</h2>
        {data.items.length === 0 ? (
          <p className="hint">No items.</p>
        ) : (
          <table aria-label="Catalogue items">
            <thead>
              <tr>
                <th>Item</th>
                <th>Category</th>
                <th>Price</th>
                <th>Description</th>
              </tr>
            </thead>
            <tbody>
              {data.items.map((it) => (
                <tr key={it.id}>
                  <td>
                    {it.name}
                    <div className="hint">{it.id}</div>
                  </td>
                  <td>{it.category ?? ""}</td>
                  <td>{it.price_kobo !== undefined && it.price_kobo !== null ? formatPrice(it.price_kobo) : "—"}</td>
                  <td>{it.description ?? ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card">
        <h2>Photos</h2>
        <p className="hint">The model does not judge photos: look at each. Approving a page approves its photos; tick any to reject.</p>
        {data.photos.length === 0 ? (
          <p className="hint">No photos.</p>
        ) : (
          <ul className="ops-photos">
            {data.photos.map((p, i) => (
              <li key={p.id}>
                <Photo url={smallest(p.urls)} review={r.id} n={i + 1} />
                <Tone label={p.moderation} tone={p.moderation === "approved" ? "ok" : p.moderation === "rejected" ? "danger" : "warn"} />
                {r.status === "pending" && p.moderation !== "rejected" && (
                  <label className="inline">
                    <input type="checkbox" checked={!!rejectPhotos[p.id]} onChange={(e) => setRejectPhotos({ ...rejectPhotos, [p.id]: e.target.checked })} />
                    Reject<span className="sr-only"> photo {i + 1}</span>
                  </label>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="card">
        <h2>Reports</h2>
        {data.reports.length === 0 ? (
          <p className="hint">No one has reported this page.</p>
        ) : (
          <table aria-label="Reports on this page">
            <thead>
              <tr>
                <th>When</th>
                <th>Reason</th>
                <th>Message</th>
                <th>Contact left</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody>
              {data.reports.map((rep) => (
                <tr key={rep.id}>
                  <td>{fmtTime(rep.created_at)}</td>
                  <td>{reportReason(rep.reason)}</td>
                  <td>{rep.message || "—"}</td>
                  <td>{rep.has_contact ? "Yes" : "No"}</td>
                  <td>{REPORT_STATUS[rep.status] ?? rep.status}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <p className="hint">
          Act on single reports, or reveal a reporter's contact, under <Link className="inline-link" to="/ops/reports">Reports</Link>.
        </p>
      </div>

      <div className="card">
        <h2>Verification</h2>
        <dl className="kv">
          <dt>Phone</dt>
          <dd>{data.verification.phone ? "Verified" : "Not verified"}</dd>
          <dt>Business</dt>
          <dd>{data.verification.business ? "Verified" : "Not verified"}</dd>
          <dt>Field agent</dt>
          <dd>{data.verification.agent_onboarded ? `Onboarded${r.agent_code ? ` by ${r.agent_code}` : ""}` : "No"}</dd>
        </dl>
      </div>

      <PageAction tenant={r.tenant_id} business={r.business} status={data.storefront.status} onDone={reload} />
    </>
  );
}

/** A photo's thumbnail, through the review's image proxy (the console's
 * Content-Security-Policy allows images from its own origin only); a link
 * opens the original too. */
function Photo({ url, review, n }: { url?: string; review: string; n: number }) {
  const [failed, setFailed] = useState(false);
  if (!url) return <span className="hint">No URL</span>;
  const src = `/v1/ops/storefront-reviews/${encodeURIComponent(review)}/image?src=${encodeURIComponent(url)}`;
  return (
    <a href={url} target="_blank" rel="noreferrer">
      {failed ? `Open photo ${n}` : <img src={src} alt={`Photo ${n}`} width={120} height={120} loading="lazy" onError={() => setFailed(true)} />}
    </a>
  );
}

/** Suspends a live page, or restores a suspended one, by hand. */
export function PageAction({ tenant, business, status, onDone }: { tenant: string; business: string; status?: string; onDone: () => void }) {
  const [reason, setReason] = useState("");
  const [done, setDone] = useState("");
  const act = useAction();
  const suspended = status === "suspended";
  const action = suspended ? "restore" : "suspend";
  const run = () =>
    act.run(async () => {
      if (!suspended && !confirm(`Suspend ${business}'s page? Customers see "This page is unavailable" until it is restored.`)) return;
      await withStepUp(`ops.storefront.${action}`, tenant, `/storefronts/${tenant}/${action}`, { reason: reason.trim() });
      setReason("");
      setDone(suspended ? "Restored. The owner is told." : "Suspended. The owner is told, with your reason.");
      onDone();
    }, "Page updated");
  return (
    <div className="card">
      <h2>{suspended ? "Restore the page" : "Suspend the page"}</h2>
      <p className="hint">
        {suspended
          ? "Puts the page back on the web. Use a review's decision instead when one waits."
          : "Hides the page on every address and stops its forms. Only a live page can be suspended."}
      </p>
      {done && (
        <div className="notice" role="status">
          {done}
        </div>
      )}
      <Field label={suspended ? "Reason (optional, for the audit)" : "Reason (the owner is told)"}>
        <input value={reason} onChange={(e) => setReason(e.target.value)} maxLength={500} />
      </Field>
      <ErrorBox error={act.error} />
      <button className={suspended ? "" : "danger"} disabled={act.busy || (!suspended && !reason.trim())} onClick={() => void run()}>
        {suspended ? "Restore with passkey" : "Suspend with passkey"}
      </button>
    </div>
  );
}

type ReportFilter = "open" | "dismissed" | "actioned" | "all";

export function Reports() {
  const [status, setStatus] = useState<ReportFilter>("open");
  const { data, error, reload } = useLoad(() => opsGet<{ reports: OpsReport[] }>(`/reports?status=${status}`), [status]);
  const [contacts, setContacts] = useState<Record<string, string>>({});
  const [open, setOpen] = useState<string | null>(null);
  const act = useAction();
  const run = (rep: OpsReport, action: "dismiss" | "action" | "contact") =>
    act.run(async () => {
      const out = await withStepUp<{ contact?: string }>(`ops.report.${action}`, rep.id, `/reports/${rep.id}/${action}`, {});
      if (action === "contact") setContacts({ ...contacts, [rep.id]: out.contact ?? "" });
      else reload();
    }, "Report updated");
  return (
    <>
      <h1>Reports</h1>
      <p className="hint">
        Visitors' reports from every page's and hosted form's footer. Three different people's open reports in 30 days suspend a page pending review. Reveal a
        reporter's contact only to ask them more: it is audited, and shown once.
      </p>
      <StatusTabs
        label="Show reports"
        value={status}
        onChange={setStatus}
        options={[
          ["open", "Open"],
          ["dismissed", "Dismissed"],
          ["actioned", "Acted on"],
          ["all", "All"],
        ]}
      />
      <ErrorBox error={error ?? act.error} />
      {data && data.reports.length === 0 && <div className="empty">{status === "open" ? "No open reports." : "No reports."}</div>}
      {data && data.reports.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Business</th>
              <th>Reason</th>
              <th>Message</th>
              <th>Reporters on site</th>
              <th>Agent</th>
              <th>When</th>
              <th>Status</th>
              <th>
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {data.reports.map((rep) => (
              <Fragment key={rep.id}>
                <tr>
                  <td>
                    {rep.business}
                    <div className="hint">
                      {rep.kind === "form" ? "Hosted form" : "Page"}
                      {rep.slug && (
                        <>
                          {" "}
                          <code>{rep.slug}</code>
                        </>
                      )}
                    </div>
                  </td>
                  <td>{reportReason(rep.reason)}</td>
                  <td style={{ whiteSpace: "pre-wrap" }}>{rep.message || "—"}</td>
                  <td>{rep.reporters_on_site}</td>
                  <td>{rep.agent_code ? <code>{rep.agent_code}</code> : "—"}</td>
                  <td>{fmtTime(rep.created_at)}</td>
                  <td>
                    {REPORT_STATUS[rep.status] ?? rep.status}
                    {rep.handled_by && <div className="hint">by {actorLabel(rep.handled_by)}</div>}
                  </td>
                  <td style={{ whiteSpace: "nowrap" }}>
                    {rep.status === "open" && (
                      <>
                        <button disabled={act.busy} onClick={() => void run(rep, "dismiss")}>
                          Dismiss
                        </button>{" "}
                        <button disabled={act.busy} onClick={() => void run(rep, "action")}>
                          Mark acted on
                        </button>{" "}
                      </>
                    )}
                    {rep.has_contact &&
                      (contacts[rep.id] !== undefined ? (
                        <span>
                          Contact: <strong>{contacts[rep.id]}</strong>
                        </span>
                      ) : (
                        <button disabled={act.busy} onClick={() => void run(rep, "contact")}>
                          Reveal contact
                        </button>
                      ))}{" "}
                    <button aria-expanded={open === rep.id} onClick={() => setOpen(open === rep.id ? null : rep.id)}>
                      Suspend or restore<span className="sr-only"> {rep.business}'s page</span>
                    </button>
                  </td>
                </tr>
                {open === rep.id && (
                  <tr>
                    <td colSpan={8}>
                      <SuspendRestore tenant={rep.tenant_id} business={rep.business} />
                    </td>
                  </tr>
                )}
              </Fragment>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}

/** Suspend or restore from a report row: the page's status is not in the list, so both are offered. */
function SuspendRestore({ tenant, business }: { tenant: string; business: string }) {
  const [restore, setRestore] = useState(false);
  return (
    <>
      <label className="inline" style={{ marginBottom: 8 }}>
        <input type="checkbox" checked={restore} onChange={(e) => setRestore(e.target.checked)} />
        The page is suspended: restore it
      </label>
      <PageAction tenant={tenant} business={business} status={restore ? "suspended" : "published"} onDone={() => undefined} />
    </>
  );
}
