import js from "@eslint/js";
import tseslint from "typescript-eslint";

export default tseslint.config(
  // .claude/ holds agent worktrees (other checkouts of this repository);
  // site/ is the built docs site (make docs).
  { ignores: ["**/dist/**", "**/node_modules/**", "web/test-results/**", "web/playwright-report/**", ".claude/**", "site/**", "web/e2e/demo/**"] },
  js.configs.recommended,
  ...tseslint.configs.strict,
  // Plain scripts the public host serves to browsers (docs/public-host.md).
  {
    files: ["engine/**/*.js"],
    languageOptions: {
      sourceType: "script",
      globals: { window: "readonly", document: "readonly", crypto: "readonly", TextEncoder: "readonly", setTimeout: "readonly", URLSearchParams: "readonly" },
    },
  },
  // The phone app's fixture recorder (mobile/tool/capture.mjs) runs on Node.
  {
    files: ["mobile/tool/*.mjs"],
    languageOptions: { globals: { process: "readonly", console: "readonly", URL: "readonly" } },
  },
  // The explainer's renderer, the recordings and the how-to videos
  // (video/README.md) run on Node; the scenes'
  // timeline runs in Chromium.
  {
    files: ["video/*.mjs", "video/recording/*.mjs", "video/howto/*.mjs"],
    languageOptions: { globals: { process: "readonly", console: "readonly", window: "readonly", document: "readonly" } },
  },
  {
    files: ["video/kit/kit.js"],
    languageOptions: {
      sourceType: "script",
      globals: { window: "readonly", document: "readonly", location: "readonly", addEventListener: "readonly", performance: "readonly", requestAnimationFrame: "readonly", URLSearchParams: "readonly" },
    },
  },
  // The service worker of chat push (docs/spec/chat-push.md): a plain script a
  // browser runs apart from the page; its functions are also run by a test.
  {
    files: ["web/public/sw.js"],
    languageOptions: {
      sourceType: "script",
      globals: {
        self: "readonly", module: "writable", fetch: "readonly", URL: "readonly", JSON: "readonly", ServiceWorkerGlobalScope: "readonly",
        btoa: "readonly", AbortController: "readonly", setTimeout: "readonly", clearTimeout: "readonly", Response: "readonly",
      },
    },
  },
  // The card maker's Node script, whose callbacks also run in the browser (docs/strategy/marketing/cards).
  {
    files: ["docs/strategy/marketing/cards/*.mjs"],
    languageOptions: {
      globals: { process: "readonly", console: "readonly", window: "readonly", document: "readonly" },
    },
  },
  // Website experiment 2 (website/experiment-2/README.md): its build runs on
  // Node and in Chromium, its page script in browsers.
  {
    files: ["website/experiment-2/*.mjs"],
    languageOptions: {
      globals: { process: "readonly", console: "readonly", window: "readonly", document: "readonly", DOMParser: "readonly", Node: "readonly" },
    },
  },
  {
    files: ["website/experiment-2/site.js"],
    languageOptions: {
      sourceType: "script",
      globals: { window: "readonly", document: "readonly", setTimeout: "readonly", performance: "readonly", requestAnimationFrame: "readonly", IntersectionObserver: "readonly", ResizeObserver: "readonly" },
    },
  },
  // The engineering atlas (docs/atlas/README.md): its build runs on Node,
  // its page script in browsers.
  {
    files: ["docs/atlas/*.mjs"],
    languageOptions: { globals: { process: "readonly", console: "readonly", Buffer: "readonly" } },
  },
  {
    files: ["docs/atlas/src/*.js"],
    languageOptions: {
      sourceType: "script",
      globals: {
        window: "readonly", document: "readonly", location: "readonly", localStorage: "readonly", navigator: "readonly",
        setTimeout: "readonly", IntersectionObserver: "readonly", HTMLElement: "readonly", Element: "readonly", Node: "readonly",
      },
    },
  },
  {
    files: ["**/*.ts", "**/*.tsx"],
    languageOptions: { parserOptions: { tsconfigRootDir: import.meta.dirname } },
    rules: {
      "@typescript-eslint/consistent-type-imports": "error",
    },
  },
);
