import { afterEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "./api";

// A 401 is the session ending, except where it is not: moving to a business
// that signs in on its own (single sign-on) leaves this session as it is.
describe("a 401", () => {
  afterEach(() => vi.unstubAllGlobals());
  function serve(body: object) {
    const told: string[] = [];
    vi.stubGlobal("window", {
      dispatchEvent: (e: Event) => told.push(e.type),
      location: { pathname: "/" },
    });
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify(body), { status: 401 })),
    );
    return told;
  }

  it("refusing a switch for single sign-on does not sign the tab out", async () => {
    const told = serve({ error: "sign in with your organisation", sso_required: true });
    await expect(api("POST", "/v1/me/switch", { tenant_id: "t" })).rejects.toBeInstanceOf(ApiError);
    expect(told).not.toContain("taskiem:unauthorised");
  });

  it("a wrong or expired code, or a wrong PIN, while changing the number does not", async () => {
    for (const body of [{ code_wrong: true }, { ticket_expired: true }, { tries_left: 2 }]) {
      const told = serve({ error: "no", ...body });
      await expect(api("POST", "/v1/me/phone/verify", { code: "000000" })).rejects.toBeInstanceOf(ApiError);
      expect(told).not.toContain("taskiem:unauthorised");
    }
  });

  it("a wrong password or passkey while deleting the account does not; an ended session does", async () => {
    for (const body of [{ password_wrong: true }, { code_wrong: true }, {}, { second_proof_required: true }]) {
      const told = serve({ error: Object.keys(body).length ? "no" : "that passkey response is not valid here; try again", ...body });
      await expect(api("POST", "/v1/me/delete", { confirm: true })).rejects.toBeInstanceOf(ApiError);
      expect(told).not.toContain("taskiem:unauthorised");
    }
    const told = serve({ error: "the session has ended" });
    await expect(api("POST", "/v1/me/delete", { confirm: true, password: "x" })).rejects.toBeInstanceOf(ApiError);
    expect(told).toContain("taskiem:unauthorised");
  });

  it("an ended session while changing the number does", async () => {
    const told = serve({ error: "the session has ended" });
    await expect(api("POST", "/v1/me/phone/verify", { code: "123456" })).rejects.toBeInstanceOf(ApiError);
    expect(told).toContain("taskiem:unauthorised");
  });

  it("anything else does", async () => {
    const told = serve({ error: "the session has ended" });
    await expect(api("GET", "/v1/me/push")).rejects.toBeInstanceOf(ApiError);
    expect(told).toContain("taskiem:unauthorised");
  });
});
