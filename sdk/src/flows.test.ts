// The repository's workflows are kept as code (.flow.ts) and as definitions
// (.wd.json), side by side. This checks they agree both ways: the code
// builds to the definition, and generating code from the definition gives
// the file as committed. TASKIEM_UPDATE_FLOWS=1 rewrites the code files.
import { existsSync, readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { CONNECTOR_ACTIONS, CONNECTOR_HELPERS } from "./connectors.gen.js";
import { generate, type Workflow } from "./index.js";

function definitions(dir: string): string[] {
  const out: string[] = [];
  for (const f of readdirSync(dir)) {
    const p = join(dir, f);
    if (statSync(p).isDirectory()) out.push(...definitions(p));
    else if (f.endsWith(".wd.json")) out.push(p);
  }
  return out.sort();
}

describe("flows/ code and definitions agree", () => {
  for (const wdPath of definitions(join(import.meta.dirname, "../../flows"))) {
    const codePath = wdPath.replace(/\.wd\.json$/, ".flow.ts");
    const name = wdPath.split("/flows/")[1];
    it(name ?? wdPath, async () => {
      const def = JSON.parse(readFileSync(wdPath, "utf8"));
      const code = generate(def, { connectors: CONNECTOR_HELPERS, actions: CONNECTOR_ACTIONS });
      if (process.env.TASKIEM_UPDATE_FLOWS === "1" || !existsSync(codePath)) writeFileSync(codePath, code);
      expect(readFileSync(codePath, "utf8"), `${codePath} is stale: run TASKIEM_UPDATE_FLOWS=1 pnpm --filter @taskiem/sdk test`).toBe(code);
      const mod = (await import(codePath)) as { default: Workflow };
      expect(mod.default.build()).toEqual(def);
    });
  }
});
