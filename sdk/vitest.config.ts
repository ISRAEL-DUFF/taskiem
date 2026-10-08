import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: {
    alias: {
      "@taskiem/sdk": fileURLToPath(new URL("./src/index.ts", import.meta.url)),
      "@taskiem/connectors": fileURLToPath(new URL("./src/connectors.gen.ts", import.meta.url)),
    },
  },
});
