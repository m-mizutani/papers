import { Mark, mergeAttributes, type Editor as TiptapEditor } from '@tiptap/core'
import Bold from '@tiptap/extension-bold'
import Code from '@tiptap/extension-code'
import Document from '@tiptap/extension-document'
import HardBreak from '@tiptap/extension-hard-break'
import Italic from '@tiptap/extension-italic'
import Link from '@tiptap/extension-link'
import Paragraph from '@tiptap/extension-paragraph'
import Strike from '@tiptap/extension-strike'
import Text from '@tiptap/extension-text'
import { Placeholder } from '@tiptap/extensions'
import { EditorContent, useEditor } from '@tiptap/react'
import { useEffect, useRef } from 'react'
import { useTranslation } from '../i18n'
import { PALETTE } from '../markup/markup'
import { isNativeEnter } from '../utils/keyboard'
import { fromDoc, toDoc, type PMDoc } from '../markup/tiptap'

// The rich text editor of one block (D11): formatting inside the block is
// the editor's; splitting, joining and ordering blocks stay papers ops.

const colorMark = (name: 'color' | 'bg', prefix: string) =>
  Mark.create({
    name,
    addAttributes() {
      return { color: { default: null, parseHTML: (el) => [...el.classList].find((c) => c.startsWith(prefix))?.slice(prefix.length) ?? null } }
    },
    parseHTML() {
      return [{ tag: `span[class*="${prefix}"]` }]
    },
    renderHTML({ HTMLAttributes, mark }) {
      return ['span', mergeAttributes(HTMLAttributes, { class: `${prefix}${String(mark.attrs.color)}` }), 0]
    },
  })

const Color = colorMark('color', 'mk-c-')
const Background = colorMark('bg', 'mk-bg-')
const SingleDoc = Document.extend({ content: 'paragraph' })

export interface RichKeys {
  /** Enter: split the block at the caret into before / after. */
  onSplit?: (before: string, after: string) => void
  /** Backspace at the very start: join with the block above. */
  onJoin?: (text: string) => void
  onEscape: () => void
  /** Enter inserts a line break; Mod+Enter finishes. */
  multiline?: boolean
}

function docJSON(editor: TiptapEditor): PMDoc {
  return editor.getJSON() as PMDoc
}

/** Markup of a part of the document (from / to are positions). */
function sliceMarkup(editor: TiptapEditor, from: number, to: number): string {
  const slice = editor.state.doc.cut(from, to)
  return fromDoc(slice.toJSON() as PMDoc)
}

