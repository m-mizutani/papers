import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { Actor, Block, Paper, PaperRef } from '../api/types'
import { initState } from '../api/paperState'
import { BlockView, type BlockContext } from './BlockView'

const actor: Actor = { kind: 'human', user_id: 'u1', name: 'Alice' }
const paper: Paper = { id: 'p0', workspace_id: 'team', title: 'Runbooks', visibility: 'public', version_number: 0, created_by: actor, created_at: '', updated_by: actor, updated_at: '' }

function block(id: string, childId: string): Block {
  return { id, workspace_id: 'team', paper_id: 'p0', order: id, type: 'paper', content: '', props: { paper_id: childId }, rev: 1, created_by: actor, created_at: '', updated_by: actor, updated_at: '' }
}

function ctxWith(children: Record<string, PaperRef>, blocks: Block[]): BlockContext {
  return {
    ws: 'team', state: initState(paper, blocks, children), canEdit: false, editing: null, conflict: null, recent: new Set(), numbers: new Map(),
    selected: new Set<string>(), menuFor: null, slashOpen: false, external: true, actions: {} as BlockContext['actions'],
  }
}

describe('child papers', () => {
  it('marks an HTML paper among the children', () => {
    // T36
    const ref = (id: string, kind: PaperRef['kind'], title: string): PaperRef => ({ id, workspace_id: 'team', kind, title, visibility: 'public', status: 'canonical', version_number: 2, created_by: actor })
    const blocks = [block('a', 'c1'), block('b', 'c2')]
    const ctx = ctxWith({ c1: ref('c1', 'blocks', 'Failover'), c2: ref('c2', 'html', 'Weekly') }, blocks)
    const { container } = render(<MemoryRouter>{blocks.map((b) => <BlockView key={b.id} block={b} ctx={ctx} />)}</MemoryRouter>)
    const cards = container.querySelectorAll('.child')
    expect(cards[0]?.querySelector('.pi')?.textContent).toBe('📄')
    expect(cards[0]?.querySelector('.cs')?.textContent).toBe('v2')
    expect(cards[1]?.querySelector('.pi')?.textContent).toBe('🧩')
    expect(cards[1]?.querySelector('.cs')?.textContent).toBe('HTML · v2')
  })

  it('T14 shows the icon set for a child, else the default for its kind', () => {
    const ref = (id: string, kind: PaperRef['kind'], icon?: string): PaperRef => ({ id, workspace_id: 'team', kind, title: id, icon, visibility: 'public', status: 'draft', version_number: 0, created_by: actor })
    const blocks = [block('a', 'c1'), block('b', 'c2'), block('c', 'c3')]
    const ctx = ctxWith({ c1: ref('c1', 'blocks', '🚀'), c2: ref('c2', 'database'), c3: ref('c3', 'blocks') }, blocks)
    const { container } = render(<MemoryRouter>{blocks.map((b) => <BlockView key={b.id} block={b} ctx={ctx} />)}</MemoryRouter>)
    const icons = [...container.querySelectorAll('.child .pi')].map((x) => x.textContent)
    expect(icons).toEqual(['🚀', '🗃️', '📄'])
  })
})
