import { describe, expect, it } from 'vitest'
import { detectShortcut } from './shortcuts'

describe('detectShortcut', () => {
  it('converts markdown prefixes', () => {
    expect(detectShortcut('## Title')).toEqual({ type: 'heading', props: { level: 2 }, content: 'Title' })
    expect(detectShortcut('- item')).toEqual({ type: 'bulleted_list_item', props: {}, content: 'item' })
    expect(detectShortcut('1. one')).toEqual({ type: 'numbered_list_item', props: {}, content: 'one' })
    expect(detectShortcut('[x] done')).toEqual({ type: 'todo', props: { checked: true }, content: 'done' })
    expect(detectShortcut('```go')).toEqual({ type: 'code', props: { language: 'go' }, content: '' })
    expect(detectShortcut('---')?.type).toBe('divider')
    expect(detectShortcut('> said')?.type).toBe('quote')
  })

  it('leaves plain text alone', () => {
    expect(detectShortcut('plain #text')).toBeUndefined()
    expect(detectShortcut('#nospace')).toBeUndefined()
  })
})
