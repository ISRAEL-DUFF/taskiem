import { describe, expect, it } from "vitest";
import {
  canScanWithCamera,
  deviceSummary,
  scanFormatOf,
  scanFormatsOf,
  toolFileValue,
  toolInputFiles,
} from "./forms";

// Device fields, build-plan-gaps H3: a code read by the browser's own
// barcode reader (no outside library), and a tool's photo and signature
// sent first, then named by their id in the start.
describe("scanning on the web", () => {
  it("names the browser's formats as the server does", () => {
    expect(scanFormatOf("qr_code")).toBe("qr");
    expect(scanFormatOf("ean_13")).toBe("ean_13");
    expect(scanFormatOf("pdf417")).toBeUndefined();
  });
  it("takes the field's formats, else every one", () => {
    expect(scanFormatsOf({ type: "scan", formats: ["ean_13"] } as never)).toEqual(["ean_13"]);
    expect(scanFormatsOf({ type: "scan" } as never)).toHaveLength(6);
  });
  it("offers the camera only where the browser has a barcode reader", () => {
    expect(canScanWithCamera()).toBe(false);
  });
  it("says a scanned code from a typed one", () => {
    expect(deviceSummary("scan", { text: "6151100093217", format: "ean_13" })).toBe("Code scanned: 6151100093217");
    expect(deviceSummary("scan", { text: "ABC" })).toBe("Code: ABC");
  });
});

describe("a tool's photo and signature", () => {
  it("splits the files to send first from the rest", () => {
    const photo = new File(["x"], "door.jpg", { type: "image/jpeg" });
    const pad = new File(["y"], "signature.png", { type: "image/png" });
    const { rest, files } = toolInputFiles({
      where: { lat: 6.5, lng: 3.4 },
      proof: photo,
      signed: { file: pad, signed_by: " Ngozi " },
      note: "ok",
    });
    expect(rest).toEqual({ where: { lat: 6.5, lng: 3.4 }, note: "ok" });
    expect(files.map((f) => [f.name, f.file.name, f.signed_by])).toEqual([
      ["proof", "door.jpg", undefined],
      ["signed", "signature.png", "Ngozi"],
    ]);
    expect(toolFileValue("0193", "Ngozi")).toEqual({ file: "0193", signed_by: "Ngozi" });
    expect(toolFileValue("0193")).toEqual({ file: "0193" });
  });
});
