// Which presses an editor leaves to the browser. ProseMirror counts clicks
// itself, from the presses it has received. The first press of a double or
// triple click on a block that is not being edited opens the editor, so the
// editor never receives it and ProseMirror would take the second press for a
// first one (placing the caret instead of selecting the word).

/** The last press an editor received. */
export interface Press {
  /** MouseEvent.detail: 1 for a single press, 2 for the second of a double click. */
  detail: number
  /** Event.timeStamp of the press. */
  time: number
  /** Whether the press was left to the browser. */
  native: boolean
}

export const NO_PRESS: Press = { detail: 0, time: 0, native: false }

/** Presses further apart than this are not one click sequence; ProseMirror uses the same limit. */
const SEQUENCE_MS = 500

/**
 * Whether a press is left to the browser: a second or later press with the
 * main button whose previous press in the sequence this editor did not
 * receive, or whose previous press was already left to the browser.
 */
export function leaveToBrowser(last: Press, e: { button: number; detail: number; timeStamp: number }): boolean {
  if (e.button !== 0 || e.detail < 2) return false
  const received = last.detail === e.detail - 1 && e.timeStamp - last.time <= SEQUENCE_MS
  return !received || last.native
}
