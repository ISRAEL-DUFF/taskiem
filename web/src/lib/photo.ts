// A photo to send: JPEG, PNG and WebP go as they are; an iPhone's HEIC is
// turned into a JPEG here when the browser can read it (Safari can), and
// otherwise the person is told what to do, in words.

export const HEIC_MESSAGE =
  "This browser cannot read HEIC photos. On an iPhone, Settings → Camera → Formats → Most Compatible makes JPEG photos, or send the photo from Safari.";

/** Whether a file is a HEIC or HEIF photo (by its type, or by its name when the type is empty). */
export function isHeic(f: Pick<File, "type" | "name">): boolean {
  return (
    /^image\/hei[cf]$/i.test(f.type) ||
    (f.type === "" && /\.hei[cf]$/i.test(f.name))
  );
}

/** The name of a converted photo: the same, ending .jpg. */
export function jpegName(name: string): string {
  return name.replace(/\.[A-Za-z0-9]+$/, "") + ".jpg";
}

/**
 * A file ready to send as a photo: HEIC read and written as a JPEG; any
 * other file as it is. Rejects with HEIC_MESSAGE when the browser cannot
 * decode HEIC.
 */
export async function preparePhoto(f: File): Promise<File> {
  if (!isHeic(f)) return f;
  try {
    const bitmap = await createImageBitmap(f);
    const canvas = document.createElement("canvas");
    canvas.width = bitmap.width;
    canvas.height = bitmap.height;
    canvas.getContext("2d")?.drawImage(bitmap, 0, 0);
    bitmap.close?.();
    const blob = await new Promise<Blob | null>((ok) =>
      canvas.toBlob(ok, "image/jpeg", 0.9),
    );
    if (!blob) throw new Error("no picture");
    return new File([blob], jpegName(f.name), { type: "image/jpeg" });
  } catch {
    throw new Error(HEIC_MESSAGE);
  }
}
