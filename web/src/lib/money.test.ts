import { describe, expect, it } from "vitest";
import {
  COMMON_COLUMNS,
  EXPORTS,
  addedLine,
  counted,
  exportFileName,
  exportPath,
  feeRatePercent,
  isDay,
  lagosDate,
  periodChoice,
  periodFor,
  periodProblem,
  periodQuery,
  periodWords,
  summaryPath,
  bpToPercent,
  channelProblem,
  closeProblem,
  closeRequest,
  closedLine,
  columnsForm,
  columnsProblem,
  columnsRequest,
  csvHeader,
  emptyClose,
  emptyEntry,
  emptyFee,
  enteredLine,
  entryProblem,
  entryRequest,
  feeForm,
  feeProblem,
  feeRequest,
  knownChannels,
  leakFilter,
  leakKindWords,
  leakPath,
  leakSentence,
  leakStatus,
  leaksPath,
  moneyAccess,
  onlyOptionalMissing,
  percentToBp,
  rateWords,
  statementFormatPath,
  statementRefusal,
  statementRequest,
  today,
  whoWords,
  type ColumnsForm,
  type EntryForm,
} from "./money";

const can =
  (...perms: string[]) =>
  (p: string) =>
    perms.includes(p);

describe("access", () => {
  it("follows the invoice permissions", () => {
    expect(moneyAccess(can("invoice.read"))).toEqual({ see: true, manage: false });
    expect(moneyAccess(can("invoice.read", "invoice.manage"))).toEqual({ see: true, manage: true });
    expect(moneyAccess(can())).toEqual({ see: false, manage: false });
  });
});

describe("leaks", () => {
  it("names each kind in words", () => {
    expect(leakKindWords("unmatched")).toBe("Money with no invoice");
    expect(leakKindWords("overdue")).toBe("Overdue invoice");
    expect(leakKindWords("duplicate")).toBe("Recorded twice");
    expect(leakKindWords("overpaid")).toBe("Invoice paid too much");
    expect(leakKindWords("void_paid")).toBe("Paid for a void invoice");
    expect(leakKindWords("fee")).toBe("Fee above the agreed rate");
    expect(leakKindWords("short_settlement")).toBe("short settlement");
  });

  it("names statuses, and a filter falls back to open", () => {
    expect(leakStatus("resolved").label).toBe("Resolved by itself");
    expect(leakStatus("written_off")).toEqual({ label: "Written off", tone: "muted" });
    expect(leakFilter(null)).toBe("open");
    expect(leakFilter("closed")).toBe("closed");
    expect(leakFilter("bogus")).toBe("open");
    expect(leaksPath("all")).toBe("/v1/money/leaks?status=all");
    expect(leaksPath("")).toBe("/v1/money/leaks?status=open");
    expect(leakPath("a b", "close")).toBe("/v1/money/leaks/a%20b/close");
  });

  it("reads the detail as a sentence", () => {
    expect(leakSentence("a fee of ₦500 on ₦10,000 through paystack/main, where the agreed rate makes ₦150")).toBe(
      "A fee of ₦500 on ₦10,000 through paystack/main, where the agreed rate makes ₦150.",
    );
  });

  it("says who closed a leak, and how", () => {
    const names = new Map([["u-2", "Bola Ade"]]);
    expect(whoWords("u-1", names, "u-1")).toBe("you");
    expect(whoWords("u-2", names, "u-1")).toBe("Bola Ade");
    expect(whoWords("u-3", names, "u-1")).toBe("a member");
    expect(whoWords("system:leaks", names)).toBe("Taskiem");
    expect(whoWords("key:k", names)).toBe("an API key");
    expect(whoWords(undefined, names)).toBe("");
    expect(closedLine({ status: "recovered", recovered_kobo: 2000000, closed_by: "u-2" }, names)).toBe("Recovered ₦20,000, closed by Bola Ade");
    expect(closedLine({ status: "written_off", closed_by: "u-1" }, names, "u-1")).toBe("Written off, closed by you");
    expect(closedLine({ status: "not_a_leak" }, names)).toBe("Not a leak");
    expect(closedLine({ status: "resolved", closed_by: "system:leaks" }, names)).toMatch(/^Closed by itself/);
    expect(closedLine({ status: "open" }, names)).toBe("");
  });

  it("checks the close form and builds its request", () => {
    expect(closeProblem(emptyClose())).toBe("Choose how it was closed.");
    expect(closeProblem({ status: "recovered", recovered: "", note: "" })).toBe("Say how much was recovered.");
    expect(closeProblem({ status: "recovered", recovered: "abc", note: "" })).toMatch(/number of naira/);
    expect(closeProblem({ status: "recovered", recovered: "0", note: "" })).toMatch(/number of naira/);
    expect(closeProblem({ status: "written_off", recovered: "", note: "x".repeat(501) })).toMatch(/at most 500/);
    expect(closeProblem({ status: "recovered", recovered: "₦20,000.50", note: "" })).toBeNull();
    expect(closeRequest({ status: "recovered", recovered: "₦20,000.50", note: " paid back " })).toEqual({ status: "recovered", recovered_kobo: 2000050, note: "paid back" });
    // A recovered amount typed before choosing another way is not sent.
    expect(closeRequest({ status: "not_a_leak", recovered: "500", note: "" })).toEqual({ status: "not_a_leak" });
  });
});

