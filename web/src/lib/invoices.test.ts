import { afterEach, describe, expect, it, vi } from "vitest";
import { postOnce } from "../api";
import {
  INVOICE_FILTERS,
  INVOICE_STATUS,
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
  koboInput,
  naira,
  newInvoiceForm,
  newRequestKey,
  newestFirst,
  numberPreview,
  paymentRows,
  plainMessage,
  sentLines,
  settingsForm,
  settingsProblem,
  settingsRequest,
  toKobo,
  voidProblem,
  amountWords,
  channelWords,
  entryMatchPath,
  groupSuggestions,
  unmatchedPayments,
  UNMATCHED_PATH,
  kindWords,
  matchedLine,
  openInvoices,
  rejectedLine,
  scoreWords,
  suggestionPath,
  howMatchedOrder,
  isOrder,
  matchChoices,
  matchRequest,
  orderPaymentRows,
  orderPaymentUndoPath,
  pickerWords,
  targetLabel,
  targetPath,
  type CustomerInvoice,
  type InvoiceForm,
  type InvoicePayment,
  type MoneyOpenOrder,
  type MoneySuggestion,
  type AccountIssuer,
  accountProblem,
  accountRequest,
  accountWords,
  issuerWords,
  newAccountForm,
  invoiceHero,
} from "./invoices";

const can =
  (...perms: string[]) =>
  (p: string) =>
    perms.includes(p);

const invoice: CustomerInvoice = {
  id: "0b6c1c1e-0000-4000-8000-000000000001",
  number: "INV-0007",
  status: "draft",
  customer: { name: "Ada Obi", phone: "08031234567", email: "ada@example.ng" },
  lines: [{ description: "Catering", quantity: 1, unit_kobo: 2500050 }],
  total_kobo: 2500050,
  paid_kobo: 0,
  due_kobo: 2500050,
  currency: "NGN",
  due_on: "2026-11-01",
  note: "Thank you",
  connection: "main",
  fee_paid_by: "business",
  allow_part_payments: false,
  sent_at: null,
  paid_at: null,
  voided_at: null,
  created_by: "user:1",
  created_at: "2026-10-20T12:00:00Z",
};

describe("money", () => {
  it("shows kobo as naira", () => {
    expect(naira(2500000)).toBe("₦25,000");
    expect(naira(2500050)).toBe("₦25,000.50");
    expect(naira(5)).toBe("₦0.05");
    expect(naira(0)).toBe("₦0");
    expect(naira(123456789)).toBe("₦1,234,567.89");
  });

  it("reads typed naira as kobo", () => {
    expect(toKobo("25,000")).toBe(2500000);
    expect(toKobo("₦25,000.50")).toBe(2500050);
    expect(toKobo("0.1")).toBe(10);
    expect(toKobo("19.99")).toBe(1999);
    expect(toKobo("")).toBeUndefined();
    expect(toKobo("lots")).toBeNaN();
    expect(toKobo("1.234")).toBeNaN();
  });

  it("puts kobo back in the form for typing", () => {
    expect(koboInput(2500050)).toBe("25000.50");
    expect(koboInput(2500000)).toBe("25000");
    expect(koboInput(5)).toBe("0.05");
  });
});

describe("statuses and the list", () => {
  it("words every status for people", () => {
    expect(Object.values(INVOICE_STATUS).map((s) => s.label)).toEqual(["Draft", "Sent", "Part-paid", "Paid", "Overdue", "Void"]);
    expect(invoiceStatus("paid")).toEqual({ label: "Paid", tone: "ok" });
    expect(invoiceStatus("overdue").tone).toBe("danger");
    expect(invoiceStatus("on_hold")).toEqual({ label: "on hold", tone: "muted" });
  });

  it("filters by status", () => {
    expect(INVOICE_FILTERS.map((f) => f.label)).toEqual(["All", "Draft", "Sent", "Part-paid", "Paid", "Overdue", "Void"]);
    expect(invoicesPath("")).toBe("/v1/invoices");
    expect(invoicesPath("part_paid")).toBe("/v1/invoices?status=part_paid");
    expect(invoicesPath("void")).toBe("/v1/invoices?status=void");
    // Something the server does not know is all of them, not a refusal.
    expect(invoicesPath("nonsense")).toBe("/v1/invoices");
  });

  it("puts the newest first", () => {
    const list = newestFirst([
      { n: 1, created_at: "2026-10-01T00:00:00Z" },
      { n: 3, created_at: "2026-10-03T00:00:00Z" },
      { n: 2, created_at: "2026-10-02T00:00:00Z" },
    ]);
    expect(list.map((x) => x.n)).toEqual([3, 2, 1]);
  });

  it("shows the masked customer and dates in words", () => {
    expect(invoiceCustomerLine({ name: "A•••", phone: "•••4567" })).toBe("A••• · •••4567");
    expect(invoiceCustomerLine({ name: "A•••", email: "a•••@example.ng" })).toBe("A••• · a•••@example.ng");
    expect(dateLabel("2026-11-01")).toBe("1 Nov 2026");
    expect(dateLabel(undefined)).toBe("");
  });

  it("finds an invoice by id or number", () => {
    expect(invoicePath("INV/2026-1")).toBe("/v1/invoices/INV%2F2026-1");
    expect(invoicePath(invoice.id, "/send")).toBe(`/v1/invoices/${invoice.id}/send`);
  });
});

