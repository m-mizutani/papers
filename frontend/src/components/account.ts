import type { Me } from '../api/types'
import { t } from '../i18n'

/**
 * The first line of the account menu: who is signed in, or, in the server's
 * anonymous mode, what being a guest of this browser means.
 */
export function accountNote(me: Me): string {
  return me.anonymous ? t('anonymousMode') : me.user.email || me.user.name
}

/**
 * Whether to offer "Sign out". An anonymous user has no way back once its
 * session is gone, so signing out would lose it for good.
 */
export function canSignOut(me: Me): boolean {
  return !me.anonymous
}
