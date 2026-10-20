import js from "@eslint/js";
import tseslint from "typescript-eslint";

export default tseslint.config(
  // .claude/ holds agent worktrees (other checkouts of this repository);
  // site/ is the built docs site (make docs).
  { ignores: ["**/dist/**", "**/node_modules/**", "web/test-results/**", "web/playwright-report/**", ".claude/**", "site/**"] },
  js.configs.recommended,
  ...tseslint.configs.strict,
  // Plain scripts the public host serves to browsers (docs/public-host.md).
  {
    files: ["engine/**/*.js"],
    languageOptions: {
      sourceType: "script",
      globals: { window: "readonly", document: "readonly", crypto: "readonly", TextEncoder: "readonly", setTimeout: "readonly" },
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
