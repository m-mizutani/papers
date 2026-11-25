import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render } from '@testing-library/react'
import { Popover } from './Popover'

function setup(withIgnore: boolean) {
  const onClose = vi.fn()
  const { getByTestId } = render(
    <div>
      <div data-testid="editor"><span data-testid="word">word</span></div>
      <div data-testid="elsewhere" />
    </div>,
  )
  const editor = getByTestId('editor')
  render(<Popover anchor={new DOMRect(0, 0, 10, 10)} onClose={onClose} ignoreWithin={withIgnore ? editor : undefined}><button type="button">B</button></Popover>)
  return { onClose, getByTestId }
}

describe('Popover', () => {
  it('closes on a press outside it', () => {
    const { onClose, getByTestId } = setup(false)
    fireEvent.mouseDown(getByTestId('word'))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('stays open on a press inside ignoreWithin, and closes on one elsewhere', () => {
    const { onClose, getByTestId } = setup(true)
    fireEvent.mouseDown(getByTestId('word'))
    fireEvent.mouseDown(getByTestId('editor'))
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.mouseDown(getByTestId('elsewhere'))
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})
