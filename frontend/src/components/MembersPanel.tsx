import { useState } from 'react'
import { api } from '../api/client'
import type { RoleBinding, Workspace } from '../api/types'
import { useTranslation } from '../i18n'
import { BindingsEditor } from './BindingsEditor'

export function MembersPanel({ workspace, onClose, onSaved }: { workspace: Workspace; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const [bindings, setBindings] = useState<RoleBinding[]>(workspace.bindings)
  const [error, setError] = useState<string>()

  const save = async () => {
    try {
      await api.updateWorkspace(workspace.id, { bindings })
      onSaved()
      onClose()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <aside className="panel" aria-label={t('members')}>
      <div className="panel-head">
        <h2>{t('members')}</h2>
        <button type="button" className="btn btn-quiet" onClick={onClose}>{t('close')}</button>
      </div>
      <BindingsEditor value={bindings} onChange={setBindings} roles={['viewer', 'editor', 'owner']} />
      {error && <p className="login-error">{error}</p>}
      <p><button type="button" className="btn btn-primary" onClick={() => void save()}>{t('save')}</button></p>
    </aside>
  )
}
