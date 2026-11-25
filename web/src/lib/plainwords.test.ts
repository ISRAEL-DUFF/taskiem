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
  // Sizes and colours (chat categories Part B): never "variant" to an owner.
  /\bvariants?\b/i,
];

/** The owner's path: the pages of the gate's five tasks. */
const OWNER_FILES = [
  "pages/Start.tsx",
  // Pages (decision 0072): the list, a page, the editor and their words.
  "pages/Pages.tsx",
  "pages/PageEditor.tsx",
  "pages/PageEditorParts.tsx",
  "pages/PageEditorG4.tsx",
  "pages/PageEditorG5.tsx",
  "pages/PageStarters.tsx",
  "pages/PageG4.tsx",
  "lib/pages.ts",
  // Your tools (made tools M1): the sheet, its answer, the tool's page,
  // and Use it from chat in the builder.
  "pages/MadeToolSheet.tsx",
  "lib/toolanswer.ts",
  "pages/UseFromChat.tsx",
  "lib/toolbuild.ts",
  "pages/SearchEverywhere.tsx",
  "pages/Saved.tsx",
  "lib/saved.ts",
  "pages/Payments.tsx",
  "pages/PaySheet.tsx",
  "pages/PhotoRead.tsx",
  "lib/photoAI.ts",
  "pages/AIPhotoReading.tsx",
  "pages/PayChip.tsx",
  "pages/SaveAsSheet.tsx",
  "lib/payments.ts",
  "lib/paymessage.ts",
  "pages/PayBatch.tsx",
  "lib/batches.ts",
  "pages/PayCollect.tsx",
  "lib/paycollect.ts",
  "pages/SavedImport.tsx",
  "lib/savedImport.ts",
  "lib/savedSources.ts",
  "pages/Storefront.tsx",
  "pages/StorefrontForms.tsx",
  "pages/SellForm.tsx",
  "pages/OneProductFront.tsx",
  // Forms left half-filled (forms that sell L3): who left, and the follow-ups.
  "pages/StorefrontLeft.tsx",
  "lib/left.ts",
  "pages/StorefrontFronts.tsx",
  "pages/FrontItems.tsx",
  "pages/StorefrontBookings.tsx",
  "pages/StorefrontOrders.tsx",
  "pages/StorefrontRiders.tsx",
  "pages/Dispatch.tsx",
  "pages/StorefrontStock.tsx",
  "pages/StockPurchases.tsx",
  "pages/StockCounts.tsx",
  "pages/StorefrontSuppliers.tsx",
  "pages/TreatLine.tsx",
  "pages/MoneyLabels.tsx",
  "pages/Things.tsx",
  "pages/MoneySpending.tsx",
  "pages/Transactions.tsx",
  "lib/transactions.ts",
  "lib/things.ts",
  "lib/treat.ts",
  "pages/Areas.tsx",
  "pages/WayIn.tsx",
  "pages/You.tsx",
  "pages/PersonChats.tsx",
  "pages/PersonTools.tsx",
  "pages/AppCards.tsx",
  "pages/ChatLinkPage.tsx",
  "pages/Discover.tsx",
  "pages/DiscoverSettings.tsx",
  "pages/YouOrders.tsx",
  "pages/YouAccount.tsx",
  "pages/YouStanding.tsx",
  "pages/PeopleSearch.tsx",
  "pages/PeopleLive.tsx",
  "pages/PeopleChat.tsx",
  "pages/People.tsx",
  "pages/GroupChat.tsx",
  "pages/Basket.tsx",
  "pages/Groups.tsx",
  "pages/Transparency.tsx",
  "pages/Legal.tsx",
  "pages/CustomerChatSettings.tsx",
  "pages/CustomerCards.tsx",
  "pages/Switch.tsx",
  "pages/ChangeNumber.tsx",
  "pages/StartSomething.tsx",
  "pages/Links.tsx",
  "pages/BusinessProfile.tsx",
  "pages/ChatPush.tsx",
  "pages/PushNotices.tsx",
  "pages/Bills.tsx",
  "pages/PayByTransfer.tsx",
  "lib/paybytransfer.ts",
  "pages/TradeOrders.tsx",
  "pages/Conversation.tsx",
  "pages/TaskiemChat.tsx",
  "pages/CustomerChat.tsx",
  "pages/CustomerOrders.tsx",
  "pages/ChatMoney.tsx",
  "pages/ChatRefCard.tsx",
  "pages/Staff.tsx",
  "pages/StaffPerson.tsx",
  "pages/Payroll.tsx",
  "lib/payroll.ts",
  "pages/PayrollSettings.tsx",
  "pages/PayrollTeam.tsx",
  "lib/payrollteam.ts",
  "pages/PayrollRun.tsx",
  "lib/payrollrun.ts",
  "pages/PayrollSchedules.tsx",
  "lib/payrollreports.ts",
  "pages/Payslip.tsx",
  "lib/payslip.ts",
  "pages/Tasks.tsx",
  "pages/ChatToolForms.tsx",
  "pages/ChatTools.tsx",
  "pages/ChatBuyer.tsx",
  "pages/ProductPhotos.tsx",
  "pages/PhotoAdd.tsx",
  "pages/SizesForms.tsx",
  "pages/ChatStaff.tsx",
  "pages/PriceLists.tsx",
  "pages/Mentions.tsx",
  "pages/Statement.tsx",
  "pages/VoiceNote.tsx",
  "pages/Invoices.tsx",
  "pages/ReceiveMoney.tsx",
  "pages/StorefrontTrust.tsx",
  "pages/Readiness.tsx",
  "pages/Approvals.tsx",
  "pages/Templates.tsx",
  "pages/AIBuild.tsx",
  "pages/Run.tsx",
  "pages/Account.tsx",
  "pages/PayFingerprint.tsx",
  "pages/Connect.tsx",
  "pages/ChatDoors.tsx",
  "pages/ChatAvailability.tsx",
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

/** Every string in a file's code (not its comments): a lib file's words
 * are shown as they are returned. */
function allStrings(file: string, src: string): string[] {
  const sf = ts.createSourceFile(file, src, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const out: string[] = [];
  const visit = (n: ts.Node) => {
    if (ts.isStringLiteral(n) || ts.isNoSubstitutionTemplateLiteral(n) || ts.isTemplateHead(n) || ts.isTemplateMiddle(n) || ts.isTemplateTail(n)) out.push(n.text);
    if (ts.isJsxText(n) && n.getText().trim()) out.push(n.getText().trim());
    ts.forEachChild(n, visit);
  };
  visit(sf);
  return out;
}

// Paying people (docs/spec/chat-pay-people.md, Tests and checks): "Pay",
// "Send money", "Beneficiary", the words bank apps use; never "payout" or
// "NUBAN" in the Pay sheet, the chip under a message, or their words.
describe("paying people says Pay, never payout", () => {
  const PAY_FILES = ["pages/PaySheet.tsx", "pages/PayChip.tsx", "pages/SaveAsSheet.tsx","pages/Payments.tsx", "lib/payments.ts", "lib/paymessage.ts", "pages/PayBatch.tsx", "lib/batches.ts", "pages/PayCollect.tsx", "lib/paycollect.ts", "pages/StaffPerson.tsx", "lib/team.ts", "pages/Payroll.tsx", "lib/payroll.ts", "pages/PayrollSettings.tsx", "pages/PayrollTeam.tsx", "lib/payrollteam.ts", "pages/PayrollRun.tsx", "lib/payrollrun.ts", "pages/PayrollSchedules.tsx", "lib/payrollreports.ts", "pages/Payslip.tsx", "lib/payslip.ts"];
  for (const f of PAY_FILES) {
    it(f, () => {
      const src = readFileSync(resolve(__dirname, "..", f), "utf8");
      const bad = allStrings(f, src).filter((t) => /\bpayouts?\b|\bNUBAN\b/i.test(t));
      expect(bad).toEqual([]);
    });
  }
});
