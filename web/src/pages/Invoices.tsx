import { Fragment, useState } from "react";
import { Link, NavLink, useLocation, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { ApiError, del, get, postOnce, put, post } from "../api";
import { useAuth } from "../auth";
import { EmptyState, ErrorBox, Field, PageHeader, Skeleton, fmtTime, useAction, useLiveRefresh, useLoad } from "../ui";
import {
  amendmentProblem,
  amendmentRequest,
  emptyAmendment,
  lineNote,
  removableLines,
  type AmendmentForm,
  FEE_CHOICES,
  INVOICE_FILTERS,
  INVOICE_LIMITS,
  SUGGESTIONS_PATH,
  UNMATCHED_PATH,
  unmatchedPayments,
  type UnmatchedMoney,
  amountWords,
  channelWords,
  entryMatchPath,
  groupSuggestions,
  kindWords,
  isOrder,
  matchChoices,
  matchRequest,
  matchedLine,
  openInvoices,
  pickerWords,
  rejectedLine,
  targetLabel,
  targetPath,
  type MatchChoice,
  type MoneyOpenOrder,
  scoreWords,
  suggestionPath,
  dateLabel,
  draftRequest,
  emptyItem,
  formFromInvoice,
  formTotal,
  howMatched,
  invoiceAccess,
  invoiceActions,
  invoiceCustomerLine,
  invoicePath,
  invoiceProblem,
  invoiceStatus,
  invoicesPath,
  itemTotal,
  naira,
  newInvoiceForm,
  newRequestKey,
  numberPreview,
  paymentRows,
  plainMessage,
  sentLines,
  settingsForm,
  settingsProblem,
  settingsRequest,
  voidProblem,
  type CustomerInvoice,
  type InvoiceForm,
  type InvoiceSent,
  type InvoiceSettings,
  type ItemInput,
  type MoneySuggestion,
  type SettingsForm,
  type WaitingPayment,
  creditWords,
  invoiceCustomerPath,
  leftWords,
  payerPath,
  payerWords,
  splitLine,
  splitProblem,
  splitRequest,
  splitRows,
  splitTotal,
  type InvoiceCustomerView,
  type MoneySplit,
  type SplitRow,
} from "../lib/invoices";
import { isMasked, telLink } from "../lib/orders";

/** Runs a request and words the server's refusal as a sentence. */
async function plainly<T>(p: Promise<T>): Promise<T> {
  try {
    return await p;
  } catch (e) {
    if (e instanceof ApiError && (e.status === 400 || e.status === 409)) e.message = plainMessage(e.message);
    throw e;
  }
}

function StatusPill({ status }: { status: string }) {
  const s = invoiceStatus(status);
  return <span className={`badge sf-status ${s.tone}`}>{s.label}</span>;
}

/** Invoices, To match (with the number of payments waiting), Settings. */
export function InvoiceTabs({ waiting }: { waiting?: number }) {
  // The pages that know the number pass it; the others ask (a refusal leaves the tab without one).
  const own = useLoad(() => (waiting === undefined ? get<{ suggestions: MoneySuggestion[] | null }>(SUGGESTIONS_PATH) : Promise.resolve(undefined)), [waiting === undefined]);
  const n = waiting ?? (own.data ? groupSuggestions(own.data.suggestions).length : 0);
  return (
    <nav className="tabs" aria-label="Invoices">
      <NavLink to="/invoices" end>
        Invoices
      </NavLink>
      <NavLink to="/invoices/match">
        To match
        {n > 0 && (
          <span className="tab-count" data-testid="to-match-count">
            {n}
            <span className="sr-only"> {n === 1 ? "payment waiting" : "payments waiting"}</span>
          </span>
        )}
      </NavLink>
      <NavLink to="/invoices/settings">Settings</NavLink>
    </nav>
  );
}

// --- matching money to invoices ---

/** Invoices > To match (GET /v1/money/suggestions): money received that may be for an invoice or a storefront order, one card per payment. */
export function InvoiceMatch() {
  const { can } = useAuth();
  const manage = invoiceAccess(can).manage;
  const list = useLoad(() => get<{ suggestions: MoneySuggestion[] | null }>(SUGGESTIONS_PATH), []);
  const left = useLoad(() => get<{ payments: UnmatchedMoney[] | null; orders?: MoneyOpenOrder[] | null }>(UNMATCHED_PATH), []);
  useLiveRefresh(list.reload);
  useLiveRefresh(left.reload);
  // The invoices a payment can be matched to by hand.
  const invoices = useLoad(() => (manage ? get<{ invoices: CustomerInvoice[] | null }>("/v1/invoices") : Promise.resolve({ invoices: [] })), [manage]);
  const [said, setSaid] = useState("");
  const waiting = list.data ? groupSuggestions(list.data.suggestions) : undefined;
  const unmatched = unmatchedPayments(left.data?.payments);
  const done = (line: string) => {
    setSaid(line);
    list.reload();
    left.reload();
    invoices.reload();
  };
  return (
    <>
      <PageHeader
        title="To match"
        description="Money that came in without the reference Taskiem gave, and the invoices or storefront orders it may be for. Match it, or say it is not that one."
      />
      <InvoiceTabs waiting={waiting?.length ?? 0} />
      <div role="status" data-testid="match-status">
        {said && <div className="notice">{said}</div>}
      </div>
      <ErrorBox error={list.error} />
      {waiting === undefined && !list.error && <Skeleton rows={3} label="Loading payments to match…" />}
      {waiting && waiting.length === 0 && unmatched.length === 0 && (
        <EmptyState icon="invoices">Nothing to match. Payments into the accounts Taskiem gave are matched to their invoice as they come in.</EmptyState>
      )}
      {waiting?.map((p) => (
        <WaitingCard key={p.entry_id} payment={p} manage={manage} open={openInvoices(invoices.data?.invoices)} orders={left.data?.orders} onDone={done} />
      ))}
      <ErrorBox error={left.error} />
      {unmatched.length > 0 && (
        <>
          <h2>No invoice suggested</h2>
          <p className="hint">Money that came in with nothing to suggest, or whose suggestions were all turned down. Match it by hand if you know the invoice or order.</p>
          {unmatched.map((p) => (
            <WaitingCard key={p.entry_id} payment={p} manage={manage} open={openInvoices(invoices.data?.invoices)} orders={left.data?.orders} onDone={done} />
          ))}
        </>
      )}
      {!manage && waiting && waiting.length > 0 && <p className="hint">You can see these. Someone who manages invoices can match them.</p>}
    </>
  );
}

function WaitingCard({
  payment: p,
  manage,
  open,
  orders,
  onDone,
}: {
  payment: WaitingPayment;
  manage: boolean;
  open: CustomerInvoice[];
  orders?: MoneyOpenOrder[] | null;
  onDone: (line: string) => void;
}) {
  const act = useAction();
  const [other, setOther] = useState("");
  const [otherTried, setOtherTried] = useState(false);
  const title = `match-${p.entry_id}`;
  const choices: MatchChoice[] = matchChoices(open, orders, p.candidates);
  const words = pickerWords((orders ?? []).length > 0);
  const decide = (c: MoneySuggestion, action: "confirm" | "reject") =>
    void act.run(async () => {
      await plainly(post(suggestionPath(c.id, action)));
      const kind = isOrder(c) ? "order" : "invoice";
      onDone(action === "confirm" ? matchedLine(p.amount_kobo, c.number, kind) : rejectedLine(c.number, kind));
    }, false);
  return (
    <section className="card match-card" aria-labelledby={`${title} ${title}-when ${title}-ref`} data-testid="waiting-payment">
      <h2 id={title}>
        <span className="sf-price">{amountWords(p.amount_kobo)}</span> <span className="hint">{kindWords(p.kind)}</span>
      </h2>
      <dl className="kv">
        <dt>Received</dt>
        <dd id={`${title}-when`}>{fmtTime(p.received_at)}</dd>
        <dt>Where</dt>
        <dd>{channelWords(p.channel)}</dd>
        <dt>Reference</dt>
        <dd id={`${title}-ref`}>{p.reference || <span className="hint">None</span>}</dd>
      </dl>
      {p.held_reason && (
        <div className="notice" data-testid="held-reason">
          Held for you: {p.held_reason}. Nothing was counted towards an invoice.
        </div>
      )}
      {leftWords(p) && (
        <p className="hint" data-testid="left-to-match">
          {leftWords(p)}
        </p>
      )}
      {p.candidates.length > 0 && <h3>May be for</h3>}
      <ul className="match-candidates">
        {p.candidates.map((c) => {
          const w = scoreWords(c.score);
          return (
            <li key={c.id} data-testid="candidate">
              <div className="match-head">
                <Link to={targetPath(c)}>{targetLabel(c)}</Link>{" "}
                <span className={`badge sf-status ${w.tone}`} title={`Score ${c.score} of 100`}>
                  {w.label}
                </span>
                {isOrder(c) && (
                  <span className="hint">
                    {" "}
                    {naira(c.due_kobo ?? 0)} to pay{c.customer ? ` · ${c.customer}` : ""}
                  </span>
                )}
              </div>
              {(c.reasons ?? []).length > 0 && (
                <ul className="match-reasons" aria-label={`Why ${targetLabel(c)}`}>
                  {(c.reasons ?? []).map((r) => (
                    <li key={r}>{r}</li>
                  ))}
                </ul>
              )}
              {manage && (
                <div className="toolbar">
                  <button type="button" className="primary" disabled={act.busy} onClick={() => decide(c, "confirm")} aria-label={`Match to ${targetLabel(c)}`}>
                    Match
                  </button>
                  <button type="button" disabled={act.busy} onClick={() => decide(c, "reject")} aria-label={`Not this one: ${targetLabel(c)}`}>
                    Not this one
                  </button>
                </div>
              )}
              {manage && c.split && (c.split.parts ?? []).length > 0 && (
                <SplitForm suggestion={c} leftKobo={c.left_kobo ?? p.amount_kobo ?? 0} onDone={onDone} />
              )}
            </li>
          );
        })}
      </ul>
      {manage && (
        <details className="match-other">
          <summary>{words.summary}</summary>
          {choices.length === 0 ? (
            <p className="hint">{words.none}</p>
          ) : (
            <form
              noValidate
              onSubmit={(e) => {
                e.preventDefault();
                setOtherTried(true);
                if (!other) return;
                const chosen = choices.find((x) => x.value === other);
                void act.run(async () => {
                  await plainly(post(entryMatchPath(p.entry_id), matchRequest(other)));
                  onDone(matchedLine(p.amount_kobo, chosen?.number ?? other, chosen?.kind));
                }, false);
              }}
            >
              <div className="row match-other-row">
                <Field label={words.label} hint={words.hint}>
                  <select value={other} onChange={(e) => setOther(e.target.value)}>
                    <option value="">{words.empty}</option>
                    {choices.map((x) => (
                      <option key={x.value} value={x.value}>
                        {x.label}
                      </option>
                    ))}
                  </select>
                </Field>
                <button type="submit" disabled={act.busy}>
                  {words.submit}
                </button>
              </div>
              {otherTried && !other && (
                <div className="error" role="alert">
                  {words.missing}
                </div>
              )}
            </form>
          )}
        </details>
      )}
      <ErrorBox error={act.error} />
    </section>
  );
}

/** Split a payment across the customer's open invoices, oldest first as offered, amounts editable. */
function SplitForm({ suggestion: c, leftKobo, onDone }: { suggestion: MoneySuggestion; leftKobo: number; onDone: (line: string) => void }) {
  const act = useAction();
  const [rows, setRows] = useState<SplitRow[]>(() => splitRows(c.split));
  const [creditRest, setCreditRest] = useState(true);
  const [tried, setTried] = useState(false);
  const problem = splitProblem(rows, leftKobo);
  const total = splitTotal(rows);
  const rest = Number.isNaN(total) ? 0 : Math.max(leftKobo - total, 0);
  const id = `split-${c.id}`;
  return (
    <details className="match-split" data-testid="split">
      <summary>Split across the customer's invoices…</summary>
      <p className="hint">
        The payment is more than is due on {c.number}. Offered oldest first across the same customer's open invoices; change the amounts if you know better. Each part is a
        match of its own, and can be undone on its own.
      </p>
      <form
        noValidate
        aria-labelledby={id}
        onSubmit={(e) => {
          e.preventDefault();
          setTried(true);
          if (problem) return;
          void act.run(async () => {
            const r = await plainly(post<MoneySplit>(suggestionPath(c.id, "confirm"), splitRequest(rows, creditRest && rest > 0)));
            onDone(splitLine(r));
          }, false);
        }}
      >
        <h4 id={id} className="sr-only">
          Split for {c.number}
        </h4>
        {rows.map((r, i) => (
          <Field key={r.invoice} label={`${r.invoice} (${naira(r.due_kobo)} due)`}>
            <input
              inputMode="decimal"
              value={r.amount}
              onChange={(e) => setRows(rows.map((x, j) => (j === i ? { ...x, amount: e.target.value } : x)))}
              aria-describedby={`${id}-rest`}
            />
          </Field>
        ))}
        <p className="hint" id={`${id}-rest`} data-testid="split-rest">
          {rest > 0 ? `${naira(rest)} not counted towards these invoices.` : "All of the payment counts towards these invoices."}
        </p>
        {rest > 0 && (
          <label className="row">
            <input type="checkbox" checked={creditRest} onChange={(e) => setCreditRest(e.target.checked)} />
            <span>Keep the rest as the customer's credit, for their next invoice
            (otherwise it waits here to be matched)</span>
          </label>
        )}
        {tried && problem && (
          <div className="error" role="alert">
            {problem}
          </div>
        )}
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy}>
            Match in parts
          </button>
        </div>
      </form>
      <ErrorBox error={act.error} />
    </details>
  );
}

