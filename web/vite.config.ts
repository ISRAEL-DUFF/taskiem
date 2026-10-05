import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In development the API runs on :8080 (taskiem serve); Vite proxies to it so
// the session cookie stays same-origin.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: { "/v1": "http://localhost:8080", "/hooks": "http://localhost:8080" },
  },
  build: { outDir: "dist", chunkSizeWarningLimit: 1500 },
});