describe("who may see and do what", () => {
  it("shows invoices with invoice.read, New invoice with invoice.manage", () => {
    expect(invoiceAccess(can("invoice.read"))).toEqual({ see: true, manage: false, reveal: false });
    expect(invoiceAccess(can("invoice.read", "invoice.manage", "pii.reveal"))).toEqual({ see: true, manage: true, reveal: true });
    expect(invoiceAccess(can()).see).toBe(false);
  });

  it("offers Edit and Send on a draft", () => {
    const a = invoiceActions("draft", can("invoice.read", "invoice.manage"));
    expect(a).toMatchObject({ edit: true, send: true, sendAgain: false, void: true, undo: true, reveal: false });
  });

  it("offers Send again once sent, part-paid or overdue", () => {
    for (const s of ["sent", "part_paid", "overdue"]) {
      expect(invoiceActions(s, can("invoice.manage"))).toMatchObject({ edit: false, send: false, sendAgain: true, void: true });
    }
  });

  it("never voids a paid or void invoice", () => {
    for (const s of ["paid", "void"]) {
      expect(invoiceActions(s, can("invoice.manage"))).toMatchObject({ edit: false, send: false, sendAgain: false, void: false });
    }
  });

  it("offers nothing that changes an invoice to someone who only reads", () => {
    for (const s of Object.keys(INVOICE_STATUS)) {
      const a = invoiceActions(s, can("invoice.read"));
      expect([a.edit, a.send, a.sendAgain, a.void, a.undo]).toEqual([false, false, false, false, false]);
    }
    expect(invoiceActions("sent", can("invoice.read", "pii.reveal")).reveal).toBe(true);
  });
});

