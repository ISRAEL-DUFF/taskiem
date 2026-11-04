import { createRequire } from "node:module";
import type { Page } from "@playwright/test";

// axe-core is a dev-only dependency (MPL-2.0, tools/licencecheck/policy.json):
// the tests inject it into the page under test; it is never bundled.
const axeSource = createRequire(import.meta.url).resolve("axe-core/axe.min.js");

export interface Violation {
  id: string;
  impact: string | null;
  help: string;
  nodes: string[];
}

/** Runs axe (WCAG 2.1 A and AA, and its best practices) on the page and
 * returns the violations, each with up to five offending elements. */
export async function axe(page: Page): Promise<Violation[]> {
  if (!(await page.evaluate(() => "axe" in window))) await page.addScriptTag({ path: axeSource });
  return page.evaluate(async () => {
    const w = window as unknown as {
      axe: {
        run: (
          ctx: Document,
          opts: object,
        ) => Promise<{ violations: { id: string; impact: string | null; help: string; nodes: { target: string[]; failureSummary?: string }[] }[] }>;
      };
    };
    const r = await w.axe.run(document, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa", "best-practice"] },
      resultTypes: ["violations"],
    });
    return r.violations.map((v) => ({
      id: v.id,
      impact: v.impact,
      help: v.help,
      nodes: v.nodes.slice(0, 5).map((n) => `${n.target.join(" ")}: ${(n.failureSummary ?? "").replace(/\s+/g, " ")}`),
    }));
  });
}

/** A readable list of violations for a failed expectation. */
export const describe = (where: string, vs: Violation[]) =>
  vs.map((v) => `${where}: ${v.id} (${v.impact}) ${v.help}\n  ${v.nodes.join("\n  ")}`).join("\n");
