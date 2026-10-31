// The gate's text test (no-technical-steps, point 2 of G-N; Appendix C):
// the words an owner never sees, checked against every piece of text the
// owner's pages show. Text under Details or Advanced (a <details>, or an
// element marked data-technical), and code (<code>, <pre>), are left out.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import ts from "typescript";
import { describe, expect, it } from "vitest";

export const GLOSSARY: RegExp[] = [
  /\bJSON\b/,
  /\bschemas?\b/i,
  /wd\/v1/,
  /wd-test/,
  /\bexpressions?\b/i,
  /\bCEL\b/,
  /\bcron\b/i,
  /\bkobo\b/i,
  /\btokens?\b/i,
  /\bwebhooks?\b/i,
  /\bHMAC\b/,
  /\bendpoints?\b/i,
  /\bpayloads?\b/i,
  /\bAPI keys?\b/i,
  /secret_key/,
  /\bstep id\b/i,
  /trigger\.body/,
  /=steps/,
  /\bnull\b/,
  /\bundefined\b/,
  /\/steps\/[0-9a-z_]/,
  /\b(status|HTTP) [1-5]\d\d\b/,
];

/** The owner's path: the pages of the gate's five tasks. */
const OWNER_FILES = [
  "pages/Start.tsx",
  "pages/Storefront.tsx",
  "pages/StorefrontForms.tsx",
  "pages/StorefrontBookings.tsx",
  "pages/StorefrontOrders.tsx",
  "pages/StorefrontRiders.tsx",
  "pages/Dispatch.tsx",
  "pages/StorefrontStock.tsx",
  "pages/StockPurchases.tsx",
  "pages/StockCounts.tsx",
  "pages/StorefrontSuppliers.tsx",
  "pages/Links.tsx",
  "pages/Bills.tsx",
  "pages/TradeOrders.tsx",
  "pages/Invoices.tsx",
  "pages/ReceiveMoney.tsx",
  "pages/StorefrontTrust.tsx",
  "pages/Readiness.tsx",
  "pages/Approvals.tsx",
  "pages/Templates.tsx",
  "pages/AIBuild.tsx",
  "pages/Run.tsx",
  "pages/Account.tsx",
  "pages/Connect.tsx",
  "pages/ChatDoors.tsx",
  "forms/ConnectionPicker.tsx",
  "forms/RolePicker.tsx",
  "forms/SheetChooser.tsx",
  "forms/ProblemLine.tsx",
  "forms/Pickers.tsx",
  "forms/RecipientField.tsx",
  "canvas/PlainPickers.tsx",
  "canvas/StepPanel.tsx",
  "canvas/FormEditor.tsx",
  "pages/Editor.tsx",
  "pages/MergeDialog.tsx",
  "pages/Workflows.tsx",
  "pages/Forms.tsx",
  "pages/StorefrontDomains.tsx",
  "pages/StorefrontEmbedSites.tsx",
  "pages/NewWorkflow.tsx",
  "builder/Canvas.tsx",
  "builder/Picker.tsx",
  "builder/StepWindow.tsx",
  "builder/ContainerSettings.tsx",
  "builder/PhoneSteps.tsx",
  "forms/FieldText.tsx",
];

const SHOWN_ATTRS = new Set(["label", "hint", "placeholder", "title", "aria-label", "description"]);

function technical(el: ts.JsxOpeningLikeElement): boolean {
  const tag = el.tagName.getText();
  if (["details", "code", "pre", "Json", "JsonInput"].includes(tag)) return true;
  return el.attributes.properties.some((a) => ts.isJsxAttribute(a) && a.name.getText() === "data-technical");
}

/** Every piece of text a file's JSX shows, outside Details, Advanced and code. */
export function shownText(file: string, src: string): string[] {
  const sf = ts.createSourceFile(file, src, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const out: string[] = [];
  const visit = (n: ts.Node, hidden: boolean) => {
    // What only Advanced shows: `advanced && …`, and `advanced ? … : …`'s first branch.
    if (ts.isBinaryExpression(n) && n.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken && n.left.getText().split("&&").some((c) => c.trim() === "advanced")) {
      visit(n.left, hidden);
      visit(n.right, true);
      return;
    }
    if (ts.isConditionalExpression(n) && /^!?advanced$/.test(n.condition.getText().trim())) {
      const neg = n.condition.getText().trim().startsWith("!");
      visit(n.whenTrue, hidden || !neg);
      visit(n.whenFalse, hidden || neg);
      return;
    }
    if (ts.isJsxElement(n) && technical(n.openingElement)) hidden = true;
    if (ts.isJsxSelfClosingElement(n) && technical(n)) hidden = true;
    if (!hidden) {
      if (ts.isJsxText(n) && n.getText().trim()) out.push(n.getText().trim());
      if ((ts.isStringLiteral(n) || ts.isNoSubstitutionTemplateLiteral(n)) && n.parent) {
        const p = n.parent;
        const inAttr = ts.isJsxAttribute(p) && SHOWN_ATTRS.has(p.name.getText());
        const inJsx = ts.isJsxExpression(p) && !!p.parent && (ts.isJsxElement(p.parent) || ts.isJsxFragment(p.parent));
        const inAttrExpr = ts.isJsxExpression(p) && !!p.parent && ts.isJsxAttribute(p.parent) && SHOWN_ATTRS.has(p.parent.name.getText());
        if (inAttr || inJsx || inAttrExpr) out.push(n.text);
      }
    }
    ts.forEachChild(n, (c) => visit(c, hidden));
  };
  visit(sf, false);
  return out;
}

describe("an owner never sees technical words", () => {
  for (const f of OWNER_FILES) {
    it(f, () => {
      const src = readFileSync(resolve(__dirname, "..", f), "utf8");
      const bad = shownText(f, src).filter((t) => GLOSSARY.some((g) => g.test(t)));
      expect(bad).toEqual([]);
    });
  }
});