describe("the form", () => {
  const filled = (patch: Partial<InvoiceForm> = {}): InvoiceForm => ({ ...newInvoiceForm(), name: "Ada Obi", phone: "08031234567", amount: "25,000.50", ...patch });

  it("starts from the business's settings", () => {
    expect(newInvoiceForm({ fee_paid_by: "customer", allow_part_payments: true })).toMatchObject({ feePaidBy: "customer", partPayments: true, mode: "amount" });
    expect(newInvoiceForm(null)).toMatchObject({ feePaidBy: "business", partPayments: false });
  });

  it("sends a single amount with its description", () => {
    const f = filled({ description: " Catering ", dueOn: "2026-11-01", note: " Thanks " });
    expect(invoiceProblem(f)).toBeNull();
    expect(formTotal(f)).toBe(2500050);
    expect(draftRequest(f)).toEqual({
      customer: { name: "Ada Obi", phone: "08031234567" },
      amount_kobo: 2500050,
      description: "Catering",
      due_on: "2026-11-01",
      note: "Thanks",
      fee_paid_by: "business",
      allow_part_payments: false,
    });
  });

  it("sends a list of items, with line totals and the total", () => {
    const f = filled({
      mode: "items",
      email: "ada@example.ng",
      phone: "",
      connection: " flutterwave ",
      feePaidBy: "customer",
      partPayments: true,
      items: [
        { description: "Jollof rice", quantity: "3", price: "1,500" },
        { description: "Chapman", quantity: "2", price: "750.50" },
      ],
    });
    expect(itemTotal(f.items[0] ?? emptyItem())).toBe(450000);
    expect(itemTotal(f.items[1] ?? emptyItem())).toBe(150100);
    expect(formTotal(f)).toBe(600100);
    expect(naira(formTotal(f))).toBe("₦6,001");
    expect(invoiceProblem(f)).toBeNull();
    expect(draftRequest(f)).toEqual({
      customer: { name: "Ada Obi", email: "ada@example.ng" },
      lines: [
        { description: "Jollof rice", quantity: 3, unit_kobo: 150000 },
        { description: "Chapman", quantity: 2, unit_kobo: 75050 },
      ],
      connection: "flutterwave",
      fee_paid_by: "customer",
      allow_part_payments: true,
    });
  });

  it("counts an item only once it has a quantity and price", () => {
    expect(itemTotal({ description: "x", quantity: "", price: "100" })).toBe(0);
    expect(itemTotal({ description: "x", quantity: "2", price: "abc" })).toBe(0);
    expect(itemTotal({ description: "x", quantity: "1.5", price: "100" })).toBe(0);
    expect(formTotal(filled({ amount: "" }))).toBe(0);
  });

  it("says plainly what is missing or wrong", () => {
    expect(invoiceProblem(filled({ name: " " }))).toBe("Give the customer's name.");
    expect(invoiceProblem(filled({ phone: "", email: "" }))).toMatch(/phone number or email/);
    expect(invoiceProblem(filled({ email: "ada@" }))).toMatch(/email does not look right/);
    expect(invoiceProblem(filled({ amount: "" }))).toBe("Give the amount.");
    expect(invoiceProblem(filled({ amount: "0" }))).toMatch(/number of naira/);
    expect(invoiceProblem(filled({ amount: "ten" }))).toMatch(/number of naira/);
    expect(invoiceProblem(filled({ note: "x".repeat(501) }))).toMatch(/500 characters/);
    const items = (i: { description: string; quantity: string; price: string }[]) => filled({ mode: "items", items: i });
    expect(invoiceProblem(items([]))).toBe("Add at least one item.");
    expect(invoiceProblem(items([{ description: "", quantity: "1", price: "5" }]))).toBe("The item needs a description.");
    expect(invoiceProblem(items([{ description: "a", quantity: "1", price: "5" }, { description: "b", quantity: "0", price: "5" }]))).toBe(
      "Item 2's quantity is a whole number, 1 or more.",
    );
    expect(invoiceProblem(items([{ description: "a", quantity: "1", price: "" }]))).toMatch(/price is a number of naira/);
    expect(invoiceProblem(items(Array.from({ length: 51 }, () => ({ description: "a", quantity: "1", price: "5" }))))).toMatch(/at most 50 items/);
  });

  it("words the server's refusal as a sentence", () => {
    expect(plainMessage("the customer needs a phone or an email")).toBe("The customer needs a phone or an email.");
    expect(plainMessage("Already sent.")).toBe("Already sent.");
    expect(plainMessage("")).toBe("");
  });

  it("opens a draft of one line as a single amount, and of several as items", () => {
    expect(formFromInvoice(invoice)).toMatchObject({ mode: "amount", amount: "25000.50", description: "Catering", dueOn: "2026-11-01", note: "Thank you" });
    const many = formFromInvoice({ ...invoice, lines: [{ description: "Rice", quantity: 3, unit_kobo: 150000 }, { description: "Tea", quantity: 1, unit_kobo: 20000 }] });
    expect(many.mode).toBe("items");
    expect(many.items).toEqual([
      { description: "Rice", quantity: "3", price: "1500" },
      { description: "Tea", quantity: "1", price: "200" },
    ]);
    expect(formTotal(many)).toBe(470000);
    // Round trip: the same draft goes back.
    expect(draftRequest(many).lines).toEqual([
      { description: "Rice", quantity: 3, unit_kobo: 150000 },
      { description: "Tea", quantity: 1, unit_kobo: 20000 },
    ]);
  });

  it("makes a fresh key for each form and each send", () => {
    const a = newRequestKey();
    expect(a).toMatch(/^web:/);
    expect(newRequestKey()).not.toBe(a);
  });
});

