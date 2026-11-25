// @vitest-environment node
import { fileURLToPath } from 'node:url'
import { ESLint } from 'eslint'
import { describe, expect, it } from 'vitest'
import { NO_AUTOFILL } from './autofill'

// The frontend directory, where eslint.config.js lives, wherever vitest was started.
const FRONTEND = fileURLToPath(new URL('../..', import.meta.url))
const RULE_MESSAGE = 'Spread {...NO_AUTOFILL}'

async function autofillErrors(code: string): Promise<number> {
  const eslint = new ESLint({ cwd: FRONTEND })
  const [result] = await eslint.lintText(code, { filePath: 'src/autofill-fixture.tsx' })
  return (result?.messages ?? []).filter((m) => m.message.startsWith(RULE_MESSAGE)).length
}

describe('NO_AUTOFILL', () => {
  it('T2 turns off the browser and every supported password manager', () => {
    expect(NO_AUTOFILL).toEqual({
      autoComplete: 'off',
      'data-1p-ignore': 'true',
      'data-lpignore': 'true',
      'data-bwignore': 'true',
      'data-form-type': 'other',
    })
  })
})

describe('the ESLint rule for NO_AUTOFILL', () => {
  it('T3a reports a text input and a textarea without it', async () => {
    expect(await autofillErrors('export const A = () => <><input value="" /><textarea /></>\n')).toBe(2)
  })

  it('T3b leaves inputs that take no typed text alone', async () => {
    const types = ['checkbox', 'radio', 'file', 'hidden', 'range', 'color', 'submit', 'button', 'reset', 'image']
    expect(await autofillErrors(`export const A = () => <>${types.map((ty) => `<input type="${ty}" />`).join('')}</>\n`)).toBe(0)
  })

  it('T3a a spread or checkbox type nested in an attribute does not excuse the outer field', async () => {
    expect(await autofillErrors("import { NO_AUTOFILL } from './autofill'\nexport const A = () => <input value=\"\" title={String(<span {...NO_AUTOFILL} type=\"checkbox\" />)} />\n")).toBe(1)
  })

  it('T3c accepts fields that spread it', async () => {
    expect(await autofillErrors("import { NO_AUTOFILL } from './autofill'\nexport const A = () => <><input {...NO_AUTOFILL} value=\"\" /><textarea {...NO_AUTOFILL} /></>\n")).toBe(0)
  })
})
