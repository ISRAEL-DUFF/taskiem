// Step-by-step guides for the companies Nigerian businesses buy domains
// from (no-technical-steps N9): where the DNS screen is, and what to type
// in each box for each record Taskiem asks for.
import type { DomainRecord } from "./domains";

export interface Registrar {
  id: string;
  name: string;
  /** How to reach the screen where records are added, one step each. */
  open: string[];
  /** What the screen calls the record's name box. */
  nameBox: string;
  /** What it calls the value box. */
  valueBox: string;
  /** The button that saves a record. */
  save: string;
}

export const REGISTRARS: Registrar[] = [
  {
    id: "whogohost",
    name: "Whogohost",
    open: ["Sign in to your Whogohost client area.", "Open Domains, then My Domains.", "Next to your domain, choose Manage Domain.", "Open DNS Management (or Manage DNS)."],
    nameBox: "Host Name",
    valueBox: "Address",
    save: "Save Changes",
  },
  {
    id: "qservers",
    name: "Qservers",
    open: ["Sign in to your Qservers client area.", "Open Domains, then My Domains.", "Choose your domain, then DNS Management."],
    nameBox: "Host Name",
    valueBox: "Address",
    save: "Save Changes",
  },
  {
    id: "domainking",
    name: "DomainKing",
    open: ["Sign in to DomainKing.", "Open My Domains and choose your domain.", "Open Manage DNS."],
    nameBox: "Host",
    valueBox: "Value",
    save: "Add Record",
  },
  {
    id: "godaddy",
    name: "GoDaddy",
    open: ["Sign in to GoDaddy and open your Domain Portfolio.", "Choose your domain, then open DNS.", "Choose Add New Record."],
    nameBox: "Name",
    valueBox: "Value",
    save: "Save",
  },
  {
    id: "namecheap",
    name: "Namecheap",
    open: [
      "Sign in to Namecheap and open Domain List.",
      "Next to your domain, choose Manage. Under Nameservers it must say Namecheap BasicDNS (or PremiumDNS); otherwise the records are kept where the nameservers point.",
      "Open the Advanced DNS tab and, under Host Records, choose Add New Record.",
    ],
    nameBox: "Host",
    valueBox: "Value",
    save: "the tick (Save)",
  },
  {
    id: "other",
    name: "Another company",
    open: ["Sign in where you bought the domain.", "Find the screen called DNS, DNS Management, Zone Editor or Name Servers settings for your domain."],
    nameBox: "Name or Host",
    valueBox: "Value, Points to or Address",
    save: "Save",
  },
];

/** What to type for one record, at a registrar. */
export function recordSteps(reg: Registrar, r: DomainRecord): string[] {
  const steps = [
    r.type === "ALIAS" ? "Add a new record of type ALIAS (some call it ANAME or CNAME flattening)." : `Add a new record of type ${r.type}.`,
    `In ${reg.nameBox}, type ${r.short || r.name}${r.short === "@" ? " (it means the domain itself; some screens want it left empty)" : ""}.`,
    `In ${reg.valueBox}, paste ${r.value}.`,
  ];
  if (r.type === "CNAME") steps.push(`If there is already an A or CNAME record for ${r.short || r.name}, delete it first: a name can have only one CNAME.`);
  if (r.type === "A" || r.type === "AAAA") steps.push(`Delete any other ${r.type} record for ${r.short || r.name} (often one the company added when you bought the domain).`);
  steps.push(`Leave TTL as it is, and press ${reg.save}.`);
  return steps;
}

/** The registrar with an id, or the "another company" guide. */
export function registrarOf(id: string): Registrar {
  return REGISTRARS.find((r) => r.id === id) ?? (REGISTRARS[REGISTRARS.length - 1] as Registrar);
}
