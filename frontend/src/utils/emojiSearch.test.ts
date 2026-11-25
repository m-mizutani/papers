import { describe, expect, it, vi } from 'vitest'
import en from 'emojibase-data/en/compact.json'
import ja from 'emojibase-data/ja/compact.json'
import { buildEmojiNames, classifyIconInput, createNameLoader, emojiHint, MAX_RESULTS, normalizeName, type RawEmoji, searchEmoji } from './emojiSearch'

const names = buildEmojiNames(en as unknown as RawEmoji[], ja as unknown as RawEmoji[])
const find = (q: string) => searchEmoji(names, q).map((x) => x.emoji)

describe('searchEmoji', () => {
  it('T1 finds beer by its English name, in any case', () => {
    expect(find('beer')).toEqual(expect.arrayContaining(['🍺', '🍻']))
    expect(find('BEER')).toEqual(find('beer'))
  })

  it('T2 finds beer by its Japanese name, in katakana or hiragana', () => {
    expect(find('ビール')).toEqual(expect.arrayContaining(['🍺', '🍻']))
    expect(find('びーる')).toEqual(find('ビール'))
  })

  it('T3 treats full-width letters as half-width, needs every word, and ignores surrounding spaces', () => {
    expect(find('ｂｅｅｒ')).toEqual(find('beer'))
    expect(find('beer mug')).toContain('🍺')
    expect(find('beer')).toContain('🫚')
    expect(find('beer mug')).not.toContain('🫚')
    expect(find('  beer  ')).toEqual(find('beer'))
    expect(find('beer  mug')[0]).toBe('🍺')
    expect(normalizeName('ビール')).toBe('びーる')
  })

  it('T4 leaves out components, flag letters and skin tone variants', () => {
    expect(names.some((x) => x.emoji === '🏻' || x.emoji === '🇦')).toBe(false)
    expect(find('skin tone').some((x) => /\p{Emoji_Modifier}/u.test(x) && [...x].length === 1)).toBe(false)
    expect(find('thumbs up').filter((x) => x.startsWith('👍'))).toEqual(['👍'])
    const long = '👩🏻‍👩🏻‍👧🏻‍👦🏻'
    expect(buildEmojiNames([{ hexcode: 'X', unicode: long, label: 'family', group: 1 }], [])).toEqual([])
  })

  it('T5 puts an exact name first', () => {
    expect(find('rocket')[0]).toBe('🚀')
    expect(find('beer mug')[0]).toBe('🍺')
  })

  it('T6 stops at the limit', () => {
    expect(find('a')).toHaveLength(MAX_RESULTS)
    expect(find('')).toEqual([])
  })
})

describe('classifyIconInput', () => {
  it('T7 tells an emoji, refused emoji and a name apart', () => {
    expect(classifyIconInput('  ')).toBe('empty')
    expect(classifyIconInput('🧪')).toBe('icon')
    expect(classifyIconInput('1️⃣')).toBe('icon')
    expect(classifyIconInput('🚀🔥')).toBe('invalid')
    expect(classifyIconInput('🇯🇵🇺🇸')).toBe('invalid')
    for (const n of ['beer', 'ビール', 'a1', 'a']) expect(classifyIconInput(n)).toBe('name')
  })
})

describe('createNameLoader', () => {
  it('T13 reads once and keeps the names, but tries again after a failure', async () => {
    const read = vi.fn<() => Promise<[RawEmoji[], RawEmoji[]]>>()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue([[{ hexcode: '1F680', unicode: '🚀', label: 'rocket', group: 5 }], []])
    const load = createNameLoader(read)
    expect(read).not.toHaveBeenCalled()
    await expect(load()).rejects.toThrow('offline')
    expect((await load())[0]?.emoji).toBe('🚀')
    await load()
    expect(read).toHaveBeenCalledTimes(2)
  })
})

describe('emojiHint', () => {
  it('T12 names an emoji in the language of the screen', () => {
    const beer = names.find((x) => x.emoji === '🍺')
    expect(beer && emojiHint(beer, 'ja')).toBe('ビールジョッキ')
    expect(beer && emojiHint(beer, 'en')).toBe('beer mug')
  })
})
