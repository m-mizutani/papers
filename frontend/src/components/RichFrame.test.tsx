import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { RichFrame, buildSrcDoc } from './RichFrame'

describe('RichFrame', () => {
  it('sandboxes content without same-origin access', () => {
    const { container } = render(<RichFrame kind="html" content="<b>x</b>" title="t" />)
    const frame = container.querySelector('iframe')
    expect(frame?.getAttribute('sandbox')).toBe('allow-scripts')
    expect(frame?.getAttribute('srcdoc')).toContain('<b>x</b>')
  })

  it('injects the size reporter into full documents', () => {
    const doc = buildSrcDoc('html', '<html><body><p>hi</p></body></html>')
    expect(doc).toMatch(/papersFrameHeight[\s\S]*<\/body>/)
    expect(buildSrcDoc('svg', '<svg/>')).toContain('<svg/>')
  })

  it('respects an explicit height', () => {
    const { container } = render(<RichFrame kind="svg" content="<svg/>" height={300} title="t" />)
    expect(container.querySelector('iframe')?.style.height).toBe('300px')
  })
})