describe("statements", () => {
  it("reads a CSV file's header", () => {
    expect(csvHeader("\uFEFFTrans Date,Narration, Debit ,Credit,Balance\r\n01/10/2026,x,,100,100")).toEqual(["Trans Date", "Narration", "Debit", "Credit", "Balance"]);
    expect(csvHeader('"Date","Description, in full","Amount ""NGN"""\n1,2,3')).toEqual(["Date", "Description, in full", 'Amount "NGN"']);
    expect(csvHeader("\n\nDate,Amount,,Date\n")).toEqual(["Date", "Amount"]);
    expect(csvHeader('"Line\nbreak",B')).toEqual(["Line\nbreak", "B"]);
    expect(csvHeader("")).toEqual([]);
  });

  it("guesses a bank's columns", () => {
    const f = columnsForm(["Trans Date", "Value Date", "Narration", "Reference", "Debit", "Credit", "Balance"]);
    expect(f).toEqual({ date: "Trans Date", description: "Narration", split: true, amount: "", in: "Credit", out: "Debit", reference: "Reference", balance: "Balance" });
    const g = columnsForm(["Date", "Details", "Amount"]);
    expect(g).toMatchObject({ date: "Date", description: "Details", split: false, amount: "Amount", reference: "", balance: "" });
    expect(columnsForm(["Money In", "Money Out", "When", "What"])).toMatchObject({ split: true, in: "Money In", out: "Money Out", date: "", description: "" });
  });

  it("prefers the channel's saved matching where the file has those columns", () => {
    const f = columnsForm(["posted", "memo", "amt", "Date"], { date: "Posted", description: "memo", amount: "amt" });
    expect(f).toMatchObject({ date: "posted", description: "memo", amount: "amt", split: false });
    // Saved columns the file lacks are guessed instead.
    expect(columnsForm(["Date", "Narration", "Amount"], { date: "Txn Date", description: "Remarks", in: "Cr", out: "Dr" })).toMatchObject({
      date: "Date",
      description: "Narration",
      split: false,
    });
  });

  it("checks the matching and builds the columns sent", () => {
    const f: ColumnsForm = { date: "D", description: "N", split: false, amount: "A", in: "", out: "", reference: "", balance: "" };
    expect(columnsProblem({ ...f, date: "" })).toMatch(/date/);
    expect(columnsProblem({ ...f, description: "" })).toMatch(/what each line was/);
    expect(columnsProblem({ ...f, amount: "" })).toMatch(/amount/);
    expect(columnsProblem({ ...f, split: true })).toMatch(/money in/);
    expect(columnsProblem({ ...f, split: true, in: "C" })).toMatch(/money out/);
    expect(columnsProblem({ ...f, reference: "A" })).toMatch(/one thing only/);
    expect(columnsProblem(f)).toBeNull();
    expect(columnsRequest(f)).toEqual({ date: "D", description: "N", amount: "A" });
    expect(columnsRequest({ ...f, split: true, in: "C", out: "Db", reference: "R", balance: "B" })).toEqual({ date: "D", description: "N", in: "C", out: "Db", reference: "R", balance: "B" });
    expect(statementRequest(" gtbank/current ", "csv")).toEqual({ channel: "gtbank/current", csv: "csv" });
    expect(statementRequest("gtbank/current", "csv", { date: "D", description: "N", amount: "A" })).toEqual({ channel: "gtbank/current", csv: "csv", columns: { date: "D", description: "N", amount: "A" } });
    expect(statementFormatPath("gtbank/current")).toBe("/v1/money/statement-format?channel=gtbank%2Fcurrent");
  });

  it("reads the server's refusal", () => {
    const body = { error: "the file has no column \"date\"", code: "statement_unreadable", missing: ["date"], problems: null };
    expect(statementRefusal(422, body, "x")).toEqual({ message: 'The file has no column "date".', missing: ["date"], problems: [] });
    expect(statementRefusal(422, { error: "2 lines cannot be read; nothing was added", code: "statement_unreadable", missing: null, problems: [{ row: 3, why: "the amount is zero" }] }, "x")).toEqual({
      message: "2 lines cannot be read; nothing was added.",
      missing: [],
      problems: [{ row: 3, why: "the amount is zero" }],
    });
    expect(statementRefusal(400, body, "x")).toBeNull();
    expect(statementRefusal(422, { code: "other" }, "x")).toBeNull();
  });

  it("knows when only an optional column is missing", () => {
    expect(onlyOptionalMissing(["reference"], COMMON_COLUMNS)).toBe(true);
    expect(onlyOptionalMissing(["Ref No"], { date: "D", description: "N", amount: "A", reference: "ref no" })).toBe(true);
    expect(onlyOptionalMissing(["date", "reference"], COMMON_COLUMNS)).toBe(false);
    expect(onlyOptionalMissing([], COMMON_COLUMNS)).toBe(false);
    expect(onlyOptionalMissing(["reference"], { date: "D", description: "N", amount: "A" })).toBe(false);
  });

  it("checks an account's name", () => {
    expect(channelProblem("gtbank/current")).toBeNull();
    expect(channelProblem("cash")).toBeNull();
    expect(channelProblem("")).toMatch(/Name the account/);
    expect(channelProblem("", true)).toBeNull();
    expect(channelProblem("GTBank Current")).toMatch(/small letters/);
    expect(channelProblem("a/b/c")).toMatch(/small letters/);
  });

  it("says what an upload added", () => {
    expect(addedLine({ added: 3, skipped: 0 })).toBe("Added 3 lines, skipped 0 already in the log.");
    expect(addedLine({ added: 1, skipped: 2 })).toBe("Added 1 line, skipped 2 already in the log.");
  });

  it("suggests the accounts already used", () => {
    expect(knownChannels(["paystack/main", "cash"], undefined, ["gtbank/current", "paystack/main", null])).toEqual(["gtbank/current", "paystack/main"]);
  });
});