/** The invoice's customer in matching: their credit, permanent account numbers and remembered payers (GET /v1/invoices/{id}/customer). */
function CustomerMatching({ invoice, manage }: { invoice: CustomerInvoice; manage: boolean }) {
  const view = useLoad(() => get<InvoiceCustomerView>(invoiceCustomerPath(invoice.id)), [invoice.id, invoice.paid_kobo]);
  const forget = useAction();
  const v = view.data;
  return (
    <section className="card" aria-labelledby="inv-customer-money" data-testid="customer-money">
      <h2 id="inv-customer-money">The customer's money</h2>
      <ErrorBox error={view.error} />
      {!v && !view.error && <Skeleton rows={2} label="Loading the customer's credit…" />}
      {v && (
        <>
          <dl className="kv">
            <dt>Credit</dt>
            <dd data-testid="customer-credit">{creditWords(v.credit_kobo)}</dd>
            <dt>Account numbers</dt>
            <dd>
              {(v.accounts ?? []).length === 0 ? (
                <span className="hint">None. Money into an account number of the customer's own is matched to their oldest open invoice first.</span>
              ) : (
                <ul className="plain-list">
                  {(v.accounts ?? []).map((a) => (
                    <li key={a.id}>
                      {a.account_number}
                      {a.bank_name ? ` · ${a.bank_name}` : ""} <span className="hint">({a.channel})</span>
                    </li>
                  ))}
                </ul>
              )}
            </dd>
          </dl>
          <h3>Payers remembered</h3>
          {(v.payers ?? []).length === 0 ? (
            <p className="hint">None yet. When someone pays for this customer and you match it, Taskiem remembers the payer and suggests this customer first next time.</p>
          ) : (
            <ul className="plain-list" aria-label="Payers remembered for this customer">
              {(v.payers ?? []).map((p) => (
                <li key={p.id} data-testid="payer">
                  {payerWords(p)}{" "}
                  {manage && (
                    <button
                      type="button"
                      disabled={forget.busy}
                      aria-label={`Forget this payer: ${p.payer}`}
                      onClick={() =>
                        void forget.run(async () => {
                          await plainly(del(payerPath(p.id)));
                          view.reload();
                        }, "Payer forgotten")
                      }
                    >
                      Forget this payer
                    </button>
                  )}
                </li>
              ))}
            </ul>
          )}
          <ErrorBox error={forget.error} />
        </>
      )}
    </section>
  );
}

