import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  { ignores: ['dist'] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ['**/*.{ts,tsx}'],
    languageOptions: { ecmaVersion: 2022, globals: globals.browser },
    plugins: { 'react-hooks': reactHooks },
    rules: {
      // The classic hook rules. The React Compiler rule set is not enabled:
      // the editor mirrors state into refs on purpose (see PaperPage).
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'error',
      // Enter must never trigger an action while an IME is composing; use
      // isImeComposing() from src/utils/keyboard.ts in every key handler.
      'no-restricted-syntax': [
        'error',
        {
          selector: "BinaryExpression[left.property.name='key'][right.value='Enter']",
          message: "Use isEnter(e) from src/utils/keyboard.ts so IME composition is respected.",
        },
        // Password managers must not offer to fill papers' fields; see
        // src/utils/autofill.ts.
        {
          selector: "JSXOpeningElement[name.name=/^(input|textarea)$/]:not(:has(> JSXAttribute[name.name='type'][value.value=/^(checkbox|radio|file|hidden|range|color|submit|button|reset|image)$/])):not(:has(> JSXSpreadAttribute[argument.name='NO_AUTOFILL']))",
          message: "Spread {...NO_AUTOFILL} from src/utils/autofill.ts so password managers ignore this field.",
        },
      ],
    },
  },
)
