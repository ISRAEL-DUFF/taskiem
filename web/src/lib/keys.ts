// Encryption keys (docs/byok.md): the tenant key hierarchy and bring your
// own key. Pure helpers for the Settings > Encryption keys page.

export type Provider = "vault_transit" | "aws_kms" | "gcp_kms" | "azure_key_vault";

export interface BYOKKey {
  id: string;
  provider: Provider;
  description: string;
  config: Record<string, string>;
  credentials_digest?: string;
  status: "active" | "unavailable" | "retired";
  checked_at?: string;
  last_ok_at?: string;
  failing_since?: string;
  check_failures: number;
  last_error?: string;
  recovered_at?: string;
  created_by: string;
  created_at: string;
  retired_at?: string;
}

export interface KeyVersion {
  version: number;
  wrapped_by: "platform" | "customer";
  byok_key_id?: string;
  created_at: string;
  retired_at?: string;
  destroyed_at?: string;
  secrets: number;
  subject_keys: number;
}

export interface KeyStatus {
  mode: "platform" | "customer";
  current_version: number;
  versions: KeyVersion[];
  byok?: BYOKKey;
  retired_byok?: BYOKKey[];
  rewrap?: { phase: "rewrap" | "destroy"; reason: string; since: string; not_before: string; secrets: number; subject_keys: number; last_error?: string };
  parked_steps: number;
  cache_ttl_seconds: number;
}

export interface KeysView {
  status: KeyStatus;
  providers: Provider[];
  plan_allows_byok: boolean;
}

/** One input on the onboarding form. */
export interface FieldSpec {
  key: string;
  label: string;
  hint?: string;
  secret?: boolean; // a credential: sent once, never shown again
  multiline?: boolean;
  optional?: boolean;
  placeholder?: string;
}

export const providerNames: Record<Provider, string> = {
  vault_transit: "OpenBao or Vault (transit)",
  aws_kms: "AWS KMS",
  gcp_kms: "Google Cloud KMS",
  azure_key_vault: "Azure Key Vault",
};

/** The configuration and credential fields each provider takes. Vault
 * takes either a token or an AppRole; `auth` picks which. */
export function providerFields(p: Provider, auth: "token" | "approle" = "token"): { config: FieldSpec[]; credentials: FieldSpec[] } {
  switch (p) {
    case "vault_transit":
      return {
        config: [
          { key: "address", label: "Address", placeholder: "https://bao.example.com:8200", hint: "HTTPS, reachable from Taskiem's egress IPs" },
          { key: "mount", label: "Transit mount", optional: true, placeholder: "transit" },
          { key: "key", label: "Key name" },
          { key: "namespace", label: "Namespace", optional: true },
          { key: "ca_cert", label: "CA certificate (PEM)", optional: true, multiline: true, hint: "Only for a private CA; added to the system roots" },
        ],
        credentials:
          auth === "token"
            ? [{ key: "token", label: "Token", secret: true, hint: "A token whose policy allows only encrypt and decrypt on this key" }]
            : [
                { key: "role_id", label: "AppRole role id", secret: true },
                { key: "secret_id", label: "AppRole secret id", secret: true },
              ],
      };
    case "aws_kms":
      return {
        config: [
          { key: "region", label: "Region", placeholder: "af-south-1" },
          { key: "key", label: "Key", placeholder: "arn:aws:kms:…:key/… or alias/…" },
        ],
        credentials: [
          { key: "access_key_id", label: "Access key id", secret: true },
          { key: "secret_access_key", label: "Secret access key", secret: true },
          { key: "session_token", label: "Session token", secret: true, optional: true },
        ],
      };
    case "gcp_kms":
      return {
        config: [{ key: "key", label: "Crypto key", placeholder: "projects/P/locations/L/keyRings/R/cryptoKeys/K" }],
        credentials: [{ key: "service_account_json", label: "Service account key (JSON)", secret: true, multiline: true }],
      };
    case "azure_key_vault":
      return {
        config: [
          { key: "address", label: "Vault URL", placeholder: "https://NAME.vault.azure.net" },
          { key: "key", label: "Key name" },
          { key: "key_version", label: "Key version", hint: "32 characters, from the portal or az keyvault key show" },
        ],
        credentials: [
          { key: "tenant_id", label: "Directory (tenant) id", secret: true },
          { key: "client_id", label: "Application (client) id", secret: true },
          { key: "client_secret", label: "Client secret", secret: true },
        ],
      };
  }
}

/** The PUT /v1/keys/byok body: trimmed, empty optional fields left out. */
export function byokRequest(p: Provider, values: Record<string, string>, auth: "token" | "approle" = "token"): Record<string, unknown> {
  const f = providerFields(p, auth);
  const body: Record<string, unknown> = { provider: p };
  for (const c of f.config) {
    const v = (values[c.key] ?? "").trim();
    if (v) body[c.key] = v;
  }
  const credentials: Record<string, string> = {};
  for (const c of f.credentials) {
    const v = c.multiline ? (values[c.key] ?? "") : (values[c.key] ?? "").trim();
    if (v.trim()) credentials[c.key] = v;
  }
  body.credentials = credentials;
  return body;
}

/** What is missing before the form can be sent. */
export function missingFields(p: Provider, values: Record<string, string>, auth: "token" | "approle" = "token"): string[] {
  const f = providerFields(p, auth);
  return [...f.config, ...f.credentials].filter((x) => !x.optional && !(values[x.key] ?? "").trim()).map((x) => x.label);
}

/** A sentence for the key's health. */
export function healthLine(k: BYOKKey): string {
  if (k.status === "unavailable") {
    return `Unavailable since ${k.failing_since ? new Date(k.failing_since).toLocaleString() : "recently"}: steps that need secrets are paused and resume when it works again.`;
  }
  if (k.check_failures > 0) return `The last ${k.check_failures === 1 ? "check" : `${k.check_failures} checks`} failed; it is marked unavailable after two in a row.`;
  return k.last_ok_at ? `Working; last checked ${new Date(k.last_ok_at).toLocaleString()}.` : "Working.";
}

/** The state of a tenant key version, for the versions table. */
export function versionState(v: KeyVersion, current: number): string {
  if (v.destroyed_at) return "destroyed";
  if (v.retired_at) return "retired";
  return v.version === current ? "current" : "being replaced";
}