describe("cash and expenses", () => {
  const now = new Date(2026, 9, 23, 10);
  const base: EntryForm = { ...emptyEntry(now), amount: "12,000", description: "Diesel" };

  it("starts on today", () => {
    expect(today(now)).toBe("2026-10-23");
    expect(emptyEntry(now)).toMatchObject({ kind: "expense", date: "2026-10-23" });
  });

  it("checks the form", () => {
    expect(entryProblem({ ...base, amount: "" }, now)).toBe("Give the amount.");
    expect(entryProblem({ ...base, amount: "-5" }, now)).toMatch(/number of naira/);
    expect(entryProblem({ ...base, description: " " }, now)).toBe("Say what it was for.");
    expect(entryProblem({ ...base, date: "2026-10-26" }, now)).toBe("The date is in the future.");
    expect(entryProblem({ ...base, date: "2026-10-24" }, now)).toBeNull();
    expect(entryProblem({ ...base, channel: "GT Bank" }, now)).toMatch(/small letters/);
    expect(entryProblem({ ...base, vendor: "x".repeat(121) }, now)).toMatch(/vendor/);
    expect(entryProblem({ ...base, kind: "cash_in", vendor: "x".repeat(121) }, now)).toBeNull();
    expect(entryProblem(base, now)).toBeNull();
  });

  it("builds the request for each kind", () => {
    expect(entryRequest({ ...base, vendor: " Total ", category: "Fuel", channel: "" })).toEqual({
      kind: "expense",
      amount_kobo: 1200000,
      occurred_at: "2026-10-23",
      description: "Diesel",
      vendor: "Total",
      category: "Fuel",
    });
    expect(entryRequest({ ...base, kind: "cash_in", vendor: "ignored", channel: "petty-cash" })).toEqual({
      kind: "cash",
      direction: "in",
      amount_kobo: 1200000,
      occurred_at: "2026-10-23",
      description: "Diesel",
      channel: "petty-cash",
    });
    expect(entryRequest({ ...base, kind: "cash_out", date: "" })).toMatchObject({ kind: "cash", direction: "out" });
    expect(entryRequest({ ...base, date: "" })).not.toHaveProperty("occurred_at");
  });

  it("says what was entered", () => {
    expect(enteredLine(base, true)).toBe("Expense of ₦12,000 added to the money log.");
    expect(enteredLine({ ...base, kind: "cash_in" }, false)).toMatch(/^Cash in of ₦12,000 was already in the money log/);
  });
});

