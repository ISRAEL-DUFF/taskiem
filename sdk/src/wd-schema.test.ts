import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import type { StepType, Trigger } from "./wd.js";

// wd.ts is written by hand from schemas/wd-v1.schema.json. These lists are
// checked against the types by `pnpm typecheck` (satisfies: every member,
// nothing else) and against the schema's enums here, so a step or trigger
// type added to one side only fails one or the other.
const stepTypes = {
  connector: true, capability: true, template: true, code: true, container: true, http: true, branch: true, parallel: true,
  foreach: true, wait: true, signal: true, callback: true, respond: true, approval: true, form: true, subflow: true,
  transform: true, ai: true,
} satisfies Record<StepType, true>;

const triggerTypes = {
  webhook: true, schedule: true, connector_event: true, capability_event: true, polling: true, database_change: true,
  manual: true, whatsapp: true, ussd: true, email: true, subflow: true, storefront: true, form: true, invoice: true, link: true,
} satisfies Record<Trigger["type"], true>;

interface Schema {
  $defs: { stepCommon: { properties: { type: { enum: string[] } } }; trigger: { properties: { type: { enum: string[] } } } };
}

const schema = JSON.parse(readFileSync(new URL("../../schemas/wd-v1.schema.json", import.meta.url), "utf8")) as Schema;

describe("wd.ts matches schemas/wd-v1.schema.json", () => {
  it("has the schema's step types", () => {
    expect(Object.keys(stepTypes).sort()).toEqual([...schema.$defs.stepCommon.properties.type.enum].sort());
  });
  it("has the schema's trigger types", () => {
    expect(Object.keys(triggerTypes).sort()).toEqual([...schema.$defs.trigger.properties.type.enum].sort());
  });
});
