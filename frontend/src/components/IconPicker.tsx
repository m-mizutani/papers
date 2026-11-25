import { useState } from 'react'
import { useTranslation } from '../i18n'
import { Popover, type Anchor } from '../ui/Popover'
import { isIcon, PICKER_ICONS } from '../utils/icon'
import { isEnter } from '../utils/keyboard'

/**
 * Chooses a paper's icon: one of the offered emoji, or any emoji typed in
 * the field. Remove shows only while an icon is set. onPick gets the icon,
 * or '' to remove it.
 */
export function IconPicker({ anchor, value, onPick, onClose }: {
  anchor: Anchor
  /** The icon set now; '' when none is. */
  value: string
  onPick: (icon: string) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [typed, setTyped] = useState('')
  const bad = typed.trim() !== '' && !isIcon(typed)
  const submit = () => {
    if (typed.trim() === '' || bad) return
    onPick(typed.trim())
  }
  return (
    <Popover anchor={anchor} onClose={onClose} className="iconpick" label={t('iconLabel')}>
      <div className="ip-grid" role="group" aria-label={t('iconLabel')}>
        {PICKER_ICONS.map((x) => (
          <button key={x} type="button" className={x === value ? 'on' : ''} aria-pressed={x === value} onClick={() => onPick(x)}>{x}</button>
        ))}
      </div>
      <div className="ip-row">
        <input className={`ip-in${bad ? ' bad' : ''}`} value={typed} placeholder={t('iconInput')} aria-label={t('iconInput')} aria-invalid={bad}
          onChange={(e) => setTyped(e.target.value)}
          onKeyDown={(e) => { if (isEnter(e)) { e.preventDefault(); submit() } }} />
        <button type="button" className="btn sm" disabled={typed.trim() === '' || bad} onClick={submit}>{t('iconSet')}</button>
        {value && <button type="button" className="btn sm" onClick={() => onPick('')}>{t('iconRemove')}</button>}
      </div>
      {bad && <div className="ip-err" role="alert">{t('iconOne')}</div>}
    </Popover>
  )
}
