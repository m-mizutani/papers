import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { api } from '../api/client'
import type { Me, PaperListItem } from '../api/types'
import { t } from '../i18n'
import { PapersPage } from './PapersPage'

const ME: Me = { user: { id: 'me1', email: 'm@example.com', name: 'Masayoshi Mizutani' }, no_authn: true }
const row = (id: string, title: string): PaperListItem => ({
  id, workspace_id: 'w', title, kind: 'blocks', visibility: 'public', status: 'canonical', version_number: 1,
  created_by: { kind: 'human', user_id: 'me1', name: 'Masayoshi Mizutani' }, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', parent: null,
} as PaperListItem)

async function show(url = '/ws/w/papers') {
  render(<MemoryRouter initialEntries={[url]}><PapersPage ws="w" me={ME} onNew={() => {}} onNewPrivate={() => {}} layoutTop={null} /></MemoryRouter>)
  await act(async () => { await Promise.resolve() })
}

afterEach(() => { vi.restoreAllMocks() })

describe('PapersPage', () => {
  it('T6 a place tab asks for that place and becomes selected', async () => {
    const list = vi.spyOn(api, 'listPapers').mockResolvedValue({ papers: [row('p1', 'Weekly')], next_cursor: '' })
    await show()
    expect(list).toHaveBeenLastCalledWith('w', expect.objectContaining({ place: 'unfiled', creator: 'me' }))
    fireEvent.click(screen.getByRole('tab', { name: t('filed') }))
    await act(async () => { await Promise.resolve() })
    expect(list).toHaveBeenLastCalledWith('w', expect.objectContaining({ place: 'filed' }))
    expect(screen.getByRole('tab', { name: t('filed') }).getAttribute('aria-selected')).toBe('true')
  })

  it('T7 the visibility button is highlighted only away from its default', async () => {
    vi.spyOn(api, 'listPapers').mockResolvedValue({ papers: [], next_cursor: '' })
    await show('/ws/w/papers?visibility=private')
    expect(screen.getByRole('button', { name: t('visibilityIs', { value: t('private') }) }).className).toContain('set')
    expect(screen.getByRole('button', { name: new RegExp(t('meMention')) }).className).not.toContain('set')
  })

  it('T17 a failed read says so and retries', async () => {
    const list = vi.spyOn(api, 'listPapers').mockRejectedValueOnce(new Error('down')).mockResolvedValue({ papers: [row('p1', 'Weekly')], next_cursor: '' })
    await show()
    expect(screen.getByText(t('papersLoadFailed'))).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: t('retry') }))
    await act(async () => { await Promise.resolve() })
    expect(list).toHaveBeenCalledTimes(2)
    expect(screen.getAllByText('Weekly').length).toBeGreaterThan(0)
  })

  it('T14 a row shows its icon or the default, and the icon of its parent; a private row has no lock before the title', async () => {
    const withIcon = { ...row('p1', 'Launch'), icon: '🚀', parent: { id: 'h', title: 'Home', icon: '📚' } } as PaperListItem
    const plain = { ...row('p2', 'Plain'), visibility: 'private' } as PaperListItem
    const item = { ...row('p3', 'Task'), parent: { id: 'd', title: 'Tasks', kind: 'database' } } as PaperListItem
    vi.spyOn(api, 'listPapers').mockResolvedValue({ papers: [withIcon, plain, item], next_cursor: '' })
    await show('/ws/w/papers?place=all')
    const rows = screen.getAllByRole('row').slice(1)
    expect(rows[0]?.querySelector('.ptt .pic')?.textContent).toBe('🚀')
    expect(rows[0]?.querySelector('.pwide .ploc .pic')?.textContent).toBe('📚')
    expect(rows[1]?.querySelector('.ptt .pic')?.textContent).toBe('📄')
    expect(rows[2]?.querySelector('.pwide .ploc .pic')?.textContent).toBe('🗃️')
  })
})
