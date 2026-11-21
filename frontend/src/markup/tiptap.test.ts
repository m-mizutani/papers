import cases from '../../../testdata/markup/cases.json'
import { fromDoc, toDoc } from './tiptap'

describe('editor document conversion', () => {
  it('keeps the formatting of every canonical case', () => {
    // The nesting order may change (bold inside or outside a colour), the
    // formatting of each character may not.
    for (const c of cases as { in: string; normalized?: string; error?: boolean }[]) {
      if (c.error || c.normalized === undefined) continue
      const doc = toDoc(c.normalized)
      const again = toDoc(fromDoc(doc))
      const flat = (d: typeof doc) => (d.content[0]?.content ?? []).flatMap((n) => n.type === 'text' ? [...n.text].map((ch) => ch + JSON.stringify([...(n.marks ?? [])].sort((a, b) => a.type.localeCompare(b.type)))) : ['\n'])
      expect(flat(again)).toEqual(flat(doc))
    }
  })

  it('writes marks in one canonical order', () => {
    const doc = toDoc('')
    doc.content[0] = { type: 'paragraph', content: [
      { type: 'text', text: 'a', marks: [{ type: 'italic' }, { type: 'bold' }, { type: 'color', attrs: { color: 'red' } }] },
      { type: 'text', text: 'b', marks: [{ type: 'color', attrs: { color: 'red' } }, { type: 'bold' }, { type: 'italic' }] },
      { type: 'hardBreak' },
      { type: 'text', text: 'x_y' },
    ] }
    expect(fromDoc(doc)).toBe('[***ab***]{.red}\nx_y')
  })

  it('shows invalid markup as text', () => {
    expect(toDoc('[a]{.neon}').content[0]?.content?.[0]).toEqual({ type: 'text', text: '[a]{.neon}' })
  })
})
