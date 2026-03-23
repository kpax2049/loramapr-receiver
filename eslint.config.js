const js = require("@eslint/js");
const globals = require("globals");

module.exports = [
  {
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
      "no-console": "off"
    }
  },
  {
    files: [".github/workflows/**/*.js"],
    languageOptions: {
      sourceType: "module"
    }
  }
];
