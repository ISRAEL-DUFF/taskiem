/** A business's own domain for its storefront (docs/storefronts.md#your-own-domain). */
export interface DomainRecord {
  type: "TXT" | "A" | "AAAA" | "CNAME" | "ALIAS";
  name: string;
  value: string;
  /** proof proves the domain (keep it); point sends visitors to the page. */
  purpose: "proof" | "point";
  note?: string;
}

export interface StorefrontDomain {
  id: string;
  domain: string;
  twin: string | null;
  token: string;
  primary: boolean;
  verified_at: string | null;
  checked_at?: string;
  check_failures?: number;
  last_check_error?: string;
  unverified_at?: string;
  created_by: string;
  created_at: string;
  status: "pending" | "live" | "unverified";
  records: DomainRecord[];
}

export interface DomainList {
  domains: StorefrontDomain[];
  max: number;
  target: { cname?: string; addresses?: string[] };
}

/** What the owner reads about a domain's state. */
export function domainState(d: StorefrontDomain): string {
  switch (d.status) {
    case "live":
      return d.primary ? "Live: your page's address" : "Live: sends visitors to your main domain";
    case "unverified":
      return `Stopped: the TXT record was not found for over a day${d.last_check_error ? ` (${d.last_check_error})` : ""}. Put it back and check again.`;
    default:
      return "Waiting for the records below. DNS changes can take a few hours to show.";
  }
}

/** The domains a domain serves, as the owner typed them first. */
export const domainHosts = (d: StorefrontDomain) => (d.twin ? [d.domain, d.twin] : [d.domain]);

/** The records still to create: the proof first, then those that point the domain. */
export const recordsInOrder = (d: StorefrontDomain) => [...d.records].sort((a, b) => (a.purpose === b.purpose ? 0 : a.purpose === "proof" ? -1 : 1));

/** A domain the owner typed, tidied for a hint (no scheme, path or trailing dot); "" when empty. */
export function tidyDomain(raw: string): string {
  return raw
    .trim()
    .toLowerCase()
    .replace(/^[a-z]+:\/\//, "")
    .replace(/[/?#].*$/, "")
    .replace(/\.$/, "");
}
