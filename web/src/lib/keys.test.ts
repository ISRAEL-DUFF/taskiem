import { describe, expect, it } from "vitest";
import { byokRequest, healthLine, missingFields, providerFields, versionState, type BYOKKey } from "./keys";

describe("keys", () => {
  it("builds the onboarding body with credentials apart", () => {
    const body = byokRequest("vault_transit", { address: " https://bao.example.com:8200 ", key: "customer", mount: "", token: " s.abc " });
    expect(body).toEqual({ provider: "vault_transit", address: "https://bao.example.com:8200", key: "customer", credentials: { token: "s.abc" } });
    const approle = byokRequest("vault_transit", { address: "https://b", key: "k", role_id: "r", secret_id: "s", token: "ignored" }, "approle");
    expect(approle.credentials).toEqual({ role_id: "r", secret_id: "s" });
  });
  it("keeps a service account key as written", () => {
    const sa = '{\n  "type": "service_account"\n}\n';
    expect(byokRequest("gcp_kms", { key: "projects/p/locations/l/keyRings/r/cryptoKeys/k", service_account_json: sa }).credentials).toEqual({ service_account_json: sa });
  });
  it("lists what is missing, ignoring optional fields", () => {
    expect(missingFields("aws_kms", { region: "af-south-1", key: "alias/x", access_key_id: "a" })).toEqual(["Secret access key"]);
    expect(missingFields("azure_key_vault", {})).toHaveLength(6);
    expect(missingFields("vault_transit", { address: "https://b", key: "k", token: "t" })).toEqual([]);
  });
  it("marks credentials secret on every provider", () => {
    for (const p of ["vault_transit", "aws_kms", "gcp_kms", "azure_key_vault"] as const) {
      expect(providerFields(p).credentials.every((c) => c.secret)).toBe(true);
      expect(providerFields(p).config.some((c) => c.secret)).toBe(false);
    }
  });
  it("describes health and versions", () => {
    const k: BYOKKey = { id: "1", provider: "aws_kms", description: "", config: {}, status: "active", check_failures: 1, created_by: "u", created_at: "2026-10-07T00:00:00Z" };
    expect(healthLine(k)).toMatch(/marked unavailable after two/);
    expect(healthLine({ ...k, status: "unavailable", failing_since: "2026-10-07T00:00:00Z" })).toMatch(/paused and resume/);
    const v = { version: 1, wrapped_by: "platform" as const, created_at: "", secrets: 0, subject_keys: 0 };
    expect(versionState(v, 2)).toBe("being replaced");
    expect(versionState({ ...v, retired_at: "x" }, 2)).toBe("retired");
    expect(versionState({ ...v, destroyed_at: "x", retired_at: "x" }, 2)).toBe("destroyed");
    expect(versionState({ ...v, version: 2 }, 2)).toBe("current");
  });
});
