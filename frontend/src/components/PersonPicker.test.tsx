import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { t } from '../i18n'
import { PersonPicker, type Person } from './PersonPicker'

const ME: Person = { id: 'me1', name: 'Masayoshi Mizutani' }

async function open(search: (q: string) => Promise<Person[]>, value = 'me') {
  const onPick = vi.fn()
  render(<PersonPicker anchor={new DOMRect(0, 0, 10, 10)} value={value} me={ME} search={search} onPick={onPick} onClose={() => {}} />)
  await act(async () => { await new Promise((r) => setTimeout(r, 0)) })
  return onPick
}

describe('PersonPicker', () => {
  it('T8 puts me and everyone first, then the people found without me', async () => {
    await open(async () => [ME, { id: 'u2', name: 'Aiko Tanaka' }])
    const names = screen.getAllByRole('option').map((o) => o.querySelector('.mt')?.textContent)
    expect(names).toEqual([`@${ME.name}`, t('everyone'), '@Aiko Tanaka'])
    expect(screen.getAllByRole('option')[0]?.getAttribute('aria-selected')).toBe('true')
  })

  it('T9 when nobody matches, me and everyone stay and the note shows', async () => {
    vi.useFakeTimers()
    const search = vi.fn(async (q: string) => (q ? [] : [{ id: 'u2', name: 'Aiko Tanaka' }]))
    render(<PersonPicker anchor={new DOMRect(0, 0, 10, 10)} value="me" me={ME} search={search} onPick={() => {}} onClose={() => {}} />)
    await act(async () => { await vi.runAllTimersAsync() })
    fireEvent.change(screen.getByLabelText(t('searchByName')), { target: { value: 'zzz' } })
    await act(async () => { await vi.runAllTimersAsync() })
    vi.useRealTimers()
    expect(search).toHaveBeenLastCalledWith('zzz')
    expect(screen.getAllByRole('option')).toHaveLength(2)
    expect(screen.getByText(t('noOneMatches'))).toBeTruthy()
  })

  it('picks with the keyboard and with a click', async () => {
    const onPick = await open(async () => [{ id: 'u2', name: 'Aiko Tanaka' }])
    const input = screen.getByLabelText(t('searchByName'))
    fireEvent.keyDown(input, { key: 'ArrowDown' })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onPick).toHaveBeenLastCalledWith('all', undefined)
    fireEvent.click(screen.getByRole('option', { name: /Aiko/ }))
    expect(onPick).toHaveBeenLastCalledWith('u2', { id: 'u2', name: 'Aiko Tanaka' })
  })
})