describe("fees", () => {
  it("turns percents into basis points and back", () => {
    expect(percentToBp("1.5")).toBe(150);
    expect(percentToBp("1.5 %")).toBe(150);
    expect(percentToBp("0.05")).toBe(5);
    expect(percentToBp("100")).toBe(10000);
    expect(percentToBp("")).toBeUndefined();
    expect(percentToBp("100.01")).toBeNaN();
    expect(percentToBp("1.555")).toBeNaN();
    expect(percentToBp("-1")).toBeNaN();
    expect(bpToPercent(150)).toBe("1.5");
    expect(bpToPercent(100)).toBe("1");
    expect(bpToPercent(5)).toBe("0.05");
    expect(bpToPercent(125)).toBe("1.25");
    expect(bpToPercent(0)).toBe("0");
    for (const bp of [0, 1, 10, 99, 150, 1234, 10000]) expect(percentToBp(bpToPercent(bp))).toBe(bp);
  });

  it("says a rate in words", () => {
    expect(rateWords({ channel: "p", percent_bp: 150, flat_kobo: 10000, cap_kobo: 200000 })).toBe("1.5% + ₦100, at most ₦2,000");
    expect(rateWords({ channel: "p", percent_bp: 0, flat_kobo: 5000 })).toBe("₦50");
    expect(rateWords({ channel: "p", percent_bp: 0, flat_kobo: 0, cap_kobo: null })).toBe("0%");
  });

  it("checks the form and builds the request", () => {
    expect(feeProblem(emptyFee())).toMatch(/Name the connection/);
    expect(feeProblem({ ...emptyFee(), channel: "paystack/main" })).toMatch(/percent, the flat fee, or both/);
    expect(feeProblem({ channel: "paystack/main", percent: "abc", flat: "", cap: "" })).toMatch(/0 to 100/);
    expect(feeProblem({ channel: "paystack/main", percent: "1.5", flat: "x", cap: "" })).toMatch(/flat fee/);
    expect(feeProblem({ channel: "paystack/main", percent: "1.5", flat: "", cap: "-1" })).toMatch(/cap/);
    expect(feeProblem({ channel: "paystack/main", percent: "1.5", flat: "100", cap: "2,000" })).toBeNull();
    expect(feeRequest({ channel: " paystack/main ", percent: "1.5", flat: "100", cap: "2,000" })).toEqual({ channel: "paystack/main", percent_bp: 150, flat_kobo: 10000, cap_kobo: 200000 });
    expect(feeRequest({ channel: "opay/main", percent: "", flat: "50", cap: "" })).toEqual({ channel: "opay/main", percent_bp: 0, flat_kobo: 5000 });
    expect(feeForm({ channel: "paystack/main", percent_bp: 150, flat_kobo: 10000, cap_kobo: 200000 })).toEqual({ channel: "paystack/main", percent: "1.5", flat: "100", cap: "2000" });
    expect(feeForm({ channel: "x", percent_bp: 150, flat_kobo: 0 })).toEqual({ channel: "x", percent: "1.5", flat: "", cap: "" });
  });
});

