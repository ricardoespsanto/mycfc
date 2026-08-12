import js from "@eslint/js";

export default [
  {
    ignores: ["ui/static/dist/**", "playwright-report/**", "test-results/**", "artifacts/**"],
  },
  js.configs.recommended,
  {
    files: ["**/*.js", "**/*.mjs"],
    languageOptions: {
      ecmaVersion: "latest",
      sourceType: "module",
      globals: {
        Buffer: "readonly",
        URL: "readonly",
        clearTimeout: "readonly",
        console: "readonly",
        document: "readonly",
        fetch: "readonly",
        FormData: "readonly",
        getComputedStyle: "readonly",
        history: "readonly",
        HTMLElement: "readonly",
        HTMLAnchorElement: "readonly",
        HTMLButtonElement: "readonly",
        HTMLDetailsElement: "readonly",
        HTMLFormElement: "readonly",
        matchMedia: "readonly",
        process: "readonly",
        setTimeout: "readonly",
        window: "readonly",
      },
    },
  },
];