describe("sending, voiding and undoing", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("sends with an Idempotency-Key", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ invoice, link: "https://pay.taskiem.page/i/abc", queued: ["sms", "email"] }), { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    const r = await postOnce<{ link: string }>(invoicePath(invoice.id, "/send"), "web:k1");
    expect(r.link).toBe("https://pay.taskiem.page/i/abc");
    const [path, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(path).toBe(`/v1/invoices/${invoice.id}/send`);
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>)["Idempotency-Key"]).toBe("web:k1");
    expect(init.body).toBe("{}");
  });

  it("creates a draft with the form's key and shows a refusal as it comes", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ error: "the customer needs a phone or an email" }), { status: 400 }));
    vi.stubGlobal("fetch", fetch);
    const body = draftRequest({ ...newInvoiceForm(), name: "Ada", phone: "0803", amount: "100" });
    await expect(postOnce("/v1/invoices", "web:form", body)).rejects.toMatchObject({ status: 400, message: "the customer needs a phone or an email" });
    const [, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect((init.headers as Record<string, string>)["Idempotency-Key"]).toBe("web:form");
    expect(JSON.parse(init.body as string)).toMatchObject({ customer: { name: "Ada", phone: "0803" }, amount_kobo: 10000 });
  });

  it("says which channels were used", () => {
    expect(sentLines({ queued: ["sms", "email"] })).toEqual(["Sent by SMS and email."]);
    expect(sentLines({ queued: ["email"], sms_over_allowance: true })).toEqual(["Sent by email.", "SMS not sent: plan allowance used."]);
    expect(sentLines({ queued: ["sms"] })).toEqual(["Sent by SMS."]);
    expect(sentLines({ queued: null })[0]).toMatch(/^Nothing was sent/);
  });

  it("needs a reason to void", () => {
    expect(voidProblem("  ")).toMatch(/Say why/);
    expect(voidProblem("Wrong amount")).toBeNull();
    expect(voidProblem("x".repeat(501))).toMatch(/500 characters/);
  });

  it("lists payments, an undone one with its undoing", () => {
    const p = (id: string, extra: Partial<InvoicePayment> = {}): InvoicePayment => ({
      id,
      amount_kobo: 1000000,
      payment_id: `ref-${id}`,
      how: "exact",
      by: "taskiem",
      reasons: [],
      created_at: "2026-10-21T10:00:00Z",
      ...extra,
    });
    const rows = paymentRows([p("a", { undone_by: "u" }), p("b"), p("u", { undoes: "a", amount_kobo: -1000000, by: "user:ada", created_at: "2026-10-22T10:00:00Z" })]);
    expect(rows.map((r) => r.payment.id)).toEqual(["a", "b"]);
    expect(rows[0]?.undoneBy?.by).toBe("user:ada");
    expect(rows[1]?.undoneBy).toBeUndefined();
    expect(paymentRows(null)).toEqual([]);
    expect(howMatched("exact")).toBe("Paid into the account Taskiem gave");
    expect(howMatched("suggested", "user:ada")).toBe("Suggested, confirmed by user:ada");
    expect(howMatched("suggested")).toBe("Suggested, then confirmed");
    expect(howMatched("suggested_auto", "taskiem")).toBe("Matched automatically");
    expect(howMatched("manual", "user:ada")).toBe("Matched by hand");
    expect(howMatched("some_new_way")).toBe("some new way");
  });
});

