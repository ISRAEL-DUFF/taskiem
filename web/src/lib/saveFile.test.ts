import { describe, expect, it } from "vitest";
import { linksCSV } from "./forms";
import { saveFile } from "./saveFile";

// A page that offers a file (the one-time form links, the debtors and
// clearance lists) makes its blob: address only when the person asks for
// the file, and lets it go straight after: the links in it are secrets,
// and a page that drew one per render kept every copy alive.
describe("saving a file the page made", () => {
  it("makes one address on the click and revokes it", () => {
    const made: string[] = [];
    const revoked: string[] = [];
    const clicked: { href: string; download: string }[] = [];
    const urls = {
      createObjectURL: () => {
        const u = `blob:x/${made.length}`;
        made.push(u);
        return u;
      },
      revokeObjectURL: (u: string) => void revoked.push(u),
    };
    const doc = {
      createElement: () => {
        const a = { href: "", download: "", click: () => void clicked.push({ href: a.href, download: a.download }) };
        return a;
      },
    };
    saveFile(new Blob([linksCSV([{ url: "https://f/r/a", expires_at: "2026-11-12" }])], { type: "text/csv" }), "f1-links.csv", { document: doc, URL: urls });
    expect(clicked).toEqual([{ href: "blob:x/0", download: "f1-links.csv" }]);
    expect(revoked).toEqual(made);
  });
});
