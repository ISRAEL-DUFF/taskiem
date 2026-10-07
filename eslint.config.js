import js from "@eslint/js";
import tseslint from "typescript-eslint";

export default tseslint.config(
  // .claude/ holds agent worktrees (other checkouts of this repository).
  { ignores: ["**/dist/**", "**/node_modules/**", "web/test-results/**", "web/playwright-report/**", ".claude/**"] },
  js.configs.recommended,
  ...tseslint.configs.strict,
  {
    files: ["**/*.ts", "**/*.tsx"],
    languageOptions: { parserOptions: { tsconfigRootDir: import.meta.dirname } },
    rules: {
      "@typescript-eslint/consistent-type-imports": "error",
    },
  },
);
