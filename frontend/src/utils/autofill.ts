// Attributes that keep password managers and the browser's own history from
// offering to fill a field. papers has no field that wants autofill (sign-in
// is Google OIDC), yet labels such as "Search by name" make 1Password offer
// identities. 1Password ignores autocomplete="off", so each manager's own
// attribute is set. Spread it into every text <input> and <textarea>; ESLint
// enforces this (eslint.config.js).
export const NO_AUTOFILL = {
  autoComplete: 'off',
  'data-1p-ignore': 'true',
  'data-lpignore': 'true',
  'data-bwignore': 'true',
  'data-form-type': 'other',
} as const
