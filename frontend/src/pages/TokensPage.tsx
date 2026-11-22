import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { ApiToken } from '../api/types'
import { useTranslation } from '../i18n'

export function mcpConfig(secret: string, origin: string): string {
  return `claude mcp add --transport http papers ${origin}/mcp --header "Authorization: Bearer ${secret}"`
}

export function TokensPage() {
  const { t } = useTranslation()
  const [tokens, setTokens] = useState<ApiToken[]>([])
  const [name, setName] = useState('Claude')
  const [kind, setKind] = useState<ApiToken['kind']>('agent')
  const [scope, setScope] = useState<ApiToken['scope']>('write')
  const [days, setDays] = useState(90)
  const [secret, setSecret] = useState<string>()
  const [error, setError] = useState<string>()

  const reload = () => void api.listTokens().then(setTokens).catch((e: unknown) => setError(String(e)))
  useEffect(reload, [])

  const create = async () => {
    try {
      const res = await api.createToken({ name, kind, scope, ttl_hours: days * 24 })
      setSecret(res.secret)
      reload()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <div className="settings">
      <h1>{t('tokens')}</h1>
      <p>{t('tokensLead')}</p>

      <form className="token-form" onSubmit={(e) => { e.preventDefault(); void create() }}>
        <label>{t('tokenName')}<input className="field" value={name} onChange={(e) => setName(e.target.value)} /></label>
        <label>
          {t('subjectKind')}
          <select className="field" value={kind} onChange={(e) => setKind(e.target.value as ApiToken['kind'])}>
            <option value="agent">{t('tokenKindAgent')}</option>
            <option value="personal">{t('tokenKindPersonal')}</option>
          </select>
        </label>
        <label>
          {t('role')}
          <select className="field" value={scope} onChange={(e) => setScope(e.target.value as ApiToken['scope'])}>
            <option value="write">{t('scopeWrite')}</option>
            <option value="read">{t('scopeRead')}</option>
          </select>
        </label>
        <label>{t('expiresInDays')}<input className="field" type="number" min={0} value={days} onChange={(e) => setDays(Number(e.target.value))} /></label>
        <button type="submit" className="btn btn-primary">{t('createToken')}</button>
      </form>

      {secret && (
        <div className="secret">
          <strong>{t('tokenCreated')}</strong>
          <pre>{secret}</pre>
          <p>{t('mcpSetup')}</p>
          <pre>{mcpConfig(secret, window.location.origin)}</pre>
        </div>
      )}
      {error && <p className="login-error">{error}</p>}

      {tokens.map((tk) => (
        <div key={tk.id} className="token-row">
          <span>
            <strong>{tk.name}</strong> {tk.kind === 'agent' ? t('tokenKindAgent') : t('tokenKindPersonal')}, {tk.scope === 'write' ? t('scopeWrite') : t('scopeRead')}
            <br />
            <span className="help">{tk.last_used_at ? t('lastUsed', { when: new Date(tk.last_used_at).toLocaleString() }) : ''}</span>
          </span>
          <button type="button" className="btn btn-quiet btn-danger" onClick={() => void api.revokeToken(tk.id).then(reload)}>{t('revoke')}</button>
        </div>
      ))}
    </div>
  )
}
