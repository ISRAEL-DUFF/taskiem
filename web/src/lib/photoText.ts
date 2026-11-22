// Reading a photo's text on the person's own device (I2; the app does the
// same with ML Kit, I1): Tesseract.js in a worker, its files served by
// Taskiem itself from /ocr/v7 (web/scripts/ocr-assets.mjs), so the photo
// is never sent anywhere to be read. Only the words go to the readers
// (POST /v1/payments/read, /v1/payment-batches/read, /v1/me/saved/read)
// with from: "photo". Loaded only when someone asks for it.

const base = "/ocr/v7";

/** Reads the text in an image file, line by line. Swappable in tests. */
export type PhotoTextReader = (file: Blob) => Promise<string>;

/** Tesseract.js, loaded on first use and kept for the page's life. */
let worker: Promise<{ recognize: (f: Blob) => Promise<{ data: { text: string } }> }> | null = null;

const tesseract: PhotoTextReader = async (file) => {
  if (!worker) {
    worker = import("tesseract.js").then(({ createWorker }) =>
      createWorker("eng", 1, {
        workerPath: `${base}/worker.min.js`,
        corePath: base,
        langPath: base,
        gzip: true,
        workerBlobURL: false,
      }),
    );
    worker.catch(() => {
      worker = null;
    });
  }
  const w = await worker;
  const { data } = await w.recognize(file);
  return data.text;
};

let reader: PhotoTextReader = tesseract;

/** The text in a photo, read on this device: lines trimmed, blank ones dropped. */
export async function readPhotoText(file: Blob): Promise<string> {
  const text = await reader(file);
  return text
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter(Boolean)
    .join("\n");
}

/** For tests: read photos with this instead. */
export function setPhotoTextReader(r: PhotoTextReader | null) {
  reader = r ?? tesseract;
}

/** The words under a photo's button: said wherever a photo is read. */
export const readOnDevice = "Read on this device, not sent.";
