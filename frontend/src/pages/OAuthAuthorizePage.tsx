import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router'
import { ApiError, api } from '../api/client'
import type { ApiToken, Me, OAuthRequest, OAuthRequestInfo } from '../api/types'
import { useTranslation, type MsgKey } from '../i18n'

/** The validity of a token issued through OAuth (usecase.OAuthTokenTTL). */
const OAUTH_TOKEN_DAYS = 90

/** The message for each OAuth error code the server reports for a request it will not redirect. */
const ERROR_MESSAGES: Record<string, MsgKey> = {
  invalid_client: 'oauthErrInvalidClient',
  invalid_redirect_uri: 'oauthErrRedirect',
  unsupported_response_type: 'oauthErrResponseType',
  invalid_code_challenge: 'oauthErrChallenge',
  invalid_target: 'oauthErrResource',
}

export function oauthErrorMessage(code: string): MsgKey {
  return ERROR_MESSAGES[code] ?? 'oauthErrOther'
}

/** Reads the authorization request from the query of /oauth/authorize. */
export function oauthRequestFrom(params: URLSearchParams): OAuthRequest {
  const get = (k: string) => params.get(k) ?? ''
  return {
    client_id: get('client_id'), redirect_uri: get('redirect_uri'), response_type: get('response_type'),
    code_challenge: get('code_challenge'), code_challenge_method: get('code_challenge_method'),
    state: get('state'), resource: get('resource'), scope: get('scope'),
  }
}

/**
 * The OAuth consent screen: an MCP client asks for a token, and the signed-in
 * person allows or denies it. A request whose redirect cannot be trusted is
 * shown as an error and never redirected.
 */
export function OAuthAuthorizePage({ me }: { me: Me }) {
  const { t } = useTranslation()
  const [params] = useSearchParams()
  const req = useMemo(() => oauthRequestFrom(params), [params])
  const [info, setInfo] = useState<OAuthRequestInfo>()
  const [error, setError] = useState<MsgKey>()
  // The person may lower the access the client asked for, never raise it.
  const [scope, setScope] = useState<ApiToken['scope']>('write')
  const [sending, setSending] = useState<'allow' | 'deny'>()

  useEffect(() => {
    let live = true
    api.checkAuthorize(req).then((r) => {
      if (!live) return
      setInfo(r)
      setScope(r.scope)
    }).catch((e: unknown) => {
      if (live) setError(oauthErrorMessage(e instanceof ApiError ? e.message : ''))
    })
    return () => { live = false }
  }, [req])

  const answer = (decision: 'allow' | 'deny') => {
    setSending(decision)
    api.decideAuthorize(req, scope, decision).then((r) => {
      window.location.assign(r.redirect_to)
    }).catch((e: unknown) => {
      setSending(undefined)
      setError(oauthErrorMessage(e instanceof ApiError ? e.message : ''))
    })
  }

  return (
    <main className="gate consent">
      <div className="consent-form">
        {error ? (
          <>
            <h1>{t('oauthErrorTitle')}</h1>
            <p className="gate-msg err" role="alert">{t(error)}</p>
            <a className="consent-back" href="/">{t('oauthBack')}</a>
          </>
        ) : info && (
          <>
            <h1>{t('oauthTitle', { client: info.client_name })}</h1>
            <dl className="kv">
              <dt>{t('oauthAccount')}</dt><dd>{me.user.email ? t('oauthAccountValue', { name: me.user.name, email: me.user.email }) : me.user.name}</dd>
              <dt>{t('oauthReturnsTo')}</dt><dd>{info.redirect_host}</dd>
              <dt>{t('tokenExpiry')}</dt><dd>{t('days', { n: OAUTH_TOKEN_DAYS })}</dd>
            </dl>
            {!sending && (
              <>
                <div role="radiogroup" aria-label={t('tokenScope')}>
                  <div className="consent-label">{t('tokenScope')}</div>
                  <label className="chk"><input type="radio" name="oauthscope" checked={scope === 'write'} disabled={info.scope === 'read'} onChange={() => setScope('write')} />{t('readWrite')}</label>
                  <label className="chk"><input type="radio" name="oauthscope" checked={scope === 'read'} onChange={() => setScope('read')} />{t('readOnly')}</label>
                </div>
                <p className="gate-msg info">{t(scope === 'read' ? 'oauthInfoRead' : 'oauthInfoWrite')}</p>
              </>
            )}
            <div className="consent-btns">
              <button type="button" className="btn" disabled={!!sending} onClick={() => answer('deny')}>{t('oauthDeny')}</button>
              <button type="button" className="btn primary" disabled={!!sending} onClick={() => answer('allow')}>{sending === 'allow' ? t('oauthAllowing') : t('oauthAllow')}</button>
            </div>
          </>
        )}
      </div>
    </main>
  )
}
