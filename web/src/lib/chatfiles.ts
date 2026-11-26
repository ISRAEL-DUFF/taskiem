// A chat's files as WhatsApp shows them (docs/spec/chat-previews-and-
// editing.md §1): a PDF with the picture of its first page and its pages,
// a document with its type's icon and colour, a photo at its own
// proportions before it has loaded. The server reads a file's type from
// its own bytes; what is said here is only what the bubble shows.

/** What every chat's attachment carries (all optional past kind, type and
 * size, so older messages read as before). */
export type FileFacts = {
  kind: "photo" | "voice" | "file" | string;
  type: string;
  size: number;
  name?: string;
  /** A PDF's page count, once its picture is drawn. */
  pages?: number;
  /** A password-protected PDF. */
  protected?: boolean;
  /** The picture of a PDF's first page: ready, none or failed; missing
   * while it is drawn. */
  preview?: "ready" | "none" | "failed";
  /** A photo's, or the PDF picture's, width and height. */
  w?: number;
  h?: number;
  /** A document's family. */
  doc?: "word" | "excel" | "powerpoint" | "opendocument" | "csv" | "text";
};

/** The family of a file for its icon and its colour. */
export type FileFamily = "pdf" | "word" | "excel" | "powerpoint" | "opendocument" | "csv" | "text" | "file";

const byExt: Record<string, FileFamily> = {
  pdf: "pdf",
  doc: "word",
  docx: "word",
  xls: "excel",
  xlsx: "excel",
  ppt: "powerpoint",
  pptx: "powerpoint",
  odt: "opendocument",
  ods: "opendocument",
  odp: "opendocument",
  csv: "csv",
  txt: "text",
};

/** A file's family: the server's word for it, else its type, else its
 * name's extension (a file still being sent). */
export function fileFamily(a: Pick<FileFacts, "type" | "doc" | "name">): FileFamily {
  if (a.doc) return a.doc;
  const t = (a.type || "").split(";")[0]?.trim().toLowerCase() ?? "";
  if (t === "application/pdf") return "pdf";
  if (t.includes("wordprocessingml") || t === "application/msword") return "word";
  if (t.includes("spreadsheetml") || t === "application/vnd.ms-excel") return "excel";
  if (t.includes("presentationml") || t === "application/vnd.ms-powerpoint") return "powerpoint";
  if (t.startsWith("application/vnd.oasis.opendocument")) return "opendocument";
  if (t === "text/csv") return "csv";
  if (t === "text/plain") return "text";
  const ext = (a.name || "").toLowerCase().split(".").pop() || "";
  return byExt[ext] ?? "file";
}

const familyWords: Record<FileFamily, string> = {
  pdf: "PDF",
  word: "Word",
  excel: "Excel",
  powerpoint: "PowerPoint",
  opendocument: "OpenDocument",
  csv: "CSV",
  text: "Text",
  file: "File",
};

/** A file's size in words: "84 KB", "1.2 MB". */
export function sizeWords(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1).replace(/\.0$/, "")} MB`;
}

/** The line under a file's name: "PDF · 3 pages · 1.2 MB", "Protected
 * PDF · 1.2 MB", "Excel · 84 KB". */
export function fileLine(a: FileFacts): string {
  const family = fileFamily(a);
  const parts: string[] = [];
  if (family === "pdf" && a.protected) parts.push("Protected PDF");
  else parts.push(familyWords[family]);
  if (family === "pdf" && !a.protected && a.pages) parts.push(a.pages === 1 ? "1 page" : `${a.pages} pages`);
  parts.push(sizeWords(a.size));
  return parts.join(" · ");
}

/** A PDF shows its first page's picture; ?preview=1 answers it. One whose
 * picture failed, or that is protected, shows the icon. */
export function hasPicture(a: FileFacts): boolean {
  return fileFamily(a) === "pdf" && !a.protected && a.preview !== "none" && a.preview !== "failed";
}

/** The size a photo or a PDF's picture takes before it has loaded, so the
 * chat does not jump: its own proportions, at most this wide. */
export function frameOf(a: FileFacts, width = 280): { width: number; height: number } | undefined {
  if (!a.w || !a.h) return undefined;
  const w = Math.min(width, a.w);
  return { width: w, height: Math.round((w * a.h) / a.w) };
}

/** What a chat accepts (§1.1): photos, voice notes, PDFs and Word, Excel,
 * PowerPoint, OpenDocument, CSV and text documents, up to 25 MB. The
 * server checks the bytes; this only spares a refused upload. */
export const CHAT_DOCUMENTS_ACCEPT =
  "application/pdf,.pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.odt,.ods,.odp,.csv,.txt," +
  "application/msword,application/vnd.ms-excel,application/vnd.ms-powerpoint," +
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document," +
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet," +
  "application/vnd.openxmlformats-officedocument.presentationml.presentation," +
  "application/vnd.oasis.opendocument.text,application/vnd.oasis.opendocument.spreadsheet,application/vnd.oasis.opendocument.presentation," +
  "text/csv,text/plain";

export const CHAT_FILES_REFUSED = "Taskiem can send photos, voice notes, PDFs and Word, Excel, PowerPoint and text documents.";

const okTypes = /^(image\/(jpeg|png|webp)|audio\/|application\/pdf$|application\/msword$|application\/vnd\.ms-(excel|powerpoint)$|application\/vnd\.openxmlformats-officedocument\.(wordprocessingml\.document|spreadsheetml\.sheet|presentationml\.presentation)$|application\/vnd\.oasis\.opendocument\.(text|spreadsheet|presentation)$|text\/(csv|plain)$)/;
const okNames = /\.(pdf|docx?|xlsx?|pptx?|odt|ods|odp|csv|txt|jpe?g|png|webp)$/i;
const programs = /\.(exe|msi|bat|cmd|com|scr|ps1|sh|apk|app|dmg|jar|js|vbs|docm|xlsm|pptm|dotm)$/i;

/** Whether a chosen file is of a kind a chat takes: by its type, or, when
 * the browser gives none it knows, by its name; never a program or a
 * document with macros, whatever its type says. */
export function chatFileOK(f: { name: string; type: string }): boolean {
  if (programs.test(f.name)) return false;
  if (f.type && okTypes.test(f.type)) return true;
  return (!f.type || f.type === "application/octet-stream" || f.type === "application/vnd.ms-excel") && okNames.test(f.name);
}
