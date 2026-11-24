// Offering a file the page holds (a CSV of one-time links, a list it
// fetched): its blob: address is made when the person asks for the file,
// and revoked once the browser has it. An address made while drawing the
// page would be made again on every render and never let go.

interface Env {
  document: { createElement(tag: "a"): { href: string; download: string; click(): void } };
  URL: { createObjectURL(b: Blob): string; revokeObjectURL(u: string): void };
}

/** Hands the browser blob to save as name. */
export function saveFile(blob: Blob, name: string, env: Env = { document, URL }): void {
  const url = env.URL.createObjectURL(blob);
  try {
    const a = env.document.createElement("a");
    a.href = url;
    a.download = name;
    a.click();
  } finally {
    env.URL.revokeObjectURL(url);
  }
}
