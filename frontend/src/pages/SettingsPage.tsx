import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { ApiToken, Me } from '../api/types'
import { ago, useTranslation, type Lang, type MsgKey } from '../i18n'
import { Modal } from '../ui/Modal'
import { useToast } from '../ui/Toast'
import { MOD_LABEL } from '../utils/keyboard'
import { setLang, setTheme, storedTheme, type Theme } from '../utils/prefs'
import { NO_AUTOFILL } from '../utils/autofill'

/** The Claude Code command that adds papers; Claude Code signs in through OAuth, so no token goes in it. */
export function mcpConfig(mcpBaseURL: string): string {
  return `claude mcp add --transport http papers ${mcpBaseURL.replace(/\/+$/, '')}/mcp`
}

/** When a token was issued, as a date and a time in the browser's time zone; '' when unknown. */
export function formatIssued(iso: string | undefined, lang: Lang, timeZone?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return new Intl.DateTimeFormat(lang, { dateStyle: 'medium', timeStyle: 'short', timeZone }).format(d)
}

/** The second line of a token row: kind, access, OAuth, issue time and last use. */
export function tokenSummary(tk: ApiToken, t: (key: MsgKey, params?: Record<string, string | number>) => string, lang: Lang, timeZone?: string, now?: number): string {
  const issued = formatIssued(tk.created_at, lang, timeZone)
  return [
    tk.kind === 'agent' ? t('tokenAgent') : t('tokenPersonal'),
    tk.scope === 'read' ? t('readOnly') : t('readWrite'),
    tk.oauth ? t('tokenOAuth') : '',
    issued ? t('issuedAt', { when: issued }) : '',
    tk.last_used_at ? t('lastUsed', { when: ago(tk.last_used_at, now) }) : t('neverUsed'),
  ].filter(Boolean).join(' · ')
}

export function ShortcutList() {
  const { t } = useTranslation()
  const rows: [string, string][] = [
    [`${MOD_LABEL} K`, t('keySearch')], [`${MOD_LABEL} S`, t('keySave')], [`${MOD_LABEL} ⇧ H`, t('keyHistory')], [`${MOD_LABEL} \\`, t('keySidebar')],
    [`${MOD_LABEL} Z / ${MOD_LABEL} ⇧ Z`, t('keyUndo')], ['/', t('keySlash')], ['#  -  1.  []  >  ```', t('keyMarkdown')], [`${MOD_LABEL} B / ${MOD_LABEL} I`, t('keyBold')],
    ['Tab / ⇧Tab', t('keyIndent')], ['Esc', t('keyEscape')], ['⇧↑ / ⇧↓', t('keyExtendSelection')], [`${MOD_LABEL === '⌘' ? '⌥' : 'Alt '}↑ / ${MOD_LABEL === '⌘' ? '⌥' : 'Alt '}↓`, t('keyMoveBlocks')], ['?', t('keyHelp')],
  ]
  return (
    <div className="list">
      {rows.map(([k, l]) => <div key={k} className="lr" style={{ padding: 'var(--sp-4) var(--sp-6)' }}><span className="lm">{l}</span><span className="kbd" style={{ fontSize: 'var(--text-sm)', padding: 'var(--sp-1) var(--sp-3)' }}>{k}</span></div>)}
    </div>
  )
}

function NewToken({ onClose, onCreated }: { onClose: () => void; onCreated: (secret: string) => void }) {
  const { t } = useTranslation()
  const [name, setName] = useState('Claude')
  const [kind, setKind] = useState<ApiToken['kind']>('agent')
  const [scope, setScope] = useState<ApiToken['scope']>('write')
  const [days, setDays] = useState(90)
  const [error, setError] = useState('')
  return (
    <Modal title={t('createToken')} onClose={onClose} actions={[{ label: t('cancel') }, {
      label: t('createToken'), kind: 'primary', disabled: !name.trim(), onClick: async () => {
        try {
          const res = await api.createToken({ name: name.trim(), kind, scope, ttl_hours: days * 24 })
          onCreated(res.secret)
          return true
        } catch (e) {
          setError(e instanceof Error ? e.message : String(e))
          return false
        }
      },
    }]}>
      <div className="field"><label htmlFor="tkname">{t('tokenName')}</label><input {...NO_AUTOFILL} id="tkname" className="inp" value={name} onChange={(e) => setName(e.target.value)} /></div>
      <div className="field">
        <span className="flabel">{t('tokenKind')}</span>
        {([['agent', t('tokenAgent'), t('tokenAgentDesc')], ['personal', t('tokenPersonal'), t('tokenPersonalDesc')]] as const).map(([k, l, d]) => (
          <label key={k} className="chk"><input type="radio" name="tkkind" checked={kind === k} onChange={() => setKind(k)} /><span>{l}<span className="muted" style={{ display: 'block', fontSize: 'var(--text-sm)' }}>{d}</span></span></label>
        ))}
      </div>
      <div className="field">
        <span className="flabel">{t('tokenScope')}</span>
        <label className="chk"><input type="radio" name="tkscope" checked={scope === 'write'} onChange={() => setScope('write')} />{t('readWrite')}</label>
        <label className="chk"><input type="radio" name="tkscope" checked={scope === 'read'} onChange={() => setScope('read')} />{t('readOnly')}</label>
      </div>
      <div className="field"><label htmlFor="tkdays">{t('tokenExpiry')}</label>
        <select id="tkdays" className="inp" value={days} onChange={(e) => setDays(Number(e.target.value))}>
          {[7, 30, 90, 365].map((n) => <option key={n} value={n}>{t('days', { n })}</option>)}
        </select>
      </div>
      {error && <p style={{ color: 'var(--danger)' }}>{error}</p>}
    </Modal>
  )
}

