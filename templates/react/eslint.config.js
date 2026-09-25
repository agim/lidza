import js from '@eslint/js'
import globals from 'globals'
import tseslint from 'typescript-eslint'
import jsxA11y from 'eslint-plugin-jsx-a11y'

// `lidza check` runs this. jsx-a11y's recommended rules are errors: a page
// a screen reader cannot use does not pass.
export default tseslint.config(
  { ignores: ['dist', 'node_modules', '.lidza', 'scripts', 'e2e', 'playwright-report', 'test-results'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  jsxA11y.flatConfigs.recommended,
  {
    files: ['**/*.{ts,tsx}'],
    languageOptions: { globals: globals.browser },
  },
)
