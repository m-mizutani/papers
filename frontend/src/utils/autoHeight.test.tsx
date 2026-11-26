import { afterEach, describe, expect, it, vi } from 'vitest'
import { render } from '@testing-library/react'
import { useRef } from 'react'
import { useAutoHeight } from './autoHeight'
import { NO_AUTOFILL } from './autofill'

// jsdom lays nothing out: scrollHeight and the width are stubbed, and the
// ResizeObserver is a fake whose callback the test calls.
let scrollHeight = 0
let width = 100
const observers: Array<() => void> = []
class FakeResizeObserver {
  constructor(private cb: () => void) {}
  observe() { observers.push(this.cb) }
  disconnect() {}
  unobserve() {}
}

function Field({ value }: { value: string }) {
  const ref = useRef<HTMLTextAreaElement>(null)
  useAutoHeight(ref, value)
  return <textarea {...NO_AUTOFILL} ref={ref} rows={1} value={value} readOnly aria-label="field" />
}

function setup() {
  vi.stubGlobal('ResizeObserver', FakeResizeObserver)
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(() => scrollHeight)
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(() => new DOMRect(0, 0, width, 10))
}

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  observers.length = 0
  scrollHeight = 0
  width = 100
})

describe('useAutoHeight', () => {
  it('T1 sets the height of the first value', () => {
    setup()
    scrollHeight = 96
    const { getByLabelText } = render(<Field value="a long title" />)
    expect((getByLabelText('field') as HTMLTextAreaElement).style.height).toBe('96px')
  })

  it('T2 sets the height again when the value changes', () => {
    setup()
    scrollHeight = 96
    const { getByLabelText, rerender } = render(<Field value="a long title" />)
    scrollHeight = 48
    rerender(<Field value="short" />)
    expect((getByLabelText('field') as HTMLTextAreaElement).style.height).toBe('48px')
  })

  it('T3 sets the height again when the width changes, and not when only the height does', () => {
    setup()
    scrollHeight = 48
    const { getByLabelText } = render(<Field value="title" />)
    const el = getByLabelText('field') as HTMLTextAreaElement
    expect(observers).toHaveLength(1)
    scrollHeight = 144
    observers[0]?.()
    expect(el.style.height).toBe('48px')
    width = 50
    observers[0]?.()
    expect(el.style.height).toBe('144px')
  })
})
