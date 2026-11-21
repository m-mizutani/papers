import { ApiError } from '../api/client'
import type { MsgKey } from '../i18n/keys'

type Translate = (key: MsgKey, params?: Record<string, string | number>) => string

/** The message for a failed request. */
export function errorMessage(e: unknown, t: Translate): string {
  if (e instanceof ApiError && e.code === 'busy') return t('busy')
  return t('error', { message: e instanceof Error ? e.message : String(e) })
}
