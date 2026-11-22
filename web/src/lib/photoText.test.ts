import { afterEach, describe, expect, it } from "vitest";
import { readPhotoText, setPhotoTextReader } from "./photoText";

describe("a photo's text", () => {
  afterEach(() => setPhotoTextReader(null));

  it("is read by the reader put in, its lines trimmed and blank ones dropped", async () => {
    const seen: Blob[] = [];
    setPhotoTextReader(async (f) => {
      seen.push(f);
      return "  Chidi Okeke \r\n\n 0123456789  \n   \nGTBank\n";
    });
    const photo = new Blob(["not a real photo"], { type: "image/png" });
    expect(await readPhotoText(photo)).toBe("Chidi Okeke\n0123456789\nGTBank");
    expect(seen).toEqual([photo]);
  });

  it("is empty for a photo with no words", async () => {
    setPhotoTextReader(async () => " \n \n");
    expect(await readPhotoText(new Blob([]))).toBe("");
  });
});
