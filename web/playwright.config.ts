import { defineConfig } from "@playwright/test";

// Chromium comes from the environment when preinstalled
// (PLAYWRIGHT_CHROMIUM_EXECUTABLE), otherwise from `playwright install`.
const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || undefined;

export default defineConfig({
  testDir: "e2e",
  // On CI, one test at a time on each of four shards (ci.yml, web-e2e):
  // two workers, the e2e server, Postgres and two Chromiums starve one
  // hosted runner of CPU, and tests time out or the runner is stopped
  // (PR 186). Locally, Playwright's default.
  workers: process.env.CI ? 1 : undefined,
  timeout: 60_000,
  retries: 0,
  reporter: [["list"]],
  use: {
    baseURL: "http://127.0.0.1:18080",
    trace: "retain-on-failure",
    // The console's motion is off for people who ask for less: tests ask
    // too, so axe never checks a page mid-fade and nothing waits on it.
    reducedMotion: "reduce",
    // The app starts a service worker for everyone (docs/spec/offline.md). A
    // page it controls can send requests past page.route, so a test's stand-in
    // for the server would be skipped now and then. Blocked here; the specs
    // about the worker itself allow it (chat-push, offline).
    serviceWorkers: "block",
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
