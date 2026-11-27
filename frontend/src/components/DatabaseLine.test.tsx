import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api } from '../api/client'
import type { PaperView } from '../api/types'
import { DatabaseLine } from './DatabaseLine'

afterEach(() => vi.restoreAllMocks())

function stub() {
  vi.spyOn(api, 'getPaper').mockImplementation(async (_ws, id) => {
    if (id === 'db1') return { paper: { id, kind: 'database', title: 'Product roadmap' } } as unknown as PaperView
    if (id === 'db2') return { paper: { id, kind: 'database', title: 'Incidents', icon: '🚨' } } as unknown as PaperView
    throw new ApiError(404, 'not_found', 'not found')
  })
}

describe('DatabaseLine (T18, T19)', () => {
  it('names each database with a link and says whether the document reads or writes it, in one wording', async () => {
    stub()
    const { container } = render(<MemoryRouter><DatabaseLine ws="team" links={[{ id: 'db1', write: true }, { id: 'db2' }]} of="page" connected /></MemoryRouter>)
    await waitFor(() => expect(screen.getByText(/Product roadmap/)).toBeTruthy())
    const line = container.querySelector('.dbline')
    expect(line?.textContent).toMatch(/connected to these databases|データベースと接続しています/)
    const chips = [...container.querySelectorAll('a.dbchip')]
    expect(chips.map((a) => a.getAttribute('href'))).toEqual(['/ws/team/p/db1', '/ws/team/p/db2'])
    expect(chips[0]?.textContent).toMatch(/Read and write|読み書き/)
    expect(chips[1]?.textContent).toMatch(/🚨 Incidents/)
    expect(chips[1]?.textContent).toMatch(/Read only|読み取り/)
  })

  it('marks a database it cannot find, and words a block as a block', async () => {
    stub()
    const { container } = render(<MemoryRouter><DatabaseLine ws="team" links={[{ id: 'gone', write: true }]} of="block" connected /></MemoryRouter>)
    await waitFor(() => expect(container.querySelector('.dbchip.gone')).not.toBeNull())
    expect(container.querySelector('a.dbchip')).toBeNull()
    expect(container.querySelector('.dbline')?.textContent).toMatch(/This block|このブロック/)
  })

  it('says the document is not connected while an earlier version is shown', () => {
    stub()
    const { container } = render(<MemoryRouter><DatabaseLine ws="team" links={[{ id: 'db1', write: true }]} of="page" connected={false} version /></MemoryRouter>)
    expect(container.querySelector('.dbline.ver')?.textContent).toMatch(/earlier version, so this page|過去の版を表示しているため、データベース/)
    expect(container.querySelector('.dbchip')).toBeNull()
  })

  it('words a block, and a paper that cannot be changed now, as not connected', () => {
    stub()
    const block = render(<MemoryRouter><DatabaseLine ws="team" links={[{ id: 'db1' }]} of="block" connected={false} version /></MemoryRouter>)
    expect(block.container.querySelector('.dbline.ver')?.textContent).toMatch(/this block|このブロック/)
    block.unmount()
    const trashed = render(<MemoryRouter><DatabaseLine ws="team" links={[{ id: 'db1' }]} of="page" connected={false} /></MemoryRouter>)
    expect(trashed.container.querySelector('.dbline.ver')?.textContent).toMatch(/cannot be changed now|今は変更できない/)
  })
})
