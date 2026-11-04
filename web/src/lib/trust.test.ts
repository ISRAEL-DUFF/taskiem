import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import {
  appealMessage,
  businessDetail,
  canAppeal,
  categoryLabel,
  fieldLabel,
  findingsSummary,
  pageStatus,
  pendingOf,
  publishNotice,
  refusalMessage,
  refusalOf,
  reportReason,
  reviewKind,
  reviewStatus,
  smallest,
  turnedDown,
  type Finding,
} from "./trust";

const finding = (verdict: string, category: string): Finding => ({ field: "about", text: "x", verdict, category, reason: "", checker: "words" });

describe("storefront trust", () => {
  it("names page statuses, with unknown ones as they are", () => {
    expect(pageStatus("in_review").label).toBe("With Taskiem for a check");
    expect(pageStatus("suspended").tone).toBe("danger");
    expect(pageStatus("published").label).toBe("Live");
    expect(pageStatus("something_new").label).toBe("something new");
  });

  it("knows when the owner can appeal", () => {
    expect(canAppeal({ status: "suspended" })).toBe(true);
    expect(canAppeal({ status: "draft", review_note: "Remove the loan offer." })).toBe(true);
    expect(turnedDown({ status: "draft", review_note: null })).toBe(false);
    expect(canAppeal({ status: "published" })).toBe(false);
    expect(canAppeal({ status: "in_review" })).toBe(false);
  });

  it("labels review kinds and statuses", () => {
    expect(reviewKind("first_publish")).toBe("First publish");
    expect(reviewKind("business")).toBe("Business verification");
    expect(reviewKind("new_kind")).toBe("new kind");
    expect(reviewStatus("pending").label).toBe("Waiting for Taskiem");
    expect(reviewStatus("rejected").tone).toBe("danger");
  });

  it("summarises findings", () => {
    expect(findingsSummary([])).toBe("Nothing found");
    expect(findingsSummary(null)).toBe("Nothing found");
    expect(findingsSummary([finding("allowed", "other")])).toBe("Nothing found");
    expect(findingsSummary([finding("blocked", "weapons"), finding("flagged", "price"), finding("flagged", "price")])).toBe("1 blocked, 2 flagged: weapons, price");
    expect(findingsSummary([finding("flagged", "")])).toBe("1 flagged");
    expect(categoryLabel("investment_scheme")).toBe("investment scheme");
    expect(categoryLabel("new_one")).toBe("new one");
    expect(fieldLabel("item 12")).toBe("Catalogue item 12");
    expect(fieldLabel("map_query")).toBe("map query");
  });

  it("reads a review's detail", () => {
    expect(businessDetail({ legal_name: "Ada Foods Ltd", address: "1 Broad St", cac_number: "RC 1", note: "" })).toEqual({
      legal_name: "Ada Foods Ltd",
      address: "1 Broad St",
      cac_number: "RC 1",
      note: "",
    });
    expect(businessDetail({ message: "x" })).toBeNull();
    expect(businessDetail(null)).toBeNull();
    expect(appealMessage({ message: "I removed it.", page_status: "suspended" })).toBe("I removed it.");
    expect(appealMessage({})).toBeNull();
    expect(reportReason("scam")).toBe("Looks like a scam");
    expect(reportReason("unknown")).toBe("unknown");
  });

  it("finds a pending review of a kind", () => {
    const t = {
      reviews: [
        { id: "1", kind: "business" as const, status: "rejected" as const, findings: [], decision_note: "No", created_at: "", decided_at: "" },
        { id: "2", kind: "business" as const, status: "pending" as const, findings: [], decision_note: null, created_at: "", decided_at: null },
      ],
    };
    expect(pendingOf(t, "business")?.id).toBe("2");
    expect(pendingOf(t, "appeal")).toBeUndefined();
    expect(pendingOf(undefined, "appeal")).toBeUndefined();
  });

  it("classifies refusals", () => {
    expect(refusalOf(new ApiError(409, "verification needed: publishing needs a verified phone"))).toBe("verification");
    expect(refusalOf(new ApiError(409, "Verification needed: taking payment when customers order needs a verified business"))).toBe("verification");
    expect(refusalOf(new ApiError(402, "not on your plan", [], { code: "plan_feature_required" }))).toBe("plan");
    expect(refusalOf(new ApiError(400, "This cannot go on a public page: \"pistol\" (weapons)"))).toBe("moderation");
    expect(refusalOf(new ApiError(409, "the page is suspended; reply to Taskiem's message to appeal"))).toBe("suspended");
    expect(refusalOf(new ApiError(409, "no workflow is deployed in prod"))).toBeNull();
    expect(refusalOf(new Error("boom"))).toBeNull();
    expect(refusalMessage("verification needed: publishing needs a verified phone")).toBe("Publishing needs a verified phone");
  });

  it("tells the owner what a publish did", () => {
    expect(publishNotice({ status: "in_review", published_version: 1 })).toMatch(/^Sent to Taskiem for a quick check/);
    expect(publishNotice({ status: "published", published_version: 3 })).toBe("Published version 3.");
  });

  it("picks a photo's smallest width", () => {
    expect(smallest({ "2000": "big", "320": "small", "800": "mid" })).toBe("small");
    expect(smallest({})).toBeUndefined();
    expect(smallest(null)).toBeUndefined();
  });
});