describe("matching money to invoices", () => {
  const s = (id: string, entry: string, score: number, extra: Partial<MoneySuggestion> = {}): MoneySuggestion => ({
    id,
    entry_id: entry,
    invoice_id: `inv-${id}`,
    number: `INV-${id}`,
    score,
    reasons: ["same amount"],
    status: "open",
    amount_kobo: 2500000,
    channel: "paystack/main",
    reference: `REF-${entry}`,
    received_at: "2026-10-22T10:00:00Z",
    kind: "account_credit",
    ...extra,
  });

  it("words a score by the thresholds", () => {
    expect(scoreWords(100).label).toBe("Strong");
    expect(scoreWords(85)).toEqual({ label: "Strong", tone: "ok" });
    expect(scoreWords(84).label).toBe("Likely");
    expect(scoreWords(60).label).toBe("Likely");
    expect(scoreWords(59)).toEqual({ label: "Possible", tone: "muted" });
    expect(scoreWords(0).label).toBe("Possible");
  });

  it("groups suggestions by payment, newest payment first, strongest candidate first", () => {
    const g = groupSuggestions([s("1", "e2", 55), s("2", "e1", 70), s("3", "e2", 92), s("4", "e1", 40)]);
    expect(g.map((p) => p.entry_id)).toEqual(["e2", "e1"]);
    expect(g[0]?.candidates.map((c) => c.number)).toEqual(["INV-3", "INV-1"]);
    expect(g[1]?.candidates.map((c) => c.score)).toEqual([70, 40]);
    expect(g[0]?.reference).toBe("REF-e2");
    expect(groupSuggestions(null)).toEqual([]);
  });

  it("words where the money came from and what it was", () => {
    expect(channelWords("paystack/main")).toBe("Paystack (main)");
    expect(channelWords("opay/shop/2")).toBe("Opay (shop/2)");
    expect(channelWords("upload")).toBe("Uploaded statement");
    expect(channelWords("cash")).toBe("Cash");
    expect(channelWords("moniepoint")).toBe("Moniepoint");
    expect(kindWords("account_credit")).toBe("Transfer into an account");
    expect(kindWords("pos_sale")).toBe("POS sale");
    expect(kindWords("something_else")).toBe("something else");
    expect(amountWords(null)).toBe("Amount not known");
    expect(amountWords(2500000)).toBe(naira(2500000));
  });

  it("offers only open invoices to match by hand", () => {
    const x = (status: string) => ({ id: status, number: status, status }) as unknown as CustomerInvoice;
    expect(openInvoices(["draft", "sent", "part_paid", "paid", "overdue", "void"].map(x)).map((i) => i.status)).toEqual(["sent", "part_paid", "overdue"]);
    expect(openInvoices(undefined)).toEqual([]);
  });

  it("names the routes and says what happened", () => {
    expect(suggestionPath("a/b", "confirm")).toBe("/v1/money/suggestions/a%2Fb/confirm");
    expect(suggestionPath("x", "reject")).toBe("/v1/money/suggestions/x/reject");
    expect(entryMatchPath("e1")).toBe("/v1/money/entries/e1/match");
    expect(matchedLine(2500000, "INV-0003")).toMatch(/matched to INV-0003\. If that is wrong, undo it on the invoice\.$/);
    expect(rejectedLine("INV-0004")).toBe("INV-0004 will not be suggested for this payment again.");
  });
});

describe("settings", () => {
  const saved = { prefix: "INV-", next_number: 7, connection: "main", fee_paid_by: "business" as const, allow_part_payments: false };

  it("round-trips the settings", () => {
    const f = settingsForm(saved);
    expect(settingsProblem(f, saved)).toBeNull();
    expect(f.autoMatch).toBe(false);
    expect(settingsRequest({ ...f, nextNumber: "10", connection: " ", partPayments: true })).toEqual({ ...saved, next_number: 10, allow_part_payments: true, auto_match: false });
  });

  it("sends every field it read, automatic matching too", () => {
    const on = { ...saved, auto_match: true };
    const f = settingsForm(on);
    expect(f.autoMatch).toBe(true);
    const body = settingsRequest(f);
    expect(body).toEqual(on);
    expect(Object.keys(body).sort()).toEqual(["allow_part_payments", "auto_match", "connection", "fee_paid_by", "next_number", "prefix"]);
    expect(settingsRequest({ ...f, autoMatch: false }).auto_match).toBe(false);
    expect(numberPreview("INV-", 7)).toBe("INV-0007");
    expect(numberPreview("A/", 12345)).toBe("A/12345");
  });

  it("only moves the next number forward", () => {
    const f = settingsForm(saved);
    expect(settingsProblem({ ...f, nextNumber: "6" }, saved)).toMatch(/only move forward.*7 or more/);
    expect(settingsProblem({ ...f, nextNumber: "7" }, saved)).toBeNull();
    expect(settingsProblem({ ...f, nextNumber: "1.5" }, saved)).toMatch(/whole number/);
  });

  it("holds the prefix to what the server takes", () => {
    const f = settingsForm(saved);
    expect(settingsProblem({ ...f, prefix: "ABCDEFGHIJKLM" }, saved)).toMatch(/12 characters/);
    expect(settingsProblem({ ...f, prefix: "IN V" }, saved)).toMatch(/letters, digits/);
    expect(settingsProblem({ ...f, prefix: "INV/2026_" }, saved)).toBeNull();
  });
});

describe("payments left to match by hand", () => {
  it("become cards with nothing suggested", () => {
    const u = { entry_id: "e9", amount_kobo: 120000, channel: "paystack/main", reference: "R-9", received_at: "2026-10-23T10:00:00Z", kind: "payment" };
    expect(unmatchedPayments([u])).toEqual([{ ...u, candidates: [] }]);
    expect(unmatchedPayments(null)).toEqual([]);
    expect(UNMATCHED_PATH).toBe("/v1/money/unmatched");
  });
});

