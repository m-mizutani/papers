import { describe, expect, it } from 'vitest'
import { leaveToBrowser, NO_PRESS } from './clicks'

const press = (detail: number, timeStamp: number, button = 0) => ({ detail, timeStamp, button })

describe('leaveToBrowser', () => {
  it('leaves the second press of a double click that opened the editor to the browser', () => {
    expect(leaveToBrowser(NO_PRESS, press(2, 1000))).toBe(true)
  })

  it('goes on leaving presses to the browser once one was', () => {
    expect(leaveToBrowser({ detail: 2, time: 1000, native: true }, press(3, 1200))).toBe(true)
  })

  it('leaves single presses to ProseMirror', () => {
    expect(leaveToBrowser(NO_PRESS, press(1, 1000))).toBe(false)
    expect(leaveToBrowser({ detail: 2, time: 900, native: true }, press(1, 1000))).toBe(false)
  })

  it('leaves a double click the editor received whole to ProseMirror', () => {
    expect(leaveToBrowser({ detail: 1, time: 1000, native: false }, press(2, 1200))).toBe(false)
    expect(leaveToBrowser({ detail: 2, time: 1200, native: false }, press(3, 1400))).toBe(false)
  })

  it('does not take a press from an earlier sequence for the previous one', () => {
    // the first press of this double click landed outside the editor
    expect(leaveToBrowser({ detail: 1, time: 1000, native: false }, press(2, 1501))).toBe(true)
    expect(leaveToBrowser({ detail: 1, time: 1000, native: false }, press(2, 1500))).toBe(false)
  })

  it('leaves presses with other buttons to ProseMirror', () => {
    expect(leaveToBrowser(NO_PRESS, press(2, 1000, 2))).toBe(false)
  })
})
