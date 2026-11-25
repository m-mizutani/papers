import { describe, expect, it } from 'vitest'
import { DEFAULTS, isSet, readFilters, toQuery } from './papersFilters'

const ME = 'me0000000000000000000a'
const AIKO = '4fQ0000000000000000000'

describe('papers list filters', () => {
  it('T10 uses the stored choice only when the URL has no filter', () => {
    const stored = JSON.stringify({ place: 'filed', creator: 'all', visibility: 'public', archived: true, sort: 'created' })
    expect(readFilters(new URLSearchParams(''), stored, ME)).toEqual({ place: 'filed', creator: 'all', visibility: 'public', archived: true, sort: 'created' })
    expect(readFilters(new URLSearchParams('place=all'), stored, ME)).toEqual({ ...DEFAULTS, place: 'all' })
    expect(readFilters(new URLSearchParams(`creator=${AIKO}`), stored, ME)).toEqual({ ...DEFAULTS, creator: AIKO })
    expect(readFilters(new URLSearchParams(''), null, ME)).toEqual(DEFAULTS)
    expect(readFilters(new URLSearchParams(''), '{broken', ME)).toEqual(DEFAULTS)
  })

  it('T10 writes only what differs from the defaults', () => {
    expect(toQuery(DEFAULTS).toString()).toBe('')
    expect(toQuery({ ...DEFAULTS, place: 'all', creator: AIKO, archived: true }).toString()).toBe(`place=all&creator=${AIKO}&archived=true`)
  })

  it('T11 takes out-of-range values as defaults and my own ID as me', () => {
    expect(readFilters(new URLSearchParams('place=xyz&visibility=secret&sort=title&archived=yes'), null, ME)).toEqual(DEFAULTS)
    const f = readFilters(new URLSearchParams(`creator=${ME}`), null, ME)
    expect(f.creator).toBe('me')
    expect(toQuery(f).has('creator')).toBe(false)
  })

  it('T7 a filter is set when it differs from its default', () => {
    expect(isSet(DEFAULTS, 'visibility')).toBe(false)
    expect(isSet({ ...DEFAULTS, visibility: 'private' }, 'visibility')).toBe(true)
    expect(isSet({ ...DEFAULTS, creator: AIKO }, 'creator')).toBe(true)
  })
})
