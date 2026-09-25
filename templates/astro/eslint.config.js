import astro from 'eslint-plugin-astro'
import jsxA11y from 'eslint-plugin-jsx-a11y'
import tseslint from 'typescript-eslint'

// `lidza check` runs this. The a11y rules are errors: a page a screen
// reader cannot use does not pass. Frontmatter and <script> blocks are
// TypeScript, parsed as such.
export default [
  { ignores: ['dist', 'node_modules', '.lidza', '.astro', 'e2e', 'playwright-report', 'test-results'] },
  ...astro.configs['flat/recommended'],
  ...astro.configs['flat/jsx-a11y-recommended'],
  {
    files: ['**/*.astro'],
    plugins: { 'jsx-a11y': jsxA11y },
    languageOptions: {
      parserOptions: { parser: tseslint.parser, extraFileExtensions: ['.astro'] },
    },
    processor: 'astro/client-side-ts',
  },
  {
    files: ['**/*.astro/*.ts', '**/*.ts'],
    languageOptions: { parser: tseslint.parser },
  },
]
