import { useSearchParams } from 'react-router'
import { useTranslation } from '../i18n'

export function LoginPage() {
  const { t } = useTranslation()
  const [params] = useSearchParams()
  const returnTo = params.get('return_to') ?? '/'
  return (
    <main className="login">
      <div className="login-sheet">
        <h1>{t('appName')}</h1>
        <p>{t('signInLead')}</p>
        {params.get('error') === 'not_allowed' && <p className="login-error">{t('signInNotAllowed')}</p>}
        <a className="btn btn-primary" href={`/api/auth/login?return_to=${encodeURIComponent(returnTo)}`}>{t('signIn')}</a>
      </div>
    </main>
  )
}
