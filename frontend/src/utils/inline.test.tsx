import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { renderInline, safeHref } from './inline'

describe('renderInline', () => {
  it('renders emphasis, code and links', () => {
    const { container } = render(<p>{renderInline('a **b** *c* `d` [e](https://x.example) ~~f~~')}</p>)
    expect(container.querySelector('strong')?.textContent).toBe('b')
    expect(container.querySelector('em')?.textContent).toBe('c')
    expect(container.querySelector('code')?.textContent).toBe('d')
    expect(container.querySelector('a')?.getAttribute('href')).toBe('https://x.example/')
    expect(container.querySelector('s')?.textContent).toBe('f')
  })

  it('never renders javascript links or raw html', () => {
    const { container } = render(<p>{renderInline('[x](javascript:alert(1)) <img src=x onerror=alert(1)>')}</p>)
    expect(container.querySelector('a')).toBeNull()
    expect(container.querySelector('img')).toBeNull()
    expect(container.textContent).toContain('<img')
  })

  it('validates hrefs', () => {
    expect(safeHref('mailto:a@b.example')).toBe('mailto:a@b.example')
    expect(safeHref('data:text/html,x')).toBeUndefined()
  })
})
