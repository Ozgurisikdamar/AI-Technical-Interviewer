import { defineConfig, globalIgnores } from 'eslint/config';
import nextVitals from 'eslint-config-next/core-web-vitals';
import nextTs from 'eslint-config-next/typescript';

/**
 * Guardrails that encode the security requirements of the candidate UI so that a regression
 * fails `pnpm lint`, not only a code review.
 */
const guardrails = [
  {
    selector: 'Literal[value=/\\/internal(\\/|$)/]',
    message: 'The web app must never reference the control plane internal API (/internal/...).',
  },
  {
    selector: 'TemplateElement[value.raw=/\\/internal(\\/|$)/]',
    message: 'The web app must never reference the control plane internal API (/internal/...).',
  },
  {
    selector:
      'CallExpression[callee.property.name=/^(publishData|sendText|streamText|sendFile|streamBytes|sendChatMessage|performRpc|registerRpcMethod|publishDataTrack|setMetadata|setName|setAttributes)$/]',
    message:
      'Candidates never publish data over LiveKit (canPublishData=false). Text answers go only through POST /api/v1/interviews/{id}/text-responses.',
  },
  {
    selector: 'MemberExpression[object.object.name="process"][object.property.name="env"][property.name=/^NEXT_PUBLIC_/]',
    message: 'Do not expose configuration to the browser bundle; keep settings server-side.',
  },
  {
    selector: 'JSXAttribute[name.name="dangerouslySetInnerHTML"]',
    message: 'Raw HTML injection is not allowed (XSS, strict CSP).',
  },
  {
    selector: 'JSXAttribute[name.name="style"]',
    message: 'Inline style attributes are blocked by the strict CSP. Use CSS modules, or set styles through the CSSOM after mount.',
  },
  {
    selector: 'MemberExpression[object.name=/^(localStorage|sessionStorage|indexedDB)$/]',
    message: 'The candidate UI keeps nothing in browser storage.',
  },
];

/** Session cookies are HttpOnly and set by the server; script writes only the language choice. */
const cookieGuardrail = {
  selector: 'MemberExpression[object.name="document"][property.name="cookie"]',
  message: 'Only src/i18n/preference.ts may use document.cookie (the language choice).',
};

const a11yRules = {
  'jsx-a11y/alt-text': 'error',
  'jsx-a11y/anchor-has-content': 'error',
  'jsx-a11y/anchor-is-valid': 'error',
  'jsx-a11y/aria-activedescendant-has-tabindex': 'error',
  'jsx-a11y/aria-props': 'error',
  'jsx-a11y/aria-proptypes': 'error',
  'jsx-a11y/aria-role': 'error',
  'jsx-a11y/aria-unsupported-elements': 'error',
  'jsx-a11y/autocomplete-valid': 'error',
  'jsx-a11y/click-events-have-key-events': 'error',
  'jsx-a11y/heading-has-content': 'error',
  'jsx-a11y/html-has-lang': 'error',
  'jsx-a11y/iframe-has-title': 'error',
  'jsx-a11y/interactive-supports-focus': 'error',
  'jsx-a11y/label-has-associated-control': 'error',
  'jsx-a11y/media-has-caption': 'error',
  'jsx-a11y/mouse-events-have-key-events': 'error',
  'jsx-a11y/no-access-key': 'error',
  'jsx-a11y/no-autofocus': 'error',
  'jsx-a11y/no-distracting-elements': 'error',
  'jsx-a11y/no-interactive-element-to-noninteractive-role': 'error',
  'jsx-a11y/no-noninteractive-element-interactions': 'error',
  'jsx-a11y/no-noninteractive-element-to-interactive-role': 'error',
  'jsx-a11y/no-noninteractive-tabindex': 'error',
  'jsx-a11y/no-redundant-roles': 'error',
  'jsx-a11y/no-static-element-interactions': 'error',
  'jsx-a11y/role-has-required-aria-props': 'error',
  'jsx-a11y/role-supports-aria-props': 'error',
  'jsx-a11y/scope': 'error',
  'jsx-a11y/tabindex-no-positive': 'error',
};

export default defineConfig([
  ...nextVitals,
  ...nextTs,
  globalIgnores(['.next/**', 'out/**', 'build/**', 'coverage/**', 'next-env.d.ts']),
  {
    name: 'ati/web',
    files: ['**/*.{ts,tsx,mts,mjs}'],
    rules: {
      ...a11yRules,
      // A labelled, scrollable caption region must be keyboard-focusable (WCAG 2.1.1).
      'jsx-a11y/no-noninteractive-tabindex': ['error', { tags: [], roles: ['region', 'tabpanel'] }],
      'no-console': ['error', { allow: ['warn', 'error'] }],
      'no-eval': 'error',
      'no-implied-eval': 'error',
      'no-new-func': 'error',
      'no-script-url': 'error',
      eqeqeq: ['error', 'always'],
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
      '@typescript-eslint/no-unused-expressions': 'error',
      '@typescript-eslint/consistent-type-imports': ['error', { fixStyle: 'inline-type-imports' }],
    },
  },
  {
    name: 'ati/web/guardrails',
    files: ['src/**/*.{ts,tsx}'],
    rules: {
      'no-restricted-syntax': ['error', ...guardrails, cookieGuardrail],
      'no-restricted-globals': [
        'error',
        { name: 'fetch', message: 'Use the same-origin API client in src/lib/api/client.ts.' },
      ],
    },
  },
  {
    name: 'ati/web/language-preference',
    // The single place allowed to write a cookie from script: `ati_lang`, the language choice.
    files: ['src/i18n/preference.ts'],
    rules: { 'no-restricted-syntax': ['error', ...guardrails] },
  },
  {
    name: 'ati/web/api-client',
    // The single place allowed to call fetch: it enforces relative /api/v1 URLs,
    // same-origin credentials, Idempotency-Key and problem+json parsing.
    files: ['src/lib/api/client.ts'],
    rules: { 'no-restricted-globals': 'off' },
  },
  {
    name: 'ati/web/tests-and-scripts',
    // Tests feed hostile inputs (javascript: URLs, /internal/ paths) to prove they are refused.
    files: ['**/*.test.ts', 'scripts/**/*.mjs'],
    rules: { 'no-restricted-globals': 'off', 'no-restricted-syntax': 'off', 'no-console': 'off', 'no-script-url': 'off' },
  },
]);
