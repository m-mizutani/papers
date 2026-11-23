import { describe, expect, it, vi } from 'vitest'
import { render } from '@testing-library/react'
import { RichFrame, acceptField, buildSrcDoc, frameTitle } from './RichFrame'

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

describe('fields', () => {
  it('accepts only a known name with a short single-line value', () => {
    const fields = { rto: '15m' }
    expect(acceptField({ papersField: 1, k: 'rto', v: '10m' }, fields)).toEqual({ name: 'rto', value: '10m' })
    // T14: a name the block does not have is dropped
    expect(acceptField({ papersField: 1, k: 'other', v: '1' }, fields)).toBeNull()
    expect(acceptField({ papersField: 1, k: 'rto', v: 'a\nb' }, fields)).toBeNull()
    expect(acceptField({ papersField: 1, k: 'rto', v: 'x'.repeat(201) }, fields)).toBeNull()
    expect(acceptField('rto', fields)).toBeNull()
  })

  it('fills values and makes them editable only when allowed', () => {
    expect(buildSrcDoc('html', '<div data-field="rto"></div>', { rto: '15m' }, true)).toContain('plaintext-only')
    expect(buildSrcDoc('html', '<div data-field="rto"></div>', { rto: '15m' }, false)).not.toContain('plaintext-only')
    expect(frameTitle('<title> Failover </title><div/>')).toBe('Failover')
  })

  it('ignores values from anywhere but its own frame (T53)', () => {
    const onField = vi.fn()
    const { container } = render(<RichFrame kind="html" content="<div data-field='rto'></div>" fields={{ rto: '15m' }} onField={onField} title="t" />)
    const frame = container.querySelector('iframe')
    window.dispatchEvent(new MessageEvent('message', { data: { papersField: 1, k: 'rto', v: '9m' }, source: window }))
    expect(onField).not.toHaveBeenCalled()
    window.dispatchEvent(new MessageEvent('message', { data: { papersField: 1, k: 'rto', v: '9m' }, source: frame?.contentWindow ?? null }))
    expect(onField).toHaveBeenCalledWith('rto', '9m')
  })
})
