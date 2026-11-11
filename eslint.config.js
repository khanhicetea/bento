import js from "@eslint/js";
import reactHooks from "eslint-plugin-react-hooks";
import tseslint from "typescript-eslint";

export default tseslint.config(
  {
    ignores: ["dist/**", "**/dist/**", "bento/**", "apps/web/src/api/generated/**"],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["apps/web/**/*.{ts,tsx}"],
    plugins: { "react-hooks": reactHooks },
    rules: {
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
      "react-hooks/rules-of-hooks": "error",
      "react-hooks/exhaustive-deps": "error",
      // AGENTS.md: React 19; do not add useMemo, useCallback or React.memo.
      // Named imports are matched by selector: `no-restricted-imports` with importNames also rejects the
      // `import * as React from "react"` namespace imports that the shadcn components use.
      "no-restricted-syntax": [
        "error",
        {
          selector:
            "ImportDeclaration[source.value='react'] > ImportSpecifier[imported.name=/^(useMemo|useCallback|memo)$/]",
          message: "Banned by AGENTS.md.",
        },
        {
          selector:
            "MemberExpression[object.name='React'][property.name=/^(useMemo|useCallback|memo)$/]",
          message: "Banned by AGENTS.md.",
        },
      ],
    },
  },
);
