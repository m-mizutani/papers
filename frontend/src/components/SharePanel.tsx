import { useState } from 'react'
import { api } from '../api/client'
import type { Paper, RoleBinding } from '../api/types'
import { useTranslation } from '../i18n'
import { BindingsEditor } from './BindingsEditor'

export function SharePanel({ ws, paper, onClose, onSaved }: { ws: string; paper: Paper; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const [restricted, setRestricted] = useState(paper.access.restricted)
  const [bindings, setBindings] = useState<RoleBinding[]>(paper.access.bindings ?? [])
  const [error, setError] = useState<string>()

  const save = async () => {
    try {
      await api.updateAccess(ws, paper.id, { restricted, bindings })
      onSaved()
      onClose()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <aside className="panel" aria-label={t('share')}>
      <div className="panel-head">
        <h2>{t('share')}</h2>
        <button type="button" className="btn btn-quiet" onClick={onClose}>{t('close')}</button>
      </div>
      <label className="toggle">
        <input type="checkbox" checked={restricted} onChange={(e) => setRestricted(e.target.checked)} />
        <span>
          {t('restricted')}
          <br />
          <span className="help">{t('restrictedHelp')}</span>
        </span>
      </label>
      <h2 className="help">{t('people')}</h2>
      <BindingsEditor value={bindings} onChange={setBindings} roles={['viewer', 'editor', 'owner']} />
      {error && <p className="login-error">{error}</p>}
      <p><button type="button" className="btn btn-primary" onClick={() => void save()}>{t('save')}</button></p>
    </aside>
  )
}
