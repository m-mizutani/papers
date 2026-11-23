import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { api } from '../api/client'
import { HtmlPage, buildPageDoc } from './HtmlPage'
import { SANDBOX_CSP } from './RichFrame'

afterEach(() => vi.restoreAllMocks())

describe('HtmlPage', () => {
  it('fetches the document and shows it in a sandboxed frame', async () => {
    // T25, T48
    const fetchHTML = vi.spyOn(api, 'fetchHTML').mockResolvedValue('<h1>Weekly</h1><script>run()</script>')
    const { container } = render(<HtmlPage ws="team" paperId="p1" attachmentId="a2" title="Weekly" external />)
    await waitFor(() => expect(container.querySelector('iframe')).not.toBeNull())
    expect(fetchHTML).toHaveBeenCalledWith('team', 'p1', 'a2')
    const frame = container.querySelector('iframe')
    expect(frame?.getAttribute('sandbox')).toBe('allow-scripts allow-popups allow-popups-to-escape-sandbox')
    expect(frame?.getAttribute('sandbox')).not.toContain('allow-same-origin')
    expect(frame?.getAttribute('referrerpolicy')).toBe('no-referrer')
    expect(frame?.getAttribute('srcdoc')).toBe('<h1>Weekly</h1><script>run()</script>')
  })

  it('shows a skeleton while loading', () => {
    // T26
    vi.spyOn(api, 'fetchHTML').mockReturnValue(new Promise(() => undefined))
    const { container } = render(<HtmlPage ws="team" paperId="p1" attachmentId="a2" title="Weekly" external />)
    expect(container.querySelector('.htmlpage[aria-busy="true"] .skel')).not.toBeNull()
    expect(container.querySelector('iframe')).toBeNull()
  })

  it('treats a page without a document as a failed load', () => {
    // T26
    const fetchHTML = vi.spyOn(api, 'fetchHTML')
    render(<HtmlPage ws="team" paperId="p1" title="Weekly" external />)
    expect(screen.getByRole('alert').textContent).toMatch(/load|読み込め/)
    expect(fetchHTML).not.toHaveBeenCalled()
  })

  it('reports a failed load and retries', async () => {
    // T27
    const fetchHTML = vi.spyOn(api, 'fetchHTML').mockRejectedValueOnce(new Error('500')).mockResolvedValueOnce('<p>back</p>')
    const { container } = render(<HtmlPage ws="team" paperId="p1" attachmentId="a2" title="Weekly" external />)
    const alert = await screen.findByRole('alert')
    fireEvent.click(alert.querySelector('button')!)
    await waitFor(() => expect(container.querySelector('iframe')?.getAttribute('srcdoc')).toBe('<p>back</p>'))
    expect(fetchHTML).toHaveBeenCalledTimes(2)
  })

  it('fetches again when the document changes (live update, a version)', async () => {
    // T29
    const fetchHTML = vi.spyOn(api, 'fetchHTML').mockImplementation(async (_ws, _p, att) => `<p>${att}</p>`)
    const { container, rerender } = render(<HtmlPage ws="team" paperId="p1" attachmentId="a1" title="Weekly" external />)
    await waitFor(() => expect(container.querySelector('iframe')?.getAttribute('srcdoc')).toBe('<p>a1</p>'))
    rerender(<HtmlPage ws="team" paperId="p1" attachmentId="a2" title="Weekly" external />)
    await waitFor(() => expect(container.querySelector('iframe')?.getAttribute('srcdoc')).toBe('<p>a2</p>'))
    expect(fetchHTML).toHaveBeenLastCalledWith('team', 'p1', 'a2')
  })

  it('keeps the document to what it embeds when the workspace says so', async () => {
    // T55
    expect(buildPageDoc('<p>x</p>', true)).toBe('<p>x</p>')
    expect(buildPageDoc('<!doctype html><p>x</p>', false)).toContain(SANDBOX_CSP)
    vi.spyOn(api, 'fetchHTML').mockResolvedValue('<p>x</p>')
    const { container } = render(<HtmlPage ws="team" paperId="p1" attachmentId="a2" title="Weekly" external={false} />)
    await waitFor(() => expect(container.querySelector('iframe')?.getAttribute('srcdoc')).toContain(SANDBOX_CSP))
  })
})