export function RichEditor({ value, onChange, onBlur, keys, placeholder, onPasteText, onPasteImage }: {
  value: string
  /** changed is false while the document equals the initial one. */
  onChange: (markup: string, changed: boolean) => void
  onBlur: () => void
  keys: RichKeys
  placeholder?: string
  /** Several paragraphs pasted: each becomes a block after this one. */
  onPasteText?: (paragraphs: string[]) => void
  onPasteImage?: (file: File) => void
}) {
  const { t } = useTranslation()
  const initial = useRef(JSON.stringify(toDoc(value)))
  const keysRef = useRef(keys)
  keysRef.current = keys
  const pasteRef = useRef({ onPasteText, onPasteImage })
  pasteRef.current = { onPasteText, onPasteImage }

  const editor = useEditor({
    extensions: [
      SingleDoc, Paragraph, Text, Bold, Italic, Strike, Code, HardBreak, Color, Background,
      Link.configure({ openOnClick: false, autolink: false, linkOnPaste: true, HTMLAttributes: { rel: 'noopener noreferrer', target: null } }),
      Placeholder.configure({ placeholder: placeholder ?? '' }),
    ],
    content: toDoc(value),
    autofocus: 'end',
    onUpdate: ({ editor: ed }) => {
      const doc = docJSON(ed)
      onChange(fromDoc(doc), JSON.stringify(doc) !== initial.current)
    },
    onBlur: () => onBlur(),
    editorProps: {
      attributes: { class: 'rich-editor', 'aria-label': t('blockText') },
      handleKeyDown: (view, event) => {
        if (event.isComposing || event.keyCode === 229) return false
        const k = keysRef.current
        const ed = editorRef.current
        if (!ed) return false
        if (event.key === 'Escape') {
          k.onEscape()
          return true
        }
        if (isNativeEnter(event)) {
          if (k.multiline) {
            if (event.metaKey || event.ctrlKey) {
              k.onEscape()
              return true
            }
            ed.commands.setHardBreak()
            return true
          }
          if (event.shiftKey) {
            ed.commands.setHardBreak()
            return true
          }
          if (k.onSplit) {
            const { from, to } = view.state.selection
            const end = view.state.doc.content.size
            k.onSplit(sliceMarkup(ed, 0, from), sliceMarkup(ed, to, end))
            return true
          }
          k.onEscape()
          return true
        }
        if (event.key === 'Backspace' && k.onJoin) {
          const { from, empty } = view.state.selection
          if (empty && from <= 1) {
            k.onJoin(fromDoc(docJSON(ed)))
            return true
          }
        }
        return false
      },
      handlePaste: (_view, event) => {
        const files = [...(event.clipboardData?.files ?? [])].filter((f) => f.type.startsWith('image/'))
        const img = files[0]
        if (img && pasteRef.current.onPasteImage) {
          pasteRef.current.onPasteImage(img)
          return true
        }
        const text = event.clipboardData?.getData('text/plain') ?? ''
        const paragraphs = text.split(/\n\s*\n/).map((p) => p.trim()).filter(Boolean)
        if (paragraphs.length > 1 && pasteRef.current.onPasteText) {
          pasteRef.current.onPasteText(paragraphs)
          return true
        }
        return false
      },
    },
  })
  const editorRef = useRef<TiptapEditor | null>(null)
  editorRef.current = editor

  // A different block value (e.g. after a conflict) resets the editor.
  useEffect(() => {
    if (!editor) return
    if (fromDoc(docJSON(editor)) === value) return
    initial.current = JSON.stringify(toDoc(value))
    editor.commands.setContent(toDoc(value), { emitUpdate: false })
  }, [editor, value])

  const toggle = (name: 'bold' | 'italic' | 'strike' | 'code') => editor?.chain().focus().toggleMark(name).run()
  const paint = (name: 'color' | 'bg', color: string | null) => {
    const chain = editor?.chain().focus()
    if (!chain) return
    if (color) chain.setMark(name, { color }).run()
    else chain.unsetMark(name).run()
  }
  const link = () => {
    if (!editor) return
    const prev = editor.getAttributes('link').href as string | undefined
    const href = window.prompt(t('linkUrl'), prev ?? 'https://')
    if (href === null) return
    if (href === '') editor.chain().focus().unsetLink().run()
    else editor.chain().focus().setLink({ href }).run()
  }
  // Keep the caret in the editor while using the toolbar.
  const keep = (e: { preventDefault: () => void }) => e.preventDefault()

  return (
    <div className="rich">
      <div className="rich-toolbar" role="toolbar" aria-label={t('formatting')} onMouseDown={keep}>
        <button type="button" className="tool" title={t('bold')} aria-label={t('bold')} onClick={() => toggle('bold')}><b>B</b></button>
        <button type="button" className="tool" title={t('italic')} aria-label={t('italic')} onClick={() => toggle('italic')}><i>I</i></button>
        <button type="button" className="tool" title={t('strike')} aria-label={t('strike')} onClick={() => toggle('strike')}><s>S</s></button>
        <button type="button" className="tool" title={t('inlineCode')} aria-label={t('inlineCode')} onClick={() => toggle('code')}>{'</>'}</button>
        <button type="button" className="tool" title={t('link')} aria-label={t('link')} onClick={link}>🔗</button>
        <span className="rich-sep" aria-hidden />
        {PALETTE.map((c) => (
          <button key={c} type="button" className={`swatch mk-c-${c}`} title={t('textColor', { color: c })} aria-label={t('textColor', { color: c })} onClick={() => paint('color', c)}>A</button>
        ))}
        <button type="button" className="swatch" title={t('clearColor')} aria-label={t('clearColor')} onClick={() => paint('color', null)}>A̸</button>
        <span className="rich-sep" aria-hidden />
        {PALETTE.map((c) => (
          <button key={c} type="button" className={`swatch mk-bg-${c}`} title={t('backgroundColor', { color: c })} aria-label={t('backgroundColor', { color: c })} onClick={() => paint('bg', c)} />
        ))}
        <button type="button" className="swatch" title={t('clearBackground')} aria-label={t('clearBackground')} onClick={() => paint('bg', null)}>∅</button>
      </div>
      <EditorContent editor={editor} />
    </div>
  )
}
