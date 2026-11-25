import { Mark, Node as TiptapNode, mergeAttributes, type Editor as TiptapEditor } from '@tiptap/core'
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
import { TextSelection } from '@tiptap/pm/state'
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useParams } from 'react-router'
import type { UserSummary } from '../api/types'
import { MentionMenu, personName } from './UserMention'
import { useTranslation } from '../i18n'
import { leaveToBrowser, NO_PRESS, type Press } from './clicks'
import { PALETTE } from '../markup/markup'
import { fromDoc, toDoc, type PMDoc } from '../markup/tiptap'
import { Popover } from '../ui/Popover'
import { isNativeEnter } from '../utils/keyboard'
import { NO_AUTOFILL } from '../utils/autofill'

// The rich text editor of one block (D11): formatting inside the block is
// the editor's; splitting, joining and ordering blocks stay papers ops. The
// formatting toolbar floats over a selection only.

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

// A mention of a user: one atom the caret cannot enter, removed whole.
const Mention = TiptapNode.create({
  name: 'mention',
  group: 'inline',
  inline: true,
  atom: true,
  selectable: false,
  addAttributes() {
    return {
      userId: { default: '', parseHTML: (el) => el.getAttribute('data-user-id') ?? '' },
      name: { default: '', parseHTML: (el) => el.getAttribute('data-name') ?? '' },
    }
  },
  parseHTML() {
    return [{ tag: 'span[data-user-id]' }]
  },
  renderHTML({ node }) {
    return ['span', { class: 'mention', 'data-user-id': String(node.attrs.userId), 'data-name': String(node.attrs.name) }, `@${String(node.attrs.name)}`]
  },
  renderText({ node }) {
    return `@${String(node.attrs.name)}`
  },
})

/** "@" typed at the start, or after a space or a punctuation mark: the text after it, with no double space. */
export function mentionQuery(before: string): string | null {
  const m = /(?:^|[\s\p{P}])@([^@\n]{0,40})$/u.exec(before)
  if (!m) return null
  const q = m[1] ?? ''
  if (q.startsWith(' ') || q.includes('  ')) return null
  return q
}

interface MentionAt {
  query: string
  from: number
  to: number
  rect: DOMRect
}

/** Where the caret goes when the editor opens. */
export type Caret = 'start' | 'end' | { x: number; y: number } | { offset: number }

export interface Slash {
  /** Text typed after "/". */
  query: string
  /** Document positions of "/query", to remove it when a choice is made. */
  from: number
  to: number
  rect: DOMRect
}

export interface RichKeys {
  /** Enter: split the block at the caret into before / after. */
  onSplit?: (before: string, after: string) => void
  /** Backspace at the very start: join with the block above. */
  onJoin?: (text: string) => void
  onEscape: () => void
  /** Up / down past the first / last line: go to the block above / below. */
  onArrowOut?: (dir: 'up' | 'down') => void
  onTab?: (shift: boolean) => void
  /** Enter when the block cannot be split (a table cell: go to the cell below). */
  onEnter?: () => void
  /** Enter inserts a line break; Mod+Enter finishes. */
  multiline?: boolean
  /** While the slash menu is open, these keys go to it. */
  slashKey?: (key: 'ArrowUp' | 'ArrowDown' | 'Enter' | 'Escape') => boolean
}

function docJSON(editor: TiptapEditor): PMDoc {
  return editor.getJSON() as PMDoc
}

/** Markup of a part of the document (from / to are positions). */
function sliceMarkup(editor: TiptapEditor, from: number, to: number): string {
  const slice = editor.state.doc.cut(from, to)
  return fromDoc(slice.toJSON() as PMDoc)
}

function placeCaret(ed: TiptapEditor, caret: Caret) {
  const size = ed.state.doc.content.size
  if (caret === 'start') ed.commands.setTextSelection(1)
  else if (caret === 'end') ed.commands.setTextSelection(Math.max(1, size - 1))
  else if ('offset' in caret) ed.commands.setTextSelection(Math.min(Math.max(1, caret.offset + 1), Math.max(1, size - 1)))
  else {
    const at = ed.view.posAtCoords({ left: caret.x, top: caret.y })
    ed.commands.setTextSelection(at ? at.pos : Math.max(1, size - 1))
  }
  ed.view.focus()
}

