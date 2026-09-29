import js from '@eslint/js'
import globals from 'globals'
import tseslint from 'typescript-eslint'
import jsxA11y from 'eslint-plugin-jsx-a11y-x'

// `lidza check` runs this. jsx-a11y's recommended rules are errors: a page
// a screen reader cannot use does not pass. jsx-a11y-x is the maintained
// fork of eslint-plugin-jsx-a11y, registered under the original name so
// rule ids stay jsx-a11y/*.
const a11y = {
  ...jsxA11y.configs.recommended,
  name: 'jsx-a11y/recommended',
  plugins: { 'jsx-a11y': jsxA11y },
  rules: Object.fromEntries(
    Object.entries(jsxA11y.configs.recommended.rules).map(([id, level]) => [id.replace(/^jsx-a11y-x\//, 'jsx-a11y/'), level]),
  ),
}

export default tseslint.config(
  { ignores: ['dist', 'node_modules', '.lidza', 'scripts', 'e2e', 'playwright-report', 'test-results'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  a11y,
  {
    files: ['**/*.{ts,tsx}'],
    languageOptions: { globals: globals.browser },
  },
)
