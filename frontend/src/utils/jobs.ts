import { ApiError, pending, waitJobs } from '../api/client'
import type { BatchResult, Job } from '../api/types'
import type { MsgKey } from '../i18n/keys'

type Translate = (key: MsgKey, params?: Record<string, string | number>) => string

/** Waiting this long on a job says that it goes on without the page. */
const slowMs = 10_000

/**
 * Follows the background jobs of a result until they finish, showing their
 * progress as notices. A failed job throws like a failed request.
 */
export async function followJobs(ws: string, res: BatchResult, notice: (msg: string) => void, t: Translate): Promise<Job[]> {
  if (!pending(res)) return res.jobs ?? []
  const started = Date.now()
  return waitJobs(ws, res, (done, total) => {
    const msg = total > 0 ? t('jobRunning', { done, total: Math.max(total, done) }) : t('jobStarting')
    notice(Date.now() - started > slowMs ? `${msg} ${t('jobContinues')}` : msg)
  })
}

/** The message for a failed request or job. */
export function errorMessage(e: unknown, t: Translate): string {
  if (e instanceof ApiError && e.code === 'busy') return t('busy')
  return t('error', { message: e instanceof Error ? e.message : String(e) })
}
