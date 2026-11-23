// Values the Vite configs put in at build time (vite.config.ts `define`).

/** Where the photo reader's files are served from, named by their exact
 * versions (scripts/ocr-version.mjs), e.g. "/ocr/7.0.0-core7.0.0-eng1.0.0". */
declare const __OCR_BASE__: string;
