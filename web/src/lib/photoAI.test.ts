import { afterEach, describe, expect, it, vi } from "vitest";
import { aiHinted, aiOffered, aiPhotoSetting, forgetAIPhotoSetting, noLineRead, offerSaid, readPhotoAI, setAIPhotoSetting, shrinkPhoto } from "./photoAI";
import { tidyLines } from "./photoText";

const on = { ai_photo_reading: true, available: true };
const off = { ai_photo_reading: false, available: true };
const none = { ai_photo_reading: true, available: false };

describe("reading a photo with AI", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    forgetAIPhotoSetting();
  });

  it("is offered only where it is on and this Taskiem can do it", () => {
    expect(aiOffered(on)).toBe(true);
    expect(aiOffered(off)).toBe(false);
    expect(aiOffered(none)).toBe(false);
  });

  it("tells only an owner, where it is off but could be on", () => {
    expect(aiHinted(off, true)).toBe(true);
    expect(aiHinted(off, false)).toBe(false);
    expect(aiHinted(on, true)).toBe(false);
    expect(aiHinted({ ai_photo_reading: false, available: false }, true)).toBe(false);
  });

  it("says what the device found without its 'Type it in' (a button there)", () => {
    expect(offerSaid("No account number in that photo. Type it in.")).toBe("No account number in that photo.");
    expect(offerSaid("Not one line of that photo could be read.")).toBe("Not one line of that photo could be read.");
    expect(offerSaid("Type it in.")).toBe("This device could not read that photo.");
  });

  it("finds no line read when every line has a problem, or there are none", () => {
    expect(noLineRead([])).toBe(true);
    expect(noLineRead([{ problem: "No account number" }, { problem: "No bank" }])).toBe(true);
    expect(noLineRead([{ problem: "No bank" }, {}])).toBe(false);
  });

  it("reads the setting once, and as Settings last saved it", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify(on), { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    expect(await aiPhotoSetting()).toEqual(on);
    expect(await aiPhotoSetting()).toEqual(on);
    expect(fetch).toHaveBeenCalledTimes(1);
    setAIPhotoSetting(off);
    expect(await aiPhotoSetting()).toEqual(off);
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("is off when the setting cannot be read, and asked again next time", async () => {
    const fetch = vi.fn(async () => new Response("{}", { status: 500 }));
    vi.stubGlobal("fetch", fetch);
    expect(await aiPhotoSetting()).toEqual({ ai_photo_reading: false, available: false });
    await aiPhotoSetting();
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("sends the photo as the multipart part 'photo' and tidies the words back", async () => {
    let sent: FormData | undefined;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_url: string, init: RequestInit) => {
        sent = init.body as FormData;
        return new Response(JSON.stringify({ text: " Chidi \n\n 0123456789 " }), { status: 200 });
      }),
    );
    const photo = new File(["x"], "list.png", { type: "image/png" });
    expect(await readPhotoAI(photo)).toEqual({ text: "Chidi\n0123456789", truncated: false });
    expect((sent?.get("photo") as File).name).toBe("list.png");
  });

  it("says when the AI's answer was cut off", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ text: "Chidi 0123456789", truncated: true }), { status: 200 })));
    expect(await readPhotoAI(new Blob(["x"]))).toEqual({ text: "Chidi 0123456789", truncated: true });
  });

  describe("made smaller before it is sent", () => {
    const drawn: { w: number; h: number; type?: string; quality?: number }[] = [];
    const stubImage = (width: number, height: number) => {
      vi.stubGlobal("createImageBitmap", vi.fn(async () => ({ width, height, close: () => {} })));
      const canvas = {
        width: 0,
        height: 0,
        getContext: () => ({ fillRect: () => {}, drawImage: () => {}, fillStyle: "" }),
        toBlob(cb: (b: Blob | null) => void, type?: string, quality?: number) {
          drawn.push({ w: this.width, h: this.height, type, quality });
          cb(new Blob(["jpeg"], { type: "image/jpeg" }));
        },
      };
      vi.stubGlobal("document", { createElement: () => canvas });
    };
    afterEach(() => {
      drawn.length = 0;
    });

    it("draws a large photo with its long edge at 2576, as a JPEG", async () => {
      stubImage(4032, 3024);
      const out = await shrinkPhoto(new File(["x"], "IMG_0042.jpeg", { type: "image/jpeg" }));
      expect(drawn).toEqual([{ w: 2576, h: 1932, type: "image/jpeg", quality: 0.85 }]);
      expect(out).toBeInstanceOf(File);
      expect((out as File).name).toBe("IMG_0042.jpg");
      expect(out.type).toBe("image/jpeg");
    });

    it("turns a HEIC the browser opens into a JPEG, at its own size", async () => {
      stubImage(1200, 1600);
      const out = await shrinkPhoto(new File(["x"], "IMG_0043.HEIC", { type: "image/heic" }));
      expect(drawn).toEqual([{ w: 1200, h: 1600, type: "image/jpeg", quality: 0.85 }]);
      expect(out.type).toBe("image/jpeg");
    });

    it("sends a small screenshot as it is", async () => {
      stubImage(1170, 2532);
      const photo = new File(["x"], "list.png", { type: "image/png" });
      expect(await shrinkPhoto(photo)).toBe(photo);
      expect(drawn).toEqual([]);
    });

    it("sends what the browser cannot open as it is, for the server to say", async () => {
      vi.stubGlobal("createImageBitmap", vi.fn(async () => Promise.reject(new Error("no"))));
      const photo = new File(["%PDF"], "list.pdf", { type: "application/pdf" });
      expect(await shrinkPhoto(photo)).toBe(photo);
    });
  });

  it("throws a refusal in the server's words", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ error: "This month's AI budget is used up: type the details in.", code: "budget" }), { status: 409 })));
    await expect(readPhotoAI(new Blob(["x"]))).rejects.toThrow("This month's AI budget is used up");
  });

  it("tidies lines as the device's are", () => {
    expect(tidyLines(" a \r\n\n b ")).toBe("a\nb");
  });
});
