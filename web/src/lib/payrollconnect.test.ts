import { describe, expect, it } from "vitest";
import {
  approverSub,
  canApproveHere,
  codeDigits,
  connectBody,
  connectionState,
  linkedWords,
  newKey,
  sortApprovers,
  type PayrollApprover,
  type PayrollConnection,
} from "./payroll";
import { splitFields } from "./connections";
import type { ConnectorInfo } from "../api";

const approver = (over: Partial<PayrollApprover>): PayrollApprover => ({
  user_id: "u1",
  name: "Ngozi Eze",
  email: "ngozi@amara.test",
  role: "OWNER",
  can_approve: true,
  can_pay: true,
  can_confirm: true,
  linked: null,
  suggested: false,
  me: false,
  ...over,
});

type Conn = NonNullable<PayrollConnection["connection"]>;

const conn = (over: Partial<Conn> = {}, top: Partial<PayrollConnection> = {}): PayrollConnection => ({
  connected: true,
  is_owner: true,
  may_link: true,
  modes: ["test"],
  connection: { id: "c1", name: "main", status: "active", auth: "oauth", test: true, scopes: ["payroll:read", "payroll:approve"], ...over },
  ...top,
});

describe("connecting Payrolla in one step (P4b-1)", () => {
  it("tells a connection in one step from a key, an ended one and none", () => {
    expect(connectionState(undefined)).toBe("none");
    expect(connectionState({ connected: false, is_owner: true, may_link: true, modes: [] })).toBe("none");
    expect(connectionState(conn())).toBe("one_step");
    expect(connectionState(conn({ auth: "key" }))).toBe("key");
    expect(connectionState(conn({ status: "expired" }, { connected: false, ended: true }))).toBe("ended");
  });

  it("lists approvers only where Payrolla allowed Approve and pay", () => {
    expect(canApproveHere(conn())).toBe(true);
    expect(canApproveHere(conn({ scopes: ["payroll:read"] }))).toBe(false);
    expect(canApproveHere(conn({ auth: "key" }))).toBe(false);
  });

  it("says what an approver can do and who they are linked to", () => {
    expect(approverSub(approver({}))).toBe("Owner · Approves and pays");
    expect(approverSub(approver({ role: "APPROVER", can_pay: false, can_confirm: false }))).toBe("Approver · Approves · No phone for codes yet");
    expect(linkedWords(approver({}))).toBe("Not linked");
    expect(linkedWords(approver({ suggested: true }))).toBe("Looks like you (same email)");
    expect(linkedWords(approver({ linked: { user_id: "x", name: "Ada" }, me: true }))).toBe("Linked to you");
    expect(linkedWords(approver({ linked: { user_id: "x", name: "Ada Obi" } }))).toBe("Linked to Ada Obi");
  });

  it("puts this person's own first, then the one with their email", () => {
    const list = sortApprovers([approver({ user_id: "a" }), approver({ user_id: "b", suggested: true }), approver({ user_id: "c", me: true })]);
    expect(list.map((a) => a.user_id)).toEqual(["c", "b", "a"]);
  });

  it("reads a typed code and builds the connect request", () => {
    expect(codeDigits("482 910")).toBe("482910");
    expect(codeDigits("48-29-10-99")).toBe("482910");
    expect(connectBody("test", false, "RC1")).toEqual({ mode: "test" });
    expect(connectBody("", true, " RC 123 ")).toEqual({ sign_up: true, rc_number: "RC 123" });
    expect(newKey()).not.toBe(newKey());
  });

  it("asks for Payrolla's key on the paste form, never what connecting in one step fills", () => {
    const payrolla = {
      auth: {
        type: "api_key",
        fields: [
          { key: "api_key", label: "API key", secret: true, required: false },
          { key: "refresh_token", label: "Refresh", secret: true, required: false },
          { key: "client_id", label: "Client", required: false },
          { key: "access_expires_at", label: "Expiry", required: false },
          { key: "webhook_secret", label: "Signing secret", secret: true, required: false },
        ],
        oauth2: {
          authorize_url: "https://x/oauth/authorize",
          token_url: "https://x/oauth/token",
          scopes: [],
          pkce: true,
          fields_from_token: { refresh_token: "refresh_token" },
          platform_client_fields: { client_id: "id" },
          linked_fields: ["access_expires_at"],
        },
      },
    } as unknown as ConnectorInfo;
    const { main, more } = splitFields(payrolla);
    expect(main.map((f) => f.key)).toEqual(["api_key"]);
    expect(more.map((f) => f.key)).toEqual(["webhook_secret"]);
  });
});
