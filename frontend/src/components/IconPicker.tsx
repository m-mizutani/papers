import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from '../i18n'
import { Popover, type Anchor } from '../ui/Popover'
import { classifyIconInput, emojiHint, loadEmojiNames, searchEmoji, type EmojiName } from '../utils/emojiSearch'
import { isIcon, PICKER_ICONS } from '../utils/icon'
import { isEnter } from '../utils/keyboard'

/**
 * Chooses a paper's icon: one of the offered emoji, one found by typing its
 * name (English or Japanese), or any emoji typed in the field. Remove shows
 * only while an icon is set. onPick gets the icon, or '' to remove it.
 */
export function IconPicker({ anchor, value, onPick, onClose, loadNames = loadEmojiNames }: {
  anchor: Anchor
  /** The icon set now; '' when none is. */
  value: string
  onPick: (icon: string) => void
  onClose: () => void
  /** Reads the emoji names; replaced in tests. */
  loadNames?: () => Promise<EmojiName[]>
}) {
  const { t, lang } = useTranslation()
  const [typed, setTyped] = useState('')
  const [names, setNames] = useState<EmojiName[] | null>(null)
  const [failed, setFailed] = useState(false)
  // Bumped to read the names again after a failure.
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let live = true
    loadNames().then((n) => { if (live) setNames(n) }, () => { if (live) setFailed(true) })
    return () => { live = false }
  }, [loadNames, attempt])

  const kind = classifyIconInput(typed)
  const results = useMemo(() => (kind === 'name' && names ? searchEmoji(names, typed) : []), [kind, names, typed])
  const byEmoji = useMemo(() => new Map((names ?? []).map((x) => [x.emoji, x])), [names])
  const hint = (e: string) => { const x = byEmoji.get(e); return x ? emojiHint(x, lang) : undefined }

  // A name sets its first result; a single character no name matches (★)
  // is set as typed, as any one emoji is.
  const literal = kind === 'icon' || (kind === 'name' && (!!names || failed) && results.length === 0 && isIcon(typed))
  const canSet = literal || (kind === 'name' && results.length > 0)
  const submit = () => {
    if (kind === 'name' && results[0]) onPick(results[0].emoji)
    else if (literal) onPick(typed.trim())
  }

  const offered = (
    <div className="ip-grid" role="group" aria-label={t('iconLabel')}>
      {PICKER_ICONS.map((x) => (
        <button key={x} type="button" className={x === value ? 'on' : ''} aria-pressed={x === value} title={hint(x)} onClick={() => onPick(x)}>{x}</button>
      ))}
    </div>
  )
  let body = offered
  if (kind === 'name') {
    if (failed) body = <>{offered}<div className="ip-err" role="alert">{t('iconNamesFailed')}</div></>
    else if (!names) body = <div className="ip-note" role="status">{t('iconLoading')}</div>
    else if (results.length === 0) body = <div className="ip-note" role="status">{t('iconNoMatch')}</div>
    else body = (
      <div className="ip-grid scroll" role="group" aria-label={t('iconLabel')}>
        {results.map((x, i) => (
          <button key={x.emoji} type="button" className={`${x.emoji === value ? 'on' : ''}${i === 0 ? ' first' : ''}`} aria-pressed={x.emoji === value} title={emojiHint(x, lang)} onClick={() => onPick(x.emoji)}>{x.emoji}</button>
        ))}
      </div>
    )
  }

  return (
    <Popover anchor={anchor} onClose={onClose} className="iconpick" label={t('iconLabel')}>
      {body}
      <div className="ip-row">
        <input className={`ip-in${kind === 'invalid' ? ' bad' : ''}`} value={typed} placeholder={t('iconInput')} aria-label={t('iconInput')} aria-invalid={kind === 'invalid'}
          onChange={(e) => {
            setTyped(e.target.value)
            if (failed) { setFailed(false); setAttempt((n) => n + 1) }
          }}
          onKeyDown={(e) => { if (isEnter(e)) { e.preventDefault(); submit() } }} />
        <button type="button" className="btn sm" disabled={!canSet} onClick={submit}>{t('iconSet')}</button>
        {value && <button type="button" className="btn sm" onClick={() => onPick('')}>{t('iconRemove')}</button>}
      </div>
      {kind === 'invalid' && <div className="ip-err" role="alert">{t('iconOne')}</div>}
    </Popover>
  )
}
