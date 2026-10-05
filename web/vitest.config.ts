import { defineConfig } from "vitest/config";

// Unit tests only; e2e/ is Playwright's.
export default defineConfig({ test: { include: ["src/**/*.test.ts"] } });