describe("matching money to storefront orders", () => {
  const order = (id: string, extra: Partial<MoneySuggestion> = {}): MoneySuggestion => ({
    id,
    entry_id: "e1",
    target_kind: "order",
    target: `order:AK-${id}`,
    invoice_id: null,
    order_id: `ord-${id}`,
    number: `AK-${id}`,
    due_kobo: 300000,
    customer: "B•••",
    score: 90,
    reasons: ["the reference mentions AK-" + id],
    status: "open",
    amount_kobo: 300000,
    channel: "paystack/main",
    reference: "TRF",
    received_at: "2026-10-22T10:00:00Z",
    kind: "account_credit",
    ...extra,
  });
  const inv = { id: "i1", number: "INV-0001", status: "sent", due_kobo: 250000, customer: { name: "A•••" } } as unknown as CustomerInvoice;
  const open: MoneyOpenOrder[] = [
    { target_kind: "order", target: "order:AK-1", number: "AK-1", due_kobo: 300000, customer: "B•••", placed_at: "2026-10-22T09:00:00Z" },
    { target_kind: "order", target: "order:AK-2", number: "AK-2", due_kobo: 120000, customer: "C•••", placed_at: "2026-10-22T09:00:00Z" },
  ];

  it("names an order and links to it", () => {
    const o = order("1");
    expect(isOrder(o)).toBe(true);
    expect(targetLabel(o)).toBe("Order AK-1");
    expect(targetPath(o)).toBe("/storefront/orders/AK-1");
    const i = { target_kind: "invoice" as const, number: "INV-0001" };
    expect(isOrder(i)).toBe(false);
    expect(targetLabel(i)).toBe("INV-0001");
    expect(targetPath(i)).toBe("/invoices/INV-0001");
    // A suggestion with no kind is an invoice's.
    expect(targetLabel({ number: "INV-0002" })).toBe("INV-0002");
  });

  it("offers invoices and orders by hand, leaving out what is suggested", () => {
    const c = matchChoices([inv], open, [order("1")]);
    expect(c.map((x) => x.value)).toEqual(["i1", "order:AK-2"]);
    expect(c[1]).toEqual({ value: "order:AK-2", kind: "order", number: "AK-2", label: `Order AK-2 · ${naira(120000)} to pay · C•••`, due_kobo: 120000 });
    expect(matchChoices([inv], null, [])).toHaveLength(1);
    expect(matchRequest("order:AK-2")).toEqual({ order: "AK-2" });
    expect(matchRequest("i1")).toEqual({ invoice: "i1" });
  });

  it("words the picker for invoices only until there are orders", () => {
    expect(pickerWords(false).summary).toBe("Match to another invoice…");
    expect(pickerWords(false).label).toBe("Invoice");
    expect(pickerWords(true).summary).toBe("Match to another invoice or order…");
    expect(pickerWords(true).submit).toBe("Match to this one");
  });

  it("says what happened to an order", () => {
    expect(matchedLine(300000, "AK-1", "order")).toMatch(/matched to order AK-1\. If that is wrong, undo it on the order\.$/);
    expect(rejectedLine("AK-1", "order")).toBe("Order AK-1 will not be suggested for this payment again.");
    expect(orderPaymentUndoPath("p/1")).toBe("/v1/money/order-payments/p%2F1/undo");
  });

  it("lists an order's matches with their undoings folded in", () => {
    const p = { id: "p1", entry_id: "e1", amount_kobo: 300000, how: "suggested", by: "user:ada", reasons: [], undoable: true, created_at: "2026-10-22T10:00:00Z" };
    const rows = orderPaymentRows([{ ...p, undone_by: "p2", undoable: false }, { ...p, id: "p2", undoes: "p1", how: "manual" }, { ...p, id: "p3", how: "exact" }]);
    expect(rows.map((r) => [r.payment.id, r.undone])).toEqual([
      ["p1", true],
      ["p3", false],
    ]);
    expect(howMatchedOrder("exact")).toBe("Paid through the order's checkout page");
    expect(howMatchedOrder("suggested", "user:ada")).toBe("Suggested, confirmed by user:ada");
    expect(orderPaymentRows(undefined)).toEqual([]);
  });
});