describe("overview", () => {
  // 23:30 UTC on Sunday 1 Nov 2026 is 00:30 on Monday 2 Nov in Lagos.
  const lateSunday = new Date("2026-11-01T23:30:00Z");
  // Friday 23 Oct 2026, midday in Lagos.
  const friday = new Date("2026-10-23T11:00:00Z");

  it("reads each choice as Lagos days, to inclusive", () => {
    expect(periodFor("today", lateSunday)).toEqual({ from: "2026-11-02", to: "2026-11-02" });
    expect(periodFor("yesterday", lateSunday)).toEqual({ from: "2026-11-01", to: "2026-11-01" });
    expect(periodFor("week", lateSunday)).toEqual({ from: "2026-11-02", to: "2026-11-02" });
    expect(periodFor("week", friday)).toEqual({ from: "2026-10-19", to: "2026-10-23" });
    expect(periodFor("month", friday)).toEqual({ from: "2026-10-01", to: "2026-10-23" });
    expect(periodFor("last_month", friday)).toEqual({ from: "2026-09-01", to: "2026-09-30" });
    expect(periodFor("last_month", new Date("2026-03-10T12:00:00Z"))).toEqual({ from: "2026-02-01", to: "2026-02-28" });
    expect(periodFor("last_month", new Date("2027-01-05T12:00:00Z"))).toEqual({ from: "2026-12-01", to: "2026-12-31" });
    expect(periodFor("custom", friday, { from: "2026-10-01", to: "2026-10-05" })).toEqual({ from: "2026-10-01", to: "2026-10-05" });
    expect(lagosDate(lateSunday)).toBe("2026-11-02");
  });

  it("keeps the picker's choice to the known ones", () => {
    expect(periodChoice(null)).toBe("today");
    expect(periodChoice("month")).toBe("month");
    expect(periodChoice("decade")).toBe("today");
  });

  it("checks chosen dates as the server does", () => {
    expect(periodProblem({ from: "", to: "" })).toMatch(/first day/);
    expect(periodProblem({ from: "2026-02-30", to: "2026-03-01" })).toMatch(/does not look right/);
    expect(periodProblem({ from: "2026-10-01", to: "" })).toMatch(/last day/);
    expect(periodProblem({ from: "2026-10-05", to: "2026-10-01" })).toMatch(/on or after/);
    expect(periodProblem({ from: "2026-01-01", to: "2027-02-04" })).toBeNull(); // 400 days
    expect(periodProblem({ from: "2026-01-01", to: "2027-02-05" })).toMatch(/at most 400 days/);
    expect(periodProblem({ from: "2026-10-01", to: "2026-10-01" })).toBeNull();
    expect(isDay("2026-10-01")).toBe(true);
    expect(isDay("2026-13-01")).toBe(false);
  });

  it("builds the summary and export URLs", () => {
    const p = { from: "2026-10-01", to: "2026-10-31" };
    expect(periodQuery(p)).toBe("from=2026-10-01&to=2026-10-31");
    expect(summaryPath(p)).toBe("/v1/money/summary?from=2026-10-01&to=2026-10-31");
    expect(exportPath("journal", p)).toBe("/v1/money/exports/journal?from=2026-10-01&to=2026-10-31");
    expect(exportPath("invoices", p, true)).toBe("/v1/money/exports/invoices?from=2026-10-01&to=2026-10-31&reveal=true");
    expect(exportPath("log", p, true)).toBe("/v1/money/exports/log?from=2026-10-01&to=2026-10-31");
    expect(exportFileName("journal", p)).toBe("journal-2026-10-01-to-2026-10-31.csv");
    expect(EXPORTS.map((e) => e.kind)).toEqual(["log", "invoices", "journal"]);
    expect(EXPORTS.find((e) => e.kind === "journal")?.help).toMatch(/debits equal credits/);
  });

  it("words the period, the fee rate and counts", () => {
    expect(periodWords({ from: "2026-10-23", to: "2026-10-23" })).toBe("23 Oct 2026");
    expect(periodWords({ from: "2026-10-01", to: "2026-10-23" })).toBe("1 Oct 2026 to 23 Oct 2026");
    expect(feeRatePercent(150)).toBe("1.50%");
    expect(feeRatePercent(7)).toBe("0.07%");
    expect(feeRatePercent(0)).toBe("0.00%");
    expect(feeRatePercent(undefined)).toBe("");
    expect(feeRatePercent(null)).toBe("");
    expect(counted(1, "invoice")).toBe("1 invoice");
    expect(counted(3, "invoice")).toBe("3 invoices");
    expect(counted(2, "leak")).toBe("2 leaks");
  });
});
