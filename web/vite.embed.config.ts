import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { fileURLToPath } from "node:url";

// The embeddable bundle (docs/embedding.md): one self-contained classic
// script, /embed/v1/taskiem.js, with its styles inlined (they go into each
// element's shadow root). Built after the console, into dist/embed/v1,
// which the server serves with its own cache policy (api/embedframe.go).
export default defineConfig({
  plugins: [react()],
  define: { "process.env.NODE_ENV": JSON.stringify("production") },
  build: {
    outDir: "dist/embed/v1",
    emptyOutDir: true,
    copyPublicDir: false,
    chunkSizeWarningLimit: 1500,
    lib: {
      entry: fileURLToPath(new URL("src/embed/index.tsx", import.meta.url)),
      name: "TaskiemEmbed",
      formats: ["iife"],
      fileName: () => "taskiem.js",
    },
  },
});
