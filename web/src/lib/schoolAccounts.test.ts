import { describe, expect, it } from "vitest";
import {
  accountStatus,
  feeLine,
  idProblem,
  issuingProblem,
  issuingRequest,
  jobLine,
  jobPercent,
  nameNote,
  planSummary,
  railOf,
  spacedNumber,
  type SchoolIssuer,
} from "./schoolAccounts";

const wallet: SchoolIssuer = {
  connection: "iswallet/main",
  rail: "iswallet",
  requirement: "Needs the director's BVN.",
  fee: "No fee per account is documented.",
  names_itself: true,
  needs_account: true,
  look_back: true,
  webhook: false,
};
const paystack: SchoolIssuer = {
  connection: "paystack/main",
  rail: "Paystack",
  requirement: "An email each.",
  names_itself: true,
  one_account_per_email: true,
  needs_account: false,
  look_back: true,
  webhook: true,
};

describe("the issuing connection", () => {
  it("needs the wallet where the rail does, and sends it only there", () => {
    expect(issuingProblem({ connection: "" }, [wallet])).toBeNull();
    expect(issuingProblem({ connection: "iswallet/main" }, [wallet])).toBe("iswallet needs the wallet the numbers pay into.");
    expect(issuingProblem({ connection: "iswallet/main", account: " wal_1 " }, [wallet])).toBeNull();
    expect(issuingProblem({ connection: "monnify/main" }, [wallet])).toBe("Choose one of the connections listed.");
    expect(
      issuingRequest(
        {
          connection: "iswallet/main",
          account: " wal_1 ",
          siblings_share_credit: true,
        },
        [wallet, paystack],
      ),
    ).toEqual({
      connection: "iswallet/main",
      account: "wal_1",
      siblings_share_credit: true,
    });
    expect(issuingRequest({ connection: "paystack/main", account: "wal_1" }, [wallet, paystack])).toEqual({ connection: "paystack/main", siblings_share_credit: false });
  });
  it("says the fee, or that none is documented", () => {
    expect(feeLine(wallet)).toBe("No fee per account is documented.");
    expect(feeLine(paystack)).toBe("Paystack documents no fee per account.");
  });
});

describe("a batch", () => {
  it("words its plan", () => {
    expect(
      planSummary({
        to_issue: 2,
        already_issued: 1,
        active: 3,
        later: 0,
        cannot: [],
        issuer: wallet,
      }),
    ).toBe("This issues 2 account numbers on iswallet (iswallet/main). 1 student of 3 already has one.");
    expect(
      planSummary({
        to_issue: 500,
        already_issued: 0,
        active: 640,
        later: 120,
        cannot: [{ student_id: "s", admission_number: "A", reason: "x" }],
        issuer: paystack,
      }),
    ).toBe(
      "This issues 500 account numbers on Paystack (paystack/main). 120 more students can be issued by the next batch (at most 500 at a time). 1 student cannot be issued yet: see why below.",
    );
    expect(
      planSummary({
        to_issue: 0,
        already_issued: 3,
        active: 3,
        later: 0,
        cannot: [],
        issuer: wallet,
      }),
    ).toBe("Every student who can have an account number on iswallet has one or is waiting. 3 students of 3 already have one.");
  });
  it("words its progress", () => {
    const j = {
      status: "running" as const,
      total: 4,
      issued: 1,
      failed: 1,
      unknown: 0,
      pending: 2,
    };
    expect(jobLine(j)).toBe("Issuing: 2 of 4 done, 1 account issued so far.");
    expect(jobPercent(j)).toBe(50);
    expect(jobLine({ ...j, status: "done", issued: 2, unknown: 1, pending: 0 })).toBe("Done: 2 accounts issued of 4. 1 student not issued. 1 student to check with the rail.");
    expect(jobPercent({ total: 0, issued: 0, failed: 0, unknown: 0 })).toBe(100);
  });
});

describe("a student's account", () => {
  it("says how it stands and what parents see", () => {
    expect(accountStatus("issued")).toEqual({ label: "Issued", tone: "ok" });
    expect(accountStatus("unknown").label).toBe("Check with the rail");
    expect(accountStatus("odd").label).toBe("odd");
    expect(nameNote({ named_by_rail: true }, "Paystack")).toMatch(/^Paystack names the account itself/);
    expect(nameNote({ named_by_rail: false, shortened: true }, "Moniepoint")).toMatch(/shortened/);
    expect(nameNote({ named_by_rail: false, flagged: true }, "Moniepoint")).toMatch(/was cut/);
    expect(nameNote({ named_by_rail: false }, "Moniepoint")).toBe("");
    expect(railOf("flutterwave/main")).toBe("Flutterwave");
    expect(railOf("acme/main")).toBe("acme");
    expect(spacedNumber("9912345678")).toBe("9912 345 678");
    expect(spacedNumber("12345")).toBe("12345");
  });
  it("checks a BVN or NIN", () => {
    expect(idProblem("", "")).toBe("Give a parent's BVN or NIN.");
    expect(idProblem("1234", "")).toBe("A BVN or NIN has 11 digits.");
    expect(idProblem("12345678901", "")).toBeNull();
    expect(idProblem("", "12345678901")).toBeNull();
  });
});
