import { defineConfig } from "vitest/config";
import { ocrBase } from "./scripts/ocr-version.mjs";

// Unit tests only; e2e/ is Playwright's. __OCR_BASE__ as vite.config.ts.
export default defineConfig({ define: { __OCR_BASE__: JSON.stringify(ocrBase()) }, test: { include: ["src/**/*.test.ts"] } });
