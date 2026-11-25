import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { t } from '../i18n'
import { IconPicker } from './IconPicker'

function open(value = '') {
  const onPick = vi.fn()
  render(<IconPicker anchor={new DOMRect(0, 0, 10, 10)} value={value} onPick={onPick} onClose={() => {}} />)
  return onPick
}

describe('IconPicker', () => {
  it('T15 picks an offered emoji', () => {
    const onPick = open()
    fireEvent.click(screen.getByRole('button', { name: '🚀' }))
    expect(onPick).toHaveBeenCalledWith('🚀')
    expect(screen.queryByRole('button', { name: t('iconRemove') })).toBeNull()
  })

  it('T15 sets a typed emoji with Enter, but not while the IME composes', () => {
    const onPick = open()
    const input = screen.getByLabelText(t('iconInput'))
    fireEvent.change(input, { target: { value: '🧪' } })
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true })
    expect(onPick).not.toHaveBeenCalled()
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onPick).toHaveBeenCalledWith('🧪')
  })

  it('T15 refuses two emoji and says so', () => {
    const onPick = open()
    const input = screen.getByLabelText(t('iconInput'))
    fireEvent.change(input, { target: { value: '🚀🔥' } })
    expect(screen.getByRole('alert').textContent).toBe(t('iconOne'))
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onPick).not.toHaveBeenCalled()
    expect((screen.getByRole('button', { name: t('iconSet') }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('T15 marks the icon set now and removes it', () => {
    const onPick = open('🚀')
    expect(screen.getByRole('button', { name: '🚀' }).getAttribute('aria-pressed')).toBe('true')
    fireEvent.click(screen.getByRole('button', { name: t('iconRemove') }))
    expect(onPick).toHaveBeenCalledWith('')
  })
})
