import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { detectLang, t } from '../i18n'
import { buildEmojiNames, type EmojiName } from '../utils/emojiSearch'
import { IconPicker } from './IconPicker'

const NAMES = buildEmojiNames(
  [
    { hexcode: '1F37A', unicode: '🍺', label: 'beer mug', tags: ['beer', 'pint'], group: 4 },
    { hexcode: '1F37B', unicode: '🍻', label: 'clinking beer mugs', tags: ['beer', 'cheers'], group: 4 },
    { hexcode: '1F680', unicode: '🚀', label: 'rocket', tags: ['space'], group: 5 },
  ],
  [
    { hexcode: '1F37A', unicode: '🍺', label: 'ビールジョッキ', tags: ['ビール'], group: 4 },
    { hexcode: '1F680', unicode: '🚀', label: 'ロケット', tags: ['宇宙'], group: 5 },
  ],
)

function open(value = '', loadNames: () => Promise<EmojiName[]> = () => Promise.resolve(NAMES)) {
  const onPick = vi.fn()
  render(<IconPicker anchor={new DOMRect(0, 0, 10, 10)} value={value} onPick={onPick} onClose={() => {}} loadNames={loadNames} />)
  return onPick
}

const type = (v: string) => fireEvent.change(screen.getByLabelText(t('iconInput')), { target: { value: v } })
const flush = () => act(async () => { await Promise.resolve() })

describe('IconPicker', () => {
  it('T15 picks an offered emoji', async () => {
    const onPick = open()
    await flush()
    fireEvent.click(screen.getByRole('button', { name: '🚀' }))
    expect(onPick).toHaveBeenCalledWith('🚀')
    expect(screen.queryByRole('button', { name: t('iconRemove') })).toBeNull()
  })

  it('T8 shows the emoji a name finds, without the one-emoji error', async () => {
    open()
    await flush()
    type('beer')
    expect(screen.getByRole('button', { name: '🍺' }).className).toContain('first')
    expect(screen.getByRole('button', { name: '🍻' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: '🚀' })).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
    type('ビール')
    expect(screen.getByRole('button', { name: '🍺' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: '🍻' })).toBeNull()
  })

  it('T9 sets the first result with Enter, but not while the IME composes', async () => {
    const onPick = open()
    await flush()
    type('beer')
    const input = screen.getByLabelText(t('iconInput'))
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true })
    expect(onPick).not.toHaveBeenCalled()
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onPick).toHaveBeenCalledWith('🍺')
  })

  it('T9 the Set button sets the first result too', async () => {
    const onPick = open()
    await flush()
    type('beer')
    fireEvent.click(screen.getByRole('button', { name: t('iconSet') }))
    expect(onPick).toHaveBeenCalledWith('🍺')
  })

  it('T10 sets a single character no name matches as typed', async () => {
    const onPick = open()
    await flush()
    type('★')
    expect(screen.getByRole('status').textContent).toBe(t('iconNoMatch'))
    fireEvent.keyDown(screen.getByLabelText(t('iconInput')), { key: 'Enter' })
    expect(onPick).toHaveBeenCalledWith('★')
  })

  it('T10 sets a typed emoji with Enter, and refuses two', async () => {
    const onPick = open()
    await flush()
    const input = screen.getByLabelText(t('iconInput'))
    type('🧪')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onPick).toHaveBeenCalledWith('🧪')
    onPick.mockClear()
    type('🚀🔥')
    expect(screen.getByRole('alert').textContent).toBe(t('iconOne'))
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onPick).not.toHaveBeenCalled()
    expect((screen.getByRole('button', { name: t('iconSet') }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('T11 says when nothing matches', async () => {
    open()
    await flush()
    type('xyzzy')
    expect(screen.getByRole('status').textContent).toBe(t('iconNoMatch'))
  })

  it('T11 says the names are loading', () => {
    open('', () => new Promise(() => {}))
    type('beer')
    expect(screen.getByRole('status').textContent).toBe(t('iconLoading'))
  })

  it('T11 keeps the offered emoji and typing when the names cannot be read', async () => {
    const onPick = open('', () => Promise.reject(new Error('offline')))
    await flush()
    type('beer')
    await flush()
    expect(screen.getByRole('alert').textContent).toBe(t('iconNamesFailed'))
    expect(screen.getByRole('button', { name: '🚀' })).toBeTruthy()
    type('🧪')
    fireEvent.keyDown(screen.getByLabelText(t('iconInput')), { key: 'Enter' })
    expect(onPick).toHaveBeenCalledWith('🧪')
  })

  it('T11 reads the names again when typing after a failure', async () => {
    const load = vi.fn<() => Promise<EmojiName[]>>().mockRejectedValueOnce(new Error('offline')).mockResolvedValue(NAMES)
    open('', load)
    await flush()
    type('beer')
    await flush()
    expect(load).toHaveBeenCalledTimes(2)
    expect(screen.getByRole('button', { name: '🍺' })).toBeTruthy()
  })

  it('T12 gives each emoji its name as a hint', async () => {
    open()
    await flush()
    expect(screen.getByRole('button', { name: '🚀' }).getAttribute('title')).toBe(detectLang() === 'ja' ? 'ロケット' : 'rocket')
  })

  it('T13 reads the names once when it opens', () => {
    const load = vi.fn(() => Promise.resolve(NAMES))
    expect(load).not.toHaveBeenCalled()
    open('', load)
    expect(load).toHaveBeenCalledTimes(1)
  })

  it('T15 marks the icon set now and removes it', async () => {
    const onPick = open('🚀')
    await flush()
    expect(screen.getByRole('button', { name: '🚀' }).getAttribute('aria-pressed')).toBe('true')
    fireEvent.click(screen.getByRole('button', { name: t('iconRemove') }))
    expect(onPick).toHaveBeenCalledWith('')
  })
})
