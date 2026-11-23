import { describe, expect, it } from 'vitest'
import type { BlockProps, BlockType } from '../api/types'
import { gapBetween } from './spacing'

const b = (type: BlockType, props: BlockProps = {}) => ({ type, props })

describe('gapBetween', () => {
  it('follows the block rhythm of the spec', () => {
    expect(gapBetween(undefined, b('paragraph'))).toBe('none')
    expect(gapBetween(b('bulleted_list_item'), b('todo'))).toBe('list')
    expect(gapBetween(b('paragraph'), b('paragraph'))).toBe('text')
    expect(gapBetween(b('heading', { level: 2 }), b('paragraph'))).toBe('head-after')
    expect(gapBetween(b('paragraph'), b('heading', { level: 1 }))).toBe('h1')
    expect(gapBetween(b('paragraph'), b('heading', { level: 2 }))).toBe('h2')
    expect(gapBetween(b('paragraph'), b('heading', { level: 3 }))).toBe('h3')
    expect(gapBetween(b('heading', { level: 1 }), b('heading', { level: 2 }))).toBe('head-pair')
    expect(gapBetween(b('heading', { level: 2 }), b('table'))).toBe('head-box')
    expect(gapBetween(b('paragraph'), b('code'))).toBe('box')
    expect(gapBetween(b('callout'), b('paragraph'))).toBe('box')
    expect(gapBetween(b('paper'), b('paper'))).toBe('card-run')
    expect(gapBetween(b('paragraph'), b('paper'))).toBe('card')
    expect(gapBetween(b('paragraph'), b('quote'))).toBe('quote')
    expect(gapBetween(b('quote'), b('paragraph'))).toBe('quote')
    expect(gapBetween(b('paragraph'), b('divider'))).toBe('none')
  })
})
