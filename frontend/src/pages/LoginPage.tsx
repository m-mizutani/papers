import { useState } from 'react'
import { useSearchParams } from 'react-router'
import { useTranslation } from '../i18n'
import { setLang } from '../utils/prefs'

const GOOGLE_G = (
  <svg width="18" height="18" viewBox="0 0 48 48" aria-hidden>
    <path fill="var(--g-y)" d="M43.6 20.5H42V20H24v8h11.3C33.7 32.7 29.2 36 24 36c-6.6 0-12-5.4-12-12s5.4-12 12-12c3 0 5.8 1.1 7.9 3l5.7-5.7C34 6 29.3 4 24 4 12.9 4 4 12.9 4 24s8.9 20 20 20 20-8.9 20-20c0-1.3-.1-2.4-.4-3.5z" />
    <path fill="var(--g-r)" d="M6.3 14.7l6.6 4.8C14.7 15.1 19 12 24 12c3 0 5.8 1.1 7.9 3l5.7-5.7C34 6 29.3 4 24 4 16.3 4 9.7 8.3 6.3 14.7z" />
    <path fill="var(--g-g)" d="M24 44c5.2 0 9.9-2 13.4-5.2l-6.2-5.2C29.2 35.1 26.7 36 24 36c-5.2 0-9.6-3.3-11.3-8l-6.5 5C9.5 39.6 16.2 44 24 44z" />
    <path fill="var(--g-b)" d="M43.6 20.5H42V20H24v8h11.3c-.8 2.2-2.2 4.2-4.1 5.6l6.2 5.2C37 39.2 44 34 44 24c0-1.3-.1-2.4-.4-3.5z" />
  </svg>
)

/** Sign-in: one button, and a small live sample of people and AI writing together (spec: R21). */
export function LoginPage() {
  const { t, lang } = useTranslation()
  const [params] = useSearchParams()
  const [going, setGoing] = useState(false)
  const returnTo = params.get('return_to') ?? '/'
  const error = params.get('error')
  const href = `/api/auth/login?return_to=${encodeURIComponent(returnTo)}`
  return (
    <main className="gate login">
      <section className="gate-brand">
        <div className="sample" aria-hidden>
          <div className="s-title">{lang === 'ja' ? 'DB フェイルオーバー手順' : 'DB failover runbook'}</div>
          <div className="s-line w90" /><div className="s-line w70" />
          <div className="s-row"><div className="s-line w60 typing p" /><span className="s-tag p">Aiko</span></div>
          <div className="s-line w80" />
          <div className="s-row"><div className="s-line w50 typing ai" /><span className="s-tag ai">✦ Claude</span></div>
          <div className="s-ver"><span className="vdot" />v3</div>
        </div>
      </section>
      <section className="gate-panel">
        <div className="gate-form">
          <h1>{t('signInTitle')}</h1>
          {error === 'not_allowed' && <div className="gate-msg err" role="alert"><strong>{t('notAllowedTitle')}</strong><span>{t('notAllowedBody')}</span></div>}
          {error === 'expired' && <div className="gate-msg info" role="status">{t('expired')}</div>}
          <a className={`gbtn${going ? ' going' : ''}`} href={href} onClick={() => setGoing(true)}>
            {going ? <span className="spin" aria-hidden /> : GOOGLE_G}
            <span>{going ? t('goingGoogle') : t('signIn')}</span>
          </a>
          {error === 'not_allowed' && <a className="linkbtn" href={href}>{t('tryOther')}</a>}
        </div>
        <div className="gate-lang" role="group" aria-label={t('language')}>
          <button type="button" className={lang === 'ja' ? 'on' : ''} aria-pressed={lang === 'ja'} onClick={() => setLang('ja')}>日本語</button>
          <button type="button" className={lang === 'en' ? 'on' : ''} aria-pressed={lang === 'en'} onClick={() => setLang('en')}>English</button>
        </div>
      </section>
    </main>
  )
}