// --- the list ---

/** The business's invoices (GET /v1/invoices), newest first, customers masked. */
export function Invoices() {
  const { can } = useAuth();
  const [params, setParams] = useSearchParams();
  const status = params.get("status") ?? "";
  const list = useLoad(() => get<{ invoices: CustomerInvoice[] | null }>(invoicesPath(status)), [status]);
  useLiveRefresh(list.reload);
  const invoices = list.data ? (list.data.invoices ?? []) : undefined;
  const newButton = invoiceAccess(can).manage ? (
    <Link className="button primary" to="/invoices/new">
      New invoice
    </Link>
  ) : undefined;
  return (
    <>
      <PageHeader title="Invoices" description="What you ask your customers to pay, newest first. Each one is marked paid only by the payments counted towards it." actions={newButton} />
      <InvoiceTabs />
      <div className="filters" role="search" aria-label="Filter invoices">
        <label>
          Show{" "}
          <select value={status} onChange={(e) => setParams(e.target.value ? { status: e.target.value } : {}, { replace: true })}>
            {INVOICE_FILTERS.map((f) => (
              <option key={f.label} value={f.value}>
                {f.label}
              </option>
            ))}
          </select>
        </label>
        <button type="button" onClick={list.reload}>
          Refresh
        </button>
      </div>
      <ErrorBox error={list.error} />
      {invoices === undefined && !list.error && <Skeleton rows={5} label="Loading invoices…" />}
      {invoices && invoices.length === 0 && (
        <EmptyState icon="invoices" action={status ? undefined : newButton}>
          {status
            ? "No invoices with this status."
            : "No invoices yet. Make one, send it to your customer by SMS and email, and see it marked paid as the money comes in."}
        </EmptyState>
      )}
      {invoices && invoices.length > 0 && (
        <table className="cards">
          <thead>
            <tr>
              <th scope="col">Number</th>
              <th scope="col">Status</th>
              <th scope="col">Customer</th>
              <th scope="col">Total</th>
              <th scope="col">Still due</th>
              <th scope="col">Due date</th>
            </tr>
          </thead>
          <tbody>
            {invoices.map((x) => (
              <tr key={x.id} data-testid="invoice">
                <td className="primary" data-label="Number">
                  <Link to={`/invoices/${encodeURIComponent(x.number)}`}>{x.number}</Link>
                </td>
                <td data-label="Status">
                  <StatusPill status={x.status} />
                </td>
                <td data-label="Customer">{invoiceCustomerLine(x.customer)}</td>
                <td data-label="Total" className="sf-price">
                  {naira(x.total_kobo)}
                </td>
                <td data-label="Still due" className="sf-price">
                  {x.status === "void" ? <span className="hint">—</span> : naira(x.due_kobo)}
                </td>
                <td data-label="Due date">{dateLabel(x.due_on) || <span className="hint">—</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}

// --- making and changing a draft ---

/** A new invoice: its form, with the business's settings as the defaults. */
export function InvoiceNew() {
  const { can } = useAuth();
  const settings = useLoad(() => get<InvoiceSettings>("/v1/invoice-settings"), []);
  const ready = settings.data !== undefined || !!settings.error;
  return (
    <>
      <PageHeader title="New invoice" actions={<Link to="/invoices">All invoices</Link>} />
      {!can("invoice.manage") ? (
        <p className="hint">You can see invoices, but not make them. Someone who manages invoices can.</p>
      ) : ready ? (
        <InvoiceEditor initial={newInvoiceForm(settings.data ?? null)} />
      ) : (
        <Skeleton rows={4} label="Loading…" />
      )}
    </>
  );
}

/** Changing a draft. Once sent, an invoice is voided and a new one sent instead. */
export function InvoiceEdit() {
  const { id = "" } = useParams();
  const { can } = useAuth();
  // The customer's details are needed in full to change them: shown to those who may see them (recorded in the audit log).
  const reveal = can("pii.reveal");
  const { data, error } = useLoad(() => get<{ invoice: CustomerInvoice; revealed: boolean }>(invoicePath(id, reveal ? "?reveal=true" : "")), [id, reveal]);
  const x = data?.invoice;
  const back = <Link to={`/invoices/${encodeURIComponent(id)}`}>Back to the invoice</Link>;
  if (!can("invoice.manage"))
    return (
      <>
        <PageHeader title={`Change ${id}`} actions={back} />
        <p className="hint">You can see invoices, but not change them.</p>
      </>
    );
  return (
    <>
      <PageHeader title={`Change ${x?.number ?? id}`} actions={back} />
      <ErrorBox error={error} />
      {!x && !error && <Skeleton rows={4} label="Loading the invoice…" />}
      {x && x.status !== "draft" && (
        <div className="notice" role="status">
          Only a draft can be changed. This invoice has been sent: to correct it, void it with the reason and send a new one.
        </div>
      )}
      {x && x.status === "draft" && (
        <>
          {!data?.revealed && (
            <p className="hint">The customer's details are hidden from you. Enter them again to save your changes.</p>
          )}
          <InvoiceEditor initial={data?.revealed ? formFromInvoice(x) : { ...formFromInvoice(x), name: "", phone: "", email: "" }} existing={x} />
        </>
      )}
    </>
  );
}

function InvoiceEditor({ initial, existing }: { initial: InvoiceForm; existing?: CustomerInvoice }) {
  const navigate = useNavigate();
  const [form, setForm] = useState(initial);
  const [tried, setTried] = useState(false);
  // One key for this form, so a retried save makes one invoice; and one per send.
  const [createKey] = useState(newRequestKey);
  const [sendKey] = useState(newRequestKey);
  // A draft this form has saved already (a send that then failed): later saves change it.
  const [saved, setSaved] = useState<CustomerInvoice | undefined>(existing);
  const act = useAction();
  const set = (patch: Partial<InvoiceForm>) => setForm((f) => ({ ...f, ...patch }));
  const setItem = (i: number, patch: Partial<ItemInput>) => setForm((f) => ({ ...f, items: f.items.map((it, n) => (n === i ? { ...it, ...patch } : it)) }));
  const problem = invoiceProblem(form);
  const total = formTotal(form);

  const save = (andSend: boolean) => {
    setTried(true);
    if (problem) return;
    void act.run(async () => {
      const body = draftRequest(form);
      const x = saved
        ? await plainly(put<CustomerInvoice>(invoicePath(saved.id), body))
        : await plainly(postOnce<CustomerInvoice>("/v1/invoices", createKey, body));
      setSaved(x);
      let sent: InvoiceSent | undefined;
      if (andSend) sent = await plainly(postOnce<InvoiceSent>(invoicePath(x.id, "/send"), sendKey));
      navigate(`/invoices/${encodeURIComponent(x.number)}`, { state: sent ? { sent } : undefined });
    }, andSend ? "Invoice sent" : "Draft saved");
  };

  return (
    <form
      className="card"
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        save(false);
      }}
    >
      <h2>Customer</h2>
      <Field label="Name">
        <input value={form.name} maxLength={INVOICE_LIMITS.name} autoComplete="off" onChange={(e) => set({ name: e.target.value })} required />
      </Field>
      <div className="row">
        <Field label="Phone number" hint="For the SMS.">
          <input type="tel" inputMode="tel" value={form.phone} autoComplete="off" onChange={(e) => set({ phone: e.target.value })} />
        </Field>
        <Field label="Email" hint="For the email.">
          <input type="email" value={form.email} autoComplete="off" onChange={(e) => set({ email: e.target.value })} />
        </Field>
      </div>
      <p className="hint" style={{ marginTop: -8 }}>
        Give a phone number or an email, or both.
      </p>

      <h2>What it is for</h2>
      <fieldset className="sf-choices">
        <legend>Charge</legend>
        <label className="row">
          <input type="radio" name="mode" checked={form.mode === "amount"} onChange={() => set({ mode: "amount" })} />
          <span>One amount</span>
        </label>
        <label className="row">
          <input type="radio" name="mode" checked={form.mode === "items"} onChange={() => set({ mode: "items" })} />
          <span>A list of items</span>
        </label>
      </fieldset>
      {form.mode === "amount" ? (
        <div className="row">
          <Field label="Amount (₦)">
            <input inputMode="decimal" value={form.amount} placeholder="25,000" onChange={(e) => set({ amount: e.target.value })} />
          </Field>
          <Field label="Description" hint="What it is for, such as Catering for 12 October. Optional.">
            <input value={form.description} maxLength={INVOICE_LIMITS.description} onChange={(e) => set({ description: e.target.value })} />
          </Field>
        </div>
      ) : (
        <ItemsEditor
          items={form.items}
          onChange={setItem}
          onAdd={() => setForm((f) => ({ ...f, items: [...f.items, emptyItem()] }))}
          onRemove={(i) => setForm((f) => ({ ...f, items: f.items.filter((_, n) => n !== i) }))}
        />
      )}
      <p className="sf-price" aria-live="polite">
        Total {naira(total)}
      </p>

      <h2>When and a note</h2>
      <div className="row">
        <Field label="Due date" hint="Optional. It is overdue the day after.">
          <input type="date" value={form.dueOn} onChange={(e) => set({ dueOn: e.target.value })} />
        </Field>
        <Field label="Note for the customer" hint="Optional.">
          <textarea value={form.note} maxLength={INVOICE_LIMITS.note} rows={2} onChange={(e) => set({ note: e.target.value })} />
        </Field>
      </div>

      <details style={{ marginBottom: 16 }}>
        <summary>More payment options</summary>
        <div style={{ marginTop: 12 }}>
          <Field label="Payments connection" hint="The connection that takes payments for this invoice. Leave it empty for your usual one.">
            <input value={form.connection} placeholder="main" onChange={(e) => set({ connection: e.target.value })} />
          </Field>
          <fieldset className="sf-choices">
            <legend>Who pays the transfer fee</legend>
            {FEE_CHOICES.map((c) => (
              <label className="row" key={c.value}>
                <input type="radio" name="fee" checked={form.feePaidBy === c.value} onChange={() => set({ feePaidBy: c.value })} />
                <span>
                  {c.label} <span className="hint">{c.help}</span>
                </span>
              </label>
            ))}
          </fieldset>
          <label className="row">
            <input type="checkbox" checked={form.partPayments} onChange={(e) => set({ partPayments: e.target.checked })} />
            <span>The customer may pay part of it</span>
          </label>
        </div>
      </details>

      {tried && problem && (
        <div className="error" role="alert">
          {problem}
        </div>
      )}
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button type="submit" disabled={act.busy}>
          Save draft
        </button>
        <button type="button" className="primary" disabled={act.busy} onClick={() => save(true)}>
          Save and send
        </button>
      </div>
      <p className="hint">Sending sends it to the customer by SMS and email, with a link to its page. Once sent, it cannot be changed.</p>
    </form>
  );
}

function ItemsEditor({
  items,
  onChange,
  onAdd,
  onRemove,
}: {
  items: ItemInput[];
  onChange: (i: number, patch: Partial<ItemInput>) => void;
  onAdd: () => void;
  onRemove: (i: number) => void;
}) {
  return (
    <div>
      {items.map((it, i) => (
        <fieldset key={i} className="sf-choices" aria-label={`Item ${i + 1}`}>
          <div className="row" style={{ alignItems: "flex-end" }}>
            <Field label="Description">
              <input value={it.description} maxLength={INVOICE_LIMITS.description} onChange={(e) => onChange(i, { description: e.target.value })} />
            </Field>
            <Field label="Quantity">
              <input inputMode="numeric" value={it.quantity} onChange={(e) => onChange(i, { quantity: e.target.value })} />
            </Field>
            <Field label="Price each (₦)">
              <input inputMode="decimal" value={it.price} placeholder="1,500" onChange={(e) => onChange(i, { price: e.target.value })} />
            </Field>
            <Field label="Amount">
              <output className="sf-price" style={{ display: "block", paddingTop: 8 }}>
                {naira(itemTotal(it))}
              </output>
            </Field>
            {items.length > 1 && (
              <button type="button" className="danger" aria-label={`Remove item ${i + 1}`} onClick={() => onRemove(i)}>
                Remove
              </button>
            )}
          </div>
        </fieldset>
      ))}
      {items.length < INVOICE_LIMITS.lines && (
        <button type="button" onClick={onAdd} style={{ marginBottom: 12 }}>
          Add an item
        </button>
      )}
    </div>
  );
}

// --- one invoice ---

/** One invoice (GET /v1/invoices/{id}): its lines and totals, payments, and what can be done with it. */
export function InvoicePage() {
  const { id = "" } = useParams();
  const { can } = useAuth();
  const location = useLocation();
  const [sent, setSent] = useState<InvoiceSent | undefined>(() => (location.state as { sent?: InvoiceSent } | null)?.sent);
  const [sendKey, setSendKey] = useState(newRequestKey);
  const [voiding, setVoiding] = useState(false);
  const [reason, setReason] = useState("");
  const [voidTried, setVoidTried] = useState(false);
  const [undoing, setUndoing] = useState<string>();
  const [revealed, setRevealed] = useState<CustomerInvoice>();
  const send = useAction();
  const voidAct = useAction();
  const undo = useAction();
  const reveal = useAction();
  const { data, error, reload } = useLoad(() => get<{ invoice: CustomerInvoice; revealed: boolean }>(invoicePath(id)), [id]);
  useLiveRefresh(reload);
  const x = data?.invoice;
  const may = invoiceActions(x?.status ?? "", can);
  const reasonProblem = voidProblem(reason);

  const doSend = (inv: CustomerInvoice) =>
    void send.run(async () => {
      const r = await plainly(postOnce<InvoiceSent>(invoicePath(inv.id, "/send"), sendKey));
      setSent(r);
      setSendKey(newRequestKey());
      reload();
    }, "Invoice sent");

  return (
    <>
      <PageHeader title={`Invoice ${x?.number ?? id}`} actions={<Link to="/invoices">All invoices</Link>} />
      <ErrorBox error={error} />
      {!x && !error && <Skeleton rows={4} label="Loading the invoice…" />}
      {x && (
        <>
          {sent && (
            <div className="notice" role="status" data-testid="sent">
              {sentLines(sent).map((l) => (
                <div key={l}>{l}</div>
              ))}
              {sent.link && (
                <div className="sf-links">
                  Its page:{" "}
                  <a href={sent.link} target="_blank" rel="noreferrer">
                    {sent.link.replace(/^https?:\/\//, "")}
                  </a>
                </div>
              )}
            </div>
          )}

          {(may.edit || may.send || may.sendAgain || may.void) && (
            <div className="toolbar">
              {may.edit && (
                <Link className="button" to={`/invoices/${encodeURIComponent(x.number)}/edit`}>
                  Edit
                </Link>
              )}
              {may.send && (
                <button className="primary" disabled={send.busy} onClick={() => doSend(x)}>
                  Send
                </button>
              )}
              {may.sendAgain && (
                <button disabled={send.busy} onClick={() => doSend(x)}>
                  Send again
                </button>
              )}
              {may.void && !voiding && (
                <button className="danger" onClick={() => setVoiding(true)}>
                  Void
                </button>
              )}
            </div>
          )}
          <ErrorBox error={send.error} />

          {voiding && may.void && (
            <section className="card" aria-labelledby="void-title">
              <h2 id="void-title">Void {x.number}?</h2>
              <p>A void invoice takes no new payment, and its page tells the customer why. It cannot be undone: to ask for the money again, send a new invoice.</p>
              <Field label="Reason">
                <textarea value={reason} rows={2} maxLength={INVOICE_LIMITS.note} onChange={(e) => setReason(e.target.value)} autoFocus />
              </Field>
              {voidTried && reasonProblem && (
                <div className="error" role="alert">
                  {reasonProblem}
                </div>
              )}
              <ErrorBox error={voidAct.error} />
              <div className="toolbar">
                <button
                  className="danger"
                  disabled={voidAct.busy}
                  onClick={() => {
                    setVoidTried(true);
                    if (reasonProblem) return;
                    void voidAct.run(async () => {
                      await plainly(post(invoicePath(x.id, "/void"), { reason: reason.trim() }));
                      setVoiding(false);
                      setReason("");
                      setVoidTried(false);
                      reload();
                    }, "Invoice voided");
                  }}
                >
                  Void invoice
                </button>
                <button
                  onClick={() => {
                    setVoiding(false);
                    setVoidTried(false);
                    voidAct.clear();
                  }}
                >
                  Keep it
                </button>
              </div>
            </section>
          )}

          <section className="card" aria-labelledby="inv-what">
            <h2 id="inv-what">What it is for</h2>
            {(x.lines ?? []).length > 0 ? (
              <table>
                <thead>
                  <tr>
                    <th scope="col">Description</th>
                    <th scope="col">Quantity</th>
                    <th scope="col">Price each</th>
                    <th scope="col">Amount</th>
                  </tr>
                </thead>
                <tbody>
                  {(x.lines ?? []).map((l, i) => (
                    <tr key={i}>
                      <td>
                        {l.description}
                        {lineNote(l) && <div className="hint">{lineNote(l)}</div>}
                      </td>
                      <td>{l.quantity}</td>
                      <td className="sf-price">{l.kind ? "" : naira(l.unit_kobo)}</td>
                      <td className="sf-price">{naira(l.quantity * l.unit_kobo)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : (
              <p className="hint">No items.</p>
            )}
            <dl className="kv" style={{ marginTop: 16 }}>
              <dt>Total</dt>
              <dd className="sf-price">{naira(x.total_kobo)}</dd>
              <dt>Paid</dt>
              <dd className="sf-price">{naira(x.paid_kobo)}</dd>
              <dt>Still due</dt>
              <dd className="sf-price">{x.status === "void" ? "Nothing: it is void" : naira(x.due_kobo)}</dd>
            </dl>
          </section>

          {may.amend && <Amend invoice={x} onDone={reload} />}

          <section className="card" aria-labelledby="inv-details">
            <h2 id="inv-details">Details</h2>
            <dl className="kv">
              <dt>Status</dt>
              <dd>
                <StatusPill status={x.status} />
              </dd>
              <dt>Due date</dt>
              <dd>{dateLabel(x.due_on) || <span className="hint">None</span>}</dd>
              <dt>Made</dt>
              <dd>{fmtTime(x.created_at)}</dd>
              <dt>Sent</dt>
              <dd>{x.sent_at ? fmtTime(x.sent_at) : <span className="hint">Not yet</span>}</dd>
              {x.paid_at && (
                <>
                  <dt>Paid</dt>
                  <dd>{fmtTime(x.paid_at)}</dd>
                </>
              )}
              {x.voided_at && (
                <>
                  <dt>Voided</dt>
                  <dd>
                    {fmtTime(x.voided_at)}
                    {x.void_reason && <div>{x.void_reason}</div>}
                  </dd>
                </>
              )}
              {x.note && (
                <>
                  <dt>Note</dt>
                  <dd style={{ whiteSpace: "pre-wrap" }}>{x.note}</dd>
                </>
              )}
              <dt>Payments connection</dt>
              <dd>{x.connection}</dd>
              <dt>Transfer fee</dt>
              <dd>{x.fee_paid_by === "customer" ? "Paid by the customer" : "Paid by the business"}</dd>
              <dt>Part payments</dt>
              <dd>{x.allow_part_payments ? "Allowed" : "Not allowed"}</dd>
            </dl>
          </section>

          <section className="card" aria-labelledby="inv-customer">
            <h2 id="inv-customer">Customer</h2>
            <CustomerDetails c={(revealed ?? x).customer} />
            {!revealed && may.reveal && (
              <>
                <ErrorBox error={reveal.error} />
                <button
                  disabled={reveal.busy}
                  onClick={() =>
                    void reveal.run(async () => {
                      const r = await get<{ invoice: CustomerInvoice; revealed: boolean }>(invoicePath(x.id, "?reveal=true"));
                      setRevealed(r.invoice);
                    }, false)
                  }
                >
                  Show customer details
                </button>
                <p className="hint">Showing them is recorded in the audit log.</p>
              </>
            )}
            {!revealed && !may.reveal && <p className="hint">The customer's details are hidden. Someone who may see personal data can show them.</p>}
          </section>

          {x.status !== "draft" && <CustomerMatching invoice={x} manage={invoiceAccess(can).manage} />}

          <section className="card" aria-labelledby="inv-payments">
            <h2 id="inv-payments">Payments</h2>
            <Payments
              invoice={x}
              canUndo={may.undo}
              undoing={undoing}
              setUndoing={(p) => {
                setUndoing(p);
                undo.clear();
              }}
              busy={undo.busy}
              onUndo={(pid) =>
                void undo.run(async () => {
                  await plainly(post(invoicePath(x.id, `/payments/${encodeURIComponent(pid)}/undo`)));
                  setUndoing(undefined);
                  reload();
                }, "Payment undone")
              }
            />
            <ErrorBox error={undo.error} />
          </section>
        </>
      )}
    </>
  );
}

/** Amendments to a sent invoice: each a recorded change with a reason, a line of its own. */
function Amend({ invoice: x, onDone }: { invoice: CustomerInvoice; onDone: () => void }) {
  const [open, setOpen] = useState(false);
  const [f, setF] = useState<AmendmentForm>(emptyAmendment);
  const [tried, setTried] = useState(false);
  const act = useAction();
  const problem = amendmentProblem(f);
  const removable = removableLines(x);
  const done = x.amendments ?? [];
  return (
    <section className="card" aria-labelledby="inv-amend">
      <h2 id="inv-amend">Amendments</h2>
      <p className="hint">
        A sent invoice is not edited: it is amended, with a reason, and each amendment shows as its own line to the customer. The total never goes below what was paid; a paid invoice whose total rises
        is part-paid again.
      </p>
      {done.length > 0 && (
        <ul data-testid="amendments">
          {done.map((a) => (
            <li key={a.id}>
              {a.line.description}: {naira(a.amount_kobo)}. {a.reason} <span className="hint">({fmtTime(a.created_at)})</span>
            </li>
          ))}
        </ul>
      )}
      {!open ? (
        <button type="button" onClick={() => setOpen(true)}>
          Amend…
        </button>
      ) : (
        <form
          aria-label="Amend the invoice"
          onSubmit={(e) => {
            e.preventDefault();
            setTried(true);
            if (problem) return;
            void act.run(async () => {
              await plainly(post(invoicePath(x.id, "/amendments"), amendmentRequest(f)));
              setF(emptyAmendment());
              setTried(false);
              setOpen(false);
              onDone();
            }, "Invoice amended");
          }}
        >
          <Field label="Change">
            <select value={f.kind} onChange={(e) => setF({ ...f, kind: e.target.value as AmendmentForm["kind"] })}>
              <option value="add">Add a line</option>
              <option value="discount">Give a discount</option>
              <option value="remove" disabled={removable.length === 0}>
                Take a line out
              </option>
            </select>
          </Field>
          {f.kind === "remove" ? (
            <Field label="Line to take out">
              <select value={f.line} onChange={(e) => setF({ ...f, line: e.target.value })}>
                <option value="">Choose…</option>
                {removable.map((l) => (
                  <option key={l.index} value={String(l.index)}>
                    {l.label}
                  </option>
                ))}
              </select>
            </Field>
          ) : (
            <>
              <Field label={f.kind === "add" ? "What it is for" : "Description (optional)"}>
                <input value={f.description} maxLength={INVOICE_LIMITS.description} onChange={(e) => setF({ ...f, description: e.target.value })} />
              </Field>
              <Field label="Amount (₦)">
                <input inputMode="decimal" value={f.amount} onChange={(e) => setF({ ...f, amount: e.target.value })} />
              </Field>
              {f.kind === "add" && (
                <Field label="Collected on behalf of (optional)" hint="For money passed on to someone else, such as an exam body.">
                  <input value={f.passThroughFor} maxLength={60} onChange={(e) => setF({ ...f, passThroughFor: e.target.value })} />
                </Field>
              )}
            </>
          )}
          <Field label="Reason">
            <textarea value={f.reason} rows={2} maxLength={300} onChange={(e) => setF({ ...f, reason: e.target.value })} />
          </Field>
          {tried && problem && (
            <div className="error" role="alert">
              {problem}
            </div>
          )}
          <ErrorBox error={act.error} />
          <div className="toolbar">
            <button type="submit" className="primary" disabled={act.busy}>
              Amend the invoice
            </button>
            <button
              type="button"
              onClick={() => {
                setOpen(false);
                setTried(false);
                act.clear();
              }}
            >
              Cancel
            </button>
          </div>
        </form>
      )}
    </section>
  );
}

function Payments({
  invoice,
  canUndo,
  undoing,
  setUndoing,
  busy,
  onUndo,
}: {
  invoice: CustomerInvoice;
  canUndo: boolean;
  undoing: string | undefined;
  setUndoing: (id: string | undefined) => void;
  busy: boolean;
  onUndo: (id: string) => void;
}) {
  const rows = paymentRows(invoice.payments);
  if (rows.length === 0)
    return <p className="hint">No payments yet. Payments are counted here as they come in from your payments connection, or when money received is matched to this invoice on To match. Nobody marks an invoice paid without a payment.</p>;
  return (
    <table className="cards">
      <thead>
        <tr>
          <th scope="col">Amount</th>
          <th scope="col">Reference</th>
          <th scope="col">How it was matched</th>
          <th scope="col">By</th>
          <th scope="col">Date</th>
          <th scope="col">
            <span className="sr-only">Actions</span>
          </th>
        </tr>
      </thead>
      <tbody>
        {rows.map(({ payment: p, undoneBy }) => {
          const struck = undoneBy ? { textDecoration: "line-through" } : undefined;
          return (
            <Fragment key={p.id}>
              <tr data-testid="payment">
                <td className="primary sf-price" data-label="Amount">
                  <span style={struck}>{naira(p.amount_kobo)}</span>
                  {undoneBy && <span className="hint"> Undone</span>}
                </td>
                <td data-label="Reference">
                  <span style={struck}>{p.payment_id}</span>
                </td>
                <td data-label="How it was matched">
                  <span style={struck} data-testid="how-matched">
                    {howMatched(p.how, p.by)}
                  </span>
                  {(p.reasons ?? []).length > 0 && (
                    <ul className="match-reasons" aria-label="Why it was matched">
                      {(p.reasons ?? []).map((r) => (
                        <li key={r}>{r}</li>
                      ))}
                    </ul>
                  )}
                </td>
                <td data-label="By">
                  <span style={struck}>{p.by}</span>
                </td>
                <td data-label="Date">
                  <span style={struck}>{fmtTime(p.created_at)}</span>
                  {undoneBy && (
                    <div className="hint">
                      Undone {fmtTime(undoneBy.created_at)}
                      {undoneBy.by && ` by ${undoneBy.by}`}
                    </div>
                  )}
                </td>
                <td data-label="">
                  {canUndo && !undoneBy && undoing !== p.id && (
                    <button type="button" onClick={() => setUndoing(p.id)} aria-label={`Undo the payment ${p.payment_id}`}>
                      Undo
                    </button>
                  )}
                </td>
              </tr>
              {undoing === p.id && !undoneBy && (
                <tr>
                  <td colSpan={6} className="primary">
                    <div role="group" aria-label="Undo this payment">
                      <p style={{ marginTop: 0 }}>
                        Take {naira(p.amount_kobo)} out of this invoice? Do this when it was matched to the wrong invoice. The payment and its undoing both stay in the history.
                      </p>
                      <div className="toolbar" style={{ marginBottom: 0 }}>
                        <button type="button" className="danger" disabled={busy} onClick={() => onUndo(p.id)}>
                          Undo payment
                        </button>
                        <button type="button" onClick={() => setUndoing(undefined)}>
                          Keep it
                        </button>
                      </div>
                    </div>
                  </td>
                </tr>
              )}
            </Fragment>
          );
        })}
      </tbody>
    </table>
  );
}

function CustomerDetails({ c }: { c: CustomerInvoice["customer"] }) {
  const tel = telLink(c.phone);
  return (
    <dl className="kv sf-links" style={{ marginBottom: 12 }}>
      <dt>Name</dt>
      <dd>{c.name || <span className="hint">—</span>}</dd>
      <dt>Phone</dt>
      <dd>{c.phone ? tel ? <a href={tel}>{c.phone}</a> : c.phone : <span className="hint">—</span>}</dd>
      <dt>Email</dt>
      <dd>{c.email ? isMasked(c.email) ? c.email : <a href={`mailto:${c.email}`}>{c.email}</a> : <span className="hint">—</span>}</dd>
    </dl>
  );
}

// --- settings ---

/** Invoices > Settings (GET/PUT /v1/invoice-settings). */
export function InvoiceSettingsPage() {
  const settings = useLoad(() => get<InvoiceSettings>("/v1/invoice-settings"), []);
  return (
    <>
      <PageHeader
        title="Invoice settings"
        description="How new invoices are numbered, their usual payment options, and how money is matched to them. Each invoice can change the payment options for itself."
      />
      <InvoiceTabs />
      <ErrorBox error={settings.error} />
      {!settings.data && !settings.error && <Skeleton rows={4} label="Loading the settings…" />}
      {settings.data && <SettingsEditor saved={settings.data} />}
    </>
  );
}

function SettingsEditor({ saved: initial }: { saved: InvoiceSettings }) {
  const { can } = useAuth();
  const manage = can("invoice.manage");
  const [saved, setSaved] = useState(initial);
  const [form, setForm] = useState<SettingsForm>(() => settingsForm(initial));
  const [tried, setTried] = useState(false);
  const act = useAction();
  const set = (patch: Partial<SettingsForm>) => setForm((f) => ({ ...f, ...patch }));
  const problem = settingsProblem(form, saved);
  return (
    <form
      className="card"
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        setTried(true);
        if (problem || !manage) return;
        void act.run(async () => {
          const s = await plainly(put<InvoiceSettings>("/v1/invoice-settings", settingsRequest(form)));
          setSaved(s);
          setForm(settingsForm(s));
          setTried(false);
        }, "Settings saved");
      }}
    >
      {!manage && <p className="hint">You can see these settings. Someone who manages invoices can change them.</p>}
      <fieldset disabled={!manage} className="sf-choices">
        <div className="row">
          <Field label="Number prefix" hint="Up to 12 letters, digits, -, _ or /.">
            <input value={form.prefix} maxLength={INVOICE_LIMITS.prefix} onChange={(e) => set({ prefix: e.target.value })} />
          </Field>
          <Field label="Next number" hint={`Can move forward, never back, so no number is given twice. The next invoice is ${numberPreview(form.prefix, Number(form.nextNumber))}.`}>
            <input type="number" inputMode="numeric" min={saved.next_number} step={1} value={form.nextNumber} onChange={(e) => set({ nextNumber: e.target.value })} />
          </Field>
        </div>
        <Field label="Payments connection" hint="The connection (Paystack, Flutterwave, OPay, …) that takes payments for invoices.">
          <input value={form.connection} placeholder="main" onChange={(e) => set({ connection: e.target.value })} />
        </Field>
        <fieldset className="sf-choices">
          <legend>Who pays the transfer fee</legend>
          {FEE_CHOICES.map((c) => (
            <label className="row" key={c.value}>
              <input type="radio" name="settings-fee" checked={form.feePaidBy === c.value} onChange={() => set({ feePaidBy: c.value })} />
              <span>
                {c.label} <span className="hint">{c.help}</span>
              </span>
            </label>
          ))}
        </fieldset>
        <label className="row">
          <input type="checkbox" checked={form.partPayments} onChange={(e) => set({ partPayments: e.target.checked })} />
          <span>Customers may pay part of an invoice</span>
        </label>
        <label className="row">
          <input type="checkbox" checked={form.autoMatch} onChange={(e) => set({ autoMatch: e.target.checked })} />
          <span>
            Match strong suggestions automatically{" "}
            <span className="hint">
              Off by default. A payment that clearly fits one invoice is matched without asking; each automatic match lists its reasons on the invoice and can be undone there.
            </span>
          </span>
        </label>
      </fieldset>
      {tried && problem && (
        <div className="error" role="alert">
          {problem}
        </div>
      )}
      <ErrorBox error={act.error} />
      {manage && (
        <button type="submit" className="primary" disabled={act.busy}>
          Save settings
        </button>
      )}
    </form>
  );
}