function FormatBar({ editor, anchor }: { editor: TiptapEditor; anchor: DOMRect }) {
  const { t } = useTranslation()
  const [sub, setSub] = useState<'color' | 'link' | null>(null)
  const [href, setHref] = useState('')
  const barRef = useRef<HTMLDivElement>(null)
  const keep = (e: { preventDefault: () => void }) => e.preventDefault()
  const toggle = (name: 'bold' | 'italic' | 'strike' | 'code') => editor.chain().focus().toggleMark(name).run()
  const paint = (name: 'color' | 'bg', color: string | null) => {
    const chain = editor.chain().focus()
    if (color) chain.setMark(name, { color }).run()
    else chain.unsetMark(name).run()
    setSub(null)
  }
  const applyLink = () => {
    const u = href.trim()
    if (/^https?:\/\//.test(u)) editor.chain().focus().setLink({ href: u }).run()
    else if (u === '') editor.chain().focus().unsetLink().run()
    setSub(null)
  }
  const on = (n: string) => (editor.isActive(n) ? 'on' : '')
  return (
    // Escape (or a click outside) while text is selected collapses the
    // selection, which hides the toolbar; the next Escape leaves the block.
    // While a colour or link box is open, that box closes first. A press in
    // the editor is left alone: ProseMirror has already set the selection it
    // makes (a double click selects a word in the same press that shows the toolbar).
    <Popover anchor={anchor} placement="above" className="fmtpop" ignoreWithin={editor.view.dom} onClose={() => { if (!sub) editor.commands.setTextSelection(editor.state.selection.to) }} label={t('formatting')}>
      <div ref={barRef} className="fmt" role="toolbar" aria-label={t('formatting')} onMouseDown={keep}>
        <button type="button" className={on('bold')} title={t('bold')} aria-label={t('bold')} onClick={() => toggle('bold')}><b>B</b></button>
        <button type="button" className={on('italic')} title={t('italic')} aria-label={t('italic')} onClick={() => toggle('italic')}><i>I</i></button>
        <button type="button" className={on('strike')} title={t('strike')} aria-label={t('strike')} onClick={() => toggle('strike')}><s>S</s></button>
        <button type="button" className={on('code')} title={t('inlineCode')} aria-label={t('inlineCode')} onClick={() => toggle('code')}><code>{'<>'}</code></button>
        <span className="vs" />
        <button type="button" title={t('link')} aria-label={t('link')} onClick={() => { setHref((editor.getAttributes('link').href as string | undefined) ?? ''); setSub(sub === 'link' ? null : 'link') }}>🔗</button>
        <button type="button" title={t('color')} aria-label={t('color')} onClick={() => setSub(sub === 'color' ? null : 'color')}>
          <span style={{ fontWeight: 700, borderBottom: 'var(--sp-1) solid var(--mk-red)', lineHeight: 1 }}>A</span> ▾
        </button>
      </div>
      {sub === 'color' && barRef.current && (
        <Popover anchor={barRef.current} onClose={() => setSub(null)} label={t('color')}>
          <div onMouseDown={keep}>
            <div className="mh">{t('textColor')}</div>
            <div className="swatches">
              <button type="button" className="sw" title={t('noColor')} aria-label={`${t('textColor')} ${t('noColor')}`} onClick={() => paint('color', null)}>∅</button>
              {PALETTE.map((c) => <button key={c} type="button" className={`sw mk-c-${c}`} title={c} aria-label={`${t('textColor')} ${c}`} onClick={() => paint('color', c)}>A</button>)}
            </div>
            <div className="mh">{t('bgColor')}</div>
            <div className="swatches">
              <button type="button" className="sw" title={t('noColor')} aria-label={`${t('bgColor')} ${t('noColor')}`} onClick={() => paint('bg', null)}>∅</button>
              {PALETTE.map((c) => <button key={c} type="button" className={`sw mk-bg-${c}`} title={c} aria-label={`${t('bgColor')} ${c}`} onClick={() => paint('bg', c)}>A</button>)}
            </div>
          </div>
        </Popover>
      )}
      {sub === 'link' && barRef.current && (
        <Popover anchor={barRef.current} onClose={() => setSub(null)} label={t('link')}>
          <input {...NO_AUTOFILL} className="inp" style={{ minWidth: 'var(--input-min)' }} autoFocus value={href} placeholder={t('linkPlaceholder')} aria-label={t('link')}
            onChange={(e) => setHref(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') setSub(null)
              if (isNativeEnter(e.nativeEvent)) {
                e.preventDefault()
                applyLink()
              }
            }} />
        </Popover>
      )}
    </Popover>
  )
}

export function RichEditor({ value, onChange, onBlur, keys, placeholder, onPasteText, onPasteImage, caret = 'end', onSlash, onEditor, label }: {
  value: string
  /** changed is false while the document equals the initial one. */
  onChange: (markup: string, changed: boolean) => void
  onBlur: () => void
  keys: RichKeys
  placeholder?: string
  /** Several lines pasted: each becomes a block after this one. */
  onPasteText?: (lines: string[]) => void
  onPasteImage?: (file: File) => void
  caret?: Caret
  /** "/" typed at the start or after a space opens the block menu. */
  onSlash?: (s: Slash | null) => void
  onEditor?: (ed: TiptapEditor | null) => void
  label?: string
}) {
  const { t } = useTranslation()
  const initial = useRef(JSON.stringify(toDoc(value)))
  const keysRef = useRef(keys)
  keysRef.current = keys
  const pasteRef = useRef({ onPasteText, onPasteImage, onSlash })
  pasteRef.current = { onPasteText, onPasteImage, onSlash }
  const [selRect, setSelRect] = useState<DOMRect | null>(null)
  const ws = useParams().ws ?? ''
  const [mention, setMention] = useState<MentionAt | null>(null)
  const mentionKey = useRef<((key: string) => boolean) | null>(null)
  const mentionOpen = useRef(false)
  mentionOpen.current = !!mention
  // The "@" whose menu was closed with Escape stays closed while it is typed on.
  const mentionDismissed = useRef<number | null>(null)
  // "@" opens the people to mention, at the caret, after a document or caret change.
  const findMention = (ed: TiptapEditor) => {
    const { from, empty } = ed.state.selection
    const q = empty && ws ? mentionQuery(ed.state.doc.textBetween(Math.max(0, from - 48), from, '\n', '\n')) : null
    const at = q === null ? null : from - q.length - 1
    if (q === null || at === null || at === mentionDismissed.current) {
      if (q === null) mentionDismissed.current = null
      setMention(null)
      return
    }
    const c = ed.view.coordsAtPos(from)
    setMention({ query: q, from: at, to: from, rect: new DOMRect(c.left, c.top, 1, c.bottom - c.top) })
  }
  // The last press this editor received, and whether it was left to the browser.
  const clicks = useRef<Press>(NO_PRESS)

  const unmounted = useRef(false)
  // The cleanup runs before the editor's own unmount, which blurs it.
  useLayoutEffect(() => {
    unmounted.current = false
    return () => { unmounted.current = true }
  }, [])

  const editor = useEditor({
    extensions: [
      SingleDoc, Paragraph, Text, Bold, Italic, Strike, Code, HardBreak, Color, Background, Mention,
      Link.configure({ openOnClick: false, autolink: false, linkOnPaste: true, HTMLAttributes: { rel: 'noopener noreferrer', target: null } }),
      Placeholder.configure({ placeholder: placeholder ?? '' }),
    ],
    content: toDoc(value),
    onUpdate: ({ editor: ed }) => {
      const doc = docJSON(ed)
      onChange(fromDoc(doc), JSON.stringify(doc) !== initial.current)
      findMention(ed)
      // "/" at the start or after a space opens the block menu.
      const report = pasteRef.current.onSlash
      if (!report) return
      const { from, empty } = ed.state.selection
      const before = ed.state.doc.textBetween(Math.max(0, from - 22), from, '\n', '\n')
      const m = empty ? /(?:^|\s)\/([^\s/]{0,20})$/.exec(before) : null
      if (!m) {
        report(null)
        return
      }
      const start = from - (m[1]?.length ?? 0) - 1
      const c = ed.view.coordsAtPos(from)
      report({ query: m[1] ?? '', from: start, to: from, rect: new DOMRect(c.left, c.top, 1, c.bottom - c.top) })
    },
    onSelectionUpdate: ({ editor: ed }) => {
      findMention(ed)
      const { from, to, empty } = ed.state.selection
      if (empty) {
        setSelRect(null)
        return
      }
      const a = ed.view.coordsAtPos(from)
      const b = ed.view.coordsAtPos(to)
      setSelRect(new DOMRect(Math.min(a.left, b.left), Math.min(a.top, b.top), Math.abs(b.right - a.left), Math.max(a.bottom, b.bottom) - Math.min(a.top, b.top)))
    },
    onBlur: ({ event }) => {
      // Clicks in the floating toolbar keep the edit going.
      const to = (event as FocusEvent).relatedTarget as Node | null
      if (to && (to as HTMLElement).closest?.('.fmtpop, .pop')) return
      // The editor going away is not the person leaving the block: when "```"
      // turns the block into code, the code editor goes on with the edit.
      if (unmounted.current) return
      setSelRect(null)
      onBlur()
    },
    editorProps: {
      attributes: { class: 'rich-editor', 'aria-label': label ?? t('blockText') },
      handleKeyDown: (view, event) => {
        if (event.isComposing || event.keyCode === 229) return false
        const k = keysRef.current
        const ed = editorRef.current
        if (!ed) return false
        if (mentionOpen.current && ['ArrowUp', 'ArrowDown', 'Enter', 'Escape'].includes(event.key) && mentionKey.current?.(event.key)) return true
        if (k.slashKey && ['ArrowUp', 'ArrowDown', 'Enter', 'Escape'].includes(event.key) && k.slashKey(event.key as 'ArrowUp')) return true
        if (event.key === 'Escape') {
          k.onEscape()
          return true
        }
        if (event.key === 'Tab' && k.onTab) {
          k.onTab(event.shiftKey)
          return true
        }
        if ((event.key === 'ArrowUp' || event.key === 'ArrowDown') && k.onArrowOut && !event.shiftKey) {
          const dir = event.key === 'ArrowUp' ? 'up' : 'down'
          if (view.endOfTextblock(dir)) {
            k.onArrowOut(dir)
            return true
          }
        }
        if (isNativeEnter(event)) {
          if (k.multiline) {
            if (event.metaKey || event.ctrlKey) {
              k.onEscape()
              return true
            }
            if (event.shiftKey && k.onSplit) {
              const { from, to } = view.state.selection
              k.onSplit(sliceMarkup(ed, 0, from), sliceMarkup(ed, to, view.state.doc.content.size))
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
            k.onSplit(sliceMarkup(ed, 0, from), sliceMarkup(ed, to, view.state.doc.content.size))
            return true
          }
          if (k.onEnter) k.onEnter()
          else k.onEscape()
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
      handleDOMEvents: {
        // A double or triple click whose first press opened this editor (see
        // clicks.ts): the browser selects the word of a double click, and the
        // paragraph of a triple click is selected here, as ProseMirror does,
        // since the browser stops at a line break.
        mousedown: (view, event) => {
          const native = leaveToBrowser(clicks.current, event)
          clicks.current = { detail: event.detail, time: event.timeStamp, native }
          if (native && event.detail >= 3) {
            event.preventDefault()
            view.dispatch(view.state.tr.setSelection(TextSelection.create(view.state.doc, 1, view.state.doc.content.size - 1)))
          }
          return native
        },
      },
      handlePaste: (_view, event) => {
        const files = [...(event.clipboardData?.files ?? [])].filter((f) => f.type.startsWith('image/'))
        const img = files[0]
        if (img && pasteRef.current.onPasteImage) {
          pasteRef.current.onPasteImage(img)
          return true
        }
        const text = event.clipboardData?.getData('text/plain') ?? ''
        // A URL pasted over selected text links the text.
        const ed = editorRef.current
        if (ed && !ed.state.selection.empty && /^https?:\/\/\S+$/.test(text.trim())) {
          ed.chain().focus().setLink({ href: text.trim() }).run()
          return true
        }
        const lines = text.replace(/\r/g, '').split('\n').filter((l) => l.trim() !== '')
        if (lines.length > 1 && pasteRef.current.onPasteText) {
          pasteRef.current.onPasteText(lines)
          return true
        }
        return false
      },
    },
  })
  const editorRef = useRef<TiptapEditor | null>(null)
  editorRef.current = editor

  // Focus in the same task that mounts the editor. Tiptap's autofocus waits
  // a frame, and keys typed in that gap (right after Enter split a block)
  // would be lost.
  const caretRef = useRef(caret)
  useLayoutEffect(() => {
    if (!editor || editor.isDestroyed) return
    placeCaret(editor, caretRef.current)
  }, [editor])

  const onEditorRef = useRef(onEditor)
  onEditorRef.current = onEditor
  useEffect(() => {
    onEditorRef.current?.(editor)
    return () => onEditorRef.current?.(null)
  }, [editor])

  // A different block value (someone else's change, a conflict choice) resets the editor.
  useEffect(() => {
    if (!editor) return
    if (fromDoc(docJSON(editor)) === value) return
    const at = editor.state.selection.from
    initial.current = JSON.stringify(toDoc(value))
    editor.commands.setContent(toDoc(value), { emitUpdate: false })
    editor.commands.setTextSelection(Math.min(at, Math.max(1, editor.state.doc.content.size - 1)))
  }, [editor, value])

  return (
    <>
      <EditorContent editor={editor} />
      {editor && selRect && <FormatBar editor={editor} anchor={selRect} />}
      {editor && mention && (
        <MentionMenu ws={ws} query={mention.query} anchor={mention.rect} keyRef={mentionKey}
          onClose={() => { mentionDismissed.current = mention.from; setMention(null) }}
          onPick={(u: UserSummary) => {
            editor.chain().focus().insertContentAt({ from: mention.from, to: mention.to }, [
              { type: 'mention', attrs: { userId: u.id, name: personName(u.name) } },
              { type: 'text', text: ' ' },
            ]).run()
            setMention(null)
          }} />
      )}
    </>
  )
}