describe("invoiceHero", () => {
  const inv = (status: string, total: number, paid: number) => ({ status, total_kobo: total, paid_kobo: paid, due_kobo: total - paid });
  it("leads with what is still due, and how much is paid", () => {
    expect(invoiceHero(inv("part_paid", 2000000, 500000))).toEqual({ label: "Still due", kobo: 1500000, sub: "of ₦20,000 · ₦5,000 paid", paid: 0.25 });
    expect(invoiceHero(inv("overdue", 100000, 0)).label).toBe("Overdue");
  });
  it("says what a draft, a paid and a void invoice are", () => {
    expect(invoiceHero(inv("draft", 100000, 0))).toMatchObject({ label: "To ask for", kobo: 100000, sub: "Not sent yet" });
    expect(invoiceHero(inv("paid", 100000, 100000))).toMatchObject({ label: "Paid in full", kobo: 100000, paid: 1 });
    expect(invoiceHero(inv("void", 100000, 0)).sub).toBe("It takes no payment");
    expect(invoiceHero(inv("void", 100000, 40000)).sub).toBe("₦400 was paid before it was voided");
  });
  it("never shows more than all of it paid, or a share of nothing", () => {
    expect(invoiceHero(inv("sent", 0, 0)).paid).toBe(0);
    expect(invoiceHero({ status: "sent", total_kobo: 100, paid_kobo: 150, due_kobo: 0 }).paid).toBe(1);
  });
});

describe("giving a customer an account number", () => {
  const iswallet: AccountIssuer = { connection: "iswallet/main", rail: "iswallet", needs_email: false, needs_bvn: false, needs_account: true, names_itself: true };
  const flw: AccountIssuer = { connection: "flutterwave/main", rail: "Flutterwave", needs_email: true, needs_bvn: true, needs_account: false, names_itself: false };

  it("starts from the first issuer, else by hand", () => {
    expect(newAccountForm([iswallet, flw])).toMatchObject({ by_hand: false, connection: "iswallet/main" });
    expect(newAccountForm([])).toMatchObject({ by_hand: true, connection: "" });
  });

  it("says what each issuer needs", () => {
    expect(issuerWords(iswallet)).toBe("iswallet (iswallet/main): iswallet names the account itself; needs the wallet the number pays into.");
    expect(issuerWords(flw)).toBe("Flutterwave (flutterwave/main): named for your business and the customer; needs the customer's email, their BVN or NIN (typed in, never kept).");
  });

  it("checks the form and builds the request", () => {
    const f = newAccountForm([iswallet, flw]);
    expect(accountProblem(f, [iswallet, flw])).toBe("iswallet needs the wallet the number pays into.");
    expect(accountProblem({ ...f, account: "wal_1" }, [iswallet, flw])).toBeUndefined();
    expect(accountRequest({ ...f, account: " wal_1 " })).toEqual({ connection: "iswallet/main", issue: true, account: "wal_1" });
    expect(accountProblem({ ...f, connection: "flutterwave/main" }, [iswallet, flw])).toBe("Flutterwave needs the customer's BVN or NIN (11 digits).");
    expect(accountRequest({ ...f, connection: "flutterwave/main", nin: "12345678901" })).toEqual({ connection: "flutterwave/main", issue: true, nin: "12345678901" });
    expect(accountProblem({ ...f, connection: "opay/main" }, [iswallet, flw])).toBe("Choose the connection to issue the account on.");
    const hand = { ...newAccountForm([]), connection: "paystack/main", account_number: "1234567890", reference: "CUS_ada", bank_name: "Titan" };
    expect(accountProblem({ ...hand, connection: "paystack" }, [])).toMatch(/like paystack\/main/);
    expect(accountProblem({ ...hand, account_number: "12" }, [])).toMatch(/4 to 40/);
    expect(accountProblem({ ...hand, reference: "" }, [])).toMatch(/reference is required/);
    expect(accountProblem(hand, [])).toBeUndefined();
    expect(accountRequest(hand)).toEqual({ connection: "paystack/main", account_number: "1234567890", reference: "CUS_ada", bank_name: "Titan" });
  });

  it("words an account", () => {
    expect(accountWords({ id: "a", channel: "iswallet/main", account_number: "9900000001", bank_name: "Wema Bank", reference: "tsk_ab12" })).toBe(
      "9900000001 · Wema Bank (iswallet/main), reference tsk_ab12",
    );
    expect(accountWords({ id: "a", channel: "paystack/main", account_number: "1234567890", reference: "CUS_ada" })).toBe("1234567890 (paystack/main), reference CUS_ada");
  });
});
