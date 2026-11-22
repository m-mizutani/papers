import { describe, expect, it } from 'vitest'
import { ApiError } from '../api/client'
import { t } from '../i18n'
import { errorMessage } from './errors'

describe('errorMessage', () => {
  it('explains a paper that is being moved', () => {
    expect(errorMessage(new ApiError(409, 'busy', 'x'), t)).toBe(t('busy'))
  })

  it('shows other errors as they are', () => {
    expect(errorMessage(new Error('boom'), t)).toContain('boom')
  })
})
