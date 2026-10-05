import { defineConfig } from "@playwright/test";

// Chromium comes from the environment when preinstalled
// (PLAYWRIGHT_CHROMIUM_EXECUTABLE), otherwise from `playwright install`.
const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || undefined;

export default defineConfig({
  testDir: "e2e",
  timeout: 60_000,
  retries: 0,
  reporter: [["list"]],
  use: {
    baseURL: "http://127.0.0.1:18080",
    trace: "retain-on-failure",
    launchOptions: executablePath ? { executablePath } : {},
  },
  webServer: {
    command: "bash e2e/serve.sh",
    url: "http://127.0.0.1:18080/readyz",
    timeout: 180_000,
    reuseExistingServer: false,
    stdout: "ignore",
    stderr: "pipe",
  },
});
