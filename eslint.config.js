const js = require("@eslint/js");
const globals = require("globals");

module.exports = [
  {
    linterOptions: {
      reportUnusedDisableDirectives: "error"
    },
    ignores: [
      "**/dist/**",
      "**/build/**",
      "**/out/**",
      "**/coverage/**",
      "**/vendor/**",
      "**/.cache/**",
      "**/*.min.js"
    ]
  },
  js.configs.recommended,
  {
    files: ["**/*.{js,cjs,mjs}"],
    languageOptions: {
      ecmaVersion: "latest",
      sourceType: "script",
      globals: {
        ...globals.node
      }
    },
    rules: {
      "no-console": "off",
      eqeqeq: ["error", "always", { null: "ignore" }],
      "no-implicit-coercion": "error",
      "no-unneeded-ternary": "error"
    }
  },
  {
    files: [".github/workflows/**/*.js"],
    languageOptions: {
      sourceType: "module"
    }
  }
];
