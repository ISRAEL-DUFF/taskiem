import { useEffect, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { ApiError, del, get, post, put } from "../api";
import { useAuth } from "../auth";
import { EmptyState, ErrorBox, Field, PageHeader, Skeleton, useAction, useLoad } from "../ui";
import { invoiceStatus, lineNote, naira } from "../lib/invoices";
import { plainMessage, schoolAccess, type SchoolClass, type SchoolSession, type SchoolTerm } from "../lib/schools";
import {
  DISCOUNT_KINDS,
  discountProblem,
  discountRequest,
  discountWords,
  emptyDiscount,
  emptyFeeRow,
  feeRows,
  feeRowsProblem,
  feeRowsRequest,
  feeRowsTotals,
  invoicedLine,
  needsApproval,
  planSummary,
  savedFeesLine,
  termTotals,
  type ClassFees,
  type Discount,
  type DiscountForm,
  type FeeRow,
  type FeesSet,
  type StudentFees,
  type TermInvoice,
  type TermInvoiced,
  type TermPlan,
} from "../lib/schoolFees";

// Fees and term invoices (decision 0046, B3; docs/schools.md#fees): a
// class's fees for a term, invoicing the term (the plan, then exactly once
// per student), the term's invoices, and a student's optional items and
// standing discounts.

/** Runs a request and words the server's refusal as a sentence; a discount needing a second person says so. */
async function plainly<T>(p: Promise<T>): Promise<T> {
  try {
    return await p;
  } catch (e) {
    if (e instanceof ApiError) {
      const why = needsApproval(e.status, e.body);
      if (why) e.message = why;
      else if (e.status === 400 || e.status === 409) e.message = plainMessage(e.message);
    }
    throw e;
  }
}

function useTerm(id: string) {
  return useLoad(async () => {
    const r = await get<{ sessions: SchoolSession[] }>("/v1/schools/sessions");
    for (const s of r.sessions) for (const t of s.terms) if (t.id === id) return t;
    throw new ApiError(404, "There is no such term.");
  }, [id]);
}

function TermLinks({ term }: { term: SchoolTerm }) {
  return (
    <nav className="tabs" aria-label={`${term.name} ${term.session}`}>
      <Link to={`/schools/terms/${term.id}/fees`}>Fees</Link>
      <Link to={`/schools/terms/${term.id}/invoices`}>Invoices</Link>
      <Link to="/schools/classes">Classes and terms</Link>
    </nav>
  );
}

// --- fees ---

/** Schools > a term's fees: each class's items, kinds and pass-through, copied from the last term or another class. */
export function SchoolTermFees() {
  const { id = "" } = useParams();
  const { me, can } = useAuth();
  const may = schoolAccess(can);
  const [params, setParams] = useSearchParams();
  const term = useTerm(id);
  const fees = useLoad(() => get<{ classes: ClassFees[]; previous_term: SchoolTerm | null }>(`/v1/schools/terms/${id}/fees`), [id]);
  // Kept here: a save reloads the fees, which draws the editor afresh.
  const [said, setSaid] = useState("");
  if (!me?.schools) return <PageHeader title="Fees" />;
  const list = fees.data?.classes ?? [];
  const classID = params.get("class") || list[0]?.class_id || "";
  const current = list.find((c) => c.class_id === classID);
  const t = term.data;
  return (
    <>
      <PageHeader
        title={t ? `Fees: ${t.name} ${t.session}` : "Fees"}
        description="Each class's fee items for the term: required for every student, or optional for those who choose them, and who an item is collected for when it is not the school's own money (an exam body's fee)."
      />
      {t && <TermLinks term={t} />}
      <ErrorBox error={term.error ?? fees.error} />
      {!fees.data && !fees.error && <Skeleton rows={4} label="Loading the fees…" />}
      {fees.data && list.length === 0 && <EmptyState icon="schools">Add the school's classes first, on Classes and terms.</EmptyState>}
      {fees.data && list.length > 0 && (
        <>
          <div className="toolbar">
            <label>
              Class{" "}
              <select
                value={classID}
                onChange={(e) => {
                  setSaid("");
                  setParams({ class: e.target.value }, { replace: true });
                }}
              >
                {list.map((c) => (
                  <option key={c.class_id} value={c.class_id}>
                    {c.class} {c.items.length ? `(${naira(c.required_kobo)})` : "(no fees yet)"}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <div role="status">{said && <div className="notice">{said}</div>}</div>
          {current && (
            <FeesEditor
              key={`${current.class_id}:${current.items.map((i) => i.id + i.amount_kobo).join()}`}
              termID={id}
              fees={current}
              classes={list}
              previous={fees.data.previous_term}
              readOnly={!may.manage || t?.state === "past"}
              onSaved={(line) => {
                setSaid(line);
                fees.reload();
              }}
            />
          )}
        </>
      )}
    </>
  );
}

function FeesEditor({
  termID,
  fees,
  classes,
  previous,
  readOnly,
  onSaved,
}: {
  termID: string;
  fees: ClassFees;
  classes: ClassFees[];
  previous: SchoolTerm | null;
  readOnly: boolean;
  onSaved: (line: string) => void;
}) {
  const [rows, setRows] = useState<FeeRow[]>(() => (fees.items.length ? feeRows(fees.items) : [emptyFeeRow()]));
  const [tried, setTried] = useState(false);
  const [from, setFrom] = useState("");
  const act = useAction();
  const problem = feeRowsProblem(rows);
  const totals = feeRowsTotals(rows);
  const set = (i: number, r: Partial<FeeRow>) => setRows(rows.map((x, j) => (j === i ? { ...x, ...r } : x)));
  const copy = (body: Record<string, unknown>) =>
    void act.run(async () => {
      const r = await plainly(post<FeesSet>(`/v1/schools/terms/${termID}/fees/${fees.class_id}/copy`, body));
      onSaved(`Copied. ${savedFeesLine(r)}`);
    }, false);
  return (
    <section className="card" aria-labelledby="fees-title">
      <h2 id="fees-title">{fees.class}</h2>
      <table className="cards">
        <thead>
          <tr>
            <th scope="col">Item</th>
            <th scope="col">Amount (₦)</th>
            <th scope="col">Kind</th>
            <th scope="col">Collected for</th>
            {!readOnly && <th scope="col">Remove</th>}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={i} data-testid="fee-row">
              <td data-label="Item">
                <input aria-label={`Item ${i + 1} name`} value={r.name} maxLength={80} readOnly={readOnly} placeholder="Tuition" onChange={(e) => set(i, { name: e.target.value })} />
              </td>
              <td data-label="Amount (₦)">
                <input aria-label={`Item ${i + 1} amount in naira`} inputMode="decimal" value={r.amount} readOnly={readOnly} placeholder="100,000" onChange={(e) => set(i, { amount: e.target.value })} />
              </td>
              <td data-label="Kind">
                <select aria-label={`Item ${i + 1} kind`} value={r.kind} disabled={readOnly} onChange={(e) => set(i, { kind: e.target.value as FeeRow["kind"] })}>
                  <option value="required">Required</option>
                  <option value="optional">Optional</option>
                </select>
              </td>
              <td data-label="Collected for">
                <input
                  aria-label={`Item ${i + 1} collected on behalf of`}
                  value={r.passThroughFor}
                  maxLength={60}
                  readOnly={readOnly}
                  placeholder="The school"
                  onChange={(e) => set(i, { passThroughFor: e.target.value })}
                />
              </td>
              {!readOnly && (
                <td data-label="Remove">
                  <button type="button" aria-label={`Remove item ${i + 1}`} onClick={() => setRows(rows.length > 1 ? rows.filter((_, j) => j !== i) : [emptyFeeRow()])}>
                    Remove
                  </button>
                </td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
      <p className="hint">
        Every student pays {naira(totals.required)}
        {totals.passThrough ? `, of which ${naira(totals.passThrough)} is collected for others` : ""}
        {totals.optional ? `; optional items come to ${naira(totals.optional)} for a student who takes them all` : ""}. Leave “Collected for” empty for the school's own fees; name the body (WAEC, NECO) for money passed on.
      </p>
      {tried && problem && (
        <div className="error" role="alert">
          {problem}
        </div>
      )}
      <ErrorBox error={act.error} />
      {!readOnly && (
        <div className="toolbar">
          <button type="button" onClick={() => setRows([...rows, emptyFeeRow()])}>
            Add an item
          </button>
          <button
            type="button"
            className="primary"
            disabled={act.busy}
            onClick={() => {
              setTried(true);
              if (problem) return;
              void act.run(async () => {
                const r = await plainly(put<FeesSet>(`/v1/schools/terms/${termID}/fees/${fees.class_id}`, feeRowsRequest(rows)));
                onSaved(savedFeesLine(r));
              }, false);
            }}
          >
            Save {fees.class}'s fees
          </button>
          {previous && (
            <button type="button" disabled={act.busy} onClick={() => copy({})}>
              Copy from {previous.name} {previous.session}
            </button>
          )}
          <label>
            Copy from class{" "}
            <select value={from} onChange={(e) => setFrom(e.target.value)}>
              <option value="">Choose…</option>
              {classes
                .filter((c) => c.class_id !== fees.class_id && c.items.length > 0)
                .map((c) => (
                  <option key={c.class_id} value={c.class_id}>
                    {c.class}
                  </option>
                ))}
            </select>
          </label>
          <button type="button" disabled={act.busy || !from} onClick={() => copy({ from_term: termID, from_class: from })}>
            Copy
          </button>
        </div>
      )}
      {readOnly && <p className="hint">These fees are read-only here: a past term's fees are its history, and changing fees needs school.manage.</p>}
    </section>
  );
}

// --- invoicing the term ---

/** Schools > a term's invoices: Invoice the term (the plan, then confirm, as drafts or sent at once), and the invoices by class and status. */
export function SchoolTermInvoices() {
  const { id = "" } = useParams();
  const { me, can } = useAuth();
  const may = schoolAccess(can);
  const [params, setParams] = useSearchParams();
  const term = useTerm(id);
  const classes = useLoad(() => get<{ classes: SchoolClass[] }>("/v1/schools/classes"), []);
  const filter = { class: params.get("class") ?? "", status: params.get("status") ?? "" };
  const q = new URLSearchParams();
  if (filter.class) q.set("class", filter.class);
  if (filter.status) q.set("status", filter.status);
  const list = useLoad(() => get<{ invoices: TermInvoice[] }>(`/v1/schools/terms/${id}/invoices${q.size ? `?${q}` : ""}`), [id, filter.class, filter.status]);
  const drafts = useLoad(() => get<{ invoices: TermInvoice[] }>(`/v1/schools/terms/${id}/invoices?status=draft`), [id]);
  if (!me?.schools) return <PageHeader title="Term invoices" />;
  const t = term.data;
  const set = (k: string, v: string) => {
    const next = new URLSearchParams(params);
    if (v) next.set(k, v);
    else next.delete(k);
    setParams(next, { replace: true });
  };
  const invoices = list.data?.invoices;
  const totals = termTotals(invoices ?? []);
  return (
    <>
      <PageHeader title={t ? `Invoices: ${t.name} ${t.session}` : "Term invoices"} description="One invoice per student for the term, built from their class's fees, their optional items and discounts." />
      {t && <TermLinks term={t} />}
      <ErrorBox error={term.error} />
      {t && may.manage && t.state === "current" && (
        <InvoiceTheTerm
          term={t}
          drafts={drafts.data?.invoices.length ?? 0}
          onDone={() => {
            list.reload();
            drafts.reload();
          }}
        />
      )}
      {t && t.state === "planned" && <p className="notice">Start the term on Classes and terms first: invoicing it invoices the students enrolled in it.</p>}
      <section className="card" aria-labelledby="term-invoices">
        <h2 id="term-invoices">The term's invoices</h2>
        <form className="filters" aria-label="Filter the term's invoices" onSubmit={(e) => e.preventDefault()}>
          <label>
            Class{" "}
            <select value={filter.class} onChange={(e) => set("class", e.target.value)}>
              <option value="">All classes</option>
              {(classes.data?.classes ?? []).map((c) => (
                <option key={c.id} value={c.id}>
                  {c.label}
                </option>
              ))}
            </select>
          </label>
          <label>
            Status{" "}
            <select value={filter.status} onChange={(e) => set("status", e.target.value)}>
              <option value="">Any</option>
              {(["draft", "sent", "part_paid", "overdue", "paid", "void"] as const).map((s) => (
                <option key={s} value={s}>
                  {invoiceStatus(s).label}
                </option>
              ))}
            </select>
          </label>
        </form>
        <ErrorBox error={list.error} />
        {!invoices && !list.error && <Skeleton rows={3} label="Loading the invoices…" />}
        {invoices && invoices.length === 0 && <p className="hint">No invoices here yet.</p>}
        {invoices && invoices.length > 0 && (
          <>
            <p data-testid="term-totals">
              {invoices.length} {invoices.length === 1 ? "invoice" : "invoices"}: {naira(totals.total)} billed, {naira(totals.paid)} paid, {naira(totals.due)} still due.
            </p>
            <table className="cards">
              <thead>
                <tr>
                  <th scope="col">Invoice</th>
                  <th scope="col">Student</th>
                  <th scope="col">Class</th>
                  <th scope="col">Status</th>
                  <th scope="col">Total</th>
                  <th scope="col">Still due</th>
                </tr>
              </thead>
              <tbody>
                {invoices.map((x) => (
                  <tr key={x.invoice_id} data-testid="term-invoice">
                    <td className="primary" data-label="Invoice">
                      <Link to={`/invoices/${encodeURIComponent(x.number)}`}>{x.number}</Link>
                    </td>
                    <td data-label="Student">
                      <Link to={`/schools/students/${x.student_id}`}>{x.admission_number}</Link> {x.name}
                    </td>
                    <td data-label="Class">{x.class}</td>
                    <td data-label="Status">
                      <span className={`badge sf-status ${invoiceStatus(x.status).tone}`}>{invoiceStatus(x.status).label}</span>
                    </td>
                    <td data-label="Total" className="sf-price">
                      {naira(x.total_kobo)}
                    </td>
                    <td data-label="Still due" className="sf-price">
                      {naira(x.due_kobo)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </>
        )}
      </section>
    </>
  );
}

function InvoiceTheTerm({ term, drafts, onDone }: { term: SchoolTerm; drafts: number; onDone: () => void }) {
  const [plan, setPlan] = useState<TermPlan>();
  const [send, setSend] = useState(false);
  const [done, setDone] = useState("");
  const act = useAction();
  return (
    <section className="card" aria-labelledby="invoice-term">
      <h2 id="invoice-term">Invoice the term</h2>
      <p className="hint">
        Each student enrolled in {term.name} gets one invoice for it, once: running it again later invoices only students added since. Each is the family's, due on {term.fees_due_on}, and numbered
        in the business's invoice series.
      </p>
      <div role="status">{done && <div className="notice" data-testid="invoiced">{done}</div>}</div>
      {!plan && (
        <button
          type="button"
          className="primary"
          disabled={act.busy}
          onClick={() =>
            void act.run(async () => {
              const r = await plainly(post<{ plan: TermPlan }>(`/v1/schools/terms/${term.id}/invoice`, {}));
              setPlan(r.plan);
              setDone("");
            }, false)
          }
        >
          Check what invoicing the term will do
        </button>
      )}
      {plan && (
        <div data-testid="term-plan">
          {planSummary(plan).map((l) => (
            <p key={l}>{l}</p>
          ))}
          {plan.classes.length > 0 && (
            <ul>
              {plan.classes.map((c) => (
                <li key={c.class_id}>
                  {c.class}: {c.students} {c.students === 1 ? "student" : "students"}, {naira(c.total_kobo)}
                </li>
              ))}
            </ul>
          )}
          {plan.no_contact.length > 0 && (
            <details>
              <summary>Students with no guardian to send to</summary>
              <ul>
                {plan.no_contact.map((s) => (
                  <li key={s.id}>
                    <Link to={`/schools/students/${s.id}`}>{s.admission_number}</Link> {s.class}
                  </li>
                ))}
              </ul>
            </details>
          )}
          {plan.to_invoice > 0 && (
            <fieldset className="field">
              <legend>When they are made</legend>
              <label className="row">
                <input type="radio" name="send" checked={!send} onChange={() => setSend(false)} /> Keep them as drafts, to check and send later
              </label>
              <label className="row">
                <input type="radio" name="send" checked={send} onChange={() => setSend(true)} /> Send them now, by SMS and email (each family's credit counts first)
              </label>
            </fieldset>
          )}
          <div className="toolbar">
            {plan.to_invoice > 0 && (
              <button
                type="button"
                className="primary"
                disabled={act.busy}
                onClick={() =>
                  void act.run(async () => {
                    const r = await plainly(post<TermInvoiced>(`/v1/schools/terms/${term.id}/invoice`, { confirm: plan.hash, send }));
                    setDone(invoicedLine(r));
                    setPlan(undefined);
                    onDone();
                  }, false)
                }
              >
                {send ? `Invoice and send ${plan.to_invoice}` : `Invoice ${plan.to_invoice} as drafts`}
              </button>
            )}
            {plan.to_invoice === 0 && drafts > 0 && (
              <button
                type="button"
                className="primary"
                disabled={act.busy}
                onClick={() =>
                  void act.run(async () => {
                    const r = await plainly(post<TermInvoiced>(`/v1/schools/terms/${term.id}/invoice`, { confirm: plan.hash, send: true }));
                    setDone(invoicedLine(r));
                    setPlan(undefined);
                    onDone();
                  }, false)
                }
              >
                Send the {drafts === 1 ? "draft" : `${drafts} drafts`}
              </button>
            )}
            <button type="button" onClick={() => setPlan(undefined)}>
              {plan.to_invoice > 0 ? "Cancel" : "Close"}
            </button>
          </div>
        </div>
      )}
      {!plan && drafts > 0 && <p className="hint">{drafts === 1 ? "1 invoice of the term is a draft" : `${drafts} invoices of the term are drafts`}: check, then send them from here.</p>}
      <ErrorBox error={act.error} />
    </section>
  );
}

// --- a student's fees ---

/** On a student's page: their fees for the current term, the optional items they take, and their standing discounts. */
export function StudentFeesPanel({ studentID }: { studentID: string }) {
  const { can } = useAuth();
  const may = schoolAccess(can);
  const fees = useLoad(async () => {
    try {
      return await get<StudentFees>(`/v1/schools/students/${studentID}/fees`);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) return null; // no term is current
      throw e;
    }
  }, [studentID]);
  const discounts = useLoad(() => get<{ discounts: Discount[]; discount_threshold_kobo: number }>(`/v1/schools/students/${studentID}/discounts`), [studentID]);
  const [chosen, setChosen] = useState<string[]>([]);
  const [said, setSaid] = useState("");
  const act = useAction();
  useEffect(() => setChosen(fees.data?.chosen ?? []), [fees.data]);
  const f = fees.data;
  const optional = (f?.items ?? []).filter((i) => i.kind === "optional");
  const reload = () => {
    fees.reload();
    discounts.reload();
  };
  return (
    <section className="card" aria-labelledby="student-fees">
      <h2 id="student-fees">Fees</h2>
      <div role="status">{said && <div className="notice">{said}</div>}</div>
      <ErrorBox error={fees.error} />
      {fees.data === null && <p className="hint">No term is current: the fees of the current term show here once one is started.</p>}
      {f && (
        <>
          {f.lines.length === 0 ? (
            <p className="hint">Their class has no fees for the current term yet.</p>
          ) : (
            <table>
              <caption className="hint">This term's invoice as the fees make it now</caption>
              <thead>
                <tr>
                  <th scope="col">Item</th>
                  <th scope="col">Amount</th>
                </tr>
              </thead>
              <tbody>
                {f.lines.map((l, i) => (
                  <tr key={i}>
                    <td>
                      {l.description}
                      {lineNote(l) && <div className="hint">{lineNote(l)}</div>}
                    </td>
                    <td className="sf-price">{naira(l.quantity * l.unit_kobo)}</td>
                  </tr>
                ))}
                <tr>
                  <th scope="row">Total</th>
                  <td className="sf-price">{naira(f.total_kobo)}</td>
                </tr>
              </tbody>
            </table>
          )}
          {f.invoice && (
            <p>
              Invoiced for the term: <Link to={`/invoices/${encodeURIComponent(f.invoice.number)}`}>{f.invoice.number}</Link> ({invoiceStatus(f.invoice.status).label.toLowerCase()},{" "}
              {naira(f.invoice.due_kobo)} still due).{f.invoice.status !== "draft" && " A sent invoice changes only by amendment, on the invoice's page."}
            </p>
          )}
          {optional.length > 0 && (
            <fieldset className="field">
              <legend>Optional items this term</legend>
              {optional.map((it) => (
                <label key={it.id} className="row">
                  <input
                    type="checkbox"
                    disabled={!may.manage}
                    checked={chosen.includes(it.id)}
                    onChange={(e) => setChosen(e.target.checked ? [...chosen, it.id] : chosen.filter((c) => c !== it.id))}
                  />{" "}
                  {it.name} ({naira(it.amount_kobo)})
                </label>
              ))}
              {may.manage && (
                <button
                  type="button"
                  disabled={act.busy}
                  onClick={() =>
                    void act.run(async () => {
                      await plainly(put(`/v1/schools/students/${studentID}/options`, { term: f.term_id, items: chosen }));
                      setSaid("Optional items saved.");
                      reload();
                    }, false)
                  }
                >
                  Save optional items
                </button>
              )}
            </fieldset>
          )}
        </>
      )}
      <h3>Standing discounts</h3>
      <ErrorBox error={discounts.error} />
      {(discounts.data?.discounts ?? []).filter((d) => !d.ended_at).length === 0 && <p className="hint">None.</p>}
      <ul>
        {(discounts.data?.discounts ?? [])
          .filter((d) => !d.ended_at)
          .map((d) => (
            <li key={d.id} data-testid="discount">
              {discountWords(d)}: {d.reason}{" "}
              {may.manage && (
                <button
                  type="button"
                  disabled={act.busy}
                  aria-label={`End the ${discountWords(d)} discount`}
                  onClick={() =>
                    void act.run(async () => {
                      await plainly(del(`/v1/schools/students/${studentID}/discounts/${d.id}`));
                      setSaid("Discount ended: invoices already sent keep it.");
                      reload();
                    }, false)
                  }
                >
                  End
                </button>
              )}
            </li>
          ))}
      </ul>
      {may.manage && (
        <AddDiscount
          studentID={studentID}
          threshold={discounts.data?.discount_threshold_kobo ?? 0}
          onAdded={() => {
            setSaid("Discount given: it is taken off each term invoice made while it stands.");
            reload();
          }}
        />
      )}
      <ErrorBox error={act.error} />
    </section>
  );
}

function AddDiscount({ studentID, threshold, onAdded }: { studentID: string; threshold: number; onAdded: () => void }) {
  const [f, setF] = useState<DiscountForm>(emptyDiscount);
  const [tried, setTried] = useState(false);
  const [waiting, setWaiting] = useState("");
  const act = useAction();
  const problem = discountProblem(f);
  return (
    <form
      aria-label="Give a standing discount"
      onSubmit={(e) => {
        e.preventDefault();
        setTried(true);
        if (problem) return;
        void act.run(async () => {
          const out = await plainly(post<Record<string, unknown>>(`/v1/schools/students/${studentID}/discounts`, discountRequest(f)));
          // At or above the threshold the discount is recorded for a
          // second person (202), not given yet.
          setWaiting(needsApproval(202, out) ?? "");
          setF(emptyDiscount());
          setTried(false);
          onAdded();
        }, false);
      }}
    >
      <div className="row" style={{ flexWrap: "wrap", gap: 8 }}>
        <Field label="Kind">
          <select value={f.kind} onChange={(e) => setF({ ...f, kind: e.target.value as DiscountForm["kind"] })}>
            {DISCOUNT_KINDS.map((k) => (
              <option key={k.value} value={k.value}>
                {k.label}
              </option>
            ))}
          </select>
        </Field>
        <Field label="As">
          <select value={f.by} onChange={(e) => setF({ ...f, by: e.target.value as DiscountForm["by"] })}>
            <option value="percent">A percent of the school's own fees</option>
            <option value="amount">An amount in naira</option>
          </select>
        </Field>
        <Field label={f.by === "percent" ? "Percent" : "Amount (₦)"}>
          <input inputMode="decimal" value={f.value} onChange={(e) => setF({ ...f, value: e.target.value })} />
        </Field>
        <Field label="Reason">
          <input value={f.reason} maxLength={200} onChange={(e) => setF({ ...f, reason: e.target.value })} placeholder="Second child in the school" />
        </Field>
      </div>
      {threshold > 0 && <p className="hint">A discount of {naira(threshold)} or more needs a second person to approve it.</p>}
      {tried && problem && (
        <div className="error" role="alert">
          {problem}
        </div>
      )}
      <ErrorBox error={act.error} />
      {waiting && (
        <div className="notice" role="status" data-testid="discount-waiting">
          {waiting}
        </div>
      )}
      <button type="submit" disabled={act.busy}>
        Give the discount
      </button>
    </form>
  );
}