/** API tokens, MCP setup, theme, language and the keyboard shortcuts (spec: R15). */
export function SettingsPage({ me, layoutTop }: { me: Me; layoutTop: React.ReactNode }) {
  const { t, lang } = useTranslation()
  const toast = useToast()
  const [tab, setTab] = useState<'tokens' | 'appearance'>('tokens')
  const [tokens, setTokens] = useState<ApiToken[]>([])
  const [creating, setCreating] = useState(false)
  const [secret, setSecret] = useState<string>()
  const [theme, setThemeState] = useState<Theme>(storedTheme)
  const reload = () => void api.listTokens().then(setTokens).catch(() => setTokens([]))
  useEffect(reload, [])
  const command = mcpConfig(me.mcp_base_url || window.location.origin)
  const copy = (s: string) => void navigator.clipboard?.writeText(s).then(() => toast(t('copied')), () => undefined)
  return (
    <section className="main">
      {layoutTop}
      <div className="scroll"><div className="col wide">
        <h1 className="page-h">{t('settings')}</h1>
        <div className="tabs" role="tablist">
          <button type="button" role="tab" aria-selected={tab === 'tokens'} className={tab === 'tokens' ? 'on' : ''} onClick={() => setTab('tokens')}>{t('tokens')}</button>
          <button type="button" role="tab" aria-selected={tab === 'appearance'} className={tab === 'appearance' ? 'on' : ''} onClick={() => setTab('appearance')}>{t('appearance')}</button>
        </div>
        {tab === 'tokens' && (
          <>
            <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 'var(--sp-7)' }}><button type="button" className="btn primary" onClick={() => setCreating(true)}>＋ {t('createToken')}</button></div>
            <div className="list">
              {tokens.length === 0 && <div className="empty">{t('noTokens')}</div>}
              {tokens.map((tk) => (
                <div key={tk.id} className="lr">
                  <span className={`face solo${tk.kind === 'agent' ? ' ai' : ''}`} style={{ background: tk.kind === 'agent' ? 'var(--ai)' : 'var(--accent)' }}>{tk.kind === 'agent' ? '✦' : '⌘'}</span>
                  <div className="lm">
                    <div className="lt">{tk.name}</div>
                    <div className="ls">{tokenSummary(tk, t, lang)}</div>
                  </div>
                  <button type="button" className="btn sm danger" onClick={() => void api.revokeToken(tk.id).then(reload)}>{t('revoke')}</button>
                </div>
              ))}
            </div>
            <h3 style={{ margin: 'var(--sp-9) 0 var(--sp-3)', fontSize: 'var(--text-h3)' }}>{t('mcpSetup')}</h3>
            <pre className="out">{command}</pre>
            <p className="muted" style={{ margin: 'var(--sp-4) 0', fontSize: 'var(--text-sm)' }}>{t('mcpHint')}</p>
            <button type="button" className="btn sm" onClick={() => copy(command)}>{t('copyCommand')}</button>
          </>
        )}
        {tab === 'appearance' && (
          <>
            <div className="field">
              <span className="flabel">{t('theme')}</span>
              <div className="seg" role="group" aria-label={t('theme')}>
                {([['system', t('themeSystem')], ['light', t('themeLight')], ['dark', t('themeDark')]] as const).map(([v, l]) => (
                  <button key={v} type="button" className={theme === v ? 'on' : ''} aria-pressed={theme === v} onClick={() => { setTheme(v); setThemeState(v) }}>{l}</button>
                ))}
              </div>
            </div>
            <div className="field">
              <span className="flabel">{t('language')}</span>
              <div className="seg" role="group" aria-label={t('language')}>
                <button type="button" className={lang === 'ja' ? 'on' : ''} aria-pressed={lang === 'ja'} onClick={() => setLang('ja')}>日本語</button>
                <button type="button" className={lang === 'en' ? 'on' : ''} aria-pressed={lang === 'en'} onClick={() => setLang('en')}>English</button>
              </div>
            </div>
            <div className="field"><span className="flabel">{t('shortcuts')}</span><ShortcutList /></div>
          </>
        )}
      </div></div>
      {creating && <NewToken onClose={() => setCreating(false)} onCreated={(s) => { setSecret(s); reload() }} />}
      {secret && (
        <Modal title={t('createToken')} onClose={() => setSecret(undefined)} actions={[{ label: t('done') }, { label: t('copy'), kind: 'primary', onClick: () => { copy(secret); return false } }]}>
          <p className="banner warn" style={{ margin: '0 0 var(--sp-5)' }}>{t('tokenOnce')}</p>
          <pre className="out">{secret}</pre>
        </Modal>
      )}
    </section>
  )
}
