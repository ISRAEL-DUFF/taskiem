import { describe, expect, it } from "vitest";
import { createElement } from "react";
import { renderToString } from "react-dom/server";
import { chatFileOK, fileFamily, fileLine, frameOf, hasPicture, sizeWords } from "./chatfiles";
import { FileCard, FileTypeIcon } from "../pages/ChatFiles";

// A chat's files as WhatsApp shows them (chat-previews-and-editing §1).
describe("chat files", () => {
  it("names each type's family, from the server's word, the type or the name", () => {
    expect(fileFamily({ type: "application/pdf" })).toBe("pdf");
    expect(fileFamily({ type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document" })).toBe("word");
    expect(fileFamily({ type: "application/vnd.ms-excel" })).toBe("excel");
    expect(fileFamily({ type: "application/vnd.openxmlformats-officedocument.presentationml.presentation" })).toBe("powerpoint");
    expect(fileFamily({ type: "application/vnd.oasis.opendocument.text" })).toBe("opendocument");
    expect(fileFamily({ type: "text/csv" })).toBe("csv");
    expect(fileFamily({ type: "", name: "stock.XLSX" })).toBe("excel");
    expect(fileFamily({ type: "text/plain", doc: "csv" })).toBe("csv");
    expect(fileFamily({ type: "application/zip", name: "x.zip" })).toBe("file");
  });

  it("says what a file is under its name", () => {
    expect(fileLine({ kind: "file", type: "application/pdf", size: 1.2 * 1024 * 1024, pages: 3, preview: "ready" })).toBe("PDF · 3 pages · 1.2 MB");
    expect(fileLine({ kind: "file", type: "application/pdf", size: 2048, pages: 1 })).toBe("PDF · 1 page · 2 KB");
    expect(fileLine({ kind: "file", type: "application/pdf", size: 2048, protected: true, preview: "none" })).toBe("Protected PDF · 2 KB");
    expect(fileLine({ kind: "file", type: "application/vnd.ms-excel", doc: "excel", size: 84 * 1024 })).toBe("Excel · 84 KB");
    expect(sizeWords(500)).toBe("500 B");
    expect(sizeWords(3 * 1024 * 1024)).toBe("3 MB");
  });

  it("shows a PDF's picture unless it has none, and a photo's frame before it loads", () => {
    expect(hasPicture({ kind: "file", type: "application/pdf", size: 1 })).toBe(true);
    expect(hasPicture({ kind: "file", type: "application/pdf", size: 1, preview: "failed" })).toBe(false);
    expect(hasPicture({ kind: "file", type: "application/pdf", size: 1, protected: true })).toBe(false);
    expect(hasPicture({ kind: "file", type: "text/plain", size: 1 })).toBe(false);
    expect(frameOf({ kind: "photo", type: "image/jpeg", size: 1, w: 1600, h: 1200 })).toEqual({ width: 280, height: 210 });
    expect(frameOf({ kind: "photo", type: "image/jpeg", size: 1 })).toBeUndefined();
  });

  it("refuses a program and a document with macros before uploading", () => {
    expect(chatFileOK({ name: "prices.docx", type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document" })).toBe(true);
    expect(chatFileOK({ name: "list.csv", type: "" })).toBe(true);
    expect(chatFileOK({ name: "list.csv", type: "application/vnd.ms-excel" })).toBe(true);
    expect(chatFileOK({ name: "photo.jpg", type: "image/jpeg" })).toBe(true);
    expect(chatFileOK({ name: "setup.exe", type: "application/x-msdownload" })).toBe(false);
    expect(chatFileOK({ name: "invoice.pdf.exe", type: "application/pdf" })).toBe(false);
    expect(chatFileOK({ name: "budget.xlsm", type: "application/vnd.ms-excel.sheet.macroEnabled.12" })).toBe(false);
    expect(chatFileOK({ name: "archive.zip", type: "application/zip" })).toBe(false);
  });

  it("draws the card: the picture for a PDF, the type's icon for a document", () => {
    const pdf = renderToString(createElement(FileCard, { a: { kind: "file", type: "application/pdf", size: 2048, name: "Prices.pdf", pages: 3, preview: "ready", w: 640, h: 905 }, src: "/v1/f/1" }));
    expect(pdf).toContain('src="/v1/f/1?preview=1"');
    expect(pdf).toContain("PDF · 3 pages · 2 KB");
    expect(pdf).toContain("<button");
    const word = renderToString(createElement(FileCard, { a: { kind: "file", type: "application/msword", doc: "word", size: 2048, name: "Order.doc" }, src: "/v1/f/2" }));
    expect(word).toContain("file-word");
    expect(word).toContain('download="Order.doc"');
    expect(word).not.toContain("preview=1");
    const failed = renderToString(createElement(FileCard, { a: { kind: "file", type: "application/pdf", size: 2048, name: "x.pdf", preview: "failed" }, src: "/v1/f/3" }));
    expect(failed).toContain("file-pdf");
    expect(failed).not.toContain("preview=1");
    expect(renderToString(createElement(FileTypeIcon, { a: { type: "", name: "deck.pptx" } }))).toContain("file-powerpoint");
  });
});
