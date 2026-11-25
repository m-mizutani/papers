import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { describe, expect, it } from 'vitest'
import type { Actor } from '../api/types'
import { renderMarkup } from '../utils/inline'
import { mentionQuery } from './RichEditor'
import { ActorMention, papersByPath, personName } from './UserMention'

const ID = '1csQdaLtbzOEz6NzfMfH6Z'

function inWorkspace(node: React.ReactNode) {
  return render(
    <MemoryRouter initialEntries={['/ws/product/p/x']}>
      <Routes><Route path="/ws/:ws/p/:paper" element={<div>{node}</div>} /></Routes>
    </MemoryRouter>,
  )
}

describe('ActorMention (T8)', () => {
  const human: Actor = { kind: 'human', user_id: ID, name: 'Masayoshi Mizutani' }
  it('shows a person as a mention', () => {
    const { container } = inWorkspace(<ActorMention actor={human} />)
    expect(container.querySelector('.mention')?.textContent).toBe('@Masayoshi Mizutani')
  })
  it('shows an AI by name and the person it acted for as a mention', () => {
    const { container } = inWorkspace(<ActorMention actor={{ ...human, kind: 'agent', agent_name: 'Claude' }} />)
    expect(container.textContent).toMatch(/^Claude \(.*@Masayoshi Mizutani.*\)$/)
    expect(container.querySelectorAll('.mention')).toHaveLength(1)
  })
  it('shows a personal token as via API, and papers itself as text', () => {
    const { container } = inWorkspace(<><ActorMention actor={{ ...human, token_id: 't' }} /><ActorMention actor={{ kind: 'system', user_id: '', name: '' }} /></>)
    expect(container.querySelectorAll('.mention')).toHaveLength(1)
    expect(container.textContent).toContain('papers')
  })
})

describe('mentions in text (R1, R4)', () => {
  it('renders a mention and opens its card with the papers link', () => {
    const { container } = inWorkspace(<p>{renderMarkup(`Ask [@Masayoshi Mizutani]{user=${ID}} now`)}</p>)
    const m = container.querySelector('.mention') as HTMLElement
    expect(m.textContent).toBe('@Masayoshi Mizutani')
    fireEvent.click(m)
    const link = screen.getByRole('link')
    expect(link.getAttribute('href')).toBe(papersByPath('product', ID))
  })
})

describe('mentionQuery (T6)', () => {
  it('opens at the start, after a space or a punctuation mark', () => {
    expect(mentionQuery('@')).toBe('')
    expect(mentionQuery('Ask @mi')).toBe('mi')
    expect(mentionQuery('（@Masa Mi')).toBe('Masa Mi')
  })
  it('does not open inside an email address or after two spaces', () => {
    expect(mentionQuery('a@b')).toBeNull()
    expect(mentionQuery('Ask @mi  ')).toBeNull()
    expect(mentionQuery('Ask @ mi')).toBeNull()
  })
})

describe('personName', () => {
  it('keeps a name on one line, trimmed, within 100 bytes', () => {
    expect(personName('  Masa\n Mizutani ')).toBe('Masa Mizutani')
    const long = personName('水'.repeat(40))
    expect(new TextEncoder().encode(long).length).toBeLessThanOrEqual(100)
    expect(long).toBe('水'.repeat(33))
  })
})

describe('the person card', () => {
  it('keeps its clicks from what holds the mention', () => {
    let rowClicks = 0
    inWorkspace(<div onClick={() => { rowClicks++ }}><ActorMention actor={{ kind: 'human', user_id: ID, name: 'Masa' }} /></div>)
    fireEvent.click(screen.getByRole('button', { name: '@Masa' }))
    fireEvent.click(screen.getByText('Masa', { selector: '.pc-name' }))
    expect(rowClicks).toBe(0)
  })
})
