import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { renderMarkup, safeHref } from './inline'

describe('renderMarkup', () => {
  it('renders emphasis, code, links and colours', () => {
    const { container } = render(<p>{renderMarkup('a **b** *c* `d` [e](https://x.example) ~~f~~ [g]{.red .bg-yellow}')}</p>)
    expect(container.querySelector('strong')?.textContent).toBe('b')
    expect(container.querySelector('em')?.textContent).toBe('c')
    expect(container.querySelector('code')?.textContent).toBe('d')
    expect(container.querySelector('a')?.getAttribute('href')).toBe('https://x.example/')
    expect(container.querySelector('s')?.textContent).toBe('f')
    expect(container.querySelector('.mk-c-red.mk-bg-yellow')?.textContent).toBe('g')
  })

  it('never renders javascript links or raw html', () => {
    const { container } = render(<p>{renderMarkup('[x](javascript:alert(1)) <img src=x onerror=alert(1)>')}</p>)
    expect(container.querySelector('a')).toBeNull()
    expect(container.querySelector('img')).toBeNull()
    expect(container.textContent).toContain('<img')
  })

  it('shows invalid markup as text', () => {
    const { container } = render(<p>{renderMarkup('[x]{.neon}')}</p>)
    expect(container.textContent).toBe('[x]{.neon}')
  })

  it('validates hrefs', () => {
    expect(safeHref('mailto:a@b.example')).toBe('mailto:a@b.example')
    expect(safeHref('data:text/html,x')).toBeUndefined()
  })
})
